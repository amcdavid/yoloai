// ABOUTME: Tests for the image contract and its probe — requirement sets, probe
// ABOUTME: script generation, parsing, and shell-injection safety under a real sh.

package imagecontract

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaticCoversTheHardcodedRuntimeNames(t *testing.T) {
	// These are the names the in-guest scripts hardcode with no fallback, so
	// an image without them fails at boot rather than degrading. Listed here
	// independently of Static() so that dropping one from the contract fails a
	// test rather than silently widening what counts as a conformant image.
	for _, name := range []string{
		"sh", "python3", "tmux", "gosu", "sudo", "git",
		"id", "chown", "usermod", "groupmod", "date",
		"/yoloai/bin/entrypoint.sh", "/yoloai/bin/entrypoint.py",
		"/yoloai/bin/sandbox-setup.py", "/run/secrets", "yoloai",
	} {
		require.True(t, hasReq(Static(), name), "Static() must require %q", name)
	}
}

func TestStaticNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Static() {
		require.False(t, seen[r.Name], "duplicate requirement %q", r.Name)
		seen[r.Name] = true
	}
}

func TestEveryRequirementExplainsTheConsequenceOfAbsence(t *testing.T) {
	// Why is surfaced verbatim to users by verify-image, so an empty or
	// name-restating one is a defect, not a style nit.
	for _, r := range append(Static(), SoftStatic()...) {
		require.NotEmpty(t, r.Why, "requirement %q has no Why", r.Name)
		require.NotEqual(t, r.Name, r.Why, "requirement %q restates its name", r.Name)
	}
}

func TestSoftStaticIsDisjointFromStatic(t *testing.T) {
	// A requirement in both tiers would be reported as a hard failure and a
	// soft warning at once — contradictory advice.
	for _, s := range SoftStatic() {
		require.False(t, hasReq(Static(), s.Name),
			"%q is in both Static and SoftStatic", s.Name)
	}
}

func TestDockerIsSoftNotHard(t *testing.T) {
	// Regression guard for the reasoning in the plan: Docker-in-Docker is a
	// 352 MB opt-in, and start_dockerd already degrades gracefully without it.
	// Promoting it to Static would make every minimal image non-conformant.
	require.True(t, hasReq(SoftStatic(), "docker"))
	require.False(t, hasReq(Static(), "docker"))
}

func TestIptablesIsHardNotSoft(t *testing.T) {
	// The mirror of the above: at 10 MB it is unconditional, and firewall.py
	// fails closed without it, so an isolated sandbox on such an image cannot
	// start at all. That is a hard requirement.
	require.True(t, hasReq(Static(), "iptables"))
	require.False(t, hasReq(SoftStatic(), "iptables"))
}

func TestAgentBinaryTakesFirstToken(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, want string
	}{
		{"bare command", "claude", "claude"},
		{"command with flags", "claude --dangerously-skip-permissions", "claude"},
		{"leading whitespace", "  codex  --flag", "codex"},
		{"tab separated", "aider\t--yes-always", "aider"},
		{"single trailing space", "gemini ", "gemini"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, ok := AgentBinary("x", tc.cmd)
			require.True(t, ok)
			require.Equal(t, tc.want, req.Name)
			require.Equal(t, KindBinary, req.Kind)
		})
	}
}

func TestAgentBinaryReportsNoneForCommandlessAgents(t *testing.T) {
	// The shell and idle pseudo-agents have no binary of their own. Returning a
	// requirement on "" would make every image fail the check.
	for _, cmd := range []string{"", "   ", "\t\n"} {
		_, ok := AgentBinary("shell", cmd)
		require.False(t, ok, "expected no requirement for %q", cmd)
	}
}

func TestAgentBinaryWhyNamesTheAgent(t *testing.T) {
	req, ok := AgentBinary("codex", "codex --flag")
	require.True(t, ok)
	require.Contains(t, req.Why, "codex")
}

// --- probe rendering ---

func TestProbeScriptEmitsOneResultPerRequirement(t *testing.T) {
	reqs := []Requirement{
		{KindBinary, "tmux", "why"},
		{KindDir, "/yoloai", "why"},
		{KindFile, "/yoloai/bin/entrypoint.sh", "why"},
		{KindUser, "yoloai", "why"},
	}
	script := ProbeScript(reqs)
	for _, r := range reqs {
		require.Contains(t, script, r.Name)
	}
	require.Equal(t, len(reqs), strings.Count(script, "printf"))
}

func TestProbeScriptUsesTheRightTestPerKind(t *testing.T) {
	require.Contains(t, ProbeScript([]Requirement{{KindBinary, "tmux", "w"}}), "command -v")
	require.Contains(t, ProbeScript([]Requirement{{KindDir, "/yoloai", "w"}}), "[ -d")
	// -e rather than -f: a bind-mount may have replaced the placeholder file.
	require.Contains(t, ProbeScript([]Requirement{{KindFile, "/f", "w"}}), "[ -e")
	require.Contains(t, ProbeScript([]Requirement{{KindUser, "yoloai", "w"}}), "id -u")
}

