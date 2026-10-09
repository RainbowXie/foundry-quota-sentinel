package auth

import (
	"context"
	"errors"
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

// openCodeWorkspaceRe 只从 Console 工作区路由 `/console/<org_...|wrk_...>[/...]` 中提取标识
// （design 决策 3 的契约）：锚定前缀可避免在文档页、活动页等无关路径里截取到 org_/wrk_
// 字样，从而静默保存成非目标工作区。
var openCodeWorkspaceRe = regexp.MustCompile(`^/console/((?:org|wrk)_[a-zA-Z0-9]+)(?:/|$)`)

const openCodeLoginPollInterval = 300 * time.Millisecond

// openCodeOrgResolveInterval 约束默认组织反查的最小间隔：轮询间隔只有 300ms，
// 若上游持续 401/429 或会话 Cookie 尚未生效，每轮都重发会形成 ~3 req/s 的热循环，
// 并可能自我延续限流。
const openCodeOrgResolveInterval = 5 * time.Second

// openCodeOrgResolveMaxAttempts 约束单次登录（最长 5 分钟）内的反查总次数：
// 超过上限后只继续等待页面自身跳到 /console/<id>/go，不再向上游施压。
const openCodeOrgResolveMaxAttempts = 5

// shouldResolveOpenCodeOrgID 判断本轮轮询是否允许发起默认组织反查。
func shouldResolveOpenCodeOrgID(attempts int, nextAllowed, now time.Time) bool {
	return attempts < openCodeOrgResolveMaxAttempts && !now.Before(nextAllowed)
}

// resolveOpenCodeOrgID 在 URL 未携带 org/wrk 段时，用已捕获的 Cookie 反查默认组织 ID。
// 做成变量以便单测注入，避免测试依赖真实网络。
var resolveOpenCodeOrgID = func(cookie string) (string, error) {
	return opencode.FetchDefaultOrgID(cookie, nil)
}

// openCodeCookieHeader 是 Cookie 过滤与拼装的唯一实现：同一个结果既用于保存凭据，
// 也用于直接请求 Console API，因此排除 auth.opencode.ai（授权流程专用）与含不安全字符的
// Cookie 必须只发生一次，避免两处过滤逻辑日后漂移出不一致的白名单。
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
	match := openCodeWorkspaceRe.FindStringSubmatch(u.Path)
	if match == nil {
		return ""
	}
	return match[1]
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
	orgResolveAttempts := 0
	var orgResolveNext time.Time
	var probeErr error
	for {
		cdp, cdpErr := browser.CDP(ctx)
		if cdpErr != nil {
			return "", "", fmt.Errorf("连接 OpenCode 登录浏览器失败: %w", cdpErr)
		}
		pageURL, urlErr := cdp.PageURL(ctx, openCodeHost)
		wsid := openCodeWorkspaceID(pageURL)
		cookies, cookieErr := cdp.BrowserCookies(ctx)
		header := openCodeCookieHeader(cookies)
		_ = cdp.Close()
		// CDP 探针失败既不终止轮询也不上报，会让持续失败只表现为「未捕获到有效凭证」，
		// 因此保留最后一次错误并入最终失败信息，让用户能区分页面地址与 Cookie 读取故障。
		if urlErr != nil {
			probeErr = fmt.Errorf("读取页面地址失败: %w", urlErr)
		} else if cookieErr != nil {
			probeErr = fmt.Errorf("读取浏览器 Cookie 失败: %w", cookieErr)
		}
		// Console v2 登录后前端路由可能停在 /console 或 /console/go 而不带 org/wrk 路径段，
		// 此时用已到手的会话 Cookie 反查默认组织，避免用户被迫手动跳到工作区页面。
		now := time.Now()
		if urlErr == nil && cookieErr == nil && header != "" && wsid == "" && openCodeConsoleURL(pageURL) &&
			shouldResolveOpenCodeOrgID(orgResolveAttempts, orgResolveNext, now) {
			orgResolveAttempts++
			orgResolveNext = now.Add(openCodeOrgResolveInterval)
			resolved, resolveErr := resolveOpenCodeOrgID(header)
			if resolveErr != nil {
				// 失败提示只记一次，避免日志淹没其他诊断信息；请求频率由上面的间隔与次数上限约束。
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
			return "", "", openCodeCaptureErr("未捕获到有效凭证（窗口已关闭）", probeErr)
		}
		select {
		case <-ctx.Done():
			return "", "", openCodeCaptureErr("未捕获到有效凭证（登录超时或已取消）", probeErr)
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

// openCodeCaptureErr 给凭证捕获失败的结论补上最后一次 CDP 探针错误，
// 避免用户只看到笼统提示而无法定位故障来源。
func openCodeCaptureErr(reason string, probeErr error) error {
	if probeErr == nil {
		return errors.New(reason)
	}
	return fmt.Errorf("%s: %w", reason, probeErr)
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
	// 账户页与登录鉴权主机分属不同来源：auth.opencode.ai 只承载授权流程，
	// 放行它会与 openCodeWorkspaceID/openCodeConsoleURL 的排除策略不一致。
	if browserauth.CookieDomainMatches(u.Hostname(), openCodeAuthHost) {
		return fmt.Errorf("OpenCode 账户页地址无效")
	}
	// 工作区标识由配置原样拼进该 URL，必须与 x-org-id 请求头共用同一白名单：
	// 只校验 scheme/host 会让误存的完整 URL 被拼成畸形路径且调用方毫无提示。
	match := openCodeWorkspaceRe.FindStringSubmatch(u.Path)
	if match == nil || !opencode.ValidWorkspaceID(match[1]) {
		return fmt.Errorf("OpenCode 账户页地址无效")
	}
	return nil
}
