package auth

import (
	"strings"
	"testing"
)

func TestValidateDeepSeekPageURL(t *testing.T) {
	if err := validateDeepSeekPageURL("https://platform.deepseek.com/usage"); err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}
	if err := validateDeepSeekPageURL("http://platform.deepseek.com/usage"); err == nil {
		t.Fatal("http URL must be rejected")
	}
	if err := validateDeepSeekPageURL("https://evil.example.com/usage"); err == nil {
		t.Fatal("evil host must be rejected")
	}
}

func TestDeepSeekStorageCandidates(t *testing.T) {
	token := "abc123456789012345678901234567890" // 34 chars
	snapshot := `{"l":{"userToken":"` + token + `"},"s":{"key":"short"}}`
	candidates := deepSeekStorageCandidates(snapshot)
	if len(candidates) != 1 || candidates[0] != token {
		t.Fatalf("expected candidates [%s], got %v", token, candidates)
	}

	nested := `{"l":{"nested":{"deep":"` + token + `"}},"s":{}}`
	candidatesNested := deepSeekStorageCandidates(nested)
	if len(candidatesNested) != 1 || candidatesNested[0] != token {
		t.Fatalf("expected nested candidate [%s], got %v", token, candidatesNested)
	}
}

func TestDeepSeekRestoreState(t *testing.T) {
	snapshot := `{"l":{"userToken":"abc123456789012345678901234567890"},"s":{},"c":[{"name":"cook","value":"val","domain":"platform.deepseek.com"}]}`
	script, cookies, err := deepSeekRestoreState(snapshot)
	if err != nil {
		t.Fatalf("deepSeekRestoreState failed: %v", err)
	}
	if !strings.Contains(script, "localStorage.setItem") {
		t.Fatalf("script does not contain localStorage restore: %s", script)
	}
	if len(cookies) != 1 || cookies[0].Name != "cook" {
		t.Fatalf("cookies not parsed properly: %+v", cookies)
	}
}

func TestDeepSeekStorageProbeExpr(t *testing.T) {
	keys := []string{"userToken", "settings"}
	expr := deepSeekStorageProbeExpr(keys)
	if !strings.Contains(expr, `["userToken","settings"].map`) {
		t.Fatalf("expected keys mapped directly: %s", expr)
	}
}
