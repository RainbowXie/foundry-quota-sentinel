package formatter

import (
	"fmt"
	"strings"

	"foundry-quota-sentinel/pkg/sdk/providers/commandcode"
	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
	"foundry-quota-sentinel/pkg/sdk/providers/ollama"
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

// FormatOpenCodeTable 将 OpenCode 原生 QuotaData 格式化为终端展示表格。
func FormatOpenCodeTable(data *opencode.QuotaData) string {
	var sb strings.Builder
	sb.WriteString("\n========================================\n")
	sb.WriteString("  OpenCode Go 套餐额度\n")
	sb.WriteString("  (涵盖所有通过套餐使用的模型)\n")
	sb.WriteString("----------------------------------------\n")
	sb.WriteString(fmt.Sprintf("  Rolling: %s  reset in %s\n", ProgressBar(data.Rolling.UsagePercent, 18), data.Rolling.ResetDisplay))
	sb.WriteString(fmt.Sprintf("  Weekly:  %s  reset in %s\n", ProgressBar(data.Weekly.UsagePercent, 18), data.Weekly.ResetDisplay))
	if data.Monthly != nil {
		sb.WriteString(fmt.Sprintf("  Monthly: %s  reset in %s\n", ProgressBar(data.Monthly.UsagePercent, 18), data.Monthly.ResetDisplay))
	} else {
		sb.WriteString("  Monthly: 无限额度\n")
	}
	sb.WriteString("========================================\n")
	sb.WriteString(fmt.Sprintf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05")))
	return sb.String()
}

// FormatDeepSeekBalanceTable 将 DeepSeek 原生 BalanceData 格式化为终端展示文本。
func FormatDeepSeekBalanceTable(data *deepseek.BalanceData, sym string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\nDeepSeek 账户余额: %s%.2f (%s)\n", sym, data.TotalBalance, data.Currency))
	if data.GrantedBalance > 0 {
		sb.WriteString(fmt.Sprintf("  赠送余额:      %s%.2f\n", sym, data.GrantedBalance))
	}
	if data.ToppedUpBalance > 0 {
		sb.WriteString(fmt.Sprintf("  充值余额:      %s%.2f\n", sym, data.ToppedUpBalance))
	}
	sb.WriteString("\n(此为 DeepSeek 独立账户余额，与 OpenCode Go 套餐无关)\n")
	return sb.String()
}

// FormatKimiTable 将 Kimi 原生 KimiQuotaData 格式化为终端展示表格。
func FormatKimiTable(accountName string, data *kimi.KimiQuotaData) string {
	var sb strings.Builder
	sb.WriteString("\n========================================\n")
	sb.WriteString(fmt.Sprintf("  Kimi Code 账户 %q\n", accountName))
	sb.WriteString("----------------------------------------\n")
	sb.WriteString(fmt.Sprintf("  总使用量:   %s  (Kimi %s / Code %s)  reset %s\n",
		kimi.FormatKimiPercent(data.Total.TotalPercent),
		kimi.FormatKimiPercent(data.Total.KimiPercent),
		kimi.FormatKimiPercent(data.Total.CodePercent),
		data.Total.ResetDisplay))
	sb.WriteString(fmt.Sprintf("  5 小时用量 · Code: %s  reset %s\n",
		kimi.FormatKimiPercent(data.FiveHour.UsagePercent),
		data.FiveHour.ResetDisplay))
	sb.WriteString(fmt.Sprintf("  7 天用量 · Code:   %s  reset %s\n",
		kimi.FormatKimiPercent(data.SevenDay.UsagePercent),
		data.SevenDay.ResetDisplay))
	sb.WriteString("========================================\n")
	sb.WriteString(fmt.Sprintf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05")))
	return sb.String()
}

// FormatCommandCodeTable 将 CommandCode 原生 QuotaData 格式化为终端展示表格。
func FormatCommandCodeTable(accountName string, data *commandcode.QuotaData) string {
	var sb strings.Builder
	sb.WriteString("\n========================================\n")
	sb.WriteString(fmt.Sprintf("  CommandCode 账户 %q\n", accountName))
	sb.WriteString("----------------------------------------\n")
	sb.WriteString(fmt.Sprintf("  5 小时用量: %s  reset in %s\n", ProgressBar(data.Rolling.UsagePercent, 18), data.Rolling.ResetDisplay))
	sb.WriteString(fmt.Sprintf("  周用量:     %s  reset in %s\n", ProgressBar(data.Weekly.UsagePercent, 18), data.Weekly.ResetDisplay))
	if data.Monthly != nil {
		sb.WriteString(fmt.Sprintf("  月用量:     %s  reset in %s\n", ProgressBar(data.Monthly.UsagePercent, 18), data.Monthly.ResetDisplay))
	} else {
		sb.WriteString("  月用量:     无限额度\n")
	}
	sb.WriteString("========================================\n")
	sb.WriteString(fmt.Sprintf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05")))
	return sb.String()
}

// FormatOllamaTable 将 Ollama 原生 QuotaData 格式化为终端展示表格。
func FormatOllamaTable(accountName string, data *ollama.QuotaData) string {
	var sb strings.Builder
	sb.WriteString("\n========================================\n")
	sb.WriteString(fmt.Sprintf("  Ollama 账户 %q\n", accountName))
	sb.WriteString("----------------------------------------\n")
	sb.WriteString(fmt.Sprintf("  Session: %s  reset in %s\n", ProgressBar(data.Rolling.UsagePercent, 18), data.Rolling.ResetDisplay))
	sb.WriteString(fmt.Sprintf("  Weekly:  %s  reset in %s\n", ProgressBar(data.Weekly.UsagePercent, 18), data.Weekly.ResetDisplay))
	sb.WriteString("========================================\n")
	sb.WriteString(fmt.Sprintf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05")))
	return sb.String()
}
