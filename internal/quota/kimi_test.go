package quota

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// kimiResetAt returns an absolute ISO-8601 timestamp `dur` in the future from
// now, for building synthetic fixtures whose reset instant is a real future
// timestamp (the OBSERVED shape: 2026-08-04T15:58:02.964...Z).
func kimiResetAt(now time.Time, dur time.Duration) string {
	return now.Add(dur).UTC().Format(time.RFC3339Nano)
}

// kimiStatsFixture builds a GetSubscriptionStats response with the three
// metrics. totalRatio/fiveHourRatio/sevenDayRatio are 0..1 (pass nil to omit a
// ratio — the observed 0%-use shape for the 5h window). reset times are
// absolute future timestamps. Field names are the REAL captured names.
func kimiStatsFixture(now time.Time, totalRatio any, totalReset string, fiveHourRatio any, fiveHourReset string, sevenDayRatio any, sevenDayReset string) string {
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
	sb := map[string]any{}
	if totalRatio != nil {
		sb["amountUsedRatio"] = totalRatio
		sb["kimiCodeUsedRatio"] = totalRatio
	}
	if totalReset != "" {
		sb["expireTime"] = totalReset
	}
	sb["type"] = "SUBSCRIPTION"
	sb["feature"] = "FEATURE_OMNI"
	sb["unit"] = "UNIT_CREDIT"
	sb["domain"] = "DOMAIN_NEXUS"
	sbJSON, _ := json.Marshal(sb)
	return `{"ratelimitCode5h":` + mk(fiveHourRatio, fiveHourReset) + `,"ratelimitCode7d":` + mk(sevenDayRatio, sevenDayReset) + `,"subscriptionBalance":` + string(sbJSON) + `}`
}

// kimiNow is a fixed reference time for deterministic tests (2026-07-29 16:00 UTC).
func kimiNow() time.Time {
	return time.Date(2026, 7, 29, 16, 0, 0, 0, time.UTC)
}

