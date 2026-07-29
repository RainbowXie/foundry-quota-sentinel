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

// kimiProtectedQuotaURL is the OBSERVED protected quota endpoint: a Buf
// Connect POST to the membership service's GetSubscriptionStats method. The
// SPA's useBalanceModel calls membershipService.getSubscriptionStats({}).
// EVIDENCE-GATED: the exact 200-body layout (which Balance fields populate)
// is confirmed by a single real login; the endpoint path and protocol are
// OBSERVED (401 on POST without auth, 405 on GET).
const kimiProtectedQuotaURL = "https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"

// kimiRequestTimeout bounds the protected quota request.
var kimiRequestTimeout = 15 * time.Second

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
		// Replace only the scheme+host of the OBSERVED endpoint path.
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

	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: kimiRequestTimeout}
	}
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

	data, err := ParseKimiQuota(string(body))
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
