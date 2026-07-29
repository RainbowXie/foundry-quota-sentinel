package quota

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// KimiQuotaData is the provider-specific Kimi Code quota aggregate for the
// membership quota page. It carries three groups: a Total group with separate
// Kimi and Code percentages (the total-usage bar's black and blue segments),
// a 5-hour Code window, and a 7-day Code window. Each has a decimal percentage
// and an absolute reset instant. Existing QuotaData/QuotaUsage JSON semantics
// for other providers are unchanged.
type KimiQuotaData struct {
	Total     KimiTotalUsage `json:"total"`
	FiveHour  KimiQuotaUsage `json:"five_hour"`
	SevenDay  KimiQuotaUsage `json:"seven_day"`
	FetchedAt time.Time      `json:"fetched_at"`
}

// KimiTotalUsage is the total-usage group: the overall total percentage
// (black+blue combined), the Kimi portion (black segment), and the Code
// portion (blue segment), plus a shared reset instant. KimiPortion =
// TotalPercent − CodePercent (the response has amountUsedRatio = total and
// kimiCodeUsedRatio = Code; there is no separate kimiUsedRatio field).
type KimiTotalUsage struct {
	Status       string    `json:"status"`
	TotalPercent float64   `json:"total_percent"`
	KimiPercent  float64   `json:"kimi_percent"`
	CodePercent  float64   `json:"code_percent"`
	ResetAt      time.Time `json:"reset_at"`
	ResetInSec   int       `json:"reset_in_sec"`
	ResetDisplay string    `json:"reset_display"`
}

// KimiQuotaUsage is one decimal Code-window metric: a normalized percentage
// (0..100, up to two decimals), an absolute reset instant, derived seconds, a
// page-consistent reset display, and a status. Uses float64 (not integer
// QuotaUsage.UsagePercent) so 2.19/10.42 survive parsing.
type KimiQuotaUsage struct {
	Status       string    `json:"status"`
	UsagePercent float64   `json:"usage_percent"`
	ResetAt      time.Time `json:"reset_at"`
	ResetInSec   int       `json:"reset_in_sec"`
	ResetDisplay string    `json:"reset_display"`
}

// kimiRateLimit is one rate-limit object in GetSubscriptionStats (OBSERVED).
type kimiRateLimit struct {
	Ratio     *float64 `json:"ratio,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
	ResetTime string   `json:"resetTime,omitempty"`
}

// kimiStatsResponse is the JSON envelope of a successful GetSubscriptionStats
// response (OBSERVED top-level keys: ratelimitCode5h, ratelimitCode7d,
// subscriptionBalance).
type kimiStatsResponse struct {
	RatelimitCode5h     *kimiRateLimit `json:"ratelimitCode5h,omitempty"`
	RatelimitCode7d     *kimiRateLimit `json:"ratelimitCode7d,omitempty"`
	SubscriptionBalance *struct {
		AmountUsedRatio   *float64 `json:"amountUsedRatio,omitempty"`
		KimiCodeUsedRatio *float64 `json:"kimiCodeUsedRatio,omitempty"`
		ExpireTime        string   `json:"expireTime,omitempty"`
	} `json:"subscriptionBalance,omitempty"`
}

type kimiConnectError struct {
	Code string `json:"code"`
}

// ParseKimiQuota parses a sanitized Kimi GetSubscriptionStats response body
// into the grouped aggregate. It requires:
//   - a well-formed JSON body with NO top-level "code" string;
//   - ratelimitCode5h, ratelimitCode7d, and subscriptionBalance all present;
//   - total = amountUsedRatio, Code = kimiCodeUsedRatio, both finite 0..1;
//   - Kimi portion = total − Code (must be >= 0);
//   - 7-day ratio finite 0..1 (5-hour absent ratio accepted as 0%);
//   - each group's reset instant present, parseable, and in the FUTURE.
//
// Percentages are ratio*100 (decimal, NOT rounded to integer). Total reset
// display = YYYY-MM-DD; 5h/7d display = MM-DD HH:mm (Asia/Shanghai local).
func ParseKimiQuota(body string, now time.Time) (*KimiQuotaData, error) {
	if body == "" {
		return nil, fmt.Errorf("Kimi 响应为空")
	}
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
	total, err := parseKimiTotal(raw.SubscriptionBalance, now)
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

func parseKimiTotal(b *struct {
	AmountUsedRatio   *float64 `json:"amountUsedRatio,omitempty"`
	KimiCodeUsedRatio *float64 `json:"kimiCodeUsedRatio,omitempty"`
	ExpireTime        string   `json:"expireTime,omitempty"`
}, now time.Time) (KimiTotalUsage, error) {
	if b.AmountUsedRatio == nil {
		return KimiTotalUsage{}, fmt.Errorf("Kimi 总使用量缺少 amountUsedRatio")
	}
	if b.KimiCodeUsedRatio == nil {
		return KimiTotalUsage{}, fmt.Errorf("Kimi 总使用量缺少 kimiCodeUsedRatio")
	}
	totalPct, err := kimiRatioToPercent(*b.AmountUsedRatio, "总使用量")
	if err != nil {
		return KimiTotalUsage{}, err
	}
	codePct, err := kimiRatioToPercent(*b.KimiCodeUsedRatio, "总使用量 Code")
	if err != nil {
		return KimiTotalUsage{}, err
	}
	kimiPct := totalPct - codePct
	if kimiPct < 0 {
		return KimiTotalUsage{}, fmt.Errorf("Kimi 总使用量 Kimi 部分为负 (%.4f − %.4f)", totalPct, codePct)
	}
	resetAt, err := kimiParseResetAt(b.ExpireTime, "总使用量", now)
	if err != nil {
		return KimiTotalUsage{}, err
	}
	return KimiTotalUsage{
		Status:       "active",
		TotalPercent: totalPct,
		KimiPercent:  kimiPct,
		CodePercent:  codePct,
		ResetAt:      resetAt,
		ResetInSec:   int(time.Until(resetAt).Seconds()),
		ResetDisplay: kimiTotalResetDisplay(resetAt),
	}, nil
}

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

func kimiRatioToPercent(r float64, name string) (float64, error) {
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0, fmt.Errorf("Kimi %s 用量比例非数值", name)
	}
	if r < 0 || r > 1 {
		return 0, fmt.Errorf("Kimi %s 用量比例 %.4f 越界（应为 0..1）", name, r)
	}
	return r * 100, nil
}

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

var kimiPageLocation = func() time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return *time.UTC
	}
	return *loc
}()

func kimiTotalResetDisplay(t time.Time) string {
	return t.In(&kimiPageLocation).Format("2006-01-02")
}

func kimiShortResetDisplay(t time.Time) string {
	return t.In(&kimiPageLocation).Format("01-02 15:04")
}

// FormatKimiPercent renders a decimal percentage up to 2 places with trailing
// zeros trimmed: 2.19 → "2.19%", 10.42 → "10.42%", 0 → "0%".
func FormatKimiPercent(p float64) string {
	if p == 0 {
		return "0%"
	}
	s := fmt.Sprintf("%.2f", p)
	s = trimTrailingZeros(s)
	return s + "%"
}

func trimTrailingZeros(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[:i]
		}
		if s[i] != '0' {
			return s[:i+1]
		}
	}
	return s
}
