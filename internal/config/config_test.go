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

// TestUpsertDeepSeekAccountBumpsGenerationOnSameTokenRelogin proofs a
// same-token re-login still bumps Generation. DeepSeek may return the
// same long-lived token while Cookie/WebStore is refreshed; a token
// fingerprint would not change, so a completion poll would wait 5
// minutes and never refresh. Generation must move on every overwrite
// regardless of token value.
func TestUpsertDeepSeekAccountBumpsGenerationOnSameTokenRelogin(t *testing.T) {
	cfg := Config{}
	cfg.UpsertDeepSeekAccount(DeepSeekAccount{Name: "work", Token: "tok-same"})
	g1 := cfg.DeepSeekAccounts[0].Generation
	if g1 == 0 {
		t.Fatal("first generation must be non-zero")
	}
	// Same token, re-login.
	cfg.UpsertDeepSeekAccount(DeepSeekAccount{Name: "work", Token: "tok-same"})
	g2 := cfg.DeepSeekAccounts[0].Generation
	if g2 != g1+1 {
		t.Fatalf("same-token re-login must bump generation: %d -> %d", g1, g2)
	}
}

// TestUpsertDeepSeekAccountGenerationStableAcrossUnrelatedSave proofs
// an unrelated config field change (window size) does NOT change THIS
// account's generation, so a generation-based poll does not falsely
// complete on a window-size save.
func TestUpsertDeepSeekAccountGenerationStableAcrossUnrelatedSave(t *testing.T) {
	cfg := Config{}
	cfg.UpsertDeepSeekAccount(DeepSeekAccount{Name: "work", Token: "tok"})
	gen := cfg.DeepSeekAccounts[0].Generation
	// Simulate SaveWindowSize: rewrites config but never touches
	// DeepSeekAccounts.
	cfg.WindowW = 999
	if cfg.DeepSeekAccounts[0].Generation != gen {
		t.Fatalf("unrelated save must not change generation: %d -> %d", gen, cfg.DeepSeekAccounts[0].Generation)
	}
}
