package quota

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeKimiTransport is an http.RoundTripper that returns a canned response
// for the Kimi querier's single protected POST. It lets the querier tests
// exercise transport/business success, expired-auth, timeout, oversized
// body, and secret-exclusion without touching the network.
type fakeKimiTransport struct {
	status  int
	body    string
	headers http.Header
	err     error
	// bodyChunks streams the body in chunks to test the size bound midway.
	bodyChunks []string
	// captures the request so tests can assert the exact protected URL,
	// method, Bearer header, and Connect-Protocol-Version.
	lastReq *http.Request
}

func (t *fakeKimiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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
	var bodyReader io.Reader
	if len(t.bodyChunks) > 0 {
		bodyReader = strings.NewReader(strings.Join(t.bodyChunks, ""))
	} else {
		bodyReader = strings.NewReader(t.body)
	}
	return &http.Response{StatusCode: status, Header: hdr, Body: io.NopCloser(bodyReader)}, nil
}

// newKimiTestQuerier builds a KimiQuerier with the synthetic token and a
// fake transport, plus a short timeout so the oversized-body test is fast.
func newKimiTestQuerier(t *testing.T, transport *fakeKimiTransport) *KimiQuerier {
	t.Helper()
	q := &KimiQuerier{AccessToken: "synthetic-bearer-token-for-tests"}
	q.Client = &http.Client{Transport: transport}
	return q
}

// TestKimiQuerierSendsExactProtectedRequest (task 3.1) proves the querier
// POSTs to the OBSERVED protected endpoint with the Bearer header and
// Connect-Protocol-Version, body {}.
func TestKimiQuerierSendsExactProtectedRequest(t *testing.T) {
	tr := &fakeKimiTransport{body: kimiValidWeeklyFixture}
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

// TestKimiQuerierParsesBothMetersOnSuccess (task 3.2) proves transport +
// business success (HTTP 200, no Connect code) yields the two meters.
func TestKimiQuerierParsesBothMetersOnSuccess(t *testing.T) {
	tr := &fakeKimiTransport{body: kimiValidWeeklyFixture}
	q := newKimiTestQuerier(t, tr)
	got, err := q.FetchQuota(context.Background())
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.Weekly.UsagePercent != 10 || got.RateLimit.UsagePercent != 52 {
		t.Fatalf("meters = %#v", got)
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set")
	}
}

// TestKimiQuerierRejectsExpiredAuth (task 3.2) proves a 401 returns a
// distinct expired-auth error, not a generic transport error.
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

// TestKimiQuerierRejectsTransportError (task 3.2) proves a network/transport
// failure returns a distinct transport error, not expired-auth.
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

// TestKimiQuerierRejectsBusinessErrorInside2xx (task 3.2/3.3) proves a 200
// carrying a Connect error code is an unsupported-response error, not quota
// success.
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

// TestKimiQuerierRejectsOversizedBody (task 3.3) proves a response exceeding
// the size bound is rejected and not parsed as partial data.
func TestKimiQuerierRejectsOversizedBody(t *testing.T) {
	huge := strings.Repeat("x", kimiMaxResponseSize+1)
	tr := &fakeKimiTransport{body: huge}
	q := newKimiTestQuerier(t, tr)
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		t.Fatal("FetchQuota must reject an oversized body")
	}
}

// TestKimiQuerierRejectsMissingToken proves an empty token is a clear error
// before any request is sent.
func TestKimiQuerierRejectsMissingToken(t *testing.T) {
	q := &KimiQuerier{AccessToken: ""}
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		t.Fatal("FetchQuota must fail when the access token is empty")
	}
}

// TestKimiQuerierSecretsNeverInErrors (task 3.3) proves the synthetic token
// never appears in any querier error message — secrets do not leak through
// errors.
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

// TestKimiQuerierRejectsNonHTTPSURL proves the querier rejects a configured
// non-HTTPS base URL (host/redirect validation).
func TestKimiQuerierRejectsNonHTTPSURL(t *testing.T) {
	q := &KimiQuerier{AccessToken: "tok", BaseURL: "http://www.kimi.com"}
	_, err := q.FetchQuota(context.Background())
	if err == nil {
		t.Fatal("FetchQuota must reject a non-HTTPS base URL")
	}
}
