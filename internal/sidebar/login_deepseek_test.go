package sidebar

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"foundry-quota-sentinel/internal/browserauth"
)

type fakeDeepSeekBrowser struct {
	cdp     *fakeDeepSeekCDP
	exited  bool
	closed  bool
	onClose func()
}

func (b *fakeDeepSeekBrowser) CDP(context.Context) (deepSeekCDP, error) { return b.cdp, nil }
func (b *fakeDeepSeekBrowser) Exited() bool                             { return b.exited }
func (b *fakeDeepSeekBrowser) Wait() error                              { return nil }
func (b *fakeDeepSeekBrowser) Close() error {
	b.closed = true
	b.exited = true
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

type fakeDeepSeekCDP struct {
	pageURL        string
	events         chan browserauth.Event
	snapshotValue  string
	closed         bool
	setCookies     []browserauth.Cookie
	browserCookies []browserauth.Cookie
}

func (c *fakeDeepSeekCDP) EnableNetwork(context.Context) error { return nil }
func (c *fakeDeepSeekCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	return append([]browserauth.Cookie(nil), c.browserCookies...), nil
}
func (c *fakeDeepSeekCDP) PageURL(context.Context, ...string) (string, error) {
	return c.pageURL, nil
}
func (c *fakeDeepSeekCDP) Events() <-chan browserauth.Event { return c.events }
func (c *fakeDeepSeekCDP) Evaluate(context.Context, string) (json.RawMessage, error) {
	if c.snapshotValue == "" {
		return json.RawMessage(`{"result":{"value":"{\"l\":{\"k\":\"candidate_abcdefghijklmnopqrstuvwxyz\"},\"s\":{}}"}}`), nil
	}
	raw, err := json.Marshal(c.snapshotValue)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"result":{"value":` + string(raw) + `}}`), nil
}
func (c *fakeDeepSeekCDP) AddScriptOnNewDocument(context.Context, string) error { return nil }
func (c *fakeDeepSeekCDP) Navigate(context.Context, string, ...string) error    { return nil }
func (c *fakeDeepSeekCDP) SetCookies(_ context.Context, cookies []browserauth.Cookie) error {
	c.setCookies = append([]browserauth.Cookie(nil), cookies...)
	return nil
}
func (c *fakeDeepSeekCDP) Close() error {
	c.closed = true
	return nil
}

func newFakeDeepSeekBrowser(onClose func()) *fakeDeepSeekBrowser {
	return &fakeDeepSeekBrowser{
		cdp:     &fakeDeepSeekCDP{pageURL: "https://platform.deepseek.com/usage"},
		onClose: onClose,
	}
}

func TestDeepSeekBearerCandidateFromNetworkEvent(t *testing.T) {
	event := browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"headers":{"authorization":"Bearer valid.token-12345678901234567890"}}`),
	}
	token, _, _ := deepSeekTokenFromEvent(event)
	if token != "valid.token-12345678901234567890" {
		t.Fatalf("token=%q", token)
	}
}

func TestDeepSeekTokenFromEventReturnsURL(t *testing.T) {
	event := browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"url":"https://platform.deepseek.com/api/usage","headers":{"authorization":"Bearer t"}}`),
	}
	token, _, url := deepSeekTokenFromEvent(event)
	if token != "t" {
		t.Fatalf("token=%q", token)
	}
	if url != "https://platform.deepseek.com/api/usage" {
		t.Fatalf("url=%q", url)
	}
}

func TestDeepSeekTokenFromEventEmptyURLWhenAbsent(t *testing.T) {
	event := browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"headers":{"authorization":"Bearer t"}}`),
	}
	token, _, url := deepSeekTokenFromEvent(event)
	if token != "t" || url != "" {
		t.Fatalf("token=%q url=%q", token, url)
	}
}

