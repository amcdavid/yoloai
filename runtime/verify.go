// ABOUTME: Shared image-contract probe runner for OCI backends that verify an image
// ABOUTME: by running a throwaway container — docker/podman and apple `container`.

package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/imagecontract"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// RunImageProbe checks imageRef against reqs by running the contract probe in a
// throwaway container via `<bin> run`, and returns one result per requirement.
//
// It is the shared body of every ImageVerifier: docker, podman, and apple
// `container` all accept the same `run --rm -i --entrypoint sh <image> -s` shape
// and differ only in the binary and its env. Keeping one implementation here
// stops the run semantics — the entrypoint override, the stdin delivery, the
// partial-output tolerance — from drifting between backends.
//
// The probe is delivered on stdin, never baked into the image: the whole point
// is to check an image yoloAI did not build. --entrypoint sh overrides whatever
// ENTRYPOINT the image declares, so a yoloAI-built base runs the probe rather
// than its own entrypoint.sh.
func RunImageProbe(ctx context.Context, bin string, env []string, imageRef string, reqs []imagecontract.Requirement) ([]imagecontract.ProbeResult, error) {
	script := imagecontract.ProbeScript(reqs)

	args := []string{"run", "--rm", "-i", "--entrypoint", "sh", imageRef, "-s"}
	cmd := sysexec.CommandContext(ctx, env, bin, args...)
	cmd.Stdin = strings.NewReader(script)
	// Combined output: an image whose shell profile prints a banner to stderr
	// would otherwise leak to the terminal. ParseProbe discards every line
	// without the result prefix, so folding stderr in is safe.
	out, err := cmd.CombinedOutput()
	if err != nil && !imagecontract.HasResults(string(out)) {
		// The probe script always exits 0, so a non-zero exit with no parseable
		// output means the container never ran (bad ref, unrunnable image, no
		// shell) — a real error. If there IS output, trust it: a partial probe
		// beats a bare exit code, and absent lines already read as missing.
		return nil, fmt.Errorf("run probe in %s via %s: %w\n%s", imageRef, bin, err, strings.TrimSpace(string(out)))
	}
	return imagecontract.ParseProbe(reqs, string(out)), nil
}
