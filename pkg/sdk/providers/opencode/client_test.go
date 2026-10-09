package opencode

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type opencodeFakeTransport struct {
	status int
	body   string
}

func (t *opencodeFakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status := t.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

type readFailReader struct {
	err error
}

func (r *readFailReader) Read([]byte) (int, error) { return 0, r.err }

// opencodeRecordingTransport 记录最后一个请求，供断言 URL 与请求头。
type opencodeRecordingTransport struct {
	request *http.Request
	status  int
	body    string
}

func (t *opencodeRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.request = req
	status := t.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
	}, nil
}

func newOpenCodeTestQuerier(tr *opencodeFakeTransport) *OpenCodeQuerier {
	q := &OpenCodeQuerier{Cookie: "console_session=synthetic-test-cookie", WorkspaceID: "wrk_synthetic123"}
	q.Client = &http.Client{Transport: tr}
	return q
}

func TestOpenCodeFetchQuotaSendsConsoleV2Request(t *testing.T) {
	tr := &opencodeRecordingTransport{body: consoleStatusBody}
	q := &OpenCodeQuerier{Cookie: "console_session=synthetic-test-cookie", WorkspaceID: "org_01JXYZ", Client: &http.Client{Transport: tr}}
	got, err := q.FetchQuota()
	if err != nil {
		t.Fatalf("console payload must parse through the client: %v", err)
	}
	if got.Rolling.Status != "active" || got.Weekly.Status != "active" {
		t.Fatalf("quota = %+v, want active rolling/weekly", got)
	}
	if tr.request == nil {
		t.Fatal("no request was issued")
	}
	if tr.request.Method != http.MethodGet {
		t.Fatalf("method = %s, want GET", tr.request.Method)
	}
	if got := tr.request.URL.String(); got != "https://opencode.ai/console/api/go/status" {
		t.Fatalf("url = %q, want the Console v2 status endpoint", got)
	}
	if got := tr.request.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got)
	}
	if got := tr.request.Header.Get("Cookie"); got != "console_session=synthetic-test-cookie" {
		t.Fatalf("Cookie = %q", got)
	}
	if got := tr.request.Header.Get("x-org-id"); got != "org_01JXYZ" {
		t.Fatalf("x-org-id = %q, want org_01JXYZ", got)
	}
}

func TestOpenCodeFetchQuotaPropagatesReadError(t *testing.T) {
	prefix := monthlyAbsentBody
	tr := &opencodeFakeTransport{}
	body := io.MultiReader(strings.NewReader(prefix), &readFailReader{err: errors.New("connection reset")})
	tr.status = http.StatusOK
	q := newOpenCodeTestQuerier(tr)
	q.Client = &http.Client{Transport: &roundTripBody{body: body}}
	_, err := q.FetchQuota()
	if err == nil {
		t.Fatal("FetchQuota must propagate a mid-body read error, not parse a partial body")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Fatalf("read error must be reported as a read failure, got %q", err)
	}
}

type roundTripBody struct {
	body io.Reader
}

func (t *roundTripBody) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(t.body)}, nil
}

func TestOpenCodeFetchQuotaRejectsOversizedResponse(t *testing.T) {
	big := canonicalBody + strings.Repeat("x", openCodeGoMaxResponseSize)
	tr := &roundTripBody{body: strings.NewReader(big)}
	q := &OpenCodeQuerier{Cookie: "console_session=synthetic-test-cookie", WorkspaceID: "wrk_synthetic123"}
	q.Client = &http.Client{Transport: tr}
	got, err := q.FetchQuota()
	if err == nil {
		t.Fatalf("oversized response must be rejected, got %+v", got)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized rejection must name the byte bound, got %q", err)
	}
	if got != nil {
		t.Fatalf("must return nil quota on oversized response")
	}
}

func TestOpenCodeFetchQuotaParsesValidBody(t *testing.T) {
	tr := &opencodeFakeTransport{body: canonicalBody}
	q := newOpenCodeTestQuerier(tr)
	got, err := q.FetchQuota()
	if err != nil {
		t.Fatalf("valid body must parse: %v", err)
	}
	assertUsage(t, "rolling", got.Rolling, wantRolling)
	assertUsage(t, "weekly", got.Weekly, wantWeekly)
	if got.Monthly == nil {
		t.Fatalf("monthly should be present in canonical body")
	}
	assertUsage(t, "monthly", *got.Monthly, wantMonthly)
}

func TestOpenCodeFetchQuotaNon200DoesNotLeakBody(t *testing.T) {
	marker := "PRIVATE-MARKER-ACCOUNT-SECRET-9f3a"
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "401", status: http.StatusUnauthorized},
		{name: "500", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := &opencodeFakeTransport{status: tc.status, body: `{"error":"` + marker + `","code":"internal"}`}
			q := newOpenCodeTestQuerier(tr)
			got, err := q.FetchQuota()
			if err == nil {
				t.Fatalf("HTTP %d must fail, got %+v", tc.status, got)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%d", tc.status)) {
				t.Fatalf("error must include the status code %d, got %q", tc.status, err)
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatalf("non-200 error must not leak the response body, got %q", err)
			}
			if got != nil {
				t.Fatalf("must return nil quota on HTTP %d", tc.status)
			}
		})
	}
}

