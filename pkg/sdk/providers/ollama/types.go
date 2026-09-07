package ollama

import "time"

// QuotaUsage 表示单个配额用量指标。
// UsagePercent 采用 float64 保留精度，ResetInSec 为重置剩余秒数，ResetDisplay 为格式化后的紧凑时间展示。
type QuotaUsage struct {
	Status       string  `json:"status"`
	UsagePercent float64 `json:"usage_percent"`
	ResetInSec   int     `json:"reset_in_sec"`
	ResetDisplay string  `json:"reset_display"`
}

// QuotaData 是 Ollama 账户的配额领域模型。
// Ollama 具有 Session（会话窗口）与 Weekly（周度窗口）两个核心指标，并兼容标准滚动展示模型。
type QuotaData struct {
	Rolling   QuotaUsage  `json:"rolling"`
	Weekly    QuotaUsage  `json:"weekly"`
	Monthly   *QuotaUsage `json:"monthly,omitempty"`
	Lapsed    bool        `json:"lapsed,omitempty"`
	FetchedAt time.Time   `json:"fetched_at"`
}
