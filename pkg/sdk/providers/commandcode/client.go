package commandcode

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	commandCodeAPIBase        = "https://api.commandcode.ai"
	commandCodeMaxResponseSize = 1 << 20
	commandCodeRequestTimeout  = 15 * time.Second
)

// CommandCodeQuerier 负责与 commandcode.ai API origin 交互获取配额信息。
type CommandCodeQuerier struct {
	Cookie   string
	UserName string
	Client   *http.Client
	BaseURL  string
}

// NewCommandCodeQuerier 从环境变量中读取凭据初始化 Querier。
func NewCommandCodeQuerier() *CommandCodeQuerier {
	return &CommandCodeQuerier{
		Cookie:   os.Getenv("COMMANDCODE_AUTH_COOKIE"),
		UserName: os.Getenv("COMMANDCODE_USER_NAME"),
	}
}

// FetchQuota 分别查询 /internal/billing/credits 与 /internal/billing/subscriptions 获取完整配额数据。
func (q *CommandCodeQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: commandCodeRequestTimeout}
	}
	creditsBody, err := q.getJSON(client, "/internal/billing/credits")
	if err != nil {
		return nil, err
	}
	subsBody, err := q.getJSON(client, "/internal/billing/subscriptions")
	if err != nil {
		return nil, err
	}
	return ParseCommandCodeQuota(creditsBody, subsBody, time.Now())
}

func (q *CommandCodeQuerier) validate() error {
	if q.Cookie == "" {
		return fmt.Errorf("commandcode cookie not set")
	}
	return nil
}

func (q *CommandCodeQuerier) getJSON(client *http.Client, path string) (string, error) {
	base := q.BaseURL
	if base == "" {
		base = commandCodeAPIBase
	}
	req, err := http.NewRequest("GET", base+path, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("cookie", q.Cookie)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("commandcode API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, commandCodeMaxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if len(body) > commandCodeMaxResponseSize {
		return "", fmt.Errorf("commandcode response exceeds %d bytes", commandCodeMaxResponseSize)
	}
	return string(body), nil
}
