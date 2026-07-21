# yoloAI runtime layer.
#
# This fragment turns an arbitrary Linux image into one yoloAI can drive. It is
# appended to a "batteries" Dockerfile (resources/Dockerfile) to compose
# yoloai-base, and is the same fragment that will be appended to a user-supplied
# base image. It must therefore assume NOTHING about what came before it beyond
# a Debian/Ubuntu-derived userland with apt.
#
# What it establishes is exactly internal/imagecontract.Static(); that package is
# the checkable statement of this file's job, and `yoloai system verify-image`
# is how you find out whether a given image satisfies it.
#
# Two conventions make the layer cherry-pickable — a user can lift the runtime
# onto a foreign base by hand with `COPY --from=yoloai-base /yoloai /yoloai`
# plus the user-creation RUN below:
#
#   * everything yoloAI ships lives under /yoloai, including the gosu binary,
#     rather than being scattered through /usr/local/bin;
#   * /yoloai/bin is put on ENV PATH (not merely /etc/profile.d), because
#     entrypoint.py resolves gosu with execvp, which reads the process PATH and
#     never sources a login profile.

# --- Contract packages -------------------------------------------------------
# The tier-1 requirements that are apt-installable. Cheap enough to be
# unconditional: the whole opt-in feature closure measured 373 MB against a
# ~5 GB base (2026-07-21), which does not justify gating. A base that already
# has these is unaffected; apt is idempotent.
#
# iptables is here rather than gated behind --network-isolated because
# firewall.py fails closed without it: an isolated sandbox on an image lacking
# iptables refuses to start, which is a worse trade than 10 MB.
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux \
    git \
    python3 \
    sudo \
    passwd \
    iptables \
    ipset \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# --- gosu --------------------------------------------------------------------
# Installed into /yoloai/bin (not /usr/local/bin) so the whole runtime is one
# cherry-pickable tree. Verified immediately: a truncated download would
# otherwise surface as an unexplained exec failure at container start.
ARG GOSU_VERSION=1.17
RUN mkdir -p /yoloai/bin \
    && curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL "https://github.com/tianon/gosu/releases/download/${GOSU_VERSION}/gosu-$(dpkg --print-architecture)" \
      -o /yoloai/bin/gosu \
    && chmod +x /yoloai/bin/gosu \
    && /yoloai/bin/gosu --version

# --- PATH --------------------------------------------------------------------
# /yoloai/bin must be on the *process* PATH so execvp finds gosu; /sbin and
# /usr/sbin so tools like sysctl resolve for subprocesses (rootlesskit child
# scripts, dockerd hooks) that don't inherit a login PATH.
ENV PATH="/yoloai/bin:/usr/sbin:/sbin:${PATH}"
# Also via profile.d, so shells started fresh by an agent pick it up even when
# Docker ENV is not inherited.
# SC2016: the literal `$PATH` is intended — it is expanded by the shell that
# sources the file at runtime, not by Docker at build time.
# hadolint ignore=SC2016
RUN echo 'export PATH="/yoloai/bin:/usr/sbin:/sbin:$PATH"' > /etc/profile.d/yoloai-path.sh

# --- Runtime user ------------------------------------------------------------
# Placeholder UID/GID; entrypoint.py remaps them to the host user's at boot.
#
# Every step tolerates the account or IDs already existing, because a foreign
# base may well ship its own non-root user — the rocker/Bioconductor images used
# for R work ship `rstudio`, and scientific images commonly claim 1000/1001. A
# plain `groupadd -g 1001` against a taken GID fails the build, so the layer
# reuses whatever is there and only creates what is missing. The name is what
# matters, not the number: entrypoint.py looks the account up by name.
#
# The docker group is joined only if it exists — a base without Docker-in-Docker
# has no such group, and an unconditional `usermod -aG docker` would fail.
RUN if ! getent group yoloai >/dev/null 2>&1; then \
        groupadd -g 1001 yoloai 2>/dev/null || groupadd yoloai; \
    fi \
    && if ! id -u yoloai >/dev/null 2>&1; then \
        useradd -m -u 1001 -g yoloai -s /bin/bash yoloai 2>/dev/null \
          || useradd -m -g yoloai -s /bin/bash yoloai; \
    fi \
    && echo 'yoloai ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/yoloai \
    && chmod 0440 /etc/sudoers.d/yoloai \
    && echo "yoloai:1001:64535" > /etc/subuid \
    && echo "yoloai:1001:64535" > /etc/subgid \
    && if getent group docker >/dev/null 2>&1; then usermod -aG docker yoloai; fi

