package sidebar

import (
	"context"
	"reflect"
	"testing"
)

type fakeOllamaCDP struct {
	browser *fakeOllamaBrowser
	cookies []cdpCookie
}

func (c *fakeOllamaCDP) Cookies(context.Context) ([]cdpCookie, error) {
	return c.cookies, nil
}

func (c *fakeOllamaCDP) Close() error { return nil }

func (c *fakeOllamaCDP) SetSessionCookie(context.Context, string) error {
	c.browser.operations = append(c.browser.operations, "set-cookie")
	return nil
}

func (c *fakeOllamaCDP) Navigate(context.Context, string) error {
	c.browser.operations = append(c.browser.operations, "navigate")
	return nil
}

type fakeOllamaBrowser struct {
	cdp        *fakeOllamaCDP
	exited     bool
	closed     bool
	operations []string
}

func (b *fakeOllamaBrowser) CDP(context.Context) (ollamaCDP, error) { return b.cdp, nil }
func (b *fakeOllamaBrowser) Exited() bool                           { return b.exited }
func (b *fakeOllamaBrowser) Close() error {
	b.closed = true
	return nil
}
func (b *fakeOllamaBrowser) Wait() error {
	b.operations = append(b.operations, "wait")
	return nil
}

func newFakeOllamaBrowser(cookies []cdpCookie) *fakeOllamaBrowser {
	browser := &fakeOllamaBrowser{}
	browser.cdp = &fakeOllamaCDP{browser: browser, cookies: cookies}
	return browser
}

func TestRunOllamaLoginReturnsOnlyValidatedSessionAndClosesBrowser(t *testing.T) {
	browser := newFakeOllamaBrowser([]cdpCookie{{
		Name: "__Secure-session", Value: "good", Domain: "ollama.com", Secure: true, HTTPOnly: true,
	}})

	got, err := runOllamaLogin(context.Background(), browser, func(cookie string) bool {
		return cookie == "__Secure-session=good"
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "__Secure-session=good" {
		t.Fatalf("cookie = %q, want validated session", got)
	}
	if !browser.closed {
		t.Fatal("browser was not closed")
	}
}

func TestRunOllamaPageInjectsSessionBeforeNavigation(t *testing.T) {
	browser := newFakeOllamaBrowser(nil)

	err := runOllamaPage(context.Background(), browser, "https://ollama.com/settings", "__Secure-session=saved")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"set-cookie", "navigate", "wait"}
	if !reflect.DeepEqual(browser.operations, want) {
		t.Fatalf("operations = %#v, want %#v", browser.operations, want)
	}
}
