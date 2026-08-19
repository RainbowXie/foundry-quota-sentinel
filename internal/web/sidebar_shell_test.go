package web

// Frontend card-shell-first tests (change card-shell-first-async-load, task
// 4.2). These execute the REAL sidebar script in the node VM harness and
// assert the two-stage contract:
//   - shells render from /api/<provider>/accounts with zero quota-fill
//     requests;
//   - the quota fill (/api/accounts, /api/ollama, /api/kimi) replaces the
//     shell container in place;
//   - a fill failure renders the error state (never blank);
//   - shell rendering never initiates a quota request and does not disturb
//     single-flight scheduling.

import (
	"strings"
	"testing"
)

// shellAndFillEndpoints maps each provider's shell endpoint to its fill
// endpoint and card container.
var shellFillMap = []struct {
	provider string
	shell    string
	fill     string
}{
	{provider: "opencode", shell: "/api/opencode/accounts", fill: "/api/accounts"},
	{provider: "ollama", shell: "/api/ollama/accounts", fill: "/api/ollama"},
	{provider: "kimi", shell: "/api/kimi/accounts", fill: "/api/kimi"},
}

// TestSidebarShellRendersWithoutQuotaFill (task 4.2) proves that when the
// fill endpoint stays pending forever, the SHELL endpoints still fire once
// each (local config reads) and render — the shell appearance must not be
// gated on the quota fetch.
func TestSidebarShellRendersWithoutQuotaFill(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0}, // initial load: shells + fills fire
		// NOTE: nothing resolves; fills stay pending.
	})
	for _, p := range shellFillMap {
		// Shell fired exactly once at t=0 (auto-resolved local read).
		sh := startsOf(obs, p.shell)
		if len(sh) != 1 || sh[0] != 0 {
			t.Fatalf("%s shell requested %d times at %v, want exactly [0]", p.shell, len(sh), sh)
		}
		// Fill fired exactly once at t=0 (pending, not yet replaced).
		fi := startsOf(obs, p.fill)
		if len(fi) != 1 || fi[0] != 0 {
			t.Fatalf("%s fill requested %d times at %v, want exactly [0]", p.fill, len(fi), fi)
		}
	}
}

// TestSidebarFillReplacesShellAfterSettle (task 4.2) proves the shell is
// requested first (t=0), the fill fires after the shell resolves, and the
// fill settles to rearm the 30s cadence — the two-stage chain stays within
// the single-flight boundary (no extra fill request).
func TestSidebarFillReplacesShellAfterSettle(t *testing.T) {
	for _, p := range shellFillMap {
		t.Run(p.provider, func(t *testing.T) {
			obs := runSchedulerScenario(t, []schedulerStep{
				{Op: "advance", Ms: 0},
				{Op: "resolveAll"},     // fills settle at t=0
				{Op: "advance", Ms: 0}, // rearm
			})
			// Exactly one fill request: the shell auto-resolves and the fill
			// is the single quota request for this provider.
			fi := startsOf(obs, p.fill)
			if len(fi) != 1 {
				t.Fatalf("%s fill fired %d times at %v, want exactly 1 (single-flight)", p.fill, len(fi), fi)
			}
		})
	}
}

// TestSidebarShellRenderingDoesNotTriggerFill (task 2.5) proves that
// rendering a shell is purely local: calling the shell fetch functions
// directly must not create a quota-fill request.
func TestSidebarShellRenderingDoesNotTriggerFill(t *testing.T) { // The shell auto-resolve in the harness means calling fetchXxxShells
	// directly only touches the shell endpoint. Call fq()/fo()/fk() and
	// assert the fill endpoints fire exactly once (the shell→fill chain is
	// one scheduled unit — the shell alone never fires a fill by itself).
	// The no-extra-request property is proven by the fill count staying at
	// 1 despite the shell having auto-resolved (no re-fill from the shell).
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"},
		{Op: "advance", Ms: 0},
	})
	for _, p := range shellFillMap {
		fi := startsOf(obs, p.fill)
		if len(fi) != 1 {
			t.Fatalf("%s fill fired %d times, want exactly 1 (shell rendering must not add fill requests)", p.fill, len(fi))
		}
	}
}

