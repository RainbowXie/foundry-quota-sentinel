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
	// sendLoadEvent controls whether NavigateWithLoader pushes
	// Page.frameNavigated events. false = no event (timeout).
	sendLoadEvent bool
	// loadEventsForNav controls which navigations get frameNavigated.
	// nil = all navigations (when sendLoadEvent=true).
	loadEventsForNav map[int]bool
	// navSPAURLs maps nav → the URL the SPA redirects to after boot
	// (the auth-decision URL). nil for a nav = use pageURL/renavURL.
	navSPAURLs map[int]string
	// skipDefaultEvent suppresses the default frameNavigated event in
	// NavigateWithLoader when onNavigate is set (so tests that inject
	// custom events via onNavigate don't also get the default one).
	skipDefaultEvent bool
	// responseBodies maps requestId → response body for the
	// GetResponseBody fake. Tests register `{"code":0,...}` for a real
	// protected response; a missing/non-zero-code body makes
	// deepSeekResponseCodeOK return false. NavigateWithLoader
	// auto-registers a default {"code":0,...} body for a requestId it
	// emits, so success-path tests don't need to wire one explicitly.
	responseBodies map[string]string
	// skipLoadingFinishedForNav suppresses the loadingFinished event on
	// a given nav so RED tests can model an unfinished response body
	// (loadingFinished never fires → body not ready → auth fails).
	skipLoadingFinishedForNav map[int]bool
	// postNavLengths models the real SPA-overwrites-userToken behavior
	// observed in the diagnostic: after nav1 the SPA overwrites userToken
	// with a short default (e.g. len 30). If runDeepSeekPage re-applies
	// the restore script (post-load Evaluate) and re-navigates, the
	// second nav sees the correct length. Map: nav → lengths. When nil
	// for a nav, falls back to localStorageLengths.
	postNavLengths map[int][]int
	// pollBasedLengths models per-navigation per-poll userToken lengths,
	// so a test can simulate "92 multiple times then 30" (SPA overwrite
	// after several stable reads). Map: nav → ordered list of lengths,
	// consumed one per Evaluate(getItem) call. Falls back to postNavLengths
	// when exhausted or nil.
	pollBasedLengths map[int][]int
	// pollBasedURLs models per-navigation per-poll page URLs, so a test
	// can simulate "usage multiple times then sign_in" (delayed redirect).
	// Map: nav → ordered list of URLs, consumed one per PageURL call.
	// Falls back to pageURL/renavURL when exhausted or nil.
	pollBasedURLs map[int][]string
	// pollLenIdx/pollURLIdx track per-nav poll counters.
	pollLenIdx map[int]int
	pollURLIdx map[int]int
	// reapplySeen tracks whether the post-load re-apply Evaluate ran.
	reapplySeen bool
	// mu guards the shared mutable fields below when a test drives
	// runDeepSeekPage in a goroutine (StaysOpenAfterNavigation).
	mu sync.Mutex
}

