package sidebar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/quota"
)

// kimiHost is the canonical Kimi consumer domain. The coordinator restricts
// captured credentials and page URLs to this host so unrelated domains
// cannot leak through.
const kimiHost = "www.kimi.com"

// kimiLoginURL is where the temporary login browser starts. The OBSERVED
// Kimi console is a client-side-gated SPA (no HTTP redirect for
// unauthenticated visitors), so starting at the console surfaces the login
// prompt in-page.
const kimiLoginURL = "https://www.kimi.com/code/console"

// kimiConsoleURL is the authenticated account page the saved-account replay
// navigates to.
const kimiConsoleURL = "https://www.kimi.com/code/console"

// kimiProtectedQuotaURL is the OBSERVED protected quota endpoint the SPA
// calls when authenticated. Mirrors the constant in internal/quota/kimi_web.go;
// duplicated here because the sidebar must not import the quota package's
// private constant, and the URL is the provider contract both layers share.
const kimiProtectedQuotaURL = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"

// kimiAddonURL is the OBSERVED canonical "购买加油包" (booster) destination.
// The add-on action opens this HTTPS Kimi page for the user without
// submitting a purchase. It passes the host/path allowlist.
const kimiAddonURL = "https://www.kimi.com/membership/subscription?tab=quota&from=kfc_console_booster"

// kimiSettleTimeout is the deadline for waiting on observable CDP events
// during the account-page auth-decision wait.
var kimiSettleTimeout = 8 * time.Second

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
	_ = cdp.Close()
	if err := browser.Close(); err != nil {
		return config.KimiAuthEnvelope{}, fmt.Errorf("关闭 Kimi 登录浏览器失败: %w", err)
	}

	for _, c := range candidates {
		if validate(c.token) {
			return kimiBuildEnvelope(c.token, cookieHeader, c.headers), nil
		}
	}
	return config.KimiAuthEnvelope{}, fmt.Errorf("未找到可验证的 Kimi 凭证")
}

// kimiBuildEnvelope fills a versioned envelope with the allowlisted replay
// fields from a captured protected request: the Bearer accessToken, the
// cookie header, and the stable browser headers (x-msh-device-id,
// x-traffic-id, x-msh-platform, x-msh-version, x-language, r-timezone,
// user-agent). Unknown headers are ignored; SetField rejects anything outside
// the allowlist. Only non-empty values are stored.
func kimiBuildEnvelope(token, cookieHeader string, headers map[string]string) config.KimiAuthEnvelope {
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
	if !isKimiConsolePage(postURL) {
		return failAndWait(fmt.Errorf("Kimi 登录态恢复失败：页面未停留在 console（path=%s），请重新登录", pathOnly(postURL)))
	}

	log.Printf("kimi: 账户页已认证（console 页，受保护接口 200，双 meter 有效）")
	signalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("Kimi 账户页浏览器异常退出: %w", err)
	}
	return nil
}

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
	_, err = quota.ParseKimiQuota(body)
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

// isKimiConsolePage reports whether the page URL is the authenticated Kimi
// code console.
func isKimiConsolePage(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Host != kimiHost {
		return false
	}
	return parsed.Path == "/code/console" || strings.HasPrefix(parsed.Path, "/code/console")
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
	if u.Scheme != "https" || !cookieDomainMatches(u.Hostname(), kimiHost) {
		return fmt.Errorf("Kimi 账户页地址无效")
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
	_ = env.SetField("cookie", "kimi_session=synthetic-session-value")
	return env
}

// RunKimiAddonPage opens the OBSERVED canonical add-on destination for the
// user without submitting a purchase. It reuses the saved auth state so the
// page opens authenticated.
func RunKimiAddonPage(envelopeJSON string) error {
	var env config.KimiAuthEnvelope
	if err := env.Decode([]byte(envelopeJSON)); err != nil {
		return err
	}
	if err := validateKimiPageURL(kimiAddonURL); err != nil {
		return err
	}
	browser, err := launchKimiBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runKimiPage(ctx, browser, kimiAddonURL, &env)
}
