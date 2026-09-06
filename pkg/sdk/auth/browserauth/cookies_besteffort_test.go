package browserauth

import (
	"context"
	"testing"
)

// TestSetCookiesBestEffortDegradesOnSingleFailure proves the DeepSeek
// best-effort injector does NOT abort when one cookie is rejected
// (e.g. a __Host- cookie Chrome refuses). The good cookie is still
// injected; the rejected one is reported by name only in the result.
// The account-page browser must stay open until the user closes it, so
// a non-fatal cookie failure must not bubble up as a flow error.
func TestSetCookiesBestEffortDegradesOnSingleFailure(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	server.rejectStorageCookieName = "__Host-bad"
	client, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	cookies := []Cookie{
		{Name: "__Host-bad", Value: "x", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
		{Name: "session", Value: "good", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
	}
	result := client.Browser().SetCookiesBestEffort(context.Background(), cookies)
	if result.Injected != 1 {
		t.Fatalf("expected 1 cookie injected, got %d (failed=%v)", result.Injected, result.Failed)
	}
	if len(result.Failed) != 1 || result.Failed[0] != "__Host-bad" {
		t.Fatalf("expected failed=[__Host-bad], got %v", result.Failed)
	}
	seen := server.MethodsSeen("browser")
	count := 0
	for _, m := range seen {
		if m == "Storage.setCookies" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected both cookies attempted, saw %d Storage.setCookies calls", count)
	}
}

// TestSetCookiesBestEffortReportsAllFailed proves a best-effort
// injection where every cookie fails reports Injected=0 and the full
// failed-name list, so runDeepSeekPage can decide whether an all-failed
// replay is fatal. It must not silently succeed.
func TestSetCookiesBestEffortReportsAllFailed(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	server.rejectStorageCookieName = "session"
	client, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	cookies := []Cookie{
		{Name: "session", Value: "x", Domain: "platform.deepseek.com", Path: "/", Secure: true, HTTPOnly: true},
	}
	result := client.Browser().SetCookiesBestEffort(context.Background(), cookies)
	if result.Injected != 0 || len(result.Failed) != 1 {
		t.Fatalf("expected 0 injected / 1 failed, got %#v", result)
	}
}
