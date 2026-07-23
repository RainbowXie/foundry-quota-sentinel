package sidebar

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
)

type fakeDeepSeekBrowser struct {
	cdp     *fakeDeepSeekCDP
	exited  bool
	closed  bool
	onClose func()
	// waitBlocks makes Wait block until waitRelease is signaled, so a
	// test can prove runDeepSeekPage stays open until the user closes
	// the window rather than returning immediately.
	waitBlocks  bool
	waitRelease chan struct{}
}

func (b *fakeDeepSeekBrowser) CDP(context.Context) (deepSeekCDP, error) { return b.cdp, nil }
func (b *fakeDeepSeekBrowser) Exited() bool                             { return b.exited }
func (b *fakeDeepSeekBrowser) Wait() error {
	if b.waitBlocks {
		<-b.waitRelease
	}
	return nil
}
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
	// rejectCookieNames makes SetCookies model a per-cookie injection
	// failure (e.g. a __Host- cookie rejected by Storage.setCookies).
	// Rejected names are dropped from setCookies and reported as a
	// per-cookie error so tests can exercise the degrade path.
	rejectCookieNames map[string]bool
	// setCookieErrs collects the per-cookie failures a tolerant
	// SetCookies would log but skip.
	setCookieErrs []string
	navigated     bool
	// localStorageLengths models the post-navigation localStorage key
	// probe: one entry per expected key (the restore verification reads
	// them in order). -1 means the key is absent; >=0 is the value
	// length. nil defaults to "all present" so existing success-path
	// page tests keep passing.
	localStorageLengths []int
	// delayedRedirectURL/delayedRedirectAt model a SPA that redirects
	// a moment after the document loads: PageURL returns pageURL for
	// the first delayedRedirectAt reads, then returns
	// delayedRedirectURL. This proves the URL-stability wait catches a
	// delayed redirect a single read would miss.
	delayedRedirectURL string
	delayedRedirectAt  int
	pageURLReads       int
	// renavURL/renavAfterNavigate model the auth-timing fix: the FIRST
	// navigation lands on pageURL (e.g. /sign_in because the SPA's auth
	// check ran before the document-start restore committed), then
	// runDeepSeekPage re-navigates; after renavAfterNavigate navigations
	// PageURL returns renavURL (the authenticated account page). This
	// proves the re-navigate path that re-applies storage before the SPA
	// auth check on the fresh document.
	renavURL           string
	renavAfterNavigate int
	navigateCount      int
	// onNavigate is invoked after each Navigate with the new navigate
	// count, so a test can push events onto the channel only after a
	// re-navigate (modeling the auth-check race fix).
	onNavigate func(nav int)
	// mu guards the shared mutable fields below when a test drives
	// runDeepSeekPage in a goroutine (StaysOpenAfterNavigation).
	mu sync.Mutex
}

