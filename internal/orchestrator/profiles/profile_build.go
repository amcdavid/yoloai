// ABOUTME: Profile image building: ensures profile Docker images are built in
// ABOUTME: dependency order (base → parent → child) and detects build secrets.
package profiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

// ProfileImageBuilder is optionally implemented by backends that support
// building custom images from profile Dockerfiles. The interface is defined in
// `runtime` alongside every other optional backend capability, so that backends
// can compile-time assert it; this alias keeps the name available to the
// orchestrator tier that consumes it.
type ProfileImageBuilder = runtime.ProfileImageBuilder

// EnsureProfileImage ensures that the Docker image for a profile and its
// entire inheritance chain are built and up to date. Non-Docker backends
// are a no-op. If force is true, all images in the chain are rebuilt.
// secrets are Docker BuildKit --secret specs passed to profile image builds.
//
// layout is the DataDir-rooted Layout used to locate the base profile
// directory and for any host-path needs Setup may have (Q-W.5 threads
// it through runtime.Backend.Setup).
func EnsureProfileImage(ctx context.Context, rt runtime.Backend, layout config.Layout, profileName string, secrets []string, progress feedback.ProgressSink, notices feedback.Sink, logger *slog.Logger, force bool) error {
	if !rt.Descriptor().Capabilities.CapAdd {
		return nil
	}

	builder, ok := runtime.ProfileImageBuilderOf(rt)
	if !ok {
		return nil
	}

	chain, err := config.ResolveProfileChain(layout, profileName)
	if err != nil {
		return err
	}

	// Ensure base image first
	baseProfileDir := filepath.Join(layout.ProfilesDir(), "base")
	if err := rt.Setup(ctx, layout, baseProfileDir, progress, notices, logger, force); err != nil {
		return fmt.Errorf("ensure base image: %w", err)
	}

	// Walk chain from root to leaf, building each profile that has a Dockerfile.
	// parentChecksum threads the ancestor chain; "" at the root, which preserves
	// the pre-label behaviour that a yoloai-base rebuild does NOT invalidate
	// profile images (see DF156 — that gap is real, predates this, and is filed
	// rather than silently closed here).
	// Seed the chain with the base image's own checksum label, so a base rebuild
	// invalidates every profile built on the previous one (DF156). Read from the
	// image rather than recomputed from the binary: it states what the store
	// actually holds, and Setup above has just made that current.
	//
	// An unreadable or unstamped base seeds "" — the pre-label behaviour — rather
	// than forcing a rebuild of everything. That case is a backend whose base
	// predates the label, and it self-corrects on the next base build.
	parentChecksum := baseChecksum(ctx, builder)

	// A profile's base:/agents: keys come from its merged config, over the
	// baked-in defaults only — a profile is self-contained (DF207).
	bakedIn, err := config.LoadBakedInDefaults()
	if err != nil {
		return fmt.Errorf("load baked-in defaults: %w", err)
	}

	for i, name := range chain {
		if name == "base" {
			continue
		}

		tag := config.ProfileImageTag(layout, name)
		buildDir, want, cleanup, ok, err := profileBuildInputs(layout, bakedIn, chain[:i+1], parentChecksum)
		if err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
		if !ok {
			// Neither a Dockerfile nor a custom base: no image of its own. Skip,
			// passing parentChecksum along unchanged.
			continue
		}
		err = buildIfStale(ctx, builder, buildDir, tag, name, want, secrets, layout, progress, notices, logger, force)
		cleanup()
		if err != nil {
			return err
		}

		parentChecksum = want
	}

	return nil
}

// buildIfStale builds tag from buildDir unless the store already holds it built
// from want (or force is set).
func buildIfStale(ctx context.Context, builder runtime.ProfileImageBuilder, buildDir, tag, profile, want string, secrets []string, layout config.Layout, progress feedback.ProgressSink, notices feedback.Sink, logger *slog.Logger, force bool) error {
	if !force && imageMatches(ctx, builder, tag, want) {
		return nil
	}
	// Progress, not a notice: this heads the build stream that follows it, and
	// the two belong on the same channel. The launch path's "recorded as built
	// but missing" line is a notice for the opposite reason — it reports a state
	// the user did not ask for and will want to know about after the fact.
	feedback.EmitProgress(progress, feedback.Progress{
		Event:   "profile.image_building",
		Message: fmt.Sprintf("Building profile image %s...", tag),
		Fields:  map[string]any{"tag": tag, "profile": profile},
	})
	if err := builder.BuildProfileImage(ctx, buildDir, tag, want, secrets, layout, progress, notices, logger); err != nil {
		return fmt.Errorf("build profile image %s: %w", tag, err)
	}
	return nil
}

// baseChecksum reads the checksum label off the base image in this backend's
// store. Empty when there is no base, no label, or it cannot be read.
func baseChecksum(ctx context.Context, builder runtime.ProfileImageBuilder) string {
	labels, ok := builder.ImageLabels(ctx, config.BaseImage)
	if !ok {
		return ""
	}
	return labels[runtime.BaseChecksumLabel]
}

