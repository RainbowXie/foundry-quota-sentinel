package sidebar

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
)

const deepSeekHost = "platform.deepseek.com"
const deepSeekLoginURL = "https://platform.deepseek.com/sign_in"
const deepSeekUsageURL = "https://platform.deepseek.com/usage"

// deepSeekTokenRe matches token-shaped strings inside storage values. The
// platform's bearer tokens are long base64-ish strings without whitespace
// or semicolons; a strict 30+ character class is plenty.
var deepSeekTokenRe = regexp.MustCompile(`[A-Za-z0-9._\-]{30,800}`)

// deepSeekSettleWindow is the period the coordinator keeps collecting
// candidates after the first time it sees a complete snapshot on the
// authenticated origin.
const deepSeekSettleWindow = 2 * time.Second

// deepSeekPollInterval is the cadence for snapshot evaluation.
const deepSeekPollInterval = 300 * time.Millisecond

// deepSeekTokenFromEvent pulls a Bearer credential from a Network
// header event along with the event's requestId and URL. The
// coordinator uses the requestId to pair the event with the matching
// requestWillBeSent (which carries the URL when the current event
// is an ExtraInfo that only carries headers). Empty / non-Bearer /
// whitespace values are ignored.
func deepSeekTokenFromEvent(event browserauth.Event) (token, requestID, requestURL string) {
	decoded, ok := browserauth.DecodeRequestHeadersEvent(event)
	if !ok {
		return "", "", ""
	}
	return browserauth.BearerToken(decoded.Headers), decoded.RequestID, decoded.URL
}

// deepSeekStorageCandidates walks a {"l":{...},"s":{...}} snapshot and
// returns every token-shaped string found. The JSON parse tolerates
// arbitrary value shapes; a malformed snapshot returns nil.
func deepSeekStorageCandidates(snapshot string) []string {
	if snapshot == "" {
		return nil
	}
	var wrapper struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
	}
	if err := json.Unmarshal([]byte(snapshot), &wrapper); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	collect := func(values map[string]json.RawMessage) {
		for _, raw := range values {
			walkStringCandidates(string(raw), seen, &out)
		}
	}
	collect(wrapper.L)
	collect(wrapper.S)
	return out
}

func walkStringCandidates(raw string, seen map[string]bool, out *[]string) {
	for _, match := range deepSeekTokenRe.FindAllString(raw, -1) {
		if seen[match] {
			continue
		}
		seen[match] = true
		*out = append(*out, match)
	}
	if len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
		return
	}
	var any any
	if err := json.Unmarshal([]byte(raw), &any); err != nil {
		return
	}
	walkJSONCandidates(any, seen, out)
}

func walkJSONCandidates(value any, seen map[string]bool, out *[]string) {
	switch v := value.(type) {
	case string:
		for _, match := range deepSeekTokenRe.FindAllString(v, -1) {
			if seen[match] {
				continue
			}
			seen[match] = true
			*out = append(*out, match)
		}
	case map[string]any:
		for _, child := range v {
			walkJSONCandidates(child, seen, out)
		}
	case []any:
		for _, child := range v {
			walkJSONCandidates(child, seen, out)
		}
	}
}

