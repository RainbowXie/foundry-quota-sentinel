package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

// errOpenCodeTestResolve 模拟 /console/api/orgs 不可用（未登录、限流等）。
var errOpenCodeTestResolve = errors.New("orgs api unavailable")

// errOpenCodeTestProbe 模拟 CDP 探针持续失败（页面地址或 Cookie 读取故障）。
var errOpenCodeTestProbe = errors.New("cdp probe failed")

type fakeOpenCodeBrowser struct {
	cdp        *fakeOpenCodeCDP
	exited     bool
	closed     bool
	onClose    func()
	onPoll     func()
	polls      int
	operations []string
}

func (b *fakeOpenCodeBrowser) CDP(context.Context) (openCodeCDP, error) {
	b.polls++
	if b.onPoll != nil {
		b.onPoll()
	}
	return b.cdp, nil
}
func (b *fakeOpenCodeBrowser) Exited() bool { return b.exited }
func (b *fakeOpenCodeBrowser) Wait() error {
	b.operations = append(b.operations, "wait")
	return nil
}
func (b *fakeOpenCodeBrowser) Close() error {
	b.closed = true
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

type fakeOpenCodeCDP struct {
	browser      *fakeOpenCodeBrowser
	cookieHeader string
	pageURL      string
	pageURLErr   error
	cookieErr    error
	closed       bool
}

func (c *fakeOpenCodeCDP) BrowserCookies(context.Context) ([]browserauth.Cookie, error) {
	if c.cookieErr != nil {
		return nil, c.cookieErr
	}
	if c.cookieHeader == "" {
		return nil, nil
	}
	return openCodeSavedCookies(c.cookieHeader)
}

// PageURL 返回浏览器当前地址；空地址模拟仍停留在鉴权页的情况。
func (c *fakeOpenCodeCDP) PageURL(context.Context, ...string) (string, error) {
	if c.pageURLErr != nil {
		return "", c.pageURLErr
	}
	if c.pageURL == "" {
		return "https://auth.opencode.ai/authorize", nil
	}
	return c.pageURL, nil
}
func (c *fakeOpenCodeCDP) SetCookies(_ context.Context, _ []browserauth.Cookie) error {
	c.browser.operations = append(c.browser.operations, "set-cookie")
	return nil
}
func (c *fakeOpenCodeCDP) Navigate(context.Context, string) error {
	c.browser.operations = append(c.browser.operations, "navigate")
	return nil
}
func (c *fakeOpenCodeCDP) Close() error {
	c.closed = true
	return nil
}

func newFakeOpenCodeBrowser(cookieHeader, pageURL string, onClose func()) *fakeOpenCodeBrowser {
	browser := &fakeOpenCodeBrowser{
		onClose: onClose,
	}
	browser.cdp = &fakeOpenCodeCDP{
		browser:      browser,
		cookieHeader: cookieHeader,
		pageURL:      pageURL,
	}
	return browser
}

// stubResolveOpenCodeOrgID 替换组织反查实现，避免单测触网，并在用例结束时恢复。
func stubResolveOpenCodeOrgID(t *testing.T, fn func(string) (string, error)) *[]string {
	t.Helper()
	calls := &[]string{}
	prev := resolveOpenCodeOrgID
	resolveOpenCodeOrgID = func(cookie string) (string, error) {
		*calls = append(*calls, cookie)
		return fn(cookie)
	}
	t.Cleanup(func() { resolveOpenCodeOrgID = prev })
	return calls
}

func TestOpenCodeLoginURLTargetsConsoleV2(t *testing.T) {
	if openCodeAuthURL != "https://opencode.ai/console/login?next=%2Fconsole%2Fgo" {
		t.Fatalf("login url = %q, want the Console v2 login page", openCodeAuthURL)
	}
}

func TestOpenCodeCookieHeaderKeepsOnlyMainDomain(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "session", Value: "good", Domain: ".opencode.ai", Secure: true, HTTPOnly: true},
		{Name: "oauth", Value: "skip", Domain: "auth.opencode.ai", Secure: true, HTTPOnly: true},
	}
	if got := openCodeCookieHeader(cookies); got != "session=good" {
		t.Fatalf("header=%q", got)
	}
}

func TestOpenCodeCookieHeaderKeepsHostPrefixedSessionCookie(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "__Host-console_session", Value: "abc123_-", Domain: "opencode.ai", Path: "/", Secure: true, HTTPOnly: true},
	}
	if got := openCodeCookieHeader(cookies); got != "__Host-console_session=abc123_-" {
		t.Fatalf("header=%q", got)
	}
}

