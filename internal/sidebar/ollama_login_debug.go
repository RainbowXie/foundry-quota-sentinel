package sidebar

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ollamaCookieMeta struct {
	Name        string
	ValueLength int
}

func redactedOllamaCookieSummary(cookies []ollamaCookieMeta) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		parts = append(parts, fmt.Sprintf("%s(len=%d)", cookie.Name, cookie.ValueLength))
	}
	return strings.Join(parts, ",")
}

func logOllamaLogin(format string, args ...any) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".foundry-quota-sentinel")
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "ollama-login-debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
