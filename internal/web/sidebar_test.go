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

// TestSidebarDeepSeekLoginDetectsReLoginByRevision proves the
// DeepSeek login completion poll keys off a config REVISION change,
// not just account-name presence. Without revision-aware polling, a
// re-login for an account that already exists stops on the first poll
// (name already in config) and uses the stale token; the rotated
// credential only shows up on the 30s interval. The login response
// must carry a pre-login revision and the poll must wait for a
// different revision. It must also handle a success=false login
// response instead of polling forever.
func TestSidebarDeepSeekLoginDetectsReLoginByRevision(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "revision") {
		t.Fatal("DeepSeek login must capture and compare a config revision to detect re-login")
	}
	if !strings.Contains(string(html), "preRev") {
		t.Fatal("DeepSeek login poll must hold a pre-login revision and wait for it to change")
	}
	if !strings.Contains(string(html), ".success") {
		t.Fatal("DeepSeek login must check the login response success flag (handle spawn failure)")
	}
}
