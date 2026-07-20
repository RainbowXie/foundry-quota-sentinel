package sidebar

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
)

const ollamaLoginPollInterval = 300 * time.Millisecond

// ollamaHost is the canonical Ollama domain. The coordinator restricts
// captured Cookies to this host so unrelated domains cannot leak through.
const ollamaHost = "ollama.com"

// ollamaWantedCookies enumerates the cookie names that make up the
// persisted Ollama credential. __Secure-session is required; cf_clearance
// and aid are retained when present to keep the session alive through
// Cloudflare challenges.
var ollamaWantedCookies = []string{"__Secure-session", "cf_clearance", "aid"}

// ollamaCDP is the subset of the shared client that the Ollama coordinator
// actually uses. Tests inject fakes that satisfy this small surface.
type ollamaCDP interface {
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	BrowserUserAgent(context.Context) (string, error)
	SetCookies(context.Context, []browserauth.Cookie) error
	SetUserAgent(context.Context, string) error
	Navigate(context.Context, string) error
	Close() error
}

// ollamaLoginBrowser wraps a shared browser and produces the coordinator's
// restricted view of CDP. It also handles the "browser exited before
// capture" race that the original implementation checked on every poll.
type ollamaLoginBrowser interface {
	CDP(ctx context.Context) (ollamaCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

// launchOllamaBrowser is a package-level variable so tests can substitute a
// fake without spawning a real Chrome instance.
var launchOllamaBrowser = func(ctx context.Context, pageURL string) (ollamaLoginBrowser, error) {
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: pageURL})
	if err != nil {
		return nil, err
	}
	return &sharedOllamaBrowser{Browser: browser}, nil
}

// sharedOllamaBrowser adapts the shared browser to the coordinator's
// narrower interface. It also re-dials the CDP connection on every poll so
// the Ollama login redirects (signin.ollama.com → ollama.com) do not
// observe a stale page target.
type sharedOllamaBrowser struct {
	*browserauth.Browser
}

func (b *sharedOllamaBrowser) CDP(ctx context.Context) (ollamaCDP, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			return &sharedOllamaClient{Connection: conn}, nil
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

// sharedOllamaClient exposes only the methods the Ollama coordinator uses.
type sharedOllamaClient struct {
	*browserauth.Connection
}

func (c *sharedOllamaClient) BrowserCookies(ctx context.Context) ([]browserauth.Cookie, error) {
	all, err := c.Browser().BrowserCookies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]browserauth.Cookie, 0, len(all))
	for _, cookie := range all {
		if cookieDomainMatches(cookie.Domain, ollamaHost) {
			out = append(out, cookie)
		}
	}
	return out, nil
}

func (c *sharedOllamaClient) BrowserUserAgent(ctx context.Context) (string, error) {
	return c.Browser().BrowserUserAgent(ctx)
}

func (c *sharedOllamaClient) SetCookies(ctx context.Context, cookies []browserauth.Cookie) error {
	return c.Browser().SetCookies(ctx, cookies)
}

func (c *sharedOllamaClient) SetUserAgent(ctx context.Context, userAgent string) error {
	if userAgent == "" {
		return nil
	}
	// Emulation.setUserAgentOverride is a per-target (page) domain
	// command; the browser endpoint returns "method not found". Sending
	// it to the browser endpoint aborts the account page (the error
	// bubbles to runOllamaPage and the defer reaps the browser = the
	// flash-close users see). Replay through the page endpoint.
	return c.Page().SetUserAgent(ctx, userAgent)
}

func (c *sharedOllamaClient) Navigate(ctx context.Context, pageURL string) error {
	return c.Page().Navigate(ctx, pageURL, ollamaHost)
}

// OllamaLoginCredentials is the durable record of one successful Ollama
// login. The Cookie field is the complete "__Secure-session=...; cf_clearance=...; aid=..."
// header persisted in configuration.
type OllamaLoginCredentials struct {
	Cookie    string
	UserAgent string
}

// RunOllamaLogin launches the shared browser, polls Cookies and User-Agent
// until a valid session is captured, then closes the browser before
// returning. The CLI command writes the credentials and triggers the Go
// quota refresh.
func RunOllamaLogin() (OllamaLoginCredentials, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchOllamaBrowser(ctx, "https://ollama.com/settings")
	if err != nil {
		return OllamaLoginCredentials{}, err
	}
	return runOllamaLogin(ctx, browser)
}

// RunOllamaPage opens a temporary browser, injects the saved Cookies and
// User-Agent, and navigates to the requested Ollama page. The browser stays
// open until the user closes it.
func RunOllamaPage(pageURL, cookieHeader, userAgent string) error {
	if err := validateOllamaPageURL(pageURL); err != nil {
		return err
	}
	cookies, err := ollamaSavedCookies(cookieHeader)
	if err != nil {
		return err
	}
	browser, err := launchOllamaBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runOllamaPage(ctx, browser, pageURL, cookies, userAgent)
}

