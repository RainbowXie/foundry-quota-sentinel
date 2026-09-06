package quota

import (
	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

type QuotaUsage = opencode.QuotaUsage
type QuotaData = opencode.QuotaData
type BalanceData = deepseek.BalanceData
type DeepSeekSummary = deepseek.DeepSeekSummary
type DeepSeekDayUsage = deepseek.DeepSeekDayUsage
type DeepSeekModelUsage = deepseek.DeepSeekModelUsage
