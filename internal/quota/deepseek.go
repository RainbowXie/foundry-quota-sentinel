package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const deepseekBaseURL = "https://api.deepseek.com"

type DeepSeekQuerier struct{ APIKey string }

func NewDeepSeekQuerier() *DeepSeekQuerier {
	return &DeepSeekQuerier{APIKey: os.Getenv("DEEPSEEK_API_KEY")}
}

func (q *DeepSeekQuerier) FetchBalance() (*BalanceData, error) {
	if q.APIKey == "" { return nil, fmt.Errorf("DEEPSEEK_API_KEY not set") }
	req, _ := http.NewRequest("GET", deepseekBaseURL+"/user/balance", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+q.APIKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return nil, fmt.Errorf("http request: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}
	var raw struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil { return nil, fmt.Errorf("decode: %w", err) }
	if !raw.IsAvailable || len(raw.BalanceInfos) == 0 { return nil, fmt.Errorf("account not available") }
	info := raw.BalanceInfos[0]
	var tb, gb, ub float64
	fmt.Sscanf(info.TotalBalance, "%f", &tb)
	fmt.Sscanf(info.GrantedBalance, "%f", &gb)
	fmt.Sscanf(info.ToppedUpBalance, "%f", &ub)
	return &BalanceData{Currency: info.Currency, TotalBalance: tb, GrantedBalance: gb, ToppedUpBalance: ub, FetchedAt: time.Now()}, nil
}
