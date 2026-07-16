package sidebar

import (
	"strings"
	"testing"
)

func TestOllamaAuthCaptureScansStorageAndRequestHeaders(t *testing.T) {
	js := ollamaAuthCaptureJS()
	for _, want := range []string{"localStorage", "sessionStorage", "XMLHttpRequest", "window.fetch", "__ocgtOllamaCandidate"} {
		if !strings.Contains(js, want) {
			t.Fatalf("capture script missing %q", want)
		}
	}
}
