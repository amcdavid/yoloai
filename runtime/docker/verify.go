// ABOUTME: Runs the imagecontract probe inside a throwaway container so `yoloai
// ABOUTME: system verify-image` can report what a candidate base image is missing.

package docker

import (
	"context"

	"github.com/kstenerud/yoloai/internal/imagecontract"
	"github.com/kstenerud/yoloai/runtime"
)

// VerifyImage checks imageRef against reqs by running the contract probe in a
// throwaway container. Implements runtime.ImageVerifier for docker and podman
// (both use this Runtime); the run semantics live in runtime.RunImageProbe,
// shared with the apple backend.
func (r *Runtime) VerifyImage(ctx context.Context, imageRef string, reqs []imagecontract.Requirement) ([]imagecontract.ProbeResult, error) {
	return runtime.RunImageProbe(ctx, r.binaryName, r.execEnv, imageRef, reqs)
}
