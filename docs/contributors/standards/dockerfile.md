> **ABOUTME:** Dockerfile conventions for yoloAI's base image and the user-authored profile
> Dockerfiles built on top of it — base-distro choice, apt/layer/pinning discipline, and the
> runtime contract every profile inherits via `FROM yoloai-base`. Covers only the base image;
> profile Dockerfiles are user-owned and asked, not forced, to follow it.

# Dockerfile Standard

Reference for yoloAI's Dockerfiles: the base image at `runtime/docker/resources/Dockerfile` and user-supplied profile Dockerfiles at `~/.yoloai/profiles/<name>/Dockerfile`.

See also: `../principles/general-principles.md §2` (boring tech — Debian + apt, not Alpine + apk); `../principles/security-principles.md §4` (least privilege — non-root runtime user); `../principles/development-principles.md §6` (warnings are signal — hadolint findings get justified suppressions or fixes); `MAKEFILE.md §The make check contract` (hadolint runs in `make check`).

## Two contexts, different audiences

| Context                                            | Authored by         | Constraints                                                                                    |
| -------------------------------------------------- | ------------------- | --------------------------------------------------------------------------------------------- |
| **Base image** (`runtime/docker/resources/Dockerfile`) | yoloAI itself       | Hadolint clean. Pinned versions where it matters. Documents the project's runtime contract. |
| **Profile Dockerfile** (`~/.yoloai/profiles/<name>/Dockerfile`) | The user           | Starts with `FROM yoloai-base`. Adds project-specific tools. User-owned.                       |

This standard covers the base image. Profile Dockerfiles are user-owned; yoloAI documents the `yoloai-base` contract in `docs/contributors/design/config.md` and trusts users to apt-install what they need on top.

### Label custom images with `com.yoloai.managed`

The base image carries `LABEL com.yoloai.managed="true"`, marking it yoloAI-authored so
`yoloai system prune` can tell yoloAI's own build artifacts from unrelated images on a shared
daemon. A profile Dockerfile that begins `FROM yoloai-base` **inherits this label automatically** —
you need do nothing.

If you build an image from an **unrelated base** — e.g. a devcontainer `build.dockerfile` that
does not `FROM yoloai-base` — add the label yourself so yoloAI still recognises it as yours:

```dockerfile
LABEL com.yoloai.managed="true"
```

This is forward-looking today: `yoloai system prune --images` currently reclaims *every* unused
image on the daemon. It is scheduled to become label-scoped after a settling period (registered in
`../deprecations.md`), after which an **unlabeled** custom image will no longer be reclaimed by
`--images` — so labeling it now is what keeps it reclaimable, and keeps an unrelated image of yours
safe from a scoped sweep.

## Base image conventions

### Base distro: `debian:trixie-slim`

Debian trixie-slim is the base. Reasons:

