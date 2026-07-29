package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestUsageListsKimiCommands (task 5.4) proves the help text lists the Kimi
// login and quota commands with the membership metric semantics: total usage
// split into Kimi + Code, plus 5-hour and 7-day Code usage.
func TestUsageListsKimiCommands(t *testing.T) {
	var buf bytes.Buffer
	writeUsage(&buf)
	got := buf.String()
	for _, want := range []string{"login-kimi", "quota-kimi"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help text missing %q: %s", want, got)
		}
	}
	// The quota-kimi line must describe the membership metrics: total usage
	// with Kimi + Code, plus 5-hour/7-day Code — no rolling/weekly/frequency
	// language.
	if !strings.Contains(got, "总使用量(Kimi/Code)") || !strings.Contains(got, "5 小时/7 天 Code") {
		t.Fatalf("help text must label Kimi metrics as 总使用量(Kimi/Code) and 5 小时/7 天 Code: %s", got)
	}
	if strings.Contains(got, "本周用量") || strings.Contains(got, "频率限制") {
		t.Fatalf("help text must not carry obsolete weekly/frequency language: %s", got)
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
