package config

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestLoadPreKimiConfigRoundTripsWithoutLoss (task 2.1) proves a config saved
// before Kimi existed round-trips with no data loss for any existing provider,
// and the Kimi field is treated as empty when absent.
func TestLoadPreKimiConfigRoundTripsWithoutLoss(t *testing.T) {
	preKimi := `{
		"active_profile": "work",
		"profiles": {"work": {"cookie": "c", "workspace_id": "wrk_1", "deepseek_api_key": "dk"}},
		"deepseek_accounts": [{"name": "ds", "token": "tok", "generation": 2}],
		"ollama_accounts": [{"name": "ol", "cookie": "olc"}],
		"window_w": 360,
		"window_h": 700
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(preKimi), &cfg); err != nil {
		t.Fatalf("load pre-Kimi config: %v", err)
	}
	if cfg.ActiveProfile != "work" || len(cfg.Profiles) != 1 || len(cfg.DeepSeekAccounts) != 1 || len(cfg.OllamaAccounts) != 1 {
		t.Fatalf("existing fields lost: %#v", cfg)
	}
	if cfg.WindowW != 360 || cfg.WindowH != 700 {
		t.Fatalf("window size lost: %dx%d", cfg.WindowW, cfg.WindowH)
	}
	if len(cfg.KimiAccounts) != 0 {
		t.Fatalf("kimi accounts = %#v, want empty for a pre-Kimi config", cfg.KimiAccounts)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("re-serialise: %v", err)
	}
	if !strings.Contains(string(out), `"work"`) || !strings.Contains(string(out), `"wrk_1"`) {
		t.Fatalf("round-trip lost existing fields: %s", out)
	}
}

// TestUpsertKimiAccountAppendsNewName (task 2.2) proves a new Kimi account is
// appended and gets a non-zero generation.
func TestUpsertKimiAccountAppendsNewName(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{{Name: "work", Generation: 1}}}
	cfg.UpsertKimiAccount(KimiAccount{Name: "personal"})
	if len(cfg.KimiAccounts) != 2 || cfg.KimiAccounts[1].Generation == 0 {
		t.Fatalf("accounts = %#v", cfg.KimiAccounts)
	}
}

// TestUpsertKimiAccountReplacesMatchingNameAndBumpsGeneration proves a
// re-login replaces only that account's auth and advances generation; other
// Kimi accounts are untouched.
func TestUpsertKimiAccountReplacesMatchingNameAndBumpsGeneration(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{
		{Name: "work", Generation: 3},
		{Name: "personal", Generation: 1},
	}}
	cfg.UpsertKimiAccount(KimiAccount{Name: "work"})
	if cfg.KimiAccounts[0].Generation != 4 || cfg.KimiAccounts[1].Generation != 1 {
		t.Fatalf("generations = %#v", cfg.KimiAccounts)
	}
}

// TestDeleteKimiAccountRemovesMatchingName proves deletion removes only the
// named account.
func TestDeleteKimiAccountRemovesMatchingName(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{{Name: "work"}, {Name: "personal"}}}
	if err := cfg.DeleteKimiAccount("work"); err != nil {
		t.Fatalf("DeleteKimiAccount: %v", err)
	}
	if len(cfg.KimiAccounts) != 1 || cfg.KimiAccounts[0].Name != "personal" {
		t.Fatalf("accounts = %#v", cfg.KimiAccounts)
	}
}

// TestDeleteKimiAccountReturnsErrorForUnknownName proves an unknown name is an
// error and the list is unchanged.
func TestDeleteKimiAccountReturnsErrorForUnknownName(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{{Name: "work"}}}
	if err := cfg.DeleteKimiAccount("missing"); err == nil {
		t.Fatal("want error for unknown name")
	}
	if len(cfg.KimiAccounts) != 1 {
		t.Fatalf("accounts changed: %#v", cfg.KimiAccounts)
	}
}

// TestKimiAuthEnvelopeEncodeDecodeRoundTrip (task 2.3) proves the versioned
// auth envelope encodes/decodes faithfully at the supported version, carrying
// the real replay fields (Bearer token + cookie + browser headers).
func TestKimiAuthEnvelopeEncodeDecodeRoundTrip(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	for _, f := range []struct{ k, v string }{
		{"accessToken", "synthetic-bearer-jwt-1234567890"},
		{"cookie", "theme=light; sid=abc"},
		{"x_msh_device_id", "7667849338827074573"},
		{"x_traffic_id", "cnmhkh0nsmmh83k53vjg"},
		{"x_msh_platform", "web"},
		{"x_msh_version", "2.0.0"},
		{"x_language", "zh-CN"},
		{"r_timezone", "Asia/Shanghai"},
		{"user_agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"},
	} {
		if err := env.SetField(f.k, f.v); err != nil {
			t.Fatalf("SetField(%q): %v", f.k, err)
		}
	}
	data, err := env.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var decoded KimiAuthEnvelope
	if err := decoded.Decode(data); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Version != kimiAuthEnvelopeVersion {
		t.Fatalf("version = %d, want %d", decoded.Version, kimiAuthEnvelopeVersion)
	}
	if got := decoded.AccessToken(); got != "synthetic-bearer-jwt-1234567890" {
		t.Fatalf("accessToken = %q", got)
	}
	if c, ok := decoded.Field("cookie"); !ok || c != "theme=light; sid=abc" {
		t.Fatalf("cookie = %q ok=%v", c, ok)
	}
	if d, ok := decoded.Field("x_msh_device_id"); !ok || d != "7667849338827074573" {
		t.Fatalf("device id = %q ok=%v", d, ok)
	}
}

// TestKimiAuthEnvelopeRejectsUnsupportedVersion proves an unknown envelope
// version fails closed and is NOT partially replayed.
func TestKimiAuthEnvelopeRejectsUnsupportedVersion(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	_ = env.SetField("accessToken", "v")
	data, _ := env.Encode()
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	raw["version"], _ = json.Marshal(kimiAuthEnvelopeVersion + 999)
	bad, _ := json.Marshal(raw)
	var decoded KimiAuthEnvelope
	if err := decoded.Decode(bad); err == nil {
		t.Fatal("Decode of unsupported version must fail, not partially replay")
	}
}

// TestKimiAuthEnvelopeRejectsControlCharsInHeaderValues proves a credential
// value carrying CR/LF (header injection) is rejected.
func TestKimiAuthEnvelopeRejectsControlCharsInHeaderValues(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	if err := env.SetField("accessToken", "v\r\nEvil: header"); err == nil {
		t.Fatal("SetField must reject a value carrying CR/LF control characters")
	}
}

// TestKimiAuthEnvelopeOmitsUnknownCapturedState proves the envelope stores
// only the allowlisted field names; an unknown name is rejected at capture.
func TestKimiAuthEnvelopeOmitsUnknownCapturedState(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	_ = env.SetField("accessToken", "keep")
	if err := env.SetField("not_allowlisted", "drop"); err == nil {
		t.Fatal("SetField must reject a field name outside the evidence allowlist")
	}
	data, _ := env.Encode()
	if strings.Contains(string(data), "not_allowlisted") {
		t.Fatalf("unknown field leaked into envelope: %s", data)
	}
}

// TestKimiAuthEnvelopeDecodeDropsUnknownFieldsAtLoad proves a hand-edited
// config carrying an unknown field is silently dropped at load (not replayed).
func TestKimiAuthEnvelopeDecodeDropsUnknownFieldsAtLoad(t *testing.T) {
	bad := `{"version":1,"fields":{"accessToken":"keep","evil_unknown":"drop"}}`
	var decoded KimiAuthEnvelope
	if err := decoded.Decode([]byte(bad)); err != nil {
		t.Fatalf("Decode dropped-unknown must succeed (drop the unknown): %v", err)
	}
	if _, ok := decoded.Field("evil_unknown"); ok {
		t.Fatal("unknown field must be dropped at load, not kept")
	}
	if got := decoded.AccessToken(); got != "keep" {
		t.Fatalf("accessToken = %q, want keep", got)
	}
}

// TestSaveKimiTokensConcurrentNoLostUpdate (hardening) proves that concurrent
// token rotations of DIFFERENT accounts do not lose either rotation. The save
// path must serialize (config-wide write lock) and re-load the latest on-disk
// config inside the lock before modifying only the target account, so the
// second save does not overwrite the first account's freshly-rotated token
// with a stale snapshot. RED: the old Load→modify→Save (unsynchronized, stale
// snapshot) loses one account's rotation; GREEN: serialized + reloaded.
func TestSaveKimiTokensConcurrentNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	configPathMu.Lock()
	configPathOverride = filepath.Join(dir, "config.json")
	configPathMu.Unlock()
	defer func() {
		configPathMu.Lock()
		configPathOverride = ""
		configPathMu.Unlock()
	}()

	// Seed two accounts with envelopes carrying distinct initial tokens.
	seed := &Config{ActiveProfile: "default", Profiles: map[string]Profile{}}
	for _, n := range []string{"alpha", "beta"} {
		var env KimiAuthEnvelope
		env.SetField("accessToken", n+"-initial-access-AAAAAAAAAAAA")
		env.SetField("refreshToken", n+"-initial-refresh-BBBBBBBBBBBB")
		seed.UpsertKimiAccount(KimiAccount{Name: n, Auth: env})
	}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	// Concurrently rotate both accounts' tokens many times. The lost-update
	// window under an unsynchronized Load→modify→Save is a classic check-then-
	// act race that is not reliably reproducible at normal scheduling speed, so
	// this is a regression guard: under the lock+in-lock-reload path both
	// accounts always end with a rotated (non-initial) token and both accounts
	// survive (no account dropped, no token reverted to a stale snapshot). The
	// race detector additionally guards the shared configPathOverride global.
	var wg sync.WaitGroup
	for _, n := range []string{"alpha", "beta"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := SaveKimiTokens(name, name+"-rotated-access-"+strconv.Itoa(i), name+"-rotated-refresh-"+strconv.Itoa(i)); err != nil {
					t.Errorf("SaveKimiTokens(%s): %v", name, err)
					return
				}
			}
		}(n)
	}
	wg.Wait()

	// Both accounts must be present and have a NON-initial (rotated) access
	// token — no lost update. A stale-snapshot save would have overwritten one
	// account's rotation with the other's older snapshot, or dropped an account.
	got := Load()
	if len(got.KimiAccounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d (lost account)", len(got.KimiAccounts))
	}
	for _, a := range got.KimiAccounts {
		tok := a.Auth.AccessToken()
		if strings.Contains(tok, "initial") {
			t.Fatalf("account %q still has initial token %q — rotation lost by concurrent save", a.Name, tok)
		}
		if !strings.HasPrefix(tok, a.Name+"-rotated-access-") {
			t.Fatalf("account %q has unexpected token %q", a.Name, tok)
		}
	}
}

// TestConfigWriteTransactionNoCrossOverwrite (hardening) proves that a token
// rotation and an unrelated config write (window-size save) running concurrently
// do NOT overwrite each other: both the rotated token AND the window size
// persist. The old unsynchronized paths each did Load→modify→Save on the whole
// config, so the window save could clobber the rotated token (and vice versa).
// All config writes must share one transaction lock.
func TestConfigWriteTransactionNoCrossOverwrite(t *testing.T) {
	dir := t.TempDir()
	configPathMu.Lock()
	configPathOverride = filepath.Join(dir, "config.json")
	configPathMu.Unlock()
	defer func() {
		configPathMu.Lock()
		configPathOverride = ""
		configPathMu.Unlock()
	}()

	// Seed one account + a window size.
	seed := &Config{ActiveProfile: "default", Profiles: map[string]Profile{}, WindowW: 100, WindowH: 100}
	var env KimiAuthEnvelope
	env.SetField("accessToken", "initial-access-AAAAAAAAAAAA")
	env.SetField("refreshToken", "initial-refresh-BBBBBBBBBBBB")
	seed.UpsertKimiAccount(KimiAccount{Name: "work", Auth: env})
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	// Concurrently: rotate the token AND save a new window size, many times.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := SaveKimiTokens("work", "rotated-access-"+strconv.Itoa(i), "rotated-refresh-"+strconv.Itoa(i)); err != nil {
				t.Errorf("SaveKimiTokens: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			SaveWindowSize(200+i, 200+i)
		}
	}()
	wg.Wait()

	got := Load()
	// The token must be rotated (not initial) — window save must not have
	// clobbered it with a stale snapshot.
	tok := ""
	if len(got.KimiAccounts) == 1 {
		tok = got.KimiAccounts[0].Auth.AccessToken()
	}
	if strings.Contains(tok, "initial") {
		t.Fatalf("token reverted to initial %q — window save overwrote the rotation", tok)
	}
	if !strings.HasPrefix(tok, "rotated-access-") {
		t.Fatalf("token = %q, want a rotated-access-* token", tok)
	}
	// The window size must be the last written (200+), not the seed 100 — the
	// rotation save must not have clobbered the window size with a stale 100.
	if got.WindowW < 200 || got.WindowH < 200 {
		t.Fatalf("window = %dx%d, want >=200 (rotation overwrote window size)", got.WindowW, got.WindowH)
	}
}