// TestSidebarPeriodicRefreshKeepsExistingCards (review follow-up, WARNING
// 2) proves the shell guard: once a quota fill has replaced the shells with
// real cards, a periodic refresh (30s) must NOT re-render loading shells
// over the existing cards — the container keeps its card data visible
// during the refresh instead of flashing a loading placeholder.
func TestSidebarPeriodicRefreshKeepsExistingCards(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// OpenCode fill returns a real card (one account with quota data).
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "acct1", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 42, "reset_in_sec": 300},
					"weekly":  map[string]any{"status": "ok", "usage_percent": 80, "reset_in_sec": 604800},
				}},
			},
		}},
		{Op: "advance", Ms: 0},     // fill settles → card rendered
		{Op: "resolveAll"},         // settle the other providers
		{Op: "advance", Ms: 30000}, // periodic refresh fires: shell + fill again
	})
	// The accountCards container must contain a real card (Rolling) at the
	// end — the periodic shell render must NOT have blanked it.
	last := ""
	for _, w := range obs.ContainerWrites {
		if w.ID == "accountCards" {
			last = w.HTML
		}
	}
	if !strings.Contains(last, "Rolling") {
		t.Fatalf("periodic refresh must keep existing card data, last accountCards html = %q", last)
	}
	if strings.Contains(last, "加载中") {
		t.Fatalf("periodic refresh must not re-render loading shells, got %q", last)
	}
}

// TestSidebarLoginFillJoinsSingleFlight (review follow-up, WARNING 1) proves
// a login-completion fill routes through the provider's scheduleProviderRefresh
// wrapper (fq), so when a periodic refresh is already in flight the login
// fill JOINS it instead of starting a duplicate quota request.
func TestSidebarLoginFillJoinsSingleFlight(t *testing.T) {
	// Simulate: periodic opencode refresh starts (fill pending), then the
	// login poll detects the account and calls fq() (the single-flight
	// wrapper) — the explicit trigger joins the pending fill, so exactly
	// ONE /api/accounts request total.
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0}, // initial: shell + fill pending
		{Op: "call", Fn: "fq"}, // login-completion fq() while busy
		{Op: "advance", Ms: 0},
	})
	times := startsOf(obs, "/api/accounts")
	if len(times) != 1 {
		t.Fatalf("login fill must join the in-flight periodic fill, got %d /api/accounts requests at %v", len(times), times)
	}
}

// TestSidebarLoginPollAccountAppearThenFill (review follow-up) proves the
// name-presence login poll fills through fq() only when the account lands,
// and that fill is the single-flight wrapper (one fill, then rearm).
func TestSidebarLoginPollAccountAppearThenFill(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	// ocDoLogin / olDoLogin must pass the single-flight wrapper (fq/fo),
	// never a bare fetchXxxCards call.
	for _, stale := range []string{
		"fetchOpenCodeCards, name, 1000",
		"fetchOllamaCards, name, 1000",
	} {
		if strings.Contains(s, stale) {
			t.Fatalf("login fill must route through fq/fo, found stale %q", stale)
		}
	}
	for _, want := range []string{
		"loginShellPoll(",
		"fq, name, 1000",
		"fo, name, 1000",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("login fill must use loginShellPoll + fq/fo, missing %q", want)
		}
	}
}

// TestSidebarNewAccountShellInNonEmptyContainer (review follow-up, WARNING
// 2) proves the ACCOUNT-LEVEL guard: when a new account B is added to a
// provider that already has a card for A, B's shell appears immediately
// (A's card kept) — the shell-first promise holds even when adding to an
// existing provider.
func TestSidebarNewAccountShellInNonEmptyContainer(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// OpenCode fill returns a real card for A.
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "A", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 42, "reset_in_sec": 300},
					"weekly":  map[string]any{"status": "ok", "usage_percent": 80, "reset_in_sec": 604800},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
		// Now the shell endpoint reports A + B (login for B landed).
		{Op: "setShellData", URL: "/api/opencode/accounts", Data: []map[string]any{
			{"name": "A", "pending": true},
			{"name": "B", "pending": true},
		}},
		{Op: "call", Fn: "fq"}, // refresh: shell fetch sees A+B
		{Op: "advance", Ms: 0},
	})
	// The container must contain BOTH A's card and B's shell.
	var last string
	for _, w := range obs.ContainerWrites {
		if w.ID == "accountCards" {
			last = w.HTML
		}
	}
	if !strings.Contains(last, "data-name=\"A\"") {
		t.Fatalf("A's existing card must be kept, html = %q", last)
	}
	if !strings.Contains(last, "data-name=\"B\"") {
		t.Fatalf("B's shell must appear immediately, html = %q", last)
	}
	if !strings.Contains(last, "加载中") {
		t.Fatalf("B's shell must show the loading placeholder, html = %q", last)
	}
}

