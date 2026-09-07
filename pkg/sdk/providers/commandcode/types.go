package commandcode

import "time"

// QuotaUsage 表示单个时间窗口的配额状态。
// UsagePercent 采用 float64 保留精度，ResetInSec 为重置剩余秒数，ResetDisplay 为格式化后的紧凑时间展示。
type QuotaUsage struct {
	Status       string  `json:"status"`
	UsagePercent float64 `json:"usage_percent"`
	ResetInSec   int     `json:"reset_in_sec"`
	ResetDisplay string  `json:"reset_display"`
}

// QuotaData 是 CommandCode 的配额数据模型。
// 独立建模 5 小时滚动、周度以及月度额度，解除跨 Provider 依赖耦合。
type QuotaData struct {
	Rolling   QuotaUsage  `json:"rolling"`
	Weekly    QuotaUsage  `json:"weekly"`
	Monthly   *QuotaUsage `json:"monthly,omitempty"`
	Lapsed    bool        `json:"lapsed,omitempty"`
	FetchedAt time.Time   `json:"fetched_at"`
}
