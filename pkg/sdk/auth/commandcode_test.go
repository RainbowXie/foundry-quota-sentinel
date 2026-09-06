package auth

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
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

func TestCommandCodeUserNameExtraction(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://commandcode.ai/RainbowXie/settings/usage", "RainbowXie"},
		{"https://commandcode.ai/user-123/settings/usage", "user-123"},
		{"https://commandcode.ai/signin", ""},
		{"https://commandcode.ai/settings", ""},
		{"https://evil.com/RainbowXie/settings/usage", ""},
	}
	for _, tt := range tests {
		got := commandCodeUserName(tt.url)
		if got != tt.want {
			t.Errorf("commandCodeUserName(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestCommandCodeUserNameFromSession(t *testing.T) {
	payload := `{"session":{"user":{"userName":"RainbowXie"}}}`
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	header := "__Secure-commandcode_prod_.session_token=abc; __Secure-commandcode_prod_.session_data=" + encoded

	got := commandCodeUserNameFromSession(header)
	if got != "RainbowXie" {
		t.Errorf("commandCodeUserNameFromSession = %q, want RainbowXie", got)
	}

	badHeader := "__Secure-commandcode_prod_.session_data=not-base64"
	if got := commandCodeUserNameFromSession(badHeader); got != "" {
		t.Errorf("expected empty for malformed base64, got %q", got)
	}
}

func TestCommandCodeSavedCookiesParsing(t *testing.T) {
	header := "__Secure-commandcode_prod_.session_token=tok1; __Secure-commandcode_prod_.session_data=data1"
	cookies, err := commandCodeSavedCookies(header)
	if err != nil {
		t.Fatalf("commandCodeSavedCookies failed: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("len(cookies) = %d, want 2", len(cookies))
	}
	if cookies[0].Domain != commandCodeApexHost {
		t.Errorf("domain = %q, want %q", cookies[0].Domain, commandCodeApexHost)
	}

	emptyHeader := ""
	if _, err := commandCodeSavedCookies(emptyHeader); err == nil {
		t.Error("expected error for empty header")
	}
}

func TestRunCommandCodeLoginHappyPath(t *testing.T) {
	cdp := &fakeCommandCodeCDP{
		cookieHeader: "__Secure-commandcode_prod_.session_token=tok; __Secure-commandcode_prod_.session_data=dat",
		pageURL:      "https://commandcode.ai/RainbowXie/settings/usage",
	}
	browser := &fakeCommandCodeBrowser{cdp: cdp}

	var validatedCookie, validatedUser string
	validate := func(cookie, user string) bool {
		validatedCookie, validatedUser = cookie, user
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cookie, user, err := runCommandCodeLogin(ctx, browser, validate)
	if err != nil {
		t.Fatalf("runCommandCodeLogin failed: %v", err)
	}
	if !browser.closed {
		t.Error("browser was not closed after login")
	}
	if user != "RainbowXie" {
		t.Errorf("user = %q, want RainbowXie", user)
	}
	if !strings.Contains(cookie, "session_token=tok") {
		t.Errorf("cookie = %q, want session_token=tok", cookie)
	}
	if validatedUser != "RainbowXie" || validatedCookie != cookie {
		t.Error("validator was not called with captured credentials")
	}
}