func TestOpenCodeCookieHeaderDropsUnsafeCookies(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "bad name", Value: "ok", Domain: "opencode.ai"},
		{Name: "session", Value: "bad;value", Domain: "opencode.ai"},
		{Name: "session", Value: "good", Domain: "opencode.ai"},
	}
	// 过滤只在 openCodeCookieHeader 内实现一次；删除重复的 filterOpenCodeCookies
	// 后该用例继续守住「域名 + 排除 auth 子域 + 字符安全」三项判据。
	if got := openCodeCookieHeader(cookies); got != "session=good" {
		t.Fatalf("header=%q, want only the safe main-domain cookie", got)
	}
}

func TestShouldResolveOpenCodeOrgID(t *testing.T) {
	now := time.Now()
	if !shouldResolveOpenCodeOrgID(0, time.Time{}, now) {
		t.Fatal("the first poll must be allowed to resolve the default org")
	}
	if shouldResolveOpenCodeOrgID(1, now.Add(openCodeOrgResolveInterval), now) {
		t.Fatal("a retry inside the rate-limit interval must be rejected")
	}
	if !shouldResolveOpenCodeOrgID(1, now, now) {
		t.Fatal("a retry after the rate-limit interval must be allowed")
	}
	if shouldResolveOpenCodeOrgID(openCodeOrgResolveMaxAttempts, time.Time{}, now) {
		t.Fatal("attempts beyond the cap must be rejected")
	}
}

func TestOpenCodeWorkspaceIDFromConsoleURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "org identifier", url: "https://opencode.ai/console/org_01JXYZ/go", want: "org_01JXYZ"},
		{name: "wrk identifier", url: "https://opencode.ai/console/wrk_abc123/go", want: "wrk_abc123"},
		{name: "workspace route without trailing segment", url: "https://opencode.ai/console/org_01JXYZ", want: "org_01JXYZ"},
		{name: "console root has no identifier", url: "https://opencode.ai/console", want: ""},
		{name: "console go route has no identifier", url: "https://opencode.ai/console/go", want: ""},
		{name: "login page has no identifier", url: "https://opencode.ai/console/login?next=%2Fconsole%2Fgo", want: ""},
		{name: "workspace token outside the console route", url: "https://opencode.ai/docs/org_fake123/go", want: ""},
		{name: "workspace token deeper in a non-console route", url: "https://opencode.ai/workspaces/wrk_fake123/go", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openCodeWorkspaceID(tt.url); got != tt.want {
				t.Fatalf("workspace=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenCodeWorkspaceIDIgnoresAuthSubdomain(t *testing.T) {
	if got := openCodeWorkspaceID("https://auth.opencode.ai/console/org_fake123/go"); got != "" {
		t.Fatalf("workspace=%q", got)
	}
}

// TestValidateOpenCodePageURL 锁定账户页地址白名单：只接受 https + opencode.ai 主机，
// 且必须与 openCodeWorkspaceID/openCodeConsoleURL 一样排除 auth 子域，
// 并将工作区标识约束为 /console/<org_|wrk_> 段：配置里误存的完整 URL 拼出的畸形路径
// 必须在这里失败，而不是被静默交给浏览器导航。
func TestValidateOpenCodePageURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "console org page", url: "https://opencode.ai/console/org_x/go"},
		{name: "console wrk page", url: "https://opencode.ai/console/wrk_abc123/go"},
		{name: "console workspace route without go", url: "https://opencode.ai/console/org_01JXYZ"},
		{name: "console root has no workspace", url: "https://opencode.ai/console", wantErr: true},
		{name: "console go route has no workspace", url: "https://opencode.ai/console/go", wantErr: true},
		{name: "pasted full url", url: "https://opencode.ai/console/https://opencode.ai/console/org_01JXYZ/go/go", wantErr: true},
		{name: "bare identifier segment", url: "https://opencode.ai/console/01JXYZ/go", wantErr: true},
		{name: "prefix without body", url: "https://opencode.ai/console/org_/go", wantErr: true},
		{name: "hyphenated body", url: "https://opencode.ai/console/org_01-xyz/go", wantErr: true},
		{name: "workspace token outside console route", url: "https://opencode.ai/docs/org_x/go", wantErr: true},
		{name: "auth subdomain", url: "https://auth.opencode.ai/console/org_x/go", wantErr: true},
		{name: "plain http", url: "http://opencode.ai/console/org_x/go", wantErr: true},
		{name: "lookalike host", url: "https://evil-opencode.ai/console/org_x/go", wantErr: true},
		{name: "foreign host", url: "https://example.com/console/org_x/go", wantErr: true},
		{name: "unparsable url", url: "https://opencode.ai/%zz", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOpenCodePageURL(tt.url)
			if tt.wantErr && err == nil {
				t.Fatalf("validateOpenCodePageURL(%q) must be rejected", tt.url)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateOpenCodePageURL(%q) = %v, want nil", tt.url, err)
			}
		})
	}
}

