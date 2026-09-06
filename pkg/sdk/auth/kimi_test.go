package auth

import (
	"testing"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

func TestValidateKimiPageURL(t *testing.T) {
	if err := validateKimiPageURL("https://www.kimi.com/membership/subscription?tab=quota"); err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}

	if err := validateKimiPageURL("http://www.kimi.com/membership/subscription?tab=quota"); err == nil {
		t.Fatal("http scheme must be rejected")
	}

	if err := validateKimiPageURL("https://evil.com/membership/subscription?tab=quota"); err == nil {
		t.Fatal("evil host must be rejected")
	}

	if err := validateKimiPageURL("https://www.kimi.com/membership/subscription"); err == nil {
		t.Fatal("missing tab=quota query param must be rejected")
	}
}

func TestKimiCookieHeader(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "session", Value: "val1", Domain: "www.kimi.com"},
		{Name: "other", Value: "val2", Domain: "evil.com"},
		{Name: "crlf", Value: "val3\r\n", Domain: "www.kimi.com"},
	}
	h := kimiCookieHeader(cookies)
	if h != "session=val1" {
		t.Fatalf("unexpected header: %q", h)
	}
}

func TestKimiBuildEnvelope(t *testing.T) {
	headers := map[string]string{
		"x-msh-device-id": "dev1",
		"x-traffic-id":    "traf1",
		"unrelated":       "skip",
	}
	env := kimiBuildEnvelope("tok", "ref", "session=1", headers)
	if env.AccessToken() != "tok" {
		t.Fatalf("AccessToken = %q, want tok", env.AccessToken())
	}
	if env.RefreshToken() != "ref" {
		t.Fatalf("RefreshToken = %q, want ref", env.RefreshToken())
	}
	if v, _ := env.Field("x_msh_device_id"); v != "dev1" {
		t.Fatalf("x_msh_device_id = %q, want dev1", v)
	}
	if _, ok := env.Field("unrelated"); ok {
		t.Fatal("unrelated field must not be in envelope")
	}
}
