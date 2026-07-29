package quota

import (
	"encoding/json"
	"fmt"
	"time"

	"foundry-quota-sentinel/internal/formatter"
)

// KimiQuotaData is the provider-specific Kimi Code quota aggregate. Kimi's
// console shows two INDEPENDENTLY resetting meters — weekly usage and a
// frequency-limit — that do not map onto QuotaData's rolling/weekly/monthly
// allowance shape. Reusing QuotaData.Rolling for the frequency limit would
// make API/CLI labels incorrect and create silent semantic debt, so the two
// meters live here as QuotaUsage leaves while QuotaData's JSON semantics
// stay unchanged for the other providers.
type KimiQuotaData struct {
	Weekly    QuotaUsage `json:"weekly"`
	RateLimit QuotaUsage `json:"rate_limit"`
	FetchedAt time.Time  `json:"fetched_at"`
}

// Kimi uses Buf Connect (connect-es) gRPC-Web over JSON, NOT the DeepSeek
// {code,msg,data} REST envelope. OBSERVED contract:
//   - Success: HTTP 200 (checked by the querier) + a response body that
//     parses as the protobuf-JSON message with NO top-level "code" string.
//   - Failure: a non-2xx HTTP status (querier) OR a body carrying a Connect
//     error envelope {"code":"unauthenticated"|...,"details":[...]}.
//
// So ParseKimiQuota rejects any body with a non-empty top-level "code" string
// (the Connect failure shape) and accepts a body with no "code" field that
// parses into the two meters. A 2xx carrying a Connect error code is a
// business/transport failure, not quota success.

// kimiMeterRaw is the JSON shape of one meter inside the Kimi
// GetSubscriptionStats response. OBSERVED proto field names (decoded from
// the FileDescriptorProto): amountUsedRatio/kimiCodeUsedRatio carry the
// usage ratio (0..1), amountLeft/amount carry remaining/total, expireTime is
// a reset timestamp. The console derives usagePercentage client-side via
// ratioToPercentage; the parser mirrors that. Field tags are EVIDENCE-GATED
// against the exact 200 body layout — update only the tags when the real
// body is captured, not the parser's contract (the tests pin the parsed
// KimiQuotaData). reset may arrive as reset_seconds (a duration),
// reset_unix (a timestamp), or reset_display (a localized "6d 12h 20min"
// countdown string); all normalize to seconds at parse time.
type kimiMeterRaw struct {
	UsedRatio    *float64 `json:"amountUsedRatio,omitempty"`
	UsedPercent  *int     `json:"usedPercent,omitempty"`
	ResetSeconds *int     `json:"reset_seconds,omitempty"`
	ResetUnix    *int64   `json:"reset_unix,omitempty"`
	ResetDisplay string   `json:"reset_display,omitempty"`
}

// kimiStatsResponseRaw is the JSON envelope of a successful Kimi
// GetSubscriptionStats response (Connect-JSON, no "code" on success). The
// meter field names are EVIDENCE-GATED; the success discriminator (no "code"
// string + both meters present) is the tested contract.
type kimiStatsResponseRaw struct {
	Weekly    *kimiMeterRaw `json:"weekly,omitempty"`
	RateLimit *kimiMeterRaw `json:"rate_limit,omitempty"`
}

// kimiConnectErrorRaw is the Connect failure envelope: a non-empty "code"
// string (e.g. "unauthenticated") + details. ParseKimiQuota rejects it.
type kimiConnectErrorRaw struct {
	Code string `json:"code"`
	Msg  string `json:"message,omitempty"`
}

// ParseKimiQuota parses a sanitized Kimi GetSubscriptionStats response body
// into the two-meter aggregate. It requires:
//   - a well-formed JSON body;
//   - NO top-level "code" string (a Connect error envelope is a failure —
//     OBSERVED: success has no "code" field, failure carries
//     {"code":"unauthenticated",...});
//   - both the weekly and the rate-limit meter present;
//   - each meter's percentage in 0..100;
//   - each meter's reset present, positive, and parseable to seconds.
//
// A Connect error inside a 2xx (non-empty "code"), a missing meter, an
// invalid percentage, or a missing/invalid reset is rejected. The two reset
// values are parsed and retained INDEPENDENTLY — one meter's reset is never
// reused for the other. Reset values may arrive as seconds, a unix timestamp,
// or a localized "6d 12h 20min" countdown string; all normalize to seconds at
// fetch time. Past, negative, missing, or unparseable resets are rejected.
func ParseKimiQuota(body string) (*KimiQuotaData, error) {
	if body == "" {
		return nil, fmt.Errorf("Kimi 响应为空")
	}
	// A Connect error envelope (non-empty "code" string) is a failure even
	// inside a 2xx. Decode the error shape first; an absent/empty code means
	// success-proceed to the stats envelope.
	var cerr kimiConnectErrorRaw
	if err := json.Unmarshal([]byte(body), &cerr); err == nil && cerr.Code != "" {
		return nil, fmt.Errorf("Kimi 接口业务失败: %s", cerr.Code)
	}
	var raw kimiStatsResponseRaw
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("Kimi 响应解析失败: %w", err)
	}
	if raw.Weekly == nil {
		return nil, fmt.Errorf("Kimi 响应缺少本周用量 meter")
	}
	if raw.RateLimit == nil {
		return nil, fmt.Errorf("Kimi 响应缺少频率限制 meter")
	}
	weekly, err := parseKimiMeter(raw.Weekly, "本周用量")
	if err != nil {
		return nil, err
	}
	rateLimit, err := parseKimiMeter(raw.RateLimit, "频率限制")
	if err != nil {
		return nil, err
	}
	return &KimiQuotaData{
		Weekly:    weekly,
		RateLimit: rateLimit,
		FetchedAt: time.Now(),
	}, nil
}

