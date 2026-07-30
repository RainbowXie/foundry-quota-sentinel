package sidebar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/quota"
)

// kimiHost is the canonical Kimi consumer domain. The coordinator restricts
// captured credentials and page URLs to this host so unrelated domains
// cannot leak through.
const kimiHost = "www.kimi.com"

// kimiLoginURL is where the temporary login browser starts. The OBSERVED Kimi
// console is a client-side-gated SPA (no HTTP redirect for unauthenticated
// visitors), so starting at the console surfaces the login prompt in-page.
const kimiLoginURL = "https://www.kimi.com/code/console"

// kimiMembershipURL is the authoritative membership quota page (the new
// account/data page), the OBSERVED target for saved-account replay.
const kimiMembershipURL = "https://www.kimi.com/membership/subscription?tab=quota"

// kimiConsoleURL kept as an alias for the membership page for backward
// compatibility with existing tests; the account/data page is now the
// membership quota page, not /code/console.
var kimiConsoleURL = kimiMembershipURL

// kimiProtectedQuotaURL is the OBSERVED protected quota endpoint the SPA
// calls when authenticated. Mirrors the constant in internal/quota/kimi_web.go;
// duplicated here because the sidebar must not import the quota package's
// private constant, and the URL is the provider contract both layers share.
const kimiProtectedQuotaURL = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"

// kimiRefreshTokenURL is the OBSERVED durable refresh endpoint the SPA itself
// calls to rotate the session pair (POST, refresh_token in the body, returns
// new accessToken + refreshToken). Mirrors the constant in
// internal/quota/kimi_web.go; duplicated here for the same reason as
// kimiProtectedQuotaURL. The watcher treats a 2xx loadingFinished response
// with a strictly-parsed pair as authoritative issuance evidence.
const kimiRefreshTokenURL = "https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken"

// kimiRefreshBodyMaxBytes bounds the RefreshToken response body read (the
// real body is ~1.5KB of JSON carrying two JWTs); anything larger is not a
// legitimate issuance response.
const kimiRefreshBodyMaxBytes = 64 << 10

// kimiIssuedTokenMaxLen bounds each parsed token (real JWTs are ~0.5-1KB).
const kimiIssuedTokenMaxLen = 4096

// kimiSettleTimeout is the deadline for waiting on observable CDP events
// during the account-page auth-decision wait.
var kimiSettleTimeout = 8 * time.Second

// KimiPageRotationSave, when set, is invoked by runKimiPage's in-page
// rotation watcher with (prevAccess, prevRefresh, newAccess, newRefresh)
// after the membership SPA rotates the token pair itself. It returns
// persisted=true when the pair was written, false when the compare-and-swap
// skipped because disk already moved ahead — the watcher logs persisted vs
// skipped truthfully. cmdOpenPage installs the per-account closure; nil
// disables the watcher (tests, non-CLI callers).
var KimiPageRotationSave func(prevAccess, prevRefresh, newAccess, newRefresh string) (persisted bool, err error)

// kimiAuthTimeoutError is the sentinel returned by kimiWaitForAuthDecision
// when the deadline elapses WITHOUT any fatal condition. RunKimiPage treats
// ONLY this exact sentinel as "no protected response observed in time". Every
// other error (channel close, ctx cancel, PageURL/parse failure, business
// error) is fatal and propagates through failAndWait. A typed error instead
// of strings.Contains means a future error message containing "超时" cannot
// be misclassified.
type kimiAuthTimeoutError struct{}

func (e *kimiAuthTimeoutError) Error() string { return "等待 Kimi 鉴权决定超时" }

func isKimiExpectedTimeout(err error) bool {
	var target *kimiAuthTimeoutError
	return errors.As(err, &target)
}