// deepSeekCDP is the coordinator's narrow view of the shared client.
type deepSeekCDP interface {
	EnableNetwork(context.Context) error
	EnablePage(context.Context) error
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	PageURL(ctx context.Context, allowedHosts ...string) (string, error)
	Events() <-chan browserauth.Event
	Evaluate(ctx context.Context, expression string) (json.RawMessage, error)
	AddScriptOnNewDocument(ctx context.Context, script string) error
	Navigate(ctx context.Context, pageURL string, allowedHosts ...string) error
	// NavigateWithLoader sends Page.navigate and returns the navigation's
	// loaderId, so the coordinator associates only this navigation's
	// requests/responses (not a guess from the first requestWillBeSent).
	NavigateWithLoader(ctx context.Context, pageURL string, allowedHosts ...string) (string, error)
	// SetCookiesBestEffort injects cookies one at a time, degrading
	// rather than aborting on a single rejection. DeepSeek's replayed
	// cookie set is best-effort over a captured snapshot, so one
	// non-injectable cookie must not flash-close the account page.
	SetCookiesBestEffort(ctx context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult
	// GetResponseBody fetches the body of a finished network response by
	// requestId (Network.getResponseBody), used to verify a protected
	// API returned business code==0, not just an HTTP 200.
	GetResponseBody(ctx context.Context, requestID string) (string, error)
	Close() error
}

type deepSeekLoginBrowser interface {
	CDP(ctx context.Context) (deepSeekCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

var launchDeepSeekBrowser = func(ctx context.Context, pageURL string) (deepSeekLoginBrowser, error) {
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: pageURL})
	if err != nil {
		return nil, err
	}
	return &sharedDeepSeekBrowser{Browser: browser}, nil
}

type sharedDeepSeekBrowser struct {
	*browserauth.Browser
}

func (b *sharedDeepSeekBrowser) CDP(ctx context.Context) (deepSeekCDP, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			return &sharedDeepSeekClient{Connection: conn}, nil
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

type sharedDeepSeekClient struct {
	*browserauth.Connection
}

func (c *sharedDeepSeekClient) EnableNetwork(ctx context.Context) error {
	return c.Page().EnableNetwork(ctx)
}
func (c *sharedDeepSeekClient) EnablePage(ctx context.Context) error {
	return c.Page().EnablePage(ctx)
}
func (c *sharedDeepSeekClient) BrowserCookies(ctx context.Context) ([]browserauth.Cookie, error) {
	return c.Browser().BrowserCookies(ctx)
}
func (c *sharedDeepSeekClient) PageURL(ctx context.Context, allowedHosts ...string) (string, error) {
	return c.Page().PageURL(ctx, allowedHosts...)
}
func (c *sharedDeepSeekClient) Events() <-chan browserauth.Event {
	return c.Page().Events()
}
func (c *sharedDeepSeekClient) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	return c.Page().Evaluate(ctx, expression)
}
func (c *sharedDeepSeekClient) AddScriptOnNewDocument(ctx context.Context, script string) error {
	return c.Page().AddScriptOnNewDocument(ctx, script)
}
func (c *sharedDeepSeekClient) Navigate(ctx context.Context, pageURL string, allowedHosts ...string) error {
	return c.Page().Navigate(ctx, pageURL, allowedHosts...)
}
func (c *sharedDeepSeekClient) NavigateWithLoader(ctx context.Context, pageURL string, allowedHosts ...string) (string, error) {
	return c.Page().NavigateWithLoader(ctx, pageURL, allowedHosts...)
}
func (c *sharedDeepSeekClient) SetCookiesBestEffort(ctx context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult {
	return c.Browser().SetCookiesBestEffort(ctx, cookies)
}
func (c *sharedDeepSeekClient) GetResponseBody(ctx context.Context, requestID string) (string, error) {
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

// RunDeepSeekLogin launches the shared browser, collects Bearer candidates
// from Network headers and storage scans, waits through the settling
// window once a snapshot is available, then closes the browser before
// asking the caller to validate candidates.
func RunDeepSeekLogin(validate func(string) bool) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchDeepSeekBrowser(ctx, deepSeekLoginURL)
	if err != nil {
		return "", "", err
	}
	return runDeepSeekLogin(ctx, browser, validate)
}

// RunDeepSeekPage opens the Usage page after replaying the saved storage
// state. The browser stays open until the user closes it.
func RunDeepSeekPage(pageURL, webStore string) error {
	if err := validateDeepSeekPageURL(pageURL); err != nil {
		return err
	}
	if _, _, err := deepSeekRestoreState(webStore); err != nil {
		return err
	}
	browser, err := launchDeepSeekBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runDeepSeekPage(ctx, browser, pageURL, webStore)
}

// deepSeekSnapshotJS reads both storage areas and returns the
// {"l":{...},"s":{...}} envelope. It is evaluated in the page origin so
// the snapshot is always for the current page's storage.
const deepSeekSnapshotJS = `JSON.stringify({l:Object.fromEntries(Object.entries(localStorage)),s:Object.fromEntries(Object.entries(sessionStorage))})`

// runDeepSeekLogin is the coordinator core. It collects candidates from
// Network header events and storage snapshots, settles for two seconds
// after the first complete snapshot on the platform origin, then closes
// the browser and validates each candidate through the caller-supplied
// closure.
func runDeepSeekLogin(ctx context.Context, browser deepSeekLoginBrowser, validate func(string) bool) (token, webStore string, err error) {
	defer func() {
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 DeepSeek 登录浏览器失败: %w", closeErr)
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return "", "", fmt.Errorf("连接 DeepSeek 登录浏览器失败: %w", err)
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		_ = cdp.Close()
		return "", "", fmt.Errorf("启用 DeepSeek 网络事件失败: %w", err)
	}

	events := cdp.Events()
	candidates := make(map[string]bool)
	networkCandidates := make(map[string]bool)
	urls := make(map[string]string)    // requestId → URL from requestWillBeSent
	pending := make(map[string]string) // token → requestId awaiting URL
	var lastSnapshot string
	var authenticatedPage bool
	var firstEligible time.Time

	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			token, requestID, requestURL := deepSeekTokenFromEvent(event)
			// Record the requestId→URL mapping first. A
			// requestWillBeSent often carries no Authorization header
			// but does carry the URL; the matching ExtraInfo (which
			// has the token but no URL) cannot be associated without
			// the prior URL.
			if requestID != "" && requestURL != "" {
				urls[requestID] = requestURL
				// Promote any pending tokens awaiting this
				// requestId.
				for t, rid := range pending {
					if rid == requestID && isOnDeepSeekHost(requestURL) {
						candidates[t] = true
						delete(pending, t)
					}
				}
			}
			if token == "" {
				continue
			}
			if requestID == "" {
				// No requestId means we can never resolve the
				// origin. Drop the token to avoid an attacker
				// pre-seeding candidates with no provenance.
				continue
			}
			if u, ok := urls[requestID]; ok {
				if isOnDeepSeekHost(u) {
					candidates[token] = true
					networkCandidates[token] = true
					delete(pending, token)
				}
				// The token is associated with a requestId;
				// if the URL is not yet on platform we keep
				// it in pending for a future URL update.
			} else {
				pending[token] = requestID
			}
		case <-time.After(deepSeekPollInterval):
		}

		pageURL, urlErr := cdp.PageURL(ctx, deepSeekHost)
		raw, snapErr := cdp.Evaluate(ctx, deepSeekSnapshotJS)
		var snapshot string
		if snapErr == nil {
			var envelope struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			}
			if jsonErr := json.Unmarshal(raw, &envelope); jsonErr == nil {
				snapshot = envelope.Result.Value
			}
		}
		// Promote any pending tokens whose URL is now known and
		// on platform, then update lastSnapshot.
		if urlErr == nil && pageURL != "" {
			for token, requestID := range pending {
				if u, ok := urls[requestID]; ok && isOnDeepSeekHost(u) {
					candidates[token] = true
					networkCandidates[token] = true
					delete(pending, token)
				}
			}
		}
		if snapErr == nil && urlErr == nil && isDeepSeekSnapshotValid(snapshot) && isOnDeepSeekHost(pageURL) {
			lastSnapshot = deepSeekSnapshotWithCookies(ctx, snapshot, cdp)
			authenticatedPage = !isDeepSeekLoginPage(pageURL)
			for _, candidate := range deepSeekStorageCandidates(snapshot) {
				candidates[candidate] = true
			}
		}

		// Settle only when we have a saved valid snapshot AND at
		// least one candidate. The two-second settling window
		// starts when both become true.
		eligible := lastSnapshot != "" && len(candidates) > 0 &&
			(len(networkCandidates) > 0 || authenticatedPage)
		if eligible {
			if firstEligible.IsZero() {
				firstEligible = time.Now()
			}
			if time.Since(firstEligible) >= deepSeekSettleWindow {
				goto settled
			}
		} else {
			firstEligible = time.Time{}
		}

		if browser.Exited() {
			// Browser closed early. Only settle if we have a
			// saved valid snapshot and at least one candidate;
			// otherwise return an error so the caller can ask
			// the user to re-login. An empty WebStore never
			// reaches the persisted credential.
			if eligible {
				goto settled
			}
			return "", "", fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("未捕获到有效凭证（登录超时或已取消）")
		default:
		}
	}

