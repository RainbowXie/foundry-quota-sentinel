package config

import (
	"encoding/json"
	"strings"
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
