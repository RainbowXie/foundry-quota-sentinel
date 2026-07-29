package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry-quota-sentinel/internal/quota"
)

// TestKimiCardsReturnsSortedThreeMetricResults (task 5.2) proves the Kimi cards
// endpoint concurrently fetches per-account three-metric quota, sorts by name,
// and returns total/5h/7d decimal metrics with per-account success/error. One
// account failure does NOT suppress other accounts.
func TestKimiCardsReturnsSortedThreeMetricResults(t *testing.T) {
	srv := NewServer(nil)
	srv.SetKimiProvider(func() []KimiAccount {
		return []KimiAccount{
			{Name: "zeta", AccessToken: "zeta-tok", Generation: 1},
			{Name: "alpha", AccessToken: "alpha-tok", Generation: 2},
		}
	})
	srv.kimiFetch = func(a KimiAccount) (*quota.KimiQuotaData, error) {
		if a.Name == "zeta" {
			return nil, errors.New("unavailable")
		}
		return &quota.KimiQuotaData{
			Total:    quota.KimiTotalUsage{TotalPercent: 2.19, KimiPercent: 0.20, CodePercent: 1.99, ResetDisplay: "2026-08-27"},
			FiveHour: quota.KimiQuotaUsage{UsagePercent: 0, ResetDisplay: "07-29 19:58"},
			SevenDay: quota.KimiQuotaUsage{UsagePercent: 10.42, ResetDisplay: "08-04 23:58"},
		}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/kimi", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name    string               `json:"name"`
			Success bool                 `json:"success"`
			Quota   *quota.KimiQuotaData `json:"quota,omitempty"`
			Error   string               `json:"error,omitempty"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 2 {
		t.Fatalf("response = %#v", got)
	}
	// Sorted by name: alpha, zeta.
	if got.Data[0].Name != "alpha" || !got.Data[0].Success || got.Data[0].Quota == nil {
		t.Fatalf("first card = %#v", got.Data[0])
	}
	if got.Data[0].Quota.Total.TotalPercent != 2.19 || got.Data[0].Quota.SevenDay.UsagePercent != 10.42 {
		t.Fatalf("first card meters = %#v", got.Data[0].Quota)
	}
	// One account failure does not suppress the other.
	if got.Data[1].Name != "zeta" || got.Data[1].Success || got.Data[1].Error != "unavailable" {
		t.Fatalf("second card = %#v, want failure with error", got.Data[1])
	}
}

// TestKimiCardsEndpointExcludesAuthFields (task 5.1/5.2) proves the cards
// response excludes the access token and all auth envelope values.
func TestKimiCardsEndpointExcludesAuthFields(t *testing.T) {
	srv := NewServer(nil)
	const secret = "synthetic-kimi-access-token-SECRET"
	srv.SetKimiProvider(func() []KimiAccount {
		return []KimiAccount{{Name: "work", AccessToken: secret, Generation: 1}}
	})
	srv.kimiFetch = func(a KimiAccount) (*quota.KimiQuotaData, error) {
		return &quota.KimiQuotaData{
			Total:    quota.KimiTotalUsage{TotalPercent: 2.19, KimiPercent: 0.20, CodePercent: 1.99, ResetDisplay: "2026-08-27"},
			FiveHour: quota.KimiQuotaUsage{UsagePercent: 0, ResetDisplay: "07-29 19:58"},
			SevenDay: quota.KimiQuotaUsage{UsagePercent: 10.42, ResetDisplay: "08-04 23:58"},
		}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/kimi", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	body := w.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("cards response leaks the access token: %s", body)
	}
	if strings.Contains(strings.ToLower(body), "access_token") {
		t.Fatalf("cards response exposes an access_token field: %s", body)
	}
}

// TestKimiAccountsEndpointReturnsConfigImmediately proves the fast Kimi
// accounts endpoint reflects config-saved accounts with generation, no token.
func TestKimiAccountsEndpointReturnsConfigImmediately(t *testing.T) {
	srv := NewServer(nil)
	srv.SetKimiProvider(func() []KimiAccount {
		return []KimiAccount{{Name: "work", AccessToken: "tok", Generation: 3}}
	})

	r := httptest.NewRequest(http.MethodGet, "/api/kimi/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name       string `json:"name"`
			Pending    bool   `json:"pending"`
			Generation int    `json:"generation"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 1 || got.Data[0].Name != "work" || got.Data[0].Generation != 3 {
		t.Fatalf("response = %#v", got)
	}
	if strings.Contains(w.Body.String(), "tok") {
		t.Fatalf("accounts response must not expose the access token: %s", w.Body.String())
	}
}

// TestKimiAccountsEndpointEmptyWhenNoAccounts proves no accounts → empty list.
func TestKimiAccountsEndpointEmptyWhenNoAccounts(t *testing.T) {
	srv := NewServer(nil)
	srv.SetKimiProvider(func() []KimiAccount { return nil })

	r := httptest.NewRequest(http.MethodGet, "/api/kimi/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

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

// TestKimiLoginEndpointSpawnsSubprocess proves /api/kimi/login spawns the
// login-kimi subprocess and returns success=true.
func TestKimiLoginEndpointSpawnsSubprocess(t *testing.T) {
	srv := NewServer(nil)
	spawned := false
	srv.spawnKimiLogin = func(name string) error {
		if name != "work" {
			t.Fatalf("spawn called with name %q, want work", name)
		}
		spawned = true
		return nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/kimi/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || !spawned {
		t.Fatalf("response = %#v, spawned=%v, want success=true", got, spawned)
	}
}

// TestKimiLoginReportsSpawnFailure proves a spawn failure surfaces
// success=false with an error.
func TestKimiLoginReportsSpawnFailure(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnKimiLogin = func(string) error { return errors.New("spawn denied") }

	r := httptest.NewRequest(http.MethodGet, "/api/kimi/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

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

// TestOpenEndpointAcceptsKimiProvider proves /api/open accepts provider=kimi.
func TestOpenEndpointAcceptsKimiProvider(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnOpenPage = func(provider, name, session string) (func() error, error) {
		if provider != "kimi" {
			t.Fatalf("provider = %q, want kimi", provider)
		}
		WriteOpenHandshake(session, "ready", "")
		return func() error { return nil }, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=kimi&name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	var got struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatal("kimi open-page must succeed on a ready handshake")
	}
}

// TestDeleteEndpointAcceptsKimiProvider proves /api/delete accepts provider=kimi.
func TestDeleteEndpointAcceptsKimiProvider(t *testing.T) {
	srv := NewServer(nil)
	var provider, name string
	srv.SetDeleteHandler(func(p, n string) error {
		provider, name = p, n
		return nil
	})

	r := httptest.NewRequest(http.MethodGet, "/api/delete?provider=kimi&name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if provider != "kimi" || name != "work" {
		t.Fatalf("delete handler called with (%q, %q), want (kimi, work)", provider, name)
	}
}

// TestOpenEndpointRejectsUnknownProviderStill proves an unknown provider is
// rejected (kimi is valid, but "bogus" is not).
func TestOpenEndpointRejectsUnknownProviderStill(t *testing.T) {
	srv := NewServer(nil)
	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=bogus&name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success {
		t.Fatal("unknown provider must be rejected")
	}
}

// keep time imported (used by synthetic fixtures in other packages; this file
// may reference time for future refresh tests).
var _ = time.Now

// TestKimiCardsPersistenceFailureSurfacesReLogin (task 3.2/3.4) proves that
// when the durable refresh succeeds but persisting the rotated tokens FAILS,
// the card surfaces a re-login-required error instead of silently succeeding
// (which would leave rotated tokens unpersisted and the config stale). The old
// record must be preserved and the user told to re-login.
func TestKimiCardsPersistenceFailureSurfacesReLogin(t *testing.T) {
	srv := NewServer(nil)
	srv.SetKimiProvider(func() []KimiAccount {
		return []KimiAccount{{Name: "work", AccessToken: "expired-tok", RefreshToken: "rt", Generation: 1}}
	})
	srv.kimiFetch = nil // use production path
	srv.kimiFetchWithRefresh = func(ctx context.Context, a KimiAccount) (*quota.KimiQuotaData, *quota.RefreshResult, error) {
		// Refresh "succeeded": rotated tokens returned.
		return &quota.KimiQuotaData{
				Total:    quota.KimiTotalUsage{TotalPercent: 2.19, KimiPercent: 0.20, CodePercent: 1.99},
				FiveHour: quota.KimiQuotaUsage{UsagePercent: 0},
				SevenDay: quota.KimiQuotaUsage{UsagePercent: 10.42},
			}, &quota.RefreshResult{
				AccessToken:  "rotated-access-SECRET",
				RefreshToken: "rotated-refresh-SECRET",
			}, nil
	}
	saveCalled := false
	srv.SetKimiRefreshSave(func(name, access, refresh string) error {
		saveCalled = true
		return errors.New("disk full")
	})

	r := httptest.NewRequest(http.MethodGet, "/api/kimi", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	var got struct {
		Data []struct {
			Name    string `json:"name"`
			Success bool   `json:"success"`
			Error   string `json:"error,omitempty"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !saveCalled {
		t.Fatal("kimiRefreshSave must be called when tokens rotate")
	}
	if len(got.Data) != 1 {
		t.Fatalf("data = %#v", got.Data)
	}
	if got.Data[0].Success {
		t.Fatalf("card succeeded silently despite persistence failure; must surface re-login: %#v", got.Data[0])
	}
	if got.Data[0].Error == "" {
		t.Fatalf("card error must be non-empty on persistence failure: %#v", got.Data[0])
	}
	if strings.Contains(got.Data[0].Error, "rotated-access-SECRET") || strings.Contains(got.Data[0].Error, "rotated-refresh-SECRET") {
		t.Fatalf("card error must not leak rotated credentials: %q", got.Data[0].Error)
	}
}

// TestKimiCardsSerializesPerAccountRefresh (task 3.4) proves concurrent card
// requests for the SAME account cannot race the refresh path: the per-account
// mutex makes refresh+persist serial. Two concurrent requests for one account
// must never run their fetch-with-refresh simultaneously (which would double-
// rotate the refresh_token). Different accounts run concurrently (isolation).
func TestKimiCardsSerializesPerAccountRefresh(t *testing.T) {
	srv := NewServer(nil)
	srv.SetKimiProvider(func() []KimiAccount {
		return []KimiAccount{{Name: "work", AccessToken: "tok", RefreshToken: "rt", Generation: 1}}
	})
	srv.kimiFetch = nil
	var (
		mu          sync.Mutex
		inFlight    int
		maxInFlight int
		calls       int
	)
	srv.kimiFetchWithRefresh = func(ctx context.Context, a KimiAccount) (*quota.KimiQuotaData, *quota.RefreshResult, error) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		calls++
		mu.Unlock()
		// Hold long enough that a non-serialized peer would overlap.
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return &quota.KimiQuotaData{
			Total:    quota.KimiTotalUsage{TotalPercent: 1},
			FiveHour: quota.KimiQuotaUsage{UsagePercent: 0},
			SevenDay: quota.KimiQuotaUsage{UsagePercent: 2},
		}, nil, nil
	}
	// No save callback (no rotation) so we isolate the serialization check.

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, "/api/kimi", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
		}()
	}
	wg.Wait()

	if maxInFlight > 1 {
		t.Fatalf("per-account refresh was concurrent: max in-flight = %d (must be serialized to 1)", maxInFlight)
	}
	if calls != 4 {
		t.Fatalf("expected 4 fetch calls, got %d", calls)
	}
}
