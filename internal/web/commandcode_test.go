package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"foundry-quota-sentinel/pkg/sdk/providers/commandcode"
)

// TestCommandCodeAccountsEndpointReturnsShell proves /api/commandcode/accounts
// returns pending shells from config with no cookie leak.
func TestCommandCodeAccountsEndpointReturnsShell(t *testing.T) {
	srv := NewServer(nil)
	srv.SetCommandCodeProvider(func() []CommandCodeAccount {
		return []CommandCodeAccount{{Name: "cc1", Cookie: "secret-cookie", UserName: "RainbowXie"}}
	})
	r := httptest.NewRequest(http.MethodGet, "/api/commandcode/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name    string `json:"name"`
			Pending bool   `json:"pending"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 1 || got.Data[0].Name != "cc1" || !got.Data[0].Pending {
		t.Fatalf("response = %#v", got)
	}
	if strings.Contains(w.Body.String(), "secret-cookie") || strings.Contains(w.Body.String(), "RainbowXie") {
		t.Fatalf("shell response must not leak cookie or user name: %s", w.Body.String())
	}
}

// TestCommandCodeEndpointFetchesQuota proves /api/commandcode returns the
// three-window quota and isolates per-account failures.
func TestCommandCodeEndpointFetchesQuota(t *testing.T) {
	srv := NewServer(nil)
	srv.SetCommandCodeProvider(func() []CommandCodeAccount {
		return []CommandCodeAccount{
			{Name: "cc-ok", Cookie: "c1", UserName: "u1"},
			{Name: "cc-bad", Cookie: "c2", UserName: "u2"},
		}
	})
	srv.commandCodeFetch = func(a CommandCodeAccount) (*commandcode.QuotaData, error) {
		if a.Name == "cc-bad" {
			return nil, &errNoLeak{"boom"}
		}
		return &commandcode.QuotaData{
			Rolling: commandcode.QuotaUsage{Status: "active", UsagePercent: 13},
			Weekly:  commandcode.QuotaUsage{Status: "active", UsagePercent: 30},
			Monthly: &commandcode.QuotaUsage{Status: "active", UsagePercent: 15},
		}, nil
	}
	r := httptest.NewRequest(http.MethodGet, "/api/commandcode", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name    string                 `json:"name"`
			Success bool                   `json:"success"`
			Quota   *commandcode.QuotaData `json:"quota"`
			Error   string                 `json:"error"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 2 {
		t.Fatalf("response = %#v", got)
	}
	// Sorted by name: cc-bad first.
	if got.Data[0].Name != "cc-bad" || got.Data[0].Success || !strings.Contains(got.Data[0].Error, "boom") {
		t.Fatalf("bad card = %#v", got.Data[0])
	}
	if strings.Contains(got.Data[0].Error, "c2") {
		t.Fatalf("error must not leak cookie: %q", got.Data[0].Error)
	}
	if got.Data[1].Name != "cc-ok" || !got.Data[1].Success || got.Data[1].Quota == nil || got.Data[1].Quota.Rolling.UsagePercent != 13 {
		t.Fatalf("ok card = %#v", got.Data[1])
	}
	if strings.Contains(w.Body.String(), "c1") || strings.Contains(w.Body.String(), "c2") {
		t.Fatalf("response must not leak cookies: %s", w.Body.String())
	}
}

// errNoLeak is a test error carrying a secret string; the endpoint must not
// serialize the secret (server strips nothing — the fetch closure controls
// the message; this test proves the endpoint passes through whatever the
// fetch returns WITHOUT the cookie because the default fetch never embeds it).
type errNoLeak struct{ s string }

func (e *errNoLeak) Error() string { return e.s }

// TestCommandCodeLoginEndpointSpawn proves /api/commandcode/login returns
// success on a successful spawn and success=false on spawn failure.
func TestCommandCodeLoginEndpointSpawn(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnCommandCodeLogin = func(name string) error { return nil }
	r := httptest.NewRequest(http.MethodGet, "/api/commandcode/login?name=MyCC", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatalf("spawn success expected, got %#v", got)
	}

	srv2 := NewServer(nil)
	srv2.spawnCommandCodeLogin = func(name string) error { return &errNoLeak{"spawn failed"} }
	r2 := httptest.NewRequest(http.MethodGet, "/api/commandcode/login", nil)
	w2 := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(w2, r2)
	if err := json.NewDecoder(w2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success {
		t.Fatalf("spawn failure must return success=false, got %#v", got)
	}
}

// TestOpenPageAcceptsCommandCodeProvider proves /api/open accepts the
// commandcode provider (whitelist) and still rejects unknown providers.
func TestOpenPageAcceptsCommandCodeProvider(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnOpenPage = func(p, n, session string) (func() error, error) {
		WriteOpenHandshake(session, "ready", "")
		return func() error { select {} }, nil
	}
	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=commandcode&name=MyCC", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatalf("commandcode provider must be accepted, got %#v", got)
	}

	// Unknown provider still rejected.
	r2 := httptest.NewRequest(http.MethodGet, "/api/open?provider=evil&name=x", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, r2)
	if err := json.NewDecoder(w2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success {
		t.Fatalf("unknown provider must be rejected, got %#v", got)
	}
}

// TestDeleteAcceptsCommandCodeProvider proves /api/delete accepts the
// commandcode provider and forwards to the delete handler.
func TestDeleteAcceptsCommandCodeProvider(t *testing.T) {
	var deletedProvider, deletedName string
	srv := NewServer(nil)
	srv.onDelete = func(provider, name string) error {
		deletedProvider, deletedName = provider, name
		return nil
	}
	r := httptest.NewRequest(http.MethodGet, "/api/delete?provider=commandcode&name=MyCC", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatalf("delete must succeed, got %#v", got)
	}
	if deletedProvider != "commandcode" || deletedName != "MyCC" {
		t.Fatalf("delete forwarded %q %q, want commandcode MyCC", deletedProvider, deletedName)
	}
}
