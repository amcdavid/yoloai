// ABOUTME: Renders the Dockerfile RUN steps that install a selected set of agent
// ABOUTME: CLIs, for the pluggable-base assembly (a custom base bakes only what it needs).

package agent

import (
	"fmt"
	"strings"
)

// nodeMajor pins the Node.js LTS line the npm-based agents install against. It
// mirrors NODE_MAJOR in the batteries Dockerfile: 20 (22 has gVisor ARM64 syscall
// incompatibilities). Keep the two in sync.
const nodeMajor = "20"

// nodeInstallStep is the guarded Node.js install emitted once when any selected
// agent installs via npm. It is a no-op when npm is already on PATH — so a base
// that already carries Node (the yoloai-minimal stack, or a foreign image that
// bundles it) skips the download, while a lean Debian base gets it. The recipe is
// the NodeSource one from the batteries Dockerfile; on a non-Debian base apt-get
// is absent and the build fails loudly, which is the documented foreign-base
// policy (a detection matrix would only pretend to support untested userlands).
//
// It installs its own prerequisites first (ca-certificates, curl, gnupg): a
// foreign base can't be assumed to ship them — a real R/Bioconductor image, for
// instance, has curl but no gpg, which otherwise fails the NodeSource key import
// with "gpg: not found". apt-get is idempotent, so a base that already has them
// pays only a metadata refresh.
const nodeInstallStep = `# Node.js ` + nodeMajor + ` — shared prerequisite for npm-based agents, installed once.
# Guarded: a base that already ships npm (e.g. yoloai-minimal) skips this entirely.
RUN command -v npm >/dev/null 2>&1 || ( \
      apt-get update \
      && apt-get install -y --no-install-recommends ca-certificates curl gnupg \
      && mkdir -p /etc/apt/keyrings \
      && curl --retry 5 --retry-delay 2 --retry-all-errors -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
         | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
      && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_` + nodeMajor + `.x nodistro main" \
         > /etc/apt/sources.list.d/nodesource.list \
      && apt-get update \
      && apt-get install -y --no-install-recommends nodejs \
      && rm -rf /var/lib/apt/lists/* )`

// InstallDockerfile renders the Dockerfile fragment that installs the given
// agents' CLIs into an assembled custom-base image. Each name must be a known
// agent with a non-empty InstallCmd; an unknown name or a pseudo-agent
// (test/idle/shell) that installs nothing is an error, so a profile can't
// silently bake an image with no working agent.
//
// The npm-based agents share one Node install (emitted once) and one retry loop
// around their `npm install -g` lines — matching the batteries Dockerfile, where
// a transient registry hiccup must not fail a multi-minute build with no recovery.
// Non-npm agents (aider, via uv/curl with its own --retry) each get their own RUN.
// The returned fragment ends without a trailing newline; the assembler joins it.
func InstallDockerfile(names []string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no agents selected to install")
	}

	var npmPkgs []string
	var otherSteps []string
	for _, name := range names {
		def := GetAgent(name)
		if def == nil {
			return "", fmt.Errorf("unknown agent %q", name)
		}
		if def.InstallCmd == "" {
			return "", fmt.Errorf("agent %q has no install command (not bakeable)", name)
		}
		if def.InstallViaNPM {
			npmPkgs = append(npmPkgs, def.InstallCmd)
			continue
		}
		otherSteps = append(otherSteps, fmt.Sprintf("# %s\nRUN %s", name, def.InstallCmd))
	}

	var b strings.Builder
	b.WriteString("# --- agent CLIs (yoloAI-generated; versions unpinned, agents track live APIs) ---")
	if len(npmPkgs) > 0 {
		b.WriteString("\n")
		b.WriteString(nodeInstallStep)
		b.WriteString("\nENV DISABLE_INSTALLATION_CHECKS=1\n")
		b.WriteString(npmRetryStep(npmPkgs))
	}
	for _, step := range otherSteps {
		b.WriteString("\n")
		b.WriteString(step)
	}
	return b.String(), nil
}

// npmRetryStep wraps the given `npm install -g <pkg>` commands in the same
// five-attempt retry loop the batteries Dockerfile uses.
func npmRetryStep(installs []string) string {
	var chained strings.Builder
	for i, cmd := range installs {
		if i > 0 {
			chained.WriteString(" \\\n        && ")
		}
		chained.WriteString(cmd)
	}
	return "RUN n=0; until [ \"$n\" -ge 5 ]; do \\\n" +
		"      " + chained.String() + " && break; \\\n" +
		"      n=$((n+1)); echo \"npm install failed (attempt $n/5); retrying in 5s\"; sleep 5; \\\n" +
		"    done; \\\n" +
		"    [ \"$n\" -lt 5 ] || { echo \"npm install failed after 5 attempts\"; exit 1; }"
}
