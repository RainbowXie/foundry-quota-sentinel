package quota

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// KimiQuotaData is the provider-specific Kimi Code quota aggregate for the
// membership quota page (https://www.kimi.com/membership/subscription?tab=quota).
// It carries three INDEPENDENTLY resetting metrics — total, 5-hour, and 7-day
// usage — each with a DECIMAL percentage and an ABSOLUTE reset instant. This
// replaces the earlier two-integer-meter console model: Kimi's real values are
// decimal (2.19%, 10.42%) and there are three metrics, not two. Existing
// QuotaData/QuotaUsage JSON semantics for other providers are unchanged.
type KimiQuotaData struct {
	Total     KimiQuotaUsage `json:"total"`
	FiveHour  KimiQuotaUsage `json:"five_hour"`
	SevenDay  KimiQuotaUsage `json:"seven_day"`
	FetchedAt time.Time      `json:"fetched_at"`
}

// KimiQuotaUsage is one decimal metric: a normalized percentage (0..100, up to
// two decimals), an absolute reset instant, the derived seconds-until-reset,
// a page-consistent absolute reset display, and a status. It deliberately uses
// float64 (not the integer QuotaUsage.UsagePercent) so values like 2.19/10.42
// survive parsing. The percentage is ratio*100 with trailing-zero trimming for
// display ("2.19%", "10.42%", "0%").
type KimiQuotaUsage struct {
	Status       string    `json:"status"`
	UsagePercent float64   `json:"usage_percent"`
	ResetAt      time.Time `json:"reset_at"`
	ResetInSec   int       `json:"reset_in_sec"`
	ResetDisplay string    `json:"reset_display"`
}

// Kimi uses Buf Connect gRPC-Web over JSON. OBSERVED contract (captured from a
// real authenticated membership-page session): success = HTTP 200 + a body
// with NO top-level "code" string; failure = non-2xx with
// {"code":"unauthenticated",...}. ParseKimiQuota rejects a body carrying a
// non-empty "code" string (the Connect error envelope) and accepts a body
// with no "code" that parses into the three metrics.

