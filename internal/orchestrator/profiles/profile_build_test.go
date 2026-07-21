// ABOUTME: Docker build-secret handling for profile builds: auto-detecting a
// ABOUTME: host ~/.npmrc, and parsing/validating id=,src= specs (order, missing
// ABOUTME: fields, tilde expansion, source-file existence).
package profiles

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

func TestAutoBuildSecrets_NpmrcExists(t *testing.T) {
	home := t.TempDir()

	npmrcPath := filepath.Join(home, ".npmrc")
	require.NoError(t, os.WriteFile(npmrcPath, []byte("registry=https://npm.example.com"), 0600))

	secrets := AutoBuildSecrets(home)
	require.Len(t, secrets, 1)
	assert.Equal(t, "id=npmrc,src="+npmrcPath, secrets[0])
}

func TestAutoBuildSecrets_NpmrcMissing(t *testing.T) {
	home := t.TempDir()

	secrets := AutoBuildSecrets(home)
	assert.Nil(t, secrets)
}

func TestValidateBuildSecret_Valid(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "token.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("secret"), 0600))

	tests := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "simple",
			spec: "id=mytoken,src=" + srcFile,
			want: "id=mytoken,src=" + srcFile,
		},
		{
			name: "reversed order",
			spec: "src=" + srcFile + ",id=mytoken",
			want: "id=mytoken,src=" + srcFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateBuildSecret(tt.spec, "/home/user")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateBuildSecret_MissingID(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "token.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("secret"), 0600))

	_, err := ValidateBuildSecret("src="+srcFile, "/home/user")
	assert.ErrorContains(t, err, "missing id=")
}

func TestValidateBuildSecret_MissingSrc(t *testing.T) {
	_, err := ValidateBuildSecret("id=mytoken", "/home/user")
	assert.ErrorContains(t, err, "missing src=")
}

func TestValidateBuildSecret_FileNotFound(t *testing.T) {
	_, err := ValidateBuildSecret("id=mytoken,src=/nonexistent/path/token.txt", "/home/user")
	assert.ErrorContains(t, err, "source file not found")
}

func TestValidateBuildSecret_TildeExpansion(t *testing.T) {
	home := t.TempDir()

	npmrcPath := filepath.Join(home, ".npmrc")
	require.NoError(t, os.WriteFile(npmrcPath, []byte("registry=https://npm.example.com"), 0600))

	got, err := ValidateBuildSecret("id=npmrc,src=~/.npmrc", home)
	require.NoError(t, err)
	assert.Equal(t, "id=npmrc,src="+npmrcPath, got)
}

// The chain-checksum tests below were retargeted from
// runtime/docker's profileBuildChecksum, which the D125 gate flagged as dead
// once the scheme moved here (DF152). Deleting them with it would have dropped
// the only coverage this logic has — DF105's lesson.

func TestChainChecksum_ValidDockerfile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM yoloai-base\nRUN apt install -y go"), 0600))

	sum := chainChecksum(dir, "")
	assert.NotEmpty(t, sum)
	assert.Len(t, sum, 64, "expected SHA-256 hex string")
}

func TestChainChecksum_Deterministic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base"), 0600))

	assert.Equal(t, chainChecksum(dir, ""), chainChecksum(dir, ""))
}

func TestChainChecksum_MissingDockerfile(t *testing.T) {
	assert.Empty(t, chainChecksum(t.TempDir(), ""),
		"no Dockerfile means no checksum, which the caller reads as 'cannot vouch' and builds")
}

// TestChainChecksum_ParentChangePropagates is the behaviour the marker scheme
// expressed by comparing file modification times, and the reason chaining
// replaced it. A parent rebuild has to invalidate every descendant, at any
// depth — and it now does so by construction rather than by the filesystem
// happening to order two writes correctly.
func TestChainChecksum_ParentChangePropagates(t *testing.T) {
	child := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(child, "Dockerfile"), []byte("FROM parent"), 0600))

	before := chainChecksum(child, "parent-checksum-v1")
	after := chainChecksum(child, "parent-checksum-v2")
	assert.NotEqual(t, before, after,
		"the child's own Dockerfile is unchanged, but its parent moved — the child is stale")

	// And a grandchild inherits the invalidation, which is what "at any depth" means.
	grand := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(grand, "Dockerfile"), []byte("FROM child"), 0600))
	assert.NotEqual(t, chainChecksum(grand, before), chainChecksum(grand, after))
}

// labelFake is a ProfileImageBuilder that only answers ImageLabels.
type labelFake struct {
	labels map[string]string
	ok     bool
}

func (labelFake) BuildProfileImage(context.Context, string, string, string, []string, config.Layout, feedback.ProgressSink, feedback.Sink, *slog.Logger) error {
	return nil
}
func (f labelFake) ImageLabels(context.Context, string) (map[string]string, bool) {
	return f.labels, f.ok
}
func (labelFake) ExpectedBaseChecksum() string { return "" }

