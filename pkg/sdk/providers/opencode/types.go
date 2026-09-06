package opencode

import "time"

// QuotaUsage 表示 OpenCode 单个时间窗口（如 rolling、weekly、monthly）的配额使用状态。
// UsagePercent 采用 float64 保留精度（上游接口自 2026-08-25 起支持小数百分比），
// ResetInSec 为重置剩余秒数，ResetDisplay 为格式化后的紧凑时间展示（如 "12m"、"3h"）。
type QuotaUsage struct {
	Status       string  `json:"status"`
	UsagePercent float64 `json:"usage_percent"`
	ResetInSec   int     `json:"reset_in_sec"`
	ResetDisplay string  `json:"reset_display"`
}

// QuotaData 是 OpenCode 的原生配额领域模型，忠实反映 upstream 的三个独立窗口与失效状态。
// 之所以不使用通用的统一快照，是因为 OpenCode 独有的 Rolling/Weekly/Monthly 三重时间窗口机制
// 无法无损压缩为单一的额度数值。
type QuotaData struct {
	Rolling   QuotaUsage  `json:"rolling"`
	Weekly    QuotaUsage  `json:"weekly"`
	Monthly   *QuotaUsage `json:"monthly,omitempty"`
	// Lapsed 标记当前账号的订阅已失效（OpenCode 配额 RPC 对失效订阅返回 null）。
	Lapsed    bool        `json:"lapsed,omitempty"`
	FetchedAt time.Time   `json:"fetched_at"`
}
