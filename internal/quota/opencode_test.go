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

// TestParseQuotaResponseAcceptsAnyNonEmptyStatus (change
// opencode-exhausted-status, tasks 2.1) proves status VALUES are no longer
// allowlisted: a quota-exhausted state or an unrecognized future value
// parses through with full fields instead of failing the whole account.
// The monthly unlimited omission (exact string compare) is unaffected.
func TestParseQuotaResponseAcceptsAnyNonEmptyStatus(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  string
		wantWeekly  *string // nil = weekly status not asserted (rolling asserts wantStatus)
		wantMonthly *string // nil = monthly absent/unlimited
	}{
		{name: "exhausted rolling status parses", body: `{rollingUsage:{status:"exhausted",resetInSec:300,usagePercent:100},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantStatus: "exhausted"},
		{name: "future status weekly parses", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"new-state-2026",resetInSec:604800,usagePercent:80}}`, wantStatus: "ok", wantWeekly: strPtr("new-state-2026")},
		{name: "exhausted monthly present renders", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"exhausted",resetInSec:2592000,usagePercent:100}}`, wantStatus: "ok", wantMonthly: strPtr("exhausted")},
		{name: "arbitrary garbage status parses", body: `{rollingUsage:{status:"garbage",resetInSec:300,usagePercent:42},weeklyUsage:{status:"bogus",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"weird",resetInSec:2592000,usagePercent:55}}`, wantStatus: "garbage", wantMonthly: strPtr("weird")},
		{name: "monthly unlimited still omitted", body: monthlyUnlimitedBody, wantStatus: "ok", wantMonthly: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuotaResponse(tt.body)
			if err != nil {
				t.Fatalf("expected parse success for %q, got error: %v", tt.body, err)
			}
			if got.Rolling.Status != tt.wantStatus {
				t.Fatalf("rolling status = %q, want %q", got.Rolling.Status, tt.wantStatus)
			}
			if tt.wantWeekly != nil && got.Weekly.Status != *tt.wantWeekly {
				t.Fatalf("weekly status = %q, want %q", got.Weekly.Status, *tt.wantWeekly)
			}
			if tt.wantMonthly == nil {
				if got.Monthly != nil {
					t.Fatalf("monthly should be nil, got %+v", *got.Monthly)
				}
			} else {
				if got.Monthly == nil {
					t.Fatalf("monthly should be present with status %q", *tt.wantMonthly)
				}
				if got.Monthly.Status != *tt.wantMonthly {
					t.Fatalf("monthly status = %q, want %q", got.Monthly.Status, *tt.wantMonthly)
				}
			}
		})
	}
}

// TestParseQuotaResponseRejectsNonStringOrEmptyStatus (change
// opencode-exhausted-status, tasks 2.2) proves the STRUCTURAL constraint
// survives the relaxation: a non-string status (unquoted) or an empty
// string status is still rejected — the non-empty-string rule cannot be
// bypassed.
func TestParseQuotaResponseRejectsNonStringOrEmptyStatus(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unquoted status value", body: `{rollingUsage:{status:ok,resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "empty string status rolling", body: `{rollingUsage:{status:"",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "empty string status weekly", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"",resetInSec:604800,usagePercent:80}}`},
		{name: "empty string status monthly", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"",resetInSec:2592000,usagePercent:55}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for non-string/empty status in %q, got %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

// TestParseQuotaResponseAcceptsFractionalUsagePercent (change
// opencode go usagePercent protocol drift, 2026-08-25) proves the parser
// tolerates the upstream switch of usagePercent from integer to decimal
// values: the OBSERVED live payload on dogcatttt@ethanxie.top carries
// usagePercent:19.3 / 18.5 / 71.3 with usage/limit byte totals. Decimals
// survive parsing unrounded (Uses float64 so 19.3 stays 19.3 — same
// decision KimiQuotaUsage documents for 2.19/10.42); integers keep
// working. Any other malformed usagePercent shape still fails closed.
func TestParseQuotaResponseAcceptsFractionalUsagePercent(t *testing.T) {
	// decimalBody mirrors the real seroval payloads captured from the
	// opencode.ai server-fn:3 endpoint on 2026-08-25 — fractional
	// usagePercent plus usage/limit byte totals that must be ignored.
	decimalBody := `{rollingUsage:{status:"ok",resetInSec:8296,usagePercent:19.3,usage:232111848,limit:1200000000},weeklyUsage:{status:"ok",resetInSec:483826,usagePercent:18.5,usage:553880414,limit:3000000000},monthlyUsage:{status:"ok",resetInSec:1478803,usagePercent:71.3,usage:4279820210,limit:6000000000}}`

	got, err := parseQuotaResponse(decimalBody)
	if err != nil {
		t.Fatalf("fractional usagePercent must parse, got: %v", err)
	}
	if got.Rolling.UsagePercent != 19.3 {
		t.Fatalf("rolling usagePercent = %v, want 19.3 (exact decimal preserved)", got.Rolling.UsagePercent)
	}
	if got.Weekly.UsagePercent != 18.5 {
		t.Fatalf("weekly usagePercent = %v, want 18.5 (exact decimal preserved)", got.Weekly.UsagePercent)
	}
	if got.Monthly == nil {
		t.Fatal("monthly must be present")
	}
	if got.Monthly.UsagePercent != 71.3 {
		t.Fatalf("monthly usagePercent = %v, want 71.3 (exact decimal preserved)", got.Monthly.UsagePercent)
	}
	if got.Rolling.ResetInSec != 8296 {
		t.Fatalf("rolling resetInSec = %d, want 8296", got.Rolling.ResetInSec)
	}
}

// TestParseQuotaResponseRejectsMalformedUsagePercent (change
// opencode go usagePercent protocol drift) keeps the fail-closed boundary:
// once usagePercent becomes a numeric string/float, only non-negative
// numeric forms are accepted — negative, null, NaN-shaped or missing
// values must still error the account instead of fabricating a percent.
func TestParseQuotaResponseRejectsMalformedUsagePercent(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "negative decimal usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:-1.5},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "non-numeric decimal usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:high},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "null usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:null},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
		{name: "quoted string usagePercent", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:"19.3"},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for malformed usagePercent in %q, got %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

// strPtr returns a pointer to s (test helper).
func strPtr(s string) *string { return &s }

// TestParseQuotaResponseExhaustedRollingParses (RED, quota-exhausted
// regression) proves an exhausted rolling window whose resetInSec is absent
// is a LEGAL state, not a "failed to parse rollingUsage" error. When a
// rolling quota is exhausted / has no reset point, the response may omit
// resetInSec; the parser must degrade gracefully (reset display "0m")
// instead of failing the whole account. [Write first, currently FAILING:
// extractUsageWindow(required=true) errors on the missing field.]
func TestParseQuotaResponseExhaustedRollingParses(t *testing.T) {
	// rollingUsage: exhausted status, usagePercent present, resetInSec ABSENT.
	// (RED→GREEN, opencode-exhausted-status tail) — a window that is
	// exhausted has NO reset point, so resetInSec being absent is legal:
	// render 100% / 0m instead of failing the whole account. Contrast with
	// status:"ok" + missing resetInSec, which stays fail-closed (malformed).
	body := `{rollingUsage:{status:"exhausted",usagePercent:100},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`
	got, err := parseQuotaResponse(body)
	if err != nil {
		t.Fatalf("exhausted rolling must parse, got error: %v", err)
	}
	if got.Rolling.UsagePercent != 100 {
		t.Fatalf("exhausted rolling percent = %v, want 100", got.Rolling.UsagePercent)
	}
	if got.Rolling.ResetInSec != 0 {
		t.Fatalf("exhausted rolling resetInSec = %d, want 0", got.Rolling.ResetInSec)
	}
}

// TestParseQuotaResponseLapsedSubscriptionMarksUnavailable (RED→GREEN,
// quota-exhausted regression, OBSERVED 2026-08-19) covers the REAL shape a
// lapsed/inactive subscription returns: the fixed quota RPC
// (openCodeGoServiceID / server-fn:3) returns `null` — no rollingUsage /
// weeklyUsage / monthlyUsage objects at all. This must NOT fail the whole
// account ("failed to parse rollingUsage"); per decision it renders as an
// explicit "订阅已失效" state on the card: a QuotaData whose windows carry
// Status "unavailable" and a Lapsed flag, so the frontend can surface the
// failure distinctly from a normal exhausted quota.
func TestParseQuotaResponseLapsedSubscriptionMarksUnavailable(t *testing.T) {
	// REAL sanitized response (lapsed subscription, server-fn:3):
	// ;0x0000002e;((self.$R=self.$R||{})["server-fn:3"]=[],null)
	real := `;0x0000002e;((self.$R=self.$R||{})["server-fn:3"]=[],null)`
	got, err := parseQuotaResponse(real)
	if err != nil {
		t.Fatalf("lapsed subscription must not fail the account, got error: %v", err)
	}
	if got == nil {
		t.Fatal("lapsed subscription must return quota data, got nil")
	}
	if !got.Lapsed {
		t.Fatal("lapsed subscription must mark QuotaData.Lapsed")
	}
	if got.Rolling.Status != "unavailable" {
		t.Fatalf("rolling status = %q, want unavailable", got.Rolling.Status)
	}
	if got.Weekly.Status != "unavailable" {
		t.Fatalf("weekly status = %q, want unavailable", got.Weekly.Status)
	}
	if got.Monthly != nil {
		t.Fatalf("lapsed subscription must omit monthly, got %+v", *got.Monthly)
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
