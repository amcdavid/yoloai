// ABOUTME: Tests for MinifyDockerfile — comment/blank stripping that keeps the
// ABOUTME: composed base under Apple container's 16 KB Dockerfile limit.

package docker

import (
	"strings"
	"testing"
)

func TestMinifyDockerfileStripsCommentsAndBlanks(t *testing.T) {
	in := "# a comment\nFROM debian\n\n   # indented comment\nRUN echo hi\n\n"
	got := string(MinifyDockerfile([]byte(in)))
	want := "FROM debian\nRUN echo hi\n"
	if got != want {
		t.Errorf("MinifyDockerfile:\n got %q\nwant %q", got, want)
	}
}

func TestMinifyDockerfileKeepsInlineHash(t *testing.T) {
	// A `#` inside a RUN command is shell, not a Dockerfile comment — must survive.
	in := "RUN echo '#notacomment' && ls\n"
	got := string(MinifyDockerfile([]byte(in)))
	if !strings.Contains(got, "#notacomment") {
		t.Errorf("inline hash stripped: %q", got)
	}
}

func TestMinifyDockerfileKeepsContinuations(t *testing.T) {
	in := "# lead\nRUN foo \\\n    && bar\n"
	got := string(MinifyDockerfile([]byte(in)))
	want := "RUN foo \\\n    && bar\n"
	if got != want {
		t.Errorf("continuation mangled:\n got %q\nwant %q", got, want)
	}
}

func TestMinifyComposedBaseUnderAppleLimit(t *testing.T) {
	// The real reason this exists: the composed base must fit Apple's 16 KB cap.
	composed := ComposeDockerfile(embeddedBatteries)
	if len(composed) <= 16384 {
		t.Skipf("composed base already under 16 KB (%d); minify still validated above", len(composed))
	}
	min := MinifyDockerfile(composed)
	if len(min) > 16384 {
		t.Errorf("minified composed base is %d bytes, still over Apple's 16384 limit", len(min))
	}
	// Minifying must not drop instructions: FROM and the ENTRYPOINT survive.
	if !strings.Contains(string(min), "FROM ") || !strings.Contains(string(min), "ENTRYPOINT") {
		t.Errorf("minify dropped a real instruction")
	}
}