settled:
	_ = cdp.Close()
	if err := browser.Close(); err != nil {
		return "", "", fmt.Errorf("关闭 DeepSeek 登录浏览器失败: %w", err)
	}

	for candidate := range candidates {
		if validate(candidate) {
			return candidate, lastSnapshot, nil
		}
	}
	return "", "", fmt.Errorf("未找到可验证的 DeepSeek 凭证")
}

// runDeepSeekPage opens the Usage page after replaying the saved storage
// state. Real diagnostic evidence (cmd/diag-deepseek, cmd/diag-deepseek2)
// proved the SPA overwrites the restored userToken with a short default on
// first boot, redirecting to /sign_in. The fix (verified by diag2) is:
//  1. Register the restore script as document-start (runs before SPA boot).
//  2. Navigate #1 — SPA boots, overwrites userToken, redirects.
//  3. Poll userToken length until it stabilizes (SPA overwrite complete).
//  4. Re-apply the restore script via Evaluate (post-load).
//  5. Navigate #2 (reload) — document-start re-applies, SPA stays on /usage.
//  6. Poll userToken length + URL until they stabilize (auth decision).
//  7. Verify userToken length matches AND URL is /usage.
//
// On failure: signalOpenPageError notifies the /api/open handshake, then
// browser.Wait() blocks until the user manually closes the browser — no
// flash-close. On success: signalOpenPageReady, then Wait.
func runDeepSeekPage(ctx context.Context, browser deepSeekLoginBrowser, pageURL, webStore string) error {
	script, cookies, err := deepSeekRestoreState(webStore)
	if err != nil {
		return err
	}
	authEntries := deepSeekAuthStorageEntries(deepSeekExpectedStorageEntries(webStore))
	if len(authEntries) == 0 {
		return fmt.Errorf("DeepSeek 登录态恢复失败：缺少 userToken 认证键")
	}

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return fmt.Errorf("连接 DeepSeek 账户页浏览器失败: %w", err)
	}
	defer cdp.Close()

	// failAndWait signals the error, keeps the browser open for the user,
	// and returns. ALL post-launch errors go through this path — no
	// direct return that would flash-close the browser.
	failAndWait := func(errMsg error) error {
		signalOpenPageError(errMsg.Error())
		_ = browser.Wait()
		return errMsg
	}

	if len(cookies) > 0 {
		result := cdp.SetCookiesBestEffort(ctx, cookies)
		if result.Injected == 0 {
			return failAndWait(fmt.Errorf("恢复 DeepSeek 登录 cookie 失败：全部 %d 个注入失败（%d 个被过滤）", len(cookies), len(result.Failed)))
		}
		log.Printf("deepseek: 账户页 cookie 回放完成，注入 %d 个，失败 %d 个", result.Injected, len(result.Failed))
	}
	if script != "" {
		if err := cdp.AddScriptOnNewDocument(ctx, script); err != nil {
			return failAndWait(fmt.Errorf("准备 DeepSeek 登录态脚本失败: %w", err))
		}
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 DeepSeek 账户页网络事件失败: %w", err))
	}
	if err := cdp.EnablePage(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 DeepSeek 账户页页面事件失败: %w", err))
	}
	events := cdp.Events()

	// Navigate #1: SPA boots and overwrites userToken with a default.
	log.Printf("deepseek: 账户页首次导航")
	loader1, err := cdp.NavigateWithLoader(ctx, pageURL, deepSeekHost)
	if err != nil {
		return failAndWait(fmt.Errorf("打开 DeepSeek 账户页失败: %w", err))
	}
	// Wait for the SPA auth-decision signal associated with THIS
	// navigation's loaderId. The signal is a URL transition to /sign_in
	// (auth failed) or /usage (auth succeeded) on the same loader, not
	// just Page.loadEventFired (which only proves document load).
	if err := deepSeekWaitForAuthDecision(ctx, cdp, events, loader1, deepSeekSettleTimeout); err != nil {
		return failAndWait(fmt.Errorf("等待 DeepSeek 账户页首次鉴权决定超时: %w", err))
	}

	// Re-apply the restore script post-load (after SPA boot overwrote
	// userToken).
	if script != "" {
		log.Printf("deepseek: post-load 重新应用登录态脚本")
		if _, err := cdp.Evaluate(ctx, script); err != nil {
			return failAndWait(fmt.Errorf("post-load 重新应用登录态脚本失败: %w", err))
		}
	}

	// Navigate #2 (reload): document-start re-applies, SPA stays on /usage.
	log.Printf("deepseek: 账户页重新导航（reload）")
	loader2, err := cdp.NavigateWithLoader(ctx, pageURL, deepSeekHost)
	if err != nil {
		return failAndWait(fmt.Errorf("重新打开 DeepSeek 账户页失败: %w", err))
	}
	// Wait for the SPA auth-decision signal on the RELOAD navigation.
	// A late loadEventFired from nav1 has a different loaderId and is
	// rejected.
	if err := deepSeekWaitForAuthDecision(ctx, cdp, events, loader2, deepSeekSettleTimeout); err != nil {
		return failAndWait(fmt.Errorf("等待 DeepSeek 账户页重新加载鉴权决定超时: %w", err))
	}

	// Verify: userToken length matches AND URL is /usage.
	postURL, _ := cdp.PageURL(ctx, deepSeekHost)
	log.Printf("deepseek: 最终 URL host=%s path=%s", hostOnly(postURL), pathOnly(postURL))
	if isDeepSeekLoginPage(postURL) || !deepSeekIsUsagePage(postURL) {
		mismatch := deepSeekStorageMismatch(ctx, cdp, authEntries)
		return failAndWait(fmt.Errorf("DeepSeek 登录态恢复失败：页面未停留在 usage（有 %d 个键不匹配），请重新登录", len(mismatch)))
	}
	if mismatch := deepSeekStorageMismatch(ctx, cdp, authEntries); len(mismatch) > 0 {
		return failAndWait(fmt.Errorf("DeepSeek 登录态恢复失败：页面在 usage 但 userToken 有 %d 个键长度不匹配", len(mismatch)))
	}

	log.Printf("deepseek: 账户页已认证（usage 页，userToken 长度匹配）")
	signalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("DeepSeek 账户页浏览器异常退出: %w", err)
	}
	return nil
}

