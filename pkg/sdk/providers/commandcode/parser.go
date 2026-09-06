package commandcode

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

var commandCodePlanCredits = map[string]int{
	"individual-go":     10,
	"individual-goat":   70,
	"individual-pro":    30,
	"individual-pro-v1": 80,
	"individual-max":    150,
	"individual-ultra":  300,
	"teams-pro":         40,
}

type commandCodeWindow struct {
	Used     *float64 `json:"used,omitempty"`
	Cap      *float64 `json:"cap,omitempty"`
	Exceeded *bool    `json:"exceeded,omitempty"`
	ResetAt  *int64   `json:"resetAt,omitempty"`
}

type commandCodeCreditsResponse struct {
	Credits *struct {
		MonthlyCredits *float64 `json:"monthlyCredits,omitempty"`
	} `json:"credits,omitempty"`
	WindowLimits *struct {
		Limited  *bool              `json:"limited,omitempty"`
		FiveHour *commandCodeWindow `json:"fiveHour,omitempty"`
		Weekly   *commandCodeWindow `json:"weekly,omitempty"`
	} `json:"windowLimits,omitempty"`
}

type commandCodeSubscription struct {
	Data *struct {
		PlanID           string `json:"planId,omitempty"`
		CurrentPeriodEnd string `json:"currentPeriodEnd,omitempty"`
	} `json:"data,omitempty"`
}

// ParseCommandCodeQuota 解析 credits 与 subscriptions 两段 JSON，返回原生 QuotaData。
func ParseCommandCodeQuota(creditsBody, subsBody string, now time.Time) (*QuotaData, error) {
	var credits commandCodeCreditsResponse
	if err := json.Unmarshal([]byte(creditsBody), &credits); err != nil {
		return nil, fmt.Errorf("commandcode credits 响应解析失败: %w", err)
	}
	if credits.WindowLimits == nil {
		return nil, fmt.Errorf("commandcode credits 响应缺少 windowLimits")
	}
	var subs commandCodeSubscription
	if err := json.Unmarshal([]byte(subsBody), &subs); err != nil {
		return nil, fmt.Errorf("commandcode subscriptions 响应解析失败: %w", err)
	}

	fiveHour, err := parseCommandCodeWindow(credits.WindowLimits.FiveHour, "5 小时", now)
	if err != nil {
		return nil, err
	}
	weekly, err := parseCommandCodeWindow(credits.WindowLimits.Weekly, "周", now)
	if err != nil {
		return nil, err
	}

	var monthly *QuotaUsage
	if credits.WindowLimits.Limited == nil || *credits.WindowLimits.Limited {
		m, err := parseCommandCodeMonthly(creditsBody, subsBody, now)
		if err != nil {
			return nil, err
		}
		monthly = m
	}

	return &QuotaData{
		Rolling:   fiveHour,
		Weekly:    weekly,
		Monthly:   monthly,
		FetchedAt: now,
	}, nil
}

func parseCommandCodeWindow(w *commandCodeWindow, name string, now time.Time) (QuotaUsage, error) {
	if w == nil || w.Used == nil || w.Cap == nil || w.ResetAt == nil {
		return QuotaUsage{}, fmt.Errorf("commandcode %s 用量窗口数据缺失", name)
	}
	used := *w.Used
	cap := *w.Cap
	if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
		return QuotaUsage{}, fmt.Errorf("commandcode %s 用量非法", name)
	}
	if math.IsNaN(cap) || math.IsInf(cap, 0) || cap <= 0 {
		return QuotaUsage{}, fmt.Errorf("commandcode %s 额度非法", name)
	}

	resetInSec := 0
	if *w.ResetAt > 0 {
		resetAt := time.UnixMilli(*w.ResetAt)
		resetInSec = int(resetAt.Sub(now).Seconds())
		if resetInSec < 0 {
			resetInSec = 0
		}
	}
	pct := clampPercent(used / cap * 100)
	return QuotaUsage{
		Status:       "active",
		UsagePercent: pct,
		ResetInSec:   resetInSec,
		ResetDisplay: formatDurationCompact(resetInSec),
	}, nil
}

func parseCommandCodeMonthly(creditsBody, subsBody string, now time.Time) (*QuotaUsage, error) {
	var subs commandCodeSubscription
	if err := json.Unmarshal([]byte(subsBody), &subs); err != nil {
		return nil, fmt.Errorf("commandcode subscriptions 响应解析失败: %w", err)
	}
	if subs.Data == nil || subs.Data.PlanID == "" {
		return nil, fmt.Errorf("commandcode 响应缺少计划标识")
	}
	var credits commandCodeCreditsResponse
	if err := json.Unmarshal([]byte(creditsBody), &credits); err != nil {
		return nil, fmt.Errorf("commandcode credits 响应解析失败: %w", err)
	}
	if credits.Credits == nil || credits.Credits.MonthlyCredits == nil {
		return nil, fmt.Errorf("commandcode 响应缺少月度额度")
	}
	cap, ok := commandCodePlanCredits[subs.Data.PlanID]
	if !ok {
		return nil, fmt.Errorf("commandcode 未知计划 %s", subs.Data.PlanID)
	}
	remaining := *credits.Credits.MonthlyCredits
	if math.IsNaN(remaining) || math.IsInf(remaining, 0) || remaining < 0 {
		return nil, fmt.Errorf("commandcode 月度剩余额度非法")
	}
	used := float64(cap) - remaining
	if used < 0 {
		used = float64(cap)
	}
	pct := clampPercent(used / float64(cap) * 100)
	resetInSec := commandCodeResetSeconds(subs.Data.CurrentPeriodEnd, now)
	return &QuotaUsage{
		Status:       "active",
		UsagePercent: pct,
		ResetInSec:   resetInSec,
		ResetDisplay: formatDurationCompact(resetInSec),
	}, nil
}

func clampPercent(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func commandCodeResetSeconds(iso string, now time.Time) int {
	if iso == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}
	s := int(t.Sub(now).Seconds())
	if s < 0 {
		return 0
	}
	return s
}

func formatDurationCompact(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	return fmt.Sprintf("%dd", seconds/86400)
}
