package sidebar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type cdpCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

type ollamaCDPClient struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	nextID int64
}

type cdpTarget struct {
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newOllamaCDP(ctx context.Context, activePortPath string) (*ollamaCDPClient, error) {
	port, err := readOllamaDevToolsPort(activePortPath)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json/list", nil)
	if err != nil {
		return nil, fmt.Errorf("创建浏览器 DevTools 请求失败: %w", err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("连接浏览器 DevTools 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("浏览器 DevTools 返回 %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取浏览器 DevTools 失败: %w", err)
	}
	var targets []cdpTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("解析浏览器 DevTools 目标失败: %w", err)
	}
	for _, target := range targets {
		if target.Type != "page" || !isLoopbackWebSocketURL(target.WebSocketDebuggerURL) {
			continue
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, target.WebSocketDebuggerURL, nil)
		if err != nil {
			return nil, fmt.Errorf("连接浏览器调试页面失败: %w", err)
		}
		return &ollamaCDPClient{conn: conn}, nil
	}
	return nil, fmt.Errorf("浏览器尚未创建可用的登录页面")
}

func readOllamaDevToolsPort(activePortPath string) (string, error) {
	data, err := os.ReadFile(activePortPath)
	if err != nil {
		return "", fmt.Errorf("读取浏览器调试端口失败: %w", err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("浏览器调试端口无效")
	}
	return strconv.Itoa(port), nil
}

func isLoopbackWebSocketURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "ws" {
		return false
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

func (c *ollamaCDPClient) Cookies(ctx context.Context) ([]cdpCookie, error) {
	var result struct {
		Cookies []cdpCookie `json:"cookies"`
	}
	if err := c.call(ctx, "Network.getCookies", map[string]any{
		"urls": []string{"https://ollama.com/"},
	}, &result); err != nil {
		return nil, err
	}
	return result.Cookies, nil
}

func (c *ollamaCDPClient) SetSessionCookie(ctx context.Context, value string) error {
	if !isSafeOllamaCookieValue(value) {
		return fmt.Errorf("Ollama 登录状态无效")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := c.call(ctx, "Network.setCookie", map[string]any{
		"url":      "https://ollama.com/",
		"name":     "__Secure-session",
		"value":    value,
		"path":     "/",
		"secure":   true,
		"httpOnly": true,
	}, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("浏览器拒绝设置 Ollama 登录状态")
	}
	return nil
}

func (c *ollamaCDPClient) Navigate(ctx context.Context, pageURL string) error {
	if !isOllamaPageURL(pageURL) {
		return fmt.Errorf("Ollama 账户页地址无效")
	}
	return c.call(ctx, "Page.navigate", map[string]any{"url": pageURL}, nil)
}

func (c *ollamaCDPClient) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok {
		deadline = contextDeadline
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("设置浏览器写入超时失败: %w", err)
	}
	if err := c.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return fmt.Errorf("调用浏览器 %s 失败: %w", method, err)
	}
	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return fmt.Errorf("设置浏览器读取超时失败: %w", err)
		}
		var response cdpResponse
		if err := c.conn.ReadJSON(&response); err != nil {
			return fmt.Errorf("读取浏览器 %s 响应失败: %w", method, err)
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("浏览器 %s 失败（%d）: %s", method, response.Error.Code, response.Error.Message)
		}
		if result != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return fmt.Errorf("解析浏览器 %s 响应失败: %w", method, err)
			}
		}
		return nil
	}
}

func (c *ollamaCDPClient) Close() error {
	return c.conn.Close()
}

func ollamaSessionCookieHeader(cookies []cdpCookie) string {
	var value string
	for _, cookie := range cookies {
		if cookie.Name != "__Secure-session" || !cookie.Secure || !cookie.HTTPOnly || !isOllamaCookieDomain(cookie.Domain) || !isSafeOllamaCookieValue(cookie.Value) {
			continue
		}
		if value != "" {
			return ""
		}
		value = cookie.Value
	}
	if value == "" {
		return ""
	}
	return "__Secure-session=" + value
}

func isOllamaCookieDomain(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	return domain == "ollama.com" || strings.HasSuffix(domain, ".ollama.com")
}

func isSafeOllamaCookieValue(value string) bool {
	return value != "" && !strings.ContainsAny(value, ";\r\n")
}

func isOllamaPageURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && u.Scheme == "https" && isOllamaCookieDomain(u.Hostname())
}
