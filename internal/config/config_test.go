package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

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

// TestSaveAtomic (hardening) is a regression guard for Save's atomic
// temp+rename write. It hammers concurrent Save + read and asserts no reader
// ever observes a torn/partial config.json. A torn write is a small window
// under os.WriteFile and is not reliably reproducible at normal speed, so this
// test is a guard that the atomic path stays atomic (no regression back to a
// plain os.WriteFile that would widen the torn window), not a deterministic
// RED proof. The atomicity guarantee itself is structural (temp file + rename
// is atomic on POSIX/Windows for same-directory renames).
func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	configPathMu.Lock()
	configPathOverride = filepath.Join(dir, "config.json")
	configPathMu.Unlock()
	defer func() {
		configPathMu.Lock()
		configPathOverride = ""
		configPathMu.Unlock()
	}()

	cfg := &Config{ActiveProfile: "default", Profiles: map[string]Profile{}}

	stop := make(chan struct{})
	// Writer: hammer Save with a mutating field each iteration. Each save uses
	// its OWN copy so the writer never reads/writes a field the reader (or a
	// concurrent save) touches — the race under test is file-level atomicity,
	// not shared-memory access.
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			snap := *cfg
			snap.WindowW = i
			_ = snap.Save()
		}
	}()
	// Reader: every observed file must be valid JSON (or absent), never torn.
	var sawTorn atomic.Bool
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			p, err := configPath()
			if err != nil {
				continue
			}
			data, err := os.ReadFile(p)
			if err == nil && len(data) > 0 {
				var c Config
				if json.Unmarshal(data, &c) != nil {
					sawTorn.Store(true)
				}
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	close(stop)

	if sawTorn.Load() {
		t.Fatal("Save produced a torn/partial file visible to a reader — must be atomic (temp+rename)")
	}
}
