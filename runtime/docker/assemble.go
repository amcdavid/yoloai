// ABOUTME: Assembles the Dockerfile for a custom-base profile image — FROM <base>
// ABOUTME: + the profile's stack + the resolved agents' installs + the runtime layer.

package docker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
)

// profileDockerfileName is the conventional stack-additions file in a profile dir.
const profileDockerfileName = "Dockerfile"

// assembleCustomBase renders the complete Dockerfile for a profile whose config
// selects a custom base. It resolves the base scheme into a stack half (with
// exactly one FROM), generates the selected agents' install steps, and appends
// the runtime layer.
//
// sourceDir is the profile directory, needed to read a FROM-less profile
// Dockerfile (for yoloai-minimal / image: bases) or the named base Dockerfile
// (for dockerfile: bases).
func assembleCustomBase(sourceDir string, spec *config.CustomBaseBuild) ([]byte, error) {
	stack, err := resolveStackHalf(sourceDir, spec.Base)
	if err != nil {
		return nil, err
	}
	installs, err := agent.InstallDockerfile(spec.Agents)
	if err != nil {
		return nil, err
	}
	return AssembleCustomBaseDockerfile(stack, installs), nil
}

// resolveStackHalf produces the pre-runtime-layer Dockerfile bytes for a base
// scheme: a stack half carrying exactly one FROM.
//
//   - yoloai-minimal: the embedded minimal stack, plus the profile's own
//     (FROM-less) Dockerfile appended as stack additions if present.
//   - image:<ref>:   a synthesized `FROM <ref>`, plus the profile's (FROM-less)
//     Dockerfile appended if present.
//   - dockerfile:<f>: the named file in the profile dir, used as-is (it carries
//     its own FROM and defines the base).
func resolveStackHalf(sourceDir string, ref config.BaseRef) ([]byte, error) {
	switch ref.Kind {
	case config.BaseMinimal:
		return withProfileStack(sourceDir, embeddedMinimalStack)
	case config.BaseExistingImage:
		from := []byte("FROM " + ref.Value + "\n")
		return withProfileStack(sourceDir, from)
	case config.BaseDockerfile:
		data, err := readProfileFile(sourceDir, ref.Value)
		if err != nil {
			return nil, fmt.Errorf("read base dockerfile %q: %w", ref.Value, err)
		}
		if !hasFROM(data) {
			return nil, fmt.Errorf("base dockerfile %q has no FROM instruction", ref.Value)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("resolveStackHalf: non-custom base kind %d", ref.Kind)
	}
}

// withProfileStack appends a profile's own Dockerfile (stack additions) to a
// stack half that already carries the FROM. The profile Dockerfile must be
// FROM-less in this mode — yoloAI owns the FROM — so a stray FROM is a clear
// error (it would start a second build stage and discard the base).
func withProfileStack(sourceDir string, base []byte) ([]byte, error) {
	extra, err := readProfileFile(sourceDir, profileDockerfileName)
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return nil, err
	}
	if hasFROM(extra) {
		return nil, fmt.Errorf("profile Dockerfile must not contain FROM when base: is set (yoloAI supplies it)")
	}
	var b bytes.Buffer
	b.Write(bytes.TrimRight(base, "\n"))
	b.WriteString("\n\n# --- profile stack additions (Dockerfile) ---\n")
	b.Write(bytes.TrimRight(extra, "\n"))
	b.WriteString("\n")
	return b.Bytes(), nil
}

// readProfileFile reads a single file from the profile directory. name is a bare
// filename (validated by config.ParseBaseRef for the dockerfile: case).
func readProfileFile(sourceDir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(sourceDir, name)) //nolint:gosec // G304: sourceDir from profile resolution, name is a bare filename
}

// hasFROM reports whether the Dockerfile bytes contain a FROM instruction on any
// line (ignoring comments and leading whitespace). Used to enforce the FROM-less
// contract for profile stack additions and to require a FROM in a base
// dockerfile.
func hasFROM(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if len(t) >= 4 && strings.EqualFold(t[:4], "FROM") && (len(t) == 4 || t[4] == ' ' || t[4] == '\t') {
			return true
		}
	}
	return false
}