// kimiCDP is the coordinator's narrow view of the shared client. It mirrors
// deepSeekCDP; Kimi needs the same Network/Page primitives plus
// GetResponseBody to verify the protected Connect response carries the two
// meters (no "code" string).
type kimiCDP interface {
	EnableNetwork(context.Context) error
	EnablePage(context.Context) error
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	PageURL(ctx context.Context, allowedHosts ...string) (string, error)
	Events() <-chan browserauth.Event
	Evaluate(ctx context.Context, expression string) (json.RawMessage, error)
	AddScriptOnNewDocument(ctx context.Context, script string) error
	Navigate(ctx context.Context, pageURL string, allowedHosts ...string) error
	NavigateWithLoader(ctx context.Context, pageURL string, allowedHosts ...string) (string, error)
	SetCookiesBestEffort(ctx context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult
	GetResponseBody(ctx context.Context, requestID string) (string, error)
	Close() error
}

type kimiLoginBrowser interface {
	CDP(ctx context.Context) (kimiCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

var launchKimiBrowser = func(ctx context.Context, pageURL string) (kimiLoginBrowser, error) {
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: pageURL})
	if err != nil {
		return nil, err
	}
	return &sharedKimiBrowser{Browser: browser}, nil
}

type sharedKimiBrowser struct {
	*browserauth.Browser
}

func (b *sharedKimiBrowser) CDP(ctx context.Context) (kimiCDP, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			return &sharedKimiClient{Connection: conn}, nil
		}
		if b.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("等待登录浏览器就绪超时: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

type sharedKimiClient struct {
	*browserauth.Connection
}

func (c *sharedKimiClient) EnableNetwork(ctx context.Context) error {
	return c.Page().EnableNetwork(ctx)
}
func (c *sharedKimiClient) EnablePage(ctx context.Context) error { return c.Page().EnablePage(ctx) }
func (c *sharedKimiClient) BrowserCookies(ctx context.Context) ([]browserauth.Cookie, error) {
	return c.Browser().BrowserCookies(ctx)
}
func (c *sharedKimiClient) PageURL(ctx context.Context, allowedHosts ...string) (string, error) {
	return c.Page().PageURL(ctx, allowedHosts...)
}
func (c *sharedKimiClient) Events() <-chan browserauth.Event { return c.Page().Events() }
func (c *sharedKimiClient) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	return c.Page().Evaluate(ctx, expression)
}
func (c *sharedKimiClient) AddScriptOnNewDocument(ctx context.Context, script string) error {
	return c.Page().AddScriptOnNewDocument(ctx, script)
}
func (c *sharedKimiClient) Navigate(ctx context.Context, pageURL string, allowedHosts ...string) error {
	return c.Page().Navigate(ctx, pageURL, allowedHosts...)
}
func (c *sharedKimiClient) NavigateWithLoader(ctx context.Context, pageURL string, allowedHosts ...string) (string, error) {
	return c.Page().NavigateWithLoader(ctx, pageURL, allowedHosts...)
}
func (c *sharedKimiClient) SetCookiesBestEffort(ctx context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult {
	return c.Browser().SetCookiesBestEffort(ctx, cookies)
}
func (c *sharedKimiClient) GetResponseBody(ctx context.Context, requestID string) (string, error) {
	raw, err := c.Page().Call(ctx, "Network.getResponseBody", map[string]any{"requestId": requestID})
	if err != nil {
		return "", err
	}
	var env struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("解析响应体失败: %w", err)
	}
	return env.Body, nil
}

// RunKimiLogin launches the shared browser at the Kimi console, captures the
// Bearer accessToken (plus the cookie header and the stable browser headers)
// from a Network header event on the protected Kimi request, settles, then
// closes the browser before asking the caller to validate the candidate token
// through the production quota path. Returns the filled versioned envelope.
func RunKimiLogin(validate func(accessToken string) bool) (config.KimiAuthEnvelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchKimiBrowser(ctx, kimiLoginURL)
	if err != nil {
		return config.KimiAuthEnvelope{}, err
	}
	return runKimiLogin(ctx, browser, validate)
}

// runKimiLogin is the coordinator core. It collects Bearer candidates from
// Network header events whose requestId maps to a Kimi-origin URL, settling
// once a candidate is available, then closes the browser and validates each
// candidate through the caller-supplied closure. The envelope it returns
// carries the Bearer accessToken, the Kimi cookie header, and the stable
// browser headers observed on the protected request — all allowlisted.
func runKimiLogin(ctx context.Context, browser kimiLoginBrowser, validate func(string) bool) (envelope config.KimiAuthEnvelope, err error) {
	defer func() {
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 Kimi 登录浏览器失败: %w", closeErr)
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return config.KimiAuthEnvelope{}, fmt.Errorf("连接 Kimi 登录浏览器失败: %w", err)
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		_ = cdp.Close()
		return config.KimiAuthEnvelope{}, fmt.Errorf("启用 Kimi 网络事件失败: %w", err)
	}

	events := cdp.Events()
	// candidates maps a Bearer token to the full request headers captured on
	// the matching Kimi-origin request (so the envelope can replay the stable
	// browser headers, not just the token).
	type candidate struct {
		token   string
		headers map[string]string
	}
	candidates := make(map[string]candidate)
	urls := make(map[string]string)               // requestId → URL from requestWillBeSent
	pending := make(map[string]map[string]string) // token → headers awaiting URL
	pendingToken := make(map[string]string)       // token → requestId
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			decoded, dOK := browserauth.DecodeRequestHeadersEvent(event)
			if !dOK {
				continue
			}
			requestID, requestURL := decoded.RequestID, decoded.URL
			headers := decoded.Headers
			token := browserauth.BearerToken(headers)
			if requestID != "" && requestURL != "" {
				urls[requestID] = requestURL
				for t, h := range pending {
					if pendingToken[t] == requestID && isOnKimiHost(requestURL) {
						candidates[t] = candidate{token: t, headers: h}
						delete(pending, t)
						delete(pendingToken, t)
					}
				}
			}
			if token == "" || requestID == "" {
				continue
			}
			if u, ok := urls[requestID]; ok {
				if isOnKimiHost(u) {
					candidates[token] = candidate{token: token, headers: headers}
					delete(pending, token)
					delete(pendingToken, token)
				}
			} else {
				pending[token] = headers
				pendingToken[token] = requestID
			}
		case <-poll.C:
		}

		// Promote pending tokens whose URL is now known and on Kimi.
		pageURL, urlErr := cdp.PageURL(ctx, kimiHost)
		if urlErr == nil && pageURL != "" {
			for t, h := range pending {
				rid := pendingToken[t]
				if u, ok := urls[rid]; ok && isOnKimiHost(u) {
					candidates[t] = candidate{token: t, headers: h}
					delete(pending, t)
					delete(pendingToken, t)
				}
			}
		}

		if len(candidates) > 0 {
			break
		}
		if browser.Exited() {
			if len(candidates) > 0 {
				break
			}
			return config.KimiAuthEnvelope{}, fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return config.KimiAuthEnvelope{}, fmt.Errorf("未捕获到有效凭证（登录超时或已取消）")
		default:
		}
	}

	// Capture cookies BEFORE closing the CDP connection / browser — once the
	// connection is closed BrowserCookies fails. Best-effort: the envelope
	// allowlist decides what persists, and a cookie-capture failure does not
	// block a token-only replay.
	capturedCookies, _ := cdp.BrowserCookies(ctx)
	cookieHeader := kimiCookieHeader(capturedCookies)
	// Capture the durable refresh_token + access_token from localStorage (the
	// SPA stores refresh_token in localStorage["refresh_token"], NOT in request
	// headers — this is the durable session the querier needs for auto-refresh).
	refreshToken := kimiReadLocalStorage(ctx, cdp, "refresh_token")
	_ = cdp.Close()
	if err := browser.Close(); err != nil {
		return config.KimiAuthEnvelope{}, fmt.Errorf("关闭 Kimi 登录浏览器失败: %w", err)
	}

	for _, c := range candidates {
		if validate(c.token) {
			return kimiBuildEnvelope(c.token, refreshToken, cookieHeader, c.headers), nil
		}
	}
	return config.KimiAuthEnvelope{}, fmt.Errorf("未找到可验证的 Kimi 凭证")
}

