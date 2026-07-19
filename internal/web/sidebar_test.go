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

// TestSidebarDeepSeekLoginDetectsReLoginByGeneration proves the
// DeepSeek login completion poll keys off the TARGET account's
// per-account GENERATION, not a token fingerprint or a global config
// revision. DeepSeek may return the same long-lived token on a re-login
// (Cookie/WebStore refreshed), so a fingerprint would not change and
// the poll would wait 5 minutes without refreshing. Generation bumps
// on every successful login save regardless of token value, and is
// untouched by window-size / other-provider saves. The login must not
// start when the accounts endpoint reports success=false (no baseline
// could be established). The login fetch catch must surface an error,
// not fall back to a poll with an empty baseline.
func TestSidebarDeepSeekLoginDetectsReLoginByGeneration(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "generation") {
		t.Fatal("DeepSeek login must capture and compare a per-account generation to detect re-login")
	}
	if !strings.Contains(string(html), "preGeneration") {
		t.Fatal("DeepSeek login poll must hold a pre-login generation and wait for it to change")
	}
	if strings.Contains(string(html), "dsLoginPoll(name, 0)") {
		t.Fatal("DeepSeek login fetch catch must surface an error, not fall back to dsLoginPoll(name, 0)")
	}
	if strings.Contains(string(html), "dsLoginPoll(name,0)") {
		t.Fatal("DeepSeek login fetch catch must surface an error, not fall back to dsLoginPoll(name,0)")
	}
	if !strings.Contains(string(html), ".success") {
		t.Fatal("DeepSeek login must check the login response success flag (handle spawn failure)")
	}
	if !strings.Contains(string(html), "账户状态不可用，登录未启动") {
		t.Fatal("DeepSeek login must NOT start when /api/deepseek/accounts reports success=false")
	}
}