// kimiRateLimit is one rate-limit object in the GetSubscriptionStats response
// (OBSERVED field names). ratio is a 0..1 usage ratio (absent at 0% usage →
// treated as 0); enabled flags the window; resetTime is an absolute ISO-8601
// timestamp.
type kimiRateLimit struct {
	Ratio     *float64 `json:"ratio,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
	ResetTime string   `json:"resetTime,omitempty"`
}

// kimiStatsResponse is the JSON envelope of a successful GetSubscriptionStats
// response (OBSERVED top-level keys: ratelimitCode5h, ratelimitCode7d,
// subscriptionBalance). The parser binds all three metrics:
//   - total: subscriptionBalance.amountUsedRatio + expireTime
//   - 5-hour: ratelimitCode5h (ratio absent => 0%) + resetTime
//   - 7-day: ratelimitCode7d.ratio + resetTime
type kimiStatsResponse struct {
	RatelimitCode5h     *kimiRateLimit `json:"ratelimitCode5h,omitempty"`
	RatelimitCode7d     *kimiRateLimit `json:"ratelimitCode7d,omitempty"`
	SubscriptionBalance *struct {
		AmountUsedRatio   *float64 `json:"amountUsedRatio,omitempty"`
		KimiCodeUsedRatio *float64 `json:"kimiCodeUsedRatio,omitempty"`
		ExpireTime        string   `json:"expireTime,omitempty"`
	} `json:"subscriptionBalance,omitempty"`
}

// kimiConnectError is the Connect failure envelope: a non-empty "code" string.
type kimiConnectError struct {
	Code string `json:"code"`
}

// ParseKimiQuota parses a sanitized Kimi GetSubscriptionStats response body
// into the three-metric decimal aggregate. It requires:
//   - a well-formed JSON body;
//   - NO top-level "code" string (a Connect error envelope is a failure);
//   - ratelimitCode5h, ratelimitCode7d, and subscriptionBalance all present;
//   - total ratio (amountUsedRatio) and 7-day ratio finite in 0..1 (an absent
//     5-hour ratio is accepted as 0%, the observed zero-use shape);
//   - each metric's reset instant present, parseable, and in the FUTURE.
//
// Percentages are ratio*100 (decimal, NOT rounded to integer). ResetAt is the
// absolute ISO-8601 timestamp; ResetInSec = seconds until ResetAt; ResetDisplay
// is the page-consistent local-time format: total as YYYY-MM-DD, 5h/7d as
// MM-DD HH:mm (Asia/Shanghai local — the observed page timezone). The three
// resets are retained INDEPENDENTLY.
func ParseKimiQuota(body string, now time.Time) (*KimiQuotaData, error) {
	if body == "" {
		return nil, fmt.Errorf("Kimi 响应为空")
	}
	// A Connect error envelope (non-empty "code" string) is a failure even
	// inside a 2xx.
	var cerr kimiConnectError
	if err := json.Unmarshal([]byte(body), &cerr); err == nil && cerr.Code != "" {
		return nil, fmt.Errorf("Kimi 接口业务失败: %s", cerr.Code)
	}
	var raw kimiStatsResponse
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("Kimi 响应解析失败: %w", err)
	}
	if raw.RatelimitCode7d == nil {
		return nil, fmt.Errorf("Kimi 响应缺少 7 天用量 meter (ratelimitCode7d)")
	}
	if raw.RatelimitCode5h == nil {
		return nil, fmt.Errorf("Kimi 响应缺少 5 小时用量 meter (ratelimitCode5h)")
	}
	if raw.SubscriptionBalance == nil {
		return nil, fmt.Errorf("Kimi 响应缺少总使用量 meter (subscriptionBalance)")
	}
	total, err := parseKimiBalance(raw.SubscriptionBalance, now)
	if err != nil {
		return nil, err
	}
	fiveHour, err := parseKimiRateLimit(raw.RatelimitCode5h, "5 小时用量", now, kimiShortResetDisplay)
	if err != nil {
		return nil, err
	}
	sevenDay, err := parseKimiRateLimit(raw.RatelimitCode7d, "7 天用量", now, kimiShortResetDisplay)
	if err != nil {
		return nil, err
	}
	return &KimiQuotaData{
		Total:     total,
		FiveHour:  fiveHour,
		SevenDay:  sevenDay,
		FetchedAt: now,
	}, nil
}

// parseKimiBalance builds the total metric from subscriptionBalance.
func parseKimiBalance(b *struct {
	AmountUsedRatio   *float64 `json:"amountUsedRatio,omitempty"`
	KimiCodeUsedRatio *float64 `json:"kimiCodeUsedRatio,omitempty"`
	ExpireTime        string   `json:"expireTime,omitempty"`
}, now time.Time) (KimiQuotaUsage, error) {
	if b.AmountUsedRatio == nil {
		return KimiQuotaUsage{}, fmt.Errorf("Kimi 总使用量缺少 amountUsedRatio")
	}
	pct, err := kimiRatioToPercent(*b.AmountUsedRatio, "总使用量")
	if err != nil {
		return KimiQuotaUsage{}, err
	}
	resetAt, err := kimiParseResetAt(b.ExpireTime, "总使用量", now)
	if err != nil {
		return KimiQuotaUsage{}, err
	}
	return KimiQuotaUsage{
		Status:       "active",
		UsagePercent: pct,
		ResetAt:      resetAt,
		ResetInSec:   int(time.Until(resetAt).Seconds()),
		ResetDisplay: kimiTotalResetDisplay(resetAt),
	}, nil
}

// parseKimiRateLimit builds one 5h/7d metric. resetFmt chooses the display
// format (short MM-DD HH:mm for the rate-limit windows).
func parseKimiRateLimit(r *kimiRateLimit, name string, now time.Time, resetFmt func(time.Time) string) (KimiQuotaUsage, error) {
	pct := 0.0
	if r.Ratio != nil {
		var err error
		pct, err = kimiRatioToPercent(*r.Ratio, name)
		if err != nil {
			return KimiQuotaUsage{}, err
		}
	}
	resetAt, err := kimiParseResetAt(r.ResetTime, name, now)
	if err != nil {
		return KimiQuotaUsage{}, err
	}
	return KimiQuotaUsage{
		Status:       "active",
		UsagePercent: pct,
		ResetAt:      resetAt,
		ResetInSec:   int(time.Until(resetAt).Seconds()),
		ResetDisplay: resetFmt(resetAt),
	}, nil
}

// kimiRatioToPercent converts a 0..1 ratio to a decimal percentage (ratio*100).
// NaN, Infinity, and out-of-range ratios are REJECTED (never clamped). The
// percentage is NOT rounded to an integer — 0.0219 → 2.19, 0.1042 → 10.42.
func kimiRatioToPercent(r float64, name string) (float64, error) {
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0, fmt.Errorf("Kimi %s 用量比例非数值", name)
	}
	if r < 0 || r > 1 {
		return 0, fmt.Errorf("Kimi %s 用量比例 %.4f 越界（应为 0..1）", name, r)
	}
	return r * 100, nil
}

// kimiParseResetAt parses an absolute ISO-8601 reset timestamp and rejects
// missing/unparseable/past values. resetAt is the source of truth.
func kimiParseResetAt(resetTime, name string, now time.Time) (time.Time, error) {
	if resetTime == "" {
		return time.Time{}, fmt.Errorf("Kimi %s 重置时间缺失", name)
	}
	t, err := time.Parse(time.RFC3339Nano, resetTime)
	if err != nil {
		t, err = time.Parse(time.RFC3339, resetTime)
		if err != nil {
			return time.Time{}, fmt.Errorf("Kimi %s 重置时间无法解析: %w", name, err)
		}
	}
	if !t.After(now) {
		return time.Time{}, fmt.Errorf("Kimi %s 重置时间已过期", name)
	}
	return t, nil
}

// kimiLocal renders a time in the observed page timezone (Asia/Shanghai).
var kimiPageLocation = func() time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return *time.UTC
	}
	return *loc
}()

// kimiTotalResetDisplay renders the total (monthly) reset as YYYY-MM-DD in the
// page timezone, matching the visible "2026-08-27" form.
func kimiTotalResetDisplay(t time.Time) string {
	return t.In(&kimiPageLocation).Format("2006-01-02")
}

// kimiShortResetDisplay renders the 5h/7d reset as MM-DD HH:mm in the page
// timezone, matching the visible "07-29 19:58" / "08-04 23:58" forms.
func kimiShortResetDisplay(t time.Time) string {
	return t.In(&kimiPageLocation).Format("01-02 15:04")
}

// FormatKimiPercent renders a decimal percentage up to 2 places with trailing
// zeros trimmed: 2.19 → "2.19%", 10.42 → "10.42%", 0 → "0%". Centralized so CLI,
// API, and sidebar render identically.
func FormatKimiPercent(p float64) string {
	if p == 0 {
		return "0%"
	}
	// Round to 2 decimals, trim trailing zeros.
	s := fmt.Sprintf("%.2f", p)
	s = trimTrailingZeros(s)
	return s + "%"
}

func trimTrailingZeros(s string) string {
	// s looks like "2.19" or "10.00" or "2.20".
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[:i] // all trailing were zeros + the dot
		}
		if s[i] != '0' {
			return s[:i+1]
		}
	}
	return s
}
