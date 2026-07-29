package quota

import (
	"encoding/json"
	"fmt"
	"time"

	"foundry-quota-sentinel/internal/formatter"
)

// KimiQuotaData is the provider-specific Kimi Code quota aggregate. Kimi's
// console shows two INDEPENDENTLY resetting meters — weekly usage (7d window)
// and a frequency limit (5h window) — that do not map onto QuotaData's
// rolling/weekly/monthly allowance shape. Reusing QuotaData.Rolling for the
// frequency limit would make API/CLI labels incorrect, so the two meters live
// here as QuotaUsage leaves while QuotaData's JSON semantics stay unchanged.
type KimiQuotaData struct {
	Weekly    QuotaUsage `json:"weekly"`
	RateLimit QuotaUsage `json:"rate_limit"`
	FetchedAt time.Time  `json:"fetched_at"`
}

// Kimi uses Buf Connect (connect-es) gRPC-Web over JSON. OBSERVED contract
// (captured from a real authenticated session): success = HTTP 200 + a body
// with NO top-level "code" string; failure = non-2xx with
// {"code":"unauthenticated",...}. ParseKimiQuota rejects a body carrying a
// non-empty "code" string (the Connect error envelope) and accepts a body
// with no "code" that parses into the two meters.

// kimiRateLimit is one meter as it appears in the GetSubscriptionStats
// response (OBSERVED field names). ratio is a 0..1 usage ratio (absent at 0%
// usage → treated as 0); resetTime is an absolute ISO-8601 timestamp
// (nanosecond precision, e.g. 2026-08-04T15:58:03.138613843Z); enabled flags
// the window.
type kimiRateLimit struct {
	Ratio     *float64 `json:"ratio,omitempty"`
	Enabled   *bool    `json:"enabled,omitempty"`
	ResetTime string   `json:"resetTime,omitempty"`
}

// kimiStatsResponse is the JSON envelope of a successful GetSubscriptionStats
// response (OBSERVED top-level keys: ratelimitCode5h, ratelimitCode7d,
// subscriptionBalance). Only the two rate-limit objects are required for the
// two-meter display; subscriptionBalance is account/wallet metadata, ignored
// by the parser.
type kimiStatsResponse struct {
	RatelimitCode5h     *kimiRateLimit  `json:"ratelimitCode5h,omitempty"`
	RatelimitCode7d     *kimiRateLimit  `json:"ratelimitCode7d,omitempty"`
	SubscriptionBalance json.RawMessage `json:"subscriptionBalance,omitempty"`
}

// kimiConnectError is the Connect failure envelope: a non-empty "code" string
// (e.g. "unauthenticated") + details. ParseKimiQuota rejects it.
type kimiConnectError struct {
	Code string `json:"code"`
}

// ParseKimiQuota parses a sanitized Kimi GetSubscriptionStats response body
// into the two-meter aggregate. It requires:
//   - a well-formed JSON body;
//   - NO top-level "code" string (a Connect error envelope is a failure);
//   - both ratelimitCode7d (weekly) and ratelimitCode5h (frequency limit)
//     present;
//   - each meter's ratio (when present) mapping to 0..100;
//   - each meter's resetTime present, parseable, and in the FUTURE.
//
// The weekly percentage is round(ratelimitCode7d.ratio*100); the frequency
// percentage is round(ratelimitCode5h.ratio*100), with an ABSENT ratio treated
// as 0% (the real response omits ratio at 0% usage). resetTime is an absolute
// ISO-8601 timestamp; the reset countdown is resetTime − now in seconds. Past,
// negative, missing, or unparseable resets are rejected. The two resets are
// retained INDEPENDENTLY — one meter's reset is never reused for the other.
func ParseKimiQuota(body string) (*KimiQuotaData, error) {
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
		return nil, fmt.Errorf("Kimi 响应缺少本周用量 meter (ratelimitCode7d)")
	}
	if raw.RatelimitCode5h == nil {
		return nil, fmt.Errorf("Kimi 响应缺少频率限制 meter (ratelimitCode5h)")
	}
	weekly, err := parseKimiMeter(raw.RatelimitCode7d, "本周用量")
	if err != nil {
		return nil, err
	}
	rateLimit, err := parseKimiMeter(raw.RatelimitCode5h, "频率限制")
	if err != nil {
		return nil, err
	}
	return &KimiQuotaData{
		Weekly:    weekly,
		RateLimit: rateLimit,
		FetchedAt: time.Now(),
	}, nil
}

// parseKimiMeter turns one rate-limit object into a QuotaUsage. meterName
// labels which meter failed. The percentage is round(ratio*100) with an absent
// ratio treated as 0%. The reset is resetTime − now (absolute ISO timestamp →
// seconds); a missing/past/unparseable resetTime is rejected.
func parseKimiMeter(raw *kimiRateLimit, meterName string) (QuotaUsage, error) {
	percent := 0
	if raw.Ratio != nil {
		r := *raw.Ratio
		// ratio is a 0..1 usage ratio; an out-of-range value is a malformed/
		// unsupported response, never silently clamped. Kimi will never
		// legitimately emit <0 or >1, so reject rather than clamp.
		if r < 0 || r > 1 {
			return QuotaUsage{}, fmt.Errorf("Kimi %s 用量比例 %.4f 越界（应为 0..1）", meterName, r)
		}
		percent = int(r*100 + 0.5)
	}
	resetSec, err := kimiResetSeconds(raw.ResetTime)
	if err != nil {
		return QuotaUsage{}, fmt.Errorf("Kimi %s: %w", meterName, err)
	}
	if resetSec <= 0 {
		return QuotaUsage{}, fmt.Errorf("Kimi %s 重置时间已过期", meterName)
	}
	return QuotaUsage{
		Status:       "active",
		UsagePercent: percent,
		ResetInSec:   resetSec,
		ResetDisplay: formatter.FormatDurationCompact(resetSec),
	}, nil
}

// kimiResetSeconds computes the reset countdown from an absolute ISO-8601
// resetTime: seconds until resetTime, relative to now. A missing, unparseable,
// or past timestamp is an error (the caller also rejects <= 0).
func kimiResetSeconds(resetTime string) (int, error) {
	if resetTime == "" {
		return 0, fmt.Errorf("重置时间缺失")
	}
	// Go's time.RFC3339Nano handles the nanosecond-precision ISO timestamps
	// Kimi emits (e.g. 2026-08-04T15:58:03.138613843Z).
	t, err := time.Parse(time.RFC3339Nano, resetTime)
	if err != nil {
		// Fall back to the looser layout for any trailing fractional variant.
		t, err = time.Parse(time.RFC3339, resetTime)
		if err != nil {
			return 0, fmt.Errorf("重置时间无法解析: %w", err)
		}
	}
	secs := int(time.Until(t).Seconds())
	if secs <= 0 {
		return 0, fmt.Errorf("重置时间已过期")
	}
	return secs, nil
}
