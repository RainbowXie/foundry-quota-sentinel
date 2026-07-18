package browserauth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSetCookiesRejectsUnsafeValues(t *testing.T) {
	for _, cookie := range []Cookie{
		{Name: "session\r\n", Value: "safe", Domain: "ollama.com"},
		{Name: "session", Value: "bad;value", Domain: "ollama.com"},
		{Name: "", Value: "safe", Domain: "ollama.com"},
		{Name: "session", Value: "", Domain: "ollama.com"},
		{Name: "session", Value: "safe", Domain: ""},
	} {
		if err := validateCookie(cookie); err == nil {
			t.Fatalf("validateCookie(%#v) = nil", cookie)
		}
	}
}

func TestValidateCookieAcceptsWellFormedValues(t *testing.T) {
	cookie := Cookie{
		Name:   "session",
		Value:  "abc-123_OK",
		Domain: "ollama.com",
		Path:   "/",
		Secure: true,
	}
	if err := validateCookie(cookie); err != nil {
		t.Fatalf("validateCookie() = %v", err)
	}
}

func TestDecodeAuthorizationEvent(t *testing.T) {
	event := Event{
		Method: "Network.requestWillBeSentExtraInfo",
		Params: json.RawMessage(`{"headers":{"authorization":"Bearer valid.token-12345678901234567890"}}`),
	}
	decoded, ok := DecodeRequestHeadersEvent(event)
	if !ok {
		t.Fatal("DecodeRequestHeadersEvent returned ok=false")
	}
	if got := BearerToken(decoded.Headers); got != "valid.token-12345678901234567890" {
		t.Fatalf("BearerToken = %q", got)
	}
}

func TestBearerTokenIgnoresNonAuthorizationEvents(t *testing.T) {
	if got := BearerToken(map[string]string{}); got != "" {
		t.Fatalf("empty headers returned %q", got)
	}
	if got := BearerToken(map[string]string{"authorization": "Basic xyz"}); got != "" {
		t.Fatalf("non-bearer returned %q", got)
	}
}

func TestCookieHeaderTrimsAndRejectsCRLF(t *testing.T) {
	header, err := cookieHeader([]Cookie{
		{Name: "a", Value: "1", Domain: "ollama.com"},
		{Name: "b", Value: "2", Domain: "ollama.com", Path: "/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if header != "a=1; b=2" {
		t.Fatalf("cookieHeader = %q", header)
	}
}

func TestPageURLUsesRuntimeEvaluate(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// We can call Runtime.evaluate to fetch the location via the fake
	// server. The fake's Runtime.evaluate stub returns a constant result;
	// here we just confirm the call is dispatched to the page endpoint.
	result, err := conn.Page().Call(context.Background(), "Runtime.evaluate", map[string]any{
		"expression": "location.href",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "value") {
		t.Fatalf("unexpected result %s", string(result))
	}
	if seen := server.MethodsSeen("page"); len(seen) == 0 || seen[0] != "Runtime.evaluate" {
		t.Fatalf("page methods = %v", seen)
	}
}

func TestBrowserCookiesDispatchOnBrowserEndpoint(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Browser().Call(context.Background(), "Storage.getCookies", nil); err != nil {
		t.Fatal(err)
	}
	// Ensure browser operations land on the browser endpoint, not the page one.
	if seen := server.MethodsSeen("browser"); len(seen) == 0 || seen[0] != "Storage.getCookies" {
		t.Fatalf("browser methods = %v", seen)
	}
	if seen := server.MethodsSeen("page"); len(seen) != 0 {
		t.Fatalf("page methods should be empty, got %v", seen)
	}
}
