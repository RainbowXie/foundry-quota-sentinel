package quota

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// kimiValidWeeklyFixture is the synthetic response representing the proposal
// sample: weekly usage 10% resetting in 6d 12h 20min (=562800s), frequency-
// limit usage 52% resetting in 3h 20min (=12000s). It models the OBSERVED
// Connect-JSON success shape: NO top-level "code" field on success (a
// non-empty "code" string is a Connect failure envelope). Meter field names
// are EVIDENCE-GATED against the real 200 body layout; the parser's CONTRACT
// (two independent meters, Connect success discriminator, percentage 0..100,
// reset in seconds) is what the tests pin. When the real CDP capture fixes
// the field names, only internal/quota/kimi.go struct tags change, not these
// tests' assertions on the parsed KimiQuotaData.
const kimiValidWeeklyFixture = `{"weekly":{"usedPercent":10,"reset_seconds":562800},"rate_limit":{"usedPercent":52,"reset_seconds":12000}}`

// kimiFixtureWithBody builds a Kimi Connect-JSON response with the two
// meters. Used by the RED tests so they share one builder and differ only in
// the value under test.
func kimiFixtureWithBody(weeklyUsed any, weeklyReset any, rateUsed any, rateReset any) string {
	return `{"weekly":` + kimiMeterJSON(weeklyUsed, weeklyReset) + `,"rate_limit":` + kimiMeterJSON(rateUsed, rateReset) + `}`
}

// kimiMeterJSON renders one meter: usedPercent from an int or usedRatio from
// a float; reset from an int (reset_seconds) or a string (reset_display).
func kimiMeterJSON(used any, reset any) string {
	meter := map[string]any{}
	switch u := used.(type) {
	case int:
		meter["usedPercent"] = u
	case float64:
		meter["amountUsedRatio"] = u
	}
	switch r := reset.(type) {
	case int:
		meter["reset_seconds"] = r
	case string:
		meter["reset_display"] = r
	case nil:
		// omit reset
	}
	b, _ := json.Marshal(meter)
	return string(b)
}

// TestParseKimiQuotaAcceptsRepresentativeValues (task 1.4 / 2.5) proves the
// parser reads the two independent meters with correct percentages, reset
// seconds, and compact displays. weekly 10% / 562800s → "6d"; rate_limit
// 52% / 12000s → "3h".
func TestParseKimiQuotaAcceptsRepresentativeValues(t *testing.T) {
	got, err := ParseKimiQuota(kimiValidWeeklyFixture)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	if got.Weekly.UsagePercent != 10 {
		t.Errorf("weekly usage = %d, want 10", got.Weekly.UsagePercent)
	}
	if got.Weekly.ResetInSec != 562800 {
		t.Errorf("weekly reset = %d, want 562800", got.Weekly.ResetInSec)
	}
	if got.Weekly.ResetDisplay != "6d" {
		t.Errorf("weekly reset display = %q, want 6d", got.Weekly.ResetDisplay)
	}
	if got.RateLimit.UsagePercent != 52 {
		t.Errorf("rate limit usage = %d, want 52", got.RateLimit.UsagePercent)
	}
	if got.RateLimit.ResetInSec != 12000 {
		t.Errorf("rate limit reset = %d, want 12000", got.RateLimit.ResetInSec)
	}
	if got.RateLimit.ResetDisplay != "3h" {
		t.Errorf("rate limit reset display = %q, want 3h", got.RateLimit.ResetDisplay)
	}
	// The two reset values are INDEPENDENT — not a shared field.
	if got.Weekly.ResetInSec == got.RateLimit.ResetInSec {
		t.Fatal("weekly and rate-limit reset must be independent values")
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set at parse time")
	}
}

// TestParseKimiQuotaRejectsConnectErrorEnvelope proves a Connect failure
// envelope (OBSERVED shape: a non-empty top-level "code" string such as
// "unauthenticated") is rejected. A 2xx carrying a Connect error code is a
// business/transport failure, NOT quota success. This replaces the DeepSeek
// code==0 assumption: Kimi success has NO "code" field.
func TestParseKimiQuotaRejectsConnectErrorEnvelope(t *testing.T) {
	body := `{"code":"unauthenticated","details":[{"type":"common.error.v1.ErrorDetail"}]}`
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject a Connect error envelope (non-empty code string)")
	}
}

// TestParseKimiQuotaRejectsBusinessErrorInside2xx proves a Connect error with
// a non-empty code (e.g. "permission_denied") is rejected even though the
// JSON is well-formed and the meters are absent.
func TestParseKimiQuotaRejectsBusinessErrorInside2xx(t *testing.T) {
	body := `{"code":"permission_denied","message":"forbidden"}`
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject a Connect permission_denied error")
	}
}

// TestParseKimiQuotaRejectsMissingMeter proves a successful response that
// omits either meter is rejected and does NOT fabricate a zero or reuse the
// other meter.
func TestParseKimiQuotaRejectsMissingMeter(t *testing.T) {
	missingWeekly := `{"rate_limit":{"usedPercent":52,"reset_seconds":12000}}`
	if _, err := ParseKimiQuota(missingWeekly); err == nil {
		t.Fatal("ParseKimiQuota must reject a response missing the weekly meter")
	}
	missingRate := `{"weekly":{"usedPercent":10,"reset_seconds":562800}}`
	if _, err := ParseKimiQuota(missingRate); err == nil {
		t.Fatal("ParseKimiQuota must reject a response missing the rate-limit meter")
	}
}

