// ABOUTME: The runtime contract a sandbox image must satisfy for yoloAI to drive it —
// ABOUTME: declared as data so it can be checked, documented, and reused across backends.

// Package imagecontract declares what a sandbox image must provide for yoloAI to
// drive it, as data rather than as an implicit property of one Dockerfile.
//
// Before this package the contract existed only as the base Dockerfile's package
// list. That made it unstatable ("what does a custom base need?" had no answer
// short of reading 284 lines of Dockerfile, most of which is toolchains yoloAI
// never calls) and unenforceable: an image missing a requirement built fine and
// failed at container start, after a multi-minute build, with a bare exec error.
//
// # Two tiers
//
// The requirements split by *when they can be checked*, which is the distinction
// that matters for enforcement:
//
//   - [Static] is invariant. Every sandbox needs it regardless of any choice the
//     user makes, so it can be checked against an image alone.
//   - [AgentBinary] is derived from the selected agent, which the image build may
//     not know: one image serves `--agent claude` and `--agent codex`
//     interchangeably, and agent_command is resolved per-sandbox at create time.
//
// Opt-in *features* (network isolation, Docker-in-Docker) are deliberately NOT a
// third tier. Their packages are unconditional in yoloAI's images because the
// whole closure measured 373 MB against a ~5 GB base — too small to justify the
// config surface and the runtime failure mode that gating would add. See
// docs/contributors/design/plans/pluggable-base-images.md.
package imagecontract

import "fmt"

// Kind distinguishes what a [Requirement] asserts about the image, which
// determines how a checker probes for it.
type Kind int

const (
	// KindBinary requires an executable resolvable on PATH.
	KindBinary Kind = iota
	// KindDir requires a directory to exist.
	KindDir
	// KindFile requires a regular file to exist. Used for bind-mount targets,
	// which must pre-exist in the image: runc auto-creates a missing mount
	// destination but Kata (and other non-runc OCI runtimes) do not.
	KindFile
	// KindUser requires a named user account to exist.
	KindUser
)

// Requirement is one element of the contract: a thing that must be present in
// the image, and the reason it must be.
//
// Why carries the *consequence of absence*, not a restatement of the name — it
// is surfaced verbatim by `yoloai system verify-image`, where a user reading
// "tmux: missing" needs to know that it is how yoloAI drives every session, not
// that tmux is a terminal multiplexer.
type Requirement struct {
	Kind Kind
	Name string // binary name, absolute path, or username
	Why  string // consequence of absence, user-facing
}

// runtimeUser is the account the entrypoint drops privileges to. It is not
// configurable: entrypoint.py hardcodes the name in its UID-remap and chown
// steps, and sandbox-setup.py assumes the matching home directory.
const runtimeUser = "yoloai"

// Universal returns the requirements every backend needs regardless of guest OS
// or isolation technology, because they back machinery that runs everywhere:
// sandbox-setup.py drives the agent under tmux on containers, Tart VMs, and
// Seatbelt alike, and copy-mode's diff/apply shells out to git in the guest.
//
// This is the subset a non-container backend can meaningfully check. Tart, whose
// guest is macOS, has no use for gosu or the UID-remap toolchain but absolutely
// needs these four.
func Universal() []Requirement {
	return []Requirement{
		{KindBinary, "sh", "the entrypoint trampoline is /bin/sh, and setup commands run via `sh -c`"},
		{KindBinary, "python3", "every entrypoint and the status monitor are Python (stdlib only)"},
		{KindBinary, "tmux", "yoloAI drives every agent session through tmux send-keys and capture-pane"},
		{KindBinary, "git", "copy-mode diff/apply runs git in the guest, not on the host"},
	}
}

