package sidebar

import (
	"context"
	"encoding/json"
	"fmt"
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
	// responseBodies maps requestId → response body for the
	// GetResponseBody fake. Tests register `{"code":0,...}` for a real
	// protected response; a missing/non-zero-code body makes
	// deepSeekResponseCodeOK return false.
	responseBodies map[string]string
	// postNavLengths models the real SPA-overwrites-userToken behavior
	// observed in the diagnostic: after nav1 the SPA overwrites userToken
	// with a short default (e.g. len 30). If runDeepSeekPage re-applies
	// the restore script (post-load Evaluate) and re-navigates, the
	// second nav sees the correct length. Map: nav → lengths. When nil
	// for a nav, falls back to localStorageLengths.
	postNavLengths map[int][]int
	// reapplySeen tracks whether the post-load re-apply Evaluate ran,
	// so the fake can model that after re-apply + re-navigate the SPA no
	// longer overwrites userToken (it sees the valid token).
	reapplySeen bool
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
	// Track whether the post-load re-apply script (the restore script
	// that sets localStorage.setItem) ran via Evaluate — this is how the
	// production code re-applies after SPA boot overwrites userToken.
	if strings.Contains(expression, "localStorage.setItem") {
		c.mu.Lock()
		c.reapplySeen = true
		c.mu.Unlock()
		return json.RawMessage(`{"result":{}}`), nil
	}
	// Post-navigation localStorage key probe (runDeepSeekPage's restore
	// verification). The production expression returns a JSON array of
	// [present, valueLength] pairs ([-1,-1] when absent). Tests set
	// localStorageLengths to the value lengths per expected key; -1 means
	// absent. A nil default means "every expected key present, length 1".
	if strings.Contains(expression, "localStorage.getItem") {
		c.mu.Lock()
		lens := append([]int(nil), c.localStorageLengths...)
		nav := c.navigateCount
		// Model the real SPA-overwrite behavior: after nav1, if the
		// re-apply has NOT run yet, the SPA overwrites userToken with a
		// short default (postNavLengths[1]). After re-apply + re-navigate,
		// the SPA no longer overwrites (postNavLengths[2]).
		if c.postNavLengths != nil {
			if l, ok := c.postNavLengths[nav]; ok {
				lens = append([]int(nil), l...)
			}
		}
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

// NavigateWithLoader models Page.navigate returning the navigation's
// loaderId. Each navigation gets a fresh loaderId ("L"+nav) so the
// coordinator can associate only this navigation's requests/responses.
// It increments navigateCount and fires onNavigate so tests that push
// events on re-navigate still work.
func (c *fakeDeepSeekCDP) NavigateWithLoader(context.Context, string, ...string) (string, error) {
	c.mu.Lock()
	c.navigated = true
	c.navigateCount++
	nav := c.navigateCount
	loader := "L" + strconv.Itoa(nav)
	hook := c.onNavigate
	c.mu.Unlock()
	if hook != nil {
		hook(nav)
	}
	return loader, nil
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

// GetResponseBody models Network.getResponseBody: returns the body string
// registered for a requestId (tests set responseBodies per requestId, e.g.
// `{"code":0,...}` for a real protected response). Empty/missing → error,
// so deepSeekResponseCodeOK returns false.
func (c *fakeDeepSeekCDP) GetResponseBody(_ context.Context, requestID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, ok := c.responseBodies[requestID]
	if !ok {
		return "", fmt.Errorf("no body for %s", requestID)
	}
	return body, nil
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
func deepSeekAuthResponseEvent(status int, url, requestID string) browserauth.Event {
	return browserauth.Event{
		Method: "Network.responseReceived",
		Params: json.RawMessage(`{"requestId":"` + requestID + `","url":"` + url + `","response":{"url":"` + url + `","status":` + strconv.Itoa(status) + `,"mimeType":"application/json"}}`),
	}
}

// deepSeekRequestEvent builds a Network.requestWillBeSent event with the
// given loaderId/frameId/requestId so the page flow can associate the
// matching response with THIS navigation window (no drain).
func deepSeekRequestEvent(loaderID, requestID, url string) browserauth.Event {
	return browserauth.Event{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{"requestId":"` + requestID + `","loaderId":"` + loaderID + `","frameId":"F1","url":"` + url + `","headers":{}}`),
	}
}

// protectedAuthSequence wires a one-shot, in-order event sequence
// (requestWillBeSent → responseReceived → loadingFinished) onto the
// fake's onNavigate hook, so the real network ordering is modelled per
// navigation (no periodic pump masking the race). Each navigation gets a
// fresh loaderId (the fake returns "L"+nav from NavigateWithLoader); the
// sequence uses that loader and a per-navigation requestId, and registers
// the response body. Only the navigation that should succeed emits the
// sequence; tests pass emitOnNav to choose (e.g. 1 for first nav, 2 for a
// re-navigate). Returns the events channel.
func protectedAuthSequence(cdp *fakeDeepSeekCDP, apiURL string, emitOnNav int) chan browserauth.Event {
	ch := make(chan browserauth.Event, 64)
	if cdp.responseBodies == nil {
		cdp.responseBodies = map[string]string{}
	}
	prev := cdp.onNavigate
	cdp.onNavigate = func(nav int) {
		if prev != nil {
			prev(nav)
		}
		if nav != emitOnNav {
			return
		}
		loader := "L" + strconv.Itoa(nav)
		rid := "r" + strconv.Itoa(nav)
		cdp.mu.Lock()
		cdp.responseBodies[rid] = `{"code":0,"data":{}}`
		cdp.mu.Unlock()
		// In-order: request, response, loadingFinished.
		ch <- deepSeekRequestEvent(loader, rid, apiURL)
		ch <- deepSeekAuthResponseEvent(200, apiURL, rid)
		ch <- browserauth.Event{Method: "Network.loadingFinished", Params: json.RawMessage(`{"requestId":"` + rid + `"}`)}
	}
	cdp.events = ch
	return ch
}

// eventsWithAuth returns a buffered events channel pre-loaded with the
// given events (NO pump). Used by tests that need a fixed set of events
// (e.g. a single stale pair that must be ignored by loaderId isolation).
func eventsWithAuth(ev ...browserauth.Event) chan browserauth.Event {
	ch := make(chan browserauth.Event, 32)
	for _, e := range ev {
		ch <- e
	}
	return ch
}

const deepSeekAuthAPIURL = "https://platform.deepseek.com/api/v0/users/get_user_summary"

// TestRunDeepSeekPageReappliesStorageAfterSPAOverwrite models the REAL
// behavior observed in the diagnostic (cmd/diag-deepseek):
//  1. document-start script restores userToken (len 102) on nav1.
//  2. SPA boots and OVERWRITES userToken with a short default (len 30).
//  3. Page redirects to /sign_in.
//
// The fix (verified in cmd/diag-deepseek2) is: after nav1, RE-APPLY the
// restore script via Evaluate (post-load), then RE-NAVIGATE. On nav2,
// the document-start script re-applies the saved value (len 102) and
// the SPA does NOT overwrite it (it recognizes the valid token), so the
// page stays on /usage.
//
// The old implementation never re-applies post-load, so this test FAILS
// on the old impl (RED). The fake models the real observed lengths:
// nav1 → userToken len 30 (overwritten), nav2 → userToken len 92 (restored).
func TestRunDeepSeekPageReappliesStorageAfterSPAOverwrite(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalSettle := deepSeekSettleDelayOverride
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekSettleDelayOverride = originalSettle
	}()
	deepSeekSettleDelayOverride = 0

	// The fake models the real diagnostic behavior:
	// - postNavLengths[1] = [30] → after nav1 SPA overwrites userToken to len 30
	// - postNavLengths[2] = [92] → after nav2 (post re-apply) SPA preserves len 92
	// - renavURL = usage → after nav2 PageURL returns /usage (SPA stays)
	cdp := &fakeDeepSeekCDP{
		pageURL:            deepSeekUsageURL,
		renavURL:           deepSeekUsageURL,
		renavAfterNavigate: 2,
		postNavLengths:     map[int][]int{1: {30}, 2: {92}},
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	// userToken saved value has length 92 (matching postNavLengths[2]).
	// Use a 92-char placeholder so deepSeekExpectedStorageEntries computes
	// expectedLen=92, matching the fake's nav2 response.
	placeholder := strings.Repeat("a", 92)
	webStore := `{"l":{"userToken":"` + placeholder + `"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("re-apply + re-navigate must recover the SPA overwrite and open the page: %v", err)
	}
	if !cdp.reapplySeen {
		t.Fatal("runDeepSeekPage must re-apply the restore script via Evaluate after SPA boot overwrites userToken")
	}
	if cdp.navigateCount < 2 {
		t.Fatalf("runDeepSeekPage must re-navigate at least once after re-apply (count=%d)", cdp.navigateCount)
	}
}

// TestRunDeepSeekPageErrorDoesNotCloseBrowser proves that when the auth
// flow fails, the error is returned (for the /api/open handshake to
// surface) but the browser is NOT closed — it stays open until the user
// manually closes it. The old impl's defer browser.Close() on error
// caused the flash-close. This test FAILS on the old impl (RED).
func TestRunDeepSeekPageErrorDoesNotCloseBrowser(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	defer func() { launchDeepSeekBrowser = originalLaunch }()

	// SPA overwrites userToken to len 30 on EVERY nav (no re-apply helps).
	// Page stuck on /sign_in → runDeepSeekPage must error, but browser
	// must NOT be closed.
	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekLoginURL,
		postNavLengths:      map[int][]int{1: {30}, 2: {30}},
		localStorageLengths: []int{92},
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	webStore := `{"l":{"userToken":"x"},"s":{}}`
	err := RunDeepSeekPage(deepSeekUsageURL, webStore)
	if err == nil {
		t.Fatal("runDeepSeekPage must return an error when auth fails (SPA stuck on login page)")
	}
	if browser.closed {
		t.Fatal("runDeepSeekPage must NOT close the browser on auth error — it must stay open for the user to close manually (flash-close regression)")
	}
}

// dsPageTestSetup is the common setup for runDeepSeekPage tests: saves
// launch/settle overrides, sets settle to 0 (no sleep in tests), and
// injects the fake browser. Returns the cdp for configuration.
func dsPageTestSetup(t *testing.T) (*fakeDeepSeekCDP, *fakeDeepSeekBrowser, func()) {
	t.Helper()
	originalLaunch := launchDeepSeekBrowser
	originalSettle := deepSeekSettleDelayOverride
	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	deepSeekSettleDelayOverride = 0
	return cdp, browser, func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekSettleDelayOverride = originalSettle
	}
}

// dsWebStore builds a webStore JSON with the given userToken value length.
func dsWebStore(userTokenLen int) string {
	v := strings.Repeat("a", userTokenLen)
	return `{"l":{"userToken":"` + v + `"},"s":{}}`
}

func TestRunDeepSeekPageRestoresStoredCookies(t *testing.T) {
	cdp, browser, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {3}, 2: {3}} // userToken preserved on both navs
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2

	webStore := `{"l":{"userToken":"aaa"},"s":{},"c":[{"name":"session","value":"cookie-value","domain":"platform.deepseek.com","path":"/","secure":true,"httpOnly":true}]}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatal(err)
	}
	if len(cdp.setCookies) != 1 || cdp.setCookies[0].Name != "session" {
		t.Fatalf("restored cookies = %#v", cdp.setCookies)
	}
	if !cdp.navigated {
		t.Fatal("page was not navigated")
	}
	if browser.closed {
		t.Fatal("browser was closed instead of staying open for the user")
	}
}

// TestRunDeepSeekPageDetectsLoginRedirect proves that when after
// re-apply + re-navigate the page is still on /sign_in, runDeepSeekPage
// surfaces a clear error.
func TestRunDeepSeekPageDetectsLoginRedirect(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.pageURL = deepSeekLoginURL                       // stuck on sign_in even after re-navigate
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}} // re-apply works but URL stays sign_in

	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when the page stays on /sign_in after re-apply + re-navigate")
	}
}

// TestRunDeepSeekPageRejectsAuthKeyMismatch proves that when userToken
// length doesn't match the saved value even after re-apply + re-navigate,
// runDeepSeekPage surfaces an error.
func TestRunDeepSeekPageRejectsAuthKeyMismatch(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {30}} // SPA overwrites on BOTH navs

	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when userToken length mismatches after re-apply + re-navigate")
	}
}

// TestRunDeepSeekPageFailsWhenUserTokenAbsent proves that when
// userToken is absent from localStorage even after re-apply, the page
// flow errors.
func TestRunDeepSeekPageFailsWhenUserTokenAbsent(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {-1}, 2: {-1}} // userToken absent on both navs

	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when userToken is absent after re-apply + re-navigate")
	}
}

// TestRunDeepSeekPageRequiresUserTokenKey proves that a webStore with no
// userToken key at all is rejected.
func TestRunDeepSeekPageRequiresUserTokenKey(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {1}, 2: {1}}

	webStore := `{"l":{"theme":"dark"},"s":{}}` // no userToken
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when the webStore has no userToken key")
	}
}

// TestRunDeepSeekPageIgnoresUnrelatedStorageKeyChange proves a non-auth
// key with a different length does not fail the restore (only userToken
// is verified).
func TestRunDeepSeekPageIgnoresUnrelatedStorageKeyChange(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	// userToken (auth) preserved; theme (non-auth) not probed.
	cdp.postNavLengths = map[int][]int{1: {3}, 2: {3}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2

	webStore := `{"l":{"userToken":"aaa","theme":"dark"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("a changed non-auth key must not fail the restore: %v", err)
	}
}

// TestRunDeepSeekPageMustReapplyAndRenavigate proves the page flow
// performs the re-apply (Evaluate with setItem) AND re-navigates
// (navigateCount >= 2).
func TestRunDeepSeekPageMustReapplyAndRenavigate(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {3}, 2: {3}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2

	webStore := dsWebStore(3)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("re-apply + re-navigate must succeed: %v", err)
	}
	if !cdp.reapplySeen {
		t.Fatal("runDeepSeekPage must re-apply the restore script via Evaluate")
	}
	if cdp.navigateCount < 2 {
		t.Fatalf("runDeepSeekPage must re-navigate at least once (count=%d)", cdp.navigateCount)
	}
}

// TestRunDeepSeekPageSurvivesSingleBadCookie proves a single
// non-injectable cookie (e.g. a __Host- cookie Chrome refuses because
// it carries a Domain) must NOT abort the whole account-page flow.
// The good cookie must still be injected, navigation must run, and the
// browser must stay open until the user closes it.
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

	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		rejectCookieNames:   map[string]bool{"__Host-bad": true},
		localStorageLengths: []int{len("tok")},
	}
	protectedAuthSequence(cdp, deepSeekAuthAPIURL, 1)
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	webStore := `{"l":{"userToken":"tok"},"s":{},"c":[` +
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

	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL, localStorageLengths: []int{len("storage-only")}}
	protectedAuthSequence(cdp, deepSeekAuthAPIURL, 1)
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	// No "c" key at all — an older saved account. userToken is the auth
	// key; storage-only (no cookies) still authenticates via it.
	webStore := `{"l":{"userToken":"storage-only"},"s":{}}`
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

	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekUsageURL,
		localStorageLengths: []int{-1}, // auth key ABSENT
	}
	protectedAuthSequence(cdp, deepSeekAuthAPIURL, 1)
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

	cdp := &fakeDeepSeekCDP{pageURL: deepSeekUsageURL, localStorageLengths: []int{len("tok")}}
	protectedAuthSequence(cdp, deepSeekAuthAPIURL, 1)
	browser := &fakeDeepSeekBrowser{cdp: cdp, waitBlocks: true, waitRelease: make(chan struct{})}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	webStore := `{"l":{"userToken":"tok"},"s":{}}`
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
