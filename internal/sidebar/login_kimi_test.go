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

// fakeKimiBrowser mirrors fakeDeepSeekBrowser: a controllable browser whose
// CDP is a fakeKimiCDP. waitBlocks/waitRelease prove RunKimiPage stays open
// until the user closes the window rather than returning immediately.
type fakeKimiBrowser struct {
	cdp         *fakeKimiCDP
	exited      bool
	closed      bool
	onClose     func()
	waitBlocks  bool
	waitRelease chan struct{}
}

func (b *fakeKimiBrowser) CDP(context.Context) (kimiCDP, error) { return b.cdp, nil }
func (b *fakeKimiBrowser) Exited() bool                         { return b.exited }
func (b *fakeKimiBrowser) Wait() error {
	if b.waitBlocks {
		<-b.waitRelease
	}
	return nil
}
func (b *fakeKimiBrowser) Close() error {
	b.closed = true
	b.exited = true
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

// fakeKimiCDP mirrors fakeDeepSeekCDP. NavigateWithLoader emits the real
// Kimi auth-decision sequence: Page.frameStartedNavigating (loaderId epoch)
// → Network.responseReceived 200 on GetSubscriptionStats (same loaderId) →
// Network.loadingFinished (same requestId). responseBodies maps requestId →
// a Connect-JSON body (no "code" on success; the parser/querier validate).
type fakeKimiCDP struct {
	pageURL        string
	events         chan browserauth.Event
	snapshotValue  string
	closed         bool
	setCookies     []browserauth.Cookie
	browserCookies []browserauth.Cookie
	navigated      bool
	navigateCount  int
	// sendLoadEvent controls whether NavigateWithLoader pushes the
	// frameStartedNavigating + responseReceived + loadingFinished sequence.
	sendLoadEvent bool
	// navConsoleURLs maps nav → the URL the SPA routes to after boot (the
	// auth-decision URL). nil for a nav = use pageURL.
	navConsoleURLs map[int]string
	// skipLoadingFinishedForNav suppresses loadingFinished on a nav so RED
	// tests can model an unfinished body.
	skipLoadingFinishedForNav map[int]bool
	// responseBodies maps requestId → Connect-JSON body registered for
	// GetResponseBody. Default success body has no "code" + both meters.
	responseBodies map[string]string
	// requestHeaders maps requestId → request headers, so the login can
	// capture the Bearer accessToken from requestWillBeSentExtraInfo.
	requestHeaders map[string]map[string]string
	// headerEvents feeds requestWillBeSentExtraInfo events for the login
	// capture path (set before RunKimiLogin).
	headerEvents []browserauth.Event
	// skipDefaultEvent suppresses the default frameStartedNavigating +
	// responseReceived + loadingFinished sequence in NavigateWithLoader when
	// onNavigate is set, so tests that inject custom events don't also get
	// the default ones.
	skipDefaultEvent bool
	// onNavigate is invoked after each NavigateWithLoader with the nav count,
	// so a test can push custom events onto the channel.
	onNavigate func(nav int)
	mu         sync.Mutex
}

func (c *fakeKimiCDP) EnableNetwork(context.Context) error { return nil }
func (c *fakeKimiCDP) EnablePage(context.Context) error    { return nil }
func (c *fakeKimiCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	return append([]browserauth.Cookie(nil), c.browserCookies...), nil
}
func (c *fakeKimiCDP) PageURL(context.Context, ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	url := c.pageURL
	if c.navConsoleURLs != nil {
		if u, ok := c.navConsoleURLs[c.navigateCount]; ok {
			url = u
		}
	}
	return url, nil
}
func (c *fakeKimiCDP) Events() <-chan browserauth.Event { return c.events }
func (c *fakeKimiCDP) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	if strings.Contains(expression, "localStorage.setItem") {
		return json.RawMessage(`{"result":{}}`), nil
	}
	if c.snapshotValue == "" {
		return json.RawMessage(`{"result":{"value":"{\"l\":{},\"s\":{}}"}}`), nil
	}
	raw, _ := json.Marshal(c.snapshotValue)
	return json.RawMessage(`{"result":{"value":` + string(raw) + `}}`), nil
}
func (c *fakeKimiCDP) AddScriptOnNewDocument(context.Context, string) error { return nil }
func (c *fakeKimiCDP) Navigate(context.Context, string, ...string) error {
	c.mu.Lock()
	c.navigated = true
	c.navigateCount++
	c.mu.Unlock()
	return nil
}

