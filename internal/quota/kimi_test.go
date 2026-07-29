package quota

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// kimiResetOffset returns an ISO-8601 timestamp `dur` in the future from now,
// for building synthetic fixtures whose resetTime is a real absolute
// timestamp (the OBSERVED shape: 2026-08-04T15:58:03.138613843Z).
func kimiResetOffset(dur time.Duration) string {
	return time.Now().Add(dur).UTC().Format(time.RFC3339Nano)
}

// kimiFixture builds a GetSubscriptionStats response with the two meters. The
// resetTime values are absolute future timestamps; ratio is 0..1 (omit for the
// 0% case). Field names are the REAL captured names.
func kimiFixture(weeklyRatio any, weeklyReset string, freqRatio any, freqReset string) string {
	mk := func(ratio any, reset string) string {
		m := map[string]any{}
		if ratio != nil {
			m["ratio"] = ratio
		}
		m["enabled"] = true
		if reset != "" {
			m["resetTime"] = reset
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	return `{"ratelimitCode5h":` + mk(freqRatio, freqReset) + `,"ratelimitCode7d":` + mk(weeklyRatio, weeklyReset) + `,"subscriptionBalance":{"id":"synthetic-id","feature":"FEATURE_OMNI","type":"SUBSCRIPTION","unit":"UNIT_CREDIT","amountUsedRatio":0.02,"kimiCodeUsedRatio":0.02,"expireTime":"2026-08-28T00:00:00Z","domain":"DOMAIN_NEXUS"}}`
}

// TestParseKimiQuotaAcceptsRepresentativeValues (task 1.4 / 2.5) proves the
// parser reads the two independent meters from the REAL captured structure:
// ratelimitCode7d.ratio (weekly) and ratelimitCode5h.ratio (frequency), with
// resetTime as absolute future timestamps. Weekly 0.10→10% / 6d (562800s);
// frequency 0.52→52% / 3h 20min (12000s).
func TestParseKimiQuotaAcceptsRepresentativeValues(t *testing.T) {
	body := kimiFixture(0.10, kimiResetOffset(562800*time.Second), 0.52, kimiResetOffset(12000*time.Second))
	got, err := ParseKimiQuota(body)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	if got.Weekly.UsagePercent != 10 {
		t.Errorf("weekly usage = %d, want 10", got.Weekly.UsagePercent)
	}
	if got.Weekly.ResetInSec < 562000 || got.Weekly.ResetInSec > 563600 {
		t.Errorf("weekly reset = %d, want ~562800", got.Weekly.ResetInSec)
	}
	if got.Weekly.ResetDisplay != "6d" {
		t.Errorf("weekly reset display = %q, want 6d", got.Weekly.ResetDisplay)
	}
	if got.RateLimit.UsagePercent != 52 {
		t.Errorf("frequency usage = %d, want 52", got.RateLimit.UsagePercent)
	}
	if got.RateLimit.ResetInSec < 11000 || got.RateLimit.ResetInSec > 13000 {
		t.Errorf("frequency reset = %d, want ~12000", got.RateLimit.ResetInSec)
	}
	if got.RateLimit.ResetDisplay != "3h" {
		t.Errorf("frequency reset display = %q, want 3h", got.RateLimit.ResetDisplay)
	}
	// The two reset values are INDEPENDENT — not a shared field.
	if got.Weekly.ResetInSec == got.RateLimit.ResetInSec {
		t.Fatal("weekly and frequency reset must be independent values")
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set at parse time")
	}
}

// TestParseKimiQuotaAcceptsAbsentRatioAsZero proves an ABSENT ratio (the real
// 0%-usage shape: ratelimitCode5h has no ratio field) parses as 0%. The reset
// countdown still comes from resetTime.
func TestParseKimiQuotaAcceptsAbsentRatioAsZero(t *testing.T) {
	body := kimiFixture(0.10, kimiResetOffset(562800*time.Second), nil, kimiResetOffset(12000*time.Second))
	got, err := ParseKimiQuota(body)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	if got.RateLimit.UsagePercent != 0 {
		t.Errorf("frequency usage = %d, want 0 (absent ratio)", got.RateLimit.UsagePercent)
	}
	if got.RateLimit.ResetInSec < 11000 {
		t.Errorf("frequency reset = %d, want countdown from resetTime", got.RateLimit.ResetInSec)
	}
}

// TestParseKimiQuotaRejectsConnectErrorEnvelope proves a Connect failure
// envelope (OBSERVED shape: a non-empty top-level "code" string) is rejected.
// A 2xx carrying a Connect error code is NOT quota success.
func TestParseKimiQuotaRejectsConnectErrorEnvelope(t *testing.T) {
	body := `{"code":"unauthenticated","details":[{"type":"common.error.v1.ErrorDetail"}]}`
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject a Connect error envelope (non-empty code string)")
	}
}

// TestParseKimiQuotaRejectsBusinessErrorInside2xx proves a Connect error with
// a non-empty code (e.g. permission_denied) is rejected.
func TestParseKimiQuotaRejectsBusinessErrorInside2xx(t *testing.T) {
	body := `{"code":"permission_denied","message":"forbidden"}`
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject a Connect permission_denied error")
	}
}

