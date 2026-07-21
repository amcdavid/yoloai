// ABOUTME: Tests for the pluggable-base config surface — base-ref parsing,
// ABOUTME: custom-base resolution, base/agents merge semantics, and validation.

package config

import "testing"

func TestParseBaseRef(t *testing.T) {
	cases := []struct {
		in       string
		wantKind BaseKind
		wantVal  string
		wantErr  bool
	}{
		{"", BaseDefault, "", false},
		{"yoloai-base", BaseDefault, "", false},
		{"yoloai-minimal", BaseMinimal, "", false},
		{"image:ghcr.io/lab/r:1.2", BaseExistingImage, "ghcr.io/lab/r:1.2", false},
		{"dockerfile:Base.Dockerfile", BaseDockerfile, "Base.Dockerfile", false},
		{"image:", 0, "", true},
		{"dockerfile:", 0, "", true},
		{"dockerfile:sub/Base", 0, "", true}, // path not allowed
		{"dockerfile:..", 0, "", true},
		{"nonsense", 0, "", true},
	}
	for _, c := range cases {
		got, err := ParseBaseRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseBaseRef(%q): expected error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseBaseRef(%q): unexpected error %v", c.in, err)
			continue
		}
		if got.Kind != c.wantKind || got.Value != c.wantVal {
			t.Errorf("ParseBaseRef(%q) = {%d, %q}, want {%d, %q}", c.in, got.Kind, got.Value, c.wantKind, c.wantVal)
		}
	}
}

func TestBaseRefIsCustom(t *testing.T) {
	if (BaseRef{Kind: BaseDefault}).IsCustom() {
		t.Error("default base must not be custom")
	}
	for _, k := range []BaseKind{BaseMinimal, BaseExistingImage, BaseDockerfile} {
		if !(BaseRef{Kind: k}).IsCustom() {
			t.Errorf("kind %d should be custom", k)
		}
	}
}

func TestResolveCustomBaseBuild(t *testing.T) {
	// Default base → nil, nil.
	got, err := ResolveCustomBaseBuild(&MergedConfig{Agent: "claude"})
	if err != nil || got != nil {
		t.Errorf("default base: got (%+v, %v), want (nil, nil)", got, err)
	}

	// Custom base, no agents → defaults to the resolved agent.
	got, err = ResolveCustomBaseBuild(&MergedConfig{Base: "yoloai-minimal", Agent: "claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || len(got.Agents) != 1 || got.Agents[0] != "claude" {
		t.Errorf("expected [claude] default, got %+v", got)
	}
	if got.Base.Kind != BaseMinimal {
		t.Errorf("expected BaseMinimal, got %d", got.Base.Kind)
	}

	// Explicit agents win over the resolved agent.
	got, err = ResolveCustomBaseBuild(&MergedConfig{Base: "yoloai-minimal", Agent: "claude", Agents: []string{"codex", "aider"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Agents) != 2 || got.Agents[0] != "codex" {
		t.Errorf("expected [codex aider], got %+v", got.Agents)
	}

	// Custom base with no resolvable agent → error.
	if _, err := ResolveCustomBaseBuild(&MergedConfig{Base: "yoloai-minimal"}); err == nil {
		t.Error("expected error for custom base with no agent")
	}

	// Explicit empty agent list → error (bakes nothing).
	if _, err := ResolveCustomBaseBuild(&MergedConfig{Base: "yoloai-minimal", Agent: "claude", Agents: []string{}}); err == nil {
		t.Error("expected error for explicit empty agents on custom base")
	}
}

func TestApplyProfileToMerged_BaseAndAgents(t *testing.T) {
	merged := &MergedConfig{}

	// Base: last non-empty wins.
	applyProfileToMerged(merged, &ProfileConfig{Base: "yoloai-minimal"})
	if merged.Base != "yoloai-minimal" {
		t.Errorf("base = %q, want yoloai-minimal", merged.Base)
	}
	applyProfileToMerged(merged, &ProfileConfig{}) // empty must not clear
	if merged.Base != "yoloai-minimal" {
		t.Errorf("empty base cleared it: %q", merged.Base)
	}
	applyProfileToMerged(merged, &ProfileConfig{Base: "image:x"})
	if merged.Base != "image:x" {
		t.Errorf("base = %q, want image:x", merged.Base)
	}

	// Agents: replacement-wins from the nearest profile that sets it, not additive.
	applyProfileToMerged(merged, &ProfileConfig{Agents: []string{"claude", "codex"}})
	applyProfileToMerged(merged, &ProfileConfig{Agents: []string{"aider"}})
	if len(merged.Agents) != 1 || merged.Agents[0] != "aider" {
		t.Errorf("agents = %+v, want replacement [aider]", merged.Agents)
	}
	// A profile that does not set agents must not clear the inherited set.
	applyProfileToMerged(merged, &ProfileConfig{})
	if len(merged.Agents) != 1 || merged.Agents[0] != "aider" {
		t.Errorf("unset agents cleared inherited set: %+v", merged.Agents)
	}
}

func TestValidateProfileBase(t *testing.T) {
	// Default base is fine on any backend.
	if err := ValidateProfileBase("", false); err != nil {
		t.Errorf("default base on non-image backend: %v", err)
	}
	// Custom base needs an image-building backend.
	if err := ValidateProfileBase("yoloai-minimal", false); err == nil {
		t.Error("custom base on non-image backend should error")
	}
	if err := ValidateProfileBase("yoloai-minimal", true); err != nil {
		t.Errorf("custom base on image backend: %v", err)
	}
	// Malformed base surfaces even on a supported backend.
	if err := ValidateProfileBase("nonsense", true); err == nil {
		t.Error("malformed base should error")
	}
}
