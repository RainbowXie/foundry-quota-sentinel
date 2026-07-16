package sidebar

import "testing"

func TestRedactedOllamaCookieSummaryNeverIncludesValues(t *testing.T) {
	got := redactedOllamaCookieSummary([]ollamaCookieMeta{{Name: "__Secure-session", ValueLength: 400}, {Name: "aid", ValueLength: 36}})
	if got != "__Secure-session(len=400),aid(len=36)" {
		t.Fatalf("summary = %q", got)
	}
}