// TestRunOpenCodePageRejectsMalformedWorkspaceURL 守住审阅证据里的具体形态：
// 配置里误存完整 Console URL 时，公开入口必须在启动浏览器前就报错。
func TestRunOpenCodePageRejectsMalformedWorkspaceURL(t *testing.T) {
	prev := launchOpenCodeBrowser
	launchOpenCodeBrowser = func(context.Context, string) (openCodeLoginBrowser, error) {
		t.Fatal("a malformed account page URL must not launch a browser")
		return nil, nil
	}
	t.Cleanup(func() { launchOpenCodeBrowser = prev })
	err := RunOpenCodePage("https://opencode.ai/console/https://opencode.ai/console/org_01JXYZ/go/go", "console_session=good")
	if err == nil {
		t.Fatal("a pasted full Console URL must be rejected before any browser launch")
	}
}

func TestOpenCodeConsoleURLClassification(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "console root", url: "https://opencode.ai/console", want: true},
		{name: "console go route", url: "https://opencode.ai/console/go", want: true},
		{name: "console workspace route", url: "https://opencode.ai/console/org_01JXYZ/go", want: true},
		{name: "trailing slash", url: "https://opencode.ai/console/", want: true},
		{name: "login page", url: "https://opencode.ai/console/login?next=%2Fconsole%2Fgo", want: false},
		{name: "legacy authorise page", url: "https://auth.opencode.ai/authorize", want: false},
		{name: "auth subdomain console page", url: "https://auth.opencode.ai/console/go", want: false},
		{name: "foreign host", url: "https://example.com/console/go", want: false},
		{name: "unparsable url", url: "https://opencode.ai/%zz", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openCodeConsoleURL(tt.url); got != tt.want {
				t.Fatalf("openCodeConsoleURL(%q)=%v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestRunOpenCodeLoginValidatesAfterClose(t *testing.T) {
	closed := false
	browser := newFakeOpenCodeBrowser("console_session=good", "https://opencode.ai/console/wrk_abc123/go", func() { closed = true })
	calls := stubResolveOpenCodeOrgID(t, func(string) (string, error) {
		t.Fatal("URL already carries a workspace id, no org lookup expected")
		return "", nil
	})
	cookie, wsid, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("browser was not closed before returning")
	}
	if cookie == "" || wsid != "wrk_abc123" {
		t.Fatalf("credentials = (%q, %q)", cookie, wsid)
	}
	if len(*calls) != 0 {
		t.Fatalf("org lookup calls = %v, want none", *calls)
	}
}

func TestRunOpenCodeLoginResolvesDefaultOrgID(t *testing.T) {
	browser := newFakeOpenCodeBrowser("__Host-console_session=abc123", "https://opencode.ai/console/go", func() {})
	calls := stubResolveOpenCodeOrgID(t, func(cookie string) (string, error) {
		if !strings.Contains(cookie, "__Host-console_session=abc123") {
			t.Fatalf("resolver cookie header = %q, want the captured session", cookie)
		}
		return "org_01JXYZ", nil
	})
	cookie, wsid, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if wsid != "org_01JXYZ" {
		t.Fatalf("workspace=%q, want the resolved default org", wsid)
	}
	if cookie != "__Host-console_session=abc123" {
		t.Fatalf("cookie=%q", cookie)
	}
	if len(*calls) != 1 {
		t.Fatalf("org lookup calls = %d, want exactly 1", len(*calls))
	}
}

func TestRunOpenCodeLoginSkipsOrgIDResolveOnLoginPage(t *testing.T) {
	browser := newFakeOpenCodeBrowser("console_session=good", "https://opencode.ai/console/login?next=%2Fconsole%2Fgo", func() {})
	browser.exited = true
	stubResolveOpenCodeOrgID(t, func(string) (string, error) {
		t.Fatal("the login page must not trigger an org lookup")
		return "", nil
	})
	_, _, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
	if err == nil {
		t.Fatal("expected error while the browser still sits on the login page")
	}
}

// TestRunOpenCodeLoginRateLimitsOrgIDResolve 守住热循环回归：
// 反查持续失败时，300ms 轮询不得每轮都重发 /console/api/orgs。
func TestRunOpenCodeLoginRateLimitsOrgIDResolve(t *testing.T) {
	browser := newFakeOpenCodeBrowser("console_session=good", "https://opencode.ai/console/go", func() {})
	// 第二次轮询后让浏览器退出，避免用例依赖 5 秒的重试间隔。
	browser.onPoll = func() {
		if browser.polls >= 2 {
			browser.exited = true
		}
	}
	calls := stubResolveOpenCodeOrgID(t, func(string) (string, error) {
		return "", errOpenCodeTestResolve
	})
	_, _, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
	if err == nil {
		t.Fatal("expected error when no workspace id could be resolved")
	}
	if len(*calls) != 1 {
		t.Fatalf("org lookup calls = %d across %d polls, want 1", len(*calls), browser.polls)
	}
}

// TestRunOpenCodeLoginReportsLastProbeError 守住静默吞错回归：
// CDP 探针持续失败时，失败结论必须带上最后一次探针错误。
func TestRunOpenCodeLoginReportsLastProbeError(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*fakeOpenCodeCDP)
		wantMsg string
	}{
		{
			name:    "page url probe",
			prepare: func(c *fakeOpenCodeCDP) { c.pageURLErr = errOpenCodeTestProbe },
			wantMsg: "读取页面地址失败",
		},
		{
			name:    "cookie probe",
			prepare: func(c *fakeOpenCodeCDP) { c.cookieErr = errOpenCodeTestProbe },
			wantMsg: "读取浏览器 Cookie 失败",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			browser := newFakeOpenCodeBrowser("console_session=good", "https://opencode.ai/console/go", func() {})
			browser.exited = true
			tt.prepare(browser.cdp)
			stubResolveOpenCodeOrgID(t, func(string) (string, error) {
				t.Fatal("a failing CDP probe must not trigger an org lookup")
				return "", nil
			})
			_, _, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
			if err == nil {
				t.Fatal("expected error when the CDP probe keeps failing")
			}
			if !strings.Contains(err.Error(), "未捕获到有效凭证（窗口已关闭）") {
				t.Fatalf("error=%q, want the capture-failure reason", err)
			}
			if !errors.Is(err, errOpenCodeTestProbe) || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("error=%q, want the last probe error attached (%s)", err, tt.wantMsg)
			}
		})
	}
}