func TestDeepSeekBearerCandidateIgnoresNonAuthHeaders(t *testing.T) {
	event := browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"headers":{"x-other":"Bearer nope"}}`),
	}
	if got, _, _ := deepSeekTokenFromEvent(event); got != "" {
		t.Fatalf("token=%q, want empty", got)
	}
}

func TestDeepSeekStorageSnapshotProducesCandidates(t *testing.T) {
	snapshot := `{"l":{"auth":"{\"token\":\"candidate_abcdefghijklmnopqrstuvwxyz\"}"},"s":{}}`
	got := deepSeekStorageCandidates(snapshot)
	if len(got) != 1 || got[0] != "candidate_abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("candidates=%v", got)
	}
}

func TestDeepSeekStorageSnapshotRejectsMalformed(t *testing.T) {
	if got := deepSeekStorageCandidates("not json"); got != nil {
		t.Fatalf("candidates=%v, want nil", got)
	}
}

// TestIsDeepSeekSnapshotValid proves the envelope-shape gate used by
// the coordinator. A non-empty string that does not parse as
// {"l":..., "s":...} must NOT be considered a valid snapshot.
func TestIsDeepSeekSnapshotValid(t *testing.T) {
	cases := []struct {
		snapshot string
		want     bool
	}{
		{`{"l":{},"s":{}}`, true},
		{`{"l":{"k":"v"},"s":{}}`, true},
		{`null`, false},
		{`{"l":{}}`, false},
		{`{"s":{}}`, false},
		{`{"l":"notmap","s":{}}`, false},
		{`not json`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isDeepSeekSnapshotValid(c.snapshot); got != c.want {
			t.Fatalf("isDeepSeekSnapshotValid(%q) = %v, want %v", c.snapshot, got, c.want)
		}
	}
}

// TestDeepSeekRecordsURLFromEmptyTokenEvent proves a
// requestWillBeSent without an Authorization header still records
// the requestId→URL mapping. Otherwise a later ExtraInfo that DOES
// carry the token cannot be associated with the platform origin.
func TestDeepSeekRecordsURLFromEmptyTokenEvent(t *testing.T) {
	events := make(chan browserauth.Event, 4)
	// First event: requestWillBeSent, no token, carries the URL.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{"requestId":"r1","url":"https://platform.deepseek.com/api/usage"}`),
	}
	// Second event: requestWillBeSentExtraInfo on the same
	// requestId, now with the Bearer token. URL is absent.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"requestId":"r1","headers":{"authorization":"Bearer paired.token.12345678901234567890"}}`),
	}
	close(events)

	browser := &fakeDeepSeekBrowser{
		exited: true,
		cdp: &fakeDeepSeekCDP{
			pageURL: "https://platform.deepseek.com/usage",
			events:  events,
		},
	}
	token, _, err := runDeepSeekLogin(context.Background(), browser, func(t string) bool {
		return t == "paired.token.12345678901234567890" ||
			t == "candidate_abcdefghijklmnopqrstuvwxyz"
	})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("expected a token to be returned")
	}
}

func TestRunDeepSeekLoginValidatesAfterBrowserClose(t *testing.T) {
	closed := false
	browser := newFakeDeepSeekBrowser(func() { closed = true })
	browser.exited = true
	_, _, err := runDeepSeekLogin(context.Background(), browser, func(string) bool { return closed })
	if err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("browser was not closed before returning")
	}
}

// TestRunDeepSeekLoginRejectsExitedWithoutValidSnapshot proves that a
// browser exit with no valid saved snapshot is rejected. A candidate
// from a non-platform request must not let the coordinator settle
// without a real platform envelope.
func TestRunDeepSeekLoginRejectsExitedWithoutValidSnapshot(t *testing.T) {
	browser := newFakeDeepSeekBrowser(func() {})
	browser.exited = true
	// The fake CDP returns a snapshot that is well-formed but the
	// test simulates "no platform origin" by overriding pageURL to
	// about:blank, so the coordinator never updates lastSnapshot.
	browser.cdp.pageURL = "about:blank"
	_, _, err := runDeepSeekLogin(context.Background(), browser, func(string) bool { return true })
	if err == nil {
		t.Fatal("expected error when browser exits without a valid platform snapshot")
	}
}

// TestRunDeepSeekLoginDoesNotAcceptPreLoginStorageCandidates proves that
// token-shaped strings on the sign-in page (Cloudflare/analytics values)
// are not enough to close the browser and create an account.
func TestRunDeepSeekLoginDoesNotAcceptPreLoginStorageCandidates(t *testing.T) {
	browser := &fakeDeepSeekBrowser{
		exited: true,
		cdp:    &fakeDeepSeekCDP{pageURL: deepSeekLoginURL},
	}
	_, _, err := runDeepSeekLogin(context.Background(), browser, func(string) bool { return true })
	if err == nil {
		t.Fatal("expected pre-login storage candidates to be rejected")
	}
}