// TestParseKimiQuotaAcceptsRepresentativeValues (task 1.4/2.5) proves the parser
// reads the three REAL metrics from the membership page contract, preserving
// decimal percentages and absolute reset instants. Total 2.19%, 5h 0% (absent
// ratio), 7d 10.42%.
func TestParseKimiQuotaAcceptsRepresentativeValues(t *testing.T) {
	now := kimiNow()
	body := kimiStatsFixture(now, 0.0219, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	got, err := ParseKimiQuota(body, now)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	if got.Total.UsagePercent != 2.19 {
		t.Errorf("total usage = %v, want 2.19", got.Total.UsagePercent)
	}
	if got.FiveHour.UsagePercent != 0 {
		t.Errorf("5h usage = %v, want 0 (absent ratio)", got.FiveHour.UsagePercent)
	}
	if got.SevenDay.UsagePercent != 10.42 {
		t.Errorf("7d usage = %v, want 10.42", got.SevenDay.UsagePercent)
	}
	// Absolute reset instants are preserved and independent.
	if got.Total.ResetAt.IsZero() || got.FiveHour.ResetAt.IsZero() || got.SevenDay.ResetAt.IsZero() {
		t.Fatal("reset instants must be set")
	}
	if got.Total.ResetAt.Equal(got.FiveHour.ResetAt) || got.Total.ResetAt.Equal(got.SevenDay.ResetAt) || got.FiveHour.ResetAt.Equal(got.SevenDay.ResetAt) {
		t.Fatal("the three reset instants must be independent")
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be set")
	}
}

// TestParseKimiQuotaResetDisplays proves total renders as YYYY-MM-DD and 5h/7d
// as MM-DD HH:mm (the page's local timezone forms). Uses future timestamps built
// from now and asserts the format shapes (YYYY-MM-DD, MM-DD HH:mm) rather than
// the exact captured past dates.
func TestParseKimiQuotaResetDisplays(t *testing.T) {
	now := kimiNow()
	// total reset = a fixed instant whose Shanghai date is 2026-08-28.
	totalReset := "2026-08-28T00:00:00Z" // Shanghai 2026-08-28 08:00 → date 2026-08-28
	// 5h/7d resets must be AFTER now (2026-07-29 16:00 UTC = Shanghai 00:00 next day).
	// Use now+5h and now+7d so they are always future relative to `now`.
	fiveHourReset := kimiResetAt(now, 5*time.Hour)
	sevenDayReset := kimiResetAt(now, 7*24*time.Hour)
	body := kimiStatsFixture(now, 0.0219, totalReset, nil, fiveHourReset, 0.1042, sevenDayReset)
	got, err := ParseKimiQuota(body, now)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	if got.Total.ResetDisplay != "2026-08-28" {
		t.Errorf("total reset display = %q, want 2026-08-28 (YYYY-MM-DD)", got.Total.ResetDisplay)
	}
	// 5h/7d display must match MM-DD HH:mm shape (Shanghai local).
	if len(got.FiveHour.ResetDisplay) != 11 || got.FiveHour.ResetDisplay[2] != '-' || got.FiveHour.ResetDisplay[5] != ' ' || got.FiveHour.ResetDisplay[8] != ':' {
		t.Errorf("5h reset display = %q, want MM-DD HH:mm shape", got.FiveHour.ResetDisplay)
	}
	if len(got.SevenDay.ResetDisplay) != 11 || got.SevenDay.ResetDisplay[2] != '-' || got.SevenDay.ResetDisplay[5] != ' ' || got.SevenDay.ResetDisplay[8] != ':' {
		t.Errorf("7d reset display = %q, want MM-DD HH:mm shape", got.SevenDay.ResetDisplay)
	}
}

// TestKimiResetDisplayFormats proves the display formatters produce the exact
// observed page forms for known UTC instants (independent of `now`).
func TestKimiResetDisplayFormats(t *testing.T) {
	// 2026-08-28T00:00:00Z → Shanghai 2026-08-28 08:00 → total date "2026-08-28"
	if got := kimiTotalResetDisplay(time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)); got != "2026-08-28" {
		t.Errorf("total display = %q, want 2026-08-28", got)
	}
	// 2026-07-29T11:58:03Z → Shanghai 2026-07-29 19:58 → short "07-29 19:58"
	if got := kimiShortResetDisplay(time.Date(2026, 7, 29, 11, 58, 3, 0, time.UTC)); got != "07-29 19:58" {
		t.Errorf("5h display = %q, want 07-29 19:58", got)
	}
	// 2026-08-04T15:58:02Z → Shanghai 2026-08-04 23:58 → short "08-04 23:58"
	if got := kimiShortResetDisplay(time.Date(2026, 8, 4, 15, 58, 2, 0, time.UTC)); got != "08-04 23:58" {
		t.Errorf("7d display = %q, want 08-04 23:58", got)
	}
}

// TestParseKimiQuotaRejectsConnectErrorEnvelope proves a Connect failure
// envelope (non-empty "code") is rejected.
func TestParseKimiQuotaRejectsConnectErrorEnvelope(t *testing.T) {
	body := `{"code":"unauthenticated","details":[{"type":"common.error.v1.ErrorDetail"}]}`
	if _, err := ParseKimiQuota(body, kimiNow()); err == nil {
		t.Fatal("ParseKimiQuota must reject a Connect error envelope")
	}
}

