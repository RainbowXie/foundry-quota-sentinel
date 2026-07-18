package sidebar

import (
	"context"
	"reflect"
	"testing"

	"foundry-quota-sentinel/internal/browserauth"
)

type fakeOllamaCDP struct {
	browser   *fakeOllamaBrowser
	cookies   []browserauth.Cookie
	batches   [][]browserauth.Cookie
	reads     int
	userAgent string
}

func (c *fakeOllamaCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	if len(c.batches) > 0 {
		index := c.reads
		c.reads++
		if index < len(c.batches) {
			return c.batches[index], nil
		}
		return c.batches[len(c.batches)-1], nil
	}
	return c.cookies, nil
}

func (c *fakeOllamaCDP) BrowserUserAgent(context.Context) (string, error) { return c.userAgent, nil }

func (c *fakeOllamaCDP) Close() error { return nil }

func (c *fakeOllamaCDP) SetCookies(_ context.Context, cookies []browserauth.Cookie) error {
	c.browser.operations = append(c.browser.operations, "set-cookie")
	c.browser.savedCookies = cookies
	return nil
}

func (c *fakeOllamaCDP) SetUserAgent(context.Context, string) error {
	c.browser.operations = append(c.browser.operations, "set-user-agent")
	return nil
}

func (c *fakeOllamaCDP) Navigate(context.Context, string) error {
	c.browser.operations = append(c.browser.operations, "navigate")
	return nil
}

type fakeOllamaBrowser struct {
	cdp          *fakeOllamaCDP
	cdpCalls     int
	exited       bool
	closed       bool
	operations   []string
	savedCookies []browserauth.Cookie
}

func (b *fakeOllamaBrowser) CDP(context.Context) (ollamaCDP, error) {
	b.cdpCalls++
	return b.cdp, nil
}
func (b *fakeOllamaBrowser) Exited() bool { return b.exited }
func (b *fakeOllamaBrowser) Close() error {
	b.closed = true
	return nil
}
func (b *fakeOllamaBrowser) Wait() error {
	b.operations = append(b.operations, "wait")
	return nil
}

func newFakeOllamaBrowser(cookies []browserauth.Cookie) *fakeOllamaBrowser {
	browser := &fakeOllamaBrowser{}
	browser.cdp = &fakeOllamaCDP{browser: browser, cookies: cookies, userAgent: "test-browser"}
	return browser
}

func TestRunOllamaLoginReturnsCapturedCredentialsWithoutValidationCallback(t *testing.T) {
	browser := newFakeOllamaBrowser([]browserauth.Cookie{{
		Name: "__Secure-session", Value: "good", Domain: "ollama.com", Secure: true, HTTPOnly: true,
	}})

	got, err := runOllamaLogin(context.Background(), browser)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cookie != "__Secure-session=good" || got.UserAgent != "test-browser" {
		t.Fatalf("credentials = %#v, want captured session and browser user agent", got)
	}
	if !browser.closed {
		t.Fatal("browser was not closed")
	}
}

func TestRunOllamaLoginReconnectsAfterEmptyCookieSnapshot(t *testing.T) {
	browser := newFakeOllamaBrowser(nil)
	browser.cdp.batches = [][]browserauth.Cookie{
		nil,
		{{Name: "__Secure-session", Value: "good", Domain: "ollama.com", Secure: true, HTTPOnly: true}},
	}

	credentials, err := runOllamaLogin(context.Background(), browser)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Cookie != "__Secure-session=good" {
		t.Fatalf("credentials = %#v", credentials)
	}
	if browser.cdpCalls < 2 {
		t.Fatalf("CDP connections = %d, want a fresh connection after an empty snapshot", browser.cdpCalls)
	}
}

func TestRunOllamaPageInjectsSessionBeforeNavigation(t *testing.T) {
	browser := newFakeOllamaBrowser(nil)

	cookies, err := ollamaSavedCookies("__Secure-session=saved")
	if err != nil {
		t.Fatal(err)
	}
	if err := runOllamaPage(context.Background(), browser, "https://ollama.com/settings", cookies, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-cookie", "navigate", "wait"}
	if !reflect.DeepEqual(browser.operations, want) {
		t.Fatalf("operations = %#v, want %#v", browser.operations, want)
	}
}

func TestOllamaSessionCookieHeaderIncludesCloudflareCookies(t *testing.T) {
	got := ollamaSessionCookieHeader([]browserauth.Cookie{
		{Name: "aid", Value: "tracking", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "cf_clearance", Value: "clearance", Domain: ".ollama.com", Secure: true, HTTPOnly: true},
		{Name: "__Secure-session", Value: "session-value", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "__Secure-session", Value: "other", Domain: "example.com", Secure: true, HTTPOnly: true},
	})
	if got != "__Secure-session=session-value; cf_clearance=clearance; aid=tracking" {
		t.Fatalf("header = %q, want Ollama session and Cloudflare cookies", got)
	}
}

func TestOllamaSessionCookieHeaderRejectsUnsafeValue(t *testing.T) {
	got := ollamaSessionCookieHeader([]browserauth.Cookie{{
		Name:   "__Secure-session",
		Value:  "bad\r\nCookie: x",
		Domain: "ollama.com",
	}})
	if got != "" {
		t.Fatalf("header = %q, want empty unsafe cookie", got)
	}
}

func TestOllamaSessionCookieHeaderKeepsSessionWhenAncillaryCookieIsDuplicated(t *testing.T) {
	got := ollamaSessionCookieHeader([]browserauth.Cookie{
		{Name: "__Secure-session", Value: "session", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "aid", Value: "first", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "aid", Value: "second", Domain: "ollama.com", Secure: true, HTTPOnly: true},
	})
	if got != "__Secure-session=session; aid=first" {
		t.Fatalf("header = %q, want session despite duplicated ancillary cookie", got)
	}
}
