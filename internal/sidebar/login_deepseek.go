package sidebar

import (
	"context"
	"encoding/json"
	"errors"
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
	start := time.Now()
	attempts := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempts++
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			log.Printf("deepseek: CDP 连接成功（耗时 %s，%d 次尝试）", time.Since(start).Round(time.Millisecond), attempts)
			return &sharedDeepSeekClient{Connection: conn}, nil
		}
		if b.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			log.Printf("deepseek: CDP 连接放弃（耗时 %s，%d 次尝试，末次错误: %v）", time.Since(start).Round(time.Millisecond), attempts, err)
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
	// 预算组成：CDP 连接 + cookie 回放（tmpfs 后亚秒级，慢盘/Defender
	// 机器留余量）+ nav1 预期超时 5s + post-load 重放 + reload + nav2
	// ≤5s + 两次 SPA 加载。15s 曾在慢盘上稳定烧穿，改为 45s。
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
//     (If the SPA explicitly rejects the login state it redirects to
//     /sign_in; two consecutive URL polls detect that EARLY and skip
//     the 5s blind wait — see deepSeekWaitForAuthDecision.)
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

	// failAndWait signals the error, keeps the browser open for the user,
	// and returns. ALL post-launch errors go through this path — no
	// direct return that would flash-close the browser.
	failAndWait := func(errMsg error) error {
		signalOpenPageError(errMsg.Error())
		_ = browser.Wait()
		return errMsg
	}

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return failAndWait(fmt.Errorf("连接 DeepSeek 账户页浏览器失败: %w", err))
	}
	defer cdp.Close()

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
	log.Printf("deepseek: 首次导航已发送（loader %s）", loader1)
	// Nav1: wait for the SPA auth-decision signal (protected API response
	// with matching loaderId + business code==0). On nav1 the SPA typically
	// overwrites userToken and redirects to /sign_in — no protected API
	// call fires, so this wait times out. That's EXPECTED: the sentinel
	// timeout means "SPA didn't authenticate on nav1" → re-apply + reload.
	// EVERY other error (CDP channel close, ctx cancellation, PageURL
	// failure, body parse failure, business code != 0) is fatal.
	nav1Err := deepSeekWaitForAuthDecision(ctx, cdp, events, loader1, true, deepSeekSettleTimeout)
	if nav1Err != nil {
		rejected := isDeepSeekExpectedNavRejection(nav1Err)
		if !rejected && !isDeepSeekExpectedNavTimeout(nav1Err) {
			return failAndWait(fmt.Errorf("等待 DeepSeek 账户页首次鉴权决定失败: %w", nav1Err))
		}
		// Nav1 expected outcome (typed): the SPA rejected the restored
		// login state. Two flavors, both mean "nav1 did not
		// authenticate" → re-apply the restore script post-load, then
		// reload so the document-start script re-applies before the
		// SPA auth check:
		//   - rejection: observed the /sign_in redirect early (SPA
		//     explicitly rejected the token) — saved ~4s vs the blind
		//     5s wait.
		//   - sentinel timeout: no protected API response AND no
		//     /sign_in within the 5s settle window (e.g. SPA
		//     overwrote userToken without a clean redirect).
		if rejected {
			log.Printf("deepseek: 观测到 /sign_in 跳转（SPA 拒绝登录态），提前 reload")
		} else {
			log.Printf("deepseek: 首次导航未观测到受保护接口响应（哨兵超时：SPA 覆盖了 userToken）")
		}
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
		log.Printf("deepseek: 重新导航已发送（loader %s）", loader2)
		// Wait for the SPA auth-decision signal on the RELOAD navigation.
		// A late response from nav1 has a different loaderId and is rejected.
		// allowSignInEarlyExit=false: /sign_in must NOT trigger an early
		// reload on nav2 (design D4) — nav2 semantics unchanged.
		if err := deepSeekWaitForAuthDecision(ctx, cdp, events, loader2, false, deepSeekSettleTimeout); err != nil {
			return failAndWait(fmt.Errorf("等待 DeepSeek 账户页重新加载鉴权决定失败: %w", err))
		}
	}
	// nav1Err == nil: nav1 unexpectedly authenticated (SPA accepted the
	// token on first boot). No re-apply + reload; the final verify below
	// confirms the page state. The sentinel/typed-timeout contract means
	// reaching here is a real auth success, not a swallowed error.

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

// deepSeekAuthTimeoutError is the sentinel returned by
// deepSeekWaitForAuthDecision when the deadline elapses WITHOUT any fatal
// condition (no CDP channel close, no ctx cancellation, no PageURL/parse
// error). runDeepSeekPage treats ONLY this exact sentinel as "nav1 did not
// authenticate in time — proceed to re-apply + reload". Every other error
// (channel closed, ctx cancelled, PageURL read failure, body parse failure)
// is fatal and propagates through failAndWait. Using a typed error instead
// of strings.Contains means a future error message that happens to contain
// "超时" cannot be misclassified as the expected nav1 timeout.
type deepSeekAuthTimeoutError struct{}