func TestProbeScriptQuotesNamesAgainstInjection(t *testing.T) {
	// The script is interpolated into a shell running as root in a container,
	// so a name carrying shell metacharacters must stay inert.
	//
	// Asserting the dangerous substring is absent would test the wrong thing:
	// correctly quoted, the payload IS present in the script, just inside single
	// quotes where the shell never evaluates it. The property is behavioural, so
	// the test executes the script and checks for the side effect.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	marker := filepath.Join(t.TempDir(), "pwned")
	payload := "x'; touch " + marker + "; echo '"

	out, err := exec.Command("sh", "-c", ProbeScript([]Requirement{{KindBinary, payload, "w"}})).CombinedOutput()
	require.NoError(t, err, "probe script failed: %s", out)

	require.NoFileExists(t, marker, "injected command executed — name was not quoted")

	// And the payload must still have been probed as a single literal name.
	results := ParseProbe([]Requirement{{KindBinary, payload, "w"}}, string(out))
	require.Len(t, results, 1)
	require.False(t, results[0].Present, "a binary by that name cannot exist")
}

func TestProbeScriptFailsClosedOnUnknownKind(t *testing.T) {
	// A Kind added without a matching probe must report missing, never present:
	// silently passing would mark every image conformant for that requirement.
	script := ProbeScript([]Requirement{{Kind: Kind(99), Name: "future", Why: "w"}})
	require.Contains(t, script, "if false;")
}

// --- probe parsing ---

func TestParseProbeMatchesResultsToRequirements(t *testing.T) {
	reqs := []Requirement{
		{KindBinary, "tmux", "w"},
		{KindBinary, "gosu", "w"},
	}
	out := resultPrefix + "OK\ttmux\n" + resultPrefix + "MISS\tgosu\n"
	results := ParseProbe(reqs, out)

	require.Len(t, results, 2)
	require.True(t, results[0].Present)
	require.False(t, results[1].Present)

	missing := Missing(results)
	require.Len(t, missing, 1)
	require.Equal(t, "gosu", missing[0].Req.Name)
}

func TestParseProbeIgnoresShellNoise(t *testing.T) {
	// Real images print MOTDs, conda activation notices, and nvm banners on
	// shell startup. None of it may be mistaken for a result.
	reqs := []Requirement{{KindBinary, "tmux", "w"}}
	out := "Welcome to Ubuntu!\n(base) conda activated\n" +
		resultPrefix + "OK\ttmux\n" +
		"WARNING: some unrelated warning\n"
	results := ParseProbe(reqs, out)
	require.Len(t, results, 1)
	require.True(t, results[0].Present)
}

func TestParseProbeTreatsAbsentLinesAsMissing(t *testing.T) {
	// A truncated probe (shell died partway) must not read as conformant.
	reqs := []Requirement{
		{KindBinary, "tmux", "w"},
		{KindBinary, "gosu", "w"},
	}
	results := ParseProbe(reqs, resultPrefix+"OK\ttmux\n")
	require.True(t, results[0].Present)
	require.False(t, results[1].Present, "requirement with no output line must be missing")
}

func TestParseProbeHandlesEmptyOutput(t *testing.T) {
	results := ParseProbe(Static(), "")
	require.Len(t, results, len(Static()))
	require.Len(t, Missing(results), len(Static()))
}

func TestMissingReturnsNilWhenAllPresent(t *testing.T) {
	results := []ProbeResult{{Req: Requirement{Name: "tmux"}, Present: true}}
	require.Empty(t, Missing(results))
}

// TestProbeScriptRunsUnderRealSh executes the generated script against the host
// shell. The script targets POSIX sh and runs inside arbitrary user images, so
// rendering it correctly is not the same as it being valid — this catches
// quoting and syntax faults that string assertions cannot.
func TestProbeScriptRunsUnderRealSh(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	reqs := []Requirement{
		{KindBinary, "sh", "w"},                        // certainly present
		{KindBinary, "definitely-not-a-real-bin", "w"}, // certainly absent
		{KindDir, "/", "w"},                            // certainly present
		{KindDir, "/no/such/dir", "w"},                 // certainly absent
	}
	cmd := exec.Command("sh", "-c", ProbeScript(reqs))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "probe script failed: %s", out)

	results := ParseProbe(reqs, string(out))
	require.True(t, results[0].Present, "sh should be found")
	require.False(t, results[1].Present, "bogus binary should be missing")
	require.True(t, results[2].Present, "/ should exist")
	require.False(t, results[3].Present, "/no/such/dir should be missing")
}

// hasReq reports whether reqs contains a requirement named name.
func hasReq(reqs []Requirement, name string) bool {
	for _, r := range reqs {
		if r.Name == name {
			return true
		}
	}
	return false
}
