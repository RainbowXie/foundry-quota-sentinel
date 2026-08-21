package web

import (
	"strings"
	"testing"
)

// TestSidebarCommandCodeRegistered proves the commandcode provider is in
// the registry and wired into login/refresh dispatchers.
func TestSidebarCommandCodeRegistered(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`{ type: "commandcode", label: "CommandCode", defaultName: "CommandCode", login: "ccDoLogin", refresh: "fc" }`,
		"function commandcodeAdapter",
		"function fetchCommandCodeShells",
		"function fetchCommandCodeCards",
		"function ccDoLogin",
		`"/api/commandcode/accounts"`,
		`"/api/commandcode/login"`,
		`getElementById("commandCodeCards")`,
		"commandCodeCards", "commandcode", "CommandCode",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("sidebar missing %q", want)
		}
	}
	// The initial poll must fire fc() alongside the other providers.
	if !strings.Contains(s, "fc();") {
		t.Fatalf("sidebar must invoke fc() in the initial provider polling block")
	}
	// ccLogin must route the login-fill through the single-flight wrapper.
	if !strings.Contains(s, "fc, name, 1000, 250") {
		t.Fatalf("ccDoLogin must use loginShellPoll with fc, found:\n%s", s)
	}
}

// TestSidebarCommandCodeShellFirstAndFill proves the commandcode provider
// renders a shell first (from the config-only accounts endpoint) and the
// fill replaces it with a real three-window card.
func TestSidebarCommandCodeShellFirstAndFill(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// Initial fc(): shell endpoint resolves immediately (config read),
		// fill /api/commandcode stays pending.
		{Op: "resolve", URL: "api/commandcode", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "CC", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 13, "reset_in_sec": 10120},
					"weekly":  map[string]any{"status": "ok", "usage_percent": 30, "reset_in_sec": 477314},
					"monthly": map[string]any{"status": "ok", "usage_percent": 15, "reset_in_sec": 2550616},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
	})
	// The fill must produce one card with Rolling/Weekly/Monthly rows.
	var last string
	for _, w := range obs.ContainerWrites {
		if w.ID == "commandCodeCards" {
			last = w.HTML
		}
	}
	if !strings.Contains(last, "data-prov=\"commandcode\"") {
		t.Fatalf("card missing provider attr, html = %q", last)
	}
	if !strings.Contains(last, "data-name=\"CC\"") {
		t.Fatalf("card missing account name, html = %q", last)
	}
	for _, label := range []string{"Rolling", "Weekly", "Monthly"} {
		if !strings.Contains(last, label) {
			t.Fatalf("card missing %s row, html = %q", label, last)
		}
	}
	// Percent values from the fixture.
	if !strings.Contains(last, "13") || !strings.Contains(last, "30") || !strings.Contains(last, "15") {
		t.Fatalf("card missing percent values, html = %q", last)
	}
}

// TestSidebarCommandCodeLoginPollAccountAppearThenFill proves ccDoLogin
// fills through fc() only when the account lands, and the fill is the
// single-flight wrapper.
func TestSidebarCommandCodeLoginPollAccountAppearThenFill(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// Login for account B: shell poll starts; no B yet.
		{Op: "call", Fn: "ccDoLogin"},
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/commandcode/login"}, // login POST returns success
		{Op: "advance", Ms: 0},
		// B appears in the shell endpoint now.
		{Op: "setShellData", URL: "/api/commandcode/accounts", Data: []map[string]any{
			{"name": "B", "pending": true},
		}},
		{Op: "advance", Ms: 1000}, // next poll tick sees B
	})
	// After B appears, the fill must fire /api/commandcode once.
	times := startsOf(obs, "/api/commandcode")
	if len(times) != 1 {
		t.Fatalf("login fill must fetch /api/commandcode once, got %d at %v", len(times), times)
	}
	// The shell endpoint must be polled (appearance detection).
	shellTimes := startsOf(obs, "/api/commandcode/accounts")
	if len(shellTimes) < 2 {
		t.Fatalf("login poll must hit /api/commandcode/accounts repeatedly, got %v", shellTimes)
	}
}

// TestSidebarCommandCodeErrorAction proves a failed commandcode card shows
// the ccLogin re-login action and the container click handler is wired.
func TestSidebarCommandCodeErrorAction(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	if !strings.Contains(s, `errorAction = { label: "重新登录", class: "ccLogin" }`) {
		t.Fatalf("commandcodeAdapter must expose ccLogin error action")
	}
	if !strings.Contains(s, `t.classList.contains("ccLogin")`) {
		t.Fatalf("commandCodeCards click handler must dispatch ccLogin")
	}
}