func (e *deepSeekAuthTimeoutError) Error() string { return "等待鉴权决定超时" }

// isDeepSeekExpectedNavTimeout reports whether err is exactly the sentinel
// nav-timeout error. It is one of the two expected nav1 outcomes
// runDeepSeekPage tolerates.
func isDeepSeekExpectedNavTimeout(err error) bool {
	var target *deepSeekAuthTimeoutError
	return errors.As(err, &target)
}

// deepSeekAuthRejectedError is the SECOND expected nav1 outcome, returned
// by deepSeekWaitForAuthDecision when the SPA explicitly rejects the
// restored login state (observed via consecutive /sign_in URL polls) before
// the 5s sentinel deadline. runDeepSeekPage treats it exactly like the
// timeout sentinel: proceed to post-load re-apply + reload. Using a typed
// error (same shape as deepSeekAuthTimeoutError) keeps the outcome
// classification type-based; no string matching.
type deepSeekAuthRejectedError struct{}

func (e *deepSeekAuthRejectedError) Error() string { return "SPA 拒绝登录态（/sign_in）" }

// isDeepSeekExpectedNavRejection reports whether err is the typed
// SPA-rejection signal. It is the other expected nav1 outcome.
func isDeepSeekExpectedNavRejection(err error) bool {
	var target *deepSeekAuthRejectedError
	return errors.As(err, &target)
}

// deepSeekSignInPollInterval is the /sign_in early-detection poll period
// inside deepSeekWaitForAuthDecision (nav1 only). Two consecutive
// observations (~600ms window) are required before declaring the SPA
// rejected the restored login state. Defined here as a single tunable
// point (design Open Question: 300ms vs 250/500ms after real-machine
// measurement).
var deepSeekSignInPollInterval = 300 * time.Millisecond

// deepSeekSignInPollTimeout bounds each single PageURL poll. The poll is
// a best-effort observation — if the renderer is busy (SPA mid-redirect)
// a Runtime.evaluate can stall, and an unbounded poll would block the
// whole select loop and DELAY the sentinel deadline (observed in the
// real-machine regression: a /sign_in redirect made the evaluate hang
// for 20s+). With a short cap the poll is recorded as "no observation"
// (design D5) and the loop keeps serving the deadline.
var deepSeekSignInPollTimeout = 800 * time.Millisecond