func TestOpenCodeFetchQuotaRejectsMalformedWorkspaceID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "bare identifier", id: "01JXYZ"},
		{name: "pasted console url", id: "https://opencode.ai/console/org_01JXYZ/go"},
		{name: "prefix without body", id: "org_"},
		{name: "hyphenated body", id: "org_01-xyz"},
		{name: "leading whitespace", id: " org_01JXYZ"},
		{name: "unexpected prefix", id: "acc_01JXYZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &opencodeRecordingTransport{body: consoleStatusBody}
			q := &OpenCodeQuerier{Cookie: "console_session=synthetic-test-cookie", WorkspaceID: tt.id, Client: &http.Client{Transport: tr}}
			got, err := q.FetchQuota()
			if err == nil {
				t.Fatalf("workspace id %q must be rejected, got %+v", tt.id, got)
			}
			if got != nil {
				t.Fatalf("must return nil quota on invalid workspace id %q", tt.id)
			}
			if tr.request != nil {
				t.Fatalf("invalid workspace id %q must not reach upstream", tt.id)
			}
		})
	}
}

func TestValidWorkspaceIDMatchesConsoleIdentifiers(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{id: "org_01JXYZ", want: true},
		{id: "wrk_abc123", want: true},
		{id: "", want: false},
		{id: "01JXYZ", want: false},
		{id: "org_", want: false},
		{id: "org_01-xyz", want: false},
		{id: "acc_01JXYZ", want: false},
		{id: " org_01JXYZ", want: false},
		{id: "https://opencode.ai/console/org_01JXYZ/go", want: false},
	}
	for _, tt := range tests {
		if got := ValidWorkspaceID(tt.id); got != tt.want {
			t.Fatalf("ValidWorkspaceID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestFetchDefaultOrgIDReturnsFirstOrgID(t *testing.T) {
	tr := &opencodeRecordingTransport{body: `[{"id":"org_01JXYZ","name":"Personal","avatarUrl":null},{"id":"org_SECOND","name":"Team"}]`}
	got, err := FetchDefaultOrgID("__Host-console_session=synthetic", &http.Client{Transport: tr})
	if err != nil {
		t.Fatalf("orgs payload must parse: %v", err)
	}
	if got != "org_01JXYZ" {
		t.Fatalf("org id = %q, want org_01JXYZ", got)
	}
	if tr.request == nil {
		t.Fatal("no request was issued")
	}
	if got := tr.request.URL.String(); got != "https://opencode.ai/console/api/orgs" {
		t.Fatalf("url = %q, want the console orgs endpoint", got)
	}
	if got := tr.request.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got)
	}
	if got := tr.request.Header.Get("Cookie"); got != "__Host-console_session=synthetic" {
		t.Fatalf("Cookie = %q", got)
	}
	if got := tr.request.Header.Get("x-org-id"); got != "" {
		t.Fatalf("orgs request must not carry x-org-id, got %q", got)
	}
}

func TestFetchDefaultOrgIDSkipsEmptyIDs(t *testing.T) {
	tr := &opencodeRecordingTransport{body: `[{"id":"","name":"placeholder"},{"id":"wrk_ABC123","name":"Legacy"}]`}
	got, err := FetchDefaultOrgID("console_session=synthetic", &http.Client{Transport: tr})
	if err != nil {
		t.Fatalf("empty id must be skipped, not accepted: %v", err)
	}
	if got != "wrk_ABC123" {
		t.Fatalf("org id = %q, want wrk_ABC123", got)
	}
}

func TestFetchDefaultOrgIDRejectsUnusableResponses(t *testing.T) {
	marker := "PRIVATE-MARKER-ACCOUNT-SECRET-9f3a"
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "empty array", body: `[]`},
		{name: "ids absent", body: `[{"name":"Personal"}]`},
		{name: "malformed id", body: `[{"id":"bad-id"}]`},
		{name: "whitespace id", body: `[{"id":" "}]`},
		{name: "header injection attempt", body: `[{"id":"org_ok\r\nx-evil: 1"}]`},
		{name: "object instead of array", body: `{"id":"org_01JXYZ"}`},
		{name: "truncated json", body: `[{"id":`},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"` + marker + `"}`},
		{name: "oversized", body: `[` + strings.Repeat("x", openCodeGoMaxResponseSize)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &opencodeRecordingTransport{status: tt.status, body: tt.body}
			got, err := FetchDefaultOrgID("console_session=synthetic", &http.Client{Transport: tr})
			if err == nil {
				t.Fatalf("expected error, got org id %q", got)
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatalf("error must not leak the response body, got %q", err)
			}
			if got != "" {
				t.Fatalf("must return empty org id on failure, got %q", got)
			}
		})
	}
}

func TestFetchDefaultOrgIDRequiresCookieWithoutRequest(t *testing.T) {
	tr := &opencodeRecordingTransport{body: `[{"id":"org_01JXYZ"}]`}
	if _, err := FetchDefaultOrgID("", &http.Client{Transport: tr}); err == nil {
		t.Fatal("empty cookie must fail")
	}
	if tr.request != nil {
		t.Fatal("empty cookie must not issue a request")
	}
}
