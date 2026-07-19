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

// TestSidebarDeepSeekLoginDetectsReLoginByFingerprint proves the
// DeepSeek login completion poll keys off the TARGET account's
// per-account fingerprint, not a global config revision. A global
// revision (file mtime) flips on any config save — window size,
// another provider — so a re-login for an existing account falsely
// completes on the first poll. The fingerprint is scoped to this
// account's credential, so only a real save of THIS account changes
// it. The login fetch catch must surface an error, not fall back to
// dsLoginPoll(name, 0) (which reintroduces the false-completion).
func TestSidebarDeepSeekLoginDetectsReLoginByFingerprint(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "fingerprint") {
		t.Fatal("DeepSeek login must capture and compare a per-account fingerprint to detect re-login")
	}
	if !strings.Contains(string(html), "preFingerprint") {
		t.Fatal("DeepSeek login poll must hold a pre-login fingerprint and wait for it to change")
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
}
