package quota

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

// kimiBaseURL is the OBSERVED Kimi data host (Connect gRPC-Web over JSON).
const kimiBaseURL = "https://www.kimi.com"

// kimiAllowedHosts is the closed set of hosts the Bearer token may be sent to.
// The protected quota endpoint lives on www.kimi.com (OBSERVED); no other host
// may receive the credential. A redirect/override to an unapproved host is
// rejected before the request is sent.
var kimiAllowedHosts = map[string]bool{"www.kimi.com": true}

func kimiAllowedHost(host string) bool {
	return kimiAllowedHosts[host]
}

// kimiProtectedQuotaURL is the OBSERVED protected quota endpoint: a Buf
// Connect POST to the membership service's GetSubscriptionStats method.
const kimiProtectedQuotaURL = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"

// kimiRefreshTokenURL is the OBSERVED durable refresh endpoint: a POST to the
// auth host's AuthService/RefreshToken with body {"refresh_token":"<jwt>"} (NO
// Bearer header — the refresh_token is in the body). Returns
// {"accessToken":"<new>","refreshToken":"<new>"} — BOTH tokens rotate.
const kimiRefreshTokenURL = "https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken"

// kimiRequestTimeout bounds the protected quota request.
var kimiRequestTimeout = 15 * time.Second

// kimiHTTPClient returns an http.Client that NEVER follows redirects. The
// Bearer accessToken (quota) and the refresh_token (refresh) are credentials
// scoped to www.kimi.com / auth.kimi.com only; a redirect off those hosts
// could carry the credential to an attacker-controlled host. The OBSERVED
// Connect endpoints never redirect, so any redirect is treated as anomalous
// and refused: the 3xx response is returned to the caller, which classifies
// it as an unsupported/non-2xx response. CheckRedirect stops following by
// returning http.ErrUseLastResponse (the default would follow up to 10 hops
// with Authorization re-attached per Go's redirect rules).
func kimiHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:       kimiRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     transport,
	}
}

// kimiEnforceNoRedirect returns a client equivalent to c but guaranteed to
// refuse redirects. Production constructs the client via kimiHTTPClient, but a
// test may inject its own client (with a fake transport) whose CheckRedirect
// is the unsafe default. The credential-safety rule must hold regardless of
// the injected client, so this wraps any client to force the no-redirect
// policy while preserving its timeout and transport. If c is nil it returns
// the canonical no-redirect client.
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

// kimiMaxResponseSize bounds the response body so an oversized or streaming
// response cannot be read indefinitely.
const kimiMaxResponseSize = 1 << 20

// Kimi Connect error code values that indicate expired/invalid auth, so the
// querier can classify them distinctly from a generic transport failure.
const kimiConnectUnauthenticated = "unauthenticated"

// ErrKimiAuthExpired is returned when the protected endpoint rejects the
// access token (HTTP 401 or a Connect "unauthenticated" code). The token
// must be refreshed by a re-login; it is NOT included in the error.
var ErrKimiAuthExpired = errors.New("Kimi 鉴权已过期，请重新登录")

// ErrKimiTransport is returned for a network/transport failure (connection
// reset, timeout, DNS, etc.) that is not an auth-expiry signal.
var ErrKimiTransport = errors.New("Kimi 网络请求失败")

// ErrKimiUnsupportedResponse is returned when the response cannot be parsed
// as a valid two-meter quota result — a Connect business error inside a 2xx,
// a missing meter, an invalid percentage/reset, or an unparseable body.
var ErrKimiUnsupportedResponse = errors.New("Kimi 响应不受支持")

// ErrKimiTimeout is returned when the protected request exceeds its deadline.
var ErrKimiTimeout = errors.New("Kimi 请求超时")

// KimiQuerier retrieves a Kimi Code account's two quota meters using the
// saved Bearer accessToken against the OBSERVED Connect gRPC-Web endpoint.
// It mirrors DeepSeekWebQuerier's Bearer-header transport but uses Connect-
// JSON success semantics (HTTP 200 + no "code" string) instead of DeepSeek's
// {code,msg,data} code==0 envelope.
type KimiQuerier struct {
	AccessToken string
	// RefreshToken is the durable Kimi session token (from localStorage). When
	// set, FetchQuotaWithRefresh can auto-refresh an expired access token.
	RefreshToken string
	// BaseURL overrides the OBSERVED host; the default is kimiBaseURL. Used
	// only by tests that want to point at a fake server — production always
	// hits www.kimi.com over HTTPS.
	BaseURL string
	// Headers carries the saved browser headers required for replay
	// (cookie + x-msh-device-id + x-traffic-id + x-msh-platform + x-msh-version
	// + x-language + r-timezone + user-agent), keyed by their HTTP header
	// names. Empty/absent entries are skipped. Verified: a Go client sending
	// the Bearer token + these headers reaches GetSubscriptionStats and gets
	// a 200. An empty map replays token-only (still works for fresh sessions).
	Headers map[string]string
	// Client is injectable for tests; nil constructs a default client with
	// kimiRequestTimeout.
	Client *http.Client
}