// NavigateWithLoader emits the real Kimi auth-decision sequence per nav.
func (c *fakeKimiCDP) NavigateWithLoader(context.Context, string, ...string) (string, error) {
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
	if c.sendLoadEvent && c.events != nil && !c.skipDefaultEvent {
		// 1. frameStartedNavigating (epoch marker with loaderId).
		fsnEvt := fmt.Sprintf(`{"frameId":"MAIN","loaderId":"%s","url":"%s","navigationType":"navigation"}`, loader, kimiConsoleURL)
		select {
		case c.events <- browserauth.Event{Method: "Page.frameStartedNavigating", Params: json.RawMessage(fsnEvt)}:
		default:
		}
		// 2. Network.responseReceived 200 on GetSubscriptionStats (same
		//    loaderId) — only when the SPA authenticated (consoleURL).
		consoleURL := kimiConsoleURL
		if c.navConsoleURLs != nil {
			if u, ok := c.navConsoleURLs[nav]; ok {
				consoleURL = u
			}
		}
		if isKimiConsolePage(consoleURL) {
			rid := fmt.Sprintf("r%d", nav)
			rrEvt := fmt.Sprintf(`{"requestId":"%s","loaderId":"%s","frameId":"MAIN","url":"%s","response":{"url":"%s","status":200,"mimeType":"application/json"}}`, rid, loader, kimiProtectedQuotaURL, kimiProtectedQuotaURL)
			select {
			case c.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(rrEvt)}:
			default:
			}
			// 3. loadingFinished (same requestId) unless suppressed.
			if c.skipLoadingFinishedForNav == nil || !c.skipLoadingFinishedForNav[nav] {
				lfEvt := fmt.Sprintf(`{"requestId":"%s"}`, rid)
				select {
				case c.events <- browserauth.Event{Method: "Network.loadingFinished", Params: json.RawMessage(lfEvt)}:
				default:
				}
			}
			if c.responseBodies == nil {
				c.responseBodies = map[string]string{}
			}
			if _, hasBody := c.responseBodies[rid]; !hasBody {
				c.responseBodies[rid] = kimiSuccessBodyFixture()
			}
		}
	}
	return loader, nil
}

func (c *fakeKimiCDP) SetCookiesBestEffort(_ context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result browserauth.CookieInjectionResult
	for _, cookie := range cookies {
		c.setCookies = append(c.setCookies, cookie)
		result.Injected++
	}
	return result
}
func (c *fakeKimiCDP) GetResponseBody(_ context.Context, requestID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, ok := c.responseBodies[requestID]
	if !ok {
		return "", fmt.Errorf("no body for %s", requestID)
	}
	return body, nil
}
func (c *fakeKimiCDP) Close() error {
	c.closed = true
	return nil
}

// navigatedSnapshot returns a race-safe copy of the navigated flag for test
// assertions when runKimiPage is driven in a separate goroutine.
func (c *fakeKimiCDP) navigatedSnapshot() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.navigated
}

// kimiSuccessBodyFixture is a synthetic Connect-JSON success body (no "code"
// string) with the three REAL metrics, mirroring the captured membership
// structure (ratelimitCode5h absent-ratio 0%, ratelimitCode7d ratio, and
// subscriptionBalance.amountUsedRatio + expireTime). resetTime/expireTime built
// at call time so they are always in the future.
func kimiSuccessBodyFixture() string {
	now := time.Now()
	return `{"ratelimitCode5h":{"enabled":true,"resetTime":"` + now.Add(5*time.Hour).UTC().Format(time.RFC3339Nano) + `"},"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + now.Add(7*24*time.Hour).UTC().Format(time.RFC3339Nano) + `"},"subscriptionBalance":{"amountUsedRatio":0.0219,"kimiCodeUsedRatio":0.0219,"expireTime":"` + now.Add(30*24*time.Hour).UTC().Format(time.RFC3339Nano) + `","type":"SUBSCRIPTION","feature":"FEATURE_OMNI","unit":"UNIT_CREDIT","domain":"DOMAIN_NEXUS"}}`
}

