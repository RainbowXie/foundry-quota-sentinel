package ollama

import "foundry-quota-sentinel/pkg/sdk/providers/opencode"

// QuotaUsage 表示单个配额用量指标。
type QuotaUsage = opencode.QuotaUsage

// QuotaData 是 Ollama 账户的配额领域模型。
type QuotaData = opencode.QuotaData
