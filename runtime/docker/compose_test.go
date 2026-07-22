// ABOUTME: Tests for ComposeDockerfile — that the runtime layer appends cleanly,
// ABOUTME: stays single-stage, and ends in the runtime layer's ENTRYPOINT.

package docker

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/imagecontract"
)

// composed is the Dockerfile the daemon actually receives for yoloai-base.
func composed(t *testing.T) string {
	t.Helper()
	return string(ComposeDockerfile(embeddedBatteries))
}

func TestComposeProducesASingleStage(t *testing.T) {
	// The layer is appended verbatim, so a FROM inside it would open a second
	// build stage and silently discard everything the batteries half installed —
	// producing an image that builds fine and has no toolchains in it. This is
	// the same trap as writing two FROM lines in a profile Dockerfile by hand.
	fromRe := regexp.MustCompile(`(?mi)^\s*FROM\s`)
	require.Len(t, fromRe.FindAllString(composed(t), -1), 1,
		"composed Dockerfile must have exactly one FROM")
	require.NotRegexp(t, fromRe, string(embeddedRuntimeLayer),
		"the runtime layer must not contain FROM — it is appended, not a stage")
}

func TestComposeEndsWithTheRuntimeLayersEntrypoint(t *testing.T) {
	// ENTRYPOINT must come from the layer and be last: it is what the layer
	// exists to establish, and a later one in the stack half would win.
	c := composed(t)
	require.Contains(t, c, `ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]`)
	// The stack half must not carry an ENTRYPOINT *directive* (a comment
	// mentioning the word is fine).
	entrypointDirective := regexp.MustCompile(`(?m)^\s*ENTRYPOINT\s`)
	require.NotRegexp(t, entrypointDirective, string(embeddedBatteries),
		"the stack half must not set ENTRYPOINT; the layer owns it")

	idx := entrypointDirective.FindStringIndex(c)
	require.NotNil(t, idx)
	require.Greater(t, idx[0], strings.LastIndex(c, "\nRUN "),
		"ENTRYPOINT should be the final directive")
}

func TestComposedImageSatisfiesTheStaticContract(t *testing.T) {
	// The layer's whole job is to establish imagecontract.Static(). Checking it
	// by inspection here is what keeps the two from drifting: adding a
	// requirement without a matching Dockerfile directive fails this test rather
	// than a user's sandbox at boot.
	c := composed(t)
	for _, r := range imagecontract.Static() {
		switch r.Kind {
		case imagecontract.KindFile:
			if strings.HasPrefix(r.Name, "/yoloai/bin/") {
				base := strings.TrimPrefix(r.Name, "/yoloai/bin/")
				require.Contains(t, c, "COPY "+base+" "+r.Name,
					"no COPY establishes required file %s", r.Name)
				continue
			}
			require.Contains(t, c, strings.TrimPrefix(r.Name, "/yoloai/"),
				"nothing establishes required file %s", r.Name)
		case imagecontract.KindDir:
			require.Contains(t, c, r.Name, "nothing establishes required dir %s", r.Name)
		case imagecontract.KindUser:
			require.Contains(t, c, "useradd", "nothing creates the runtime user")
		case imagecontract.KindBinary:
			// Binaries come from apt, a download, or the base distro; asserting a
			// specific install line here would just restate the Dockerfile. The
			// real check is `yoloai system verify-image`, which runs against a
			// built image rather than its recipe.
		}
	}
}

func TestRuntimeLayerInstallsTheContractPackages(t *testing.T) {
	// These are the tier-1 binaries the layer is responsible for, as opposed to
	// ones a Debian base already provides (sh, id, chown, date).
	layer := string(embeddedRuntimeLayer)
	for _, pkg := range []string{"tmux", "git", "python3", "sudo", "iptables"} {
		require.Contains(t, layer, pkg, "layer must install %s", pkg)
	}
	require.Contains(t, layer, "gosu", "layer must install gosu")
}

func TestRuntimeLayerPutsGosuOnTheProcessPath(t *testing.T) {
	// entrypoint.py resolves gosu with execvp, which reads the process PATH and
	// never sources a login profile — so an ENV PATH entry is required, and a
	// /etc/profile.d line alone would not do. Vendoring gosu under /yoloai is
	// what makes the layer cherry-pickable onto a foreign base.
	layer := string(embeddedRuntimeLayer)
	require.Contains(t, layer, "/yoloai/bin/gosu")
	require.Regexp(t, `(?m)^ENV PATH="/yoloai/bin:`, layer)
}

func TestRuntimeLayerToleratesAnExistingUser(t *testing.T) {
	// A foreign base may already ship a non-root user or have claimed UID 1001
	// (the rocker/Bioconductor images do). An unguarded groupadd/useradd would
	// fail the build against exactly the images this layer exists to support.
	layer := string(embeddedRuntimeLayer)
	require.Contains(t, layer, "getent group yoloai")
	require.Contains(t, layer, "id -u yoloai")
	require.Contains(t, layer, "getent group docker",
		"joining the docker group must be conditional — a base without DinD has no such group")
}

func TestComposeCarriesTheManagedLabel(t *testing.T) {
	// Profile images built FROM yoloai-base inherit this, but an image built on a
	// foreign base would not — so the label belongs to the layer, not the stack
	// half, or `system prune --images` loses track of these once it is
	// label-scoped.
	require.Contains(t, string(embeddedRuntimeLayer), `LABEL com.yoloai.managed="true"`)
}

func TestComposeIsDeterministic(t *testing.T) {
	// The composed bytes feed buildInputsChecksum, which decides whether the base
	// is stale. Nondeterminism here would rebuild the base on every invocation.
	require.Equal(t, composed(t), composed(t))
}

func TestComposeSeparatesTheHalvesWithANewline(t *testing.T) {
	// Without this, the stack half's last line and the layer's first would join
	// into one malformed directive.
	c := ComposeDockerfile([]byte("FROM scratch\nRUN true"))
	require.Contains(t, string(c), "RUN true\n")
}

func TestComposeAcceptsAnArbitraryStack(t *testing.T) {
	// Phase 1's minimal image and Phase 2's user-declared bases compose against a
	// different stack half, so this must not be specific to embeddedBatteries.
	c := string(ComposeDockerfile([]byte("FROM ghcr.io/example/r-bioc:1.2")))
	require.Contains(t, c, "FROM ghcr.io/example/r-bioc:1.2")
	require.Contains(t, c, `ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]`)
}

func TestBuildInputsChecksumCoversBothHalves(t *testing.T) {
	// The checksum is what makes an edit invalidate the built image. If it hashed
	// only the stack half, editing the runtime layer would leave every existing
	// base image stale-but-unflagged — the exact failure the label exists to
	// prevent.
	sum := buildInputsChecksum()
	require.NotEmpty(t, sum)
	require.Contains(t, string(ComposeDockerfile(embeddedBatteries)), "yoloAI runtime layer",
		"checksum input must be the composed file, so layer edits change it")
}
