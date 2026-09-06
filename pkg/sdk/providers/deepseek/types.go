package deepseek

import "time"

// BalanceData 是通过官方 API Key 查询获取的 DeepSeek 账户余额数据。
type BalanceData struct {
	Currency        string    `json:"currency"`
	TotalBalance    float64   `json:"total_balance"`
	GrantedBalance  float64   `json:"granted_balance"`
	ToppedUpBalance float64   `json:"topped_up_balance"`
	FetchedAt       time.Time `json:"fetched_at"`
}

// DeepSeekSummary 是通过网页端 Bearer Token 查询到的钱包与当月汇总模型。
type DeepSeekSummary struct {
	Currency        string  `json:"currency"`
	Balance         float64 `json:"balance"`          // normal_wallets 同币种余额累加
	TokenEstimation int64   `json:"token_estimation"` // 可用 token 估算总量
	MonthlyUsage    int64   `json:"monthly_usage"`    // 本月累计用量（token）
	CurrentToken    int64   `json:"current_token"`    // 赠送额度剩余
}

// DeepSeekDayUsage 记录某个模型在特定日期的各项 Token 消耗。
type DeepSeekDayUsage struct {
	Date      string `json:"date"`
	CacheHit  int64  `json:"cache_hit"`  // 输入命中缓存 token
	CacheMiss int64  `json:"cache_miss"` // 输入未命中缓存 token
	Output    int64  `json:"output"`     // 输出 token
	Total     int64  `json:"total"`      // 总量 = CacheHit + CacheMiss + Output
}

// DeepSeekModelUsage 聚合某个模型在当月的逐日用量明细。
type DeepSeekModelUsage struct {
	Model string             `json:"model"`
	Days  []DeepSeekDayUsage `json:"days"`
}