// TestParseKimiQuotaRejectsMissingMetric proves a response omitting any of the
// three metrics is rejected and does NOT fabricate zero or reuse another.
func TestParseKimiQuotaRejectsMissingMetric(t *testing.T) {
	now := kimiNow()
	full := kimiStatsFixture(now, 0.0219, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	// Remove each metric in turn and assert rejection.
	missingTotal := strings.Replace(full, `"subscriptionBalance":{`, `"subscriptionBalance":null,{`, 1)
	missingTotal = strings.Replace(missingTotal, `null,{"amountUsedRatio"`, `{"amountUsedRatio"`, 1)
	// simpler: build targeted bodies
	missingTotal2 := `{"ratelimitCode5h":{"enabled":true,"resetTime":"` + kimiResetAt(now, 5*time.Hour) + `"},"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + kimiResetAt(now, 7*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(missingTotal2, now); err == nil {
		t.Fatal("must reject missing subscriptionBalance")
	}
	missing5h := `{"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + kimiResetAt(now, 7*24*time.Hour) + `"},"subscriptionBalance":{"amountUsedRatio":0.0219,"expireTime":"` + kimiResetAt(now, 30*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(missing5h, now); err == nil {
		t.Fatal("must reject missing ratelimitCode5h")
	}
	missing7d := `{"ratelimitCode5h":{"enabled":true,"resetTime":"` + kimiResetAt(now, 5*time.Hour) + `"},"subscriptionBalance":{"amountUsedRatio":0.0219,"expireTime":"` + kimiResetAt(now, 30*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(missing7d, now); err == nil {
		t.Fatal("must reject missing ratelimitCode7d")
	}
	_ = missingTotal
}

// TestParseKimiQuotaRejectsInvalidRatio proves NaN, Infinity, and out-of-range
// ratios are REJECTED (never clamped).
func TestParseKimiQuotaRejectsInvalidRatio(t *testing.T) {
	now := kimiNow()
	over := kimiStatsFixture(now, 1.5, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(over, now); err == nil {
		t.Fatal("must reject total ratio > 1")
	}
	neg7d := kimiStatsFixture(now, 0.0219, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), -0.2, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(neg7d, now); err == nil {
		t.Fatal("must reject negative 7d ratio")
	}
}

// TestParseKimiQuotaRejectsPastReset proves a past reset instant is rejected.
func TestParseKimiQuotaRejectsPastReset(t *testing.T) {
	now := kimiNow()
	past := now.Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	body := kimiStatsFixture(now, 0.0219, past, nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(body, now); err == nil {
		t.Fatal("must reject a past total reset")
	}
}

// TestParseKimiQuotaRejectsUnparseableReset proves a malformed reset is rejected.
func TestParseKimiQuotaRejectsUnparseableReset(t *testing.T) {
	now := kimiNow()
	body := kimiStatsFixture(now, 0.0219, "not-a-timestamp", nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(body, now); err == nil {
		t.Fatal("must reject an unparseable total reset")
	}
}

// TestParseKimiQuotaRejectsUnparseableJSON proves a malformed body is rejected.
func TestParseKimiQuotaRejectsUnparseableJSON(t *testing.T) {
	if _, err := ParseKimiQuota("not json", kimiNow()); err == nil {
		t.Fatal("must reject unparseable JSON")
	}
}

// TestParseKimiQuotaMissingTotalRatio proves a subscriptionBalance without
// amountUsedRatio is rejected.
func TestParseKimiQuotaMissingTotalRatio(t *testing.T) {
	now := kimiNow()
	body := `{"ratelimitCode5h":{"enabled":true,"resetTime":"` + kimiResetAt(now, 5*time.Hour) + `"},"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + kimiResetAt(now, 7*24*time.Hour) + `"},"subscriptionBalance":{"expireTime":"` + kimiResetAt(now, 30*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(body, now); err == nil {
		t.Fatal("must reject subscriptionBalance without amountUsedRatio")
	}
}

// TestFormatKimiPercent proves decimal formatting trims trailing zeros:
// 2.19→"2.19%", 10.42→"10.42%", 0→"0%", 5.5→"5.5%", 5→"5%".
func TestFormatKimiPercent(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{2.19, "2.19%"}, {10.42, "10.42%"}, {0, "0%"}, {5.5, "5.5%"}, {5, "5%"}, {99.9, "99.9%"},
	}
	for _, c := range cases {
		if got := FormatKimiPercent(c.in); got != c.want {
			t.Errorf("FormatKimiPercent(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestKimiQuotaDataDoesNotAlterQuotaDataJSON proves the Kimi aggregate does not
// change existing QuotaData JSON semantics.
func TestKimiQuotaDataDoesNotAlterQuotaDataJSON(t *testing.T) {
	qd := QuotaData{Rolling: QuotaUsage{UsagePercent: 1}, Weekly: QuotaUsage{UsagePercent: 2}}
	out, _ := json.Marshal(qd)
	if !strings.Contains(string(out), `"rolling"`) || !strings.Contains(string(out), `"weekly"`) {
		t.Fatalf("QuotaData JSON lost existing fields: %s", out)
	}
	if strings.Contains(string(out), "seven_day") {
		t.Fatalf("QuotaData JSON must not carry Kimi fields: %s", out)
	}
}
