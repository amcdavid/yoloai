// ABOUTME: Tests for InstallDockerfile — the agent-CLI install fragment generator
// ABOUTME: used when assembling a custom-base profile image.

package agent

import (
	"strings"
	"testing"
)

func TestInstallDockerfileSingleNPMAgent(t *testing.T) {
	got, err := InstallDockerfile([]string{"claude"})
	if err != nil {
		t.Fatalf("InstallDockerfile: %v", err)
	}
	if !strings.Contains(got, "npm install -g @anthropic-ai/claude-code") {
		t.Errorf("missing claude install:\n%s", got)
	}
	if !strings.Contains(got, "command -v npm") {
		t.Errorf("npm agent should emit the guarded Node install:\n%s", got)
	}
	if strings.Contains(got, "aider") {
		t.Errorf("only claude selected, aider must not appear:\n%s", got)
	}
}

func TestInstallDockerfileNodeEmittedOnce(t *testing.T) {
	got, err := InstallDockerfile([]string{"claude", "codex", "opencode"})
	if err != nil {
		t.Fatalf("InstallDockerfile: %v", err)
	}
	if n := strings.Count(got, "command -v npm"); n != 1 {
		t.Errorf("Node prerequisite must be emitted exactly once, got %d:\n%s", n, got)
	}
	// All three npm installs share one retry loop (one RUN n=0 guard).
	if n := strings.Count(got, `RUN n=0; until`); n != 1 {
		t.Errorf("npm agents must share one retry loop, got %d:\n%s", n, got)
	}
	for _, pkg := range []string{"@anthropic-ai/claude-code", "@openai/codex", "opencode-ai"} {
		if !strings.Contains(got, pkg) {
			t.Errorf("missing package %s:\n%s", pkg, got)
		}
	}
}

func TestInstallDockerfileNonNPMAgentNoNode(t *testing.T) {
	got, err := InstallDockerfile([]string{"aider"})
	if err != nil {
		t.Fatalf("InstallDockerfile: %v", err)
	}
	if strings.Contains(got, "command -v npm") {
		t.Errorf("aider is not npm-based; Node install must not appear:\n%s", got)
	}
	if !strings.Contains(got, "uv tool install --python 3.12 aider-chat") {
		t.Errorf("missing aider install:\n%s", got)
	}
}

func TestInstallDockerfileMixed(t *testing.T) {
	got, err := InstallDockerfile([]string{"claude", "aider"})
	if err != nil {
		t.Fatalf("InstallDockerfile: %v", err)
	}
	if !strings.Contains(got, "command -v npm") {
		t.Errorf("mixed set has an npm agent; Node install expected:\n%s", got)
	}
	if !strings.Contains(got, "aider-chat") {
		t.Errorf("mixed set must still install aider:\n%s", got)
	}
}

func TestInstallDockerfileRejectsUnknownAndPseudo(t *testing.T) {
	for _, name := range []string{"nope", "test", "idle"} {
		if _, err := InstallDockerfile([]string{name}); err == nil {
			t.Errorf("expected error for non-bakeable agent %q", name)
		}
	}
}

func TestInstallDockerfileRejectsEmpty(t *testing.T) {
	if _, err := InstallDockerfile(nil); err == nil {
		t.Error("expected error for empty agent set")
	}
}
