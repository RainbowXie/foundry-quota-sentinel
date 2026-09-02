package sidebar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
)

// commandCodeLoginURL is the commandcode.ai sign-in page. The user signs
// in via GitHub OAuth or email; afterwards the app redirects to
// https://commandcode.ai/{login}/settings/usage whose path carries the
// GitHub login used as the usage-page identity.
const commandCodeLoginURL = "https://commandcode.ai/signin"

// commandCodeHost is the cookie-bearing origin for commandcode.ai. The
// session is a HttpOnly pair scoped to the apex domain
// (__Secure-commandcode_prod_.session_token + session_data), captured via
// CDP and replayed as a Cookie header to api.commandcode.ai.
const commandCodeHost = "commandcode.ai"

// commandCodeApexHost is the apex-domain identifier passed to CDP SetCookies.
// A leading dot (.commandcode.ai) ensures Chrome sets a Domain cookie rather
// than a Host-Only cookie, allowing API requests to subdomains (such as
// api.commandcode.ai) to share the session.
const commandCodeApexHost = ".commandcode.ai"

const commandCodeLoginPollInterval = 300 * time.Millisecond

// commandCodeUserNameRe matches the GitHub-login segment in the usage
// page URL path (/RainbowXie/settings/usage → RainbowXie).
var commandCodeUserNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9\-]*$`)

// commandCodeCDP is the subset of the shared CDP client the coordinator
// uses. Tests inject fakes satisfying this small surface.
type commandCodeCDP interface {
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	PageURL(ctx context.Context, allowedHosts ...string) (string, error)
	SetCookies(context.Context, []browserauth.Cookie) error
	Navigate(context.Context, string) error
	Close() error
}

// commandCodeLoginBrowser wraps a shared browser and produces the
// coordinator's restricted view of CDP.
type commandCodeLoginBrowser interface {
	CDP(ctx context.Context) (commandCodeCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

// launchCommandCodeBrowser is a package-level variable so tests can
// substitute a fake without spawning a real Chrome instance.
var launchCommandCodeBrowser = func(ctx context.Context, pageURL string) (commandCodeLoginBrowser, error) {
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: pageURL})
	if err != nil {
		return nil, err
	}
	return &sharedCommandCodeBrowser{Browser: browser}, nil
}

// sharedCommandCodeBrowser adapts the shared browser to the coordinator's
// narrower interface. It re-dials the CDP connection on every poll so the
// commandcode.ai redirect chain (signin → /{login}/settings/usage) does
// not observe a stale page target.
type sharedCommandCodeBrowser struct {
	*browserauth.Browser
}

func (b *sharedCommandCodeBrowser) CDP(ctx context.Context) (commandCodeCDP, error) {
	start := time.Now()
	attempts := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempts++
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			log.Printf("commandcode: CDP 连接成功（耗时 %s，%d 次尝试）", time.Since(start).Round(time.Millisecond), attempts)
			return &sharedCommandCodeClient{Connection: conn}, nil
		}
		if b.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			log.Printf("commandcode: CDP 连接放弃（耗时 %s，%d 次尝试，末次错误: %v）", time.Since(start).Round(time.Millisecond), attempts, err)
			return nil, fmt.Errorf("等待登录浏览器就绪超时: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// sharedCommandCodeClient adapts the shared connection to the narrow
// commandCodeCDP surface.
type sharedCommandCodeClient struct {
	*browserauth.Connection
}

func (c *sharedCommandCodeClient) BrowserCookies(ctx context.Context) ([]browserauth.Cookie, error) {
	return c.Browser().BrowserCookies(ctx)
}

func (c *sharedCommandCodeClient) PageURL(ctx context.Context, allowedHosts ...string) (string, error) {
	return c.Page().PageURL(ctx, allowedHosts...)
}

func (c *sharedCommandCodeClient) SetCookies(ctx context.Context, cookies []browserauth.Cookie) error {
	return c.Browser().SetCookies(ctx, cookies)
}

func (c *sharedCommandCodeClient) Navigate(ctx context.Context, pageURL string) error {
	return c.Page().Navigate(ctx, pageURL, commandCodeHost)
}

// RunCommandCodeLogin launches the shared browser, polls PageURL for the
// usage-page user-name segment and BrowserCookies for the commandcode.ai
// session pair, then closes the browser and returns the saved credential
// pair. The CLI caller supplies validate(cookie, userName) — it is
// invoked only after the browser has been reaped so a transient quota
// request cannot leave the browser process alive.
func RunCommandCodeLogin(validate func(cookie, userName string) bool) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchCommandCodeBrowser(ctx, commandCodeLoginURL)
	if err != nil {
		return "", "", err
	}
	return runCommandCodeLogin(ctx, browser, validate)
}

// RunCommandCodePage opens the supplied usage URL after injecting the
// saved cookies. The browser stays open until the user closes it.
func RunCommandCodePage(pageURL, cookie string) error {
	if err := validateCommandCodePageURL(pageURL); err != nil {
		return err
	}
	cookies, err := commandCodeSavedCookies(cookie)
	if err != nil {
		return err
	}
	browser, err := launchCommandCodeBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runCommandCodePage(ctx, browser, pageURL, cookies)
}

func runCommandCodeLogin(ctx context.Context, browser commandCodeLoginBrowser, validate func(string, string) bool) (cookie, userName string, err error) {
	defer func() {
		// Close regardless of how we exit. Validation runs BEFORE this
		// defer fires (it is in the same function body), so the browser
		// is already reaped by the time validate makes its outbound Go
		// request.
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 CommandCode 登录浏览器失败: %w", closeErr)
		}
	}()

	var capturedCookie, capturedUserName string
	for {
		cdp, cdpErr := browser.CDP(ctx)
		if cdpErr != nil {
			return "", "", fmt.Errorf("连接 CommandCode 登录浏览器失败: %w", cdpErr)
		}
		pageURL, urlErr := cdp.PageURL(ctx, commandCodeHost)
		cookies, cookieErr := cdp.BrowserCookies(ctx)
		header := commandCodeCookieHeader(filterCommandCodeCookies(cookies))
		// userName 有两个来源：usage 页 URL 路径，或 session_data cookie 的
		// base64url payload（session.user.userName）。登录后用户未必会停在
		// usage 页（可能落在 studio/主页），所以 URL 缺失时从 cookie 解。
		userName := commandCodeUserName(pageURL)
		if userName == "" && header != "" {
			userName = commandCodeUserNameFromSession(header)
		}
		_ = cdp.Close()
		if urlErr == nil && cookieErr == nil && userName != "" && header != "" {
			capturedCookie, capturedUserName = header, userName
			break
		}
		if browser.Exited() {
			return "", "", fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("未捕获到有效凭证（登录超时或已取消）")
		case <-time.After(commandCodeLoginPollInterval):
		}
	}

	// Close the browser BEFORE invoking the Go validator, mirroring the
	// OpenCode flow: the validator issues its own network request and the
	// user's machine must not still be hosting an app-owned Chrome
	// process for that round trip.
	if closeErr := browser.Close(); closeErr != nil {
		return "", "", fmt.Errorf("关闭 CommandCode 登录浏览器失败: %w", closeErr)
	}
	if !validate(capturedCookie, capturedUserName) {
		return "", "", fmt.Errorf("CommandCode 凭证验证失败")
	}
	return capturedCookie, capturedUserName, nil
}

func runCommandCodePage(ctx context.Context, browser commandCodeLoginBrowser, pageURL string, cookies []browserauth.Cookie) (err error) {
	defer func() {
		if err != nil {
			_ = browser.Close()
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return fmt.Errorf("连接 CommandCode 账户页浏览器失败: %w", err)
	}
	defer cdp.Close()
	if len(cookies) > 0 {
		if err := cdp.SetCookies(ctx, cookies); err != nil {
			return fmt.Errorf("注入 CommandCode 登录状态失败: %w", err)
		}
		log.Printf("commandcode: 账户页 cookie 注入完成（%d 个）", len(cookies))
	}
	if err := cdp.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("打开 CommandCode 账户页失败: %w", err)
	}
	log.Printf("commandcode: 账户页导航已发送")
	signalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("CommandCode 账户页浏览器异常退出: %w", err)
	}
	return nil
}

// commandCodeUserName extracts the GitHub-login segment from a
// commandcode.ai usage-page URL (https://commandcode.ai/{login}/settings/usage).
// Only the apex domain carries the login; any other origin is rejected.
func commandCodeUserName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if !cookieDomainMatches(u.Hostname(), commandCodeHost) {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[1] != "settings" {
		return ""
	}
	if !commandCodeUserNameRe.MatchString(parts[0]) {
		return ""
	}
	return parts[0]
}

// commandCodeUserNameFromSession derives the GitHub login from the
// session_data cookie when the current page URL has no usage-path login
// (the user may be parked on /studio or the home page after OAuth). The
// session_data cookie is a base64url-encoded JSON blob carrying
// session.user.userName (OBSERVED). The cookie is HttpOnly so the login
// flow reads it via CDP; here we decode the captured header. Malformed
// base64 or a missing userName fails closed to empty (never guesses).
func commandCodeUserNameFromSession(cookieHeader string) string {
	for _, segment := range strings.Split(cookieHeader, ";") {
		segment = strings.TrimSpace(segment)
		if !strings.HasPrefix(segment, "__Secure-commandcode_prod_.session_data=") {
			continue
		}
		payload := strings.TrimPrefix(segment, "__Secure-commandcode_prod_.session_data=")
		decoded, err := base64.RawURLEncoding.DecodeString(payload)
		if err != nil {
			return ""
		}
		var doc struct {
			Session struct {
				User struct {
					UserName string `json:"userName"`
				} `json:"user"`
			} `json:"session"`
		}
		if err := json.Unmarshal(decoded, &doc); err != nil {
			return ""
		}
		if commandCodeUserNameRe.MatchString(doc.Session.User.UserName) {
			return doc.Session.User.UserName
		}
		return ""
	}
	return ""
}

// commandCodeCookieHeader serialises the saved commandcode.ai cookies into
// a "name=value; name=value" header. Only cookies scoped to the apex
// commandcode.ai domain are kept (there is no auth subdomain for this
// service); cookies whose name or value fall outside the safe character
// set are dropped so a captured corruption never reaches the persisted
// credential.
func commandCodeCookieHeader(cookies []browserauth.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if !cookieDomainMatches(cookie.Domain, commandCodeHost) {
			continue
		}
		if !openCodeCookieValueSafe(cookie) {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

// filterCommandCodeCookies narrows the captured cookie set to the apex
// commandcode.ai domain and drops unsafe cookies (same safe-char policy
// as the OpenCode flow).
func filterCommandCodeCookies(cookies []browserauth.Cookie) []browserauth.Cookie {
	out := make([]browserauth.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !cookieDomainMatches(cookie.Domain, commandCodeHost) {
			continue
		}
		if !openCodeCookieValueSafe(cookie) {
			continue
		}
		out = append(out, cookie)
	}
	return out
}

// commandCodeSavedCookies parses a persisted "name=value; name=value"
// header into a list of secure cookies for the commandcode.ai origin,
// reusing the OpenCode saved-cookie parser (same charsets and duplicate
// rejection so the config write fails atomically).
func commandCodeSavedCookies(cookieHeader string) ([]browserauth.Cookie, error) {
	if cookieHeader == "" {
		return nil, fmt.Errorf("CommandCode 登录状态无效")
	}
	out := make([]browserauth.Cookie, 0)
	seen := make(map[string]bool)
	for _, segment := range strings.Split(cookieHeader, ";") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		name, value, ok := strings.Cut(segment, "=")
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("CommandCode 登录状态无效")
		}
		if !openCodeCookieNameRe.MatchString(name) || !openCodeCookieValueRe.MatchString(value) {
			return nil, fmt.Errorf("CommandCode 登录状态无效")
		}
		if seen[name] {
			return nil, fmt.Errorf("CommandCode 登录状态无效")
		}
		seen[name] = true
		out = append(out, browserauth.Cookie{
			Name:     name,
			Value:    value,
			Domain:   commandCodeApexHost,
			Path:     "/",
			Secure:   true,
			HTTPOnly: true,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("CommandCode 登录状态无效")
	}
	return out, nil
}

func validateCommandCodePageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("CommandCode 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" || !cookieDomainMatches(u.Hostname(), commandCodeHost) {
		return fmt.Errorf("CommandCode 账户页地址无效")
	}
	return nil
}
