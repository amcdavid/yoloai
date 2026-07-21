# yoloai-minimal, the lean stack half.
#
# This is the "batteries" analogue of resources/Dockerfile, but stripped to the
# few packages the runtime layer and the agent installs can't do without: a
# Debian base plus curl/gnupg/ca-certificates. It is NOT a complete image — the
# assembler appends the selected agents' installs and then the runtime layer
# (resources/runtime-layer.Dockerfile), exactly as it does for a user base.
#
# Everything the *runtime* needs — tmux, git, python3, sudo, iptables, ipset,
# gosu, the runtime user, the /yoloai tree, the entrypoint — is in the runtime
# layer, so it is deliberately absent here. What remains is only what must be
# present *before* that layer runs: curl (the layer downloads gosu with it) and
# gnupg + ca-certificates (the Node install fetches the NodeSource key with them).
#
# This exists so `base: yoloai-minimal` gives a profile a ~1.7 GB-lighter image
# than yoloai-base — no unused agent CLIs, no Go/Rust/clang toolchains — while
# still being a yoloAI-authored, apt-based userland the runtime layer can drive.
FROM debian:trixie-slim

# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    gnupg \
    && rm -rf /var/lib/apt/lists/*
