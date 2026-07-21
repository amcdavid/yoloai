> **ABOUTME:** Plan for decoupling the sandbox runtime contract from the batteries-included
> `yoloai-base` image, so users can bring their own base stack and have yoloAI layer its
> runtime onto it. The agent set becomes the one build-time variable in yoloAI's own layer;
> everything else about the stack is the user's choice.

# Pluggable base images

- **Status:** IN-PROGRESS — Phase 0 built (internal/imagecontract, the composed
  runtime layer proving the split, the tart contract-check dedup, and `yoloai
  system verify-image` on the OCI backends). Phase 2 (`base:`/`agents:`) built on
  `feat/pluggable-base` and unit-tested; `yoloai-minimal` folded into it as a
  named base rather than a separate phase (see below). **Live container-build
  validation still pending** (the host has no docker/hadolint/golangci-lint, and
  the apple builder has the terminal-COPY bug) — the assembly is covered by unit
  tests, not yet an end-to-end build. Phase 3 (authoring skill) not started.
- **Depends on:** —

## Phase 2 — as built (2026-07-21)

Landed on `feat/pluggable-base` (off Phase 0's `feat/image-contract`):

- **`InstallCmd`/`InstallViaNPM` on `agent.Definition`** (`internal/agent/agent.go`),
  populated for every real agent; `agent.InstallDockerfile(names)`
  (`internal/agent/install.go`) renders the install fragment, emitting the Node
  prerequisite once for the npm agents and one shared retry loop.
- **`base:`/`agents:` config keys** — parsed in `internal/config/profile.go`
  (handlers + `MergedConfig` + `ResolvedProfileConfig` + `profile info` render +
  scaffold), with `base:` last-non-empty-wins and `agents:` replacement-wins.
- **`config.ParseBaseRef` / `BaseRef` / `CustomBaseBuild` / `ResolveCustomBaseBuild`**
  — the four base forms as data, and the merged-config → build-spec resolution
  (agents default to the resolved agent).
- **Assembly** in `runtime/docker` — `AssembleCustomBaseDockerfile` +
  `assemble.go`'s `resolveStackHalf` (yoloai-minimal / image: / dockerfile:),
  `resources/minimal.Dockerfile` embedded, the runtime-layer COPY files injected
  into the custom-base build context, and `profileBuildChecksum` extended to hash
  the assembled Dockerfile.
- **Wiring** — the `ProfileImageBuilder` interface and `EnsureProfileImage` now
  thread the `*CustomBaseBuild` spec and build a `base:`-only profile even without
  a Dockerfile; `ResolveProfileImage` gives such a profile its own image tag.
- **Validation** — `config.ValidateProfileBase` (custom base needs an
  image-building backend; malformed base fails loudly) wired into the create path;
  a FROM in a base:-mode profile Dockerfile is rejected at assembly.
- **Docs** — `config.md` documents both keys, the four base forms, the FROM-less
  rule, and the staleness coverage.

**Known gap (not filed in the maintainer's findings register — fork work):** for
`base: image:<ref>`, a moving *remote* tag is not re-resolved until `--rebuild`;
the checksum tracks the ref string and the assembled bytes, not the pulled digest.
Documented in `profileBuildChecksum`'s comment and `config.md`.

## Decisions (2026-07-21)

Two questions settled the shape of the remaining work:

- **`base:` is profile-scoped, not a system-wide swap.** It operates at the
  *profile-image* layer, reusing the existing `BuildProfileImage` machinery, and
  leaves `yoloai-base` and the ~25 sites that hardcode its name untouched. A
  profile with a non-default `base:` builds its own standalone image (base +
  stack + agent installs + runtime layer); the default `yoloai new` and every
  existing profile are unchanged. A *global* default-base setting was considered
  and deferred — profiles are the customization surface, and per-profile covers
  "bring your own base". This is what collapses the earlier "Phase 1 then Phase
  2" into one lift: `yoloai-minimal` is not a separate system base to swap in,
  it is just a named base a profile can point at.

- **`agents:` defaults to a single agent — and this is NOT a breaking change.**
  Because `base:` is profile-scoped, `yoloai-base` still bakes all five agents,
  so the single-agent default only governs images built on a *custom* base — a
  brand-new feature with no existing users. Nothing that works today changes. A
  `--agent X` against a custom-base image that did not bake X is a rebuild
  prompt, not a regression of prior behaviour.

## Problem

`yoloai-base` (`runtime/docker/resources/Dockerfile`) is one image carrying every stack:
Debian trixie + build-essential + cmake + clang + Go + Rust + Node + Python + Docker CE +
golangci-lint + five agent CLIs + aider-on-uv + the VS Code CLI. It is the only supported
base for container backends, because profile Dockerfiles are required to `FROM yoloai-base`.

This is a one-size-fits-all image that fits no one. The specific mismatch varies by user — a
Go developer carries R nothing, an R developer carries the Go toolchain and golangci-lint, a
Codex user carries four unused agent CLIs, an aider user carries the other four — but the
shape is always the same: **most of the image is stack the user did not ask for, and the stack
they did want must be layered on top of all of it.** There is no combination of defaults that
fixes this, because the preferences genuinely differ. The fix is to stop choosing on the
user's behalf.

Two costs:

1. **Bloat.** ~5 GB, multi-minute first-run build, mostly stacks any given user never invokes.
2. **No escape hatch.** A team with a working domain image (bioinformatics, ML, embedded)
   cannot use it. Their only option is to re-install their whole stack *on top of* everything
   above.

The root cause is that yoloAI's runtime contract — what a sandbox must provide for yoloAI to
drive it — is not written down anywhere machine-checkable. It is implicit in a 284-line
Dockerfile that also happens to install Rust.

## Measured: where the weight actually is

Real disk deltas, `debian:trixie-slim` arm64, measured 2026-07-21. These numbers drive every
design decision below, so they are recorded rather than estimated.

| Group | Size |
| --- | --- |
| `debian:trixie-slim` baseline | 125 MB |
| Tier-1 runtime deps (`tmux git python3 sudo ca-certificates passwd`) | 149 MB |
| `iptables` + `ipset` (network isolation) | **10 MB** |
| `uidmap slirp4netns fuse-overlayfs iproute2` (rootless) | **11 MB** |
| Docker CE stack (DinD) | **352 MB** |
| `nodejs` 20 | 181 MB |
| `@anthropic-ai/claude-code` | 307 MB |
| gemini-cli + codex + opencode | **1030 MB** |
| uv + aider (bundles its own Python 3.12) | **710 MB** |
| build-essential + cmake + clang | 609 MB |
| golangci-lint (see §Incidental — mostly build cache) | 706 MB |
| Rust (minimal profile) | 538 MB |
| Go toolchain | 264 MB |
| VS Code CLI | 26 MB |
| `dnsutils` | 8 MB |

**The closure of every opt-in feature is 373 MB — about 7% of the image.** The agent CLIs and
language toolchains are ~3.9 GB, and for any given user most of that is unused: the agents are
mutually exclusive in practice (one is selected per sandbox), and the toolchains serve
whichever language that user does not work in.

So feature gating is not where the win is, and buying it would cost a config surface, a
contract tier, and a class of runtime failure. **Features stay unconditional; the agent set is
the one thing yoloAI's own layer makes configurable.** Everything else — language toolchains,
domain libraries — moves out of yoloAI's layer entirely and becomes the user's base image
(§3).

Per-agent costs, so a profile author can compute their own image rather than accept a default:

| Agent | Cost | Notes |
| --- | --- | --- |
| `nodejs` 20 | 181 MB | shared prerequisite for the npm-installed agents below |
| claude-code | 307 MB | |
| gemini-cli + codex + opencode | 1030 MB | measured together; ~340 MB each |
| aider (via uv) | 710 MB | bundles its own Python 3.12 — no Node needed |

> **Open question — Docker-in-Docker.** The 7% figure is against today's ~5 GB base. Against a
> ~1.1 GB minimal image the same 373 MB is ~33%, and 352 MB of it is the Docker CE stack alone.
> Unlike the other features, DinD is already expressible with **zero new config surface** —
> `isolation:` is an existing profile key — so gating it would be nearly free in design terms.
> This plan keeps it unconditional per the current decision, but the tradeoff is worth
> revisiting once `yoloai-minimal` is measured for real rather than projected.

## 1. The contract, in two tiers

Derived by tracing every in-guest invocation: `entrypoint.sh` → `entrypoint.py` →
`sandbox-setup.py` (+ `setup_helpers.py`, `tmux_io.py`, `status-monitor.py`, `firewall.py`),
plus host-side `Exec` calls in `runtime/docker/docker.go` and `runtime/*/`.

The tiers differ by **when they can be checked**: tier 1 needs nothing but the image; tier 2
is a function of which agent was selected, which the image build may not know.

### Tier 1 — static. Always required, checkable with no arguments.

| Requirement | Why | Source |
| --- | --- | --- |
| `/bin/sh` | trampoline; `sh -c` for setup commands and the launch probe | `entrypoint.sh`, `entrypoint.py:213`, `runtime/docker/launch.go:37` |
| `python3` (stdlib only, ≥3.9) | every entrypoint and the status monitor are Python | `entrypoint.sh:10` |
| `tmux` | the entire interaction model — send-keys, capture-pane, status | `tmux_io.py:67`, `status-monitor.py` |
| `gosu` | `os.execvp("gosu", …)` to drop root → `yoloai`; hardcoded, no fallback | `entrypoint.py:315,324` |
| user `yoloai`, home `/home/yoloai` | hardcoded in UID remap and chown | `entrypoint.py:97-117` |
| `id`, `chown`, `usermod`, `groupmod` | UID/GID remap to match host user | `entrypoint.py:97-107` |
| `date` | the JSONL boot record in the trampoline | `entrypoint.sh:9` |
| passwordless `sudo` for `yoloai` | privileged `mount --make-shared`; agent escalation | `entrypoint.py:247` |
| `git` | `:copy` mode diff/apply runs git **inside** the container | `runtime/docker/docker.go:756` |
| `/yoloai/{bin,tmux,logs,files,cache,overlay}` + placeholders, owned by `yoloai`; `/run/secrets` | bind-mount targets must pre-exist (Kata and any non-runc OCI runtime will not auto-create them) | Dockerfile:221-254 |
| `/yoloai/bin/*` — the twelve embedded scripts | **the crux, see §2** | `resources.go`, `build.go:createBuildContext` |
| `LANG=C.UTF-8` | without it agents fall back to ASCII rendering | Dockerfile:238 |
| `LABEL com.yoloai.managed="true"` | prune scoping; inherited today via `FROM yoloai-base` | Dockerfile:281 |
| `iptables` (+ `ipset`) | `--network-isolated`. 10 MB — unconditional | `firewall.py` |
| rootless helpers | podman-rootless parity. 11 MB — unconditional | Dockerfile:44-48 |
| Docker CE + compose + `mount` | `--isolation container-privileged`. 352 MB — unconditional per §Measured | `sandbox-setup.py:start_dockerd` |

`ipset` is a genuine soft dependency even within tier 1: `firewall.py:173` degrades to per-IP
iptables rules when it is absent. Keep that behaviour; it costs nothing.

### Tier 2 — agent-derived. A function of the selected agent, not a constant.

The required binary is the first token of `Definition.InteractiveCmd`. It cannot fold into
tier 1 because **the image is built before the agent is known** — `agent_command` is resolved
per-sandbox at create time (`runtimeconfig.go:46`) from `--agent` / profile `agent:`, and one
image serves `--agent claude` and `--agent codex` interchangeably. The check already exists at
`sandbox-setup.py:926` (`shutil.which`); it lacks only a good error message.

### Not required by yoloAI at all

`tini` (docker `--init` supplies it — `docker.go:539`), `dnsutils`/`dig` (**not used anywhere**
— `firewall.py:74` resolves via `socket.getaddrinfo`), Go, Rust, clang, cmake, build-essential,
binutils-gold, golangci-lint, aider/uv, the non-selected agent CLIs, the VS Code CLI, ripgrep,
fd-find, jq, rsync, less, file, unzip, openssh-client, pkg-config, libssl-dev.

### On Alpine and on the native Claude installer

Two size wins that should **not** be taken:

- **Alpine.** `standards/dockerfile.md:49` records it as rejected on musl-vs-glibc grounds with
  a documented Claude Code installer failure. It saves ~30 MB against a ~3.9 GB reduction from
  dropping toolchains — the bloat is *what* we install, not the distro.
- **Claude's native installer.** Measures 252 MB against 488 MB for node+claude-code, and needs
  no system Node (`tart/build.go:91` already uses it). But `Dockerfile:133-136` rejects it
  because it bundles Bun, which **ignores proxy env vars** and auto-updates — load-bearing given
  `plans/egress-proxy-build.md`. Node stays for container backends.

## 2. Can we drop `yoloai-base` as a layer?

**Not today — and the failure is silent.** Two independent blockers:

1. **The runtime scripts only exist inside the Go binary.** They are `go:embed`-ed
   (`resources.go`) and materialized *only* into the base image's build context
   (`build.go:createBuildContext`). The profile build context (`createProfileBuildContext`) tars
   the user's profile directory and nothing else. So `FROM r-base:4.4` yields an empty
   `/yoloai/bin` — the user cannot hand-write those files, they are not on disk.
2. **Nothing validates the `FROM` line.** `ResolveProfileImage` picks a tag; no code reads the
   Dockerfile's base. The build *succeeds* and the container dies at boot, after a multi-minute
   build, with a bare exec error.

**But the machinery is close.** `BuildProfileImage` already accepts an arbitrary Dockerfile and
tag; `ProfileImageNeedsBuild` already takes a `parentDir` for rebuild propagation. What is
missing is a *runtime layer* yoloAI can stamp onto any image, plus a contract check.

## 2a. Composing with an existing domain image

The natural thing to reach for is "extend from both my stack and yoloAI":

```dockerfile
FROM yoloai-base
FROM ghcr.io/example/r-bioc-singlecell:bioc3.22-r2   # ← does NOT merge
RUN R -q -e "pak::install('devtools')"
```

**This does not work, and fails silently.** Two `FROM` lines declare two build *stages*; the
second starts from a fresh filesystem and the first is discarded. The result is the bioc image
with no yoloAI runtime in it, which builds successfully and dies at boot. OCI has no
image-union operation — the only cross-stage mechanism is `COPY --from=`, which copies paths,
not package state, users, or apt metadata.

Since images cannot be merged, the composition must be **directional**: pick the fat stack as
the base and apply the thin layer on top. yoloAI's runtime is the thin one (scripts + a user +
a handful of packages), so it is the layer, and the domain image is the base. Three modes:

| Mode | Form | When |
| --- | --- | --- |
| **1. Stack on yoloAI** | `FROM yoloai-base` + install your stack | Today's model. Fine when the stack is a few apt/R/pip installs. |
| **2. yoloAI on stack** *(new)* | `base: image:ghcr.io/example/r-bioc:…` | The stack is a maintained third-party image. yoloAI appends its runtime layer. **The general answer.** |
| **3. Hand-rolled** *(new, escape hatch)* | user writes multi-stage with `COPY --from=` | Full control; needed when the layer's assumptions (below) don't hold. |

The example above is mode 2, and becomes:

```yaml
# ~/.yoloai/library/profiles/r-dev/config.yaml
base: image:ghcr.io/example/r-bioc-singlecell:bioc3.22-r2
agents: [claude]
```

```dockerfile
# ~/.yoloai/library/profiles/r-dev/Dockerfile — optional, stack additions only
RUN R -q -e "pak::install('devtools')"
```

No `FROM` line at all: yoloAI supplies the base from `config.yaml` and appends its runtime
layer after the user's `RUN` steps. The user's Dockerfile never mentions yoloAI, which is the
property that makes an existing domain image usable unmodified.

### Design requirement: the runtime layer must be cherry-pickable

Mode 3 is only viable if the layer's filesystem contribution is self-contained enough to
`COPY --from=`. That argues for vendoring the layer's binaries (notably `gosu`) under
`/yoloai/bin` rather than `/usr/local/bin`, so that:

```dockerfile
FROM ghcr.io/example/r-bioc-singlecell:bioc3.22-r2
COPY --from=yoloai-base /yoloai /yoloai
RUN /yoloai/bin/install-runtime.sh    # user creation, sudoers, LANG, label
```

is a complete and supportable escape hatch. This is a constraint on how Phase 0 writes
`runtime-layer.Dockerfile.tmpl`, and it costs nothing to honour if decided up front.

### Assumptions the layer makes about a foreign base

Each is a real failure mode against real-world domain images, and each needs an explicit
decision in Phase 2:

- **Debian/Ubuntu derived.** The layer's package installs are apt-based. Scientific images are
  usually Debian/Ubuntu (the rocker/Bioconductor lineage is), but RHEL/rocky and Alpine bases
  exist. Options: detect and branch, ship per-family templates, or document the constraint and
  fail `verify-image` with a clear message. **Recommend documenting + failing clearly** first;
  branch later only if demand appears.
- **UID 1001 is free.** The layer creates `yoloai` at 1001. Many domain images already ship a
  non-root user (rocker uses `rstudio`), and a collision breaks `useradd`. The layer must
  tolerate an existing UID — either reuse the base's user or pick a free UID and record it.
- **`ENTRYPOINT`/`CMD` are ours to take.** Domain images often set `CMD` to R or a notebook
  server. The layer overrides `ENTRYPOINT`, which is correct and intended, but should be called
  out so users are not surprised their image "stopped launching RStudio".
- **No `com.yoloai.managed` label.** Inherited free via `FROM yoloai-base`; a foreign base does
  not carry it, so the layer must stamp it (see Phase 2).

## 3. Plan

### Phase 0 — make the contract a real artifact

No user-visible change; converts the implicit contract into something checkable.

- **`internal/imagecontract`** — declares tier 1 as data (binaries, paths, user, env) and tier 2
  as a derived query `RequiredAgentBinary(def) string`. One source of truth for checks and docs.
- **Refactor `runtime/tart/build.go:101`** (`requiredTools = ["tmux","node","jq","rg","claude"]`)
  to consume it. That list is today's only contract check, and it is both stale and
  tart-specific; folding it in proves the abstraction and removes a duplicate.
- **`runtime-layer.Dockerfile.tmpl`** — an embedded template producing the final stage that makes
  *any* image yoloAI-drivable: `COPY` the twelve scripts, create the `yoloai` user + sudoers +
  subuid/subgid, create `/yoloai/*` and `/run/secrets`, set `LANG`, `YOLOAI_DIR`, `PATH`, the
  `com.yoloai.managed` label, `ENTRYPOINT`, and install the tier-1 feature packages. Extracted
  from the tail of the current base Dockerfile (lines 174-283), which is already exactly this.
  `yoloai-base` becomes "batteries stage + runtime layer", proving the split by construction.
- **`yoloai system verify-image <ref> [--agent <name>]`** — runs the contract check in a
  throwaway container. Tier 1 needs no flag; `--agent` adds the tier-2 check. This turns a silent
  boot failure into an actionable message, and gives an agent-authored Dockerfile something to
  iterate against.
- **Agent preflight in `entrypoint.py`** — it already reads the resolved config; check the agent
  binary up front and fail with "not baked into this image; add it to `agents:` and rebuild"
  rather than the current bare miss at `sandbox-setup.py:926`.

### Phase 2 — user-declared bases and selectable agents (`feat/pluggable-base`)

Both keys are profile-scoped and build through the existing profile-image path, so
`yoloai-base` and its ~25 backend sites are never touched. `yoloai-minimal` is a named base a
profile points at, not a system base to swap — which is why the earlier standalone Phase 1
folds in here.

**`base:` key** in profile `config.yaml`:

```yaml
base: yoloai-base                      # default, current behaviour (FROM yoloai-base, no re-layer)
base: yoloai-minimal                   # embedded minimal stack, built on demand
base: image:ghcr.io/lab/r-stack:1.2    # an existing image, unmodified (§2a mode 2)
base: dockerfile:Base.Dockerfile       # a Dockerfile in the profile dir
```

For every value except the default, yoloAI assembles the profile image as `FROM <base>` + the
profile's (FROM-less) stack steps + the `agents:` installs + the Phase 0 runtime layer, then
builds it with `BuildProfileImage` under the profile's own tag. The default keeps today's exact
behaviour: `FROM yoloai-base`, which already carries the runtime layer, so it is not re-applied.
The user's own Dockerfile never needs to know anything about yoloAI — the point that makes an
existing domain image usable as-is.

**Base × profile-Dockerfile coexistence.** When `base:` is set, the profile Dockerfile is
FROM-less (stack additions only) and yoloAI injects the `FROM` and appends the runtime layer.
When `base:` is unset, the profile Dockerfile keeps its required `FROM yoloai-base` and current
behaviour — no re-layering. A profile Dockerfile that carries its own `FROM` *and* sets `base:`
is a validation error.

**`agents:` key** — which agents the assembled image bakes:

```yaml
agents: [claude]        # default on a custom base: [<the profile's resolved agent>]
```

Only meaningful on a custom base (on `yoloai-base` the five agents are already present and
cannot be un-layered). Defaults to the single resolved agent — the size win — and is not a
breaking change (see Decisions above). Needs an **`InstallCmd` on `agent.Definition`**, which
the struct's own doc comment already promises ("describes an agent's install…") but which does
not exist — the install is hardcoded in the base Dockerfile's `npm install -g` line. Moving it
into each agent's definition is what makes the set selectable and colocates the install with the
rest of the declaration. `node` is a shared prerequisite, emitted once if any declared agent is
npm-based.

Surfaces that must change — each is a real coupling:

- `internal/config/profile.go` — `ProfileConfig`/`MergedConfig` gain `Base` and `Agents`;
  profile-only handlers; `Base` merges last-non-empty-wins (matching `Backend`), `Agents`
  replacement-wins from the nearest profile that sets it (an agent *set* is not additively
  composed — a child asking for `[codex]` means codex, not codex-plus-parent's-agents).
- `ResolveProfileImage` — a profile with `base:` set has its own image even without a Dockerfile
  (the base itself is the customization), so image resolution keys on `base:` too, not only
  "does a Dockerfile exist".
- **`profileBuildChecksum` must include the base ref, the agent set, and the runtime-layer
  checksum.** It hashes the profile Dockerfile alone today, and `config.yaml` is excluded from
  the build context (`createProfileBuildContext`) — so without this, editing `base:` or `agents:`
  would not trigger a rebuild. Easiest bug to ship here.
- For `base: image:<ref>`, staleness also tracks the resolved **digest** (not the tag, which
  moves), matching the devcontainer-wrapper precedent in `environments.md`; `--pull` forces a
  re-resolve.
- `com.yoloai.managed` — free today via `FROM yoloai-base`. A foreign base does not carry it;
  the runtime layer (Phase 0) already stamps it, so a custom-base image gets it via the appended
  layer. Verified present by construction.
- `profileScaffold` (`profile.go:24`) — document both new keys.
- `ProfileInfo` / `profile info` — report the resolved base and agent set.
- **Apple has no `--secret` support** (`config.md:179`); the assembled layer must never require a
  build secret. It doesn't — keep it that way.
- **Tart and seatbelt have no OCI image concept.** `base:` is a clear validation error there.

**Foreign-base failure policy.** The runtime layer assumes a Debian/Ubuntu apt userland, a free
`yoloai`/UID-1001 (guarded — it reuses an existing account), and that `ENTRYPOINT` is yoloAI's
to take. A non-Debian base (RHEL, Alpine) is not detected-and-branched; it fails `verify-image`
with a clear message. Documenting + failing clearly beats a detection matrix that pretends to
support bases it has not been tested against.

### Phase 3 — the authoring skill

Only after Phase 0. A skill that bespoke-builds a Dockerfile is good ergonomics, but it must not
*be* the contract — a markdown checklist an agent reads is unverifiable and drifts the moment
either side changes. Correct layering: `internal/imagecontract` is the source of truth → the doc
is generated from it → the skill iterates against `yoloai system verify-image` until it passes.
That gives the agent a real success signal instead of prose.

## Incidental findings

Landed on `fix/base-image-gocache` (a separate branch off `main`, independent of Phase 0):

- **~640 MB of dead Go build cache + 77 MB npm cache** removed from the base. `go install` wrote
  to `$HOME/.cache/go-build` (GOCACHE, uncleaned) and npm kept every tarball under `$HOME/.npm`.
  Confirmed by measurement, not inference: 640 MB against a 50 MB golangci-lint binary.
- **`standards/dockerfile.md`** corrected: agent CLIs are baked (not installed at create time);
  entrypoint scripts are bind-mounted for seatbelt/tart only (container backends `COPY` them,
  which is why `buildInputsChecksum` exists); `dnsutils`/`dig` is unused (`firewall.py` uses
  `socket.getaddrinfo`).
- **`Dockerfile` Bun/proxy rationale** rehomed inline; it had cited a nonexistent
  `docs/dev/research/implementation.md`.

Still open:

- **`design/environments.md`** describes devcontainer `build.dockerfile` auto-layering onto an
  arbitrary base; `devcontainer.go` marks that field "not used yet". This is a design doc for
  intended behaviour, not a stale claim — left as-is, but note it overlaps §2a mode 2 and should
  be reconciled with `base: dockerfile:` when Phase 2 lands.

## Sequencing (as built)

- **`fix/base-image-gocache`** (off `main`) — the cache/doc cleanup. Independent; mergeable alone.
- **`feat/image-contract`** (off `main`) — Phase 0. Complete and validated: the contract as data,
  the composed runtime layer proving the split, the tart dedup, and `verify-image`.
- **`feat/pluggable-base`** (off `feat/image-contract`) — Phase 2. In progress.

`agents:` and `base:` ship together here rather than split: under the profile-scoped decision
they share one assembly path (`FROM <base>` + stack + agent installs + runtime layer), so
splitting them would mean building that path twice. Phase 3 (the authoring skill) stays optional
polish, gated on Phase 0's `verify-image` as the agent's success signal.

Original note, still true: **`agents:` carries most of the size win** — ~1.7 GB of unbaked agent
CLIs for a single-agent custom-base image, against 373 MB for the entire feature closure. It
benefits every user regardless of which agent or stack they prefer.