// kimiPageTestSetup is the common setup for RunKimiPage tests: saves the
// launch override and injects the fake browser. Returns the cdp + browser.
func kimiPageTestSetup(t *testing.T) (*fakeKimiCDP, *fakeKimiBrowser, func()) {
	t.Helper()
	originalLaunch := launchKimiBrowser
	originalSettle := kimiSettleTimeout
	cdp := &fakeKimiCDP{
		pageURL:        kimiConsoleURL,
		events:         make(chan browserauth.Event, 32),
		sendLoadEvent:  true,
		navConsoleURLs: map[int]string{1: kimiConsoleURL},
	}
	browser := &fakeKimiBrowser{cdp: cdp}
	launchKimiBrowser = func(context.Context, string) (kimiLoginBrowser, error) {
		return browser, nil
	}
	kimiSettleTimeout = 500 * time.Millisecond
	return cdp, browser, func() {
		launchKimiBrowser = originalLaunch
		kimiSettleTimeout = originalSettle
	}
}

// TestRunKimiPageOpensConsoleAndWaitForUserClose (task 4.4/4.5) proves the
// account page reaches the authenticated console, signals ready, and stays
// open until the user closes the window. The protected response 200 +
// loadingFinished + valid two-meter body is the auth signal.
func TestRunKimiPageOpensConsoleAndWaitForUserClose(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	browser.waitBlocks = true
	browser.waitRelease = make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- RunKimiPage(kimiConsoleURL, kimiTestEnvelope()) }()

	// Must stay blocked on Wait (not returned) once ready.
	select {
	case err := <-done:
		t.Fatalf("RunKimiPage returned before user closed: %v", err)
	case <-time.After(200 * time.Millisecond):
		// expected: blocked on Wait
	}
	if !cdp.navigatedSnapshot() {
		t.Fatal("navigation did not run")
	}
	browser.waitRelease <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunKimiPage returned error after user close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunKimiPage did not return after user closed the window")
	}
}

// TestRunKimiPageSignalsReadyBeforeWait (task 4.4) proves the ready
// handshake fires BEFORE browser.Wait blocks, so /api/open observes ready
// while the browser stays open.
func TestRunKimiPageSignalsReadyBeforeWait(t *testing.T) {
	_, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	originalReady := OpenPageReady
	readyCh := make(chan struct{}, 1)
	OpenPageReady = func() {
		select {
		case readyCh <- struct{}{}:
		default:
		}
	}
	defer func() {
		OpenPageReady = originalReady
		resetOpenPageErrorOnce()
	}()
	resetOpenPageErrorOnce()
	browser.waitBlocks = true
	browser.waitRelease = make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- RunKimiPage(kimiConsoleURL, kimiTestEnvelope()) }()
	select {
	case <-readyCh:
		// ready signalled before Wait
	case <-time.After(time.Second):
		t.Fatal("signalOpenPageReady must fire before browser.Wait blocks")
	}
	select {
	case err := <-done:
		t.Fatalf("returned before user close: %v", err)
	case <-time.After(50 * time.Millisecond):
		// expected: blocked on Wait
	}
	browser.waitRelease <- struct{}{}
	<-done
}

