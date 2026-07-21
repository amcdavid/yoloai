// ABOUTME: Tests for custom-base Dockerfile assembly — stack-half resolution,
// ABOUTME: FROM enforcement, lineage stamping, and the materialised build context.

package docker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

var fromLine = regexp.MustCompile(`(?m)^\s*FROM\s`)

func TestAssembleCustomBaseDockerfileHasSingleFROM(t *testing.T) {
	stack := []byte("FROM debian:trixie-slim\nRUN apt-get update")
	out := AssembleCustomBaseDockerfile(stack, "RUN npm install -g x")
	if n := len(fromLine.FindAll(out, -1)); n != 1 {
		t.Errorf("assembled Dockerfile must have exactly one FROM, got %d:\n%s", n, out)
	}
	// Runtime layer's ENTRYPOINT must be last (it comes from the appended layer).
	if !strings.Contains(string(out), "ENTRYPOINT") {
		t.Errorf("assembled Dockerfile missing runtime-layer ENTRYPOINT:\n%s", out)
	}
	if !strings.Contains(string(out), "RUN npm install -g x") {
		t.Errorf("assembled Dockerfile missing agent installs:\n%s", out)
	}
}

func TestAssembleCustomBaseDockerfileNoAgents(t *testing.T) {
	out := AssembleCustomBaseDockerfile([]byte("FROM alpine"), "")
	if n := len(fromLine.FindAll(out, -1)); n != 1 {
		t.Errorf("want one FROM, got %d", n)
	}
	// Still gets the runtime layer even with no agent installs.
	if !strings.Contains(string(out), "com.yoloai.managed") {
		t.Errorf("runtime layer (managed label) must always be appended:\n%s", out)
	}
}

func TestHasFROM(t *testing.T) {
	yes := []string{"FROM x", "  from debian", "# c\nFROM y", "FROM\tx"}
	no := []string{"", "# FROM commented", "RUN echo FROM", "FROMAGE x"}
	for _, s := range yes {
		if !hasFROM([]byte(s)) {
			t.Errorf("hasFROM(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if hasFROM([]byte(s)) {
			t.Errorf("hasFROM(%q) = true, want false", s)
		}
	}
}

func TestResolveStackHalf_Minimal(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveStackHalf(dir, config.BaseRef{Kind: config.BaseMinimal})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("FROM debian:trixie-slim")) {
		t.Errorf("minimal stack should carry its own FROM:\n%s", got)
	}
}

func TestResolveStackHalf_MinimalWithProfileStack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("RUN apt-get install -y r-base"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveStackHalf(dir, config.BaseRef{Kind: config.BaseMinimal})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("r-base")) {
		t.Errorf("profile stack additions should be appended:\n%s", got)
	}
	if n := len(fromLine.FindAll(got, -1)); n != 1 {
		t.Errorf("want one FROM, got %d", n)
	}
}

func TestResolveStackHalf_ImageRef(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveStackHalf(dir, config.BaseRef{Kind: config.BaseExistingImage, Value: "ghcr.io/lab/r:1.2"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("FROM ghcr.io/lab/r:1.2")) {
		t.Errorf("image base should synthesize FROM <ref>:\n%s", got)
	}
}

func TestResolveStackHalf_RejectsFROMInProfileStack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM sneaky\nRUN x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveStackHalf(dir, config.BaseRef{Kind: config.BaseMinimal}); err == nil {
		t.Error("a FROM in the profile stack additions must be rejected when base: is set")
	}
}

func TestResolveStackHalf_DockerfileBase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Base.Dockerfile"), []byte("FROM ubuntu:24.04\nRUN apt-get update"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveStackHalf(dir, config.BaseRef{Kind: config.BaseDockerfile, Value: "Base.Dockerfile"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("FROM ubuntu:24.04")) {
		t.Errorf("dockerfile base used as-is:\n%s", got)
	}
}

