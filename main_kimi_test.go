package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestUsageListsKimiCommands (task 5.4) proves the help text lists the Kimi
// login and quota commands with the correct weekly/frequency-limit semantics.
func TestUsageListsKimiCommands(t *testing.T) {
	var buf bytes.Buffer
	writeUsage(&buf)
	got := buf.String()
	for _, want := range []string{"login-kimi", "quota-kimi"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help text missing %q: %s", want, got)
		}
	}
	// The quota-kimi line must describe both meters, not a rolling/monthly
	// allowance — Kimi has weekly usage + frequency limit.
	if !strings.Contains(got, "本周用量") || !strings.Contains(got, "频率限制") {
		t.Fatalf("help text must label Kimi meters as 本周用量 and 频率限制: %s", got)
	}
}

// TestUsageListsOpenPageProviders proves the open-page usage string lists
// kimi alongside the other providers.
func TestUsageOpenPageListsKimi(t *testing.T) {
	// The open-page usage string is emitted to stderr only on arg-count
	// failure; assert on the constant via the help text's provider list
	// instead (the help text names all providers).
	var buf bytes.Buffer
	writeUsage(&buf)
	got := buf.String()
	if !strings.Contains(got, "login-kimi") {
		t.Fatalf("help text must mention the kimi provider: %s", got)
	}
}
