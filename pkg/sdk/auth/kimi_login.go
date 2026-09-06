package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

// RunKimiLogin 启动 CDP 浏览器引导用户完成 Kimi 登录，并捕获 Bearer Token、RefreshToken 与 Cookies 构建完整信封。
func RunKimiLogin(validate func(accessToken string) bool) (kimi.KimiAuthEnvelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchKimiBrowser(ctx, kimiLoginURL)
	if err != nil {
		return kimi.KimiAuthEnvelope{}, err
	}
	return runKimiLogin(ctx, browser, validate)
}

func runKimiLogin(ctx context.Context, browser kimiLoginBrowser, validate func(string) bool) (envelope kimi.KimiAuthEnvelope, err error) {
	defer func() {
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 Kimi 登录浏览器失败: %w", closeErr)
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return kimi.KimiAuthEnvelope{}, fmt.Errorf("连接 Kimi 登录浏览器失败: %w", err)
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		_ = cdp.Close()
		return kimi.KimiAuthEnvelope{}, fmt.Errorf("启用 Kimi 网络事件失败: %w", err)
	}

	events := cdp.Events()
	type candidate struct {
		token   string
		headers map[string]string
	}
	candidates := make(map[string]candidate)
	urls := make(map[string]string)
	pending := make(map[string]map[string]string)
	pendingToken := make(map[string]string)
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			decoded, dOK := browserauth.DecodeRequestHeadersEvent(event)
			if !dOK {
				continue
			}
			requestID, requestURL := decoded.RequestID, decoded.URL
			headers := decoded.Headers
			token := browserauth.BearerToken(headers)

			if requestID != "" && requestURL != "" {
				urls[requestID] = requestURL
				for t, h := range pending {
					if pendingToken[t] == requestID && isOnKimiHost(requestURL) {
						candidates[t] = candidate{token: t, headers: h}
						delete(pending, t)
						delete(pendingToken, t)
					}
				}
			}
			if token == "" || requestID == "" {
				continue
			}
			if u, ok := urls[requestID]; ok {
				if isOnKimiHost(u) {
					candidates[token] = candidate{token: token, headers: headers}
					delete(pending, token)
					delete(pendingToken, token)
				}
			} else {
				pending[token] = headers
				pendingToken[token] = requestID
			}
		case <-poll.C:
		}

		pageURL, urlErr := cdp.PageURL(ctx, kimiHost)
		if urlErr == nil && pageURL != "" {
			for t, h := range pending {
				rid := pendingToken[t]
				if u, ok := urls[rid]; ok && isOnKimiHost(u) {
					candidates[t] = candidate{token: t, headers: h}
					delete(pending, t)
					delete(pendingToken, t)
				}
			}
		}

		if len(candidates) > 0 {
			break
		}
		if browser.Exited() {
			if len(candidates) > 0 {
				break
			}
			return kimi.KimiAuthEnvelope{}, fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return kimi.KimiAuthEnvelope{}, fmt.Errorf("未捕获到有效凭证（登录超时或已取消）")
		default:
		}
	}

	capturedCookies, _ := cdp.BrowserCookies(ctx)
	cookieHeader := kimiCookieHeader(capturedCookies)
	refreshToken := kimiReadLocalStorage(ctx, cdp, "refresh_token")
	_ = cdp.Close()
	if err := browser.Close(); err != nil {
		return kimi.KimiAuthEnvelope{}, fmt.Errorf("关闭 Kimi 登录浏览器失败: %w", err)
	}

	for _, c := range candidates {
		if validate(c.token) {
			return kimiBuildEnvelope(c.token, refreshToken, cookieHeader, c.headers), nil
		}
	}
	return kimi.KimiAuthEnvelope{}, fmt.Errorf("未找到可验证的 Kimi 凭证")
}

func kimiReadLocalStorage(ctx context.Context, cdp kimiCDP, key string) string {
	keyJSON, _ := json.Marshal(key)
	expr := fmt.Sprintf(`JSON.stringify(localStorage.getItem(%s))`, string(keyJSON))
	raw, err := cdp.Evaluate(ctx, expr)
	if err != nil {
		return ""
	}
	var envelope struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return ""
	}
	var s string
	if json.Unmarshal([]byte(envelope.Result.Value), &s) != nil {
		return ""
	}
	return s
}

func kimiBuildEnvelope(token, refreshToken, cookieHeader string, headers map[string]string) kimi.KimiAuthEnvelope {
	env := kimi.KimiAuthEnvelope{Version: kimi.KimiAuthEnvelopeVersion()}
	h2f := map[string]string{
		"x-msh-device-id": "x_msh_device_id",
		"x-traffic-id":    "x_traffic_id",
		"x-msh-platform":  "x_msh_platform",
		"x-msh-version":   "x_msh_version",
		"x-language":      "x_language",
		"r-timezone":      "r_timezone",
		"user-agent":      "user_agent",
	}
	_ = env.SetField("accessToken", token)
	if refreshToken != "" {
		_ = env.SetField("refreshToken", refreshToken)
	}
	if cookieHeader != "" {
		_ = env.SetField("cookie", cookieHeader)
	}
	for headerName, fieldName := range h2f {
		if v, ok := headers[headerName]; ok && v != "" {
			_ = env.SetField(fieldName, v)
		}
	}
	return env
}

func kimiCookieHeader(cookies []browserauth.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range filterKimiCookies(cookies) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func kimiTokenFromEvent(event browserauth.Event) (token, requestID, requestURL string) {
	decoded, ok := browserauth.DecodeRequestHeadersEvent(event)
	if !ok {
		return "", "", ""
	}
	return browserauth.BearerToken(decoded.Headers), decoded.RequestID, decoded.URL
}

func filterKimiCookies(cookies []browserauth.Cookie) []browserauth.Cookie {
	out := make([]browserauth.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !browserauth.CookieDomainMatches(cookie.Domain, kimiHost) || cookie.Value == "" {
			continue
		}
		if strings.ContainsAny(cookie.Name+cookie.Value, ";\r\n") {
			continue
		}
		out = append(out, cookie)
	}
	return out
}
