package browserauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event is a single protocol event delivered on a Client's Events channel.
type Event struct {
	Method string
	Params json.RawMessage
}

// cdpResponse is a JSON-RPC response from the DevTools endpoint.
type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Client multiplexes a single DevTools WebSocket connection: command
// responses (matched by id) go to pending callers, asynchronous events
// (matched by method) go to a buffered channel. Callers receive one Client
// per logical target; the same package owns the read loop.
type Client struct {
	conn      *websocket.Conn
	debugName string

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan cdpResponse

	events     chan Event
	eventsOnce sync.Once
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error

	// signalDone closes the shared done channel exactly once across the
	// whole Connection. Every read loop and every Close path funnels
	// through this function so a Close race cannot double-close the
	// channel.
	signalDone func()
}

// Connect dials a running DevTools endpoint, validates it is loopback, and
// returns a Client whose Browser and Page sub-connections share the same
// lifecycle. The context governs the discovery HTTP calls; the resulting
// Clients are not context-bound but honour ctx on every Call.
func Connect(ctx context.Context, debugAddress string) (*Connection, error) {
	if !isLoopbackDebugAddress(debugAddress) {
		return nil, fmt.Errorf("浏览器调试地址无效")
	}
	browserURL, pageURL, err := discoverDevTools(ctx, debugAddress)
	if err != nil {
		return nil, err
	}
	conn := &Connection{
		debugAddress: debugAddress,
		done:         make(chan struct{}),
	}
	conn.browser = &Client{
		debugName:  "browser",
		pending:    map[int64]chan cdpResponse{},
		events:     make(chan Event, 64),
		done:       conn.done,
		signalDone: conn.signalDone,
	}
	conn.page = &Client{
		debugName:  "page",
		pending:    map[int64]chan cdpResponse{},
		events:     make(chan Event, 64),
		done:       conn.done,
		signalDone: conn.signalDone,
	}
	if err := conn.dialBrowser(ctx, browserURL); err != nil {
		return nil, err
	}
	if err := conn.dialPage(ctx, pageURL); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// Connection owns the browser-wide and page-scoped DevTools Clients. Both
// Clients close together when the connection ends.
type Connection struct {
	debugAddress string
	browser      *Client
	page         *Client
	done         chan struct{}
	doneOnce     sync.Once
	closeOnce    sync.Once
	closeErr     error
}

// signalDone fires the Connection-level done once. It is the single
// point that closes the shared done channel; every read loop, Client
// close, and Connection close path calls this.
func (c *Connection) signalDone() {
	c.doneOnce.Do(func() { close(c.done) })
}

// Browser returns the browser-level Client (Storage.getCookies,
// Browser.getVersion, Network.setCookie, ...).
func (c *Connection) Browser() *Client { return c.browser }

// Page returns the page-level Client (Runtime.evaluate, Page.navigate,
// Page.addScriptToEvaluateOnNewDocument, Network.* events, ...).
func (c *Connection) Page() *Client { return c.page }

// DebugAddress returns the loopback host:port this connection targets.
func (c *Connection) DebugAddress() string { return c.debugAddress }

// Close shuts down both Clients. Pending Calls fail immediately; events
// stop being delivered. The shared done channel is closed exactly once
// via the Connection-owned signalDone closure. Every path that detects
// "this connection is over" (Connection.Close, Client.Close, read loop
// exit) funnels through the same closure, so a race cannot double-close
// done.
func (c *Connection) Close() error {
	c.closeOnce.Do(func() {
		c.signalDone()
		var first error
		if err := c.browser.shutdown(); err != nil && first == nil {
			first = err
		}
		if err := c.page.shutdown(); err != nil && first == nil {
			first = err
		}
		c.closeErr = first
	})
	return c.closeErr
}

func (c *Connection) dialBrowser(ctx context.Context, rawURL string) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		return fmt.Errorf("连接浏览器调试端点失败: %w", err)
	}
	c.browser.conn = conn
	go c.browser.readLoop()
	return nil
}

func (c *Connection) dialPage(ctx context.Context, rawURL string) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		return fmt.Errorf("连接页面调试端点失败: %w", err)
	}
	c.page.conn = conn
	go c.page.readLoop()
	return nil
}

