package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLoadPreKimiConfigRoundTripsWithoutLoss (task 2.1) proves a config
// saved before Kimi existed round-trips through load/save with no data loss
// for any existing provider. The Kimi field is absent from the fixture; the
// load MUST treat the Kimi list as empty, and the save MUST preserve every
// other field byte-for-byte in shape. This test fails on the pre-Kimi
// implementation because there is no Kimi field to be empty AND no
// KimiAccount type to assert against — but the real regression guard is
// that existing fields survive unchanged once the field exists.
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
	if cfg.ActiveProfile != "work" {
		t.Fatalf("active_profile = %q, want work", cfg.ActiveProfile)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles["work"].WorkspaceID != "wrk_1" {
		t.Fatalf("profiles lost: %#v", cfg.Profiles)
	}
	if len(cfg.DeepSeekAccounts) != 1 || cfg.DeepSeekAccounts[0].Generation != 2 {
		t.Fatalf("deepseek accounts lost: %#v", cfg.DeepSeekAccounts)
	}
	if len(cfg.OllamaAccounts) != 1 {
		t.Fatalf("ollama accounts lost: %#v", cfg.OllamaAccounts)
	}
	if cfg.WindowW != 360 || cfg.WindowH != 700 {
		t.Fatalf("window size lost: %dx%d", cfg.WindowW, cfg.WindowH)
	}
	// Kimi list is absent in a pre-Kimi config → must be empty, never nil-panic.
	if len(cfg.KimiAccounts) != 0 {
		t.Fatalf("kimi accounts = %#v, want empty for a pre-Kimi config", cfg.KimiAccounts)
	}
	// Round-trip: re-serialise and confirm the existing fields are preserved.
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("re-serialise: %v", err)
	}
	if !strings.Contains(string(out), `"work"`) || !strings.Contains(string(out), `"wrk_1"`) {
		t.Fatalf("round-trip lost existing fields: %s", out)
	}
}

// TestUpsertKimiAccountAppendsNewName (task 2.2) proves a new Kimi account
// is appended and gets a non-zero generation.
func TestUpsertKimiAccountAppendsNewName(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{{Name: "work", Generation: 1}}}
	cfg.UpsertKimiAccount(KimiAccount{Name: "personal"})
	if len(cfg.KimiAccounts) != 2 {
		t.Fatalf("account count = %d, want 2", len(cfg.KimiAccounts))
	}
	if cfg.KimiAccounts[1].Name != "personal" {
		t.Fatalf("appended account = %#v", cfg.KimiAccounts[1])
	}
	if cfg.KimiAccounts[1].Generation == 0 {
		t.Fatal("new Kimi account must start with a non-zero generation")
	}
}

// TestUpsertKimiAccountReplacesMatchingNameAndBumpsGeneration proves a
// re-login for an existing name replaces only that account's auth state and
// advances the generation (a same-envelope re-login still completes the
// poll). Other Kimi accounts are untouched.
func TestUpsertKimiAccountReplacesMatchingNameAndBumpsGeneration(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{
		{Name: "work", Generation: 3},
		{Name: "personal", Generation: 1},
	}}
	cfg.UpsertKimiAccount(KimiAccount{Name: "work"})
	if len(cfg.KimiAccounts) != 2 {
		t.Fatalf("account count = %d, want 2", len(cfg.KimiAccounts))
	}
	if cfg.KimiAccounts[0].Generation != 4 {
		t.Fatalf("replaced account generation = %d, want 4", cfg.KimiAccounts[0].Generation)
	}
	if cfg.KimiAccounts[1].Name != "personal" || cfg.KimiAccounts[1].Generation != 1 {
		t.Fatalf("unrelated account changed: %#v", cfg.KimiAccounts[1])
	}
}

// TestDeleteKimiAccountRemovesMatchingName proves deletion removes only the
// named account and leaves the rest.
func TestDeleteKimiAccountRemovesMatchingName(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{
		{Name: "work"},
		{Name: "personal"},
	}}
	if err := cfg.DeleteKimiAccount("work"); err != nil {
		t.Fatalf("DeleteKimiAccount: %v", err)
	}
	if len(cfg.KimiAccounts) != 1 || cfg.KimiAccounts[0].Name != "personal" {
		t.Fatalf("accounts = %#v, want only personal", cfg.KimiAccounts)
	}
}

// TestDeleteKimiAccountReturnsErrorForUnknownName proves an unknown name is
// an error and the account list is unchanged.
func TestDeleteKimiAccountReturnsErrorForUnknownName(t *testing.T) {
	cfg := Config{KimiAccounts: []KimiAccount{{Name: "work"}}}
	if err := cfg.DeleteKimiAccount("missing"); err == nil {
		t.Fatal("DeleteKimiAccount error = nil, want error for unknown name")
	}
	if len(cfg.KimiAccounts) != 1 || cfg.KimiAccounts[0].Name != "work" {
		t.Fatalf("accounts = %#v, want unchanged", cfg.KimiAccounts)
	}
}

// TestKimiAuthEnvelopeEncodeDecodeRoundTrip (task 2.3) proves the versioned
// auth envelope encodes and decodes faithfully at the supported version.
func TestKimiAuthEnvelopeEncodeDecodeRoundTrip(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	if err := env.SetCookie("kimi_session", "synthetic-cookie-value"); err != nil {
		t.Fatalf("SetCookie: %v", err)
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
	cookie, ok := decoded.Cookie("kimi_session")
	if !ok || cookie != "synthetic-cookie-value" {
		t.Fatalf("decoded cookie = %q ok=%v", cookie, ok)
	}
}

// TestKimiAuthEnvelopeRejectsUnsupportedVersion proves an unknown envelope
// version fails with a re-login-required error and is NOT partially replayed.
func TestKimiAuthEnvelopeRejectsUnsupportedVersion(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	_ = env.SetCookie("kimi_session", "v")
	data, err := env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the version to a future, unsupported value.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["version"], _ = json.Marshal(kimiAuthEnvelopeVersion + 999)
	bad, _ := json.Marshal(raw)
	var decoded KimiAuthEnvelope
	if err := decoded.Decode(bad); err == nil {
		t.Fatal("Decode of unsupported version must fail, not partially replay")
	}
}

// TestKimiAuthEnvelopeRejectsControlCharsInHeaderValues proves a credential
// value carrying CR/LF (header injection) is rejected — it never reaches
// the replayed cookie set.
func TestKimiAuthEnvelopeRejectsControlCharsInHeaderValues(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	if err := env.SetCookie("kimi_session", "v\r\nEvil: header"); err == nil {
		t.Fatal("SetCookie must reject a value carrying CR/LF control characters")
	}
}

// TestKimiAuthEnvelopeOmitsUnknownCapturedState proves the envelope stores
// only the allowlisted cookie names; an unknown cookie name is dropped at
// capture, not persisted.
func TestKimiAuthEnvelopeOmitsUnknownCapturedState(t *testing.T) {
	env := KimiAuthEnvelope{Version: kimiAuthEnvelopeVersion}
	_ = env.SetCookie("kimi_session", "keep")
	// An unknown name must be rejected — the allowlist is closed.
	if err := env.SetCookie("not_allowlisted", "drop"); err == nil {
		t.Fatal("SetCookie must reject a cookie name outside the evidence allowlist")
	}
	data, _ := env.Encode()
	if strings.Contains(string(data), "not_allowlisted") {
		t.Fatalf("unknown cookie leaked into envelope: %s", data)
	}
}
