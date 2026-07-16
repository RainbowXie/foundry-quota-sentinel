package sidebar

import "testing"

func TestOllamaLoginCompleteURL(t *testing.T) {
	for _, raw := range []string{
		"https://ollama.com/auth/callback?code=example",
		"https://ollama.com/settings",
	} {
		if !isOllamaLoginCompleteURL(raw) {
			t.Fatalf("isOllamaLoginCompleteURL(%q) = false", raw)
		}
	}
	for _, raw := range []string{
		"https://ollama.com/",
		"https://signin.ollama.com/",
		"https://ollama.com/signin",
	} {
		if isOllamaLoginCompleteURL(raw) {
			t.Fatalf("isOllamaLoginCompleteURL(%q) = true", raw)
		}
	}
}