func (c *fakeDeepSeekCDP) EnableNetwork(context.Context) error { return nil }
func (c *fakeDeepSeekCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	return append([]browserauth.Cookie(nil), c.browserCookies...), nil
}
func (c *fakeDeepSeekCDP) PageURL(context.Context, ...string) (string, error) {
	c.mu.Lock()
	c.pageURLReads++
	reads := c.pageURLReads
	url := c.pageURL
	if c.delayedRedirectURL != "" && reads > c.delayedRedirectAt {
		url = c.delayedRedirectURL
	}
	// After enough re-navigations, the fresh document's document-start
	// restore runs before the SPA auth check → the page stays on the
	// authenticated account URL.
	if c.renavURL != "" && c.navigateCount >= c.renavAfterNavigate {
		url = c.renavURL
	}
	c.mu.Unlock()
	return url, nil
}
func (c *fakeDeepSeekCDP) Events() <-chan browserauth.Event { return c.events }
func (c *fakeDeepSeekCDP) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	// Post-navigation localStorage key probe (runDeepSeekPage's restore
	// verification). The production expression returns a JSON array of
	// [present, valueLength] pairs ([-1,-1] when absent). Tests set
	// localStorageLengths to the value lengths per expected key; -1 means
	// absent. A nil default means "every expected key present, length 1".
	if strings.Contains(expression, "localStorage.getItem") {
		c.mu.Lock()
		lens := append([]int(nil), c.localStorageLengths...)
		c.mu.Unlock()
		if lens == nil {
			lens = []int{1}
		}
		pairs := make([][2]int, len(lens))
		for i, n := range lens {
			if n < 0 {
				pairs[i] = [2]int{-1, -1}
			} else {
				pairs[i] = [2]int{1, n}
			}
		}
		// The production expression wraps the array in JSON.stringify,
		// so result.value is a STRING like "[[1,12]]". Mirror that so
		// the helper parses it back into [][2]int.
		inner, _ := json.Marshal(pairs)
		stringified, _ := json.Marshal(string(inner))
		return json.RawMessage(`{"result":{"value":` + string(stringified) + `}}`), nil
	}
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
func (c *fakeDeepSeekCDP) Navigate(context.Context, string, ...string) error {
	c.mu.Lock()
	c.navigated = true
	c.navigateCount++
	nav := c.navigateCount
	hook := c.onNavigate
	c.mu.Unlock()
	if hook != nil {
		hook(nav)
	}
	return nil
}

// SetCookiesBestEffort mirrors the shared best-effort injector: each
// rejected cookie is recorded by name and skipped, the rest are
// injected, and the result reports counts. A single failure must not
// abort the page flow.
func (c *fakeDeepSeekCDP) SetCookiesBestEffort(_ context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result browserauth.CookieInjectionResult
	for _, cookie := range cookies {
		if c.rejectCookieNames != nil && c.rejectCookieNames[cookie.Name] {
			c.setCookieErrs = append(c.setCookieErrs, cookie.Name)
			result.Failed = append(result.Failed, cookie.Name)
			continue
		}
		c.setCookies = append(c.setCookies, cookie)
		result.Injected++
	}
	return result
}
func (c *fakeDeepSeekCDP) Close() error {
	c.closed = true
	return nil
}

// navigatedSnapshot returns a race-safe copy of the navigated flag and
// the injected/failed cookie slices for test assertions when
// runDeepSeekPage is driven in a separate goroutine.
func (c *fakeDeepSeekCDP) navigatedSnapshot() (bool, []browserauth.Cookie, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nav := c.navigated
	cookies := append([]browserauth.Cookie(nil), c.setCookies...)
	errs := append([]string(nil), c.setCookieErrs...)
	return nav, cookies, errs
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

// deepSeekAuthResponseEvent builds a Network.responseReceived event the
// page flow treats as the observable authenticated signal: a 2xx on a
// platform API URL. Tests inject it on the events channel.
func deepSeekAuthResponseEvent(status int, url string) browserauth.Event {
	return browserauth.Event{
		Method: "Network.responseReceived",
		Params: json.RawMessage(`{"requestId":"r1","url":"` + url + `","response":{"url":"` + url + `","status":` + strconv.Itoa(status) + `,"mimeType":"application/json"}}`),
	}
}

// eventsWithAuth returns a buffered, un-opened events channel pre-loaded
// with the given events (e.g. a 200 platform API response). Additional
// events can be pushed after.
func eventsWithAuth(ev ...browserauth.Event) chan browserauth.Event {
	ch := make(chan browserauth.Event, 16)
	for _, e := range ev {
		ch <- e
	}
	return ch
}

const deepSeekAuthAPIURL = "https://platform.deepseek.com/api/v0/users/current"

func TestRunDeepSeekPageRestoresStoredCookies(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 500 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL, events: events}
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
	if !cdp.navigated {
		t.Fatal("page was not navigated to the account URL")
	}
	if browser.closed {
		t.Fatal("browser was closed instead of staying open for the user")
	}
}

