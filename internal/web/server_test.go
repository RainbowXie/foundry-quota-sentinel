package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"foundry-quota-sentinel/internal/quota"
)

func TestOllamaCardsReturnsSortedQuotaResults(t *testing.T) {
	srv := NewServer(nil)
	srv.SetOllamaAccounts([]OllamaAccount{
		{Name: "zeta", Cookie: "zeta-cookie"},
		{Name: "alpha", Cookie: "alpha-cookie"},
	})
	srv.ollamaFetch = func(a OllamaAccount) (*quota.QuotaData, error) {
		if a.Name == "zeta" {
			return nil, errors.New("unavailable")
		}
		return &quota.QuotaData{Rolling: quota.QuotaUsage{UsagePercent: 42}}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/ollama", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name    string           `json:"name"`
			Success bool             `json:"success"`
			Quota   *quota.QuotaData `json:"quota"`
			Error   string           `json:"error"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 2 {
		t.Fatalf("response = %#v", got)
	}
	if got.Data[0].Name != "alpha" || !got.Data[0].Success || got.Data[0].Quota == nil {
		t.Fatalf("first card = %#v", got.Data[0])
	}
	if got.Data[1].Name != "zeta" || got.Data[1].Success || got.Data[1].Error != "unavailable" {
		t.Fatalf("second card = %#v", got.Data[1])
	}
}

func TestDeleteOllamaCallsConfiguredHandler(t *testing.T) {
	srv := NewServer(nil)
	var provider, name string
	srv.SetDeleteHandler(func(gotProvider, gotName string) error {
		provider, name = gotProvider, gotName
		return nil
	})

	r := httptest.NewRequest(http.MethodGet, "/api/delete?provider=ollama&name=home", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if provider != "ollama" || name != "home" {
		t.Fatalf("delete handler called with (%q, %q), want (ollama, home)", provider, name)
	}
}

// TestDeepSeekAccountsEndpointReturnsConfigImmediately proves the
// fast accounts endpoint reflects config-saved DeepSeek accounts
// without waiting for FetchSummary/FetchUsage. After a successful
// login the sidebar must be able to render a loading card shell the
// moment the account is written to config, not after the slow remote
// data fetch completes. The endpoint must NOT call the network
// fetcher at all.
func TestDeepSeekAccountsEndpointReturnsConfigImmediately(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok"}}
	})

	start := time.Now()
	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if elapsed > time.Second {
		t.Fatalf("accounts endpoint blocked for %v on remote fetch", elapsed)
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
	if !got.Success || len(got.Data) != 1 || got.Data[0].Name != "work" || !got.Data[0].Pending {
		t.Fatalf("response = %#v", got)
	}
}

// TestDeepSeekAccountsEndpointEmptyWhenNoAccounts proves the fast
// accounts endpoint returns an empty list (not an error, not a ghost
// card) when config has no DeepSeek account — e.g. right after a
// login that was cancelled before the account was saved.
func TestDeepSeekAccountsEndpointEmptyWhenNoAccounts(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount { return nil })

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 0 {
		t.Fatalf("response = %#v, want empty data", got)
	}
}

// TestDeepSeekAccountsEndpointReportsRevision proves the fast accounts
// endpoint exposes a non-credential config revision. The sidebar must
// detect a re-login (account already exists, token rotated) by a
// revision CHANGE, not by name presence — otherwise the poll stops on
// the stale config and the new token only appears on the 30s interval.
func TestDeepSeekAccountsEndpointReportsRevision(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok"}}
	})
	srv.deepseekRevision = func() int64 { return 12345 }

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success  bool  `json:"success"`
		Revision int64 `json:"revision"`
		Data     []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Revision != 12345 {
		t.Fatalf("response = %#v, want revision=12345", got)
	}
}

// TestDeepSeekLoginReturnsPreLoginRevision proves /api/deepseek/login
// returns the config revision captured BEFORE the login subprocess is
// spawned. The sidebar captures this and polls /api/deepseek/accounts
// until the revision changes, so a re-login for an existing account is
// detected by the new save, not by the stale name match.
func TestDeepSeekLoginReturnsPreLoginRevision(t *testing.T) {
	srv := NewServer(nil)
	srv.deepseekRevision = func() int64 { return 77 }
	srv.spawnDeepSeekLogin = func(string) error { return nil }

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success  bool   `json:"success"`
		Revision int64  `json:"revision"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Revision != 77 {
		t.Fatalf("response = %#v, want success=true revision=77", got)
	}
}

// TestDeepSeekLoginReportsSpawnFailure proves /api/deepseek/login
// returns success=false when the login subprocess cannot be spawned.
// The sidebar must surface this instead of polling forever for a
// revision change that will never come.
func TestDeepSeekLoginReportsSpawnFailure(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnDeepSeekLogin = func(string) error { return errors.New("spawn denied") }

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success || got.Error == "" {
		t.Fatalf("response = %#v, want success=false with error", got)
	}
}
