package sidebar

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
)

const openCodeAuthURL = "https://auth.opencode.ai/authorize?client_id=app&redirect_uri=https%3A%2F%2Fopencode.ai%2Fauth%2Fcallback&response_type=code"

const openCodeHost = "opencode.ai"

// openCodeAuthHost is the cookie-bearing origin for the OAuth dance. We
// must exclude this domain from the saved credential because its cookies
// are scoped to auth.opencode.ai and would never be sent to opencode.ai.
const openCodeAuthHost = "auth.opencode.ai"

var openCodeWorkspaceRe = regexp.MustCompile(`wrk_[a-zA-Z0-9]+`)

const openCodeLoginPollInterval = 300 * time.Millisecond

// openCodeCookieHeader serialises the saved opencode.ai cookies into a
// "name=value; name=value" header. Cookies on the auth subdomain are
// filtered out so they never reach the main origin; cookies whose
// name or value fall outside the safe character set are dropped so a
// captured corruption never reaches the persisted credential.
func openCodeCookieHeader(cookies []browserauth.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if !cookieDomainMatches(cookie.Domain, openCodeHost) {
			continue
		}
		if cookieDomainMatches(cookie.Domain, openCodeAuthHost) {
			continue
		}
		if !openCodeCookieValueSafe(cookie) {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

// openCodeWorkspaceID returns the wrk_ token from an opencode.ai page URL
// when the page is on the main domain. The auth subdomain never carries
// workspace IDs, so it is rejected explicitly.
func openCodeWorkspaceID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if !cookieDomainMatches(u.Hostname(), openCodeHost) || cookieDomainMatches(u.Hostname(), openCodeAuthHost) {
		return ""
	}
	return openCodeWorkspaceRe.FindString(u.Path)
}

// openCodePageURL checks the page URL. The login polls PageURL until the
// workspace pattern appears.
type openCodePageURL interface {
	PageURL(ctx context.Context, allowedHosts ...string) (string, error)
}

type openCodeCDP interface {
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	PageURL(ctx context.Context, allowedHosts ...string) (string, error)
	SetCookies(context.Context, []browserauth.Cookie) error
	Navigate(context.Context, string) error
	Close() error
}

type openCodeLoginBrowser interface {
	CDP(ctx context.Context) (openCodeCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

// launchOpenCodeBrowser is a package-level variable so tests can inject a
// fake browser without spawning a real Chrome instance.
var launchOpenCodeBrowser = func(ctx context.Context, pageURL string) (openCodeLoginBrowser, error) {
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: pageURL})
	if err != nil {
		return nil, err
	}
	return &sharedOpenCodeBrowser{Browser: browser}, nil
}

type sharedOpenCodeBrowser struct {
	*browserauth.Browser
}

func (b *sharedOpenCodeBrowser) CDP(ctx context.Context) (openCodeCDP, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			return &sharedOpenCodeClient{Connection: conn}, nil
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

type sharedOpenCodeClient struct {
	*browserauth.Connection
}

func (c *sharedOpenCodeClient) BrowserCookies(ctx context.Context) ([]browserauth.Cookie, error) {
	return c.Browser().BrowserCookies(ctx)
}

func (c *sharedOpenCodeClient) PageURL(ctx context.Context, allowedHosts ...string) (string, error) {
	return c.Page().PageURL(ctx, allowedHosts...)
}

func (c *sharedOpenCodeClient) SetCookies(ctx context.Context, cookies []browserauth.Cookie) error {
	return c.Browser().SetCookies(ctx, cookies)
}

func (c *sharedOpenCodeClient) Navigate(ctx context.Context, pageURL string) error {
	return c.Page().Navigate(ctx, pageURL, openCodeHost)
}

// RunOpenCodeLogin launches the shared browser, polls PageURL for a
// workspace pattern and BrowserCookies for at least one opencode.ai
// cookie, then closes the browser and returns the saved credential pair.
// The CLI caller supplies validate(cookie, wsid) — it is invoked only
// after the browser has been reaped so a transient quota request cannot
// leave the browser process alive.
func RunOpenCodeLogin(validate func(cookie, wsid string) bool) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchOpenCodeBrowser(ctx, openCodeAuthURL)
	if err != nil {
		return "", "", err
	}
	return runOpenCodeLogin(ctx, browser, validate)
}

// RunOpenCodePage opens the supplied workspace URL after injecting the
// saved cookies. The browser stays open until the user closes it.
func RunOpenCodePage(pageURL, cookie string) error {
	if err := validateOpenCodePageURL(pageURL); err != nil {
		return err
	}
	cookies, err := openCodeSavedCookies(cookie)
	if err != nil {
		return err
	}
	browser, err := launchOpenCodeBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runOpenCodePage(ctx, browser, pageURL, cookies)
}

func runOpenCodeLogin(ctx context.Context, browser openCodeLoginBrowser, validate func(string, string) bool) (cookie, wsid string, err error) {
	defer func() {
		// Close regardless of how we exit. Validation runs BEFORE this
		// defer fires (it is in the same function body), so the
		// browser is already reaped by the time validate makes its
		// outbound Go request.
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 OpenCode 登录浏览器失败: %w", closeErr)
		}
	}()

	var capturedCookie, capturedWS string
	for {
		cdp, cdpErr := browser.CDP(ctx)
		if cdpErr != nil {
			return "", "", fmt.Errorf("连接 OpenCode 登录浏览器失败: %w", cdpErr)
		}
		pageURL, urlErr := cdp.PageURL(ctx, openCodeHost)
		wsid := openCodeWorkspaceID(pageURL)
		cookies, cookieErr := cdp.BrowserCookies(ctx)
		header := openCodeCookieHeader(filterOpenCodeCookies(cookies))
		_ = cdp.Close()
		if urlErr == nil && cookieErr == nil && wsid != "" && header != "" {
			capturedCookie, capturedWS = header, wsid
			break
		}
		if browser.Exited() {
			return "", "", fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("未捕获到有效凭证（登录超时或已取消）")
		case <-time.After(openCodeLoginPollInterval):
		}
	}

	// Close the browser BEFORE invoking the Go validator. The validator
	// issues a network request of its own, and the user's machine must
	// not still be hosting an application-owned Chrome process for the
	// duration of that round trip.
	if closeErr := browser.Close(); closeErr != nil {
		return "", "", fmt.Errorf("关闭 OpenCode 登录浏览器失败: %w", closeErr)
	}
	if !validate(capturedCookie, capturedWS) {
		return "", "", fmt.Errorf("OpenCode 凭证验证失败")
	}
	return capturedCookie, capturedWS, nil
}

