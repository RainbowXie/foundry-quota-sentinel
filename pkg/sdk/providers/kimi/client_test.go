package kimi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeKimiTransport struct {
	status      int
	body        string
	headers     http.Header
	err         error
	refreshBody *string
	lastReq     *http.Request
	refreshReqs []*http.Request
}

func (t *fakeKimiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

func newKimiTestQuerier(t *testing.T, transport *fakeKimiTransport) *KimiQuerier {
	t.Helper()
	q := &KimiQuerier{AccessToken: "synthetic-bearer-token-for-tests"}
	q.Client = &http.Client{Transport: transport}
	return q
}

func kimiValidStatsBody() string {
	now := time.Now()
	return kimiStatsFixture(now, 0.0219, 0.0199, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
}

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

func TestKimiQuerierRejectsOversizedBody(t *testing.T) {
	huge := strings.Repeat("x", kimiMaxResponseSize+1)
	tr := &fakeKimiTransport{body: huge}
	q := newKimiTestQuerier(t, tr)
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must reject an oversized body")
	}
}

func TestKimiQuerierRejectsMissingToken(t *testing.T) {
	q := &KimiQuerier{AccessToken: ""}
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must fail when the access token is empty")
	}
}

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

func TestKimiQuerierRejectsNonHTTPSURL(t *testing.T) {
	q := &KimiQuerier{AccessToken: "tok", BaseURL: "http://www.kimi.com"}
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must reject a non-HTTPS base URL")
	}
}

func TestKimiQuerierRejectsNonKimiHost(t *testing.T) {
	q := &KimiQuerier{AccessToken: "tok", BaseURL: "https://evil.example.com"}
	if _, err := q.FetchQuota(context.Background()); err == nil {
		t.Fatal("FetchQuota must reject an HTTPS base URL on a non-Kimi host")
	}
}

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

type redirectTransport struct {
	target     string
	followedTo string
	hadBearer  bool
	statsBody  string
}

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "www.kimi.com" || req.URL.Host == "auth.kimi.com" {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{r.target + "/apiv2/x"}, "Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("moved")),
		}, nil
	}
	r.followedTo = req.URL.Host
	r.hadBearer = req.Header.Get("Authorization") != ""
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.statsBody)),
	}, nil
}

func TestKimiQuerierDoesNotFollowRedirectToEvilHost(t *testing.T) {
	rt := &redirectTransport{
		target:    "https://evil.example.com",
		statsBody: kimiValidStatsBody(),
	}
	q := &KimiQuerier{
		AccessToken: "synthetic-bearer-token-for-tests",
		Client:      &http.Client{Transport: rt},
	}
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		if rt.followedTo != "" {
			t.Fatalf("Bearer token was carried to redirect target %q", rt.followedTo)
		}
	}
	if rt.followedTo != "" && rt.hadBearer {
		t.Fatalf("FATAL: Bearer Authorization leaked to redirect host %q", rt.followedTo)
	}
}

func TestKimiRefreshDoesNotFollowRedirectToEvilHost(t *testing.T) {
	rt := &redirectTransport{
		target:    "https://evil.example.com",
		statsBody: `{"accessToken":"x","refreshToken":"y"}`,
	}
	q := &KimiQuerier{
		AccessToken:  "tok",
		RefreshToken: "synthetic-refresh-token",
		Client:       &http.Client{Transport: rt},
	}
	_, err := q.Refresh(context.Background())
	if err == nil {
		if rt.followedTo != "" {
			t.Fatalf("refresh_token was carried to redirect target %q", rt.followedTo)
		}
	}
	if rt.followedTo != "" {
		t.Fatalf("FATAL: request reached redirect host %q", rt.followedTo)
	}
}
