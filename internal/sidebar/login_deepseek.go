package sidebar

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
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
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	PageURL(ctx context.Context, allowedHosts ...string) (string, error)
	Events() <-chan browserauth.Event
	Evaluate(ctx context.Context, expression string) (json.RawMessage, error)
	AddScriptOnNewDocument(ctx context.Context, script string) error
	Navigate(ctx context.Context, pageURL string, allowedHosts ...string) error
	// SetCookiesBestEffort injects cookies one at a time, degrading
	// rather than aborting on a single rejection. DeepSeek's replayed
	// cookie set is best-effort over a captured snapshot, so one
	// non-injectable cookie must not flash-close the account page.
	SetCookiesBestEffort(ctx context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult
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
func (c *sharedDeepSeekClient) SetCookiesBestEffort(ctx context.Context, cookies []browserauth.Cookie) browserauth.CookieInjectionResult {
	return c.Browser().SetCookiesBestEffort(ctx, cookies)
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
	script, cookies, err := deepSeekRestoreState(webStore)
	if err != nil {
		return err
	}
	browser, err := launchDeepSeekBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runDeepSeekPage(ctx, browser, pageURL, script, cookies)
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

func runDeepSeekPage(ctx context.Context, browser deepSeekLoginBrowser, pageURL, script string, cookies []browserauth.Cookie) (err error) {
	defer func() {
		if err != nil {
			_ = browser.Close()
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return fmt.Errorf("连接 DeepSeek 账户页浏览器失败: %w", err)
	}
	defer cdp.Close()
	if len(cookies) > 0 {
		// Best-effort: a single non-injectable cookie (e.g. a __Host-
		// cookie Chrome refuses) must not abort the page. We log the
		// failed names and continue; an all-failed replay still
		// surfaces an error so the page does not open unauthenticated.
		result := cdp.SetCookiesBestEffort(ctx, cookies)
		if result.Injected == 0 {
			return fmt.Errorf("恢复 DeepSeek 登录 cookie 失败：全部 %d 个注入失败（%d 个被过滤）", len(cookies), len(result.Failed))
		}
		log.Printf("deepseek: 账户页 cookie 回放完成，注入 %d 个，失败 %d 个（仅记名称）", result.Injected, len(result.Failed))
	}
	if script != "" {
		if err := cdp.AddScriptOnNewDocument(ctx, script); err != nil {
			return fmt.Errorf("准备 DeepSeek 登录态脚本失败: %w", err)
		}
	}
	if err := cdp.Navigate(ctx, pageURL, deepSeekHost); err != nil {
		return fmt.Errorf("打开 DeepSeek 账户页失败: %w", err)
	}
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("DeepSeek 账户页浏览器异常退出: %w", err)
	}
	return nil
}

// deepSeekRestoreScript turns a saved webStore JSON snapshot into a
// document-start script. The snapshot is JSON-encoded so user data is
// never concatenated into executable code. An empty snapshot yields a
// no-op script so the page still loads.
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
