package auth

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

const ollamaLoginPollInterval = 300 * time.Millisecond
const ollamaHost = "ollama.com"

var ollamaWantedCookies = []string{"__Secure-session", "cf_clearance", "aid"}

type ollamaCDP interface {
	BrowserCookies(context.Context) ([]browserauth.Cookie, error)
	BrowserUserAgent(context.Context) (string, error)
	SetCookies(context.Context, []browserauth.Cookie) error
	SetUserAgent(context.Context, string) error
	Navigate(context.Context, string) error
	Close() error
}

type ollamaLoginBrowser interface {
	CDP(ctx context.Context) (ollamaCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

var launchOllamaBrowser = func(ctx context.Context, pageURL string) (ollamaLoginBrowser, error) {
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: pageURL})
	if err != nil {
		return nil, err
	}
	return &sharedOllamaBrowser{Browser: browser}, nil
}

type sharedOllamaBrowser struct {
	*browserauth.Browser
}

func (b *sharedOllamaBrowser) CDP(ctx context.Context) (ollamaCDP, error) {
	start := time.Now()
	attempts := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempts++
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			log.Printf("ollama: CDP 连接成功（耗时 %s，%d 次尝试）", time.Since(start).Round(time.Millisecond), attempts)
			return &sharedOllamaClient{Connection: conn}, nil
		}
		if b.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			log.Printf("ollama: CDP 连接放弃（耗时 %s，%d 次尝试，末次错误: %v）", time.Since(start).Round(time.Millisecond), attempts, err)
			return nil, fmt.Errorf("等待登录浏览器就绪超时: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

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
		if browserauth.CookieDomainMatches(cookie.Domain, ollamaHost) {
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
	return c.Page().SetUserAgent(ctx, userAgent)
}

func (c *sharedOllamaClient) Navigate(ctx context.Context, pageURL string) error {
	return c.Page().Navigate(ctx, pageURL, ollamaHost)
}

// OllamaLoginCredentials 保存成功登录 Ollama 后截获的 Cookie 与 User-Agent。
type OllamaLoginCredentials struct {
	Cookie    string
	UserAgent string
}

// RunOllamaLogin 启动 CDP 浏览器引导用户登录 Ollama 并捕获凭据。
func RunOllamaLogin() (OllamaLoginCredentials, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchOllamaBrowser(ctx, "https://ollama.com/settings")
	if err != nil {
		return OllamaLoginCredentials{}, err
	}
	return runOllamaLogin(ctx, browser)
}

// RunOllamaPage 注入 Cookie 和 User-Agent 打开指定的 Ollama 页面。
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
		log.Printf("ollama: 账户页 cookie 注入完成（%d 个）", len(cookies))
	}
	if userAgent != "" {
		if err := cdp.SetUserAgent(ctx, userAgent); err != nil {
			return fmt.Errorf("应用 Ollama 浏览器标识失败: %w", err)
		}
	}
	if err := cdp.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("打开 Ollama 账户页失败: %w", err)
	}
	log.Printf("ollama: 账户页导航已发送")
	SignalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("Ollama 账户页浏览器异常退出: %w", err)
	}
	return nil
}

func ollamaSessionCookieHeader(cookies []browserauth.Cookie) string {
	values := make(map[string]string, len(ollamaWantedCookies))
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HTTPOnly || !browserauth.CookieDomainMatches(cookie.Domain, ollamaHost) {
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
	if u.Scheme != "https" || !browserauth.CookieDomainMatches(u.Hostname(), ollamaHost) {
		return fmt.Errorf("Ollama 账户页地址无效")
	}
	return nil
}
