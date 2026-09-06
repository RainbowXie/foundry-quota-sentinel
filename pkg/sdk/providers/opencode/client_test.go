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
	status  int
	body    string
	bodyErr error
}

func (t *opencodeFakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.bodyErr != nil {
		return nil, t.bodyErr
	}
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

func newOpenCodeTestQuerier(tr *opencodeFakeTransport) *OpenCodeQuerier {
	q := &OpenCodeQuerier{Cookie: "synthetic-test-cookie", WorkspaceID: "synthetic-test-workspace"}
	q.Client = &http.Client{Transport: tr}
	return q
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
	q := &OpenCodeQuerier{Cookie: "synthetic-test-cookie", WorkspaceID: "synthetic-test-workspace"}
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