- **Boring** (`../principles/general-principles.md §2`). Wide Debian familiarity in the developer community; apt is well-documented; package availability is broad.
- **Slim variant** drops docs, locale data, and other bulk that the sandbox doesn't need.
- **Rejected alternatives**: Alpine (musl libc → glibc-specific tools fail; the Bun-bundled Claude Code installer had documented issues per `docs/contributors/design/questions-unresolved.md` #2), Ubuntu (no advantage over Debian; larger), distroless (can't apt install at runtime, doesn't compose with profile system).

### Shell: bash with pipefail

```dockerfile
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
```

This is non-default. Docker's default `/bin/sh -c` doesn't honour pipefail; without it, `cmd1 | cmd2` exits zero whenever `cmd2` succeeds even if `cmd1` failed. Setting `bash -o pipefail` early is the recommended hadolint-aligned pattern (DL4006).

### apt-install pattern

Every apt install follows the pattern:

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg1 \
    pkg2 \
    pkg3 \
    && rm -rf /var/lib/apt/lists/*
```

- `apt-get update` and `apt-get install` in a single `RUN` — separating them risks stale package lists in cached layers (hadolint DL3009).
- `--no-install-recommends` — keeps the image small; recommended packages get pulled by surprise otherwise.
- `rm -rf /var/lib/apt/lists/*` at the end — drops apt cache from the layer.
- Packages listed one-per-line, sorted-by-purpose-then-alphabetically when the purpose is clear, alphabetically otherwise.

### Pinning

Pin versions when the package's behaviour can change in ways that break us. Don't pin when the package's behaviour is stable across versions (apt-installed dev tools, system libraries).

- `golang-go` — *not* pinned in apt; Go is installed separately by tag (see "Go install" pattern below).
- `nodejs` — pinned via NodeSource repository to Node 22 LTS (rationale: `docs/contributors/design/questions-unresolved.md` #2).
- Downloaded binaries (gosu, ko, etc.) — pinned by version + checksum where possible.
- apt packages without a moving-target risk — left unpinned (hadolint DL3008 is suppressed for those `RUN` lines with `# hadolint ignore=DL3008` and a comment explaining the unpinned choice).

### Hadolint compliance

The base Dockerfile is hadolint clean (`make hadolint` in CI). Suppressions follow the same justification rule as Go lint suppressions (per `../principles/development-principles.md §6`):

```dockerfile
# hadolint ignore=DL3008
# We don't pin Debian apt packages because Debian stable rarely changes
# package behaviour and pinning every package would force constant updates.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ...
```

The `# hadolint ignore=...` directive immediately precedes the `RUN` line. The explanatory comment is above the directive.

### Non-root runtime user

The container runs as user `yoloai` matching the host UID/GID, not root. This is the least-privilege application (`../principles/security-principles.md §4`):

- Claude Code refuses to run as root for `--dangerously-skip-permissions`.
- `yoloai` user has passwordless `sudo` (commit `83ac029`, 2026-03-12) for cases where a recipe needs elevated commands — opt-in, not default.
- The actual UID is set at container creation time so bind-mounted files have correct ownership.

### Layer ordering

Layers that change frequently go last; layers that rarely change go first:

1. Base distro + system packages (rare changes).
2. Language runtimes (Node, Python — change with version bumps).
3. Tool downloads (gosu, dev tools).
4. Project-specific configuration (user/group, sudo).
5. Entrypoint scripts (change most often during development).

Caching benefit: a change to the entrypoint script doesn't invalidate the apt-install layer.

## What goes in the base image

The base image carries everything yoloAI assumes is present in a sandbox:

- **tmux** — session management; every backend uses it.
- **git** — required for `:copy` mode's git-based diff/apply, which runs git *inside* the container.
- **gosu** — the entrypoint drops root to `yoloai` with it; the name is hardcoded, with no fallback.
- **python3** — every entrypoint and the status monitor are Python (stdlib only).
- **iptables + ipset** — required for `--network-isolated`. `ipset` is a soft dependency: `firewall.py` falls back to per-IP iptables rules without it.
- **sudo** — for the `yoloai` user passwordless escalation.
- **Standard dev tooling** (build-essential, cmake, clang, curl, jq, ripgrep, fd-find, etc.) — broad coverage so most agents work out-of-box.
- **Node.js 20 LTS** — for Claude Code, Codex, Gemini CLI installation. (Node 22 has syscall incompatibilities with gVisor ARM64 — see the `NODE_MAJOR` ARG in the Dockerfile.)
- **Docker CE + Compose plugin** — for Docker-in-Docker (D22 `--isolation container-privileged`).
- **The agent CLIs** — see below.

What does NOT go in the base image:

- **API keys** — injected at runtime via `/run/secrets/` (`../principles/security-principles.md §6`).
- **User-specific configs** — handled via `agent_files` seeding mechanism.
- **Anything per-project** — that's what profile Dockerfiles are for.

**`dnsutils` is present but unused.** This section used to justify it as "`dig` for domain
resolution in the network-isolation entrypoint". Nothing calls `dig`: `firewall.py` resolves
allowlisted domains with Python's `socket.getaddrinfo`. It costs ~8 MB and is a candidate for
removal — left in for now because an agent may use it interactively, which is a judgment call
about the sandbox's dev-tool surface rather than a yoloAI requirement.

**Agent CLIs are baked into the image, not installed at sandbox creation.** An earlier version
of this section claimed the opposite. The install is the `npm install -g` layer in the base
Dockerfile; at create time `sandbox-setup.py` only *checks* for the resolved agent's binary
(`shutil.which`) and aborts the launch with "run `yoloai system build`" when it is missing.
This matters because it means the image must be rebuilt to change which agents are available —
`agent.Definition` carries no install command to do it any other way.

## Profile Dockerfiles (user-supplied)

Profile Dockerfiles live at `~/.yoloai/profiles/<name>/Dockerfile`. They must begin with:

```dockerfile
FROM yoloai-base
```

Beyond that, the user has full Dockerfile expressiveness. The base image's user and entrypoint are inherited; profile Dockerfiles typically add language-specific tooling (Go toolchain, Rust toolchain, project-specific lint tools, etc.).

Profile Dockerfiles are NOT hadolint-checked by yoloAI's CI — they're user-authored. The hadolint discipline is documented as a recommendation in `docs/contributors/design/config.md`.

## Embedded resources: baked for container backends, staged for the others

The entrypoint files (`entrypoint.sh`, `entrypoint.py`, the setup and status-monitor scripts)
are `//go:embed`ed into the binary (`runtime/docker/resources.go`). How they reach the guest
differs by backend, and the distinction is easy to get wrong:

| Backend | Delivery | Consequence |
| --- | --- | --- |
| docker, podman, apple, containerd | **Baked** via `COPY` into `/yoloai/bin` at image build | Changing a script **requires a base rebuild** |
| seatbelt, tart | **Staged** host-side into the sandbox dir (`config.BinDirName`) | No image concept; picked up per sandbox |

For the container backends this is precisely why `buildInputsChecksum` (`runtime/docker/build.go`)
hashes all twelve embedded files and stamps the digest onto the image as the
`yoloai.base.checksum` label: it is what makes a script edit invalidate the base image.

An earlier version of this section claimed the scripts were bind-mounted at run time for every
backend, and therefore that the base never needed rebuilding when they changed. That is true
only for seatbelt and tart. If it were true for the container backends, `buildInputsChecksum`
would have no reason to exist.

The Dockerfile still only needs to provide the *environment* the entrypoints run in — it just
also carries the entrypoints themselves.

## ENTRYPOINT vs CMD

The base image does not set a final `ENTRYPOINT` / `CMD` — yoloAI specifies them at `docker run` time. The container starts with `entrypoint.sh` (the trampoline, per `SHELL.md`) which exec's into `entrypoint.py`.

Rationale: the entrypoint is a runtime contract, not a build-time one. Different backends (Tart, Seatbelt) don't even use a Docker entrypoint; codifying one in the base image would create a false consistency.

## Cross-references

- `../principles/general-principles.md §2` — boring tech (Debian + apt over Alpine + apk).
- `../principles/security-principles.md §4` — least privilege (non-root user, capability discipline).
- `../principles/security-principles.md §6` — credentials never in env vars (the Dockerfile must not bake them in).
- `../principles/development-principles.md §6` — warnings are signal (hadolint findings get justified suppressions).
- `MAKEFILE.md` — `make hadolint` is part of `make check`.
- `SHELL.md` — the entrypoint trampoline pattern (`#!/bin/sh`, minimal).
- `docs/contributors/design/config.md` — profile system design (how user profiles compose with the base image).
