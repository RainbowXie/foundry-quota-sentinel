package sidebar

import "net/url"

func isOllamaLoginCompleteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() == "ollama.com" && (u.Path == "/auth/callback" || u.Path == "/settings")
}
