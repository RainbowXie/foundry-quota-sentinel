package formatter

import (
	"strings"
	"testing"

	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

// TestFormatOpenCodeTableAbsentMonthlyIsNotUnlimited 守住报告口径：
// Monthly 为空只代表上游没有给出月窗口（v2 的 meters.month 可缺省），
// 表格不得据此替上游断言“无限额度”。
func TestFormatOpenCodeTableAbsentMonthlyIsNotUnlimited(t *testing.T) {
	data := &opencode.QuotaData{
		Rolling: opencode.QuotaUsage{Status: "active", UsagePercent: 20, ResetDisplay: "5m"},
		Weekly:  opencode.QuotaUsage{Status: "active", UsagePercent: 10, ResetDisplay: "7d"},
	}
	out := FormatOpenCodeTable(data)
	if !strings.Contains(out, "Monthly: 未提供/无月窗口") {
		t.Fatalf("absent monthly must be reported as not provided, got:\n%s", out)
	}
	if strings.Contains(out, "无限额度") {
		t.Fatalf("absent monthly must not be claimed unlimited, got:\n%s", out)
	}
}

func TestFormatOpenCodeTableRendersPresentMonthly(t *testing.T) {
	data := &opencode.QuotaData{
		Rolling: opencode.QuotaUsage{Status: "active", UsagePercent: 20, ResetDisplay: "5m"},
		Weekly:  opencode.QuotaUsage{Status: "active", UsagePercent: 10, ResetDisplay: "7d"},
		Monthly: &opencode.QuotaUsage{Status: "active", UsagePercent: 25, ResetDisplay: "30d"},
	}
	out := FormatOpenCodeTable(data)
	if !strings.Contains(out, "reset in 30d") {
		t.Fatalf("present monthly must render its reset display, got:\n%s", out)
	}
	if strings.Contains(out, "未提供/无月窗口") {
		t.Fatalf("present monthly must not be reported as absent, got:\n%s", out)
	}
}

// TestFormatOpenCodeTableLapsedSubscriptionRendersNotice 验证订阅失效时展示明确提示，
// 且不得输出假空指标或虚假的重置时间倒计时。
func TestFormatOpenCodeTableLapsedSubscriptionRendersNotice(t *testing.T) {
	data := &opencode.QuotaData{
		Rolling: opencode.QuotaUsage{Status: "unavailable", UsagePercent: 0, ResetDisplay: "0s"},
		Weekly:  opencode.QuotaUsage{Status: "unavailable", UsagePercent: 0, ResetDisplay: "0s"},
		Lapsed:  true,
	}
	out := FormatOpenCodeTable(data)
	if !strings.Contains(out, "OpenCode Go 订阅已失效") {
		t.Fatalf("lapsed subscription must render notice, got:\n%s", out)
	}
	if strings.Contains(out, "Rolling:") || strings.Contains(out, "Weekly:") {
		t.Fatalf("lapsed subscription must not render normal meter rows, got:\n%s", out)
	}
}