// kimiReadLocalStorage reads a single localStorage key value via
// Runtime.evaluate. Returns "" on any error (best-effort capture). The value
// is the durable session credential (e.g. refresh_token), stored in the
// allowlisted envelope — never logged.
func kimiReadLocalStorage(ctx context.Context, cdp kimiCDP, key string) string {
	// JSON.stringify the value so returnByValue delivers a JSON string we can
	// unwrap, and escape the key safely.
	keyJSON, _ := json.Marshal(key)
	expr := fmt.Sprintf(`JSON.stringify(localStorage.getItem(%s))`, string(keyJSON))
	raw, err := cdp.Evaluate(ctx, expr)
	if err != nil {
		return ""
	}
	var envelope struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	// envelope.Result.Value is a JSON string like "\"<token>\"" or "null".
	var s string
	if json.Unmarshal([]byte(envelope.Result.Value), &s) != nil {
		return ""
	}
	return s
}

// kimiBuildEnvelope fills a versioned envelope with the allowlisted replay
// fields: the Bearer accessToken, the durable refresh_token (from localStorage),
// the cookie header, and the stable browser headers. Unknown headers are
// ignored; SetField rejects anything outside the allowlist. Only non-empty
// values are stored.
func kimiBuildEnvelope(token, refreshToken, cookieHeader string, headers map[string]string) config.KimiAuthEnvelope {
	env := config.KimiAuthEnvelope{Version: config.KimiAuthEnvelopeVersion()}
	// Header name → envelope field name (allowlisted).
	h2f := map[string]string{
		"x-msh-device-id": "x_msh_device_id",
		"x-traffic-id":    "x_traffic_id",
		"x-msh-platform":  "x_msh_platform",
		"x-msh-version":   "x_msh_version",
		"x-language":      "x_language",
		"r-timezone":      "r_timezone",
		"user-agent":      "user_agent",
	}
	_ = env.SetField("accessToken", token)
	if refreshToken != "" {
		_ = env.SetField("refreshToken", refreshToken)
	}
	if cookieHeader != "" {
		_ = env.SetField("cookie", cookieHeader)
	}
	for headerName, fieldName := range h2f {
		if v, ok := headers[headerName]; ok && v != "" {
			_ = env.SetField(fieldName, v)
		}
	}
	return env
}

