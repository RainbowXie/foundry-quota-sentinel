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

// SetCookies installs cookies through Storage.setCookies using the
// protocol's {cookies:[...]} envelope. Each cookie must pass
// validateCookie; the call is rejected if any entry is unsafe so
// partial injection can never leak bad data into a session.
//
// The call is strict: a single rejected cookie aborts the batch and
// returns an error. OpenCode and Ollama rely on this all-or-nothing
// behaviour for their account-page flows. DeepSeek, whose replayed
// cookie set is best-effort over a captured snapshot, uses
// SetCookiesBestEffort instead so one non-injectable cookie does not
// flash-close the page.
func (c *Client) SetCookies(ctx context.Context, cookies []Cookie) error {
	for _, cookie := range cookies {
		if err := validateCookie(cookie); err != nil {
			return err
		}
	}
	envelope := make([]map[string]any, 0, len(cookies))
	for _, cookie := range cookies {
		envelope = append(envelope, cookieParam(cookie))
	}
	if _, err := c.Call(ctx, "Storage.setCookies", map[string]any{"cookies": envelope}); err != nil {
		return fmt.Errorf("注入 cookie 失败: %w", err)
	}
	return nil
}

// SetCookiesBestEffort injects cookies one Storage.setCookies call at a
// time using the {cookies:[...]} envelope, degrading rather than
// aborting. A cookie Chrome rejects (e.g. a __Host- cookie captured
// with a Domain, which Storage.setCookies refuses) is logged by name
// and value length and skipped; the remaining cookies are still set.
// The account-page browser must stay open until the user closes it, so
// a non-fatal cookie failure must not bubble up as a flow error.
//
// CookieInjectionResult reports how many cookies were injected and
// which names failed, by name only — no value is ever logged. The
// caller decides whether an all-failed result is fatal.
type CookieInjectionResult struct {
	Injected int
	Failed   []string
}

func (c *Client) SetCookiesBestEffort(ctx context.Context, cookies []Cookie) CookieInjectionResult {
	var result CookieInjectionResult
	for _, cookie := range cookies {
		if err := validateCookie(cookie); err != nil {
			log.Printf("browserauth: 跳过 cookie %q（长度 %d）: %v", cookie.Name, len(cookie.Value), err)
			result.Failed = append(result.Failed, cookie.Name)
			continue
		}
		if _, err := c.Call(ctx, "Storage.setCookies", map[string]any{
			"cookies": []map[string]any{cookieParam(cookie)},
		}); err != nil {
			log.Printf("browserauth: 注入 cookie %q 失败（长度 %d）: %v", cookie.Name, len(cookie.Value), err)
			result.Failed = append(result.Failed, cookie.Name)
			continue
		}
		result.Injected++
	}
	return result
}

// cookieParam builds the Storage.setCookies CookieParam for one cookie.
// __Host- prefixed cookies must not carry a Domain and must be scoped
// to an https URL instead; Chrome rejects a __Host- cookie that sets a
// domain. Non-host cookies keep their captured Domain.
func cookieParam(cookie Cookie) map[string]any {
	domain := cookie.Domain
	url := ""
	if strings.HasPrefix(cookie.Name, "__Host-") {
		domain = ""
		url = "https://" + strings.TrimPrefix(cookie.Domain, ".")
	}
	params := map[string]any{
		"name":     cookie.Name,
		"value":    cookie.Value,
		"secure":   cookie.Secure,
		"httpOnly": cookie.HTTPOnly,
	}
	if domain != "" {
		params["domain"] = domain
	} else if url != "" {
		params["url"] = url
	}
	if cookie.Path != "" {
		params["path"] = cookie.Path
	}
	return params
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