// deepSeekSettleTimeout is the deadline for waiting on observable CDP events.
var deepSeekSettleTimeout = 5 * time.Second

// deepSeekWaitForAuthDecision waits for an observable signal that the SPA
// has made its auth decision for THIS navigation. The signal is
// Page.frameNavigated carrying a URL on /usage or /sign_in \u2014 this
// event carries a frameId that identifies the frame (and thus the
// navigation), unlike Page.loadEventFired which carries no frame/loader
// info.
//
// A late frameNavigated from a PREVIOUS navigation is naturally consumed
// and ignored \u2014 only a frameNavigated arriving AFTER this nav's
// NavigateWithLoader with /usage or /sign_in satisfies the wait.
//
// The function returns an explicit error on timeout, CDP channel close,
// or ctx cancellation \u2014 never silently succeeds.
func deepSeekWaitForAuthDecision(ctx context.Context, cdp deepSeekCDP, events <-chan browserauth.Event, loaderID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("\u7b49\u5f85\u9274\u6743\u51b3\u5b9a\u8d85\u65f6")
		}
		select {
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("CDP \u4e8b\u4ef6\u901a\u9053\u5df2\u5173\u95ed")
			}
			// Page.frameNavigated carries frameId + URL \u2014 the SPA's
			// auth-decision signal: after document load, the SPA redirects
			// to /usage (authed) or /sign_in (not authed). This event is
			// associated with the frame that navigated, unlike loadEventFired.
			if fn, ok := browserauth.DecodeFrameNavigatedEvent(ev); ok {
				if isDeepSeekLoginPage(fn.URL) || deepSeekIsUsagePage(fn.URL) {
					log.Printf("deepseek: \u9274\u6743\u51b3\u5b9a\u5df2\u89c2\u6d4b\uff08frameId \u957f\u5ea6 %d\uff0cURL path=%s\uff09", len(fn.FrameID), pathOnly(fn.URL))
					return nil
				}
				continue
			}
		case <-time.After(remaining):
			return fmt.Errorf("\u7b49\u5f85\u9274\u6743\u51b3\u5b9a\u8d85\u65f6")
		case <-ctx.Done():
			return fmt.Errorf("\u7b49\u5f85\u9274\u6743\u51b3\u5b9a\u88ab\u53d6\u6d88: %w", ctx.Err())
		}
	}
}

