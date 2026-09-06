package auth

import (
	"context"
	"testing"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

type fakeOpenCodeBrowser struct {
	cdp        *fakeOpenCodeCDP
	exited     bool
	closed     bool
	onClose    func()
	operations []string
}

func (b *fakeOpenCodeBrowser) CDP(context.Context) (openCodeCDP, error) { return b.cdp, nil }
func (b *fakeOpenCodeBrowser) Exited() bool                             { return b.exited }
func (b *fakeOpenCodeBrowser) Wait() error {
	b.operations = append(b.operations, "wait")
	return nil
}
func (b *fakeOpenCodeBrowser) Close() error {
	b.closed = true
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

type fakeOpenCodeCDP struct {
	browser      *fakeOpenCodeBrowser
	cookieHeader string
	workspaceID  string
	pageURL      string
	closed       bool
}

func (c *fakeOpenCodeCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	if c.cookieHeader == "" {
		return nil, nil
	}
	return openCodeSavedCookies(c.cookieHeader)
}
func (c *fakeOpenCodeCDP) PageURL(context.Context, ...string) (string, error) {
	if c.workspaceID == "" {
		return "https://auth.opencode.ai/authorize", nil
	}
	return "https://opencode.ai/workspace/" + c.workspaceID + "/go", nil
}
func (c *fakeOpenCodeCDP) SetCookies(_ context.Context, _ []browserauth.Cookie) error {
	c.browser.operations = append(c.browser.operations, "set-cookie")
	return nil
}
func (c *fakeOpenCodeCDP) Navigate(context.Context, string) error {
	c.browser.operations = append(c.browser.operations, "navigate")
	return nil
}
func (c *fakeOpenCodeCDP) Close() error {
	c.closed = true
	return nil
}

func newFakeOpenCodeBrowser(cookieHeader, workspaceID string, onClose func()) *fakeOpenCodeBrowser {
	browser := &fakeOpenCodeBrowser{
		onClose: onClose,
	}
	browser.cdp = &fakeOpenCodeCDP{
		browser:      browser,
		cookieHeader: cookieHeader,
		workspaceID:  workspaceID,
	}
	return browser
}

func TestOpenCodeCookieHeaderKeepsOnlyMainDomain(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "session", Value: "good", Domain: ".opencode.ai", Secure: true, HTTPOnly: true},
		{Name: "oauth", Value: "skip", Domain: "auth.opencode.ai", Secure: true, HTTPOnly: true},
	}
	if got := openCodeCookieHeader(cookies); got != "session=good" {
		t.Fatalf("header=%q", got)
	}
}

func TestOpenCodeWorkspaceIDFromURL(t *testing.T) {
	if got := openCodeWorkspaceID("https://opencode.ai/workspace/wrk_abc123/go"); got != "wrk_abc123" {
		t.Fatalf("workspace=%q", got)
	}
}

func TestOpenCodeWorkspaceIDIgnoresAuthSubdomain(t *testing.T) {
	if got := openCodeWorkspaceID("https://auth.opencode.ai/wrk_fake123"); got != "" {
		t.Fatalf("workspace=%q", got)
	}
}

func TestRunOpenCodeLoginValidatesAfterClose(t *testing.T) {
	closed := false
	browser := newFakeOpenCodeBrowser("session=good", "wrk_abc123", func() { closed = true })
	cookie, wsid, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("browser was not closed before returning")
	}
	if cookie == "" || wsid == "" {
		t.Fatalf("credentials = (%q, %q)", cookie, wsid)
	}
}

func TestRunOpenCodeLoginRejectsEmptyCredentials(t *testing.T) {
	browser := newFakeOpenCodeBrowser("", "", func() {})
	browser.exited = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := runOpenCodeLogin(ctx, browser, func(string, string) bool { return true })
	if err == nil {
		t.Fatal("expected error when no workspace is observed")
	}
}
