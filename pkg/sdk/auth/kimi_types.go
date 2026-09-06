package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

const (
	kimiHost                = "www.kimi.com"
	kimiLoginURL            = "https://www.kimi.com/code/console"
	kimiMembershipURL       = "https://www.kimi.com/membership/subscription?tab=quota"
	kimiProtectedQuotaURL   = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"
	kimiRefreshTokenURL     = "https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken"
	kimiRefreshBodyMaxBytes = 64 << 10
	kimiIssuedTokenMaxLen   = 4096
)

var kimiConsoleURL = kimiMembershipURL
var kimiSettleTimeout = 8 * time.Second

// KimiPageRotationSave 是页面内 Token 自动轮换持久化回调钩子。
var KimiPageRotationSave func(prevAccess, prevRefresh, newAccess, newRefresh string) (persisted bool, err error)

type kimiAuthTimeoutError struct{}

func (e *kimiAuthTimeoutError) Error() string { return "等待 Kimi 鉴权决定超时" }

func isKimiExpectedTimeout(err error) bool {
	var target *kimiAuthTimeoutError
	return errors.As(err, &target)
}

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
	start := time.Now()
	attempts := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempts++
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			log.Printf("kimi: CDP 连接成功（耗时 %s，%d 次尝试）", time.Since(start).Round(time.Millisecond), attempts)
			return &sharedKimiClient{Connection: conn}, nil
		}
		if b.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			log.Printf("kimi: CDP 连接放弃（耗时 %s，%d 次尝试，末次错误: %v）", time.Since(start).Round(time.Millisecond), attempts, err)
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

func isKimiMembershipPage(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Host != kimiHost {
		return false
	}
	if parsed.Path != "/membership/subscription" {
		return false
	}
	return parsed.Query().Get("tab") == "quota"
}

func isKimiConsolePage(u string) bool { return isKimiMembershipPage(u) }

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

func isKimiRefreshTokenURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme != "https" || parsed.Host != "auth.kimi.com" {
		return false
	}
	return parsed.Path == "/api/account.gateway.v1.AuthService/RefreshToken"
}

func isOnKimiHost(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	return browserauth.CookieDomainMatches(u.Hostname(), kimiHost)
}

func validateKimiPageURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("Kimi 账户页地址无效: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("Kimi 账户页地址必须为 https")
	}
	if !isKimiMembershipPage(rawURL) {
		return fmt.Errorf("Kimi 账户页地址必须是 %s?tab=quota", kimiMembershipURL)
	}
	return nil
}