// TestParseKimiQuotaRejectsMissingMeter proves a response omitting either
// meter is rejected and does NOT fabricate a zero or reuse the other meter.
func TestParseKimiQuotaRejectsMissingMeter(t *testing.T) {
	missingWeekly := `{"ratelimitCode5h":{"ratio":0.52,"enabled":true,"resetTime":"` + kimiResetOffset(12000*time.Second) + `"}}`
	if _, err := ParseKimiQuota(missingWeekly); err == nil {
		t.Fatal("ParseKimiQuota must reject a response missing ratelimitCode7d")
	}
	missingFreq := `{"ratelimitCode7d":{"ratio":0.10,"enabled":true,"resetTime":"` + kimiResetOffset(562800*time.Second) + `"}}`
	if _, err := ParseKimiQuota(missingFreq); err == nil {
		t.Fatal("ParseKimiQuota must reject a response missing ratelimitCode5h")
	}
}

// TestParseKimiQuotaRejectsInvalidPercentage proves a ratio outside 0..1 is
// REJECTED (not clamped): a weekly ratio of 1.5 or a negative frequency
// ratio must fail. Kimi's ratio is a 0..1 usage ratio; an out-of-range value
// is a malformed/unsupported response, never a silently-clamped 100%.
func TestParseKimiQuotaRejectsInvalidPercentage(t *testing.T) {
	over := kimiFixture(1.5, kimiResetOffset(562800*time.Second), 0.52, kimiResetOffset(12000*time.Second))
	if _, err := ParseKimiQuota(over); err == nil {
		t.Fatal("ParseKimiQuota must reject a weekly ratio > 1 (not clamp to 100)")
	}
	negative := kimiFixture(0.10, kimiResetOffset(562800*time.Second), -0.2, kimiResetOffset(12000*time.Second))
	if _, err := ParseKimiQuota(negative); err == nil {
		t.Fatal("ParseKimiQuota must reject a negative frequency ratio")
	}
}

// TestParseKimiQuotaRejectsMissingReset proves a missing resetTime is rejected.
func TestParseKimiQuotaRejectsMissingReset(t *testing.T) {
	missing := `{"ratelimitCode5h":{"ratio":0.52,"enabled":true},"ratelimitCode7d":{"ratio":0.10,"enabled":true,"resetTime":"` + kimiResetOffset(562800*time.Second) + `"}}`
	if _, err := ParseKimiQuota(missing); err == nil {
		t.Fatal("ParseKimiQuota must reject a missing frequency resetTime")
	}
}

// TestParseKimiQuotaRejectsPastReset proves a past resetTime is rejected — the
// reset must be in the future.
func TestParseKimiQuotaRejectsPastReset(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	body := kimiFixture(0.10, past, 0.52, kimiResetOffset(12000*time.Second))
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject a past weekly resetTime")
	}
}

// TestParseKimiQuotaRejectsUnparseableReset proves a malformed resetTime is
// rejected.
func TestParseKimiQuotaRejectsUnparseableReset(t *testing.T) {
	body := kimiFixture(0.10, "not-a-timestamp", 0.52, kimiResetOffset(12000*time.Second))
	if _, err := ParseKimiQuota(body); err == nil {
		t.Fatal("ParseKimiQuota must reject an unparseable resetTime")
	}
}

// TestParseKimiQuotaRejectsUnparseableJSON proves a malformed body is rejected.
func TestParseKimiQuotaRejectsUnparseableJSON(t *testing.T) {
	if _, err := ParseKimiQuota("not json"); err == nil {
		t.Fatal("ParseKimiQuota must reject unparseable JSON")
	}
}

// TestParseKimiQuotaResetFromAbsoluteTimestamp proves the reset countdown is
// resetTime − now (absolute ISO-8601 timestamp), NOT a duration field. This is
// the OBSERVED reset representation.
func TestParseKimiQuotaResetFromAbsoluteTimestamp(t *testing.T) {
	// 3h 20min from now = 12000s.
	body := kimiFixture(0.10, kimiResetOffset(562800*time.Second), 0.52, kimiResetOffset(12000*time.Second))
	got, err := ParseKimiQuota(body)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	if got.RateLimit.ResetDisplay != "3h" {
		t.Errorf("frequency reset display = %q, want 3h (from absolute timestamp)", got.RateLimit.ResetDisplay)
	}
}

// TestKimiQuotaDataReusesQuotaUsageLeaves (task 2.4) proves the Kimi aggregate
// reuses the shared QuotaUsage leaf type without changing QuotaData JSON
// semantics.
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
// (existing provider) is unchanged by the Kimi aggregate's existence.
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