// TestBaseChecksum_ReadsTheLabelAndToleratesAbsence pins the seed's input
// (DF156). The profile chain is seeded with the base image's own checksum label,
// so a base rebuild invalidates every profile built on the previous one — and
// that only works if the read is right.
//
// Absence deliberately seeds "" rather than something invalidating: a backend
// whose base predates the label would otherwise rebuild every profile on every
// launch forever, and it self-corrects the next time the base is built.
func TestBaseChecksum_ReadsTheLabelAndToleratesAbsence(t *testing.T) {
	ctx := context.Background()

	got := baseChecksum(ctx, labelFake{labels: map[string]string{
		runtime.BaseChecksumLabel: "abc123",
		"com.yoloai.managed":      "true",
	}, ok: true})
	assert.Equal(t, "abc123", got, "the base's own label is what seeds the chain")

	assert.Empty(t, baseChecksum(ctx, labelFake{labels: map[string]string{}, ok: true}),
		"a base present but unlabelled seeds empty — pre-label behaviour, not a forced rebuild")
	assert.Empty(t, baseChecksum(ctx, labelFake{ok: false}),
		"an unreadable base seeds empty rather than inventing a value")
}

// TestChainChecksum_BaseSeedPropagates is the behaviour DF156 is about: the same
// profile Dockerfile yields a different checksum when the base underneath it
// moved, so the profile rebuilds without anything having to compare timestamps
// or force a rebuild of the base.
func TestChainChecksum_BaseSeedPropagates(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base"), 0600))

	assert.NotEqual(t, chainChecksum(dir, "base-v1"), chainChecksum(dir, "base-v2"),
		"an unchanged profile Dockerfile on a moved base is stale")
	assert.NotEqual(t, chainChecksum(dir, "base-v1"), chainChecksum(dir, ""),
		"seeded and unseeded must differ, or the seed is not reaching the hash")
}

// customBaseFake is a backend that records what it was asked to build. It
// embeds runtime.Backend so only the methods EnsureProfileImage reaches need
// writing; anything else panics, which is the right failure for a test of this
// path.
type customBaseFake struct {
	runtime.Backend
	builtDockerfile map[string]string // tag → the Dockerfile in the dir it was handed
	builtChecksum   map[string]string // tag → the checksum it was asked to stamp
}

func (*customBaseFake) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{Capabilities: runtime.BackendCaps{CapAdd: true}}
}

func (*customBaseFake) Setup(context.Context, config.Layout, string, feedback.ProgressSink, feedback.Sink, *slog.Logger, bool) error {
	return nil
}

func (f *customBaseFake) BuildProfileImage(_ context.Context, dir, tag, checksum string, _ []string, _ config.Layout, _ feedback.ProgressSink, _ feedback.Sink, _ *slog.Logger) error {
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile")) //nolint:gosec // G304: test temp dir
	if err != nil {
		return err
	}
	f.builtDockerfile[tag] = string(data)
	f.builtChecksum[tag] = checksum
	return nil
}

func (*customBaseFake) ImageLabels(context.Context, string) (map[string]string, bool) {
	return nil, false // nothing in the store: every image needs building
}

func (*customBaseFake) ExpectedBaseChecksum() string { return "" }

// TestEnsureProfileImage_CustomBaseBuildsTheAssembly covers the wiring rather
// than the assembler: a profile with base: and no Dockerfile still gets an
// image, the backend is handed the assembled context rather than the profile
// directory, and the checksum it stamps moves when agents: does — the edit that
// otherwise reaches no build input at all, since config.yaml is never in one.
func TestEnsureProfileImage_CustomBaseBuildsTheAssembly(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal("test")
	profileDir := layout.ProfileDir("rdev")
	require.NoError(t, os.MkdirAll(profileDir, 0o750))
	writeConfig := func(agents string) {
		cfg := "base: image:rocker/r-ver:4.4\nagents: [" + agents + "]\n"
		require.NoError(t, os.WriteFile(filepath.Join(profileDir, "config.yaml"), []byte(cfg), 0o600))
	}

	build := func() (dockerfile, checksum string) {
		f := &customBaseFake{builtDockerfile: map[string]string{}, builtChecksum: map[string]string{}}
		require.NoError(t, EnsureProfileImage(context.Background(), f, layout, "rdev", nil, feedback.DiscardProgress, feedback.Discard, slog.New(slog.DiscardHandler), false))
		tag := config.ProfileImageTag(layout, "rdev")
		require.Contains(t, f.builtDockerfile, tag, "a base:-only profile must still get an image")
		return f.builtDockerfile[tag], f.builtChecksum[tag]
	}

	writeConfig("claude")
	dockerfile, claudeSum := build()
	assert.Contains(t, dockerfile, "FROM rocker/r-ver:4.4")
	assert.Contains(t, dockerfile, `ENTRYPOINT ["/yoloai/bin/entrypoint.sh"]`, "the runtime layer must be appended")
	assert.NotEmpty(t, claudeSum)

	writeConfig("codex")
	_, codexSum := build()
	assert.NotEqual(t, claudeSum, codexSum, "an agents: edit must change the checksum, or it never rebuilds")
}