func TestResolveStackHalf_DockerfileBaseNeedsFROM(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Base.Dockerfile"), []byte("RUN echo no-from"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveStackHalf(dir, config.BaseRef{Kind: config.BaseDockerfile, Value: "Base.Dockerfile"}); err == nil {
		t.Error("a base dockerfile without FROM must be rejected")
	}
}

func TestAssembleCustomBase_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	spec := &config.CustomBaseBuild{
		Base:   config.BaseRef{Kind: config.BaseMinimal},
		Agents: []string{"claude"},
	}
	out, err := assembleCustomBase(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM debian:trixie-slim", `npm install -g "@anthropic-ai/claude-code@>=`, "com.yoloai.managed", "ENTRYPOINT"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("assembled Dockerfile missing %q:\n%s", want, out)
		}
	}
	if n := len(fromLine.FindAll(out, -1)); n != 1 {
		t.Errorf("want one FROM, got %d", n)
	}
}

func TestAssembleCustomBase_ChangesWithAgents(t *testing.T) {
	// The profile's chain checksum hashes the assembled Dockerfile, and base:/
	// agents: live in config.yaml, which no build context carries. So the
	// assembly is the only thing that can carry an agents: edit into a rebuild.
	dir := t.TempDir()
	c1 := &config.CustomBaseBuild{Base: config.BaseRef{Kind: config.BaseMinimal}, Agents: []string{"claude"}}
	c2 := &config.CustomBaseBuild{Base: config.BaseRef{Kind: config.BaseMinimal}, Agents: []string{"codex"}}
	a1, err := assembleCustomBase(dir, c1)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := assembleCustomBase(dir, c2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a1, a2) {
		t.Error("changing agents: must change the assembled Dockerfile")
	}
}

func TestAssembleCustomBase_StampsBaseLineage(t *testing.T) {
	// A custom-base image does not inherit yoloai-base's lineage label, and an
	// absent label reads as stale: without this every start would warn (DF156).
	dir := t.TempDir()
	spec := &config.CustomBaseBuild{Base: config.BaseRef{Kind: config.BaseExistingImage, Value: "debian:trixie-slim"}, Agents: []string{"claude"}}
	out, err := assembleCustomBase(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("LABEL %s=%q", runtime.BaseChecksumLabel, buildInputsChecksum())
	if !strings.Contains(string(out), want) {
		t.Errorf("assembled Dockerfile lacks %q:\n%s", want, out)
	}
}

func TestWriteCustomBaseContext(t *testing.T) {
	src := t.TempDir()
	// A FROM-less profile Dockerfile (stack additions) must be replaced by the
	// assembly, config.yaml kept out, and a profile's own copy of a runtime file
	// left alone.
	for name, content := range map[string]string{
		"Dockerfile":  "RUN apt-get install -y r-base",
		"config.yaml": "base: yoloai-minimal",
		"tmux.conf":   "# the user's own",
		"renv.lock":   "{}",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dst := t.TempDir()
	spec := &config.CustomBaseBuild{Base: config.BaseRef{Kind: config.BaseMinimal}, Agents: []string{"claude"}}
	assembled, err := WriteCustomBaseContext(src, spec, dst)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dst, name)) //nolint:gosec // G304: a test temp dir
		if err != nil {
			t.Fatalf("%s missing from custom-base build context: %v", name, err)
		}
		return string(data)
	}
	if got := read("Dockerfile"); got != string(assembled) {
		t.Errorf("Dockerfile in context should be the assembly, got:\n%s", got)
	}
	if !strings.Contains(string(assembled), "r-base") {
		t.Error("the profile's stack additions must be folded into the assembly")
	}
	if got := read("tmux.conf"); got != "# the user's own" {
		t.Errorf("a profile's own runtime file must win, got %q", got)
	}
	read("renv.lock")
	// Runtime-layer COPY targets must be present or the build's COPY fails.
	for _, f := range []string{"entrypoint.sh", "entrypoint.py", "status-monitor.py", "yoloai-resume"} {
		read(f)
	}
	if _, err := os.Stat(filepath.Join(dst, "config.yaml")); err == nil {
		t.Error("config.yaml must not enter the build context")
	}
}