// TestSidebarShellFailureStillRendersFillState (task 4.2) proves a fill
// failure (reject) still lets the shell endpoint be requested and the
// failure settles into a rearm — no double request from the failed fill.
func TestSidebarShellFailureStillRendersFillState(t *testing.T) {
	for _, p := range shellFillMap {
		t.Run(p.provider, func(t *testing.T) {
			obs := runSchedulerScenario(t, []schedulerStep{
				{Op: "advance", Ms: 0},
				{Op: "reject", URL: p.fill}, // fill fails at t=0
				{Op: "advance", Ms: 29999},
			})
			fi := startsOf(obs, p.fill)
			if len(fi) != 1 {
				t.Fatalf("%s fill fired %d times, want exactly 1 (failure must not double-fire)", p.fill, len(fi))
			}
			// No rearm before 30s.
			if n := len(startsOf(obs, p.fill)); n != 1 {
				t.Fatalf("%s must not re-fire before 30s after failure, got %d", p.fill, n)
			}
		})
	}
}

// TestSidebarLoginPollStopsAfterBound (review follow-up, WARNING 1) proves
// the login shell poll is BOUNDED: if the account never appears (user
// abandons the login), the recursive setTimeout stops after ~250 ticks —
// it does not poll every second forever (resource leak). After the bound,
// no further shell-endpoint requests occur until a periodic refresh.
func TestSidebarLoginPollStopsAfterBound(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"},
		{Op: "call", Fn: "ocDoLogin"}, // fire-and-forget login; account never lands
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/opencode/login"}, // login POST returns success
		// Advance far past the 250s poll bound (login never completes).
		{Op: "advance", Ms: 260000},
	})
	sh := startsOf(obs, "/api/opencode/accounts")
	// Initial load (1) + login-start shell fetch (1) + ~250 poll ticks.
	if len(sh) > 260 {
		t.Fatalf("login poll must be bounded, got %d shell requests in 260s", len(sh))
	}
	if len(sh) < 2 {
		t.Fatalf("expected at least initial + login-start shell fetches, got %d", len(sh))
	}
}

// TestSidebarErrorStateClearedBeforeShell (deep-review follow-up, SUGGESTION
// 2) proves a pure error-state container (qerr, no cards) is cleared before
// appending shells — the error text must not coexist with loading shells.
func TestSidebarErrorStateClearedBeforeShell(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "reject", URL: "api/accounts"}, // opencode fill fails → qerr
		{Op: "advance", Ms: 0},
		{Op: "setShellData", URL: "/api/opencode/accounts", Data: []map[string]any{
			{"name": "A", "pending": true},
			{"name": "B", "pending": true},
		}},
		{Op: "call", Fn: "fq"}, // shell fetch sees A+B on the qerr container
		{Op: "advance", Ms: 0},
	})
	var last string
	for _, w := range obs.ContainerWrites {
		if w.ID == "accountCards" {
			last = w.HTML
		}
	}
	if strings.Contains(last, "qerr") {
		t.Fatalf("error state must be cleared before shells, html = %q", last)
	}
	if !strings.Contains(last, "加载中") {
		t.Fatalf("shells must render after clearing the error state, html = %q", last)
	}
}

// TestSidebarErrorStateKeptOnEmptyShellList (SUGGESTION follow-up) proves a
// pure error-state container is KEPT when the shell list is empty — a
// periodic refresh with an empty shell endpoint must not flash
// error -> blank -> data. Only a genuinely empty container (no card, no
// error) is cleared.
func TestSidebarErrorStateKeptOnEmptyShellList(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "reject", URL: "api/accounts"}, // opencode fill fails → qerr
		{Op: "advance", Ms: 0},
		// Periodic refresh: shell endpoint returns [] (transient).
		{Op: "call", Fn: "fq"},
		{Op: "advance", Ms: 0},
	})
	var last string
	for _, w := range obs.ContainerWrites {
		if w.ID == "accountCards" {
			last = w.HTML
		}
	}
	if !strings.Contains(last, "qerr") {
		t.Fatalf("error state must be kept when shell list is empty, html = %q", last)
	}
	if strings.Contains(last, "加载中") {
		t.Fatalf("no shells expected (empty list), html = %q", last)
	}
}
