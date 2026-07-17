package sidebar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestNewOllamaCDPConnectsUsingKnownDebugAddressWithoutPortFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/one",
			})
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]string{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = newOllamaCDP(context.Background(), parsed.Host)
	if err == nil || !strings.Contains(err.Error(), "浏览器尚未创建可用的登录页面") {
		t.Fatalf("newOllamaCDP() error = %v, want HTTP DevTools connection without DevToolsActivePort", err)
	}
}

func TestOllamaSessionCookieHeaderIncludesCloudflareCookies(t *testing.T) {
	got := ollamaSessionCookieHeader([]cdpCookie{
		{Name: "aid", Value: "tracking", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "cf_clearance", Value: "clearance", Domain: ".ollama.com", Secure: true, HTTPOnly: true},
		{Name: "__Secure-session", Value: "session-value", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "__Secure-session", Value: "other", Domain: "example.com", Secure: true, HTTPOnly: true},
	})
	if got != "__Secure-session=session-value; cf_clearance=clearance; aid=tracking" {
		t.Fatalf("header = %q, want Ollama session and Cloudflare cookies", got)
	}
}

func TestOllamaSessionCookieHeaderRejectsUnsafeValue(t *testing.T) {
	got := ollamaSessionCookieHeader([]cdpCookie{{
		Name:   "__Secure-session",
		Value:  "bad\r\nCookie: x",
		Domain: "ollama.com",
	}})
	if got != "" {
		t.Fatalf("header = %q, want empty unsafe cookie", got)
	}
}

func TestOllamaSessionCookieHeaderKeepsSessionWhenAncillaryCookieIsDuplicated(t *testing.T) {
	got := ollamaSessionCookieHeader([]cdpCookie{
		{Name: "__Secure-session", Value: "session", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "aid", Value: "first", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "aid", Value: "second", Domain: "ollama.com", Secure: true, HTTPOnly: true},
	})
	if got != "__Secure-session=session; aid=first" {
		t.Fatalf("header = %q, want session despite duplicated ancillary cookie", got)
	}
}

func TestCDPCookiesUseBrowserStorageTarget(t *testing.T) {
	var server *httptest.Server
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"webSocketDebuggerUrl": "ws" + server.URL[len("http"):] + "/devtools/browser/one",
			})
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]string{{
				"type":                 "page",
				"webSocketDebuggerUrl": "ws" + server.URL[len("http"):] + "/devtools/page/one",
			}})
		case "/devtools/page/one":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			// The page target is retained for account-page navigation. Cookie reads
			// must use the browser target below so redirects cannot stale the session.
			for {
				var request struct {
					ID     int64           `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := conn.ReadJSON(&request); err != nil {
					return
				}
				t.Errorf("page target received unexpected method %q", request.Method)
				return
			}
		case "/devtools/browser/one":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			var request struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Method != "Storage.getCookies" {
				t.Errorf("method = %q", request.Method)
				return
			}
			if err := conn.WriteJSON(map[string]any{"method": "Page.frameStartedLoading", "params": map[string]any{}}); err != nil {
				t.Error(err)
				return
			}
			_ = conn.WriteJSON(map[string]any{
				"id": request.ID,
				"result": map[string]any{"cookies": []map[string]any{{
					"name": "__Secure-session", "value": "test-session", "domain": "ollama.com", "secure": true, "httpOnly": true,
				}}},
			})
		}
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newOllamaCDP(context.Background(), parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	cookies, err := client.Cookies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 || cookies[0].Name != "__Secure-session" {
		t.Fatalf("cookies = %#v", cookies)
	}
}
