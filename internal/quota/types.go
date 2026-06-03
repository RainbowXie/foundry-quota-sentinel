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
