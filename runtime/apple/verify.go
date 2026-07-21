// ABOUTME: Apple `container` implementation of runtime.ImageVerifier — runs the
// ABOUTME: contract probe in a throwaway container so verify-image works on macOS.

package apple

import (
	"context"

	"github.com/kstenerud/yoloai/internal/imagecontract"
	"github.com/kstenerud/yoloai/runtime"
)

// VerifyImage checks imageRef against reqs by running the contract probe in a
// throwaway `container run`. Implements runtime.ImageVerifier; the run semantics
// are shared with the docker backend via runtime.RunImageProbe.
func (r *Runtime) VerifyImage(ctx context.Context, imageRef string, reqs []imagecontract.Requirement) ([]imagecontract.ProbeResult, error) {
	return runtime.RunImageProbe(ctx, r.containerBin, r.execEnv, imageRef, reqs)
}
