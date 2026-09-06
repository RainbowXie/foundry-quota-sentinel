package kimi

import (
	"encoding/json"
	"math"
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

func TestParseKimiQuotaAcceptsGroupedValues(t *testing.T) {
	now := kimiNow()
	body := kimiStatsFixture(now, 0.0219, 0.0199, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.1042, kimiResetAt(now, 7*24*time.Hour))
	got, err := ParseKimiQuota(body, now)
	if err != nil {
		t.Fatalf("ParseKimiQuota: %v", err)
	}
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
	if !approx(got.SevenDay.UsagePercent, 10.42) {
		t.Errorf("7d = %v, want 10.42", got.SevenDay.UsagePercent)
	}
}

func TestFormatKimiPercent(t *testing.T) {
	tests := []struct {
		val  float64
		want string
	}{
		{0, "0%"},
		{2.19, "2.19%"},
		{10.4, "10.4%"},
		{100, "100%"},
	}
	for _, tt := range tests {
		if got := FormatKimiPercent(tt.val); got != tt.want {
			t.Errorf("FormatKimiPercent(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestParseKimiQuotaRejectsMalformed(t *testing.T) {
	now := kimiNow()
	tests := []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "not json", body: "not json"},
		{name: "connect error", body: `{"code":"unauthenticated"}`},
		{name: "missing 7d", body: `{"ratelimitCode5h":{},"subscriptionBalance":{}}`},
		{name: "total kimi negative", body: kimiStatsFixture(now, 0.01, 0.02, kimiResetAt(now, 30*24*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.10, kimiResetAt(now, 7*24*time.Hour))},
		{name: "expired reset", body: kimiStatsFixture(now, 0.10, 0.05, kimiResetAt(now, -1*time.Hour), nil, kimiResetAt(now, 5*time.Hour), 0.10, kimiResetAt(now, 7*24*time.Hour))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseKimiQuota(tt.body, now)
			if err == nil {
				t.Fatalf("expected error for %q, got %+v", tt.name, got)
			}
		})
	}
}