func (c *fakeDeepSeekCDP) EnableNetwork(context.Context) error { return nil }
func (c *fakeDeepSeekCDP) EnablePage(context.Context) error    { return nil }
func (c *fakeDeepSeekCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	return append([]browserauth.Cookie(nil), c.browserCookies...), nil
}
func (c *fakeDeepSeekCDP) PageURL(context.Context, ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pageURLReads++
	url := c.pageURL
	// If navSPAURLs is set for the current nav, return that URL
	// (it's the SPA's post-routing URL = what PageURL should see).
	if c.navSPAURLs != nil {
		if u, ok := c.navSPAURLs[c.navigateCount]; ok {
			url = u
		}
	}
	// Poll-based URLs override if set.
	if c.pollBasedURLs != nil {
		if urls, ok := c.pollBasedURLs[c.navigateCount]; ok {
			idx := c.pollURLIdx[c.navigateCount]
			if idx < len(urls) {
				url = urls[idx]
				c.pollURLIdx[c.navigateCount] = idx + 1
			} else if len(urls) > 0 {
				url = urls[len(urls)-1] // hold last
			}
		}
	}
	if c.delayedRedirectURL != "" && c.pageURLReads > c.delayedRedirectAt {
		url = c.delayedRedirectURL
	}
	if c.renavURL != "" && c.navigateCount >= c.renavAfterNavigate {
		if c.pollBasedURLs == nil && c.navSPAURLs == nil {
			url = c.renavURL
		}
	}
	return url, nil
}
func (c *fakeDeepSeekCDP) Events() <-chan browserauth.Event { return c.events }
func (c *fakeDeepSeekCDP) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	if strings.Contains(expression, "localStorage.setItem") {
		c.mu.Lock()
		c.reapplySeen = true
		c.mu.Unlock()
		return json.RawMessage(`{"result":{}}`), nil
	}
	if strings.Contains(expression, "localStorage.getItem") {
		c.mu.Lock()
		lens := append([]int(nil), c.localStorageLengths...)
		nav := c.navigateCount
		// Poll-based lengths: consume one length per Evaluate(getItem) call.
		if c.pollBasedLengths != nil {
			if ls, ok := c.pollBasedLengths[nav]; ok {
				idx := c.pollLenIdx[nav]
				if idx < len(ls) {
					lens = []int{ls[idx]}
					c.pollLenIdx[nav] = idx + 1
				} else if len(ls) > 0 {
					lens = []int{ls[len(ls)-1]} // hold last
				}
			}
		}
		// Fallback to postNavLengths if no poll-based lengths for this nav.
		if lens == nil && c.postNavLengths != nil {
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
	sendLoad := c.sendLoadEvent
	loadForNav := c.loadEventsForNav
	c.mu.Unlock()
	if hook != nil {
		hook(nav)
	}
	// Send the real event sequence per nav (unless skipDefaultEvent):
	// 1. Page.frameStartedNavigating (carries loaderId = epoch marker).
	// 2. Network.responseReceived on a protected API URL (status 200,
	//    same loaderId) — the real SPA auth-decision signal.
	// The fake's PageURL returns the SPA's post-routing URL (navSPAURLs).
	if sendLoad && c.events != nil && !c.skipDefaultEvent {
		if loadForNav == nil || loadForNav[nav] {
			// 1. frameStartedNavigating: epoch marker with loaderId.
			fsnEvt := fmt.Sprintf(`{"frameId":"MAIN","loaderId":"%s","url":"%s","navigationType":"navigation"}`, loader, deepSeekUsageURL)
			select {
			case c.events <- browserauth.Event{Method: "Page.frameStartedNavigating", Params: json.RawMessage(fsnEvt)}:
			default:
			}
			// 2. Network.responseReceived on get_user_summary (status 200,
			//    same loaderId) — the real protected API auth signal.
			// Only sent if the SPA accepted the token (navSPAURLs[nav]
			// is /usage). If navSPAURLs[nav] is /sign_in, no protected
			// API response is sent (SPA didn't authenticate).
			spaURL := deepSeekUsageURL
			if c.navSPAURLs != nil {
				if u, ok := c.navSPAURLs[nav]; ok {
					spaURL = u
				}
			} else if c.renavURL != "" && nav >= c.renavAfterNavigate {
				spaURL = c.renavURL
			} else {
				spaURL = c.pageURL
			}
			if deepSeekIsUsagePage(spaURL) {
				rid := fmt.Sprintf("r%d", nav)
				rrEvt := fmt.Sprintf(`{"requestId":"%s","loaderId":"%s","frameId":"MAIN","url":"https://platform.deepseek.com/api/v0/users/get_user_summary","response":{"url":"https://platform.deepseek.com/api/v0/users/get_user_summary","status":200,"mimeType":"application/json"}}`, rid, loader)
				select {
				case c.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(rrEvt)}:
				default:
				}
				// 3. Network.loadingFinished (same requestId) — the body is
				//    only available once loading has finished. Suppressed when
				//    skipLoadingFinishedForNav[nav] is set so RED tests can
				//    model an unfinished body (no loadingFinished).
				if c.skipLoadingFinishedForNav == nil || !c.skipLoadingFinishedForNav[nav] {
					lfEvt := fmt.Sprintf(`{"requestId":"%s"}`, rid)
					select {
					case c.events <- browserauth.Event{Method: "Network.loadingFinished", Params: json.RawMessage(lfEvt)}:
					default:
					}
				}
				// Register the default business code==0 body for this
				// requestId unless the test overrode responseBodies[rid]
				// (e.g. to model a business error or a missing code field).
				if c.responseBodies == nil {
					c.responseBodies = map[string]string{}
				}
				if _, hasBody := c.responseBodies[rid]; !hasBody {
					c.responseBodies[rid] = `{"code":0,"data":{}}`
				}
			}
		}
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
	originalSettle := deepSeekSettleTimeout
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekSettleTimeout = originalSettle
	}()
	deepSeekSettleTimeout = 500 * time.Millisecond

	cdp := &fakeDeepSeekCDP{
		pageURL:            deepSeekUsageURL,
		renavURL:           deepSeekUsageURL,
		renavAfterNavigate: 2,
		postNavLengths:     map[int][]int{1: {30}, 2: {92}},
		events:             make(chan browserauth.Event, 32),
		sendLoadEvent:      true,
		navSPAURLs:         map[int]string{1: deepSeekLoginURL, 2: deepSeekUsageURL},
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
	originalSettle := deepSeekSettleTimeout
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekSettleTimeout = originalSettle
	}()
	deepSeekSettleTimeout = 0

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

// TestRunDeepSeekPageErrorSignalsThenWaits proves the error→signal→Wait
// ordering: on auth failure, signalOpenPageError fires BEFORE browser.Wait
// blocks, so the /api/open handshake receives the error while the browser
// stays open. The error hook must fire, then Wait must block until the user
// closes, then the browser is fully reclaimed.
func TestRunDeepSeekPageErrorSignalsThenWaits(t *testing.T) {
	originalLaunch := launchDeepSeekBrowser
	originalSettle := deepSeekSettleTimeout
	originalErrorHook := OpenPageError
	defer func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekSettleTimeout = originalSettle
		OpenPageError = originalErrorHook
		resetOpenPageErrorOnce()
	}()
	deepSeekSettleTimeout = 200 * time.Millisecond
	resetOpenPageErrorOnce()

	errorCh := make(chan string, 1)
	OpenPageError = func(msg string) {
		select {
		case errorCh <- msg:
		default:
		}
	}

	cdp := &fakeDeepSeekCDP{
		pageURL:             deepSeekLoginURL,
		postNavLengths:      map[int][]int{1: {30}, 2: {30}},
		localStorageLengths: []int{92},
		events:              make(chan browserauth.Event, 32),
		sendLoadEvent:       true,
		navSPAURLs:          map[int]string{1: deepSeekLoginURL, 2: deepSeekLoginURL},
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp, waitBlocks: true, waitRelease: make(chan struct{})}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}

	webStore := `{"l":{"userToken":"x"},"s":{}}`
	done := make(chan error, 1)
	go func() { done <- RunDeepSeekPage(deepSeekUsageURL, webStore) }()

	// Error must be signalled before Wait blocks. Wait for the error
	// signal on errorCh (channel sync — happens-before guaranteed).
	select {
	case <-errorCh:
		// error signalled
	case <-time.After(time.Second):
		t.Fatal("signalOpenPageError must fire before browser.Wait blocks")
	}

	// Verify still blocked on Wait (not returned yet).
	select {
	case err := <-done:
		t.Fatalf("RunDeepSeekPage returned before user closed window: %v", err)
	case <-time.After(50 * time.Millisecond):
		// expected: blocked on Wait
	}
	if browser.closed {
		t.Fatal("browser must NOT be closed while waiting for user")
	}

	// User closes the window → Wait returns → browser reclaimed.
	browser.waitRelease <- struct{}{}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunDeepSeekPage must return the auth error after user closes")
		}
	case <-time.After(time.Second):
		t.Fatal("RunDeepSeekPage did not return after user closed the window")
	}
}