func runOllamaLogin(ctx context.Context, browser ollamaLoginBrowser) (credentials OllamaLoginCredentials, err error) {
	defer func() {
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 Ollama 登录浏览器失败: %w", closeErr)
		}
	}()

	for {
		// The login bounces between signin.ollama.com and ollama.com; redial
		// every iteration so the current page target is the one being read.
		cdp, err := browser.CDP(ctx)
		if err != nil {
			return OllamaLoginCredentials{}, fmt.Errorf("连接 Ollama 登录浏览器失败: %w", err)
		}
		cookies, cookieErr := cdp.BrowserCookies(ctx)
		if cookieErr == nil {
			if candidate := ollamaSessionCookieHeader(cookies); candidate != "" {
				userAgent, userAgentErr := cdp.BrowserUserAgent(ctx)
				_ = cdp.Close()
				if userAgentErr != nil {
					return OllamaLoginCredentials{}, fmt.Errorf("读取 Ollama 登录浏览器标识失败: %w", userAgentErr)
				}
				credentials = OllamaLoginCredentials{Cookie: candidate, UserAgent: userAgent}
				return credentials, nil
			}
		}
		_ = cdp.Close()
		if cookieErr != nil {
			if browser.Exited() {
				return OllamaLoginCredentials{}, fmt.Errorf("未获取到有效 Ollama 凭证（登录窗口已关闭）")
			}
			return OllamaLoginCredentials{}, fmt.Errorf("读取 Ollama 登录状态失败: %w", cookieErr)
		}
		if browser.Exited() {
			return OllamaLoginCredentials{}, fmt.Errorf("未获取到有效 Ollama 凭证（登录窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return OllamaLoginCredentials{}, fmt.Errorf("未获取到有效 Ollama 凭证（登录超时或已取消）")
		case <-time.After(ollamaLoginPollInterval):
		}
	}
}

func runOllamaPage(ctx context.Context, browser ollamaLoginBrowser, pageURL string, cookies []browserauth.Cookie, userAgent string) (err error) {
	defer func() {
		if err != nil {
			_ = browser.Close()
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return fmt.Errorf("连接 Ollama 账户页浏览器失败: %w", err)
	}
	defer cdp.Close()
	if len(cookies) > 0 {
		if err := cdp.SetCookies(ctx, cookies); err != nil {
			return fmt.Errorf("注入 Ollama 登录状态失败: %w", err)
		}
	}
	if userAgent != "" {
		if err := cdp.SetUserAgent(ctx, userAgent); err != nil {
			return fmt.Errorf("应用 Ollama 浏览器标识失败: %w", err)
		}
	}
	if err := cdp.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("打开 Ollama 账户页失败: %w", err)
	}
	signalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("Ollama 账户页浏览器异常退出: %w", err)
	}
	return nil
}

// ollamaSessionCookieHeader builds the persisted Ollama Cookie header from
// the captured browser Cookies. __Secure-session is required; the ancillary
// names are appended in deterministic order when present.
func ollamaSessionCookieHeader(cookies []browserauth.Cookie) string {
	values := make(map[string]string, len(ollamaWantedCookies))
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HTTPOnly || !cookieDomainMatches(cookie.Domain, ollamaHost) {
			continue
		}
		for _, name := range ollamaWantedCookies {
			if cookie.Name != name {
				continue
			}
			if _, taken := values[name]; taken {
				break
			}
			values[name] = cookie.Value
			break
		}
	}
	if values["__Secure-session"] == "" {
		return ""
	}
	parts := make([]string, 0, len(ollamaWantedCookies))
	for _, name := range ollamaWantedCookies {
		if values[name] != "" {
			parts = append(parts, name+"="+values[name])
		}
	}
	return strings.Join(parts, "; ")
}

// ollamaSavedCookies parses the persisted Cookie header back into a list
// of secure cookies for the Ollama domain. Anything unrecognised is
// rejected so a malformed credential cannot reach the browser.
func ollamaSavedCookies(cookieHeader string) ([]browserauth.Cookie, error) {
	parts := strings.Split(cookieHeader, ";")
	out := make([]browserauth.Cookie, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("Ollama 登录状态无效")
		}
		if !ollamaWantedCookie(name) {
			continue
		}
		if seen[name] || value == "" {
			return nil, fmt.Errorf("Ollama 登录状态无效")
		}
		seen[name] = true
		out = append(out, browserauth.Cookie{
			Name:     name,
			Value:    value,
			Domain:   ollamaHost,
			Path:     "/",
			Secure:   true,
			HTTPOnly: true,
		})
	}
	if !seen["__Secure-session"] {
		return nil, fmt.Errorf("Ollama 登录状态无效")
	}
	return out, nil
}

func ollamaWantedCookie(name string) bool {
	for _, candidate := range ollamaWantedCookies {
		if candidate == name {
			return true
		}
	}
	return false
}

func validateOllamaPageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("Ollama 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" || !cookieDomainMatches(u.Hostname(), ollamaHost) {
		return fmt.Errorf("Ollama 账户页地址无效")
	}
	return nil
}

// cookieDomainMatches mirrors the shared filter so the coordinator can
// reject unrelated host cookies without importing a private helper from
// browserauth. Keep both implementations in sync.
func cookieDomainMatches(host, policyDomain string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), ".")
	policyDomain = strings.ToLower(strings.TrimSpace(policyDomain))
	if host == "" || policyDomain == "" {
		return false
	}
	return host == policyDomain || strings.HasSuffix(host, "."+policyDomain)
}