// kimiCookieHeader builds a "name=value; name=value" Cookie header from the
// captured browser cookies on the Kimi host. Empty values and values with
// control characters are dropped.
func kimiCookieHeader(cookies []browserauth.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range filterKimiCookies(cookies) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// kimiTokenFromEvent pulls a Bearer accessToken from a Network header event
// with its requestId and URL, mirroring deepSeekTokenFromEvent. Kept for tests
// that drive the header event path directly.
func kimiTokenFromEvent(event browserauth.Event) (token, requestID, requestURL string) {
	decoded, ok := browserauth.DecodeRequestHeadersEvent(event)
	if !ok {
		return "", "", ""
	}
	return browserauth.BearerToken(decoded.Headers), decoded.RequestID, decoded.URL
}

// filterKimiCookies narrows captured cookies to the Kimi host.
func filterKimiCookies(cookies []browserauth.Cookie) []browserauth.Cookie {
	out := make([]browserauth.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !cookieDomainMatches(cookie.Domain, kimiHost) || cookie.Value == "" {
			continue
		}
		if strings.ContainsAny(cookie.Name+cookie.Value, ";\r\n") {
			continue
		}
		out = append(out, cookie)
	}
	return out
}

// RunKimiPage opens the saved Kimi console after replaying the saved auth
// state. The browser stays open until the user closes it.
func RunKimiPage(pageURL, envelopeJSON string) error {
	if err := validateKimiPageURL(pageURL); err != nil {
		return err
	}
	var env config.KimiAuthEnvelope
	if err := env.Decode([]byte(envelopeJSON)); err != nil {
		return err
	}
	browser, err := launchKimiBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return runKimiPage(ctx, browser, pageURL, &env)
}

// runKimiPage opens the console after replaying the saved auth state. On
// failure after launch, signalOpenPageError fires BEFORE browser.Wait blocks
// — no flash-close. On success, signalOpenPageReady then Wait.
func runKimiPage(ctx context.Context, browser kimiLoginBrowser, pageURL string, env *config.KimiAuthEnvelope) error {
	cdp, err := browser.CDP(ctx)
	if err != nil {
		return err
	}
	defer cdp.Close()

	failAndWait := func(errMsg error) error {
		signalOpenPageError(errMsg.Error())
		_ = browser.Wait()
		return errMsg
	}

	// Replay cookies (best-effort) before the protected navigation.
	cookies := kimiEnvelopeCookies(env)
	if len(cookies) > 0 {
		result := cdp.SetCookiesBestEffort(ctx, cookies)
		log.Printf("kimi: 账户页 cookie 回放完成，注入 %d 个，失败 %d 个", result.Injected, len(result.Failed))
	}
	// Restore the durable SPA session into localStorage at document start.
	// The membership SPA reads localStorage["access_token"] + ["refresh_token"]
	// to make authenticated calls; cookies alone are NOT enough (confirmed: a
	// cookie-only replay did not trigger the protected response). The script is
	// JSON-encoded so the tokens are never concatenated into executable code.
	if script := kimiStorageRestoreScript(env); script != "" {
		if err := cdp.AddScriptOnNewDocument(ctx, script); err != nil {
			return failAndWait(fmt.Errorf("准备 Kimi 登录态脚本失败: %w", err))
		}
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 Kimi 账户页网络事件失败: %w", err))
	}
	if err := cdp.EnablePage(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 Kimi 账户页页面事件失败: %w", err))
	}
	events := cdp.Events()

	log.Printf("kimi: 账户页导航")
	loader, err := cdp.NavigateWithLoader(ctx, pageURL, kimiHost)
	if err != nil {
		return failAndWait(fmt.Errorf("打开 Kimi 账户页失败: %w", err))
	}
	if err := kimiWaitForAuthDecision(ctx, cdp, events, loader, kimiSettleTimeout); err != nil {
		if !isKimiExpectedTimeout(err) {
			return failAndWait(fmt.Errorf("等待 Kimi 账户页鉴权决定失败: %w", err))
		}
		return failAndWait(fmt.Errorf("Kimi 账户页未观测到受保护接口响应（鉴权未通过），请重新登录"))
	}

	postURL, _ := cdp.PageURL(ctx, kimiHost)
	log.Printf("kimi: 最终 URL host=%s path=%s", hostOnly(postURL), pathOnly(postURL))
	if !isKimiMembershipPage(postURL) {
		return failAndWait(fmt.Errorf("Kimi 登录态恢复失败：页面未停留在 membership（path=%s），请重新登录", pathOnly(postURL)))
	}

	log.Printf("kimi: 账户页已认证（membership 页，受保护接口 200，三 metric 有效）")
	signalOpenPageReady()

	// Round-7: watch for the membership SPA's OWN in-page token rotation.
	// Once the replayed access token expires, the SPA refreshes it itself and
	// rotates BOTH tokens in localStorage; without capture the on-disk refresh
	// token would be invalidated by the page's in-flight rotation. The watcher
	// takes over the (already buffered) events channel after the auth decision.
	// It uses its own context — the page ctx above is a 20s setup deadline.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	stopWatch := make(chan struct{})
	var watcherWG sync.WaitGroup
	if KimiPageRotationSave != nil {
		watcherWG.Add(1)
		go func() {
			defer watcherWG.Done()
			kimiWatchInPageRotation(watchCtx, cdp, events, env.AccessToken(), env.RefreshToken(), stopWatch, KimiPageRotationSave)
		}()
	}
	waitErr := browser.Wait()
	close(stopWatch)
	// Let the watcher drain rotation evidence already queued by the read loop
	// BEFORE cancelling its context — the events channel closes when the
	// connection dies (readLoop defer), bounding this wait. Cancelling first
	// would kill in-flight evidence reads and drop a close-raced rotation.
	watcherWG.Wait()
	cancelWatch()
	if waitErr != nil {
		return fmt.Errorf("Kimi 账户页浏览器异常退出: %w", waitErr)
	}
	return nil
}

// kimiRequestFacts accumulates what the watcher knows about one in-flight
// request: its URL (requestWillBeSent), its Bearer token (ExtraInfo/request
// headers), and the response status (responseReceived). CDP does NOT
// guarantee the relative order of these events (ExtraInfo may arrive after
// responseReceived), so facts are accumulated in ANY order and the evidence
// chain is only evaluated once loadingFinished completes the request.
type kimiRequestFacts struct {
	url    string
	token  string
	status int // 0 = no responseReceived observed yet
}

// kimiWatchInPageRotation consumes CDP network events for the rest of the
// page session and persists the SPA's in-page token rotations. Two
// evidence paths, both evaluated at loadingFinished (adjudicated round-8):
//
//  1. PRIMARY (authoritative issuance): a completed RefreshToken response —
//     exact https://auth.kimi.com/api/account.gateway.v1.AuthService/
//     RefreshToken URL + 2xx + loadingFinished + strictly-parsed non-empty
//     accessToken/refreshToken — is CAS-persisted IMMEDIATELY (never gated
//     on a later quota call; a close in between would lose the pair). This
//     path needs NO localStorage — it captures the memory-type rotation the
//     real session exhibited.
//  2. SECONDARY (localStorage fallback): a request to the EXACT protected
//     GetSubscriptionStats URL (an unrelated /apiv2/ 2xx is NOT evidence)
//     carrying a Bearer token ≠ lastAccess, answered 2xx, body valid per the
//     two-meter quota parser, AND localStorage access_token == the evidenced
//     token with refresh_token non-empty — persisted via the same CAS.
//
// save receives (prevAccess, prevRefresh, newAccess, newRefresh) so the
// caller compare-and-swaps BOTH fields against disk under its lock; the
// persisted bool is logged truthfully (persisted vs skipped — never a false
// persisted); lastAccess/lastRefresh advance only after a real persist.
//
// Observability: every request carrying a NEW Bearer token whose chain does
// not complete is logged with the drop reason (endpoint, status, event
// order, token LENGTHS only — never token values, bodies, or hashes), so a
// real session shows exactly what the SPA did after an in-page refresh.
//
// Close-race: after stop is observed (browser.Wait returned) the watcher
// does NOT return immediately — it keeps processing until the events
// channel CLOSES (the read loop closes it when the connection dies) or ctx
// is cancelled, so rotation evidence already queued by the read loop is not
// dropped. Evidence reads (body/localStorage) on a dead connection simply
// fail and skip — no garbage is persisted.
func kimiWatchInPageRotation(ctx context.Context, cdp kimiCDP, events <-chan browserauth.Event, initAccess, initRefresh string, stop <-chan struct{}, save func(prevAccess, prevRefresh, newAccess, newRefresh string) (bool, error)) {
	facts := map[string]*kimiRequestFacts{} // requestId → accumulated facts
	lastAccess, lastRefresh := initAccess, initRefresh
	fact := func(rid string) *kimiRequestFacts {
		f, ok := facts[rid]
		if !ok {
			f = &kimiRequestFacts{}
			facts[rid] = f
		}
		return f
	}

	// persist hands a rotated pair to the CAS save hook and logs the truthful
	// outcome (persisted vs skipped — never a false persisted). lastAccess/
	// lastRefresh advance only on a real persist.
	persist := func(newAccess, newRefresh, source string) {
		persisted, err := save(lastAccess, lastRefresh, newAccess, newRefresh)
		switch {
		case err != nil:
			log.Printf("kimi: 页面内 token 轮换持久化失败（%s）: %v", source, err)
		case !persisted:
			log.Printf("kimi: 页面内 token 轮换保存 skipped（%s，磁盘已前进；页面 access 长度 %d，新 access 长度 %d）", source, len(lastAccess), len(newAccess))
		default:
			log.Printf("kimi: 页面内 token 轮换已捕获并持久化（%s，access 长度 %d→%d）", source, len(lastAccess), len(newAccess))
			lastAccess, lastRefresh = newAccess, newRefresh
		}
	}

	handle := func(ev browserauth.Event) {
		if rh, ok := browserauth.DecodeRequestHeadersEvent(ev); ok {
			if rh.RequestID == "" {
				return
			}
			f := fact(rh.RequestID)
			if rh.URL != "" {
				f.url = rh.URL
			}
			if tok := browserauth.BearerToken(rh.Headers); tok != "" {
				f.token = tok
			}
			return
		}
		if rr, ok := browserauth.DecodeResponseReceivedEvent(ev); ok {
			if rr.RequestID != "" {
				fact(rr.RequestID).status = rr.Status
			}
			return
		}
		if ev.Method == "Network.loadingFailed" {
			var p struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(ev.Params, &p) == nil && p.RequestID != "" {
				delete(facts, p.RequestID)
			}
			return
		}
		lf, ok := browserauth.DecodeLoadingFinishedEvent(ev)
		if !ok {
			return
		}
		f, ok := facts[lf.RequestID]
		if !ok {
			return
		}
		delete(facts, lf.RequestID)

		// A completed RefreshToken response is the AUTHORITATIVE issuance
		// evidence (adjudicated round-8 design): exact URL + 2xx +
		// loadingFinished + strictly-parsed non-empty pair. It is CAS-persisted
		// IMMEDIATELY — NOT gated on a later quota call, because a window close
		// between the two events would lose the rotated pair. A subsequent exact
		// GetSubscriptionStats carrying the new access token is only usability
		// corroboration exercised in the real acceptance.
		if isKimiRefreshTokenURL(f.url) {
			if f.status < 200 || f.status >= 300 {
				log.Printf("kimi: RefreshToken 响应状态 %d，不计为轮换", f.status)
				return
			}
			pair, ok := kimiParseRefreshResponseBody(ctx, cdp, lf.RequestID)
			if !ok {
				log.Printf("kimi: RefreshToken 响应体未通过严格校验（事件止于体校验），不计为轮换")
				return
			}
			if pair.access == lastAccess {
				log.Printf("kimi: RefreshToken 签发的 access（长度 %d）与当前一致，非轮换，跳过", len(pair.access))
				return
			}
			log.Printf("kimi: RefreshToken 轮换签发已观测（新 access 长度 %d），立即 CAS 持久化", len(pair.access))
			persist(pair.access, pair.refresh, "RefreshToken 签发证据")
			return
		}

		if f.token == "" || f.token == lastAccess {
			return // current-token traffic: nothing rotated
		}
		// A request carrying a NEW Bearer token completed — narrate the chain.
		hostPath := "?"
		if u, err := url.Parse(f.url); err == nil {
			hostPath = u.Host + u.Path
		}
		if !isKimiProtectedURL(f.url) {
			log.Printf("kimi: 新 token（长度 %d）出现在非 quota 端点 %s，不计为轮换证据", len(f.token), hostPath)
			return
		}
		if f.status < 200 || f.status >= 300 {
			log.Printf("kimi: 新 token（长度 %d）的 quota 请求状态 %d，不计为轮换证据", len(f.token), f.status)
			return
		}
		if !kimiResponseBodyValid(ctx, cdp, lf.RequestID) {
			log.Printf("kimi: 新 token（长度 %d）的 quota 响应体无效，不计为轮换证据", len(f.token))
			return
		}
		at := kimiReadLocalStorage(ctx, cdp, "access_token")
		rt := kimiReadLocalStorage(ctx, cdp, "refresh_token")
		if at == "" || rt == "" {
			log.Printf("kimi: quota 新 token 已证据，但 localStorage 读取失败（页面可能已关闭）")
			return
		}
		if at != f.token {
			log.Printf("kimi: localStorage access（长度 %d）与证据 token（长度 %d）不一致，跳过（可能再次轮换）", len(at), len(f.token))
			return
		}
		persist(at, rt, "quota 证据+localStorage 一致")
	}

	stopped := false
	for {
		if stopped {
			// Post-stop drain: process until the producer closes the channel
			// (browser death ends the read loop) or ctx is cancelled.
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				handle(ev)
			case <-ctx.Done():
				return
			}
			continue
		}
		select {
		case <-stop:
			stopped = true
		case ev, ok := <-events:
			if !ok {
				return
			}
			handle(ev)
		case <-ctx.Done():
			return
		}
	}
}

