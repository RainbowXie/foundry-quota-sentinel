package deepseek

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const deepseekBaseURL = "https://api.deepseek.com"

// DeepSeekAPIQuerier 负责使用开放平台的 sk- API Key 获取官方标准接口余额。
type DeepSeekAPIQuerier struct {
	APIKey string
	Client *http.Client
}

// DeepSeekQuerier 保留类型别名，保证向后兼容。
type DeepSeekQuerier = DeepSeekAPIQuerier

// NewDeepSeekQuerier 从环境变量 DEEPSEEK_API_KEY 创建 DeepSeekAPIQuerier。
func NewDeepSeekQuerier() *DeepSeekAPIQuerier {
	return &DeepSeekAPIQuerier{APIKey: os.Getenv("DEEPSEEK_API_KEY")}
}

// FetchBalance 调用 /user/balance 获取可用余额详情。
func (q *DeepSeekAPIQuerier) FetchBalance() (*BalanceData, error) {
	if q.APIKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY not set")
	}
	req, err := http.NewRequest("GET", deepseekBaseURL+"/user/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+q.APIKey)

	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
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
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if !raw.IsAvailable || len(raw.BalanceInfos) == 0 {
		return nil, fmt.Errorf("account not available")
	}
	info := raw.BalanceInfos[0]
	var tb, gb, ub float64
	fmt.Sscanf(info.TotalBalance, "%f", &tb)
	fmt.Sscanf(info.GrantedBalance, "%f", &gb)
	fmt.Sscanf(info.ToppedUpBalance, "%f", &ub)
	return &BalanceData{
		Currency:        info.Currency,
		TotalBalance:    tb,
		GrantedBalance:  gb,
		ToppedUpBalance: ub,
		FetchedAt:       time.Now(),
	}, nil
}
