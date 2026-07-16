package sidebar

import (
	"strings"
	"testing"
)

func TestOllamaLoginBootstrapStartsAtHomepageThenSignsInOnce(t *testing.T) {
	js := ollamaLoginBootstrapJS()
	for _, want := range []string{"location.pathname !== \"/\"", "sessionStorage", "location.replace(\"/signin\")"} {
		if !strings.Contains(js, want) {
			t.Fatalf("bootstrap script missing %q: %s", want, js)
		}
	}
}
