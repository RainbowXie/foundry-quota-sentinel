package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