func runOpenCodePage(ctx context.Context, browser openCodeLoginBrowser, pageURL string, cookies []browserauth.Cookie) (err error) {
	defer func() {
		if err != nil {
			_ = browser.Close()
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return fmt.Errorf("连接 OpenCode 账户页浏览器失败: %w", err)
	}
	defer cdp.Close()
	if len(cookies) > 0 {
		if err := cdp.SetCookies(ctx, cookies); err != nil {
			return fmt.Errorf("注入 OpenCode 登录状态失败: %w", err)
		}
	}
	if err := cdp.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("打开 OpenCode 账户页失败: %w", err)
	}
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("OpenCode 账户页浏览器异常退出: %w", err)
	}
	return nil
}

// filterOpenCodeCookies narrows the captured cookie set to the main
// domain so the auth subdomain cookies never reach the saved
// credential. It also drops cookies whose name or value falls
// outside the safe character set so a captured corruption cannot
// reach the persisted credential.
func filterOpenCodeCookies(cookies []browserauth.Cookie) []browserauth.Cookie {
	out := make([]browserauth.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !cookieDomainMatches(cookie.Domain, openCodeHost) {
			continue
		}
		if cookieDomainMatches(cookie.Domain, openCodeAuthHost) {
			continue
		}
		if !openCodeCookieValueSafe(cookie) {
			continue
		}
		out = append(out, cookie)
	}
	return out
}

// openCodeCookieValueSafe reports whether a captured cookie is safe
// to keep in the persisted credential. The check reuses the saved
// parser's name and value character sets so the two paths cannot
// drift; an unsafe cookie is dropped at capture time rather than
// being saved and re-rejected at write time.
func openCodeCookieValueSafe(cookie browserauth.Cookie) bool {
	if cookie.Name == "" || cookie.Value == "" {
		return false
	}
	return openCodeCookieNameRe.MatchString(cookie.Name) &&
		openCodeCookieValueRe.MatchString(cookie.Value)
}

// Names cannot contain `=` because the parser splits on the first
// `=`; they cannot contain `;`, whitespace, quotes, or backslash
// because those would let an attacker forge the cookie line.
var openCodeCookieNameRe = regexp.MustCompile(`^[A-Za-z0-9._\-%+:@]+$`)

// openCodeCookieValueRe is the character set allowed in a cookie value.
// It follows RFC 6265 cookie-octet (%x21 / %x23-2B / %x2D-3A / %x3C-5B /
// %x5D-7E) plus `=` (base64 padding, common in real session cookies). The
// previous narrow set rejected characters real opencode.ai session
// cookies carry (e.g. /, ~, *, !, (, ), #, $, &, <, >, ?, [, ]), so
// RunOpenCodePage failed BEFORE the browser launched and /api/open
// swallowed the error — the user saw "no reaction". `;`, CR, LF,
// whitespace, `"` and `\` stay rejected because they would let an
// attacker forge a header or smuggle a second cookie.
var openCodeCookieValueRe = regexp.MustCompile(`^[\x21\x23-\x2B\x2D-\x3A\x3C-\x5B\x5D-\x7E=]+$`)

// openCodeSavedCookies parses a persisted "name=value; name=value"
// header into a list of secure cookies for the opencode.ai origin.
// The split on `;` is the canonical cookie-separator; each segment
// is split once on the first `=` to separate name and value, then
// each side is matched against its own character set. The value
// charset permits `=` so real session cookies (base64, JWT) survive
// the round trip. Empty, duplicate, or malformed pairs are rejected
// so the configuration write fails atomically rather than partially.
func openCodeSavedCookies(cookieHeader string) ([]browserauth.Cookie, error) {
	if cookieHeader == "" {
		return nil, fmt.Errorf("OpenCode 登录状态无效")
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
			return nil, fmt.Errorf("OpenCode 登录状态无效")
		}
		if !openCodeCookieNameRe.MatchString(name) || !openCodeCookieValueRe.MatchString(value) {
			return nil, fmt.Errorf("OpenCode 登录状态无效")
		}
		if seen[name] {
			return nil, fmt.Errorf("OpenCode 登录状态无效")
		}
		seen[name] = true
		out = append(out, browserauth.Cookie{
			Name:     name,
			Value:    value,
			Domain:   openCodeHost,
			Path:     "/",
			Secure:   true,
			HTTPOnly: true,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("OpenCode 登录状态无效")
	}
	return out, nil
}

func validateOpenCodePageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("OpenCode 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" || !cookieDomainMatches(u.Hostname(), openCodeHost) {
		return fmt.Errorf("OpenCode 账户页地址无效")
	}
	return nil
}