// kimiStorageRestoreScript builds a document-start script that installs the
// saved access_token + refresh_token into localStorage before the membership
// SPA boots, so it makes authenticated calls. The tokens are JSON-encoded
// (never concatenated into executable code) and the keys are the OBSERVED
// localStorage names ("access_token", "refresh_token").
func kimiStorageRestoreScript(env *config.KimiAuthEnvelope) string {
	at := env.AccessToken()
	rt := env.RefreshToken()
	if at == "" && rt == "" {
		return ""
	}
	atJSON, _ := json.Marshal(at)
	rtJSON, _ := json.Marshal(rt)
	return `(function(){try{` +
		`if(localStorage){` +
		fmt.Sprintf(`localStorage.setItem("access_token",%s);`, string(atJSON)) +
		fmt.Sprintf(`localStorage.setItem("refresh_token",%s);`, string(rtJSON)) +
		`}}catch(e){}})();`
}

// isKimiMembershipPage reports whether the page URL is the authenticated
// membership quota page (the account/data page). The spec pins this page to
// https://www.kimi.com/membership/subscription?tab=quota exactly: the host must
// be www.kimi.com, the path must be EXACTLY /membership/subscription (no
// trailing segments, no prefix-only match), and the tab query param must be
// quota. A missing or wrong tab, a trailing path, or a non-Kimi host is
// rejected — the account page must be the authoritative quota page, not a
// look-alike route.
func isKimiMembershipPage(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Host != kimiHost {
		return false
	}
	if parsed.Path != "/membership/subscription" {
		return false
	}
	return parsed.Query().Get("tab") == "quota"
}