func TestRunDeepSeekPageRestoresStoredCookies(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	defer func() { launchDeepSeekBrowser = originalLaunch }()

	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	webStore := `{"l":{},"s":{},"c":[{"name":"session","value":"cookie-value","domain":"platform.deepseek.com","path":"/","secure":true,"httpOnly":true}]}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatal(err)
	}
	if len(cdp.setCookies) != 1 || cdp.setCookies[0].Name != "session" {
		t.Fatalf("restored cookies = %#v", cdp.setCookies)
	}
}

func TestDeepSeekSnapshotWithCookiesPersistsPlatformCookies(t *testing.T) {
	cdp := &fakeDeepSeekCDP{browserCookies: []browserauth.Cookie{
		{Name: "session", Value: "v", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
		{Name: "other", Value: "x", Domain: "example.com", Path: "/"},
	}}
	got := deepSeekSnapshotWithCookies(context.Background(), `{"l":{},"s":{}}`, cdp)
	if !strings.Contains(got, `"name":"session"`) || strings.Contains(got, `"name":"other"`) {
		t.Fatalf("snapshot cookies = %s", got)
	}
}

// TestRunDeepSeekLoginRejectsOrphanPlatformTokenOnExit proves that
// a platform-origin network candidate alone is not enough to settle
// when the browser closes before a valid storage snapshot has been
// observed. The coordinator must NOT return an empty WebStore; the
// existing account must remain untouched.
func TestRunDeepSeekLoginRejectsOrphanPlatformTokenOnExit(t *testing.T) {
	events := make(chan browserauth.Event, 2)
	events <- browserauth.Event{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{"requestId":"r1","url":"https://platform.deepseek.com/api/usage"}`),
	}
	events <- browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"requestId":"r1","headers":{"authorization":"Bearer orphan.platform.12345678901234567890"}}`),
	}
	close(events)

	browser := &fakeDeepSeekBrowser{
		onClose: func() {},
		cdp: &fakeDeepSeekCDP{
			pageURL: "https://platform.deepseek.com/usage",
		},
	}
	browser.exited = true
	browser.cdp.events = events
	// snapshotValue is an invalid envelope (not a {l,s} pair) so
	// the fake default is bypassed and lastSnapshot stays empty.
	browser.cdp.snapshotValue = "null"
	_, store, err := runDeepSeekLogin(context.Background(), browser, func(string) bool { return true })
	if err == nil {
		t.Fatal("expected error: orphan platform token must not produce a WebStore on browser exit")
	}
	if store != "" {
		t.Fatalf("got WebStore %q, want empty (preserve existing account)", store)
	}
}

// TestDeepSeekPairsExtraInfoWithRequestID proves the coordinator
// accepts an ExtraInfo (no URL) only after the matching
// requestWillBeSent (with URL) lands on the platform origin. This
// is the production ordering from Chrome; an attacker cannot
// pre-seed a token without a real requestId.
func TestDeepSeekPairsExtraInfoWithRequestID(t *testing.T) {
	events := make(chan browserauth.Event, 4)
	// requestWillBeSent with URL arrives first, on platform origin.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{"requestId":"r1","url":"https://platform.deepseek.com/api/usage"}`),
	}
	// ExtraInfo on the same requestId with the Bearer header.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"requestId":"r1","headers":{"authorization":"Bearer real.token.12345678901234567890"}}`),
	}
	close(events)

	browser := &fakeDeepSeekBrowser{
		exited: true,
		cdp: &fakeDeepSeekCDP{
			pageURL:       "https://platform.deepseek.com/usage",
			events:        events,
			snapshotValue: `{"l":{},"s":{}}`,
		},
	}
	called := false
	_, store, err := runDeepSeekLogin(context.Background(), browser, func(t string) bool {
		called = true
		return t == "real.token.12345678901234567890"
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("validator was not called for the platform-origin token")
	}
	if store == "" {
		t.Fatal("expected a saved storage snapshot")
	}
}

// TestDeepSeekDropsExtraInfoWithoutMatchingRequestID proves an
// ExtraInfo with a requestId that never sees a matching
// requestWillBeSent is dropped. A third-party token cannot
// impersonate a platform request by forging the requestId alone.
func TestDeepSeekDropsExtraInfoWithoutMatchingRequestID(t *testing.T) {
	events := make(chan browserauth.Event, 2)
	// ExtraInfo on requestId="r1" with no prior requestWillBeSent.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"requestId":"r1","headers":{"authorization":"Bearer orphan.token.12345678901234567890"}}`),
	}
	close(events)

	// Use a snapshot that is NOT a valid envelope so the only
	// candidate available is the orphan token. The coordinator must
	// not settle on the orphan alone.
	empty := &fakeDeepSeekCDP{
		pageURL:       "https://platform.deepseek.com/usage",
		events:        events,
		snapshotValue: "null",
	}
	browser := &fakeDeepSeekBrowser{exited: true, cdp: empty}
	called := false
	_, _, err := runDeepSeekLogin(context.Background(), browser, func(string) bool {
		called = true
		return true
	})
	if err == nil {
		t.Fatal("expected error: orphan ExtraInfo must not settle")
	}
	if called {
		t.Fatal("validator should not be called for an orphan ExtraInfo")
	}
}
