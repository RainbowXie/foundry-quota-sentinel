package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	ollamaBaseURL         = "https://ollama.com"
	ollamaUserAgent       = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	maxOllamaResponseSize = 1 << 20
)

var ollamaRequestTimeout = 15 * time.Second

// OllamaQuerier 负责通过带有登录态的 Cookie 访问 ollama.com/settings 获取用量配额。
type OllamaQuerier struct {
	Cookie    string
	UserAgent string
	BaseURL   string
	Client    *http.Client
}

// NewOllamaQuerier 从环境变量中读取 Cookie 构建 OllamaQuerier。
func NewOllamaQuerier() *OllamaQuerier {
	return &OllamaQuerier{
		Cookie: os.Getenv("OLLAMA_AUTH_COOKIE"),
	}
}

// FetchQuota 请求 /settings 页面并解析 Session 与 Weekly 额度。
func (q *OllamaQuerier) FetchQuota() (*QuotaData, error) {
	if q.Cookie == "" {
		return nil, fmt.Errorf("Ollama cookie not set")
	}

	baseURL := q.BaseURL
	if baseURL == "" {
		baseURL = ollamaBaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), ollamaRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/settings", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	userAgent := strings.TrimSpace(q.UserAgent)
	if userAgent == "" {
		userAgent = ollamaUserAgent
	}
	if strings.ContainsAny(userAgent, "\r\n") {
		return nil, fmt.Errorf("invalid Ollama user agent")
	}
	req.Header.Set("Cookie", q.Cookie)
	req.Header.Set("User-Agent", userAgent)

	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: ollamaRequestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("Ollama returned %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOllamaResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxOllamaResponseSize {
		return nil, fmt.Errorf("Ollama response exceeds %d bytes", maxOllamaResponseSize)
	}
	return ParseOllamaQuota(string(body))
}
