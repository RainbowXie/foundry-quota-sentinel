package web

import (
	"strings"
	"testing"
)

func TestSidebarDoesNotRenderProviderEmptyStateCards(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "未配置账户") {
		t.Fatal("provider-specific empty state cards hide the account-add flow")
	}
}

// TestSidebarDeepSeekLoginDoesNotGuessWithFixedTimeout proves the
// DeepSeek login flow observes config completion via dsLoginPoll
// instead of guessing the login is done after a fixed setTimeout(fd,
// 1500). A fixed delay either fires too early (account not saved yet
// → no card) or too late (card appears long after the browser
// closed), which is the "card appears too slowly" symptom.
func TestSidebarDeepSeekLoginDoesNotGuessWithFixedTimeout(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "setTimeout(fd, 1500)") {
		t.Fatal("DeepSeek login must poll /api/deepseek/accounts, not guess with setTimeout(fd, 1500)")
	}
	if !strings.Contains(string(html), "dsLoginPoll") {
		t.Fatal("DeepSeek login must use dsLoginPoll to observe config completion")
	}
	if !strings.Contains(string(html), "/api/deepseek/accounts") {
		t.Fatal("DeepSeek login must poll the fast /api/deepseek/accounts endpoint")
	}
}