// TestRunDeepSeekPageDetectsLoginRedirect proves that when the replayed
// login state does NOT authenticate the page (no successful platform API
// request observed) and the SPA sits on /sign_in, runDeepSeekPage
// surfaces a clear error instead of silently leaving the user on a login
// page. Two same-URL reads and a length match are NOT success; only the
// observed auth request is. Here no auth request ever arrives.
func TestRunDeepSeekPageDetectsLoginRedirect(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekLoginURL,                // stuck on sign_in
		localStorageLengths: []int{len("x")},                 // auth key length matches, but no auth request
		events:              make(chan browserauth.Event, 1), // no auth event
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when no authenticated platform request is observed (page is the login page)")
	}
}

// TestRunDeepSeekPageRejectsAuthRequestWhenAuthKeyMismatch proves the
// auth request alone is not success: the prerequisite auth-key length
// match must also hold. A 200 platform response arrives, but the
// restored userToken length mismatches → the page is NOT treated as
// authenticated.
func TestRunDeepSeekPageRejectsAuthRequestWhenAuthKeyMismatch(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{len("wrong")}, // auth key present but wrong length
		events:              events,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("a 200 platform request with a mismatched auth key must NOT be treated as authenticated")
	}
}

// TestRunDeepSeekPageRejectsDelayedSigninAfterUsage proves that two
// consecutive usage-URL reads followed by a delayed jump to /sign_in
// must NOT be treated as login success. The old two-same-URL logic
// would have returned success on the two usage reads and missed the
// redirect. The new signal requires an observed auth request; with no
// auth request the delayed redirect surfaces as a failure.
func TestRunDeepSeekPageRejectsDelayedSigninAfterUsage(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL, // two reads of usage...
		localStorageLengths: []int{len("x")},
		delayedRedirectURL:  deepSeekLoginURL, // ...then jumps to sign_in
		delayedRedirectAt:   3,
		events:              make(chan browserauth.Event, 1), // no auth request
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("two usage reads then a delayed jump to /sign_in must NOT be treated as login success")
	}
}

// TestRunDeepSeekPageIgnoresUnrelatedStorageKeyChange proves only the
// AUTH keys (token-bearing) are verified. A non-auth key whose live
// length differs from the snapshot must not fail the restore, provided
// the auth key matches AND the auth request is observed. This stops a
// changed analytics/UI key from masquerading as a restore failure or a
// spurious success.
func TestRunDeepSeekPageIgnoresUnrelatedStorageKeyChange(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 500 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	// Saved: userToken="x" (auth, len 1), theme="dark" (non-auth, len 4).
	// Only the AUTH key (userToken) is probed; the non-auth theme key is
	// not verified, so its live length (whatever it is) is irrelevant.
	// The fake returns one length per probed auth key.
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{len("x")}, // userToken matches; theme is not probed
		events:              events,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x","theme":"dark"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("a changed non-auth key must not fail the restore when the auth key matches and the auth request is observed: %v", err)
	}
}

