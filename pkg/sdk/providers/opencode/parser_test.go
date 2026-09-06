package opencode

import (
	"testing"
)

const (
	canonicalBody = `{rollingUsage:$R[1]={status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:$R[2]={status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:$R[3]={status:"ok",resetInSec:2592000,usagePercent:55}}`
	referenceDriftBody = `{region:$R[1]=["us"],rollingUsage:$R[7]={status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:$R[8]={status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:$R[9]={status:"ok",resetInSec:2592000,usagePercent:55}}`
	inlineBody = `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`
	reorderedFieldsBody = `{rollingUsage:{usagePercent:42,status:"ok",resetInSec:300},weeklyUsage:{usagePercent:80,resetInSec:604800,status:"ok"},monthlyUsage:{status:"ok",usagePercent:55,resetInSec:2592000}}`
	whitespaceBody = `{rollingUsage: $R[1] = { status: "ok", resetInSec: 300, usagePercent: 42 },weeklyUsage:{status:"ok", resetInSec:604800, usagePercent:80},monthlyUsage:{ status:"ok",resetInSec:2592000,usagePercent:55 }}`
	additionalPropsBody = `{rollingUsage:{foo:1,status:"ok",bar:"x",resetInSec:300,baz:true,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80,extra:0},monthlyUsage:{status:"ok",resetInSec:2592000,usagePercent:55}}`
	monthlyAbsentBody = `{rollingUsage:$R[1]={status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:$R[2]={status:"ok",resetInSec:604800,usagePercent:80}}`
	monthlyUnlimitedBody = `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"unlimited",resetInSec:0,usagePercent:0}}`
)

var (
	wantRolling = QuotaUsage{Status: "ok", UsagePercent: 42, ResetInSec: 300, ResetDisplay: "5m"}
	wantWeekly  = QuotaUsage{Status: "ok", UsagePercent: 80, ResetInSec: 604800, ResetDisplay: "7d"}
	wantMonthly = QuotaUsage{Status: "ok", UsagePercent: 55, ResetInSec: 2592000, ResetDisplay: "30d"}
)

func TestParseQuotaResponseREDFixtureNowParses(t *testing.T) {
	got, err := ParseQuotaResponse(reorderedFieldsBody)
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
		{name: "string value with window-name text is ignored", body: `{rollingUsage:{status:"ok",note:" monthlyUsage: unavailable",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string value with quota-shaped text is ignored", body: `{rollingUsage:{status:"ok",note:"monthlyUsage:{status:\"ok\",resetInSec:2592000,usagePercent:55}",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string with quota-shaped text does not duplicate rolling", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},note:"rollingUsage:{status:\"ok\",resetInSec:300,usagePercent:42}",weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string with malformed ref-shaped text is ignored", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},note:"monthlyUsage:$R[abc]={status:\"ok\"",weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "string with escaped quote then window text is ignored", body: `{rollingUsage:{status:"ok",note:"say \"hi\" then monthlyUsage: x",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
		{name: "prefixed name (xmonthlyUsage) is not an occurrence", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},xmonthlyUsage:{status:"ok",resetInSec:1,usagePercent:55}}`, wantRolling: wantRolling, wantWeekly: wantWeekly, wantMonthly: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQuotaResponse(tt.body)
			if err != nil {
				t.Fatalf("ParseQuotaResponse(%q) returned error: %v", tt.body, err)
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
			got, err := ParseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for %q, got quota result %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

func TestParseQuotaResponseAcceptsAnyNonEmptyStatus(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  string
		wantWeekly  *string
		wantMonthly *string
	}{
		{name: "exhausted rolling status parses", body: `{rollingUsage:{status:"exhausted",resetInSec:300,usagePercent:100},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`, wantStatus: "exhausted"},
		{name: "future status weekly parses", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"new-state-2026",resetInSec:604800,usagePercent:80}}`, wantStatus: "ok", wantWeekly: strPtr("new-state-2026")},
		{name: "exhausted monthly present renders", body: `{rollingUsage:{status:"ok",resetInSec:300,usagePercent:42},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"exhausted",resetInSec:2592000,usagePercent:100}}`, wantStatus: "ok", wantMonthly: strPtr("exhausted")},
		{name: "arbitrary garbage status parses", body: `{rollingUsage:{status:"garbage",resetInSec:300,usagePercent:42},weeklyUsage:{status:"bogus",resetInSec:604800,usagePercent:80},monthlyUsage:{status:"weird",resetInSec:2592000,usagePercent:55}}`, wantStatus: "garbage", wantMonthly: strPtr("weird")},
		{name: "monthly unlimited still omitted", body: monthlyUnlimitedBody, wantStatus: "ok", wantMonthly: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQuotaResponse(tt.body)
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
			got, err := ParseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for non-string/empty status in %q, got %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

func TestParseQuotaResponseAcceptsFractionalUsagePercent(t *testing.T) {
	decimalBody := `{rollingUsage:{status:"ok",resetInSec:8296,usagePercent:19.3,usage:232111848,limit:1200000000},weeklyUsage:{status:"ok",resetInSec:483826,usagePercent:18.5,usage:553880414,limit:3000000000},monthlyUsage:{status:"ok",resetInSec:1478803,usagePercent:71.3,usage:4279820210,limit:6000000000}}`

	got, err := ParseQuotaResponse(decimalBody)
	if err != nil {
		t.Fatalf("fractional usagePercent must parse, got: %v", err)
	}
	if got.Rolling.UsagePercent != 19.3 {
		t.Fatalf("rolling usagePercent = %v, want 19.3", got.Rolling.UsagePercent)
	}
	if got.Weekly.UsagePercent != 18.5 {
		t.Fatalf("weekly usagePercent = %v, want 18.5", got.Weekly.UsagePercent)
	}
	if got.Monthly == nil {
		t.Fatal("monthly must be present")
	}
	if got.Monthly.UsagePercent != 71.3 {
		t.Fatalf("monthly usagePercent = %v, want 71.3", got.Monthly.UsagePercent)
	}
	if got.Rolling.ResetInSec != 8296 {
		t.Fatalf("rolling resetInSec = %d, want 8296", got.Rolling.ResetInSec)
	}
}

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
			got, err := ParseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for malformed usagePercent in %q, got %+v", tt.body, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %q, got %+v", tt.body, got)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestParseQuotaResponseExhaustedRollingParses(t *testing.T) {
	body := `{rollingUsage:{status:"exhausted",usagePercent:100},weeklyUsage:{status:"ok",resetInSec:604800,usagePercent:80}}`
	got, err := ParseQuotaResponse(body)
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

func TestParseQuotaResponseLapsedSubscriptionMarksUnavailable(t *testing.T) {
	real := `;0x0000002e;((self.$R=self.$R||{})["server-fn:3"]=[],null)`
	got, err := ParseQuotaResponse(real)
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
