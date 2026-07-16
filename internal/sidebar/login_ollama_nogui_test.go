//go:build nogui

package sidebar

import (
	"strings"
	"testing"
)

func TestRunOllamaLoginWithoutGUIExplainsLimitation(t *testing.T) {
	_, err := RunOllamaLogin(func(string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "图形界面") {
		t.Fatalf("RunOllamaLogin() error = %v, want GUI limitation", err)
	}
}

func TestRunOllamaPageWithoutGUIExplainsLimitation(t *testing.T) {
	err := RunOllamaPage("https://ollama.com/settings", "session=value")
	if err == nil || !strings.Contains(err.Error(), "图形界面") {
		t.Fatalf("RunOllamaPage() error = %v, want GUI limitation", err)
	}
}
