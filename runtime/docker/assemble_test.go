// ABOUTME: Tests for custom-base Dockerfile assembly — stack-half resolution,
// ABOUTME: FROM enforcement, checksum sensitivity, and build-context injection.

package docker

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
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
	for _, want := range []string{"FROM debian:trixie-slim", "npm install -g @anthropic-ai/claude-code", "com.yoloai.managed", "ENTRYPOINT"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("assembled Dockerfile missing %q:\n%s", want, out)
		}
	}
	if n := len(fromLine.FindAll(out, -1)); n != 1 {
		t.Errorf("want one FROM, got %d", n)
	}
}

func TestProfileBuildChecksum_CustomBaseChangesWithAgents(t *testing.T) {
	dir := t.TempDir()
	c1 := &config.CustomBaseBuild{Base: config.BaseRef{Kind: config.BaseMinimal}, Agents: []string{"claude"}}
	c2 := &config.CustomBaseBuild{Base: config.BaseRef{Kind: config.BaseMinimal}, Agents: []string{"codex"}}
	s1 := profileBuildChecksum(dir, c1)
	s2 := profileBuildChecksum(dir, c2)
	if s1 == "" || s2 == "" {
		t.Fatalf("empty checksum: %q %q", s1, s2)
	}
	if s1 == s2 {
		t.Error("changing agents: must change the build checksum")
	}
}

func TestCreateProfileBuildContext_CustomInjectsRuntimeFiles(t *testing.T) {
	dir := t.TempDir()
	// A FROM-less profile Dockerfile (stack additions) must be dropped in favour
	// of the override.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("RUN apt-get install -y r-base"), 0600); err != nil {
		t.Fatal(err)
	}
	override := []byte("FROM debian\nENTRYPOINT [\"/yoloai/bin/entrypoint.sh\"]")
	reader, err := createProfileBuildContext(dir, override)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		files[hdr.Name] = data
	}
	if !bytes.Equal(files["Dockerfile"], override) {
		t.Errorf("Dockerfile in context should be the override, got:\n%s", files["Dockerfile"])
	}
	// Runtime-layer COPY targets must be present or the build's COPY fails.
	for _, f := range []string{"entrypoint.sh", "entrypoint.py", "tmux.conf", "status-monitor.py"} {
		if _, ok := files[f]; !ok {
			t.Errorf("runtime-layer file %q missing from custom-base build context", f)
		}
	}
}