// FetchQuota retrieves and parses both meters for one Kimi account. It:
//   - requires a non-empty access token;
//   - validates the base URL is HTTPS on the Kimi host;
//   - POSTs {} to the protected endpoint with Authorization: Bearer,
//     Content-Type: application/json, Connect-Protocol-Version: 1;
//   - classifies 401 / Connect "unauthenticated" as ErrKimiAuthExpired;
//   - classifies network/timeout failures as ErrKimiTransport / ErrKimiTimeout;
//   - bounds the response body to kimiMaxResponseSize;
//   - requires HTTP 200 + a body with no Connect "code" string that parses
//     into the two meters (ErrKimiUnsupportedResponse otherwise).
//
// The access token never appears in any returned error.
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
		// Strict host allowlist: the Bearer token must only ever be sent to
		// the canonical Kimi data host, never to an unapproved host (a
		// redirect/override to evil.example.com must not carry credentials).
		if !kimiAllowedHost(u.Hostname()) {
			return nil, fmt.Errorf("Kimi base URL 主机 %q 不在允许列表", u.Hostname())
		}
		// Replace only the scheme+host of the OBSERVED endpoint path; the
		// protected path is fixed and never derived from BaseURL.
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
	// Replay the saved browser headers (cookie + x-msh-* + user-agent + ...).
	// Each value was allowlisted + CR/LF-rejected at capture; set them
	// best-effort — a token-only replay still works for fresh sessions.
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
		// A Connect "unauthenticated" code inside a 2xx is also auth-expiry.
		if isKimiAuthErrorCode(string(body)) {
			return nil, fmt.Errorf("%w: %v", ErrKimiAuthExpired, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrKimiUnsupportedResponse, err)
	}
	return data, nil
}

// isKimiAuthErrorCode reports whether a body carries the OBSERVED Connect
// "unauthenticated" code, so a 2xx auth failure is classified as auth-expiry
// rather than unsupported-response.
func isKimiAuthErrorCode(body string) bool {
	var cerr kimiConnectError
	if err := json.Unmarshal([]byte(body), &cerr); err != nil {
		return false
	}
	return cerr.Code == kimiConnectUnauthenticated
}

// ErrKimiRefreshFailed is returned when the durable refresh request rejects
// the saved refresh token (the session is no longer valid → re-login required).
var ErrKimiRefreshFailed = errors.New("Kimi 会话刷新失败，请重新登录")

// RefreshResult is the outcome of a successful refresh: the rotated access +
// refresh tokens the caller must atomically persist for this account.
type RefreshResult struct {
	AccessToken  string
	RefreshToken string
}

// Refresh calls the OBSERVED RefreshToken endpoint with the saved refresh_token
// and returns the rotated access + refresh tokens. The refresh_token is in the
// request body (NO Bearer header). Both tokens rotate. Returns
// ErrKimiRefreshFailed if the refresh is rejected (expired/revoked refresh
// token); the caller preserves the last envelope and reports re-login-required.
// The refresh_token never appears in the returned error.
func (q *KimiQuerier) Refresh(ctx context.Context) (RefreshResult, error) {
	if strings.TrimSpace(q.RefreshToken) == "" {
		return RefreshResult{}, fmt.Errorf("%w: 无 refresh_token", ErrKimiRefreshFailed)
	}
	body := fmt.Sprintf(`{"refresh_token":%s}`, mustJSONString(q.RefreshToken))
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

// mustJSONString JSON-encodes s as a JSON string literal (for embedding in a
// request body safely, no injection).
func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// FetchQuotaWithRefresh fetches the three metrics, auto-refreshing an expired
// access token once. On a 401 / Connect "unauthenticated" (ErrKimiAuthExpired),
// it calls Refresh; on success it retries the quota call once with the rotated
// access token and returns the rotated tokens via RefreshResult (non-nil) so
// the caller can atomically persist them. On refresh failure it returns
// ErrKimiRefreshFailed (caller preserves the old envelope, reports re-login).
// Refresh is account-scoped: the caller's credential-update boundary serializes
// rotation (the per-account refresh mutex lives in the caller, not here).
func (q *KimiQuerier) FetchQuotaWithRefresh(ctx context.Context) (*KimiQuotaData, *RefreshResult, error) {
	data, err := q.FetchQuota(ctx)
	if err == nil {
		return data, nil, nil
	}
	if !errors.Is(err, ErrKimiAuthExpired) {
		return nil, nil, err
	}
	// Access token expired — refresh once.
	rr, rerr := q.Refresh(ctx)
	if rerr != nil {
		return nil, nil, rerr
	}
	// Retry with the rotated access token.
	q.AccessToken = rr.AccessToken
	q.RefreshToken = rr.RefreshToken
	data, err = q.FetchQuota(ctx)
	if err != nil {
		return nil, nil, err
	}
	return data, &rr, nil
}
