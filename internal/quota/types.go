package quota

import "time"

type QuotaUsage struct {
	Status       string `json:"status"`
	UsagePercent int    `json:"usage_percent"`
	ResetInSec   int    `json:"reset_in_sec"`
	ResetDisplay string `json:"reset_display"`
}

type QuotaData struct {
	Rolling   QuotaUsage  `json:"rolling"`
	Weekly    QuotaUsage  `json:"weekly"`
	Monthly   *QuotaUsage `json:"monthly,omitempty"`
	FetchedAt time.Time   `json:"fetched_at"`
}

type BalanceData struct {
	Currency        string  `json:"currency"`
	TotalBalance    float64 `json:"total_balance"`
	GrantedBalance  float64 `json:"granted_balance"`
	ToppedUpBalance float64 `json:"topped_up_balance"`
	FetchedAt       time.Time `json:"fetched_at"`
}

// DeepSeekSummary 是网页后台钱包/汇总（get_user_summary）的精简视图。
type DeepSeekSummary struct {
	Currency        string  `json:"currency"`
	Balance         float64 `json:"balance"`          // normal_wallets 同币种求和
	TokenEstimation int64   `json:"token_estimation"` // 可用 token 估算
	MonthlyUsage    int64   `json:"monthly_usage"`    // 本月用量（token）
	CurrentToken    int64   `json:"current_token"`    // 赠送额度
}

// DeepSeekDayUsage 是某模型某一天的 token 用量。
type DeepSeekDayUsage struct {
	Date      string `json:"date"`
	CacheHit  int64  `json:"cache_hit"`  // 输入命中缓存
	CacheMiss int64  `json:"cache_miss"` // 输入未命中缓存
	Output    int64  `json:"output"`     // 输出
	Total     int64  `json:"total"`      // = CacheHit + CacheMiss + Output
}

// DeepSeekModelUsage 是某个模型当月按天的用量（官网每个模型一张图）。
type DeepSeekModelUsage struct {
	Model string             `json:"model"`
	Days  []DeepSeekDayUsage `json:"days"`
}
