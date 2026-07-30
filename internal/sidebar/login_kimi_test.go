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
	// eventsCloseOnce models the real death semantics exactly once: when the
	// one-shot browser process exits (Wait returns), the CDP read loop dies
	// and CLOSES the events channel (browserauth readLoop defer). The
	// round-8 watcher's post-stop drain terminates on that close.
	eventsCloseOnce sync.Once
}

func (b *fakeKimiBrowser) CDP(context.Context) (kimiCDP, error) { return b.cdp, nil }
func (b *fakeKimiBrowser) Exited() bool                         { return b.exited }
func (b *fakeKimiBrowser) Wait() error {
	if b.waitBlocks {
		<-b.waitRelease
	}
	b.eventsCloseOnce.Do(func() {
		if b.cdp != nil && b.cdp.events != nil {
			close(b.cdp.events)
		}
	})
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
	// localStorage models the page's window.localStorage for the
	// kimiReadLocalStorage Evaluate path (round-7 in-page rotation watcher):
	// Evaluate expressions containing localStorage.getItem("<key>") read from
	// this map; absent keys yield JSON null.
	localStorage map[string]string
	mu           sync.Mutex
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
	if strings.Contains(expression, "localStorage.getItem") {
		// kimiReadLocalStorage builds JSON.stringify(localStorage.getItem(<keyJSON>))
		// — extract the JSON-encoded key and answer from the fake storage map.
		key := ""
		if start := strings.Index(expression, "getItem("); start >= 0 {
			arg := expression[start+len("getItem("):]
			// The first ")" closes getItem( — a trailing ")" may close an
			// outer JSON.stringify( wrapper.
			if end := strings.Index(arg, ")"); end >= 0 {
				_ = json.Unmarshal([]byte(strings.TrimSpace(arg[:end])), &key)
			}
		}
		c.mu.Lock()
		stored, ok := c.localStorage[key]
		c.mu.Unlock()
		if !ok {
			return json.RawMessage(`{"result":{"value":"null"}}`), nil
		}
		inner, _ := json.Marshal(stored)
		raw, _ := json.Marshal(string(inner))
		return json.RawMessage(`{"result":{"value":` + string(raw) + `}}`), nil
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

// --- Round-7: in-page SPA token rotation capture (RED→GREEN) ---

// setLocalStorage writes a fake window.localStorage entry (race-safe) for the
// in-page rotation watcher tests.
func (c *fakeKimiCDP) setLocalStorage(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.localStorage == nil {
		c.localStorage = map[string]string{}
	}
	c.localStorage[key] = value
}

// kimiRotationEventSequence builds the CDP events evidencing an in-page token
// rotation: the SPA calls a protected API carrying a NEW Bearer token and the
// server answers with the given status. requestWillBeSent carries the URL;
// requestWillBeSentExtraInfo carries the Authorization header; loadingFinished
// completes the body so the watcher can validate it.
func kimiRotationEventSequence(requestID, newToken, url string, status int) []browserauth.Event {
	reqEvt := fmt.Sprintf(`{"requestId":%q,"url":%q,"request":{"url":%q,"headers":{}}}`, requestID, url, url)
	extraEvt := fmt.Sprintf(`{"requestId":%q,"headers":{"authorization":"Bearer %s"}}`, requestID, newToken)
	respEvt := fmt.Sprintf(`{"requestId":%q,"response":{"url":%q,"status":%d,"mimeType":"application/json"}}`, requestID, url, status)
	finEvt := fmt.Sprintf(`{"requestId":%q}`, requestID)
	return []browserauth.Event{
		{Method: "Network.requestWillBeSent", Params: json.RawMessage(reqEvt)},
		{Method: "Network.requestWillBeSentExtraInfo", Params: json.RawMessage(extraEvt)},
		{Method: "Network.responseReceived", Params: json.RawMessage(respEvt)},
		{Method: "Network.loadingFinished", Params: json.RawMessage(finEvt)},
	}
}

// pushKimiRotationSequence registers a valid two-meter response body for the
// request and pushes the full evidence chain onto the fake event channel.
func pushKimiRotationSequence(cdp *fakeKimiCDP, requestID, newToken, url string, status int) {
	cdp.mu.Lock()
	if cdp.responseBodies == nil {
		cdp.responseBodies = map[string]string{}
	}
	cdp.responseBodies[requestID] = kimiSuccessBodyFixture()
	cdp.mu.Unlock()
	for _, ev := range kimiRotationEventSequence(requestID, newToken, url, status) {
		cdp.events <- ev
	}
}

type kimiRotationCapture struct {
	prev, prevRefresh, newAccess, newRefresh string
}

// kimiRotationTestHook installs OpenPageReady + KimiPageRotationSave for a
// watcher test and returns the ready channel, the capture channel, and
// cleanup.
func kimiRotationTestHook(t *testing.T) (chan struct{}, chan kimiRotationCapture, func()) {
	t.Helper()
	originalReady := OpenPageReady
	originalSave := KimiPageRotationSave
	readyCh := make(chan struct{}, 1)
	OpenPageReady = func() {
		select {
		case readyCh <- struct{}{}:
		default:
		}
	}
	captures := make(chan kimiRotationCapture, 4)
	KimiPageRotationSave = func(prev, prevRefresh, newAccess, newRefresh string) (bool, error) {
		captures <- kimiRotationCapture{prev: prev, prevRefresh: prevRefresh, newAccess: newAccess, newRefresh: newRefresh}
		return true, nil
	}
	resetOpenPageErrorOnce()
	return readyCh, captures, func() {
		OpenPageReady = originalReady
		KimiPageRotationSave = originalSave
		resetOpenPageErrorOnce()
	}
}

// TestRunKimiPageCapturesSpaRotationAfterProtectedEvidence (round-7
// RED→GREEN) proves that when the membership SPA rotates the access token
// itself (access-token expiry → in-page refresh rotates BOTH tokens in
// localStorage), RunKimiPage captures the rotated pair and persists it via
// the installed save hook — evidenced by a 2xx protected /apiv2/ response
// carrying the NEW Bearer token, with the localStorage pair consistent with
// that token. RED: no watcher existed, so the page's in-flight rotation left
// the on-disk refresh token invalidated (next CLI/Web run forced re-login).
func TestRunKimiPageCapturesSpaRotationAfterProtectedEvidence(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	readyCh, captures, hookCleanup := kimiRotationTestHook(t)
	defer hookCleanup()
	browser.waitBlocks = true
	browser.waitRelease = make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- RunKimiPage(kimiConsoleURL, kimiTestEnvelope()) }()
	select {
	case <-readyCh:
	case <-time.After(2 * time.Second):
		t.Fatal("page did not signal ready")
	}

	// The SPA rotates in-page: localStorage now carries the rotated pair and
	// the retried protected call carries the NEW Bearer token (server: 200,
	// loadingFinished, valid two-meter body).
	cdp.setLocalStorage("access_token", "spa-rotated-access-1234567890")
	cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-1234567890")
	pushKimiRotationSequence(cdp, "rot1", "spa-rotated-access-1234567890", kimiProtectedQuotaURL, 200)

	select {
	case got := <-captures:
		if got.prev != "synthetic-bearer-jwt-1234567890" {
			t.Fatalf("prev = %q, want the replayed access token", got.prev)
		}
		if got.newAccess != "spa-rotated-access-1234567890" || got.newRefresh != "spa-rotated-refresh-1234567890" {
			t.Fatalf("captured = (%q, %q), want the SPA-rotated pair", got.newAccess, got.newRefresh)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-page rotation was NOT captured: the SPA-rotated pair must persist via the save hook (RED: no watcher)")
	}

	browser.waitRelease <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("RunKimiPage returned error after user close: %v", err)
	}
}

// TestRunKimiPageSkipsRotationWithoutProtectedEvidence (round-7 safety) proves
// the watcher does NOT persist localStorage blindly: no capture when the new
// token only appears on a non-protected URL, when the protected call is
// rejected (401), or when localStorage disagrees with the evidenced token.
func TestRunKimiPageSkipsRotationWithoutProtectedEvidence(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		status   int
		lsAccess string
	}{
		{
			name:     "non-protected URL outside apiv2",
			url:      "https://www.kimi.com/api/public/ping",
			status:   200,
			lsAccess: "spa-rotated-access-1234567890",
		},
		{
			// Round-8: the /apiv2/ prefix alone MUST NOT count as evidence —
			// only the exact GetSubscriptionStats host/path does.
			name:     "unrelated endpoint inside apiv2 namespace",
			url:      "https://www.kimi.com/apiv2/kimi.gateway.account.v1.AccountService/GetProfile",
			status:   200,
			lsAccess: "spa-rotated-access-1234567890",
		},
		{
			name:     "protected call rejected 401",
			url:      kimiProtectedQuotaURL,
			status:   401,
			lsAccess: "spa-rotated-access-1234567890",
		},
		{
			name:     "localStorage disagrees with evidenced token",
			url:      kimiProtectedQuotaURL,
			status:   200,
			lsAccess: "some-other-access-0000000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cdp, browser, cleanup := kimiPageTestSetup(t)
			defer cleanup()
			readyCh, captures, hookCleanup := kimiRotationTestHook(t)
			defer hookCleanup()
			browser.waitBlocks = true
			browser.waitRelease = make(chan struct{})

			done := make(chan error, 1)
			go func() { done <- RunKimiPage(kimiConsoleURL, kimiTestEnvelope()) }()
			select {
			case <-readyCh:
			case <-time.After(2 * time.Second):
				t.Fatal("page did not signal ready")
			}

			cdp.setLocalStorage("access_token", tc.lsAccess)
			cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-1234567890")
			pushKimiRotationSequence(cdp, "sk1", "spa-rotated-access-1234567890", tc.url, tc.status)

			select {
			case got := <-captures:
				t.Fatalf("save hook fired (%+v) without valid protected-response evidence — localStorage must not be trusted blindly", got)
			case <-time.After(400 * time.Millisecond):
				// expected: no capture
			}
			browser.waitRelease <- struct{}{}
			<-done
		})
	}
}

