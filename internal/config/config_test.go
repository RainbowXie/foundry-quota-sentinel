package config

import "testing"

func TestUpsertOllamaAccountAppendsNewName(t *testing.T) {
	cfg := Config{OllamaAccounts: []OllamaAccount{{Name: "work", Cookie: "work-cookie"}}}

	cfg.UpsertOllamaAccount(OllamaAccount{Name: "personal", Cookie: "personal-cookie"})

	if got, want := cfg.OllamaAccounts, []OllamaAccount{
		{Name: "work", Cookie: "work-cookie"},
		{Name: "personal", Cookie: "personal-cookie"},
	}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("accounts = %#v, want %#v", got, want)
	}
}

func TestUpsertOllamaAccountReplacesMatchingName(t *testing.T) {
	cfg := Config{OllamaAccounts: []OllamaAccount{
		{Name: "work", Cookie: "old-cookie"},
		{Name: "personal", Cookie: "personal-cookie"},
	}}

	cfg.UpsertOllamaAccount(OllamaAccount{Name: "work", Cookie: "new-cookie"})

	if len(cfg.OllamaAccounts) != 2 {
		t.Fatalf("account count = %d, want 2", len(cfg.OllamaAccounts))
	}
	if got := cfg.OllamaAccounts[0]; got != (OllamaAccount{Name: "work", Cookie: "new-cookie"}) {
		t.Errorf("first account = %#v, want replacement", got)
	}
	if got := cfg.OllamaAccounts[1]; got != (OllamaAccount{Name: "personal", Cookie: "personal-cookie"}) {
		t.Errorf("second account = %#v, want unchanged account", got)
	}
}

func TestDeleteOllamaAccountRemovesMatchingName(t *testing.T) {
	cfg := Config{OllamaAccounts: []OllamaAccount{
		{Name: "work", Cookie: "work-cookie"},
		{Name: "personal", Cookie: "personal-cookie"},
	}}

	if err := cfg.DeleteOllamaAccount("work"); err != nil {
		t.Fatalf("DeleteOllamaAccount() error = %v", err)
	}

	if got, want := cfg.OllamaAccounts, []OllamaAccount{{Name: "personal", Cookie: "personal-cookie"}}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("accounts = %#v, want %#v", got, want)
	}
}

func TestDeleteOllamaAccountReturnsErrorForUnknownName(t *testing.T) {
	cfg := Config{OllamaAccounts: []OllamaAccount{{Name: "work", Cookie: "work-cookie"}}}

	if err := cfg.DeleteOllamaAccount("missing"); err == nil {
		t.Fatal("DeleteOllamaAccount() error = nil, want error for an unknown account")
	}

	if got, want := cfg.OllamaAccounts, []OllamaAccount{{Name: "work", Cookie: "work-cookie"}}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("accounts = %#v, want %#v", got, want)
	}
}