func hostOnly(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func pathOnly(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Path
}

// deepSeekAuthKey is the EXACT localStorage key that carries the restored
// credential. Only this key is verified (a broad token/auth/user regex
// would accept unrelated keys like "userSettings" and could mask a
// missing userToken). Match is case-insensitive on the key name.
const deepSeekAuthKey = "userToken"

// deepSeekAuthStorageEntries narrows the expected storage set to the
// single userToken auth key. A non-auth key changing (or absent) must
// not be treated as a restore failure or a success signal, and an
// unrelated auth-ish key must not masquerade as the credential.
func deepSeekAuthStorageEntries(all []deepSeekStorageEntry) []deepSeekStorageEntry {
	out := make([]deepSeekStorageEntry, 0, len(all))
	for _, e := range all {
		if strings.EqualFold(e.key, deepSeekAuthKey) {
			out = append(out, e)
		}
	}
	return out
}

// deepSeekProtectedAPIs are the EXACT protected platform API endpoints
// the project has verified the authenticated usage page calls:
// /api/v0/users/get_user_summary (FetchSummary) and
// /api/v0/usage/amount (FetchUsage). Strict parsed (scheme+host+path),
// not a regex substring, so a login-page public 2xx on a different path
// cannot match.
var deepSeekProtectedAPIs = []string{
	"https://platform.deepseek.com/api/v0/users/get_user_summary",
	"https://platform.deepseek.com/api/v0/usage/amount",
}

// isProtectedAPIURL reports whether a response URL is one of the verified
// protected endpoints. The usage/amount URL carries query params, so the
// query is stripped before comparing the path. Strict: scheme+host+path.
func isProtectedAPIURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme != "https" || parsed.Host != "platform.deepseek.com" {
		return false
	}
	switch parsed.Path {
	case "/api/v0/users/get_user_summary", "/api/v0/usage/amount":
		return true
	}
	return false
}

// deepSeekNavTracker associates network requests (and their responses)
// with the navigation that issued them via loaderId, instead of draining
// the event channel. requestWillBeSent sets the current window's
// loaderId (the first one seen) and records requestId→loaderId; a
// responseReceived is accepted only if its requestId was seen in a
// requestWillBeSent whose loaderId matches the navigation's loaderId
// (captured from Page.navigate, not guessed from the first request), so
// a late response from a previous navigation cannot authenticate the
// current window.
type deepSeekNavTracker struct {
	navLoader  string // the loaderId returned by Page.navigate for this window
	reqLoaders map[string]string
}

func newDeepSeekNavTracker(loaderID string) *deepSeekNavTracker {
	return &deepSeekNavTracker{navLoader: loaderID, reqLoaders: map[string]string{}}
}

// resetWindow sets a fresh navigation's loaderId (from Page.navigate) and
// clears the requestId→loaderId map. The event channel is NOT drained.
func (t *deepSeekNavTracker) resetWindow(loaderID string) {
	t.navLoader = loaderID
	t.reqLoaders = map[string]string{}
}

// recordRequest associates a requestWillBeSent with its loaderId. Only
// requests whose loaderId matches the navigation's loaderId are tracked
// (sub-frames or stale requests from a previous navigation are ignored).
func (t *deepSeekNavTracker) recordRequest(req browserauth.RequestHeadersEvent) {
	if t.navLoader != "" && req.LoaderID == t.navLoader {
		t.reqLoaders[req.RequestID] = req.LoaderID
	}
}

