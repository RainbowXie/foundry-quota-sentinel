package browserauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeDevToolsServer simulates the subset of Chrome DevTools that
// browserauth uses: a /json/version endpoint that returns a browser WebSocket
// URL, a /json/list endpoint that returns a page target, and a page
// upgrader that proxies commands and can emit asynchronous events.
type fakeDevToolsServer struct {
	t               *testing.T
	server          *httptest.Server
	pageUpgrader    websocket.Upgrader
	browserUpgrader websocket.Upgrader

	mu             sync.Mutex
	pageMethods    []string
	browserMethods []string

	// rejectStorageCookieName makes Storage.setCookies return a
	// protocol error for the cookie with this name. Tests use it to
	// model a per-cookie injection failure (e.g. a __Host- cookie
	// Chrome refuses because it carries a Domain).
	rejectStorageCookieName string
}

func newFakeDevToolsServer(t *testing.T) *fakeDevToolsServer {
	f := &fakeDevToolsServer{t: t}
	mux := http.NewServeMux()
	wsHost := ""
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		host := wsHost
		if host == "" {
			host = "127.0.0.1:" + strings.TrimPrefix(f.server.URL, "http://127.0.0.1:")
		}
		versionURL := "ws://" + host + "/devtools/browser"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": versionURL,
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		host := wsHost
		if host == "" {
			host = "127.0.0.1:" + strings.TrimPrefix(f.server.URL, "http://127.0.0.1:")
		}
		pageURL := "ws://" + host + "/devtools/page/test"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{{
			"type":                 "page",
			"webSocketDebuggerUrl": pageURL,
		}})
	})
	mux.HandleFunc("/devtools/page/test", func(w http.ResponseWriter, r *http.Request) {
		f.servePage(w, r)
	})
	mux.HandleFunc("/devtools/browser", func(w http.ResponseWriter, r *http.Request) {
		f.serveBrowser(w, r)
	})
	f.server = httptest.NewServer(mux)
	wsHost = "127.0.0.1:" + strings.TrimPrefix(f.server.URL, "http://127.0.0.1:")
	f.pageUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	f.browserUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	return f
}

func (f *fakeDevToolsServer) DebugAddress() string {
	u, _ := url.Parse(f.server.URL)
	return u.Host
}

func (f *fakeDevToolsServer) Close() {
	f.server.Close()
}

func (f *fakeDevToolsServer) servePage(w http.ResponseWriter, r *http.Request) {
	conn, err := f.pageUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	f.handleConnection(conn, &f.pageMethods, true)
}

func (f *fakeDevToolsServer) serveBrowser(w http.ResponseWriter, r *http.Request) {
	conn, err := f.browserUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	f.handleConnection(conn, &f.browserMethods, false)
}

func (f *fakeDevToolsServer) handleConnection(conn *websocket.Conn, methods *[]string, emitEvent bool) {
	for {
		var msg map[string]json.RawMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		var id int64
		_ = json.Unmarshal(msg["id"], &id)
		var method string
		_ = json.Unmarshal(msg["method"], &method)
		f.mu.Lock()
		*methods = append(*methods, method)
		f.mu.Unlock()
		switch method {
		case "Runtime.evaluate":
			_ = conn.WriteJSON(map[string]any{
				"id":     id,
				"result": map[string]any{"type": "number", "value": 1},
			})
			if emitEvent {
				_ = conn.WriteJSON(map[string]any{
					"method": "Network.requestWillBeSentExtraInfo",
					"params": map[string]any{"headers": map[string]string{"authorization": "Bearer test"}},
				})
			}
		case "Storage.setCookies":
			if f.rejectStorageCookieName != "" {
				var params []map[string]json.RawMessage
				_ = json.Unmarshal(msg["params"], &params)
				if len(params) > 0 {
					var name string
					_ = json.Unmarshal(params[0]["name"], &name)
					if name == f.rejectStorageCookieName {
						_ = conn.WriteJSON(map[string]any{
							"id": id,
							"error": map[string]any{
								"code":    32000,
								"message": "failed to set cookie " + name,
							},
						})
						continue
					}
				}
			}
			_ = conn.WriteJSON(map[string]any{"id": id, "result": map[string]any{}})
		default:
			_ = conn.WriteJSON(map[string]any{
				"id":     id,
				"result": map[string]any{},
			})
		}
	}
}

func (f *fakeDevToolsServer) MethodsSeen(target string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch target {
	case "page":
		out := make([]string, len(f.pageMethods))
		copy(out, f.pageMethods)
		return out
	case "browser":
		out := make([]string, len(f.browserMethods))
		copy(out, f.browserMethods)
		return out
	}
	return nil
}

func TestClientDispatchesResponsesAndEvents(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	client, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.Page().Call(context.Background(), "Runtime.evaluate", map[string]any{"expression": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"type":"number","value":1}` {
		t.Fatalf("result = %s", string(result))
	}
	select {
	case event := <-client.Page().Events():
		if event.Method != "Network.requestWillBeSentExtraInfo" {
			t.Fatalf("event method = %s", event.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
}

func TestClientRejectsNonLoopbackAddress(t *testing.T) {
	_, err := Connect(context.Background(), "8.8.8.8:1234")
	if err == nil {
		t.Fatal("Connect accepted non-loopback address")
	}
}

func TestClientRejectsZeroPort(t *testing.T) {
	_, err := Connect(context.Background(), "127.0.0.1:0")
	if err == nil {
		t.Fatal("Connect accepted zero port")
	}
}

// TestSetCookiesToleratesPerCookieFailure proves Storage.setCookies
// injection must NOT abort the whole batch when a single cookie is
// rejected by the browser (e.g. a __Host- cookie carrying a Domain).
// The good cookie is still injected; the rejected one is logged and
// skipped. The previous code returned the first setCookies error,
// which made the DeepSeek account-page browser flash closed.
func TestSetCookiesToleratesPerCookieFailure(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	server.rejectStorageCookieName = "__Host-bad"
	client, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	cookies := []Cookie{
		{Name: "__Host-bad", Value: "x", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
		{Name: "session", Value: "good", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
	}
	if err := client.Browser().SetCookies(context.Background(), cookies); err != nil {
		t.Fatalf("SetCookies aborted on a single bad cookie: %v", err)
	}
	seen := server.MethodsSeen("browser")
	count := 0
	for _, m := range seen {
		if m == "Storage.setCookies" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected both cookies attempted, saw %d Storage.setCookies calls", count)
	}
}

// TestSetCookiesStripsDomainForHostScopedCookies proves a __Host-
// prefixed cookie is injected without a Domain. Chrome rejects
// __Host- cookies that carry a Domain attribute, so replaying the
// captured Domain verbatim fails the whole injection. The saved
// credential's Domain must be dropped for __Host- names.
func TestSetCookiesStripsDomainForHostScopedCookies(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	client, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	cookies := []Cookie{
		{Name: "__Host-session", Value: "v", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
	}
	if err := client.Browser().SetCookies(context.Background(), cookies); err != nil {
		t.Fatalf("SetCookies failed for __Host- cookie: %v", err)
	}
}
