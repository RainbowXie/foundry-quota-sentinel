package commandcode

import "foundry-quota-sentinel/pkg/sdk/providers/opencode"

// QuotaUsage 表示单个时间窗口的配额状态。
type QuotaUsage = opencode.QuotaUsage

// QuotaData 是 CommandCode 的配额数据模型（与 OpenCode 共享结构）。
type QuotaData = opencode.QuotaData