# --- Agent state and home seed placeholders ----------------------------------
# Bind mount targets must exist in the image: Kata Containers (and any OCI
# runtime that, unlike runc, does not auto-create missing targets) requires both
# directory and file destinations to be present before the mount is applied.
#
# The file placeholders are deliberately EMPTY and generic — agent-specific
# defaults (e.g. aider's "{}" conf) live in the agent definition via
# SeedFile.Content, which always stages the file so it is bind-mounted *over*
# the empty placeholder; the placeholder is never the file the agent reads.
RUN mkdir -p \
        /home/yoloai/.claude \
        /home/yoloai/.gemini \
        /home/yoloai/.codex \
        /home/yoloai/.local/share/opencode \
        /home/yoloai/.config/github-copilot \
        /home/yoloai/.config/opencode \
        /home/yoloai/.vscode/cli \
    && touch \
        /home/yoloai/.claude.json \
        /home/yoloai/.opencode.json \
        /home/yoloai/.aider.conf.yml \
        /home/yoloai/.tmux.conf \
    && chown -R yoloai:yoloai /home/yoloai

# UTF-8 locale — C.UTF-8 is always present on Debian/Ubuntu without locale-gen.
# Without this, apps like Claude Code fall back to ASCII rendering.
ENV LANG=C.UTF-8

# --- Sandbox state tree ------------------------------------------------------
ENV YOLOAI_DIR=/yoloai
RUN mkdir -p \
        /yoloai/bin \
        /yoloai/tmux \
        /yoloai/logs \
        /yoloai/files \
        /yoloai/cache \
        /yoloai/overlay \
    && touch \
        /yoloai/agent-status.json \
        /yoloai/runtime-config.json \
        /yoloai/prompt.txt \
    && mkdir -p /run/secrets \
    && chown -R yoloai:yoloai /yoloai

COPY tmux.conf /yoloai/tmux/tmux.conf
COPY entrypoint.sh /yoloai/bin/entrypoint.sh
COPY entrypoint.py /yoloai/bin/entrypoint.py
COPY firewall.py /yoloai/bin/firewall.py
COPY install-firewall.py /yoloai/bin/install-firewall.py
COPY sandbox-setup.py /yoloai/bin/sandbox-setup.py
COPY setup_helpers.py /yoloai/bin/setup_helpers.py
COPY tmux_io.py /yoloai/bin/tmux_io.py
COPY status-monitor.py /yoloai/bin/status-monitor.py
COPY diagnose-idle.sh /yoloai/bin/diagnose-idle.sh
COPY agent-run.sh /yoloai/bin/agent-run.sh
COPY yoloai-resume /yoloai/bin/yoloai-resume
RUN chmod +x /yoloai/bin/entrypoint.sh /yoloai/bin/entrypoint.py /yoloai/bin/install-firewall.py /yoloai/bin/diagnose-idle.sh /yoloai/bin/agent-run.sh /yoloai/bin/yoloai-resume

# Marks this image (and every profile image built FROM it — LABELs are inherited)
# as yoloai-authored, matching the com.yoloai.managed label already stamped on
# yoloai-created volumes. Stamped here rather than in the batteries half so that
# an image built on a FOREIGN base still carries it: `FROM yoloai-base` inherits
# the label for free, but `base: image:ghcr.io/...` would not.
LABEL com.yoloai.managed="true"

ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]
