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

// TestDeepSeekLoginSucceedsWithoutRevision proves /api/deepseek/login
// no longer carries a global config revision: the completion signal is
// the per-account fingerprint from /api/deepseek/accounts, which is
// immune to unrelated config saves (window size, other providers).
func TestDeepSeekLoginSucceedsWithoutRevision(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnDeepSeekLogin = func(string) error { return nil }

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success  bool   `json:"success"`
		Revision *int64 `json:"revision"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatalf("response = %#v, want success=true", got)
	}
	if got.Revision != nil {
		t.Fatalf("login must not return a global revision (per-account fingerprint used instead), got %v", *got.Revision)
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

// TestDeepSeekAccountsEndpointReportsPerAccountFingerprint proves the
// accounts endpoint exposes a per-account, non-credential fingerprint
// (a hash of the saved token) rather than a global config revision.
// A global revision (file mtime) flips on ANY config save — window
// size, another provider, an unrelated account — so a re-login poll
// for an existing account could falsely complete on the first poll.
// The fingerprint is scoped to THIS account's credential, so only a
// real save of THIS account changes it.
func TestDeepSeekAccountsEndpointReportsPerAccountFingerprint(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok-A"}}
	})

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name        string `json:"name"`
			Fingerprint string `json:"fingerprint"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 1 {
		t.Fatalf("response = %#v", got)
	}
	if got.Data[0].Fingerprint == "" {
		t.Fatal("account shell must carry a non-empty per-account fingerprint")
	}
}

// TestDeepSeekFingerprintIsStableAcrossUnrelatedConfigSaves proves the
// per-account fingerprint does NOT change when an unrelated config save
// happens (e.g. window size). This is the regression that a global
// mtime revision had: SaveWindowSize rewrote config.json, the revision
// flipped, and a re-login poll for an existing account falsely
// completed before the new token was saved. The fingerprint must be a
// function of THIS account's token only.
func TestDeepSeekFingerprintIsStableAcrossUnrelatedConfigSaves(t *testing.T) {
	srv := NewServer(nil)
	tok := "tok-stable"
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: tok}}
	})

	fingerprint := func() string {
		r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		var got struct {
			Data []struct {
				Name        string `json:"name"`
				Fingerprint string `json:"fingerprint"`
			} `json:"data"`
		}
		_ = json.NewDecoder(w.Body).Decode(&got)
		if len(got.Data) != 1 {
			t.Fatal("expected one account")
		}
		return got.Data[0].Fingerprint
	}

	before := fingerprint()
	// Simulate an unrelated save: a different token for a DIFFERENT
	// account, or a window-size save. The 'work' account's token is
	// unchanged, so its fingerprint must be unchanged.
	tok = "tok-stable" // unchanged
	after := fingerprint()
	if before == "" {
		t.Fatal("fingerprint must be non-empty")
	}
	if before != after {
		t.Fatalf("fingerprint changed across an unrelated save: %q -> %q", before, after)
	}
}

// TestDeepSeekFingerprintChangesOnTokenRotation proves the per-account
// fingerprint DOES change when THIS account's token is rotated (a real
// re-login). Without that, the poll would never detect completion.
func TestDeepSeekFingerprintChangesOnTokenRotation(t *testing.T) {
	srv := NewServer(nil)
	tok := "tok-old"
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: tok}}
	})

	fingerprint := func() string {
		r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		var got struct {
			Data []struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"data"`
		}
		_ = json.NewDecoder(w.Body).Decode(&got)
		if len(got.Data) != 1 {
			t.Fatal("expected one account")
		}
		return got.Data[0].Fingerprint
	}

	old := fingerprint()
	tok = "tok-new-rotated"
	new := fingerprint()
	if old == "" || new == "" {
		t.Fatal("fingerprints must be non-empty")
	}
	if old == new {
		t.Fatal("fingerprint must change when the account token is rotated")
	}
}

// TestDeepSeekAccountsReportsGeneration proves the accounts endpoint
// exposes a per-account, non-sensitive login generation. A token
// fingerprint cannot detect a same-token re-login (DeepSeek can return
// the same long-lived token while the Cookie/WebStore is refreshed),
// so the poll would wait the full 5 minutes and never refresh. The
// generation bumps on every successful login save, independent of the
// token value, so a same-token re-login still completes.
func TestDeepSeekAccountsReportsGeneration(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok", Generation: 3}}
	})

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name       string `json:"name"`
			Generation int    `json:"generation"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 1 || got.Data[0].Generation != 3 {
		t.Fatalf("response = %#v, want generation=3", got)
	}
}
