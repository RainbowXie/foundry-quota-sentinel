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

func runDeepSeekPage(ctx context.Context, browser deepSeekLoginBrowser, pageURL, webStore string) (err error) {
	defer func() {
		if err != nil {
			_ = browser.Close()
		}
	}()

	script, cookies, err := deepSeekRestoreState(webStore)
	if err != nil {
		return err
	}
	// expectedStorage are the localStorage entries the restore script
	// must re-establish after navigation. The post-navigation CDP check
	// compares each live value's LENGTH to the saved snapshot — proving
	// the document-start script applied AND the SPA did not silently
	// overwrite the restored value. This is the real fix for "页面仍要求
	// 登录": a missing or length-mismatched key is surfaced as an error
	// rather than a silent half-authenticated page.
	expectedStorage := deepSeekExpectedStorageEntries(webStore)

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
	// Wait for the SPA to settle before judging the auth state. The
	// platform may client-redirect (e.g. to /sign_in) a moment after the
	// document loads; reading location.href once right after Navigate
	// races that redirect. We poll location.href and require it to STAY
	// stable across two consecutive polls (no further navigation), then
	// check localStorage. This is the "wait for URL/redirect to stabilize"
	// the fix needs.
	postURL, settleErr := deepSeekWaitForURLStable(ctx, cdp, 5*time.Second, 200*time.Millisecond)
	if settleErr != nil {
		return settleErr
	}
	if isDeepSeekLoginPage(postURL) {
		// Distinguish "restore applied but the SPA/server rejected it"
		// from "restore did not apply": check the storage entries first.
		if mismatch := deepSeekStorageMismatch(ctx, cdp, expectedStorage); len(mismatch) > 0 {
			return fmt.Errorf("DeepSeek 登录态恢复失败：document-start 脚本未生效，localStorage 有 %d 个键缺失或长度不匹配（页面重定向到登录页）", len(mismatch))
		}
		return fmt.Errorf("DeepSeek 登录态恢复失败：页面重定向到登录页，请重新登录")
	}
	if mismatch := deepSeekStorageMismatch(ctx, cdp, expectedStorage); len(mismatch) > 0 {
		return fmt.Errorf("DeepSeek 登录态恢复失败：document-start 脚本未生效，localStorage 有 %d 个键缺失或长度不匹配", len(mismatch))
	}
	log.Printf("deepseek: 账户页导航后地址已稳定，localStorage 恢复 %d 个键（长度匹配）", len(expectedStorage))
	signalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("DeepSeek 账户页浏览器异常退出: %w", err)
	}
	return nil
}

// deepSeekRestoreScript turns a saved webStore JSON snapshot into a
// document-start script. The snapshot is JSON-encoded so user data is
// never concatenated into executable code. An empty snapshot yields a
// no-op script so the page still loads.
// deepSeekWaitForURLStable polls location.href until it is non-blank and
// stays the same across two consecutive polls (within a deadline). The
// SPA may client-redirect a moment after the document loads; reading the
// URL once right after Navigate races that redirect and can mis-classify
// the auth state. Returns the stabilized URL or an error on timeout.
func deepSeekWaitForURLStable(ctx context.Context, cdp deepSeekCDP, timeout, interval time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var prev string
	stable := 0
	for time.Now().Before(deadline) {
		u, err := cdp.PageURL(ctx, deepSeekHost)
		if err == nil && u != "" && !strings.HasSuffix(u, "about:blank") {
			if u == prev {
				stable++
				if stable >= 1 { // two consecutive identical reads
					return u, nil
				}
			} else {
				prev = u
				stable = 0
			}
		} else {
			stable = 0
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("DeepSeek 账户页状态检查超时: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
	if prev != "" {
		return prev, nil
	}
	return "", fmt.Errorf("DeepSeek 账户页导航后无法读取页面地址")
}

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
	keysJSON, _ := json.Marshal(keys)
	expr := fmt.Sprintf(`JSON.stringify([%s].map(function(k){var v=localStorage.getItem(k);return v==null?[-1,-1]:[1,v.length]}))`, string(keysJSON))
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