// deepSeekWaitForAuthDecision waits for the real SPA auth-decision
// signal: a Network.responseReceived with status 2xx on a PROTECTED
// DeepSeek API endpoint (get_user_summary / usage/amount) whose
// loaderId matches the current navigation's frameStartedNavigating
// loaderId, AND whose response body's top-level "code" field EXISTS
// and equals 0. This is the real observable auth signal — the SPA
// makes these API calls only when it has accepted the userToken, and
// the business code==0 confirms the protected call actually succeeded
// (not a 200 carrying an auth/business error).
//
// The function:
//  1. Waits for Page.frameStartedNavigating with non-empty loaderId →
//     epochLoaderID (unique per navigation).
//  2. Waits for Network.responseReceived with status 2xx on a
//     protected API URL AND whose loaderId == epochLoaderID. The
//     requestId is captured for the body-read step.
//  3. Waits for Network.loadingFinished with the SAME requestId —
//     the body is only available once loading has finished.
//  4. Calls Network.getResponseBody(requestId) and requires the
//     top-level "code" field to exist and equal 0.
//  5. Reads PageURL to confirm the page is on /usage.
//
// A late event from a previous navigation has a DIFFERENT loaderId,
// so it cannot match at step 2 — real cross-navigation association
// via loaderId on the response event itself.
//
// When allowSignInEarlyExit is true (nav1 only), the wait ALSO polls
// the address-bar URL every deepSeekSignInPollInterval: two consecutive
// /sign_in observations mean the SPA rejected the restored login state
// and the wait returns the typed rejection EARLY — no need to wait the
// full 5s sentinel (the SPA redirects to /sign_in immediately on
// rejection). nav2 passes false: no /sign_in polling, its timeout-is-
// fatal semantics are unchanged.
//
// Returns:
//   - *deepSeekAuthRejectedError (typed rejection) when allowSignInEarlyExit
//     is true and the SPA explicitly rejected the restored login state: two
//     consecutive ~300ms polls observe a /sign_in URL. runDeepSeekPage treats
//     this as the expected nav1 rejection → re-apply + reload.
//   - *deepSeekAuthTimeoutError (sentinel) on deadline elapse with no
//     fatal condition. runDeepSeekPage treats this as the expected nav1
//     timeout → re-apply + reload.
//   - a wrapped fatal error on CDP channel close, ctx cancellation,
//     PageURL read failure, or body parse failure — these propagate.
func deepSeekWaitForAuthDecision(ctx context.Context, cdp deepSeekCDP, events <-chan browserauth.Event, _ string, allowSignInEarlyExit bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var epochLoaderID string
	var pendingRequestID string // requestId awaiting loadingFinished
	phase := 0                  // 0=wait frameStartedNavigating, 1=wait protected response, 2=wait loadingFinished
	// signInStreak counts CONSECUTIVE polls that observed /sign_in. The
	// SPA rejects the restored login state by redirecting there; two
	// consecutive observations (~600ms window) is the stable-decision
	// bar (design D2) that prevents a transient SPA boot pass through
	// /sign_in from being misjudged. Only armed on nav1
	// (allowSignInEarlyExit).
	signInStreak := 0
	// signInTicker is nil (blocks forever in select) when early exit is
	// disabled — nav2 never polls for /sign_in (design D4).
	var signInTicker <-chan time.Time
	if allowSignInEarlyExit {
		t := time.NewTicker(deepSeekSignInPollInterval)
		defer t.Stop()
		signInTicker = t.C
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &deepSeekAuthTimeoutError{}
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
					log.Printf("deepseek: 导航 epoch 已确定（loaderId 长度 %d）", len(epochLoaderID))
				}
				continue
			}
			if phase == 1 {
				// Wait for Network.responseReceived with 2xx on a protected
				// API URL whose loaderId == epochLoaderID. Capture the
				// requestId for the loadingFinished + body-read steps.
				if rr, ok := browserauth.DecodeResponseReceivedEvent(ev); ok {
					if !(rr.Status >= 200 && rr.Status < 300) || !isProtectedAPIURL(rr.URL) || rr.RequestID == "" {
						continue
					}
					var raw struct {
						LoaderID string `json:"loaderId"`
					}
					if json.Unmarshal(ev.Params, &raw) != nil || raw.LoaderID != epochLoaderID {
						continue // stale/previous navigation response — loaderId mismatch
					}
					pendingRequestID = rr.RequestID
					phase = 2
					log.Printf("deepseek: 受保护接口 2xx 已观测（loaderId 匹配），等待 loadingFinished")
					continue
				}
				continue
			}
			if phase == 2 {
				// Wait for Network.loadingFinished with the SAME requestId,
				// then read the body and require code field exists and == 0.
				if lf, ok := browserauth.DecodeLoadingFinishedEvent(ev); ok {
					if lf.RequestID != pendingRequestID {
						continue
					}
					if !deepSeekResponseCodeOK(ctx, cdp, lf.RequestID) {
						return fmt.Errorf("受保护接口响应体业务 code 检查失败（缺失或非 0）")
					}
					// Confirm the page is on /usage.
					postURL, err := cdp.PageURL(ctx, deepSeekHost)
					if err != nil {
						return fmt.Errorf("读取鉴权后 URL 失败: %w", err)
					}
					if !deepSeekIsUsagePage(postURL) {
						return fmt.Errorf("受保护接口业务 code=0 但页面未在 usage（path=%s）", pathOnly(postURL))
					}
					log.Printf("deepseek: 鉴权决定已观测（受保护接口 2xx，loaderId 匹配，业务 code=0，URL path=/usage）")
					return nil
				}
				continue
			}
		case <-signInTicker:
			// SPA-rejection early check (nav1 only): poll the address bar
			// URL. A transient CDP failure counts as NO observation and
			// keeps waiting (design D5) — it must not escalate to fatal.
			// isDeepSeekLoginPage (existing predicate) decides /sign_in.
			// The poll is time-boxed: a busy renderer must not stall the
			// wait loop past the sentinel deadline (real-machine finding:
			// the SPA redirecting to /sign_in can block Runtime.evaluate).
			pollCtx, cancel := context.WithTimeout(ctx, deepSeekSignInPollTimeout)
			url, err := cdp.PageURL(pollCtx, deepSeekHost)
			cancel()
			if err != nil {
				log.Printf("deepseek: /sign_in 轮询失败（记为无观测）: %v", err)
				continue
			}
			if isDeepSeekLoginPage(url) {
				signInStreak++
				if signInStreak >= 2 {
					log.Printf("deepseek: 连续 %d 次观测到 /sign_in（SPA 拒绝登录态）", signInStreak)
					return &deepSeekAuthRejectedError{}
				}
			} else {
				// Page left /sign_in: reset the streak (single transient
				// observation must not trigger the early exit).
				signInStreak = 0
			}
		case <-time.After(remaining):
			return &deepSeekAuthTimeoutError{}
		case <-ctx.Done():
			return fmt.Errorf("等待鉴权决定被取消: %w", ctx.Err())
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
