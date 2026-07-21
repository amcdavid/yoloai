// ABOUTME: Composes a buildable Dockerfile from a stack half plus yoloAI's runtime
// ABOUTME: layer, so the layer can be applied to yoloai-base and foreign bases alike.

package docker

import "bytes"

// composeSeparator introduces the appended layer in the composed output. Build
// logs and `docker history` show the composed file, not its two sources, so the
// boundary is marked to keep a reader from hunting for a `runtime-layer` file
// that never reaches the daemon.
const composeSeparator = `
# ============================================================================
# yoloAI runtime layer — appended from runtime/docker/resources/runtime-layer.Dockerfile.
# Do not edit here; edit that file. Everything below establishes
# internal/imagecontract.Static().
# ============================================================================
`

// ComposeDockerfile returns stack with yoloAI's runtime layer appended, yielding
// a complete, buildable Dockerfile.
//
// The split exists so that "what stack is in this image" and "what yoloAI needs
// to drive it" are separately expressible. yoloai-base is the stack half plus
// this layer; a user-supplied base image is that image plus this same layer.
// Keeping one layer for both is what stops the foreign-base path from drifting
// into a second, subtly different definition of the runtime contract.
//
// stack is passed in rather than read from the embedded batteries so callers can
// compose against a different stack half — Phase 1's minimal image and Phase 2's
// user-declared bases both do.
//
// The layer is appended verbatim, so it must contain no FROM: a second FROM
// would start a new build stage and silently discard everything the stack half
// established. buildable() in the tests asserts that.
func ComposeDockerfile(stack []byte) []byte {
	var b bytes.Buffer
	b.Grow(len(stack) + len(composeSeparator) + len(embeddedRuntimeLayer) + 2)
	b.Write(bytes.TrimRight(stack, "\n"))
	b.WriteString("\n")
	b.WriteString(composeSeparator)
	b.Write(embeddedRuntimeLayer)
	if !bytes.HasSuffix(embeddedRuntimeLayer, []byte("\n")) {
		b.WriteString("\n")
	}
	return b.Bytes()
}
