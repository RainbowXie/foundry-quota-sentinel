package browserauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
)

// Cookie is the shared representation used by both capture and injection.
// DevTools Cookies carry additional fields (expires, sameSite, ...) but the
// shared package only needs the subset every provider consumes.
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Secure   bool
	HTTPOnly bool
}

// cookieDomainMatches reports whether the given cookie's effective domain is
// the same as or a subdomain of policyDomain. policyDomain must be a bare
// hostname without a leading dot; an empty Domain means host-only.
func cookieDomainMatches(cookieDomain, policyDomain string) bool {
	cookieDomain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookieDomain)), ".")
	policyDomain = strings.ToLower(strings.TrimSpace(policyDomain))
	if cookieDomain == "" || policyDomain == "" {
		return false
	}
	return cookieDomain == policyDomain || strings.HasSuffix(cookieDomain, "."+policyDomain)
}

// validateCookie rejects names, values, and domains that would let a
// captured credential smuggle a control character into a header or
// configuration. Empty names or values are also rejected.
func validateCookie(cookie Cookie) error {
	if cookie.Name == "" || cookie.Value == "" {
		return fmt.Errorf("cookie 名称或值为空")
	}
	if strings.ContainsAny(cookie.Name, ";\r\n") || strings.ContainsAny(cookie.Value, ";\r\n") {
		return fmt.Errorf("cookie 包含非法字符")
	}
	if cookie.Domain == "" {
		return fmt.Errorf("cookie 域为空")
	}
	return nil
}

// cookieHeader serialises a list of cookies into a "name=value; name=value"
// header value. The order is preserved so callers can keep deterministic
// ordering when they care (e.g. the OpenCode Cookie field).
func cookieHeader(cookies []Cookie) (string, error) {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if err := validateCookie(cookie); err != nil {
			return "", err
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; "), nil
}

// BrowserCookies calls Storage.getCookies on the browser endpoint and
// returns the canonical Cookie list.
func (c *Client) BrowserCookies(ctx context.Context) ([]Cookie, error) {
	raw, err := c.Call(ctx, "Storage.getCookies", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Cookies []struct {
			Name     string `json:"name"`
			Value    string `json:"value"`
			Domain   string `json:"domain"`
			Path     string `json:"path"`
			Secure   bool   `json:"secure"`
			HTTPOnly bool   `json:"httpOnly"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解析 Storage.getCookies 响应失败: %w", err)
	}
	out := make([]Cookie, 0, len(result.Cookies))
	for _, ck := range result.Cookies {
		out = append(out, Cookie{
			Name:     ck.Name,
			Value:    ck.Value,
			Domain:   ck.Domain,
			Path:     ck.Path,
			Secure:   ck.Secure,
			HTTPOnly: ck.HTTPOnly,
		})
	}
	return out, nil
}

// BrowserUserAgent returns the browser's User-Agent string from
// Browser.getVersion. The string is rejected if it contains a control
// character that could be used to forge a request header.
func (c *Client) BrowserUserAgent(ctx context.Context) (string, error) {
	raw, err := c.Call(ctx, "Browser.getVersion", nil)
	if err != nil {
		return "", err
	}
	var result struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("解析 Browser.getVersion 响应失败: %w", err)
	}
	if strings.TrimSpace(result.UserAgent) == "" || strings.ContainsAny(result.UserAgent, "\r\n") {
		return "", fmt.Errorf("浏览器返回的 User-Agent 无效")
	}
	return result.UserAgent, nil
}

// SetCookies installs cookies through Storage.setCookies. Each cookie must
// pass validateCookie; a cookie whose name or value is unsafe is dropped
// before any network call so partial injection can never leak bad data into
// a session.
//
// Injection is per-cookie and degrades rather than aborts: a single cookie
// that Chrome rejects (e.g. a __Host- cookie that was captured with a Domain)
// is logged by name and skipped, and the remaining cookies are still
// injected. The account-page browser must stay open until the user closes
// it, so a non-fatal cookie failure must not bubble up as a flow error.
// The returned error is non-nil only when no cookie could be injected at all
// and at least one was attempted — that is the case where the saved
// credential is genuinely unusable and the caller should surface an error
// instead of opening an unauthenticated page.
func (c *Client) SetCookies(ctx context.Context, cookies []Cookie) error {
	safe := make([]Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if err := validateCookie(cookie); err != nil {
			log.Printf("browserauth: 跳过 cookie %q（长度 %d）: %v", cookie.Name, len(cookie.Value), err)
			continue
		}
		safe = append(safe, cookie)
	}
	if len(safe) == 0 {
		if len(cookies) == 0 {
			return nil
		}
		return fmt.Errorf("无可注入的 cookie（共 %d 个全部被过滤）", len(cookies))
	}
	injected := 0
	for _, cookie := range safe {
		// __Host- prefixed cookies must not carry a Domain; Chrome
		// rejects them and the whole replay would otherwise fail. The
		// cookie is host-scoped to the page's own origin.
		domain := cookie.Domain
		if strings.HasPrefix(cookie.Name, "__Host-") {
			domain = ""
		}
		params := map[string]any{
			"name":     cookie.Name,
			"value":    cookie.Value,
			"domain":   domain,
			"path":     cookie.Path,
			"secure":   cookie.Secure,
			"httpOnly": cookie.HTTPOnly,
		}
		if _, err := c.Call(ctx, "Storage.setCookies", []map[string]any{params}); err != nil {
			log.Printf("browserauth: 注入 cookie %q 失败（长度 %d）: %v", cookie.Name, len(cookie.Value), err)
			continue
		}
		injected++
	}
	if injected == 0 {
		return fmt.Errorf("cookie 注入全部失败（共 %d 个）", len(safe))
	}
	return nil
}

// SetUserAgent applies Emulation.setUserAgentOverride. An empty value
// returns an error so callers cannot accidentally clear the override.
func (c *Client) SetUserAgent(ctx context.Context, userAgent string) error {
	if userAgent == "" || strings.ContainsAny(userAgent, "\r\n") {
		return fmt.Errorf("浏览器 User-Agent 无效")
	}
	if _, err := c.Call(ctx, "Emulation.setUserAgentOverride", map[string]any{
		"userAgent": userAgent,
	}); err != nil {
		return err
	}
	return nil
}

// parseHTTPLoopbackURL accepts only HTTPS URLs whose host is the provider's
// loopback or domain. The caller passes the permitted hostnames.
func parseHTTPLoopbackURL(rawURL string, allowedHosts ...string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("URL 解析失败: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("URL 必须为 https")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range allowedHosts {
		if cookieDomainMatches(host, allowed) {
			return nil
		}
	}
	return fmt.Errorf("URL 域 %q 不在允许列表", host)
}
