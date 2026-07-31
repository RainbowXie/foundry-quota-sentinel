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

// TestSidebarOpenPageHandlesResponse proves the "open account page"
// action does not fire-and-forget /api/open. A spawn failure (or a
// future subprocess-exit signal) must be surfaced to the user; the
// previous code ignored the response entirely, so an OpenCode page
// that failed before the browser launched (e.g. a rejected cookie)
// showed "no reaction". The handler must read the response and surface
// success=false.
func TestSidebarOpenPageHandlesResponse(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	// The open-page fetch must inspect the JSON response, not just fire.
	if !strings.Contains(string(html), "openPage") {
		t.Fatal("open-account-page action must route through a named openPage handler that checks the response")
	}
	if !strings.Contains(string(html), "openPageError") {
		t.Fatal("open-account-page must surface a spawn failure via an openPageError path")
	}
}

// TestSidebarRendersKimiCardsAndAddon (task 5.5, updated by
// fix-kimi-card-layout) proves the sidebar HTML has a Kimi cards container,
// a kcard renderer using the shared Rolling/Weekly/Monthly labels (the old
// provider-specific Chinese metric labels are gone from the renderer), an
// add-on (购买加油包) action, and a Kimi provider option in the add-account
// modal.
func TestSidebarRendersKimiCardsAndAddon(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`id="kimiCards"`,
		`function kcard`,
		`krow("Rolling", d.five_hour`,
		`krow("Weekly", d.seven_day`,
		`kimiAddon`,
		`购买加油包`,
		`data-type="kimi"`,
		`kimiDoLogin`,
		`/api/kimi`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("sidebar HTML missing %q (Kimi card/addon/modal wiring)", want)
		}
	}
	for _, stale := range []string{`总使用量`, `5 小时用量`, `7 天用量`} {
		if strings.Contains(s, stale) {
			t.Fatalf("Kimi renderer must not keep provider-specific label %q (shared Rolling/Weekly/Monthly vocabulary)", stale)
		}
	}
	// The account/details action opens the membership quota page (kimi
	// provider), not a separate purchase route; no purchase is automated.
	if !strings.Contains(s, `openPage("kimi"`) {
		t.Fatal("购买加油包 must route through openPage(\"kimi\", ...) (membership page) without purchasing")
	}
}

// TestSidebarKimiLoginPollsGeneration (task 5.5) proves the Kimi login
// completion poll keys off the per-account generation (like DeepSeek), not a
// fixed timeout.
func TestSidebarKimiLoginPollsGeneration(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !strings.Contains(s, "kimiLoginPoll") {
		t.Fatal("Kimi login must use kimiLoginPoll to observe config completion")
	}
	if !strings.Contains(s, "/api/kimi/accounts") {
		t.Fatal("Kimi login must poll the fast /api/kimi/accounts endpoint")
	}
	if strings.Contains(s, "setTimeout(fk, 1500)") {
		t.Fatal("Kimi login must poll /api/kimi/accounts, not guess with setTimeout(fk, 1500)")
	}
}