// TestRunDeepSeekPageWaitsForTransitionNotEarlySamples proves the round-1
// condition-wait requires a real TRANSITION from saved-length to
// overwritten-length, not just two early identical samples. Uses non-zero
// observable Page.loadEventFired signal, not poll-based thresholds.
// The fake sends loadEventFired on each nav; the test verifies reapply
// happens after the load event and before the reload.
func TestRunDeepSeekPageWaitsForTransitionNotEarlySamples(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2
	placeholder := strings.Repeat("a", 92)
	webStore := `{"l":{"userToken":"` + placeholder + `"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("must succeed when userToken transitions 92→30→92: %v", err)
	}
	if !cdp.reapplySeen {
		t.Fatal("runDeepSeekPage must re-apply after the first load event")
	}
	if cdp.navigateCount < 2 {
		t.Fatalf("runDeepSeekPage must re-navigate at least once (count=%d)", cdp.navigateCount)
	}
}

// TestRunDeepSeekPageHandlesFirstSampleAlreadyOverwritten proves that
// when the first poll already sees the overwritten value (SPA booted
// before the first poll), the wait completes via 2 stable overwritten reads.
func TestRunDeepSeekPageHandlesFirstSampleAlreadyOverwritten(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2
	placeholder := strings.Repeat("a", 92)
	webStore := `{"l":{"userToken":"` + placeholder + `"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("must succeed when first sample is already overwritten: %v", err)
	}
	if !cdp.reapplySeen {
		t.Fatal("runDeepSeekPage must re-apply even when first sample is already overwritten")
	}
}

// TestRunDeepSeekPageDetectsDelayedJumpToLoginAfterUsageStable proves
// that after reload the page lands on /sign_in (not /usage), the final
// URL check catches it. The loadEventFired fires (document loaded) but
// the SPA redirected to /sign_in.
// The 3-read threshold prevents settling early.
func TestRunDeepSeekPageDetectsDelayedJumpToLoginAfterUsageStable(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	// Nav2 SPA redirects to /sign_in (auth failed after reload).
	cdp.navSPAURLs = map[int]string{1: deepSeekLoginURL, 2: deepSeekLoginURL}
	cdp.pageURL = deepSeekLoginURL
	cdp.renavURL = deepSeekLoginURL
	cdp.renavAfterNavigate = 2
	webStore := `{"l":{"userToken":"` + strings.Repeat("a", 92) + `"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when the page is on /sign_in after reload")
	}
}

// TestRunDeepSeekPageLoadEventTimeout proves that when the browser never
// sends Page.loadEventFired, runDeepSeekPage returns a timeout error
// (not hanging or silently succeeding). This is the observable signal
// failure path.
func TestRunDeepSeekPageLoadEventTimeout(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.sendLoadEvent = false // no loadEventFired → timeout
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	deepSeekSettleTimeout = 200 * time.Millisecond
	webStore := `{"l":{"userToken":"` + strings.Repeat("a", 92) + `"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must return a timeout error when loadEventFired never arrives")
	}
}

// TestRunDeepSeekPageTimeoutDoesNotFlashClose proves that when the auth-
// decision wait times out, the browser is NOT closed (no flash-close).
// The error goes through failAndWait → signalOpenPageError → browser.Wait.
func TestRunDeepSeekPageTimeoutDoesNotFlashClose(t *testing.T) {
	cdp, browser, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.sendLoadEvent = false // no loadEventFired → timeout
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	deepSeekSettleTimeout = 200 * time.Millisecond
	webStore := `{"l":{"userToken":"` + strings.Repeat("a", 92) + `"},"s":{}}`
	err := RunDeepSeekPage(deepSeekUsageURL, webStore)
	if err == nil {
		t.Fatal("runDeepSeekPage must return a timeout error")
	}
	if browser.closed {
		t.Fatal("browser must NOT be closed on timeout — it must stay open via failAndWait→browser.Wait")
	}
}

// TestRunDeepSeekPageCrossNavLateLoadEventRejected proves that a late
// Page.loadEventFired from nav1 (arriving during nav2's wait) does NOT
// falsely satisfy nav2's auth-decision wait. The fake sends loadEventFired
// only for nav1 (loadEventsForNav={1:true}), so nav2 never gets one and
// must time out — proving the cross-nav late event is not consumed.
func TestRunDeepSeekPageCrossNavLateLoadEventRejected(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	// Send loadEvent only on nav1, NOT nav2.
	cdp.loadEventsForNav = map[int]bool{1: true, 2: false}
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2
	webStore := `{"l":{"userToken":"` + strings.Repeat("a", 92) + `"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when nav2's loadEvent never arrives (late nav1 event must not satisfy nav2)")
	}
}

// TestRunDeepSeekPageRejectsInitialFrameNavigatedWithoutSPA proves
// that the initial /usage frameNavigated (from the NavigateWithLoader
// call itself) must NOT be treated as the auth decision — the SPA
// hasn't booted yet. The fake sends ONLY the frameNavigated (no
// navigatedWithinDocument), so the wait must time out. An impl that
// accepts the initial frameNavigated as success would pass this test
// wrongly (RED on such an impl).
func TestRunDeepSeekPageRejectsInitialFrameNavigatedWithoutSPA(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	// Override: send ONLY frameNavigated, NO navigatedWithinDocument.
	cdp.skipDefaultEvent = true
	cdp.onNavigate = func(nav int) {
		loader := "L" + strconv.Itoa(nav)
		evt := fmt.Sprintf(`{"frame":{"id":"F%d","loaderId":"%s","url":"%s"}}`, nav, loader, deepSeekUsageURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameNavigated", Params: json.RawMessage(evt)}:
		default:
		}
	}
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must NOT succeed on initial frameNavigated alone (SPA hasn't made auth decision)")
	}
}

// TestRunDeepSeekPageRejectsSubFrameNavigatedWithinDocument proves a
// navigatedWithinDocument with a DIFFERENT frameId (sub-frame) is NOT
// accepted as the SPA auth-decision signal. Only same-frameId events
// count.
func TestRunDeepSeekPageRejectsSubFrameNavigatedWithinDocument(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.skipDefaultEvent = true
	cdp.onNavigate = func(nav int) {
		loader := "L" + strconv.Itoa(nav)
		// frameNavigated with main frame (no parentId).
		fnEvt := fmt.Sprintf(`{"frame":{"id":"MAIN%d","loaderId":"%s","url":"%s"}}`, nav, loader, deepSeekUsageURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameNavigated", Params: json.RawMessage(fnEvt)}:
		default:
		}
		// navigatedWithinDocument with a DIFFERENT frameId (sub-frame).
		nwdEvt := fmt.Sprintf(`{"frameId":"SUB%d","url":"%s"}`, nav, deepSeekUsageURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.navigatedWithinDocument", Params: json.RawMessage(nwdEvt)}:
		default:
		}
	}
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must reject navigatedWithinDocument with a different frameId (sub-frame)")
	}
}

// TestRunDeepSeekPageRejectsCrossNavLateNavigatedWithinDocument proves
// that a late navigatedWithinDocument from nav1 (SAME frameId "MAIN",
// SAME url=/usage, matching nav2's PageURL=/usage) arriving after
// nav2's frameNavigated does NOT satisfy nav2's wait. The epoch guard
// (only one navigatedWithinDocument per epoch after frameNavigated)
// accepts the FIRST — which is the injected late event — so the REAL
// nav2 event (second) is rejected. This test FAILS if the prod code
// accepts the late event as nav2's auth decision.
//
// To prove the epoch guard works correctly, this test injects the
// late event with url=/usage (same as nav2's PageURL) so the url/path
// checks alone cannot distinguish it. Only the epoch guard (first
// navigatedWithinDocument after frameNavigated) distinguishes it.
// The test verifies the page flow still succeeds (the late event is
// accepted as the first — both are /usage, so auth is correct either
// way), but the key is that it does NOT hang or incorrectly error.
func TestRunDeepSeekPageAcceptsFirstNavigatedWithinDocumentPerEpoch(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.skipDefaultEvent = true
	// Inject the real event sequence per nav:
	// 1. frameStartedNavigating (epoch marker with unique loaderId).
	// 2. For nav2 only: Network.responseReceived on protected API
	//    (status 200, same loaderId) — the auth-decision signal.
	// After nav2's response, inject a LATE nav1 responseReceived with
	// nav1's loaderId (different from nav2's) — must NOT re-trigger.
	nav1LateInjected := false
	cdp.onNavigate = func(nav int) {
		loader := "L" + strconv.Itoa(nav)
		// 1. frameStartedNavigating.
		fsnEvt := fmt.Sprintf(`{"frameId":"MAIN","loaderId":"%s","url":"%s","navigationType":"navigation"}`, loader, deepSeekUsageURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameStartedNavigating", Params: json.RawMessage(fsnEvt)}:
		default:
		}
		// BEFORE nav2's correct response, inject a LATE nav1
		// responseReceived with nav1's loaderId (L1 != L2). It must be
		// rejected by loaderId mismatch in phase 1 and NOT authenticate.
		// This proves a stale response from a previous navigation cannot
		// satisfy the current window's auth check.
		if nav >= 2 && !nav1LateInjected {
			nav1LateInjected = true
			lateEvt := fmt.Sprintf(`{"requestId":"r1_late","loaderId":"L1","frameId":"MAIN","url":"https://platform.deepseek.com/api/v0/users/get_user_summary","response":{"url":"https://platform.deepseek.com/api/v0/users/get_user_summary","status":200,"mimeType":"application/json"}}`)
			select {
			case cdp.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(lateEvt)}:
			default:
			}
		}
		// 2. For nav2 (authed): Network.responseReceived on protected API,
		//    then loadingFinished (same requestId), with a code==0 body
		//    registered. The auth signal requires all three.
		if nav >= 2 {
			rid := fmt.Sprintf("r%d", nav)
			rrEvt := fmt.Sprintf(`{"requestId":"%s","loaderId":"%s","frameId":"MAIN","url":"https://platform.deepseek.com/api/v0/users/get_user_summary","response":{"url":"https://platform.deepseek.com/api/v0/users/get_user_summary","status":200,"mimeType":"application/json"}}`, rid, loader)
			select {
			case cdp.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(rrEvt)}:
			default:
			}
			// loadingFinished makes the body available for getResponseBody.
			lfEvt := fmt.Sprintf(`{"requestId":"%s"}`, rid)
			select {
			case cdp.events <- browserauth.Event{Method: "Network.loadingFinished", Params: json.RawMessage(lfEvt)}:
			default:
			}
			if cdp.responseBodies == nil {
				cdp.responseBodies = map[string]string{}
			}
			cdp.responseBodies[rid] = `{"code":0,"data":{}}`
		}
	}
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.navSPAURLs = map[int]string{1: deepSeekLoginURL, 2: deepSeekUsageURL}
	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err != nil {
		t.Fatalf("must succeed (nav2's loaderId L2 distinguishes from late nav1 L1): %v", err)
	}
}

// dsProtectedSeq emits the full protected-API auth sequence (frameStartedNavigating
// → responseReceived → loadingFinished) for a given nav, with a configurable
// response body registered for the requestId. Used by the business-code RED
// tests so they share one event-ordering helper and differ only in the body.
func dsProtectedSeq(t *testing.T, cdp *fakeDeepSeekCDP, emitOnNav int, body string) {
	t.Helper()
	cdp.skipDefaultEvent = true
	prev := cdp.onNavigate
	cdp.onNavigate = func(nav int) {
		if prev != nil {
			prev(nav)
		}
		loader := "L" + strconv.Itoa(nav)
		// 1. frameStartedNavigating (epoch marker).
		fsnEvt := fmt.Sprintf(`{"frameId":"MAIN","loaderId":"%s","url":"%s","navigationType":"navigation"}`, loader, deepSeekUsageURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameStartedNavigating", Params: json.RawMessage(fsnEvt)}:
		default:
		}
		if nav != emitOnNav {
			return
		}
		rid := fmt.Sprintf("r%d", nav)
		// 2. responseReceived (2xx, protected, same loaderId).
		rrEvt := fmt.Sprintf(`{"requestId":"%s","loaderId":"%s","frameId":"MAIN","url":"https://platform.deepseek.com/api/v0/users/get_user_summary","response":{"url":"https://platform.deepseek.com/api/v0/users/get_user_summary","status":200,"mimeType":"application/json"}}`, rid, loader)
		select {
		case cdp.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(rrEvt)}:
		default:
		}
		// 3. loadingFinished (same requestId).
		lfEvt := fmt.Sprintf(`{"requestId":"%s"}`, rid)
		select {
		case cdp.events <- browserauth.Event{Method: "Network.loadingFinished", Params: json.RawMessage(lfEvt)}:
		default:
		}
		if cdp.responseBodies == nil {
			cdp.responseBodies = map[string]string{}
		}
		cdp.responseBodies[rid] = body
	}
}

// TestRunDeepSeekPageRejects200WithBusinessError proves a 200 response
// carrying a business error (code != 0) is NOT accepted as the auth
// signal. The protected endpoint must return code==0. nav2's body has
// code=401 (unauthorized) → runDeepSeekPage must fail.
func TestRunDeepSeekPageRejects200WithBusinessError(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.navSPAURLs = map[int]string{1: deepSeekLoginURL, 2: deepSeekUsageURL}
	dsProtectedSeq(t, cdp, 2, `{"code":401,"msg":"token expired"}`)
	webStore := dsWebStore(92)
	err := RunDeepSeekPage(deepSeekUsageURL, webStore)
	if err == nil {
		t.Fatal("runDeepSeekPage must fail when the protected API returns business code != 0 (200 + business error is not auth)")
	}
}

// TestRunDeepSeekPageRejects200MissingCodeField proves a 200 response
// with NO top-level "code" field is NOT accepted. The code field MUST
// exist and equal 0; a missing field is a failure (not a real protected
// response).
func TestRunDeepSeekPageRejects200MissingCodeField(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.navSPAURLs = map[int]string{1: deepSeekLoginURL, 2: deepSeekUsageURL}
	dsProtectedSeq(t, cdp, 2, `{"data":{}}`)
	webStore := dsWebStore(92)
	err := RunDeepSeekPage(deepSeekUsageURL, webStore)
	if err == nil {
		t.Fatal("runDeepSeekPage must fail when the protected API response body has no top-level code field")
	}
}

// TestRunDeepSeekPageRejectsUnfinishedBody proves a 200 response whose
// body is NOT yet finished (no loadingFinished event) is NOT accepted.
// The body is only available after loadingFinished; without it, the
// auth check cannot confirm code==0 and must fail.
func TestRunDeepSeekPageRejectsUnfinishedBody(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.navSPAURLs = map[int]string{1: deepSeekLoginURL, 2: deepSeekUsageURL}
	cdp.skipDefaultEvent = true
	// Suppress loadingFinished on nav2 so the body is never ready.
	cdp.skipLoadingFinishedForNav = map[int]bool{2: true}
	prev := cdp.onNavigate
	cdp.onNavigate = func(nav int) {
		if prev != nil {
			prev(nav)
		}
		loader := "L" + strconv.Itoa(nav)
		fsnEvt := fmt.Sprintf(`{"frameId":"MAIN","loaderId":"%s","url":"%s","navigationType":"navigation"}`, loader, deepSeekUsageURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameStartedNavigating", Params: json.RawMessage(fsnEvt)}:
		default:
		}
		if nav != 2 {
			return
		}
		rid := fmt.Sprintf("r%d", nav)
		rrEvt := fmt.Sprintf(`{"requestId":"%s","loaderId":"%s","frameId":"MAIN","url":"https://platform.deepseek.com/api/v0/users/get_user_summary","response":{"url":"https://platform.deepseek.com/api/v0/users/get_user_summary","status":200,"mimeType":"application/json"}}`, rid, loader)
		select {
		case cdp.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(rrEvt)}:
		default:
		}
		// NO loadingFinished — body not ready. Register the body anyway
		// to prove the absence of loadingFinished (not the body content)
		// is what fails the check.
		if cdp.responseBodies == nil {
			cdp.responseBodies = map[string]string{}
		}
		cdp.responseBodies[rid] = `{"code":0,"data":{}}`
	}
	webStore := dsWebStore(92)
	err := RunDeepSeekPage(deepSeekUsageURL, webStore)
	if err == nil {
		t.Fatal("runDeepSeekPage must fail when loadingFinished never fires (body not ready)")
	}
}

// TestRunDeepSeekPageRejectsSubFrameFrameNavigated proves a
// frameNavigated with a parentId (sub-frame/iframe) is NOT accepted
// as the SPA auth-decision signal, even if the URL is /usage. Only
// main-frame (parentId absent) events count.
func TestRunDeepSeekPageRejectsSubFrameFrameNavigated(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.skipDefaultEvent = true
	// Override: send a sub-frame frameNavigated (with parentId) instead
	// of the default main-frame one.
	cdp.onNavigate = func(nav int) {
		loader := "L" + strconv.Itoa(nav)
		spaURL := deepSeekLoginURL
		if cdp.navSPAURLs != nil {
			if u, ok := cdp.navSPAURLs[nav]; ok {
				spaURL = u
			}
		}
		// Sub-frame: has parentId.
		evt := fmt.Sprintf(`{"frame":{"id":"SUB%d","loaderId":"%s","url":"%s","parentId":"F%d"}}`, nav, loader, spaURL, nav)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameNavigated", Params: json.RawMessage(evt)}:
		default:
		}
	}
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must reject sub-frame frameNavigated (parentId present)")
	}
}

// TestRunDeepSeekPageRejectsEmptyLoaderId proves a frameNavigated with
// an empty loaderId is NOT accepted — the loaderId must be non-empty
// and strictly match the current navigation's loaderId.
func TestRunDeepSeekPageRejectsEmptyLoaderId(t *testing.T) {
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	deepSeekSettleTimeout = 500 * time.Millisecond
	cdp.skipDefaultEvent = true
	// Override: send frameNavigated with empty loaderId.
	cdp.onNavigate = func(nav int) {
		spaURL := deepSeekLoginURL
		if cdp.navSPAURLs != nil {
			if u, ok := cdp.navSPAURLs[nav]; ok {
				spaURL = u
			}
		}
		evt := fmt.Sprintf(`{"frame":{"id":"F%d","loaderId":"","url":"%s"}}`, nav, spaURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameNavigated", Params: json.RawMessage(evt)}:
		default:
		}
	}
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	webStore := dsWebStore(92)
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must reject frameNavigated with empty loaderId")
	}
}

// dsPageTestSetup is the common setup for runDeepSeekPage tests: saves
// launch/settle overrides, sets settle to 0 (no sleep in tests), and
// injects the fake browser. Returns the cdp for configuration.
func dsPageTestSetup(t *testing.T) (*fakeDeepSeekCDP, *fakeDeepSeekBrowser, func()) {
	t.Helper()
	originalLaunch := launchDeepSeekBrowser
	originalSettle := deepSeekSettleTimeout
	cdp := &fakeDeepSeekCDP{
		pageURL:       deepSeekUsageURL,
		events:        make(chan browserauth.Event, 32),
		sendLoadEvent: true,
		// Default: nav1 SPA redirects to /sign_in (token overwritten),
		// nav2 SPA stays on /usage (token accepted after re-apply).
		navSPAURLs:         map[int]string{1: deepSeekLoginURL, 2: deepSeekUsageURL},
		renavURL:           deepSeekUsageURL,
		renavAfterNavigate: 2,
	}
	browser := &fakeDeepSeekBrowser{cdp: cdp}
	launchDeepSeekBrowser = func(context.Context, string) (deepSeekLoginBrowser, error) {
		return browser, nil
	}
	deepSeekSettleTimeout = 500 * time.Millisecond
	return cdp, browser, func() {
		launchDeepSeekBrowser = originalLaunch
		deepSeekSettleTimeout = originalSettle
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
	cdp.pageURL = deepSeekLoginURL
	cdp.renavURL = deepSeekLoginURL
	cdp.postNavLengths = map[int][]int{1: {30}, 2: {92}}
	cdp.navSPAURLs = map[int]string{1: deepSeekLoginURL, 2: deepSeekLoginURL}

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
	deepSeekSettleTimeout = 500 * time.Millisecond
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
	deepSeekSettleTimeout = 500 * time.Millisecond
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
	cdp, browser, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.rejectCookieNames = map[string]bool{"__Host-bad": true}
	cdp.postNavLengths = map[int][]int{1: {3}, 2: {3}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2

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
	cdp, browser, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {len("storage-only")}, 2: {len("storage-only")}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2

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
	cdp, _, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {-1}, 2: {-1}} // userToken absent
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2

	webStore := `{"l":{"userToken":"x"},"s":{}}`
	if err := RunDeepSeekPage(deepSeekUsageURL, webStore); err == nil {
		t.Fatal("runDeepSeekPage must error when the auth key is absent")
	}
}

// TestRunDeepSeekPageStaysOpenAfterNavigation proves the account-page
// browser blocks on browser.Wait (the user closing the window) rather
// than returning immediately and being reaped. This is the regression
// guard for the flash-close: Wait must be the final step.
func TestRunDeepSeekPageStaysOpenAfterNavigation(t *testing.T) {
	cdp, browser, cleanup := dsPageTestSetup(t)
	defer cleanup()
	cdp.postNavLengths = map[int][]int{1: {3}, 2: {3}}
	cdp.renavURL = deepSeekUsageURL
	cdp.renavAfterNavigate = 2
	browser.waitBlocks = true
	browser.waitRelease = make(chan struct{})

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
