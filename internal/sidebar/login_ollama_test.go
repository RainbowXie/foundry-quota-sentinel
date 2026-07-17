package sidebar

import (
	"context"
	"reflect"
	"testing"
)

type fakeOllamaCDP struct {
	browser   *fakeOllamaBrowser
	cookies   []cdpCookie
	batches   [][]cdpCookie
	reads     int
	userAgent string
}

func (c *fakeOllamaCDP) Cookies(context.Context) ([]cdpCookie, error) {
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

func (c *fakeOllamaCDP) UserAgent(context.Context) (string, error) { return c.userAgent, nil }

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
	cdpCalls   int
	exited     bool
	closed     bool
	operations []string
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

func newFakeOllamaBrowser(cookies []cdpCookie) *fakeOllamaBrowser {
	browser := &fakeOllamaBrowser{}
	browser.cdp = &fakeOllamaCDP{browser: browser, cookies: cookies, userAgent: "test-browser"}
	return browser
}

func TestRunOllamaLoginReturnsCapturedCredentialsWithoutValidationCallback(t *testing.T) {
	browser := newFakeOllamaBrowser([]cdpCookie{{
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
	browser.cdp.batches = [][]cdpCookie{
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

	err := runOllamaPage(context.Background(), browser, "https://ollama.com/settings", "__Secure-session=saved")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"set-cookie", "navigate", "wait"}
	if !reflect.DeepEqual(browser.operations, want) {
		t.Fatalf("operations = %#v, want %#v", browser.operations, want)
	}
}