// responseInWindow reports whether a responseReceived belongs to the
// current navigation window (its requestId was tracked under the
// navigation's loaderId).
func (t *deepSeekNavTracker) responseInWindow(resp browserauth.ResponseReceivedEvent) bool {
	if t.navLoader == "" {
		return false
	}
	return t.reqLoaders[resp.RequestID] == t.navLoader
}

// deepSeekAuthWaitPerNav is how long each navigation waits for the
// protected auth signal before re-navigating. Defaults to 5s in
// production; tests lower it to keep failure-path tests fast.
var deepSeekAuthWaitPerNav = 5 * time.Second

func deepSeekEnsureAuthenticated(ctx context.Context, cdp deepSeekCDP, events <-chan browserauth.Event, pageURL string, authKeys []deepSeekStorageEntry, loaderID string, maxRenav int) error {
	// The userToken auth key is REQUIRED. No auth keys means there is no
	// credential to verify — a protected request alone (e.g. from a
	// cached/previously-authenticated session) must NOT count.
	if len(authKeys) == 0 {
		return fmt.Errorf("DeepSeek 登录态恢复失败：缺少 userToken 认证键，无法验证登录态")
	}
	tracker := newDeepSeekNavTracker(loaderID)
	for attempt := 0; ; attempt++ {
		// The event channel is NOT drained — events keep flowing, but
		// only responses whose requestId was tracked under this
		// navigation's loaderId count (a late response from a previous
		// navigation has a stale loaderId and is rejected).
		if observed := deepSeekWaitForProtectedAuth(ctx, cdp, events, authKeys, tracker, deepSeekAuthWaitPerNav); observed {
			// Final guard: after the protected request succeeds, the
			// page must be on the usage page (not just not /sign_in —
			// e.g. a transient intermediate page is not authenticated).
			postURL, _ := cdp.PageURL(ctx, deepSeekHost)
			if !deepSeekIsUsagePage(postURL) {
				return fmt.Errorf("DeepSeek 登录态恢复失败：受保护接口成功但页面未停留在 usage（host 验证未通过），请重新登录")
			}
			return nil
		}
		if attempt >= maxRenav {
			// Distinguish failure modes without logging the full URL.
			postURL, _ := cdp.PageURL(ctx, deepSeekHost)
			if isDeepSeekLoginPage(postURL) {
				return fmt.Errorf("DeepSeek 登录态恢复失败：重导航 %d 次后仍停留在登录页，请重新登录", maxRenav)
			}
			if mismatch := deepSeekStorageMismatch(ctx, cdp, authKeys); len(mismatch) > 0 {
				return fmt.Errorf("DeepSeek 登录态恢复失败：未观测到受保护接口成功响应，认证键有 %d 个缺失或长度不匹配", len(mismatch))
			}
			return fmt.Errorf("DeepSeek 登录态恢复失败：认证键已恢复但未观测到受保护接口成功响应")
		}
		log.Printf("deepseek: 账户页未观测到受保护接口成功响应，重新导航让 document-start 脚本在 SPA 鉴权前生效")
		nextLoader, err := cdp.NavigateWithLoader(ctx, pageURL, deepSeekHost)
		if err != nil {
			return fmt.Errorf("重新打开 DeepSeek 账户页失败: %w", err)
		}
		tracker.resetWindow(nextLoader)
	}
}

// deepSeekIsUsagePage reports whether the page URL is the authenticated
// usage page. The path must be /usage (the account page), on the platform
// host. A login page or intermediate route is NOT authenticated.
func deepSeekIsUsagePage(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Host != "platform.deepseek.com" {
		return false
	}
	return parsed.Path == "/usage" || strings.HasPrefix(parsed.Path, "/usage/")
}

// deepSeekWaitForProtectedAuth waits, within a deadline, for a protected
// API request to complete in THIS navigation window. The full sequence
// for a single requestId must be observed:
//  1. requestWillBeSent (loaderId == navLoader) — associates requestId.
//  2. responseReceived (status 2xx, protected URL) — the request returned.
//  3. loadingFinished (same requestId) — the body is available.
//
// Only then is the body read and checked for code==0 (code field MUST
// exist and equal 0). The userToken auth key must be present and length-
// matched as the prerequisite. A response/body from a previous
// navigation (stale loaderId, or never tracked) is rejected. No periodic
// pump masks the race: events arrive in their real order on the channel.
func deepSeekWaitForProtectedAuth(ctx context.Context, cdp deepSeekCDP, events <-chan browserauth.Event, authKeys []deepSeekStorageEntry, tracker *deepSeekNavTracker, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	// pending tracks requestIds that had a protected 2xx response in this
	// window, awaiting their loadingFinished before the body is read.
	pending := make(map[string]bool)
	authed := false
	for time.Now().Before(deadline) && !authed {
		select {
		case ev, ok := <-events:
			if !ok {
				return false
			}
			// 1. requestWillBeSent: track requests in this window.
			if req, isReq := browserauth.DecodeRequestHeadersEvent(ev); isReq && ev.Method == "Network.requestWillBeSent" {
				tracker.recordRequest(req)
				continue
			}
			// 2. responseReceived: protected 2xx in this window.
			if rr, isRR := browserauth.DecodeResponseReceivedEvent(ev); isRR {
				if !tracker.responseInWindow(rr) {
					continue // stale/unknown — not this window's request
				}
				if !(rr.Status >= 200 && rr.Status < 300) || !isProtectedAPIURL(rr.URL) {
					continue
				}
				// Prerequisite: the userToken auth key is present with a
				// matching length.
				if mismatch := deepSeekStorageMismatch(ctx, cdp, authKeys); len(mismatch) > 0 {
					continue
				}
				pending[rr.RequestID] = true
				continue
			}
			// 3. loadingFinished: body is ready; read it and check code==0.
			if lf, isLF := browserauth.DecodeLoadingFinishedEvent(ev); isLF {
				if !pending[lf.RequestID] {
					continue // not a protected 2xx we are waiting on
				}
				delete(pending, lf.RequestID)
				if !deepSeekResponseCodeOK(ctx, cdp, lf.RequestID) {
					continue // body missing/unparseable or code != 0
				}
				log.Printf("deepseek: 观测到受保护接口成功响应（host=platform.deepseek.com，业务 code=0）")
				authed = true
			}
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return false
		}
	}
	return authed
}