func discoverDevTools(ctx context.Context, debugAddress string) (browserWS, pageWS string, err error) {
	browserWS, err = fetchBrowserEndpoint(ctx, debugAddress)
	if err != nil {
		return "", "", err
	}
	pageWS, err = fetchFirstPageEndpoint(ctx, debugAddress)
	if err != nil {
		return "", "", err
	}
	return browserWS, pageWS, nil
}

func fetchBrowserEndpoint(ctx context.Context, debugAddress string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+debugAddress+"/json/version", nil)
	if err != nil {
		return "", fmt.Errorf("创建浏览器 DevTools 版本请求失败: %w", err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("连接浏览器 DevTools 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("浏览器 DevTools 返回 %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取浏览器 DevTools 版本失败: %w", err)
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return "", fmt.Errorf("解析浏览器 DevTools 版本失败: %w", err)
	}
	if !isLoopbackWebSocketURL(version.WebSocketDebuggerURL) {
		return "", fmt.Errorf("浏览器调试端点无效")
	}
	return version.WebSocketDebuggerURL, nil
}

func fetchFirstPageEndpoint(ctx context.Context, debugAddress string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+debugAddress+"/json/list", nil)
	if err != nil {
		return "", fmt.Errorf("创建浏览器 DevTools 请求失败: %w", err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("连接浏览器 DevTools 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("浏览器 DevTools 返回 %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取浏览器 DevTools 失败: %w", err)
	}
	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(data, &targets); err != nil {
		return "", fmt.Errorf("解析浏览器 DevTools 目标失败: %w", err)
	}
	for _, target := range targets {
		if target.Type != "page" || !isLoopbackWebSocketURL(target.WebSocketDebuggerURL) {
			continue
		}
		return target.WebSocketDebuggerURL, nil
	}
	return "", fmt.Errorf("浏览器尚未创建可用的登录页面")
}

// Call sends a JSON-RPC command and returns the raw result. Concurrent
// calls on the same Client are serialised: the gorilla websocket.Conn is
// not safe for concurrent WriteJSON, so the call mutex covers both the
// id assignment and the write. The context bounds how long the call may
// wait; a cancelled context or a closed connection fails the call.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("浏览器调试连接已关闭")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan cdpResponse, 1)
	c.pending[id] = ch
	payload := map[string]any{"id": id, "method": method, "params": params}
	conn := c.conn
	writeErr := conn.WriteJSON(payload)
	c.mu.Unlock()

	if writeErr != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("调用浏览器 %s 失败: %w", method, writeErr)
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return nil, fmt.Errorf("浏览器 %s 失败（%d）: %s", method, response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("浏览器调试连接已关闭")
	case <-ctx.Done():
		return nil, fmt.Errorf("浏览器 %s 等待响应超时: %w", method, ctx.Err())
	}
}

// Events returns the buffered channel of asynchronous protocol events. The
// channel closes when the connection ends.
func (c *Client) Events() <-chan Event { return c.events }

// Close terminates the WebSocket and fails every pending call. Events
// stop being delivered; subsequent Calls return an error.
//
// The shared done channel is owned by the enclosing Connection. The
// read loop also signals done when the WebSocket itself dies
// unexpectedly, so the "server hung up" race is covered without
// double-close. This method only triggers shutdown; it never closes
// done.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.shutdown()
	})
	return c.closeErr
}

// shutdown releases the WebSocket and pending callers without closing the
// shared done channel. Connection.Close uses this so done is closed once.
// Per-call pending channels are left for the read loop to drain; closing
// them here would let a waiting Call receive the zero value, which looks
// indistinguishable from a successful empty response.
func (c *Client) shutdown() error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Lock()
	for id := range c.pending {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) readLoop() {
	defer c.eventsOnce.Do(func() { close(c.events) })
	defer c.signalDone()
	for {
		var raw json.RawMessage
		if err := c.conn.ReadJSON(&raw); err != nil {
			return
		}
		var envelope struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			continue
		}
		if envelope.Method != "" {
			// Non-blocking send: a slow consumer must not stall the read
			// loop and back up the WebSocket. Excess events are dropped.
			select {
			case c.events <- Event{Method: envelope.Method, Params: envelope.Params}:
			default:
			}
			continue
		}
		var response cdpResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[response.ID]
		c.mu.Unlock()
		if ok {
			select {
			case ch <- response:
			case <-c.done:
				return
			}
		}
	}
}

func isLoopbackDebugAddress(address string) bool {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return false
	}
	port, err := strconv.Atoi(rawPort)
	return err == nil && port > 0 && port <= 65535
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