func TestRunOpenCodeLoginKeepsPollingWhenOrgIDResolveFails(t *testing.T) {
	browser := newFakeOpenCodeBrowser("console_session=good", "https://opencode.ai/console/go", func() {})
	browser.exited = true
	calls := stubResolveOpenCodeOrgID(t, func(string) (string, error) {
		return "", errOpenCodeTestResolve
	})
	_, _, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool { return true })
	if err == nil {
		t.Fatal("expected error when no workspace id could be resolved")
	}
	if !strings.Contains(err.Error(), "未捕获到有效凭证") {
		t.Fatalf("error=%q, want the capture-timeout message", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("org lookup calls = %d, want 1 attempt before the exit check", len(*calls))
	}
}

func TestRunOpenCodeLoginRejectsEmptyCredentials(t *testing.T) {
	browser := newFakeOpenCodeBrowser("", "", func() {})
	browser.exited = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := runOpenCodeLogin(ctx, browser, func(string, string) bool { return true })
	if err == nil {
		t.Fatal("expected error when no workspace is observed")
	}
}

func TestOpenCodeSavedCookiesKeepsHostPrefixedCookie(t *testing.T) {
	cookies, err := openCodeSavedCookies("__Host-console_session=abc123; console_session=def456")
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 2 {
		t.Fatalf("cookies = %d, want 2", len(cookies))
	}
	if cookies[0].Name != "__Host-console_session" || cookies[0].Value != "abc123" {
		t.Fatalf("first cookie = %+v", cookies[0])
	}
	// Domain 只作为注入 URL 的来源：browserauth.cookieParam 会对 __Host- 前缀
	// 自动清空 Domain 并改用 https URL，符合 Chrome 的宿主前缀约束。
	for _, cookie := range cookies {
		if cookie.Domain != openCodeHost || cookie.Path != "/" || !cookie.Secure || !cookie.HTTPOnly {
			t.Fatalf("cookie = %+v, want host-only opencode.ai defaults", cookie)
		}
	}
}

func TestOpenCodeSavedCookiesRejectsMalformedHeader(t *testing.T) {
	for _, header := range []string{"", "console_session", "=value", "console_session=ok; console_session=dup", "con sole=ok"} {
		if _, err := openCodeSavedCookies(header); err == nil {
			t.Fatalf("header %q must be rejected", header)
		}
	}
}
