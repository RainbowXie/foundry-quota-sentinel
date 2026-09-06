package opencode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	openCodeGoBaseURL   = "https://opencode.ai"
	openCodeGoServiceID = "c7389bd0e731f80f49593e5ee53835475f4e28594dd6bd83eb229bab753498cd"
	// openCodeGoMaxResponseSize 限制配额响应体积上限为 1MB。
	// 读取 maxSize+1 字节以严格检测超出边界的超大恶意或异常响应，避免被静默截断造成解析不完整。
	openCodeGoMaxResponseSize = 1 << 20
	openCodeGoRequestTimeout  = 15 * time.Second
)

// OpenCodeQuerier 负责与 opencode.ai 官方后端服务通信并获取原生配额数据。
type OpenCodeQuerier struct {
	Cookie      string
	WorkspaceID string
	// Client 允许注入自定义 http.Client（测试或特殊代理场景），为 nil 时采用默认超时客户端。
	Client *http.Client
}

// NewOpenCodeQuerier 从环境变量中读取凭据并创建 OpenCodeQuerier 实例。
func NewOpenCodeQuerier() *OpenCodeQuerier {
	return &OpenCodeQuerier{
		Cookie:      os.Getenv("OPENCODE_GO_AUTH_COOKIE"),
		WorkspaceID: os.Getenv("OPENCODE_GO_WORKSPACE_ID"),
	}
}

// FetchQuota 发送 RPC 请求并解析返回 OpenCode 的原生 QuotaData。
func (q *OpenCodeQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	args := buildRPCArgs(q.WorkspaceID)
	reqURL := fmt.Sprintf("%s/_server?id=%s&args=%s", openCodeGoBaseURL, openCodeGoServiceID, url.QueryEscape(args))
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("cookie", q.Cookie)
	req.Header.Set("x-server-id", openCodeGoServiceID)
	req.Header.Set("x-server-instance", "server-fn:3")
	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: openCodeGoRequestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 诊断信息严禁输出 body，防止泄露私人账号敏感凭据。
		return nil, fmt.Errorf("opencode API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, openCodeGoMaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > openCodeGoMaxResponseSize {
		return nil, fmt.Errorf("opencode quota response exceeds %d bytes", openCodeGoMaxResponseSize)
	}
	return ParseQuotaResponse(string(body))
}

func (q *OpenCodeQuerier) validate() error {
	if q.Cookie == "" {
		return fmt.Errorf("OPENCODE_GO_AUTH_COOKIE not set")
	}
	if q.WorkspaceID == "" {
		return fmt.Errorf("OPENCODE_GO_WORKSPACE_ID not set")
	}
	return nil
}

func buildRPCArgs(workspaceID string) string {
	data, _ := json.Marshal(map[string]any{
		"t": map[string]any{"t": 9, "i": 0, "l": 1, "a": []any{map[string]any{"t": 1, "s": workspaceID}}, "o": 0},
		"f": 31, "m": []any{},
	})
	return string(data)
}