// parseKimiMeter turns one raw meter into a QuotaUsage with a normalized
// reset. meterName labels which meter failed so the error identifies it.
// The percentage comes from usedPercent if present, else from usedRatio
// (0..1 → 0..100, rounded), mirroring the console's ratioToPercentage.
func parseKimiMeter(raw *kimiMeterRaw, meterName string) (QuotaUsage, error) {
	percent, ok := kimiPercent(raw)
	if !ok || percent < 0 || percent > 100 {
		return QuotaUsage{}, fmt.Errorf("Kimi %s 用量百分比无效", meterName)
	}
	resetSec, err := kimiResetSeconds(raw)
	if err != nil {
		return QuotaUsage{}, fmt.Errorf("Kimi %s: %w", meterName, err)
	}
	if resetSec <= 0 {
		return QuotaUsage{}, fmt.Errorf("Kimi %s 重置时间无效或已过期", meterName)
	}
	return QuotaUsage{
		Status:       "active",
		UsagePercent: percent,
		ResetInSec:   resetSec,
		ResetDisplay: formatter.FormatDurationCompact(resetSec),
	}, nil
}

// kimiPercent resolves the meter's usage percentage. usedPercent (an explicit
// integer) wins; otherwise usedRatio (a 0..1 float) is scaled to 0..100 and
// rounded, mirroring the console's ratioToPercentage.
func kimiPercent(raw *kimiMeterRaw) (int, bool) {
	if raw.UsedPercent != nil {
		return *raw.UsedPercent, true
	}
	if raw.UsedRatio != nil {
		r := *raw.UsedRatio
		if r < 0 || r > 1 {
			return int(r * 100), true // let the 0..100 range check reject it
		}
		return int(r*100 + 0.5), true
	}
	return 0, false
}

// kimiResetSeconds normalizes a meter's reset to seconds. Priority:
// reset_seconds (duration) → reset_unix (absolute timestamp → relative now)
// → reset_display (localized countdown string). A missing or unparseable
// reset is an error; a past timestamp or non-positive duration is rejected
// by the caller via the <= 0 check.
func kimiResetSeconds(raw *kimiMeterRaw) (int, error) {
	if raw.ResetSeconds != nil {
		return *raw.ResetSeconds, nil
	}
	if raw.ResetUnix != nil {
		secs := int(time.Until(time.Unix(*raw.ResetUnix, 0)).Seconds())
		return secs, nil
	}
	if raw.ResetDisplay != "" {
		secs, err := parseKimiCountdown(raw.ResetDisplay)
		if err != nil {
			return 0, err
		}
		return secs, nil
	}
	return 0, fmt.Errorf("重置时间缺失")
}

// parseKimiCountdown parses a localized countdown string like "6d 12h 20min"
// or "3h 20min" into seconds. Each component is <number><unit> where unit is
// d/h/min/s (case-insensitive, min not m to avoid ambiguity). Components may
// appear in any order but each unit at most once. An unparseable component
// is an error.
func parseKimiCountdown(s string) (int, error) {
	var total int
	seen := map[string]bool{}
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == ',' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return 0, fmt.Errorf("重置显示值 %q 缺少数字", s)
		}
		var n int
		for _, c := range s[start:i] {
			n = n*10 + int(c-'0')
		}
		for i < len(s) && s[i] == ' ' {
			i++
		}
		unitStart := i
		for i < len(s) && !isDigitOrSpace(s[i]) && s[i] != ',' {
			i++
		}
		unit := lower(s[unitStart:i])
		if seen[unit] {
			return 0, fmt.Errorf("重置显示值 %q 单位 %q 重复", s, unit)
		}
		seen[unit] = true
		switch unit {
		case "d":
			total += n * 86400
		case "h":
			total += n * 3600
		case "min":
			total += n * 60
		case "s":
			total += n
		default:
			return 0, fmt.Errorf("重置显示值 %q 含未知单位 %q", s, unit)
		}
	}
	if total <= 0 {
		return 0, fmt.Errorf("重置显示值 %q 无法解析为正数", s)
	}
	return total, nil
}

func isDigitOrSpace(c byte) bool {
	return (c >= '0' && c <= '9') || c == ' '
}

func lower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