// isKimiConsolePage is retained as an alias for isKimiMembershipPage for
// backward compatibility with tests that assert on the account-page boundary.
func isKimiConsolePage(u string) bool { return isKimiMembershipPage(u) }

// kimiWaitForAuthDecision waits for the real Kimi auth-decision signal:
// Network.responseReceived 200 on the protected GetSubscriptionStats URL
// whose loaderId matches the current navigation's frameStartedNavigating
// loaderId, AND whose loadingFinished body parses as the two-meter quota
// result (no Connect "code" string). This is the Kimi analog of
// deepSeekWaitForAuthDecision, using the OBSERVED Connect success
// discriminator instead of DeepSeek's code==0.
func kimiWaitForAuthDecision(ctx context.Context, cdp kimiCDP, events <-chan browserauth.Event, _ string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var epochLoaderID string
	var pendingRequestID string
	phase := 0
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &kimiAuthTimeoutError{}
		}
		select {
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("CDP 事件通道已关闭")
			}
			if phase == 0 {
				if fsn, ok := browserauth.DecodeFrameStartedNavigatingEvent(ev); ok && fsn.LoaderID != "" {
					epochLoaderID = fsn.LoaderID
					phase = 1
					log.Printf("kimi: 导航 epoch 已确定（loaderId 长度 %d）", len(epochLoaderID))
				}
				continue
			}
			if phase == 1 {
				if rr, ok := browserauth.DecodeResponseReceivedEvent(ev); ok {
					if !(rr.Status >= 200 && rr.Status < 300) || !isKimiProtectedURL(rr.URL) || rr.RequestID == "" {
						continue
					}
					var raw struct {
						LoaderID string `json:"loaderId"`
					}
					if json.Unmarshal(ev.Params, &raw) != nil || raw.LoaderID != epochLoaderID {
						continue // stale/previous navigation response
					}
					pendingRequestID = rr.RequestID
					phase = 2
					log.Printf("kimi: 受保护接口 2xx 已观测（loaderId 匹配），等待 loadingFinished")
					continue
				}
				continue
			}
			if phase == 2 {
				if lf, ok := browserauth.DecodeLoadingFinishedEvent(ev); ok {
					if lf.RequestID != pendingRequestID {
						continue
					}
					if !kimiResponseBodyValid(ctx, cdp, lf.RequestID) {
						return fmt.Errorf("受保护接口响应体校验失败（Connect 错误或缺少双 meter）")
					}
					postURL, err := cdp.PageURL(ctx, kimiHost)
					if err != nil {
						return fmt.Errorf("读取鉴权后 URL 失败: %w", err)
					}
					if !isKimiConsolePage(postURL) {
						return fmt.Errorf("受保护接口响应有效但页面未在 console（path=%s）", pathOnly(postURL))
					}
					log.Printf("kimi: 鉴权决定已观测（受保护接口 200，loaderId 匹配，双 meter 有效，console 页）")
					return nil
				}
				continue
			}
		case <-time.After(remaining):
			return &kimiAuthTimeoutError{}
		case <-ctx.Done():
			return fmt.Errorf("等待鉴权决定被取消: %w", ctx.Err())
		}
	}
}

