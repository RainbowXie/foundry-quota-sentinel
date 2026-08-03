package quota

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Synthetic sanitized fixtures for the OpenCode Go quota parser.
//
// These bodies are MINIMAL SYNTHETIC STAND-INS for the seroval-like
// serialization returned by the OpenCode Go RPC endpoint. They retain only
// the quota structure and representative numeric values; they contain NO
// cookies, workspace identifiers, request headers, account names, raw
// private payloads, or other authentication/account material. Per the
// capability spec, parser fixtures must never carry real credentials.
const (
	// canonicalBody is the exact-object shape the legacy regex matched.
	canonicalBody = `{rollingUsage:$R[1]={status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:$R[2]={status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:$R[3]={status:"ok",resetInSec:2592000,usagePercent:55}}`

	// referenceDriftBody moves every $R[n] number so the index no longer
	// matches any fixed expectation (a preceding field can shift them all).
	referenceDriftBody = `{region:$R[1]=["us"],rollingUsage:$R[7]={status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:$R[8]={status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:$R[9]={status:"ok",resetInSec:2592000,usagePercent:55}}`

	// inlineBody drops the $R[n]= assignment entirely; seroval emits the
	// object inline when no later reference reuses it.
	inlineBody = `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`

	// reorderedFieldsBody changes the property order inside every object.
	reorderedFieldsBody = `{rollingUsage:{usagePercent:42,status:"ok",resetInSec:300},weeklyUsage:{usagePercent:80,resetInSec:604800,status:"ok"},monthlyUsage:{status:"ok",usagePercent:55,resetInSec:2592000}}`

	// whitespaceBody adds insignificant whitespace between tokens.
	whitespaceBody = `{rollingUsage: $R[1] = { status: "ok", resetInSec: 300, usagePercent: 42 },weeklyUsage:{status:"ok", resetInSec:604800, usagePercent:80},monthlyUsage:{ status:"ok",resetInSec:2592000,usagePercent:55 }}`

	// additionalPropsBody adds unrelated primitive properties before,
	// between, and after the required ones inside each usage object.
	additionalPropsBody = `{rollingUsage:{foo:1,status:"ok",bar:"x",resetInSec:300,baz:true,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80,extra:0},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`

	// monthlyAbsentBody omits monthly entirely (rolling/weekly only).
	monthlyAbsentBody = `{rollingUsage:$R[1]={status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:$R[2]={status:"ok",resetInSec:604800,usagePercent:80}}`

	// monthlyUnlimitedBody uses the supported "unlimited" status for
	// monthly; the parser must preserve the existing unlimited-omission
	// behavior (monthly stays nil).
	monthlyUnlimitedBody = `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"unlimited",resetInSec:0,usagePercent:0}}`
)

// wantRolling / wantWeekly / wantMonthly are the expected parsed values for
// the accepted-shape fixtures above.
var (
	wantRolling = QuotaUsage{Status: "ok", UsagePercent: 42, ResetInSec: 300, ResetDisplay: "5m"}
	wantWeekly  = QuotaUsage{Status: "ok", UsagePercent: 80, ResetInSec: 604800, ResetDisplay: "7d"}
	wantMonthly = QuotaUsage{Status: "ok", UsagePercent: 55, ResetInSec: 2592000, ResetDisplay: "30d"}
)

// TestParseQuotaResponseREDFixtureNowParses (task 1.2, turned GREEN in 2.x)
// documents the structural shape behind the intermittent
// "failed to parse rollingUsage": the legacy parser matched ONE exact
// whole-object string, so a reordered object failed. The fix (bounded
// structural extraction) must accept this shape. The fixture is fully
// synthetic/sanitized.
func TestParseQuotaResponseREDFixtureNowParses(t *testing.T) {
	got, err := parseQuotaResponse(reorderedFieldsBody)
	if err != nil {
		t.Fatalf("reordered-fields fixture must parse after fix, got: %v", err)
	}
	assertUsage(t, "rolling", got.Rolling, wantRolling)
	assertUsage(t, "weekly", got.Weekly, wantWeekly)
	if got.Monthly == nil {
		t.Fatalf("monthly should be present in reordered fixture")
	}
	assertUsage(t, "monthly", *got.Monthly, wantMonthly)
}

