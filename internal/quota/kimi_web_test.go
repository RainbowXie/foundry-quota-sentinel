package quota

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeKimiTransport is an http.RoundTripper that returns a canned response for
// the Kimi querier's protected POST / refresh POST.
type fakeKimiTransport struct {
	status  int
	body    string
	headers http.Header
	err     error
	// refreshBody is returned for a POST to the refresh endpoint; if nil, no
	// refresh handling.
	refreshBody *string
	// lastReq captures the request for assertions.
	lastReq *http.Request
	// refreshReqs captures refresh requests.
	refreshReqs []*http.Request
}

func (t *fakeKimiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Refresh endpoint detection.
	if strings.Contains(req.URL.String(), "auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken") && t.refreshBody != nil {
		t.refreshReqs = append(t.refreshReqs, req)
		status := t.status
		if status == 0 {
			status = http.StatusOK
		}
		hdr := t.headers
		if hdr == nil {
			hdr = http.Header{"Content-Type": []string{"application/json"}}
		}
		return &http.Response{StatusCode: status, Header: hdr, Body: io.NopCloser(strings.NewReader(*t.refreshBody))}, nil
	}
	t.lastReq = req
	if t.err != nil {
		return nil, t.err
	}
	status := t.status
	if status == 0 {
		status = http.StatusOK
	}
	hdr := t.headers
	if hdr == nil {
		hdr = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{StatusCode: status, Header: hdr, Body: io.NopCloser(strings.NewReader(t.body))}, nil
}

// newKimiTestQuerier builds a KimiQuerier with a synthetic token + headers.
func newKimiTestQuerier(t *testing.T, transport *fakeKimiTransport) *KimiQuerier {
	t.Helper()
	q := &KimiQuerier{AccessToken: "synthetic-bearer-token-for-tests"}
	q.Client = &http.Client{Transport: transport}
	return q
}

// kimiValidStatsBody is a synthetic GetSubscriptionStats 200 body mirroring the
// REAL three-metric structure (ratelimitCode5h/7d + subscriptionBalance, decimal
// ratios, absolute ISO resetTime). resetTime built at test time → future.
func kimiValidStatsBody() string {
	now := time.Now()
	return kimiStatsFixture(now, 0.0219, 0.0199, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
}

// TestKimiQuerierSendsExactProtectedRequest (task 3.1) proves the querier
// POSTs to the OBSERVED protected endpoint with the Bearer header and
// Connect-Protocol-Version, body {}.
func TestKimiQuerierSendsExactProtectedRequest(t *testing.T) {
	tr := &fakeKimiTransport{body: kimiValidStatsBody()}
	q := newKimiTestQuerier(t, tr)
	if _, err := q.FetchQuota(context.Background()); err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if tr.lastReq == nil {
		t.Fatal("no request was sent")
	}
	if tr.lastReq.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", tr.lastReq.Method)
	}
	if tr.lastReq.URL.String() != kimiProtectedQuotaURL {
		t.Errorf("url = %q, want %q", tr.lastReq.URL.String(), kimiProtectedQuotaURL)
	}
	if got := tr.lastReq.Header.Get("Authorization"); got != "Bearer synthetic-bearer-token-for-tests" {
		t.Errorf("Authorization = %q, want Bearer <token>", got)
	}
	if got := tr.lastReq.Header.Get("Connect-Protocol-Version"); got != "1" {
		t.Errorf("Connect-Protocol-Version = %q, want 1", got)
	}
	if got := tr.lastReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// TestKimiQuerierParsesThreeMetricsOnSuccess (task 3.2) proves transport +
// business success (HTTP 200, no Connect code) yields the three decimal metrics.
func TestKimiQuerierParsesThreeMetricsOnSuccess(t *testing.T) {
	tr := &fakeKimiTransport{body: kimiValidStatsBody()}
	q := newKimiTestQuerier(t, tr)
	got, err := q.FetchQuota(context.Background())
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.Total.TotalPercent != 2.19 || got.FiveHour.UsagePercent != 0 || got.SevenDay.UsagePercent != 10.42 {
		t.Fatalf("meters = %#v", got)
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set")
	}
}

// TestKimiQuerierRejectsExpiredAuth (task 3.2) proves a 401 returns a distinct
// expired-auth error.
func TestKimiQuerierRejectsExpiredAuth(t *testing.T) {
	tr := &fakeKimiTransport{status: http.StatusUnauthorized, body: `{"code":"unauthenticated"}`}
	q := newKimiTestQuerier(t, tr)
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		t.Fatal("FetchQuota must fail on 401")
	}
	if !errors.Is(err, ErrKimiAuthExpired) {
		t.Fatalf("err = %v, want ErrKimiAuthExpired", err)
	}
}

// TestKimiQuerierRejectsTransportError proves a network failure returns a
// transport error, not expired-auth.
func TestKimiQuerierRejectsTransportError(t *testing.T) {
	tr := &fakeKimiTransport{err: errors.New("connection reset")}
	q := newKimiTestQuerier(t, tr)
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		t.Fatal("FetchQuota must fail on transport error")
	}
	if errors.Is(err, ErrKimiAuthExpired) {
		t.Fatal("transport error must not be classified as expired-auth")
	}
	if !errors.Is(err, ErrKimiTransport) {
		t.Fatalf("err = %v, want ErrKimiTransport", err)
	}
}