// kimiResponseBodyValid reads the protected response body and reports whether
// it parses as the two-meter quota result (the production parser). A Connect
// error envelope (non-empty "code") or a missing/invalid meter fails — this
// is the OBSERVED Connect success discriminator (no "code" string on
// success), the Kimi analog of DeepSeek's code==0 check.
func kimiResponseBodyValid(ctx context.Context, cdp kimiCDP, requestID string) bool {
	body, err := cdp.GetResponseBody(ctx, requestID)
	if err != nil || body == "" {
		return false
	}
	_, err = quota.ParseKimiQuota(body, time.Now())
	return err == nil
}

// isKimiProtectedURL reports whether a response URL is the OBSERVED protected
// GetSubscriptionStats endpoint. Strict scheme+host+path, not a regex
// substring, so a public Kimi 2xx on a different path cannot match.
func isKimiProtectedURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme != "https" || parsed.Host != kimiHost {
		return false
	}
	return parsed.Path == "/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"
}

// isKimiRefreshTokenURL reports whether a request URL is the exact RefreshToken
// endpoint (strict scheme + host auth.kimi.com + exact path).
func isKimiRefreshTokenURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme != "https" || parsed.Host != "auth.kimi.com" {
		return false
	}
	return parsed.Path == "/api/account.gateway.v1.AuthService/RefreshToken"
}