// TestRunKimiPageChainsSequentialRotations (round-7) proves a long page
// session captures MULTIPLE in-page rotations in order, with prev chaining
// (second capture's prev is the first rotated token).
func TestRunKimiPageChainsSequentialRotations(t *testing.T) {
	cdp, browser, cleanup := kimiPageTestSetup(t)
	defer cleanup()
	readyCh, captures, hookCleanup := kimiRotationTestHook(t)
	defer hookCleanup()
	browser.waitBlocks = true
	browser.waitRelease = make(chan struct{})

	done := make(chan error, 1)
	go func() { done <- RunKimiPage(kimiConsoleURL, kimiTestEnvelope()) }()
	select {
	case <-readyCh:
	case <-time.After(2 * time.Second):
		t.Fatal("page did not signal ready")
	}

	// First rotation.
	cdp.setLocalStorage("access_token", "spa-rotated-access-1-1234567890")
	cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-1-1234567890")
	pushKimiRotationSequence(cdp, "rot1", "spa-rotated-access-1-1234567890", kimiProtectedQuotaURL, 200)
	select {
	case got := <-captures:
		if got.prev != "synthetic-bearer-jwt-1234567890" || got.newAccess != "spa-rotated-access-1-1234567890" {
			t.Fatalf("first capture = (%q → %q), want replayed → rotated-1", got.prev, got.newAccess)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first in-page rotation not captured")
	}

	// Second rotation (access expires again in a long session).
	cdp.setLocalStorage("access_token", "spa-rotated-access-2-1234567890")
	cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-2-1234567890")
	pushKimiRotationSequence(cdp, "rot2", "spa-rotated-access-2-1234567890", kimiProtectedQuotaURL, 200)
	select {
	case got := <-captures:
		if got.prev != "spa-rotated-access-1-1234567890" {
			t.Fatalf("second capture prev = %q, want rotated-1 (chain)", got.prev)
		}
		if got.newAccess != "spa-rotated-access-2-1234567890" || got.newRefresh != "spa-rotated-refresh-2-1234567890" {
			t.Fatalf("second capture = (%q, %q), want rotated-2 pair", got.newAccess, got.newRefresh)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second in-page rotation not captured")
	}

	browser.waitRelease <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("RunKimiPage returned error after user close: %v", err)
	}
}

// TestKimiWatcherDrainsQueuedRotationAfterStop (round-8 close-race
// RED→GREEN) proves the watcher does NOT let stop preempt queued rotation
// evidence: when the window close fires stop while a rotation's events are
// still in flight (the read loop forwards them before the connection dies),
// the watcher keeps processing until the events channel CLOSES — the save
// hook still fires. Deterministic: stop is closed FIRST with an empty
// channel (the old return-on-stop code path exits immediately), and only
// then is the evidence chain delivered; the channel close (browser death)
// terminates the drain.
func TestKimiWatcherDrainsQueuedRotationAfterStop(t *testing.T) {
	cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
	cdp.setLocalStorage("access_token", "spa-rotated-access-1234567890")
	cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-1234567890")

	stop := make(chan struct{})
	close(stop) // window closed BEFORE the rotation events arrive

	captured := make(chan kimiRotationCapture, 1)
	save := func(prev, prevRefresh, newAccess, newRefresh string) (bool, error) {
		captured <- kimiRotationCapture{prev: prev, prevRefresh: prevRefresh, newAccess: newAccess, newRefresh: newRefresh}
		return true, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		kimiWatchInPageRotation(context.Background(), cdp, cdp.events, "synthetic-bearer-jwt-1234567890", "synthetic-refresh-jwt-1234567890", stop, save)
	}()

	// The rotation evidence chain arrives AFTER stop (read loop forwarded it
	// while the connection was dying), then the channel closes (browser dead).
	pushKimiRotationSequence(cdp, "rot-close", "spa-rotated-access-1234567890", kimiProtectedQuotaURL, 200)
	close(cdp.events)

	select {
	case got := <-captured:
		if got.prev != "synthetic-bearer-jwt-1234567890" || got.newAccess != "spa-rotated-access-1234567890" || got.newRefresh != "spa-rotated-refresh-1234567890" {
			t.Fatalf("captured = %+v, want the SPA-rotated pair with replayed prev", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued rotation dropped on stop: watcher must keep processing queued evidence until the events channel closes (RED: stop preempted)")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not exit after the events channel closed")
	}
}

// TestKimiWatcherCapturesWhenExtraInfoArrivesAfterResponse (round-8 event
// ordering RED→GREEN) proves the watcher tolerates CDP event reordering:
// requestWillBeSentExtraInfo (which carries the Authorization header) may
// arrive AFTER Network.responseReceived for the same request. The evidence
// chain must be evaluated at loadingFinished from accumulated facts, not
// armed only when the token is seen before the response. RED (state-machine
// version): responseReceived arrived unarmed, the URL/token facts were
// dropped, and the late ExtraInfo could not re-arm — capture missed.
func TestKimiWatcherCapturesWhenExtraInfoArrivesAfterResponse(t *testing.T) {
	cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
	cdp.setLocalStorage("access_token", "spa-rotated-access-1234567890")
	cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-1234567890")

	// Reordered chain: requestWillBeSent → responseReceived → ExtraInfo →
	// loadingFinished (ExtraInfo AFTER the response).
	reqEvt := fmt.Sprintf(`{"requestId":"reord1","url":%q,"request":{"url":%q,"headers":{}}}`, kimiProtectedQuotaURL, kimiProtectedQuotaURL)
	respEvt := fmt.Sprintf(`{"requestId":"reord1","response":{"url":%q,"status":200,"mimeType":"application/json"}}`, kimiProtectedQuotaURL)
	extraEvt := `{"requestId":"reord1","headers":{"authorization":"Bearer spa-rotated-access-1234567890"}}`
	finEvt := `{"requestId":"reord1"}`
	cdp.mu.Lock()
	cdp.responseBodies = map[string]string{"reord1": kimiSuccessBodyFixture()}
	cdp.mu.Unlock()
	for _, raw := range []struct{ method, params string }{
		{"Network.requestWillBeSent", reqEvt},
		{"Network.responseReceived", respEvt},
		{"Network.requestWillBeSentExtraInfo", extraEvt},
		{"Network.loadingFinished", finEvt},
	} {
		cdp.events <- browserauth.Event{Method: raw.method, Params: json.RawMessage(raw.params)}
	}

	captured := make(chan kimiRotationCapture, 1)
	save := func(prev, prevRefresh, newAccess, newRefresh string) (bool, error) {
		captured <- kimiRotationCapture{prev: prev, prevRefresh: prevRefresh, newAccess: newAccess, newRefresh: newRefresh}
		return true, nil
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		kimiWatchInPageRotation(context.Background(), cdp, cdp.events, "synthetic-bearer-jwt-1234567890", "synthetic-refresh-jwt-1234567890", stop, save)
	}()

	select {
	case got := <-captured:
		if got.prev != "synthetic-bearer-jwt-1234567890" || got.newAccess != "spa-rotated-access-1234567890" || got.newRefresh != "spa-rotated-refresh-1234567890" {
			t.Fatalf("captured = %+v, want the SPA-rotated pair", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rotation missed when ExtraInfo arrived after responseReceived — facts must accumulate in any order and evaluate at loadingFinished")
	}
	close(stop)
	close(cdp.events)
	<-done
}

// --- Round-8 adjudicated design: RefreshToken response = authoritative issuance ---

const kimiTestRefreshURL = "https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken"

// kimiRefreshChain builds the RefreshToken evidence chain events: the exact
// refresh endpoint request, a responseReceived with the given status, and
// loadingFinished. The response body is registered separately.
func kimiRefreshChain(requestID string, status int) []browserauth.Event {
	reqEvt := fmt.Sprintf(`{"requestId":%q,"url":%q,"request":{"url":%q,"headers":{"content-type":"application/json"}}}`, requestID, kimiTestRefreshURL, kimiTestRefreshURL)
	respEvt := fmt.Sprintf(`{"requestId":%q,"response":{"url":%q,"status":%d,"mimeType":"application/json"}}`, requestID, kimiTestRefreshURL, status)
	finEvt := fmt.Sprintf(`{"requestId":%q}`, requestID)
	return []browserauth.Event{
		{Method: "Network.requestWillBeSent", Params: json.RawMessage(reqEvt)},
		{Method: "Network.responseReceived", Params: json.RawMessage(respEvt)},
		{Method: "Network.loadingFinished", Params: json.RawMessage(finEvt)},
	}
}

// kimiWatcherTestRig starts the watcher directly with a fake CDP and returns
// the capture channel + stop handles.
func kimiWatcherTestRig(cdp *fakeKimiCDP) (chan kimiRotationCapture, chan struct{}, chan struct{}) {
	captured := make(chan kimiRotationCapture, 4)
	save := func(prev, prevRefresh, newAccess, newRefresh string) (bool, error) {
		captured <- kimiRotationCapture{prev: prev, prevRefresh: prevRefresh, newAccess: newAccess, newRefresh: newRefresh}
		return true, nil
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		kimiWatchInPageRotation(context.Background(), cdp, cdp.events, "synthetic-bearer-jwt-1234567890", "synthetic-refresh-jwt-1234567890", stop, save)
	}()
	return captured, stop, done
}

// TestKimiWatcherPersistsMemoryRotationFromRefreshResponse (round-8
// adjudicated RED→GREEN: 内存型轮换成功) proves the exact RefreshToken
// response chain — exact URL + 2xx + loadingFinished + strictly-parsed
// non-empty accessToken/refreshToken — is authoritative issuance evidence and
// is CAS-persisted IMMEDIATELY, WITHOUT any localStorage involvement (the
// memory-type rotation observed in the real session) and WITHOUT waiting for
// a later quota call (a close between the two events would lose the pair).
func TestKimiWatcherPersistsMemoryRotationFromRefreshResponse(t *testing.T) {
	cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
	// NO localStorage entries at all: the SPA kept the rotated pair in memory.
	cdp.mu.Lock()
	cdp.responseBodies = map[string]string{
		"ref1": `{"accessToken":"spa-issued-access-606-AAAAAAAAAAAA","refreshToken":"spa-issued-refresh-607-BBBBBBBBBBBB"}`,
	}
	cdp.mu.Unlock()
	captured, stop, done := kimiWatcherTestRig(cdp)
	defer func() { close(stop); close(cdp.events); <-done }()

	for _, ev := range kimiRefreshChain("ref1", 200) {
		cdp.events <- ev
	}

	select {
	case got := <-captured:
		if got.prev != "synthetic-bearer-jwt-1234567890" || got.prevRefresh != "synthetic-refresh-jwt-1234567890" {
			t.Fatalf("prev pair = (%q, %q), want the replayed pair", got.prev, got.prevRefresh)
		}
		if got.newAccess != "spa-issued-access-606-AAAAAAAAAAAA" || got.newRefresh != "spa-issued-refresh-607-BBBBBBBBBBBB" {
			t.Fatalf("captured = (%q, %q), want the server-issued pair", got.newAccess, got.newRefresh)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshToken issuance evidence was NOT persisted immediately (RED: no refresh-response capture path)")
	}
}

// TestKimiWatcherSkipsRefreshBusinessOrMalformedBody (round-8 RED→GREEN:
// refresh 2xx 业务/畸形体) proves a 2xx RefreshToken response whose body is a
// business error, malformed JSON, missing a field, empty, or oversize is NOT
// persisted — 2xx alone is not evidence.
func TestKimiWatcherSkipsRefreshBusinessOrMalformedBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"business error body", `{"code":"INVALID_TOKEN","message":"refresh token expired"}`},
		{"malformed JSON", `{"accessToken":`},
		{"missing refreshToken", `{"accessToken":"spa-issued-access-606-AAAAAAAAAAAA"}`},
		{"empty accessToken", `{"accessToken":"","refreshToken":"spa-issued-refresh-607-BBBBBBBBBBBB"}`},
		{"oversize body", `{"accessToken":"` + strings.Repeat("A", 70<<10) + `","refreshToken":"r"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
			cdp.mu.Lock()
			cdp.responseBodies = map[string]string{"ref1": tc.body}
			cdp.mu.Unlock()
			captured, stop, done := kimiWatcherTestRig(cdp)
			defer func() { close(stop); close(cdp.events); <-done }()

			for _, ev := range kimiRefreshChain("ref1", 200) {
				cdp.events <- ev
			}
			select {
			case got := <-captured:
				t.Fatalf("save fired (%+v) on a non-authoritative RefreshToken body — strict parse must reject it", got)
			case <-time.After(400 * time.Millisecond):
				// expected: no capture
			}
		})
	}
}

// TestKimiWatcherSkipsRefreshResponseNon2xx proves a rejected RefreshToken
// call (401) is not evidence even with a parseable body registered.
func TestKimiWatcherSkipsRefreshResponseNon2xx(t *testing.T) {
	cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
	cdp.mu.Lock()
	cdp.responseBodies = map[string]string{
		"ref1": `{"accessToken":"spa-issued-access-606-AAAAAAAAAAAA","refreshToken":"spa-issued-refresh-607-BBBBBBBBBBBB"}`,
	}
	cdp.mu.Unlock()
	captured, stop, done := kimiWatcherTestRig(cdp)
	defer func() { close(stop); close(cdp.events); <-done }()

	for _, ev := range kimiRefreshChain("ref1", 401) {
		cdp.events <- ev
	}
	select {
	case got := <-captured:
		t.Fatalf("save fired (%+v) on a 401 RefreshToken response", got)
	case <-time.After(400 * time.Millisecond):
		// expected: no capture
	}
}

// TestKimiWatcherSkipsRefreshSameAccessToken (round-8: token 不匹配) proves a
// RefreshToken response whose accessToken equals the CURRENT token is not a
// rotation — no persist.
func TestKimiWatcherSkipsRefreshSameAccessToken(t *testing.T) {
	cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
	cdp.mu.Lock()
	cdp.responseBodies = map[string]string{
		"ref1": `{"accessToken":"synthetic-bearer-jwt-1234567890","refreshToken":"some-other-refresh-1234567890"}`,
	}
	cdp.mu.Unlock()
	captured, stop, done := kimiWatcherTestRig(cdp)
	defer func() { close(stop); close(cdp.events); <-done }()

	for _, ev := range kimiRefreshChain("ref1", 200) {
		cdp.events <- ev
	}
	select {
	case got := <-captured:
		t.Fatalf("save fired (%+v) when the issued access equals the current token — not a rotation", got)
	case <-time.After(400 * time.Millisecond):
		// expected: no capture
	}
}

// TestKimiWatcherChainsRefreshRotations proves multiple sequential in-page
// refreshes chain correctly (prev of rotation N+1 is the pair issued in N).
func TestKimiWatcherChainsRefreshRotations(t *testing.T) {
	cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 16)}
	cdp.mu.Lock()
	cdp.responseBodies = map[string]string{
		"ref1": `{"accessToken":"spa-issued-access-1-AAAAAAAAAAAA","refreshToken":"spa-issued-refresh-1-BBBBBBBBBBBB"}`,
		"ref2": `{"accessToken":"spa-issued-access-2-CCCCCCCCCCCC","refreshToken":"spa-issued-refresh-2-DDDDDDDDDDDD"}`,
	}
	cdp.mu.Unlock()
	captured, stop, done := kimiWatcherTestRig(cdp)
	defer func() { close(stop); close(cdp.events); <-done }()

	for _, ev := range kimiRefreshChain("ref1", 200) {
		cdp.events <- ev
	}
	select {
	case got := <-captured:
		if got.prev != "synthetic-bearer-jwt-1234567890" || got.newAccess != "spa-issued-access-1-AAAAAAAAAAAA" {
			t.Fatalf("first capture = (%q → %q)", got.prev, got.newAccess)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh rotation not captured")
	}
	for _, ev := range kimiRefreshChain("ref2", 200) {
		cdp.events <- ev
	}
	select {
	case got := <-captured:
		if got.prev != "spa-issued-access-1-AAAAAAAAAAAA" || got.prevRefresh != "spa-issued-refresh-1-BBBBBBBBBBBB" {
			t.Fatalf("second capture prev = (%q, %q), want the first issued pair", got.prev, got.prevRefresh)
		}
		if got.newAccess != "spa-issued-access-2-CCCCCCCCCCCC" || got.newRefresh != "spa-issued-refresh-2-DDDDDDDDDDDD" {
			t.Fatalf("second capture = (%q, %q)", got.newAccess, got.newRefresh)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second refresh rotation not captured")
	}
}

// --- Round-9: cross host/path redirect is never evidence ---

// kimiRefreshChainWithResponseURL builds the refresh evidence chain where the
// response's FINAL url differs from the request URL (a redirect chain whose
// last hop left the allowlist).
func kimiRefreshChainWithResponseURL(requestID string, status int, responseURL string) []browserauth.Event {
	reqEvt := fmt.Sprintf(`{"requestId":%q,"url":%q,"request":{"url":%q,"headers":{"content-type":"application/json"}}}`, requestID, kimiTestRefreshURL, kimiTestRefreshURL)
	respEvt := fmt.Sprintf(`{"requestId":%q,"response":{"url":%q,"status":%d,"mimeType":"application/json"}}`, requestID, responseURL, status)
	finEvt := fmt.Sprintf(`{"requestId":%q}`, requestID)
	return []browserauth.Event{
		{Method: "Network.requestWillBeSent", Params: json.RawMessage(reqEvt)},
		{Method: "Network.responseReceived", Params: json.RawMessage(respEvt)},
		{Method: "Network.loadingFinished", Params: json.RawMessage(finEvt)},
	}
}

// TestKimiWatcherSkipsRefreshRedirectedResponse (round-9 RED→GREEN) proves
// that an exact RefreshToken REQUEST whose 2xx response's FINAL URL left the
// allowlist (cross host/path redirect) is NOT authoritative issuance
// evidence — even with a strictly valid body. RED: only the request URL was
// checked, so the redirected 2xx persisted the pair.
func TestKimiWatcherSkipsRefreshRedirectedResponse(t *testing.T) {
	cases := []struct {
		name        string
		responseURL string
	}{
		{"redirect to foreign host", "https://auth.kimi.com.evil.example/api/account.gateway.v1.AuthService/RefreshToken"},
		{"redirect to different path on same host", "https://auth.kimi.com/api/account.gateway.v2.AuthService/RefreshToken"},
		{"redirect to http downgrade", "http://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
			cdp.mu.Lock()
			cdp.responseBodies = map[string]string{
				"ref1": `{"accessToken":"spa-issued-access-606-AAAAAAAAAAAA","refreshToken":"spa-issued-refresh-607-BBBBBBBBBBBB"}`,
			}
			cdp.mu.Unlock()
			captured, stop, done := kimiWatcherTestRig(cdp)
			defer func() { close(stop); close(cdp.events); <-done }()

			for _, ev := range kimiRefreshChainWithResponseURL("ref1", 200, tc.responseURL) {
				cdp.events <- ev
			}
			select {
			case got := <-captured:
				t.Fatalf("save fired (%+v) on a redirected RefreshToken 2xx — the final response URL must also match the allowlist exactly", got)
			case <-time.After(400 * time.Millisecond):
				// expected: no capture
			}
		})
	}
}

// TestKimiWatcherSkipsQuotaRedirectedResponse (round-9 RED→GREEN) proves the
// same final-URL gate on the quota/localStorage path: an exact
// GetSubscriptionStats REQUEST whose 2xx response's FINAL URL left the
// allowlist is NOT quota evidence — even with a valid body and a consistent
// localStorage pair.
func TestKimiWatcherSkipsQuotaRedirectedResponse(t *testing.T) {
	cases := []struct {
		name        string
		responseURL string
	}{
		{"redirect to foreign host", "https://www.kimi.com.evil.example/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"},
		{"redirect to different path on same host", "https://www.kimi.com/apiv2/kimi.gateway.membership.v3.MembershipService/GetSubscriptionStats"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cdp := &fakeKimiCDP{events: make(chan browserauth.Event, 8)}
			cdp.setLocalStorage("access_token", "spa-rotated-access-1234567890")
			cdp.setLocalStorage("refresh_token", "spa-rotated-refresh-1234567890")
			cdp.mu.Lock()
			cdp.responseBodies = map[string]string{"q1": kimiSuccessBodyFixture()}
			cdp.mu.Unlock()
			captured, stop, done := kimiWatcherTestRig(cdp)
			defer func() { close(stop); close(cdp.events); <-done }()

			reqEvt := fmt.Sprintf(`{"requestId":"q1","url":%q,"request":{"url":%q,"headers":{}}}`, kimiProtectedQuotaURL, kimiProtectedQuotaURL)
			extraEvt := `{"requestId":"q1","headers":{"authorization":"Bearer spa-rotated-access-1234567890"}}`
			respEvt := fmt.Sprintf(`{"requestId":"q1","response":{"url":%q,"status":200,"mimeType":"application/json"}}`, tc.responseURL)
			finEvt := `{"requestId":"q1"}`
			for _, raw := range []struct{ method, params string }{
				{"Network.requestWillBeSent", reqEvt},
				{"Network.requestWillBeSentExtraInfo", extraEvt},
				{"Network.responseReceived", respEvt},
				{"Network.loadingFinished", finEvt},
			} {
				cdp.events <- browserauth.Event{Method: raw.method, Params: json.RawMessage(raw.params)}
			}
			select {
			case got := <-captured:
				t.Fatalf("save fired (%+v) on a redirected quota 2xx — the final response URL must also match the allowlist exactly", got)
			case <-time.After(400 * time.Millisecond):
				// expected: no capture
			}
		})
	}
}