// TestKimiQuerierRejectsBusinessErrorInside2xx proves a 200 carrying a Connect
// error code is an unsupported-response error.
func TestKimiQuerierRejectsBusinessErrorInside2xx(t *testing.T) {
	tr := &fakeKimiTransport{status: http.StatusOK, body: `{"code":"permission_denied"}`}
	q := newKimiTestQuerier(t, tr)
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		t.Fatal("FetchQuota must reject a 200 carrying a Connect error code")
	}
	if !errors.Is(err, ErrKimiUnsupportedResponse) {
		t.Fatalf("err = %v, want ErrKimiUnsupportedResponse", err)
	}
}

// TestKimiQuerierRejectsOversizedBody proves a response exceeding the size
// bound is rejected.
func TestKimiQuerierRejectsOversizedBody(t *testing.T) {
	huge := strings.Repeat("x", kimiMaxResponseSize+1)
	tr := &fakeKimiTransport{body: huge}
	q := newKimiTestQuerier(t, tr)
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must reject an oversized body")
	}
}

// TestKimiQuerierRejectsMissingToken proves an empty token is a clear error.
func TestKimiQuerierRejectsMissingToken(t *testing.T) {
	q := &KimiQuerier{AccessToken: ""}
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must fail when the access token is empty")
	}
}

// TestKimiQuerierSecretsNeverInErrors (task 3.3) proves the synthetic token
// never appears in any querier error message.
func TestKimiQuerierSecretsNeverInErrors(t *testing.T) {
	cases := []struct {
		name string
		tr   *fakeKimiTransport
	}{
		{"expired-auth", &fakeKimiTransport{status: http.StatusUnauthorized, body: `{"code":"unauthenticated"}`}},
		{"transport-error", &fakeKimiTransport{err: errors.New("connection reset")}},
		{"business-error", &fakeKimiTransport{status: http.StatusOK, body: `{"code":"permission_denied"}`}},
		{"oversized", &fakeKimiTransport{body: strings.Repeat("x", kimiMaxResponseSize+1)}},
		{"unparseable", &fakeKimiTransport{body: "not json"}},
	}
	const secret = "synthetic-bearer-token-for-tests"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := newKimiTestQuerier(t, c.tr)
			_, err := q.FetchQuota(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks the access token: %q", err.Error())
			}
		})
	}
}

// TestKimiQuerierRejectsNonHTTPSURL proves the querier rejects a non-HTTPS base URL.
func TestKimiQuerierRejectsNonHTTPSURL(t *testing.T) {
	q := &KimiQuerier{AccessToken: "tok", BaseURL: "http://www.kimi.com"}
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must reject a non-HTTPS base URL")
	}
}

// TestKimiQuerierRejectsNonKimiHost proves the querier rejects an HTTPS base URL
// on a host outside the Kimi allowlist.
func TestKimiQuerierRejectsNonKimiHost(t *testing.T) {
	q := &KimiQuerier{AccessToken: "tok", BaseURL: "https://evil.example.com"}
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must reject an HTTPS base URL on a non-Kimi host")
	}
}

// TestKimiQuerierEndpointIsExactProtectedPath proves the default endpoint is the
// exact OBSERVED protected path on www.kimi.com.
func TestKimiQuerierEndpointIsExactProtectedPath(t *testing.T) {
	tr := &fakeKimiTransport{body: kimiValidStatsBody()}
	q := newKimiTestQuerier(t, tr)
	if _, err := q.FetchQuota(context.Background()); err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got := tr.lastReq.URL.Path; got != "/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats" {
		t.Fatalf("endpoint path = %q, want the exact protected path", got)
	}
	if got := tr.lastReq.URL.Host; got != "www.kimi.com" {
		t.Fatalf("endpoint host = %q, want www.kimi.com", got)
	}
}
