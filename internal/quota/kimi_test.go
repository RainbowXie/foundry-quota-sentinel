package quota

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func kimiResetAt(now time.Time, dur time.Duration) string {
	return now.Add(dur).UTC().Format(time.RFC3339Nano)
}

func kimiStatsFixture(now time.Time, totalRatio, codeRatio any, totalReset string, fiveHourRatio any, fiveHourReset string, sevenDayRatio any, sevenDayReset string) string {
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
	}
	if codeRatio != nil {
		sb["kimiCodeUsedRatio"] = codeRatio
	}
	if totalReset != "" {
		sb["expireTime"] = totalReset
	}
	sb["type"] = "SUBSCRIPTION"
	sbJSON, _ := json.Marshal(sb)
	return `{"ratelimitCode5h":` + mk(fiveHourRatio, fiveHourReset) + `,"ratelimitCode7d":` + mk(sevenDayRatio, sevenDayReset) + `,"subscriptionBalance":` + string(sbJSON) + `}`
}

func kimiNow() time.Time {
	return time.Date(2026, 7, 29, 16, 0, 0, 0, time.UTC)
}

// TestParseKimiQuotaAcceptsGroupedValues proves the parser reads the four
// grouped values: total Kimi (black), total Code (blue), 5h Code, 7d Code.
// Uses DISTINCT total/Code ratios so label separation is provable.
func TestParseKimiQuotaAcceptsGroupedValues(t *testing.T) {
	now := kimiNow()
	// total=0.0219 (2.19%), code=0.0199 (1.99%) → kimi=0.20%
	body := kimiStatsFixture(now, 0.0219, 0.0199, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	got, err := ParseKimiQuota(body, now)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
	// Use tolerance for float64 comparisons (0.0199*100 = 1.99000...2).
	approx := func(a, b float64) bool { return math.Abs(a-b) < 0.001 }
	if !approx(got.Total.TotalPercent, 2.19) {
		t.Errorf("total = %v, want 2.19", got.Total.TotalPercent)
	}
	if !approx(got.Total.CodePercent, 1.99) {
		t.Errorf("code = %v, want 1.99", got.Total.CodePercent)
	}
	if !approx(got.Total.KimiPercent, 0.20) {
		t.Errorf("kimi = %v, want 0.20", got.Total.KimiPercent)
	}
	if got.FiveHour.UsagePercent != 0 {
		t.Errorf("5h = %v, want 0", got.FiveHour.UsagePercent)
	}
	if got.SevenDay.UsagePercent != 10.42 {
		t.Errorf("7d = %v, want 10.42", got.SevenDay.UsagePercent)
	}
	if got.Total.ResetAt.IsZero() || got.FiveHour.ResetAt.IsZero() || got.SevenDay.ResetAt.IsZero() {
		t.Fatal("reset instants must be set")
	}
}

// TestParseKimiQuotaTotalKimiIsDifference proves Kimi = total − code.
func TestParseKimiQuotaTotalKimiIsDifference(t *testing.T) {
	now := kimiNow()
	body := kimiStatsFixture(now, 0.05, 0.03, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	got, err := ParseKimiQuota(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total.KimiPercent != 2.0 {
		t.Errorf("kimi = %v, want 2.0 (5-3)", got.Total.KimiPercent)
	}
}

// TestParseKimiQuotaRejectsNegativeKimi proves code > total is rejected.
func TestParseKimiQuotaRejectsNegativeKimi(t *testing.T) {
	now := kimiNow()
	body := kimiStatsFixture(now, 0.02, 0.03, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(body, now); err == nil {
		t.Fatal("must reject code > total (negative kimi)")
	}
}

// TestParseKimiQuotaResetDisplays proves total=YYYY-MM-DD, 5h/7d=MM-DD HH:mm.
func TestParseKimiQuotaResetDisplays(t *testing.T) {
	now := kimiNow()
	body := kimiStatsFixture(now, 0.0219, 0.0199, "2026-08-28T00:00:00Z", nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	got, err := ParseKimiQuota(body, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total.ResetDisplay != "2026-08-28" {
		t.Errorf("total display = %q, want 2026-08-28", got.Total.ResetDisplay)
	}
	if len(got.FiveHour.ResetDisplay) != 11 || got.FiveHour.ResetDisplay[2] != '-' {
		t.Errorf("5h display = %q, want MM-DD HH:mm", got.FiveHour.ResetDisplay)
	}
}

func TestKimiResetDisplayFormats(t *testing.T) {
	if got := kimiTotalResetDisplay(time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)); got != "2026-08-28" {
		t.Errorf("total = %q", got)
	}
	if got := kimiShortResetDisplay(time.Date(2026, 7, 29, 11, 58, 3, 0, time.UTC)); got != "07-29 19:58" {
		t.Errorf("5h = %q", got)
	}
}

func TestParseKimiQuotaRejectsConnectError(t *testing.T) {
	if _, err := ParseKimiQuota(`{"code":"unauthenticated"}`, kimiNow()); err == nil {
		t.Fatal("must reject Connect error")
	}
}

func TestParseKimiQuotaRejectsMissingMetric(t *testing.T) {
	now := kimiNow()
	missingTotal := `{"ratelimitCode5h":{"enabled":true,"resetTime":"` + kimiResetAt(now, 5*time.Hour) + `"},"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + kimiResetAt(now, 7*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(missingTotal, now); err == nil {
		t.Fatal("must reject missing subscriptionBalance")
	}
	missing5h := `{"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + kimiResetAt(now, 7*24*time.Hour) + `"},"subscriptionBalance":{"amountUsedRatio":0.0219,"kimiCodeUsedRatio":0.0199,"expireTime":"` + kimiResetAt(now, 30*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(missing5h, now); err == nil {
		t.Fatal("must reject missing ratelimitCode5h")
	}
}

func TestParseKimiQuotaRejectsInvalidRatio(t *testing.T) {
	now := kimiNow()
	over := kimiStatsFixture(now, 1.5, 0.01, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(over, now); err == nil {
		t.Fatal("must reject total ratio > 1")
	}
}

func TestParseKimiQuotaRejectsPastReset(t *testing.T) {
	now := kimiNow()
	past := now.Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	body := kimiStatsFixture(now, 0.0219, 0.0199, past, nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	if _, err := ParseKimiQuota(body, now); err == nil {
		t.Fatal("must reject past total reset")
	}
}

func TestParseKimiQuotaRejectsUnparseableJSON(t *testing.T) {
	if _, err := ParseKimiQuota("not json", kimiNow()); err == nil {
		t.Fatal("must reject unparseable JSON")
	}
}

func TestParseKimiQuotaMissingCodeRatio(t *testing.T) {
	now := kimiNow()
	body := `{"ratelimitCode5h":{"enabled":true,"resetTime":"` + kimiResetAt(now, 5*time.Hour) + `"},"ratelimitCode7d":{"ratio":0.1042,"enabled":true,"resetTime":"` + kimiResetAt(now, 7*24*time.Hour) + `"},"subscriptionBalance":{"amountUsedRatio":0.0219,"expireTime":"` + kimiResetAt(now, 30*24*time.Hour) + `"}}`
	if _, err := ParseKimiQuota(body, now); err == nil {
		t.Fatal("must reject missing kimiCodeUsedRatio")
	}
}

func TestFormatKimiPercent(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{2.19, "2.19%"}, {10.42, "10.42%"}, {0, "0%"}, {5.5, "5.5%"}, {5, "5%"},
	}
	for _, c := range cases {
		if got := FormatKimiPercent(c.in); got != c.want {
			t.Errorf("FormatKimiPercent(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

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
