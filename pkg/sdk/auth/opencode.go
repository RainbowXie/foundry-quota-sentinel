package auth

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

// openCodeAuthURL 指向 Console v2 登录页：旧版 /auth/authorize 授权流程已不再服务新控制台。
const openCodeAuthURL = "https://opencode.ai/console/login?next=%2Fconsole%2Fgo"
const openCodeHost = "opencode.ai"
const openCodeAuthHost = "auth.opencode.ai"

// openCodeWorkspaceRe 同时匹配 org_ 与 wrk_ 两种标识：Console v2 用组织 ID 路由
// （/console/org_xxx/go），旧账号仍可能落在 wrk_ 工作区路径上。
var openCodeWorkspaceRe = regexp.MustCompile(`(?:org|wrk)_[a-zA-Z0-9]+`)

const openCodeLoginPollInterval = 300 * time.Millisecond

// resolveOpenCodeOrgID 在 URL 未携带 org/wrk 段时，用已捕获的 Cookie 反查默认组织 ID。
// 做成变量以便单测注入，避免测试依赖真实网络。
var resolveOpenCodeOrgID = func(cookie string) (string, error) {
	return opencode.FetchDefaultOrgID(cookie, nil)
}

func openCodeCookieHeader(cookies []browserauth.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if !browserauth.CookieDomainMatches(cookie.Domain, openCodeHost) {
			continue
		}
		if browserauth.CookieDomainMatches(cookie.Domain, openCodeAuthHost) {
			continue
		}
		if !openCodeCookieValueSafe(cookie) {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

func openCodeWorkspaceID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if !browserauth.CookieDomainMatches(u.Hostname(), openCodeHost) || browserauth.CookieDomainMatches(u.Hostname(), openCodeAuthHost) {
		return ""
	}
	return openCodeWorkspaceRe.FindString(u.Path)
}

// openCodeConsoleURL 判断页面是否已落在 Console 控制台内部（登录页除外）。
// 只有进入控制台才说明会话可能已建立；登录页上的无关 Cookie 不该触发组织反查请求。
func openCodeConsoleURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if !browserauth.CookieDomainMatches(u.Hostname(), openCodeHost) || browserauth.CookieDomainMatches(u.Hostname(), openCodeAuthHost) {
		return false
	}
	path := strings.TrimSuffix(u.Path, "/")
	if strings.HasPrefix(path, "/console/login") {
		return false
	}
	return path == "/console" || strings.HasPrefix(path, "/console/")
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
	start := time.Now()
	attempts := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempts++
		conn, err := browserauth.Connect(ctx, b.DebugAddress())
		if err == nil {
			log.Printf("opencode: CDP 连接成功（耗时 %s，%d 次尝试）", time.Since(start).Round(time.Millisecond), attempts)
			return &sharedOpenCodeClient{Connection: conn}, nil
		}
		if b.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			log.Printf("opencode: CDP 连接放弃（耗时 %s，%d 次尝试，末次错误: %v）", time.Since(start).Round(time.Millisecond), attempts, err)
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

// RunOpenCodeLogin 启动 CDP 浏览器引导用户登录 OpenCode，并轮询捕获 Cookie 与 WorkspaceID。
func RunOpenCodeLogin(validate func(cookie, wsid string) bool) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchOpenCodeBrowser(ctx, openCodeAuthURL)
	if err != nil {
		return "", "", err
	}
	return runOpenCodeLogin(ctx, browser, validate)
}

// RunOpenCodePage 注入 Cookie 并打开指定的 OpenCode 账户页，保持浏览器常驻直到用户自行关闭。
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
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 OpenCode 登录浏览器失败: %w", closeErr)
		}
	}()

	var capturedCookie, capturedWS string
	orgResolveLogged := false
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
		// Console v2 登录后前端路由可能停在 /console 或 /console/go 而不带 org/wrk 路径段，
		// 此时用已到手的会话 Cookie 反查默认组织，避免用户被迫手动跳到工作区页面。
		if urlErr == nil && cookieErr == nil && header != "" && wsid == "" && openCodeConsoleURL(pageURL) {
			resolved, resolveErr := resolveOpenCodeOrgID(header)
			if resolveErr != nil {
				// 轮询间隔 300ms，失败只提示一次，避免日志淹没有价值的诊断信息。
				if !orgResolveLogged {
					log.Printf("opencode: 解析默认组织失败，继续等待页面跳转: %v", resolveErr)
					orgResolveLogged = true
				}
			} else {
				wsid = resolved
			}
		}
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
		log.Printf("opencode: 账户页 cookie 注入完成（%d 个）", len(cookies))
	}
	if err := cdp.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("打开 OpenCode 账户页失败: %w", err)
	}
	log.Printf("opencode: 账户页导航已发送")
	SignalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("OpenCode 账户页浏览器异常退出: %w", err)
	}
	return nil
}

func filterOpenCodeCookies(cookies []browserauth.Cookie) []browserauth.Cookie {
	out := make([]browserauth.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !browserauth.CookieDomainMatches(cookie.Domain, openCodeHost) {
			continue
		}
		if browserauth.CookieDomainMatches(cookie.Domain, openCodeAuthHost) {
			continue
		}
		if !openCodeCookieValueSafe(cookie) {
			continue
		}
		out = append(out, cookie)
	}
	return out
}

func openCodeCookieValueSafe(cookie browserauth.Cookie) bool {
	if cookie.Name == "" || cookie.Value == "" {
		return false
	}
	return openCodeCookieNameRe.MatchString(cookie.Name) &&
		openCodeCookieValueRe.MatchString(cookie.Value)
}

var openCodeCookieNameRe = regexp.MustCompile(`^[A-Za-z0-9._\-%+:@]+$`)
var openCodeCookieValueRe = regexp.MustCompile(`^[\x21\x23-\x2B\x2D-\x3A\x3C-\x5B\x5D-\x7E=]+$`)

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
		// __Host-console_session 等宿主前缀 Cookie 在这里不需要特殊分支：
		// Domain 保留为 opencodeHost 仅用于给 browserauth.cookieParam 选择注入 URL，
		// 注入时它会自动清空 Domain 并改用 url，符合 __Host- 不得带 Domain 的规范。
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
	if u.Scheme != "https" || !browserauth.CookieDomainMatches(u.Hostname(), openCodeHost) {
		return fmt.Errorf("OpenCode 账户页地址无效")
	}
	return nil
}