// TestParseQuotaResponseAcceptedShapes (task 1.3) is table-driven and covers
// reference drift, optional $R[n]=, reordered fields, whitespace, additional
// properties, monthly absent, monthly unlimited, and canonical behavior.
func TestParseQuotaResponseAcceptedShapes(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRolling QuotaUsage
		wantWeekly  QuotaUsage
		wantMonthly *QuotaUsage
	}{
		{name: "canonical", body: canonicalBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: &wantMonthly},
		{name: "reference drift", body: referenceDriftBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: &wantMonthly},
		{name: "inline without reference", body: inlineBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: &wantMonthly},
		{name: "reordered fields", body: reorderedFieldsBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: &wantMonthly},
		{name: "whitespace", body: whitespaceBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: &wantMonthly},
		{name: "additional properties", body: additionalPropsBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: &wantMonthly},
		{name: "monthly absent", body: monthlyAbsentBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "monthly unlimited", body: monthlyUnlimitedBody, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		// Review follow-up: string values containing window-name or
		// quota-shaped text must be IGNORED (spec: unrelated primitive
		// properties are ignored). They must neither create a window,
		// duplicate an existing one, nor trigger a malformed error.
		{name: "string value with window-name text is ignored", body: `{rollingUsage:{status:"ok",note:" monthlyUsage: unavailable",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string value with quota-shaped text is ignored", body: `{rollingUsage:{status:"ok",note:"monthlyUsage:{status:\"ok\",resetInSec:2592000,usagePercent:55}",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string with quota-shaped text does not duplicate rolling", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},note:"rollingUsage:{status:\"ok\",resetInSec:300,usagePercent:42}",weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string with malformed ref-shaped text is ignored", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},note:"monthlyUsage:$R[abc]={status:\"ok\"",weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string with escaped quote then window text is ignored", body: `{rollingUsage:{status:"ok",note:"say \"hi\" then monthlyUsage: x",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "prefixed name (xmonthlyUsage) is not an occurrence", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},xmonthlyUsage:{status:"ok",resetInSec:1,usagePercent:55}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuotaResponse(tt.body)
			if err != nil {
				t.Fatalf("parseQuotaResponse(%q) returned error: %v", tt.body, err)
			}
			assertUsage(t, "rolling", got.Rolling, tt.wantRolling)
			assertUsage(t, "weekly", got.Weekly, tt.wantWeekly)
			if tt.wantMonthly == nil {
				if got.Monthly != nil {
					t.Fatalf("monthly should be nil, got %+v", *got.Monthly)
				}
			} else {
				if got.Monthly == nil {
					t.Fatalf("monthly should be present, got nil")
				}
				assertUsage(t, "monthly", *got.Monthly, *tt.wantMonthly)
			}
		})
	}
}

func assertUsage(t *testing.T, window string, got, want QuotaUsage) {
	t.Helper()
	if got != want {
		t.Fatalf("%s mismatch:\n got %+v\nwant %+v", window, got, want)
	}
}

// TestParseQuotaResponseRejectsMalformed (task 1.4 + review follow-up) is
// table-driven and proves the parser fails closed: missing/truncated/
// duplicate/malformed/negative values, quota-shaped text under unrelated
// field names, and PRESENT-BUT-MALFORMED occurrences (truncated object,
// malformed reference assignment, non-object value, malformed+valid
// duplicate) must produce a window-specific error and NEVER fabricate
// zeros or substitute another window. A present-but-broken optional
// monthly window must not be mistaken for absent.
func TestParseQuotaResponseRejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "rolling missing", body: `{weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`},
		{name: "weekly missing", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`},
		{name: "rolling truncated inside object", body: `{rollingUsage:{status:"ok",resetInSec:300`},
		{name: "rolling truncated no close brace", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42}`},
		{name: "monthly truncated (present-but-malformed must error)", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:1`},
		{name: "monthly malformed reference assignment", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:$R[abc]={status:"ok",resetInSec:1,usagePercent:55}}`},
		{name: "monthly non-object value", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:"unlimited"}`},
		{name: "monthly valid then malformed duplicate", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55},monthlyUsage:{status:"ok",resetInSec:1`},
		{name: "monthly malformed then valid duplicate", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:1,monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`},
		{name: "duplicate status", body: `{rollingUsage:{status:"ok",status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "duplicate usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42,usagePercent:43},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "negative resetInSec", body: `{rollingUsage:{status:"ok",resetInSec:-300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "negative usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:-42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "non-numeric resetInSec", body: `{rollingUsage:{status:"ok",resetInSec:"soon",usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "non-numeric usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:"high"},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "missing status", body: `{rollingUsage:{resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "missing resetInSec", body: `{rollingUsage:{status:"ok",usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "missing usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "quota-shaped text under unrelated field", body: `{otherUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "rolling only inside a string value (genuinely absent)", body: `{note:"rollingUsage:{status:\"ok\",resetInSec:300,usagePercent:42}",weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "monthly malformed present", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:-1,usagePercent:55}}`},
		{name: "duplicate nested object", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "empty body", body: ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for %q, got quota result %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

// TestParseQuotaResponseRejectsUnsupportedStatus (review follow-up, task
// 2.2) proves status is validated against the confirmed upstream allowlist
// (ok / unlimited): any other value fails closed for every window.
func TestParseQuotaResponseRejectsUnsupportedStatus(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "rolling unsupported status", body: `{rollingUsage:{status:"garbage",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "weekly unsupported status", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"bogus",resetInSec:604800,usagePercent:80}}`},
		{name: "monthly unsupported status", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"weird",resetInSec:2592000,usagePercent:55}}`},
		{name: "unquoted status value", body: `{rollingUsage:{status:ok,resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for unsupported status in %q, got %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

// ---- FetchQuota HTTP→parser boundary tests (review follow-up) ----
//
// The parser itself is exercised directly above; these tests drive the
// FetchQuota READ boundary with an injectable transport so genuine
// transport/read errors and oversized responses are never misread as a
// complete quota result.

// opencodeFakeTransport is an http.RoundTripper returning a canned body or
// error for OpenCode Go FetchQuota tests.
type opencodeFakeTransport struct {
	status  int
	body    string
	bodyErr error // if set, RoundTrip returns this transport error
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

// readFailReader returns the wrapped error once, then EOF — simulates a
// connection that breaks mid-body after some bytes were delivered.
type readFailReader struct {
	err error
}

func (r *readFailReader) Read([]byte) (int, error) { return 0, r.err }

// newOpenCodeTestQuerier builds a querier with synthetic (non-secret)
// credentials and an injectable client so FetchQuota's HTTP layer is
// deterministic.
func newOpenCodeTestQuerier(tr *opencodeFakeTransport) *OpenCodeGoQuerier {
	q := &OpenCodeGoQuerier{Cookie: "synthetic-test-cookie", WorkspaceID: "synthetic-test-workspace"}
	q.Client = &http.Client{Transport: tr}
	return q
}

// TestOpenCodeGoFetchQuotaPropagatesReadError (review follow-up) proves a
// transport error DURING body read — after rolling/weekly bytes are
// delivered but before an optional monthly could arrive — is a hard error,
// never a successful parse with monthly absent.
func TestOpenCodeGoFetchQuotaPropagatesReadError(t *testing.T) {
	// rolling + weekly complete, then the reader dies before monthly.
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

// roundTripBody is an http.RoundTripper that serves a custom io.Reader
// body so tests can inject mid-read failures.
type roundTripBody struct {
	body io.Reader
}

func (t *roundTripBody) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(t.body)}, nil
}

// TestOpenCodeGoFetchQuotaRejectsOversizedResponse (review follow-up) proves
// a response exceeding the 1 MiB bound is rejected even when its prefix is a
// fully valid quota body — the oversized tail must not be silently truncated
// into a partial result.
func TestOpenCodeGoFetchQuotaRejectsOversizedResponse(t *testing.T) {
	big := canonicalBody + strings.Repeat("x", openCodeGoMaxResponseSize)
	tr := &roundTripBody{body: strings.NewReader(big)}
	q := &OpenCodeGoQuerier{Cookie: "synthetic-test-cookie", WorkspaceID: "synthetic-test-workspace"}
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

// TestOpenCodeGoFetchQuotaParsesValidBody proves the FetchQuota boundary
// passes an in-bounds canonical body through to the parser successfully.
func TestOpenCodeGoFetchQuotaParsesValidBody(t *testing.T) {
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

// TestOpenCodeGoFetchQuotaNon200DoesNotLeakBody (review follow-up, task 2.3)
// proves a non-200 error carries ONLY the status code — never the upstream
// response body, which may contain private/account material that would
// otherwise reach CLI output, Web cards, or logs.
func TestOpenCodeGoFetchQuotaNon200DoesNotLeakBody(t *testing.T) {
	// A synthetic private marker that MUST NOT appear in any error.
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
