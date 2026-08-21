package sidebar

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"foundry-quota-sentinel/internal/browserauth"
)

type fakeCommandCodeBrowser struct {
	cdp     *fakeCommandCodeCDP
	exited  bool
	closed  bool
	onClose func()
}

func (b *fakeCommandCodeBrowser) CDP(context.Context) (commandCodeCDP, error) { return b.cdp, nil }
func (b *fakeCommandCodeBrowser) Exited() bool                                { return b.exited }
func (b *fakeCommandCodeBrowser) Wait() error                                 { return nil }
func (b *fakeCommandCodeBrowser) Close() error {
	b.closed = true
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

type fakeCommandCodeCDP struct {
	cookieHeader string
	pageURL      string
	closed       bool
}

func (c *fakeCommandCodeCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	if c.cookieHeader == "" {
		return nil, nil
	}
	return commandCodeSavedCookies(c.cookieHeader)
}
func (c *fakeCommandCodeCDP) PageURL(context.Context, ...string) (string, error) {
	if c.pageURL == "" {
		return "https://commandcode.ai/signin", nil
	}
	return c.pageURL, nil
}
func (c *fakeCommandCodeCDP) SetCookies(context.Context, []browserauth.Cookie) error { return nil }
func (c *fakeCommandCodeCDP) Navigate(context.Context, string) error                { return nil }
func (c *fakeCommandCodeCDP) Close() error {
	c.closed = true
	return nil
}

const fakeCommandCodeCookie = "__Secure-commandcode_prod_.session_token=tok123; __Secure-commandcode_prod_.session_data=data456"

func TestRunCommandCodeLoginCaptures(t *testing.T) {
	cdp := &fakeCommandCodeCDP{cookieHeader: fakeCommandCodeCookie, pageURL: "https://commandcode.ai/RainbowXie/settings/usage"}
	browser := &fakeCommandCodeBrowser{cdp: cdp}
	var gotUser string
	validated := false
	cookie, user, err := runCommandCodeLogin(context.Background(), browser, func(c, u string) bool {
		gotUser = u
		validated = true
		return true
	})
	if err != nil {
		t.Fatalf("runCommandCodeLogin: %v", err)
	}
	if !browser.closed {
		t.Error("browser not closed after login")
	}
	if !validated {
		t.Error("validator not invoked")
	}
	if cookie != fakeCommandCodeCookie {
		t.Errorf("cookie = %q, want %q", cookie, fakeCommandCodeCookie)
	}
	if gotUser != "RainbowXie" {
		t.Errorf("userName = %q, want RainbowXie", gotUser)
	}
	if user != "RainbowXie" {
		t.Errorf("returned userName = %q, want RainbowXie", user)
	}
}

func TestRunCommandCodeLoginValidationFails(t *testing.T) {
	cdp := &fakeCommandCodeCDP{cookieHeader: fakeCommandCodeCookie, pageURL: "https://commandcode.ai/RainbowXie/settings/usage"}
	browser := &fakeCommandCodeBrowser{cdp: cdp}
	_, _, err := runCommandCodeLogin(context.Background(), browser, func(c, u string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "验证失败") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestRunCommandCodeLoginBrowserExited(t *testing.T) {
	cdp := &fakeCommandCodeCDP{cookieHeader: "", pageURL: "https://commandcode.ai/signin"}
	browser := &fakeCommandCodeBrowser{cdp: cdp, exited: true}
	_, _, err := runCommandCodeLogin(context.Background(), browser, func(c, u string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "窗口已关闭") {
		t.Fatalf("expected exited error, got %v", err)
	}
}

func TestCommandCodeUserName(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://commandcode.ai/RainbowXie/settings/usage", "RainbowXie"},
		{"https://commandcode.ai/signin", ""},
		{"https://commandcode.ai/studio", ""},
		{"https://api.commandcode.ai/internal/usage", ""},
		{"https://commandcode.ai/foo/settings/billing", "foo"},
		{"https://evil.example.com/RainbowXie/settings/usage", ""},
		{"https://commandcode.ai/", ""},
	}
	for _, c := range cases {
		if got := commandCodeUserName(c.url); got != c.want {
			t.Errorf("commandCodeUserName(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestCommandCodeUserNameFromSession(t *testing.T) {
	// A real-shaped session_data payload (sanitized) carrying userName.
	// base64url(JSON with session.user.userName).
	importBase64 := func(s string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(s))
	}
	sessionJSON := `{"session":{"user":{"userName":"RainbowXie","name":"Eds"}},"expiresAt":"2026-08-27T03:26:52.220Z"}`
	sessionData := importBase64(sessionJSON)
	header := "__Secure-commandcode_prod_.session_token=tok; __Secure-commandcode_prod_.session_data=" + sessionData

	if got := commandCodeUserNameFromSession(header); got != "RainbowXie" {
		t.Errorf("commandCodeUserNameFromSession = %q, want RainbowXie", got)
	}

	// Missing userName -> empty.
	noUser := `{"session":{}}`
	header2 := "__Secure-commandcode_prod_.session_data=" + importBase64(noUser)
	if got := commandCodeUserNameFromSession(header2); got != "" {
		t.Errorf("no-user header = %q, want empty", got)
	}

	// Malformed base64 -> empty.
	header3 := "__Secure-commandcode_prod_.session_data=%%%invalid"
	if got := commandCodeUserNameFromSession(header3); got != "" {
		t.Errorf("malformed header = %q, want empty", got)
	}

	// No session_data cookie at all -> empty.
	if got := commandCodeUserNameFromSession("__Secure-commandcode_prod_.session_token=tok"); got != "" {
		t.Errorf("token-only header = %q, want empty", got)
	}
}

// TestRunCommandCodeLoginFromSessionWhenNotOnUsagePage proves the capture
// loop succeeds from the session_data cookie alone (user parked on
// /studio after OAuth, not the usage page).
func TestRunCommandCodeLoginFromSessionWhenNotOnUsagePage(t *testing.T) {
	sessionData := base64.RawURLEncoding.EncodeToString([]byte(
		`{"session":{"user":{"userName":"RainbowXie"}}}`))
	cookie := "__Secure-commandcode_prod_.session_token=tok; __Secure-commandcode_prod_.session_data=" + sessionData
	cdp := &fakeCommandCodeCDP{cookieHeader: cookie, pageURL: "https://commandcode.ai/studio"}
	browser := &fakeCommandCodeBrowser{cdp: cdp}
	gotCookie, gotUser, err := runCommandCodeLogin(context.Background(), browser, func(c, u string) bool { return true })
	if err != nil {
		t.Fatalf("runCommandCodeLogin: %v", err)
	}
	if gotUser != "RainbowXie" {
		t.Errorf("userName = %q, want RainbowXie (from session_data)", gotUser)
	}
	if gotCookie != cookie {
		t.Errorf("cookie mismatch")
	}
}

func TestCommandCodeSavedCookies(t *testing.T) {
	// Valid pair round-trips.
	cookies, err := commandCodeSavedCookies(fakeCommandCodeCookie)
	if err != nil {
		t.Fatalf("savedCookies: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2", len(cookies))
	}
	if cookies[0].Name != "__Secure-commandcode_prod_.session_token" || cookies[0].Domain != commandCodeHost {
		t.Errorf("cookie0 = %+v, want commandcode.ai domain", cookies[0])
	}

	// Empty is invalid.
	if _, err := commandCodeSavedCookies(""); err == nil {
		t.Error("empty header should be invalid")
	}
	// Duplicate names are invalid.
	if _, err := commandCodeSavedCookies("a=1; a=2"); err == nil {
		t.Error("duplicate names should be invalid")
	}
	// Dangerous chars are invalid.
	if _, err := commandCodeSavedCookies("a=1; b=va\"l"); err == nil {
		t.Error("quote in value should be invalid")
	}
}

func TestCommandCodeCookieHeaderFilters(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "__Secure-commandcode_prod_.session_token", Value: "tok", Domain: "commandcode.ai"},
		{Name: "__Secure-commandcode_prod_.session_data", Value: "data", Domain: "commandcode.ai"},
		{Name: "google", Value: "g", Domain: "google.com"},       // wrong domain -> dropped
		{Name: "bad", Value: "va\"l", Domain: "commandcode.ai"},  // unsafe -> dropped
	}
	header := commandCodeCookieHeader(cookies)
	if !strings.Contains(header, "session_token=tok") {
		t.Errorf("header missing session_token: %q", header)
	}
	if !strings.Contains(header, "session_data=data") {
		t.Errorf("header missing session_data: %q", header)
	}
	if strings.Contains(header, "google=") {
		t.Errorf("header leaked cross-domain cookie: %q", header)
	}
	if strings.Contains(header, "bad=") {
		t.Errorf("header kept unsafe cookie: %q", header)
	}
}

func TestValidateCommandCodePageURL(t *testing.T) {
	if err := validateCommandCodePageURL("https://commandcode.ai/RainbowXie/settings/usage"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
	if err := validateCommandCodePageURL("http://commandcode.ai/x"); err == nil {
		t.Error("http URL accepted")
	}
	if err := validateCommandCodePageURL("https://evil.example.com/x"); err == nil {
		t.Error("foreign host accepted")
	}
}