// TestRunKimiPageErrorSignalsThenWaits (task 4.4) proves that on auth
// failure (protected response business error), signalOpenPageError fires
// BEFORE browser.Wait blocks — no flash-close. The browser stays open for
// the user to close manually.
func TestRunKimiPageErrorSignalsThenWaits(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	originalError := OpenPageError
	errorCh := make(chan string, 1)
	OpenPageError = func(msg string) {
		select {
		case errorCh <- msg:
		default:
		}
	}
	defer func() {
		OpenPageError = originalError
		resetOpenPageErrorOnce()
	}()
	resetOpenPageErrorOnce()
	// Business error: 200 carrying a Connect error code.
	cdp.responseBodies = map[string]string{"r1": `{"code":"permission_denied"}`}
	browser.waitBlocks = true
	browser.waitRelease = make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- RunKimiPage(kimiConsoleURL, kimiTestEnvelope()) }()
	select {
	case <-errorCh:
		// error signalled before Wait
	case <-time.After(time.Second):
		t.Fatal("signalOpenPageError must fire before browser.Wait blocks")
	}
	if browser.closed {
		t.Fatal("browser must NOT be closed on auth error (flash-close regression)")
	}
	browser.waitRelease <- struct{}{}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunKimiPage must return the auth error after user closes")
		}
	case <-time.After(time.Second):
		t.Fatal("did not return after user closed")
	}
}

// TestRunKimiPageDoesNotFlashCloseOnTimeout proves a sentinel timeout (no
// protected response observed) goes through failAndWait → the browser stays
// open, error signalled, not closed.
func TestRunKimiPageDoesNotFlashCloseOnTimeout(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	cdp.sendLoadEvent = false // no protected response → sentinel timeout
	kimiSettleTimeout = 200 * time.Millisecond
	err := RunKimiPage(kimiConsoleURL, kimiTestEnvelope())
	if err == nil {
		t.Fatal("RunKimiPage must return a timeout error")
	}
	if browser.closed {
		t.Fatal("browser must NOT be closed on timeout (flash-close regression)")
	}
}

// TestRunKimiPageRejectsUnfinishedBody (task 4.4) proves a 200 whose body
// is not finished (no loadingFinished) is NOT accepted — body not ready.
func TestRunKimiPageRejectsUnfinishedBody(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	cdp.skipLoadingFinishedForNav = map[int]bool{1: true}
	cdp.responseBodies = map[string]string{"r1": kimiSuccessBodyFixture()}
	err := RunKimiPage(kimiConsoleURL, kimiTestEnvelope())
	if err == nil {
		t.Fatal("RunKimiPage must fail when loadingFinished never fires (body not ready)")
	}
	if browser.closed {
		t.Fatal("browser must NOT be closed on unfinished-body failure")
	}
}

// TestRunKimiPageRejects200WithBusinessError (task 4.4) proves a 200
// carrying a Connect business error code is NOT accepted as auth.
func TestRunKimiPageRejects200WithBusinessError(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	cdp.responseBodies = map[string]string{"r1": `{"code":"permission_denied"}`}
	err := RunKimiPage(kimiConsoleURL, kimiTestEnvelope())
	if err == nil {
		t.Fatal("RunKimiPage must reject a 200 carrying a Connect error code")
	}
	if browser.closed {
		t.Fatal("browser must NOT be closed on business-error failure (flash-close)")
	}
}

