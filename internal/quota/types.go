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

type QuotaResult struct {
	Success      bool         `json:"success"`
	ProviderName string       `json:"provider_name"`
	Quota        *QuotaData   `json:"quota,omitempty"`
	Balance      *BalanceData `json:"balance,omitempty"`
	Error        string       `json:"error,omitempty"`
}

type TokenStats struct {
	Date         string  `json:"date"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost"`
	RequestCount int     `json:"request_count"`
}