// TestRunDeepSeekPageAcceptsMultiKeyRestore proves the storage
// verification checks every AUTH key. Two auth keys with matching
// lengths plus an observed auth request must succeed.
func TestRunDeepSeekPageAcceptsMultiKeyRestore(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 500 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{len("alpha-token"), len("user-beta")},
		events:              events,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	// Both keys are auth (user...). Sorted: "user_alpha" < "userToken"?
	// Use names that sort in the order we give lengths: "aaa" then "zzz"
	// both token-bearing via "user"/"token". Use explicit token names.
	webStore := `{"l":{"usertokenA":"alpha-token","usertokenB":"user-beta"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("multi auth-key restore with matching lengths + auth request must succeed: %v", err)
	}
}

// TestRunDeepSeekPageRejectsMultiKeyWithMissingAuthKey proves a missing
// AUTH key (not a non-auth key) fails the restore even with an auth
// request observed.
func TestRunDeepSeekPageRejectsMultiKeyWithMissingAuthKey(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{len("alpha-token"), -1}, // second auth key absent
		events:              events,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"usertokenA":"alpha-token","usertokenB":"user-beta"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when an auth key is absent even if an auth request was observed")
	}
}

// TestRunDeepSeekPageRenavigatesToAuthenticate proves the real auth-
// timing fix: when the first navigation never yields an auth request
// (the SPA's auth check ran before the document-start restore
// committed), runDeepSeekPage RE-NAVIGATES so the document-start
// restore runs before the SPA's auth check on the fresh document. The
// fake delivers the auth request only after a re-navigate.
func TestRunDeepSeekPageRenavigatesToAuthenticate(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	// Buffer holds the auth event; it is delivered only after the second
	// Navigate. We push it on the channel once navigateCount reaches 2.
	events := make(chan browserauth.Event, 4)
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{len("x")},
		events:              events,
		onNavigate: func(nav int) {
			if nav >= 2 {
				select {
				case events <- deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL):
				default:
				}
			}
		},
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("re-navigate must recover an auth-check race and open the page: %v", err)
	}
	if cdp.navigateCount < 2 {
		t.Fatalf("runDeepSeekPage must re-navigate at least once (count=%d) when the first navigation yields no auth request", cdp.navigateCount)
	}
}

// TestRunDeepSeekPageDoesNotTreatSamePageLengthMatchAsSuccess proves a
// same-page setItem that only makes the storage length match is NOT
// success when no authenticated platform request is observed. The URL
// stays /sign_in, lengths match, but no auth request arrives → must
// error, not succeed.
func TestRunDeepSeekPageDoesNotTreatSamePageLengthMatchAsSuccess(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekLoginURL, // stuck on sign_in
		localStorageLengths: []int{len("x")},  // lengths "match" but no auth request
		events:              make(chan browserauth.Event, 1),
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must NOT treat a same-page length match as success when no auth request is observed")
	}
}

// TestDeepSeekStorageProbeExprStructure validates the generated CDP
// Runtime.evaluate expression itself — not just the helper's Go-side
// parsing. It must map over the keys array directly (no extra wrapping
// array, which would iterate a single nested-array element and return
// one entry for all keys), reference localStorage.getItem per key, and
// return [1,len]/[-1,-1] pairs wrapped in JSON.stringify.
func TestDeepSeekStorageProbeExprStructure(t *testing.T) {
	cases := [][]string{
		{"userToken"},
		{"alpha", "beta"},
	}
	for _, keys := range cases {
		expr := deepSeekStorageProbeExpr(keys)
		keysJSON, _ := json.Marshal(keys)
		// The keys JSON array must be mapped directly inside
		// JSON.stringify(...): the expression contains "<keysJSON>.map("
		// — NOT "[<keysJSON>].map(" (the double-bracket bug).
		if !strings.Contains(expr, string(keysJSON)+".map(") {
			t.Fatalf("expr must map keys array directly, got: %s", expr)
		}
		if strings.Contains(expr, "["+string(keysJSON)+"].map") {
			t.Fatalf("expr must not wrap keys in an extra array (double-bracket bug): %s", expr)
		}
		if !strings.Contains(expr, "localStorage.getItem(k)") {
			t.Fatalf("expr must read localStorage per key: %s", expr)
		}
		if !strings.Contains(expr, "v==null?[-1,-1]:[1,v.length]") {
			t.Fatalf("expr must return [-1,-1]/[1,len] pairs: %s", expr)
		}
		if !strings.HasPrefix(expr, "JSON.stringify(") {
			t.Fatalf("expr must wrap in JSON.stringify for returnByValue: %s", expr)
		}
	}
}

// TestRunDeepSeekPageSurvivesSingleBadCookie proves a single
// non-injectable cookie (e.g. a __Host- cookie Chrome refuses because
// it carries a Domain) must NOT abort the whole account-page flow.
// The previous code returned the first SetCookies error and the defer
// closed the browser — the visible symptom was the account-page
// browser flashing closed. The good cookie must still be injected,
// navigation must run, and the browser must stay open until the user
// closes it.
func TestRunDeepSeekPageSurvivesSingleBadCookie(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 500 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{
		pageURL:           deepSeekUsageURL,
		rejectCookieNames: map[string]bool{"__Host-bad": true},
		events:            events,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	webStore := `{"l":{},"s":{},"c":[` +
		`{"name":"__Host-bad","value":"x","domain":"platform.deepseek.com","path":"/","secure":true,"httpOnly":true},` +
		`{"name":"session","value":"good","domain":"platform.deepseek.com","path":"/","secure":true,"httpOnly":true}` +
		`]}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("account page aborted on a single bad cookie: %v", err)
	}
	if browser.closed {
		t.Fatal("browser was closed after a non-fatal cookie failure (flash-close regression)")
	}
	if !cdp.navigated {
		t.Fatal("navigation did not run after a non-fatal cookie failure")
	}
	if len(cdp.setCookies) != 1 || cdp.setCookies[0].Name != "session" {
		t.Fatalf("good cookie was not injected, setCookies = %#v", cdp.setCookies)
	}
	if len(cdp.setCookieErrs) != 1 || cdp.setCookieErrs[0] != "__Host-bad" {
		t.Fatalf("bad cookie should be reported but skipped, errs = %#v", cdp.setCookieErrs)
	}
}