// TestRunKimiPageRejectsUnrelatedEndpoint (task 4.4) proves a 200 on an
// unrelated Kimi endpoint is NOT treated as the auth signal — even when the
// body would parse as valid quota data. The fake injects responseReceived
// 200 on a non-protected URL with a valid two-meter body; RunKimiPage must
// NOT accept it as auth (the protected-endpoint check rejects it), so it
// times out / fails rather than succeeding.
func TestRunKimiPageRejectsUnrelatedEndpoint(t *testing.T) {
	cdp, _, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	kimiSettleTimeout = 300 * time.Millisecond
	cdp.sendLoadEvent = false
	cdp.navConsoleURLs = map[int]string{1: kimiConsoleURL}
	cdp.skipDefaultEvent = true
	// Inject a responseReceived 200 on an UNRELATED URL with a valid body —
	// must not authenticate because the URL is not GetSubscriptionStats.
	cdp.onNavigate = func(nav int) {
		loader := "L" + strconv.Itoa(nav)
		fsnEvt := fmt.Sprintf(`{"frameId":"MAIN","loaderId":"%s","url":"%s","navigationType":"navigation"}`, loader, kimiConsoleURL)
		select {
		case cdp.events <- browserauth.Event{Method: "Page.frameStartedNavigating", Params: json.RawMessage(fsnEvt)}:
		default:
		}
		rid := fmt.Sprintf("r%d", nav)
		rrEvt := fmt.Sprintf(`{"requestId":"%s","loaderId":"%s","frameId":"MAIN","url":"https://www.kimi.com/api/unrelated","response":{"url":"https://www.kimi.com/api/unrelated","status":200,"mimeType":"application/json"}}`, rid, loader)
		select {
		case cdp.events <- browserauth.Event{Method: "Network.responseReceived", Params: json.RawMessage(rrEvt)}:
		default:
		}
		lfEvt := fmt.Sprintf(`{"requestId":"%s"}`, rid)
		select {
		case cdp.events <- browserauth.Event{Method: "Network.loadingFinished", Params: json.RawMessage(lfEvt)}:
		default:
		}
		if cdp.responseBodies == nil {
			cdp.responseBodies = map[string]string{}
		}
		cdp.responseBodies[rid] = kimiSuccessBodyFixture()
	}
	err := RunKimiPage(kimiConsoleURL, kimiTestEnvelope())
	if err == nil {
		t.Fatal("RunKimiPage must NOT accept a 200 on an unrelated endpoint as auth")
	}
}

// TestRunKimiLoginCapturesBearerAndValidatesBeforeSave (task 4.2/4.3) proves
// the login captures a Bearer accessToken from a requestWillBeSentExtraInfo
// on the Kimi origin, validates it through the production quota parser path
// (the protected response with both meters), and returns the envelope — but
// only AFTER the browser is reaped. A token from a non-Kimi origin is dropped.
func TestRunKimiLoginCapturesBearerAndValidatesBeforeSave(t *testing.T) {
	originalLaunch := launchKimiBrowser
	defer func() { launchKimiBrowser = originalLaunch }()
	events := make(chan browserauth.Event, 4)
	// requestWillBeSent on the Kimi origin with the URL.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{"requestId":"r1","url":"https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"}`),
	}
	// ExtraInfo on the same requestId with the Bearer header.
	events <- browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"requestId":"r1","headers":{"authorization":"Bearer synthetic-kimi-access-token-1234567890"}}`),
	}
	close(events)
	cdp := &fakeKimiCDP{
		pageURL:       kimiConsoleURL,
		events:        events,
		snapshotValue: `{"l":{},"s":{}}`,
	}
	browser := &fakeKimiBrowser{cdp: cdp, exited: true}
	launchKimiBrowser = func(context.Context, string) (kimiLoginBrowser, error) {
		return browser, nil
	}
	validate := func(token string) bool { return token == "synthetic-kimi-access-token-1234567890" }
	env, err := runKimiLogin(context.Background(), browser, validate)
	if err != nil {
		t.Fatalf("runKimiLogin: %v", err)
	}
	if got := env.AccessToken(); got != "synthetic-kimi-access-token-1234567890" {
		t.Fatalf("token = %q", got)
	}
	if !browser.closed {
		t.Fatal("login browser must be closed before returning (reaped before validation)")
	}
}

// TestRunKimiLoginRejectsNonKimiOriginToken proves a Bearer token from a
// non-Kimi origin is dropped — an attacker cannot pre-seed a credential.
func TestRunKimiLoginRejectsNonKimiOriginToken(t *testing.T) {
	originalLaunch := launchKimiBrowser
	defer func() { launchKimiBrowser = originalLaunch }()
	events := make(chan browserauth.Event, 2)
	events <- browserauth.Event{
		Method: "Network.requestWillBeSent",
		Params: json.RawMessage(`{"requestId":"r1","url":"https://evil.example.com/api"}`),
	}
	events <- browserauth.Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"requestId":"r1","headers":{"authorization":"Bearer evil.token.12345678901234567890"}}`),
	}
	close(events)
	cdp := &fakeKimiCDP{pageURL: kimiConsoleURL, events: events, snapshotValue: `{"l":{},"s":{}}`}
	browser := &fakeKimiBrowser{cdp: cdp, exited: true}
	launchKimiBrowser = func(context.Context, string) (kimiLoginBrowser, error) {
		return browser, nil
	}
	validate := func(string) bool { return true }
	_, err := runKimiLogin(context.Background(), browser, validate)
	if err == nil {
		t.Fatal("runKimiLogin must reject a token from a non-Kimi origin")
	}
}