// Static returns the tier-1 requirements for a *container* image: [Universal]
// plus everything the Linux entrypoint chain assumes, regardless of which agent
// runs in it or which features are enabled.
//
// The returned slice is freshly built per call, so callers may sort or filter it
// without disturbing other callers.
func Static() []Requirement {
	return append(Universal(), []Requirement{
		// --- Privilege handling ---
		{KindBinary, "gosu", "the entrypoint drops root to the runtime user with it; the name is hardcoded, with no fallback"},
		{KindBinary, "sudo", "the runtime user needs passwordless escalation for privileged-isolation setup"},
		{KindUser, runtimeUser, "the entrypoint remaps this account's UID to the host user's and chowns its home"},

		// --- UID remap toolchain (entrypoint.py runs these as root at boot) ---
		{KindBinary, "id", "used to read the runtime user's current UID/GID before remapping"},
		{KindBinary, "chown", "used to fix ownership of the home and state directories after remapping"},
		{KindBinary, "usermod", "used to remap the runtime user's UID to match the host"},
		{KindBinary, "groupmod", "used to remap the runtime user's GID to match the host"},
		{KindBinary, "date", "the entrypoint trampoline timestamps its first log line with it"},

		// --- Feature packages: unconditional by decision, not by necessity ---
		{KindBinary, "iptables", "network isolation installs a default-deny ruleset with it; without it an isolated sandbox refuses to start"},

		// --- State tree. Bind-mount targets must pre-exist (Kata does not create them). ---
		{KindDir, "/yoloai", "root of the sandbox state tree that yoloAI bind-mounts into"},
		{KindDir, "/yoloai/bin", "holds the runtime scripts the entrypoint execs"},
		{KindDir, "/yoloai/tmux", "holds the tmux config and socket"},
		{KindDir, "/yoloai/logs", "the host tails sandbox.jsonl here to track boot and agent state"},
		{KindDir, "/yoloai/files", "file-transfer staging area"},
		{KindDir, "/yoloai/cache", "agent cache mount point"},
		{KindDir, "/yoloai/overlay", "overlay mount point"},
		{KindDir, "/run/secrets", "credentials are delivered here at boot and read by the entrypoint"},
		{KindFile, "/yoloai/agent-status.json", "the status monitor writes agent idle/active transitions here"},
		{KindFile, "/yoloai/runtime-config.json", "the entrypoint reads the resolved sandbox config from here"},

		// --- The runtime scripts themselves ---
		{KindFile, "/yoloai/bin/entrypoint.sh", "container entrypoint; execs the Python entrypoint"},
		{KindFile, "/yoloai/bin/entrypoint.py", "root-stage setup: UID remap, secrets, network isolation"},
		{KindFile, "/yoloai/bin/sandbox-setup.py", "user-stage setup: launches the agent under tmux"},
	}...)
}

// Names extracts the requirement names from reqs, in order. Convenience for
// callers that build their own shell probe rather than using [ProbeScript].
func Names(reqs []Requirement) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Name
	}
	return out
}

// SoftStatic returns requirements whose absence degrades behaviour but does not
// prevent a sandbox from working. A checker should report these separately from
// [Static] — reporting them as failures would make a legitimately minimal image
// look broken.
func SoftStatic() []Requirement {
	return []Requirement{
		{KindBinary, "ipset", "network isolation falls back to slower per-IP iptables rules without it"},
		{KindBinary, "docker", "`--isolation container-privileged` cannot start a nested daemon without it"},
	}
}

// AgentBinary returns the tier-2 requirement for an agent whose interactive
// launch command is interactiveCmd, and reports whether there is one to check.
//
// The binary is the command's first token. This cannot be folded into [Static]
// because the image is built before the agent is known: `agent_command` is
// resolved per-sandbox at create time from --agent or the profile's `agent:`
// key, and one image serves several agents interchangeably.
//
// ok is false when interactiveCmd is empty — the shell and idle pseudo-agents
// have no binary of their own, and a caller must not synthesise a requirement
// on "".
func AgentBinary(agentType, interactiveCmd string) (req Requirement, ok bool) {
	bin := firstToken(interactiveCmd)
	if bin == "" {
		return Requirement{}, false
	}
	return Requirement{
		Kind: KindBinary,
		Name: bin,
		Why:  fmt.Sprintf("agent %q launches with it; the image was built without that agent", agentType),
	}, true
}

// firstToken returns the first whitespace-delimited token of s, or "" if there
// is none. Written out rather than using strings.Fields to avoid allocating a
// slice of every token when only the first is wanted.
func firstToken(s string) string {
	start := -1
	for i := 0; i < len(s); i++ {
		if isSpace(s[i]) {
			if start >= 0 {
				return s[start:i]
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start < 0 {
		return ""
	}
	return s[start:]
}

// isSpace reports whether b is an ASCII space character. Agent commands are
// ASCII by construction (they are Go string literals in the agent definitions),
// so this does not need to handle multi-byte whitespace.
func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