// kimiIssuedPair is a server-issued access/refresh pair parsed from a
// RefreshToken response body.
type kimiIssuedPair struct {
	access  string
	refresh string
}

// kimiParseRefreshResponseBody reads the RefreshToken response body (bounded)
// and strictly parses the issued pair: valid JSON object, BOTH accessToken
// and refreshToken present, non-empty, length-bounded, and free of whitespace
// / control characters. A business-error body, malformed JSON, a missing or
// empty field, or an oversize body is NOT evidence — 2xx alone never
// suffices.
func kimiParseRefreshResponseBody(ctx context.Context, cdp kimiCDP, requestID string) (kimiIssuedPair, bool) {
	body, err := cdp.GetResponseBody(ctx, requestID)
	if err != nil || body == "" || len(body) > kimiRefreshBodyMaxBytes {
		return kimiIssuedPair{}, false
	}
	var parsed struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if json.Unmarshal([]byte(body), &parsed) != nil {
		return kimiIssuedPair{}, false
	}
	at, rt := parsed.AccessToken, parsed.RefreshToken
	if at == "" || rt == "" || len(at) > kimiIssuedTokenMaxLen || len(rt) > kimiIssuedTokenMaxLen {
		return kimiIssuedPair{}, false
	}
	if strings.ContainsAny(at+rt, " \t\r\n;") {
		return kimiIssuedPair{}, false
	}
	return kimiIssuedPair{access: at, refresh: rt}, true
}

func isOnKimiHost(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	return cookieDomainMatches(u.Hostname(), kimiHost)
}

func validateKimiPageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("Kimi 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("Kimi 账户页地址必须为 https")
	}
	// Replay must target EXACTLY the membership quota page: host www.kimi.com,
	// path /membership/subscription, tab=quota. A looser host-only check would
	// allow replaying cookies/storage at /code/console or an arbitrary Kimi
	// path; the account/data page is pinned to the membership quota page.
	if !isKimiMembershipPage(rawURL) {
		return fmt.Errorf("Kimi 账户页地址必须是 %s?tab=quota", kimiMembershipURL)
	}
	return nil
}

// kimiEnvelopeCookies parses the saved envelope's raw cookie header (the
// "cookie" field, a "name=value; name=value" string) into browserauth.Cookie
// values for the Kimi host. An absent/empty cookie field yields no cookies
// (a token-only replay is valid). Malformed pairs are skipped, not fatal.
func kimiEnvelopeCookies(env *config.KimiAuthEnvelope) []browserauth.Cookie {
	if env == nil {
		return nil
	}
	raw, ok := env.Field("cookie")
	if !ok || raw == "" {
		return nil
	}
	out := make([]browserauth.Cookie, 0)
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok || name == "" || value == "" {
			continue
		}
		if strings.ContainsAny(name+value, ";\r\n") {
			continue
		}
		out = append(out, browserauth.Cookie{
			Name: name, Value: value, Domain: kimiHost, Path: "/", Secure: true,
		})
	}
	return out
}

// kimiAuthEnvelopeForTest builds a minimal valid envelope for tests. Exposed
// so the test helper in login_kimi_test.go can construct one without
// importing config's private allowlist.
func kimiAuthEnvelopeForTest() config.KimiAuthEnvelope {
	env := config.KimiAuthEnvelope{Version: config.KimiAuthEnvelopeVersion()}
	_ = env.SetField("accessToken", "synthetic-bearer-jwt-1234567890")
	_ = env.SetField("refreshToken", "synthetic-refresh-jwt-1234567890")
	_ = env.SetField("cookie", "kimi_session=synthetic-session-value")
	return env
}

// RunKimiAddonPage is retained as a thin alias for RunKimiPage: the membership
// quota page itself contains the user-controlled booster/purchase UI, so the
// account/details action opens that page. No separate automated purchase route
// exists or is desired.
func RunKimiAddonPage(envelopeJSON string) error {
	return RunKimiPage(kimiMembershipURL, envelopeJSON)
}
