package deepseek

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const deepseekWebBaseURL = "https://platform.deepseek.com/api/v0"

// DeepSeekWebQuerier 使用网页登录态捕获的 Bearer Token 访问 platform.deepseek.com 内部接口。
// 能够获取官方 OpenAPI 无法直接提供的富量化指标（钱包汇总、Token 估算、按天明细）。
type DeepSeekWebQuerier struct {
	Token  string
	Client *http.Client
}

func (q *DeepSeekWebQuerier) setWebHeaders(req *http.Request) {
	req.Header.Set("accept", "*/*")
	req.Header.Set("authorization", "Bearer "+q.Token)
	req.Header.Set("referer", "https://platform.deepseek.com/usage")
	req.Header.Set("user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("x-client-version", "1.0.0")
	req.Header.Set("x-app-version", "1.0.0")
	req.Header.Set("x-client-locale", "zh_CN")
	req.Header.Set("x-client-bundle-id", "com.deepseek.chat")
	req.Header.Set("x-client-timezone-offset", "28800")
}

func (q *DeepSeekWebQuerier) getBizData(url string) (json.RawMessage, error) {
	if q.Token == "" {
		return nil, fmt.Errorf("DeepSeek token 为空")
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	q.setWebHeaders(req)
	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("鉴权失败：token 可能已过期 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			BizData json.RawMessage `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("接口错误 code=%d: %s", env.Code, env.Msg)
	}
	return env.Data.BizData, nil
}

func parseNum(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return int64(f), true
	}
	return 0, false
}

func parseFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

// FetchSummary 调用 get_user_summary 获取钱包汇总与估算信息。
func (q *DeepSeekWebQuerier) FetchSummary() (*DeepSeekSummary, error) {
	raw, err := q.getBizData(deepseekWebBaseURL + "/users/get_user_summary")
	if err != nil {
		return nil, err
	}
	var bd struct {
		CurrentToken  json.Number `json:"current_token"`
		MonthlyUsage  any         `json:"monthly_usage"`
		NormalWallets []struct {
			Currency        string `json:"currency"`
			Balance         string `json:"balance"`
			TokenEstimation string `json:"token_estimation"`
		} `json:"normal_wallets"`
	}
	if err := json.Unmarshal(raw, &bd); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}
	s := &DeepSeekSummary{}
	if n, err := bd.CurrentToken.Int64(); err == nil {
		s.CurrentToken = n
	}
	if n, ok := parseNum(bd.MonthlyUsage); ok {
		s.MonthlyUsage = n
	}
	for _, w := range bd.NormalWallets {
		if s.Currency == "" {
			s.Currency = w.Currency
		}
		s.Balance += parseFloat(w.Balance)
		if n, ok := parseNum(w.TokenEstimation); ok {
			s.TokenEstimation += n
		}
	}
	return s, nil
}

// FetchUsage 调用 usage/amount 获取各模型按天消耗明细。
func (q *DeepSeekWebQuerier) FetchUsage(year, month int) ([]DeepSeekModelUsage, error) {
	url := fmt.Sprintf("%s/usage/amount?month=%d&year=%d", deepseekWebBaseURL, month, year)
	raw, err := q.getBizData(url)
	if err != nil {
		return nil, err
	}
	var bd struct {
		Total []struct {
			Model string `json:"model"`
		} `json:"total"`
		Days []struct {
			Date string `json:"date"`
			Data []struct {
				Model string `json:"model"`
				Usage []struct {
					Type   string `json:"type"`
					Amount string `json:"amount"`
				} `json:"usage"`
			} `json:"data"`
		} `json:"days"`
	}
	if err := json.Unmarshal(raw, &bd); err != nil {
		return nil, fmt.Errorf("decode usage: %w", err)
	}

	order := make([]string, 0, len(bd.Total))
	for _, t := range bd.Total {
		order = append(order, t.Model)
	}
	perModel := map[string][]DeepSeekDayUsage{}
	nonzero := map[string]bool{}
	for _, day := range bd.Days {
		for _, md := range day.Data {
			du := DeepSeekDayUsage{Date: day.Date}
			for _, u := range md.Usage {
				n, _ := parseNum(u.Amount)
				switch u.Type {
				case "PROMPT_CACHE_HIT_TOKEN":
					du.CacheHit = n
				case "PROMPT_CACHE_MISS_TOKEN":
					du.CacheMiss = n
				case "RESPONSE_TOKEN":
					du.Output = n
				}
			}
			du.Total = du.CacheHit + du.CacheMiss + du.Output
			if du.Total > 0 {
				nonzero[md.Model] = true
			}
			perModel[md.Model] = append(perModel[md.Model], du)
		}
	}

	out := make([]DeepSeekModelUsage, 0, len(order))
	for _, m := range order {
		if !nonzero[m] {
			continue
		}
		out = append(out, DeepSeekModelUsage{Model: m, Days: perModel[m]})
	}
	return out, nil
}