// TestRunKimiLoginReportsCancellationOnEarlyClose proves a browser close
// before any credential is captured returns a cancellation error and does
// NOT save or overwrite the account.
func TestRunKimiLoginReportsCancellationOnEarlyClose(t *testing.T) {
	originalLaunch := launchKimiBrowser
	defer func() { launchKimiBrowser = originalLaunch }()
	cdp := &fakeKimiCDP{pageURL: "about:blank", events: make(chan browserauth.Event, 1)}
	browser := &fakeKimiBrowser{cdp: cdp, exited: true}
	launchKimiBrowser = func(context.Context, string) (kimiLoginBrowser, error) {
		return browser, nil
	}
	_, err := runKimiLogin(context.Background(), browser, func(string) bool { return true })
	if err == nil {
		t.Fatal("runKimiLogin must error when the browser closes before a credential is captured")
	}
}

// kimiTestEnvelope builds a synthetic auth envelope for the page tests.
func kimiTestEnvelope() string {
	env := kimiAuthEnvelopeForTest()
	data, _ := env.Encode()
	return string(data)
}

// TestIsKimiMembershipPageStrict (task 4.4 exact membership URL) proves the
// account/data page check requires the EXACT host www.kimi.com, the EXACT path
// /membership/subscription, AND tab=quota. The previous check accepted a
// missing tab and used a confusing prefix condition; the spec pins the page to
// https://www.kimi.com/membership/subscription?tab=quota exactly.
func TestIsKimiMembershipPageStrict(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"exact", "https://www.kimi.com/membership/subscription?tab=quota", true},
		{"missing tab must reject", "https://www.kimi.com/membership/subscription", false},
		{"wrong tab must reject", "https://www.kimi.com/membership/subscription?tab=billing", false},
		{"trailing path must reject", "https://www.kimi.com/membership/subscription/extra?tab=quota", false},
		{"wrong host must reject", "https://evil.example.com/membership/subscription?tab=quota", false},
		{"console path must reject", "https://www.kimi.com/code/console", false},
		{"extra query ok", "https://www.kimi.com/membership/subscription?tab=quota&x=1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isKimiMembershipPage(c.url); got != c.want {
				t.Fatalf("isKimiMembershipPage(%q) = %v, want %v", c.url, got, c.want)
			}
		})
	}
}

// TestValidateKimiPageURLRequiresExactMembership (task 4.4 pre-replay URL)
// proves the account-page URL validator requires the EXACT membership page:
// host www.kimi.com, path /membership/subscription, tab=quota. The previous
// check accepted any path on the Kimi host (only scheme+host), so a replay
// could be pointed at /code/console or an arbitrary Kimi path. Replay must
// target exactly the membership quota page.
func TestValidateKimiPageURLRequiresExactMembership(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ok   bool
	}{
		{"exact", "https://www.kimi.com/membership/subscription?tab=quota", true},
		{"missing tab", "https://www.kimi.com/membership/subscription", false},
		{"wrong tab", "https://www.kimi.com/membership/subscription?tab=billing", false},
		{"console path rejected", "https://www.kimi.com/code/console", false},
		{"arbitrary path rejected", "https://www.kimi.com/something?tab=quota", false},
		{"trailing path rejected", "https://www.kimi.com/membership/subscription/extra?tab=quota", false},
		{"wrong host rejected", "https://evil.example.com/membership/subscription?tab=quota", false},
		{"non-https rejected", "http://www.kimi.com/membership/subscription?tab=quota", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateKimiPageURL(c.url)
			if c.ok && err != nil {
				t.Fatalf("validateKimiPageURL(%q) = %v, want nil", c.url, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("validateKimiPageURL(%q) = nil, want error", c.url)
			}
		})
	}
}
