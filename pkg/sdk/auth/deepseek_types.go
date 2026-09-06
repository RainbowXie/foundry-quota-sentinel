package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

const deepSeekHost = "platform.deepseek.com"
const deepSeekLoginURL = "https://platform.deepseek.com/sign_in"
const deepSeekUsageURL = "https://platform.deepseek.com/usage"

type deepSeekCDP interface {
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

func isOnDeepSeekHost(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	return browserauth.CookieDomainMatches(u.Hostname(), deepSeekHost)
}

func isDeepSeekLoginPage(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil || !browserauth.CookieDomainMatches(u.Hostname(), deepSeekHost) {
		return false
	}
	return u.Path == "/sign_in" || u.Path == "/sign_in/"
}

func validateDeepSeekPageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("DeepSeek 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" || !browserauth.CookieDomainMatches(u.Hostname(), deepSeekHost) {
		return fmt.Errorf("DeepSeek 账户页地址无效")
	}
	return nil
}

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
