package sidebar

import (
	"strings"
	"testing"
)

func TestOllamaUserAgentUsesWebKitCompatibleSafariProfile(t *testing.T) {
	if !strings.Contains(ollamaUserAgent, "Version/") || !strings.Contains(ollamaUserAgent, "Safari/") {
		t.Fatalf("Ollama user agent must select the WebKit-compatible Safari path: %q", ollamaUserAgent)
	}
	if strings.Contains(ollamaUserAgent, "Chrome/") {
		t.Fatalf("Ollama user agent must not claim Chromium capabilities: %q", ollamaUserAgent)
	}
}
