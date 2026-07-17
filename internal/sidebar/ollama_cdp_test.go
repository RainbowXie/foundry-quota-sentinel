package sidebar

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gorilla/websocket"
)

func TestOllamaSessionCookieHeaderAcceptsOnlyOllamaSession(t *testing.T) {
	got := ollamaSessionCookieHeader([]cdpCookie{
		{Name: "aid", Value: "tracking", Domain: "ollama.com"},
		{Name: "__Secure-session", Value: "session-value", Domain: "ollama.com", Secure: true, HTTPOnly: true},
		{Name: "__Secure-session", Value: "other", Domain: "example.com", Secure: true, HTTPOnly: true},
	})
	if got != "__Secure-session=session-value" {
		t.Fatalf("header = %q, want only Ollama session", got)
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

func TestCDPCookiesRequestsOnlyOllamaURL(t *testing.T) {
	var server *httptest.Server
	var requestedURLs []string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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
			var request struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Method != "Network.getCookies" {
				t.Errorf("method = %q", request.Method)
				return
			}
			var params struct {
				URLs []string `json:"urls"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Error(err)
				return
			}
			requestedURLs = params.URLs
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
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	portFile := filepath.Join(t.TempDir(), "DevToolsActivePort")
	if err := os.WriteFile(portFile, []byte(port+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := newOllamaCDP(context.Background(), portFile)
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
	if want := []string{"https://ollama.com/"}; !reflect.DeepEqual(requestedURLs, want) {
		t.Fatalf("urls = %#v, want %#v", requestedURLs, want)
	}
}