// TestParseKimiQuotaRejectsInvalidPercentage proves a percentage outside 0..100
// is rejected with an error identifying the invalid meter.
func TestParseKimiQuotaRejectsInvalidPercentage(t *testing.T) {
	body := kimiFixtureWithBody(150, 562800, 52, 12000)
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject a weekly percentage > 100")
	}
	body2 := kimiFixtureWithBody(10, 562800, -1, 12000)
	if _, err := ParseKimiQuota(body2); err == nil {
		t.Fatal("ParseKimiQuota must reject a negative rate-limit percentage")
	}
}

// TestParseKimiQuotaRejectsInvalidReset proves a missing/zero/negative reset
// is rejected. A reset must be a positive duration in the future.
func TestParseKimiQuotaRejectsInvalidReset(t *testing.T) {
	missing := kimiFixtureWithBody(10, nil, 52, 12000)
	if _, err := ParseKimiQuota(missing); err == nil {
		t.Fatal("ParseKimiQuota must reject a missing weekly reset")
	}
	zero := kimiFixtureWithBody(10, 0, 52, 12000)
	if _, err := ParseKimiQuota(zero); err == nil {
		t.Fatal("ParseKimiQuota must reject a zero weekly reset")
	}
	negative := kimiFixtureWithBody(10, 562800, 52, -5)
	if _, err := ParseKimiQuota(negative); err == nil {
		t.Fatal("ParseKimiQuota must reject a negative rate-limit reset")
	}
}

// TestParseKimiQuotaRejectsUnparseableJSON proves a malformed body is rejected.
func TestParseKimiQuotaRejectsUnparseableJSON(t *testing.T) {
	if _, err := ParseKimiQuota("not json"); err == nil {
		t.Fatal("ParseKimiQuota must reject unparseable JSON")
	}
}

// TestParseKimiQuotaResetNormalization proves the parser normalizes a reset
// represented as a localized countdown string ("6d 12h 20min") into seconds,
// matching the proposal sample. 6d 12h 20min = 518400 + 43200 + 1200 = 562800;
// 3h 20min = 10800 + 1200 = 12000. This is the EVIDENCE-GATED reset shape: the
// real capture may deliver seconds, a timestamp, or a string; the parser must
// convert to seconds and reject unparseable forms.
func TestParseKimiQuotaResetNormalization(t *testing.T) {
	body := `{"weekly":{"usedPercent":10,"reset_display":"6d 12h 20min"},"rate_limit":{"usedPercent":52,"reset_display":"3h 20min"}}`
	got, err := ParseKimiQuota(body)
	if err != nil {
		t.Fatalf("ParseKimiQuota with display strings: %v", err)
	}
	if got.Weekly.ResetInSec != 562800 {
		t.Errorf("weekly reset from display = %d, want 562800", got.Weekly.ResetInSec)
	}
	if got.RateLimit.ResetInSec != 12000 {
		t.Errorf("rate limit reset from display = %d, want 12000", got.RateLimit.ResetInSec)
	}
}

// TestParseKimiQuotaScalesRatioToPercentage proves the parser derives the
// percentage from amountUsedRatio (0..1) when usedPercent is absent, mirroring
// the console's ratioToPercentage. 0.10 → 10, 0.52 → 52.
func TestParseKimiQuotaScalesRatioToPercentage(t *testing.T) {
	body := `{"weekly":{"amountUsedRatio":0.10,"reset_seconds":562800},"rate_limit":{"amountUsedRatio":0.52,"reset_seconds":12000}}`
	got, err := ParseKimiQuota(body)
	if err != nil {
		t.Fatalf("ParseKimiQuota with ratios: %v", err)
	}
	if got.Weekly.UsagePercent != 10 {
		t.Errorf("weekly usage from ratio = %d, want 10", got.Weekly.UsagePercent)
	}
	if got.RateLimit.UsagePercent != 52 {
		t.Errorf("rate limit usage from ratio = %d, want 52", got.RateLimit.UsagePercent)
	}
}

// TestKimiQuotaDataReusesQuotaUsageLeaves (task 2.4) proves the Kimi aggregate
// reuses the shared QuotaUsage leaf type without changing QuotaData JSON
// semantics. KimiQuotaData has Weekly, RateLimit, and FetchedAt; it does NOT
// touch QuotaData.Rolling/Weekly/Monthly.
func TestKimiQuotaDataReusesQuotaUsageLeaves(t *testing.T) {
	var kd KimiQuotaData
	kd.Weekly = QuotaUsage{Status: "active", UsagePercent: 10, ResetInSec: 562800, ResetDisplay: "6d"}
	kd.RateLimit = QuotaUsage{Status: "active", UsagePercent: 52, ResetInSec: 12000, ResetDisplay: "3h"}
	kd.FetchedAt = time.Now()
	if kd.Weekly.UsagePercent != 10 || kd.RateLimit.UsagePercent != 52 {
		t.Fatalf("KimiQuotaData leaves = %#v", kd)
	}
}

// TestKimiQuotaDataDoesNotAlterQuotaDataJSON proves a serialized QuotaData
// (existing provider) is unchanged by the Kimi aggregate's existence — the
// two types are independent.
func TestKimiQuotaDataDoesNotAlterQuotaDataJSON(t *testing.T) {
	qd := QuotaData{Rolling: QuotaUsage{UsagePercent: 1}, Weekly: QuotaUsage{UsagePercent: 2}}
	out, _ := jsonMarshal(qd)
	if !strings.Contains(out, `"rolling"`) || !strings.Contains(out, `"weekly"`) {
		t.Fatalf("QuotaData JSON lost existing fields: %s", out)
	}
	if strings.Contains(out, "rate_limit") {
		t.Fatalf("QuotaData JSON must not carry Kimi's rate_limit field: %s", out)
	}
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