// TestRunDeepSeekPageStorageOnlySurvivesNoCookies proves an account
// whose saved WebStore has no cookie envelope (only localStorage /
// sessionStorage) still opens the page and keeps the browser alive.
// The cookie-replay step must be a no-op, not an error. The token key
// matches and an auth request is observed.
func TestRunDeepSeekPageStorageOnlySurvivesNoCookies(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 500 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL, localStorageLengths: []int{len("storage-only")}, events: events}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	// No "c" key at all — an older saved account.
	webStore := `{"l":{"token":"storage-only"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("storage-only account page failed: %v", err)
	}
	if browser.closed {
		t.Fatal("storage-only account browser was closed instead of staying open")
	}
	if !cdp.navigated {
		t.Fatal("storage-only account was not navigated")
	}
	if len(cdp.setCookies) != 0 {
		t.Fatalf("expected no cookie injection, got %#v", cdp.setCookies)
	}
}

// TestRunDeepSeekPageFailsWhenRestoreDidNotApply proves the real fix:
// runDeepSeekPage does NOT treat the page as authenticated when the
// auth key is absent, even if a platform request arrives. The auth-key
// prerequisite (length match) must hold before the auth request counts.
func TestRunDeepSeekPageFailsWhenRestoreDidNotApply(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 80 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{-1}, // auth key ABSENT
		events:              events,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when the auth key is absent even if a platform request arrives")
	}
}

// TestRunDeepSeekPageStaysOpenAfterNavigation proves the account-page
// browser blocks on browser.Wait (the user closing the window) rather
// than returning immediately and being reaped. This is the regression
// guard for the flash-close: Wait must be the final step.
func TestRunDeepSeekPageStaysOpenAfterNavigation(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalWait := deepSeekAuthWaitPerNav
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekAuthWaitPerNav = originalWait
	}()
	deepSeekAuthWaitPerNav = 500 * time.Millisecond

	events := eventsWithAuth(deepSeekAuthResponseEvent(200, deepSeekAuthAPIURL))
	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL, events: events}
	browser := &fakeDeepSeekBrowser{cdp: cdp, waitBlocks: true, waitRelease: make(chan struct{})}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	webStore := `{"l":{},"s":{}}`
	done := make(chan error, 1)
	go func() { done <- RunDeepSeekPage(deepSeekUsageURL, webStore) }()
	select {
	case err := <-done:
		t.Fatalf("RunDeepSeekPage returned before the user closed the window: %v", err)
	case <-time.After(150 * time.Millisecond):
		// expected: still blocked on Wait
	}
	nav, _, _ := cdp.navigatedSnapshot()
	if !nav {
		t.Fatal("navigation did not run")
	}
	browser.waitRelease <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDeepSeekPage returned error after user close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunDeepSeekPage did not return after the user closed the window")
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