// profileBuildInputs resolves what to build for the last profile in chain: the
// directory to hand the backend, and the chain checksum to expect. ok is false
// when the profile has neither a Dockerfile nor a custom base, and so no image
// of its own. cleanup is always safe to call.
//
// The chain checksum folds the parent's in, so an ancestor's Dockerfile change
// reaches every descendant without comparing file timestamps. A custom base
// builds from an assembled context instead of the profile directory, and hashes
// the assembled Dockerfile; see customBaseContext.
func profileBuildInputs(layout config.Layout, bakedIn *config.YoloaiConfig, chain []string, parentChecksum string) (buildDir, want string, cleanup func(), ok bool, err error) {
	cleanup = func() {}
	name := chain[len(chain)-1]
	merged, err := config.MergeProfileChain(layout, bakedIn, chain)
	if err != nil {
		return "", "", cleanup, false, err
	}
	custom, err := config.ResolveCustomBaseBuild(merged)
	if err != nil {
		return "", "", cleanup, false, err
	}
	profileDir := layout.ProfileDir(name)
	if custom == nil {
		if !config.ProfileHasDockerfile(layout, name) {
			return "", "", cleanup, false, nil
		}
		return profileDir, chainChecksum(profileDir, parentChecksum), cleanup, true, nil
	}
	dir, assembled, done, err := customBaseContext(profileDir, custom)
	if err != nil {
		return "", "", cleanup, false, err
	}
	return dir, chainChecksumOf(assembled, parentChecksum), done, true, nil
}

// customBaseContext writes the assembled build context for a profile whose
// config selects a custom base (base:/agents:) into a fresh temp directory, and
// returns it with the assembled Dockerfile and a cleanup.
//
// The assembled Dockerfile is what gets hashed, not the profile's on-disk one:
// it folds in the base ref, the agent installs and the runtime layer, none of
// which the on-disk file mentions — and config.yaml, where base: and agents:
// live, is not part of any build context. Hashing the file on disk would let an
// edit to either key silently not rebuild.
//
// The base is hashed by its ref string, not the digest it resolved to, so a
// moving tag is not followed until --rebuild (DF237).
//
// Building from a separate directory keeps the backends out of it entirely: the
// result is an ordinary profile directory, so every image backend builds a
// custom base through its unchanged BuildProfileImage.
func customBaseContext(profileDir string, custom *config.CustomBaseBuild) (dir string, assembled []byte, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "yoloai-custom-base-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("create custom-base build context: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	assembled, err = dockerrt.WriteCustomBaseContext(profileDir, custom, dir)
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return dir, assembled, cleanup, nil
}

// chainChecksum is the profile-image build checksum: this profile's Dockerfile,
// folded together with its parent's checksum.
//
// Chaining is what makes an ancestor's change reach a descendant. The scheme it
// replaces compared the *modification times* of two marker files, which asked
// "was the parent's bookkeeping touched more recently than mine" — a question
// about the filesystem rather than about the images. Folding the parent's value
// in means a parent rebuild changes the child's expected value by construction,
// at any depth, with nothing to keep in sync.
//
// Returns "" when the Dockerfile cannot be read, which the caller treats as
// "cannot vouch" and therefore builds.
func chainChecksum(profileDir, parentChecksum string) string {
	data, err := os.ReadFile(filepath.Join(profileDir, "Dockerfile")) //nolint:gosec // G304: profileDir comes from profile resolution
	if err != nil {
		return ""
	}
	return chainChecksumOf(data, parentChecksum)
}

// chainChecksumOf is chainChecksum over Dockerfile bytes already in hand — for a
// custom base, the assembled Dockerfile, which exists nowhere on disk until
// customBaseContext writes it.
func chainChecksumOf(data []byte, parentChecksum string) string {
	h := sha256.New()
	h.Write([]byte("Dockerfile"))
	h.Write(data)
	h.Write([]byte(parentChecksum))
	return hex.EncodeToString(h.Sum(nil))
}

// imageMatches reports whether the store already holds tag built from want.
//
// Every "no" answer — absent image, no label, unreadable — is the same answer:
// we cannot vouch for it, so build. That collapses what used to be three
// separate questions (is the marker there, does it match, does the image still
// exist) into one, because the label cannot outlive the image it is on.
func imageMatches(ctx context.Context, builder runtime.ProfileImageBuilder, tag, want string) bool {
	if want == "" {
		return false
	}
	labels, ok := builder.ImageLabels(ctx, tag)
	return ok && labels[runtime.ProfileChecksumLabel] == want
}

// AutoBuildSecrets detects well-known credential files on the host and
// returns Docker BuildKit --secret specs for them. Returns nil if nothing
// is detected.
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
func AutoBuildSecrets(homeDir string) []string {
	npmrcPath := config.ExpandTilde("~/.npmrc", homeDir)
	if _, err := os.Stat(npmrcPath); err == nil {
		return []string{"id=npmrc,src=" + npmrcPath}
	}
	return nil
}

// ValidateBuildSecret validates a Docker BuildKit --secret spec string.
// The expected format is "id=<name>,src=<path>". Tilde expansion is applied
// to the src= value. Returns the expanded spec or an error.
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
func ValidateBuildSecret(spec, homeDir string) (string, error) {
	parts := strings.Split(spec, ",")

	var id, src string
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "id="):
			id = strings.TrimPrefix(p, "id=")
		case strings.HasPrefix(p, "src="):
			src = strings.TrimPrefix(p, "src=")
		}
	}

	if id == "" {
		return "", fmt.Errorf("build secret %q: missing id= field", spec)
	}
	if src == "" {
		return "", fmt.Errorf("build secret %q: missing src= field", spec)
	}

	expanded := config.ExpandTilde(src, homeDir)
	if _, err := os.Stat(expanded); err != nil {
		return "", fmt.Errorf("build secret %q: source file not found: %s", spec, expanded)
	}

	return "id=" + id + ",src=" + expanded, nil
}