// deepSeekResponseCodeOK fetches the response body for a requestId and
// reports whether its top-level "code" field EXISTS and equals 0. A
// missing body, an unparseable body, or a body with no "code" field is a
// failure — the protected endpoint must explicitly return code==0.
func deepSeekResponseCodeOK(ctx context.Context, cdp deepSeekCDP, requestID string) bool {
	body, err := cdp.GetResponseBody(ctx, requestID)
	if err != nil || body == "" {
		return false
	}
	// Decode into a map so the "code" field must be PRESENT.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return false
	}
	codeBytes, ok := raw["code"]
	if !ok {
		return false // code field missing — not a real protected response
	}
	var code int
	if err := json.Unmarshal(codeBytes, &code); err != nil {
		return false
	}
	return code == 0
}

// deepSeekRestoreScript turns a saved webStore JSON snapshot into a
// document-start script. The snapshot is JSON-encoded so user data is
// never concatenated into executable code. An empty snapshot yields a
// no-op script so the page still loads.

// deepSeekStorageEntry is one expected localStorage key plus the length
// of the value the saved snapshot stored for it. The post-navigation
// probe compares the live value's length to expectedLen to catch both
// "key absent" (document-start script did not apply) and "value changed
// silently" (the SPA overwrote it before the probe). Only key names and
// lengths are ever logged — never values.
type deepSeekStorageEntry struct {
	key         string
	expectedLen int
}

