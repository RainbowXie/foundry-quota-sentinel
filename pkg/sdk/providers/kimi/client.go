package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const kimiBaseURL = "https://www.kimi.com"
const kimiProtectedQuotaURL = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"
const kimiRefreshTokenURL = "https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken"
const kimiMaxResponseSize = 1 << 20
const kimiConnectUnauthenticated = "unauthenticated"

var kimiAllowedHosts = map[string]bool{"www.kimi.com": true}

func kimiAllowedHost(host string) bool {
	return kimiAllowedHosts[host]
}

var kimiRequestTimeout = 15 * time.Second

func kimiHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:       kimiRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     transport,
	}
}

func kimiEnforceNoRedirect(c *http.Client) *http.Client {
	if c == nil {
		return kimiHTTPClient(nil)
	}
	clone := *c
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if clone.Timeout == 0 {
		clone.Timeout = kimiRequestTimeout
	}
	return &clone
}

// ErrKimiRefreshFailed 表示长效会话刷新请求被拒绝，需要重新触发浏览器登录。
var ErrKimiRefreshFailed = errors.New("Kimi 会话刷新失败，请重新登录")

// RefreshResult 是 Token 轮换成功的响应结构。
type RefreshResult struct {
	AccessToken  string
	RefreshToken string
}

// KimiQuerier 负责与 Kimi 官方 Connect gRPC-Web 接口交互以查询配额及执行会话轮换。
type KimiQuerier struct {
	AccessToken  string
	RefreshToken string
	BaseURL      string
	Headers      map[string]string
	Client       *http.Client
}

// FetchQuota 使用当前持有的 AccessToken 查询 Kimi 原生配额数据。
func (q *KimiQuerier) FetchQuota(ctx context.Context) (*KimiQuotaData, error) {
	if strings.TrimSpace(q.AccessToken) == "" {
		return nil, fmt.Errorf("Kimi accessToken 为空")
	}
	endpoint := kimiProtectedQuotaURL
	if q.BaseURL != "" {
		u, err := url.Parse(q.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("Kimi base URL 无效: %w", err)
		}
		if u.Scheme != "https" {
			return nil, fmt.Errorf("Kimi base URL 必须为 https")
		}
		if !kimiAllowedHost(u.Hostname()) {
			return nil, fmt.Errorf("Kimi base URL 主机 %q 不在允许列表", u.Hostname())
		}
		endpoint = u.Scheme + "://" + u.Host + "/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"
	}

	reqCtx := ctx
	cancel := func() {}
	if _, ok := reqCtx.Deadline(); !ok {
		reqCtx, cancel = context.WithTimeout(reqCtx, kimiRequestTimeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKimiTransport, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+q.AccessToken)
	for name, value := range q.Headers {
		if value == "" || strings.ContainsAny(value, "\r\n") {
			continue
		}
		req.Header.Set(name, value)
	}

	client := kimiEnforceNoRedirect(q.Client)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrKimiTimeout, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrKimiTransport, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w (HTTP 401)", ErrKimiAuthExpired)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrKimiUnsupportedResponse, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, kimiMaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKimiTransport, err)
	}
	if len(body) > kimiMaxResponseSize {
		return nil, fmt.Errorf("%w: 响应超过 %d 字节", ErrKimiUnsupportedResponse, kimiMaxResponseSize)
	}

	data, err := ParseKimiQuota(string(body), time.Now())
	if err != nil {
		if isKimiAuthErrorCode(string(body)) {
			return nil, fmt.Errorf("%w: %v", ErrKimiAuthExpired, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrKimiUnsupportedResponse, err)
	}
	return data, nil
}

func isKimiAuthErrorCode(body string) bool {
	var cerr kimiConnectError
	if err := json.Unmarshal([]byte(body), &cerr); err != nil {
		return false
	}
	return cerr.Code == kimiConnectUnauthenticated
}

// Refresh 使用持有的 RefreshToken 请求官方接口完成双 Token 轮换。
func (q *KimiQuerier) Refresh(ctx context.Context) (RefreshResult, error) {
	if strings.TrimSpace(q.RefreshToken) == "" {
		return RefreshResult{}, fmt.Errorf("%w: 无 refresh_token", ErrKimiRefreshFailed)
	}
	b, _ := json.Marshal(q.RefreshToken)
	body := fmt.Sprintf(`{"refresh_token":%s}`, string(b))

	reqCtx := ctx
	cancel := func() {}
	if _, ok := reqCtx.Deadline(); !ok {
		reqCtx, cancel = context.WithTimeout(reqCtx, kimiRequestTimeout)
	}
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, kimiRefreshTokenURL, strings.NewReader(body))
	if err != nil {
		return RefreshResult{}, fmt.Errorf("%w: %v", ErrKimiTransport, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	for name, value := range q.Headers {
		if value == "" || strings.ContainsAny(value, "\r\n") {
			continue
		}
		req.Header.Set(name, value)
	}

	client := kimiEnforceNoRedirect(q.Client)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return RefreshResult{}, fmt.Errorf("%w: %v", ErrKimiTimeout, err)
		}
		return RefreshResult{}, fmt.Errorf("%w: %v", ErrKimiTransport, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return RefreshResult{}, fmt.Errorf("%w (HTTP %d)", ErrKimiRefreshFailed, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RefreshResult{}, fmt.Errorf("%w: HTTP %d", ErrKimiRefreshFailed, resp.StatusCode)
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, kimiMaxResponseSize))
	var rotated struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(respBody, &rotated); err != nil {
		return RefreshResult{}, fmt.Errorf("%w: %v", ErrKimiRefreshFailed, err)
	}
	if rotated.AccessToken == "" || rotated.RefreshToken == "" {
		return RefreshResult{}, fmt.Errorf("%w: 响应缺少轮换 token", ErrKimiRefreshFailed)
	}
	return RefreshResult{AccessToken: rotated.AccessToken, RefreshToken: rotated.RefreshToken}, nil
}

// FetchQuotaWithRefresh 在捕获到 401 或鉴权失效时自动尝试一次 Token 轮换并重试配额获取。
func (q *KimiQuerier) FetchQuotaWithRefresh(ctx context.Context) (*KimiQuotaData, *RefreshResult, error) {
	data, err := q.FetchQuota(ctx)
	if err == nil {
		return data, nil, nil
	}
	if !errors.Is(err, ErrKimiAuthExpired) {
		return nil, nil, err
	}
	rr, rerr := q.Refresh(ctx)
	if rerr != nil {
		return nil, nil, rerr
	}
	q.AccessToken = rr.AccessToken
	q.RefreshToken = rr.RefreshToken
	data, err = q.FetchQuota(ctx)
	if err != nil {
		return nil, nil, err
	}
	return data, &rr, nil
}