// deepSeekExpectedStorageEntries returns the localStorage ("l") keys and
// the length of the value each one must hold after restore. The length
// is that of the DECODED string (what localStorage actually stores), not
// the raw JSON, because deepSeekRestoreScript stores the decoded value.
func deepSeekExpectedStorageEntries(webStore string) []deepSeekStorageEntry {
	if webStore == "" {
		return nil
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
	}
	if err := json.Unmarshal([]byte(webStore), &envelope); err != nil {
		return nil
	}
	entries := make([]deepSeekStorageEntry, 0, len(envelope.L))
	for k, raw := range envelope.L {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			// Non-string value: fall back to raw token length.
			s = string(raw)
		}
		entries = append(entries, deepSeekStorageEntry{key: k, expectedLen: len(s)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	return entries
}

// deepSeekStorageProbeExpr builds the CDP Runtime.evaluate expression
// that probes a set of localStorage keys and returns, per key,
// [1, valueLength] when present or [-1, -1] when absent, wrapped in
// JSON.stringify so Runtime.evaluate's returnByValue delivers a JSON
// string. The keys array is mapped directly (not wrapped in an extra
// array — that bug iterated a single nested-array element and produced
// one entry regardless of the key count). Exposed for a test that
// validates the generated expression's structure.
func deepSeekStorageProbeExpr(keys []string) string {
	keysJSON, _ := json.Marshal(keys)
	return fmt.Sprintf(`JSON.stringify(%s.map(function(k){var v=localStorage.getItem(k);return v==null?[-1,-1]:[1,v.length]}))`, string(keysJSON))
}

// deepSeekStorageMismatch evaluates the page's localStorage after
// navigation and returns the expected entries that are absent or whose
// live value length differs from the saved snapshot. Credential-free:
// only key names and lengths are reported.
func deepSeekStorageMismatch(ctx context.Context, cdp deepSeekCDP, expected []deepSeekStorageEntry) []deepSeekStorageEntry {
	if len(expected) == 0 {
		return nil
	}
	keys := make([]string, len(expected))
	for i, e := range expected {
		keys[i] = e.key
	}
	// keysJSON is itself a JSON array like ["k1","k2"]; map over it
	// directly (NOT wrapped in another array — that would iterate a
	// single nested-array element and return one entry for all keys).
	expr := deepSeekStorageProbeExpr(keys)
	raw, err := cdp.Evaluate(ctx, expr)
	if err != nil {
		return expected // evaluate failed — treat all as mismatch so the caller surfaces a real error
	}
	var envelope struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return expected
	}
	var live [][2]int
	if err := json.Unmarshal([]byte(envelope.Result.Value), &live); err != nil || len(live) != len(expected) {
		return expected
	}
	var mismatch []deepSeekStorageEntry
	for i, e := range expected {
		present, liveLen := live[i][0], live[i][1]
		if present < 0 {
			mismatch = append(mismatch, e)
			log.Printf("deepseek: localStorage 键 %q 缺失（document-start 脚本未生效）", e.key)
			continue
		}
		if liveLen != e.expectedLen {
			mismatch = append(mismatch, e)
			log.Printf("deepseek: localStorage 键 %q 长度不匹配：期望 %d 实际 %d（SPA 可能覆盖了恢复值）", e.key, e.expectedLen, liveLen)
			continue
		}
		log.Printf("deepseek: localStorage 键 %q 已恢复（长度 %d）", e.key, liveLen)
	}
	return mismatch
}

func deepSeekRestoreScript(webStore string) (string, error) {
	if webStore == "" {
		webStore = `{"l":{},"s":{}}`
	}
	if !json.Valid([]byte(webStore)) {
		return "", fmt.Errorf("DeepSeek 登录态快照无效")
	}
	encoded, err := json.Marshal(webStore)
	if err != nil {
		return "", fmt.Errorf("DeepSeek 登录态脚本生成失败: %w", err)
	}
	return `(function(){try{var raw=` + string(encoded) + `;var o=JSON.parse(raw);var l=o.l||{};var s=o.s||{};` +
		`for(var k in l){try{localStorage.setItem(k,l[k])}catch(e){}};` +
		`for(var k in s){try{sessionStorage.setItem(k,s[k])}catch(e){}};` +
		`}catch(e){}})();`, nil
}

type deepSeekStoredCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

func deepSeekRestoreState(webStore string) (string, []browserauth.Cookie, error) {
	if webStore == "" {
		script, err := deepSeekRestoreScript(webStore)
		return script, nil, err
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
		C []deepSeekStoredCookie     `json:"c"`
	}
	if err := json.Unmarshal([]byte(webStore), &envelope); err != nil {
		return "", nil, fmt.Errorf("DeepSeek 登录态快照无效: %w", err)
	}
	cookies := make([]browserauth.Cookie, 0, len(envelope.C))
	for _, cookie := range envelope.C {
		if cookie.Name == "" || cookie.Value == "" || cookie.Domain == "" {
			continue
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		cookies = append(cookies, browserauth.Cookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain,
			Path: path, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly,
		})
	}
	script, err := deepSeekRestoreScript(webStore)
	return script, cookies, err
}

func deepSeekSnapshotWithCookies(ctx context.Context, snapshot string, cdp deepSeekCDP) string {
	cookies, err := cdp.BrowserCookies(ctx)
	if err != nil {
		return snapshot
	}
	stored := make([]deepSeekStoredCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !cookieDomainMatches(cookie.Domain, deepSeekHost) || cookie.Value == "" ||
			strings.ContainsAny(cookie.Name+cookie.Value, ";\r\n") {
			continue
		}
		stored = append(stored, deepSeekStoredCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain,
			Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly,
		})
	}
	if len(stored) == 0 {
		return snapshot
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
		C []deepSeekStoredCookie     `json:"c"`
	}
	if err := json.Unmarshal([]byte(snapshot), &envelope); err != nil {
		return snapshot
	}
	envelope.C = stored
	data, err := json.Marshal(envelope)
	if err != nil {
		return snapshot
	}
	return string(data)
}

// isOnDeepSeekHost reports whether the page URL is on the platform
// origin.
func isOnDeepSeekHost(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	return cookieDomainMatches(u.Hostname(), deepSeekHost)
}

func isDeepSeekLoginPage(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil || !cookieDomainMatches(u.Hostname(), deepSeekHost) {
		return false
	}
	return u.Path == "/sign_in" || u.Path == "/sign_in/"
}

// isDeepSeekSnapshotValid reports whether a storage snapshot string
// is a parseable {"l":{...},"s":{...}} envelope with both keys
// present. Anything else (null, missing keys, malformed JSON, an
// empty string) is rejected so a transient mid-navigation snapshot
// cannot be mistaken for a real envelope.
func isDeepSeekSnapshotValid(snapshot string) bool {
	if snapshot == "" {
		return false
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
	}
	if err := json.Unmarshal([]byte(snapshot), &envelope); err != nil {
		return false
	}
	return envelope.L != nil && envelope.S != nil
}

func validateDeepSeekPageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("DeepSeek 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" || !cookieDomainMatches(u.Hostname(), deepSeekHost) {
		return fmt.Errorf("DeepSeek 账户页地址无效")
	}
	return nil
}
