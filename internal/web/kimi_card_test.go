package web

// Focused presentation tests for the Kimi sidebar card
// (openspec change fix-kimi-card-layout). Two layers:
//
//  1. Structural assertions on the embedded sidebar.html (pure Go).
//  2. Rendering assertions that EXECUTE the real in-page renderer
//     (qesc/kpct/krow/ktotal/kcard) via node with a stubbed DOM, using
//     distinct synthetic values so a swapped mapping cannot pass. The
//     node layer skips when node is unavailable.
//
// The renderer executes the exact <script> block shipped to the browser —
// substring-only assertions are insufficient for the value mapping
// (design decision: "Test rendered contracts rather than only token
// presence").

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readSidebarHTML loads the embedded sidebar page under test.
func readSidebarHTML(t *testing.T) string {
	t.Helper()
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(html)
}

// kimiNodeHarness is the node driver that evals the real inline <script>
// (the block defining kcard) with a stubbed browser environment and then
// renders one card DTO, printing the resulting markup to stdout.
const kimiNodeHarness = `
const fs = require("fs");
const vm = require("vm");
const html = fs.readFileSync(process.argv[2], "utf8");
const blocks = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)]
	.map((m) => m[1])
	.filter((s) => s.includes("function kcard"));
if (!blocks.length) {
	console.error("kcard script block not found");
	process.exit(2);
}
// A universal proxy absorbs every browser object the script touches at
// top level (document, echarts, header wiring, modals, context menu...).
function universal() {
	const fn = function () { return pr; };
	const pr = new Proxy(fn, {
		get(t, k) {
			if (k === Symbol.toPrimitive) return () => "";
			return pr;
		},
		apply() { return pr; },
		set() { return true; },
	});
	return pr;
}
const pr = universal();
const sandbox = {
	console,
	document: pr,
	window: pr,
	echarts: pr,
	fetch: () => new Promise(() => {}),
	alert: () => {},
	confirm: () => false,
	setInterval: () => 0,
	setTimeout: () => 0,
	clearTimeout: () => 0,
	clearInterval: () => 0,
	requestAnimationFrame: () => 0,
	localStorage: { getItem: () => null, setItem: () => {} },
};
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
vm.runInContext(blocks[0], sandbox);
const dto = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
process.stdout.write(sandbox.kcard(dto));
`

// renderKimiCard executes the real renderer against dto and returns the
// card markup. Skips when node is not installed.
func renderKimiCard(t *testing.T, dto map[string]any) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable: skipping renderer-execution test")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.cjs")
	if err := os.WriteFile(harness, []byte(kimiNodeHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	dtoPath := filepath.Join(dir, "dto.json")
	if err := os.WriteFile(dtoPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sidebarPath, err := filepath.Abs(filepath.Join("static", "sidebar.html"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harness, sidebarPath, dtoPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node renderer harness failed: %v\n%s", err, out)
	}
	return string(out)
}

// kimiSuccessDTO returns a card DTO with DISTINCT values per window so any
// swapped or reused mapping is observable in the rendered output.
func kimiSuccessDTO() map[string]any {
	return map[string]any{
		"name":    "synthetic-account",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 7.9, "reset_display": "RESET-5H", "reset_in_sec": 18000},
			"seven_day": map[string]any{"usage_percent": 56.45, "reset_display": "RESET-7D", "reset_in_sec": 172800},
			"total": map[string]any{
				"total_percent": 11.86,
				"kimi_percent":  0.03,
				"code_percent":  11.83,
				"reset_display": "RESET-TOTAL",
				"reset_in_sec":  1987200,
			},
		},
	}
}

// qrSegments splits rendered card markup into its quota-row segments.
// Accepts both unquoted (<div class=qr) and quoted (<div class="qr") forms.
func qrSegments(html string) []string {
	parts := strings.Split(html, "<div class=\"qr\"")
	if len(parts) == 1 {
		parts = strings.Split(html, "<div class=qr")
	}
	segs := make([]string, 0, len(parts))
	for _, p := range parts[1:] {
		segs = append(segs, p)
	}
	return segs
}

// --- 1.2 grid parity ---

// TestKimiCardsShareResponsiveGrid proves #kimiCards is governed by the
// SAME responsive grid container rule and the SAME direct-child margin
// rule as the OpenCode Go card container (#accountCards): one shared
// selector group, not a Kimi-only width contract (design: layouts must
// not be allowed to drift again).
func TestKimiCardsShareResponsiveGrid(t *testing.T) {
	html := readSidebarHTML(t)
	containerRule := regexp.MustCompile(`(?s)([^{}]*#accountCards[^{}]*)\{[^}]*repeat\(auto-fill,\s*minmax\(320px,\s*1fr\)\)`)
	m := containerRule.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("shared account-card grid rule (repeat(auto-fill, minmax(320px, 1fr))) not found")
	}
	if !strings.Contains(m[1], "#kimiCards") {
		t.Fatalf("#kimiCards is not part of the shared responsive grid selector group; selector group was:\n%s", strings.TrimSpace(m[1]))
	}
	childRule := regexp.MustCompile(`(?s)([^{}]*#accountCards\s*>\s*\*[^{}]*)\{`)
	cm := childRule.FindStringSubmatch(html)
	if cm == nil {
		t.Fatal("shared direct-child margin rule (#accountCards > *) not found")
	}
	if !regexp.MustCompile(`#kimiCards\s*>\s*\*`).MatchString(cm[1]) {
		t.Fatalf("#kimiCards > * is not part of the shared direct-child margin selector group; selector group was:\n%s", strings.TrimSpace(cm[1]))
	}
}

// --- 1.3 distinct-value mapping ---

// TestKimiCardRendersRollingWeeklyMonthlyMapping executes the renderer
// with distinct synthetic values and proves the primary row ORDER and
// exact field mapping: five_hour → Rolling, seven_day → Weekly,
// total → Monthly (value + reset each). Old provider-specific Chinese
// labels must be gone from a successful card.
func TestKimiCardRendersRollingWeeklyMonthlyMapping(t *testing.T) {
	out := renderKimiCard(t, kimiSuccessDTO())

	iRolling := strings.Index(out, "Rolling")
	iWeekly := strings.Index(out, "Weekly")
	iMonthly := strings.Index(out, "Monthly")
	if iRolling < 0 || iWeekly < 0 || iMonthly < 0 {
		t.Fatalf("successful Kimi card must render Rolling/Weekly/Monthly rows; got:\n%s", out)
	}
	if !(iRolling < iWeekly && iWeekly < iMonthly) {
		t.Fatalf("row order must be Rolling, Weekly, Monthly (idx %d, %d, %d); got:\n%s", iRolling, iWeekly, iMonthly, out)
	}

	segs := qrSegments(out)
	if len(segs) < 3 {
		t.Fatalf("expected at least 3 quota rows, got %d; markup:\n%s", len(segs), out)
	}
	rowFor := func(label string) string {
		for _, s := range segs {
			if strings.Contains(s, ">"+label+"<") || strings.HasPrefix(strings.TrimSpace(s), ">"+label) {
				return s
			}
		}
		// fall back to a looser label match within the row
		for _, s := range segs {
			if strings.Contains(s, label) {
				return s
			}
		}
		return ""
	}
	rolling, weekly, monthly := rowFor("Rolling"), rowFor("Weekly"), rowFor("Monthly")
	if rolling == "" || weekly == "" || monthly == "" {
		t.Fatalf("could not locate all three labeled rows; segments: %#v", segs)
	}
	expect := map[string][2]string{
		"Rolling": {"7.9%", "5h"},
		"Weekly":  {"56.45%", "2d"},
		"Monthly": {"11.86%", "23d"},
	}
	rows := map[string]string{"Rolling": rolling, "Weekly": weekly, "Monthly": monthly}
	for label, want := range expect {
		if !strings.Contains(rows[label], want[0]) {
			t.Fatalf("%s row must display its own percentage %s (distinct-value mapping); row: %s", label, want[0], rows[label])
		}
		if !strings.Contains(rows[label], want[1]) {
			t.Fatalf("%s row must display its own reset %s; row: %s", label, want[1], rows[label])
		}
	}

	for _, stale := range []string{"总使用量", "5 小时用量", "7 天用量"} {
		if strings.Contains(out, stale) {
			t.Fatalf("successful Kimi card must not keep provider-specific label %q; got:\n%s", stale, out)
		}
	}
}

// --- 1.4 Monthly breakdown + decimal trimming ---

// TestKimiCardRendersMonthlyKimiCodeBreakdown proves the separate decimal
// Kimi and Code contributions render as secondary text directly below the
// Monthly row, via a dedicated presentation class, with only unnecessary
// trailing zeros trimmed.
func TestKimiCardRendersMonthlyKimiCodeBreakdown(t *testing.T) {
	out := renderKimiCard(t, kimiSuccessDTO())

	iMonthly := strings.Index(out, "Monthly")
	iKimi := strings.Index(out, "Kimi 0.03%")
	iCode := strings.Index(out, "Code 11.83%")
	if iMonthly < 0 || iKimi < 0 || iCode < 0 {
		t.Fatalf("Monthly breakdown must render as 'Kimi 0.03%%' and 'Code 11.83%%'; got:\n%s", out)
	}
	if !(iMonthly < iKimi && iMonthly < iCode) {
		t.Fatalf("breakdown must appear below the Monthly row (Monthly idx %d, Kimi idx %d, Code idx %d)", iMonthly, iKimi, iCode)
	}

	// Trimming: 11.00 → "11%", 7.90 → "7.9%", 0 → "0%", two-decimal values
	// preserved exactly.
	dto := kimiSuccessDTO()
	q := dto["quota"].(map[string]any)
	q["five_hour"] = map[string]any{"usage_percent": 7.90, "reset_display": "R1", "reset_in_sec": 3600}
	q["seven_day"] = map[string]any{"usage_percent": 0.0, "reset_display": "R2", "reset_in_sec": 86400}
	q["total"] = map[string]any{"total_percent": 11.0, "kimi_percent": 0.0, "code_percent": 11.0, "reset_display": "R3", "reset_in_sec": 172800}
	out2 := renderKimiCard(t, dto)
	for _, want := range []string{"7.9%", "11%", "Kimi 0%", "Code 11%"} {
		if !strings.Contains(out2, want) {
			t.Fatalf("trimmed decimal %q missing from rendered card:\n%s", want, out2)
		}
	}
	for _, bad := range []string{"7.90%", "11.0%", "11.00%", "0.00%"} {
		if strings.Contains(out2, bad) {
			t.Fatalf("unnecessary trailing zeros must be trimmed; found %q in:\n%s", bad, out2)
		}
	}
}

// --- 1.5 (revised) truthful Monthly fill ---

// TestKimiCardMonthlySingleTruthfulFill proves the Monthly track contains
// exactly ONE progress fill whose width comes from total.total_percent —
// not separate vertically-stacked Kimi/Code fills that clip inside the
// fixed-height track and make a non-zero total look empty. The contributor
// values appear only in the secondary text breakdown.
func TestKimiCardMonthlySingleTruthfulFill(t *testing.T) {
	dto := kimiSuccessDTO()
	q := dto["quota"].(map[string]any)
	q["total"] = map[string]any{"total_percent": 11.92, "kimi_percent": 0.03, "code_percent": 11.89, "reset_display": "RESET-TOTAL", "reset_in_sec": 1987200}
	out := renderKimiCard(t, dto)

	segs := qrSegments(out)
	var monthly string
	for _, s := range segs {
		if strings.Contains(s, "Monthly") {
			monthly = s
			break
		}
	}
	if monthly == "" {
		t.Fatalf("Monthly row missing; got:\n%s", out)
	}
	fills := regexp.MustCompile(`class="qf[^"]*"`).FindAllString(monthly, -1)
	if len(fills) != 1 {
		t.Fatalf("Monthly track must contain exactly one progress fill, got %d (%v); row: %s", len(fills), fills, monthly)
	}
	if !strings.Contains(monthly, "width:11.92%") {
		t.Fatalf("the single Monthly fill must be width:11.92%% (total.total_percent); row: %s", monthly)
	}
	for _, contributor := range []string{"width:0.03%", "width:11.89%"} {
		if strings.Contains(out, contributor) {
			t.Fatalf("Kimi/Code contributor values must not be rendered as progress fills (%s); got:\n%s", contributor, out)
		}
	}
	// Text breakdown stays.
	if !strings.Contains(out, "Kimi 0.03%") || !strings.Contains(out, "Code 11.89%") {
		t.Fatalf("secondary text breakdown must remain below Monthly; got:\n%s", out)
	}

	// Zero total: fill width 0%, row and value still present.
	q["total"] = map[string]any{"total_percent": 0.0, "kimi_percent": 0.0, "code_percent": 0.0, "reset_display": "RESET-TOTAL", "reset_in_sec": 1987200}
	out0 := renderKimiCard(t, dto)
	if !strings.Contains(out0, "Monthly") || !strings.Contains(out0, "0%") {
		t.Fatalf("zero-total Monthly row must remain present with a formatted value; got:\n%s", out0)
	}
	segs0 := qrSegments(out0)
	var m0 string
	for _, s := range segs0 {
		if strings.Contains(s, "Monthly") {
			m0 = s
			break
		}
	}
	if m0 == "" || !strings.Contains(m0, "width:0%") {
		t.Fatalf("zero-total Monthly fill must be width:0%%; row: %s", m0)
	}
}

// --- 1.6 (revised) booster absence ---

// TestKimiCardOmitsBoosterAffordance proves neither the successful nor the
// error-state Kimi card renders 购买加油包 / a kimiAddon action, that no
// kimiAddon CSS or delegated click branch remains, and that the UNRELATED
// generic account-page flow (context menu → openPage) stays intact.
func TestKimiCardOmitsBoosterAffordance(t *testing.T) {
	out := renderKimiCard(t, kimiSuccessDTO())
	for _, banned := range []string{"购买加油包", "kimiAddon"} {
		if strings.Contains(out, banned) {
			t.Fatalf("successful Kimi card must not render %q; got:\n%s", banned, out)
		}
	}
	errOut := renderKimiCard(t, map[string]any{"name": "broken-account", "success": false, "error": "凭证已过期，请重新登录"})
	for _, banned := range []string{"购买加油包", "kimiAddon"} {
		if strings.Contains(errOut, banned) {
			t.Fatalf("error-state Kimi card must not render %q; got:\n%s", banned, errOut)
		}
	}
	if !strings.Contains(errOut, "kimiLogin") {
		t.Fatalf("error-state card must keep its kimiLogin re-login affordance; got:\n%s", errOut)
	}

	html := readSidebarHTML(t)
	if strings.Contains(html, "kimiAddon") {
		t.Fatal("no kimiAddon markup, CSS, or delegated click branch may remain in the sidebar")
	}
	// The generic context-menu account-page action is unrelated wiring and
	// must remain: menu item + handler routing through openPage.
	if !strings.Contains(html, `id="ctxOpen"`) {
		t.Fatal("generic context-menu open item (ctxOpen) must remain")
	}
	if !regexp.MustCompile(`openItem\.addEventListener\("click"[\s\S]*?openPage\(cur\.prov, cur\.name\)`).MatchString(html) {
		t.Fatal("generic context-menu open action must keep routing through openPage(cur.prov, cur.name)")
	}
}

// TestOpenPageReachableFromGlobalScope (regression) proves openPage is
// declared at the script's TOP LEVEL and is callable — the pre-existing
// ctx-menu IIFE scoping left it invisible to top-level delegated listeners
// (card clicks died with a ReferenceError and showed "no reaction"). The
// fetch stub records calls so the assertion exercises the real call path,
// not just the symbol's presence. This guards the generic account-page
// path independently of any card-level affordance.
func TestOpenPageReachableFromGlobalScope(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable: skipping renderer-execution test")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.cjs")
	script := `
const fs = require("fs");
const vm = require("vm");
const html = fs.readFileSync(process.argv[2], "utf8");
const blocks = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)]
	.map((m) => m[1])
	.filter((s) => s.includes("function kcard"));
function universal() {
	const fn = function () { return pr; };
	const pr = new Proxy(fn, {
		get(t, k) { if (k === Symbol.toPrimitive) return () => ""; return pr; },
		apply() { return pr; },
		set() { return true; },
	});
	return pr;
}
const pr = universal();
const calls = [];
const sandbox = {
	console,
	document: pr,
	window: pr,
	echarts: pr,
	fetch: (u) => { calls.push(String(u)); return new Promise(() => {}); },
	alert: () => {},
	confirm: () => false,
	setInterval: () => 0,
	setTimeout: () => 0,
	clearTimeout: () => 0,
	clearInterval: () => 0,
	requestAnimationFrame: () => 0,
	localStorage: { getItem: () => null, setItem: () => {} },
};
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
vm.runInContext(blocks[0], sandbox);
if (typeof sandbox.openPage !== "function") {
	console.error("openPage is " + typeof sandbox.openPage + " — scoped inside the ctx-menu IIFE, unreachable from the kimiCards delegation");
	process.exit(3);
}
sandbox.openPage("kimi", "layout1");
setImmediate(() => {
	if (!calls.some((u) => u.includes("/api/open") && u.includes("provider=kimi") && u.includes("name=layout1"))) {
		console.error("openPage did not fetch /api/open for the named account; calls=" + JSON.stringify(calls));
		process.exit(4);
	}
	console.log("openPage reachable and fetches /api/open for the named account");
});
`
	if err := os.WriteFile(harness, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	sidebarPath, err := filepath.Abs(filepath.Join("static", "sidebar.html"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harness, sidebarPath).CombinedOutput()
	if err != nil {
		t.Fatalf("openPage must be a top-level callable that fetches the authenticated page route: %v\n%s", err, out)
	}
}

// --- 1.6 state preservation ---

// TestKimiCardErrorStateFabricatesNoMetrics renders a failed account and
// proves the error branch shows the actionable error with its re-login
// affordance and does NOT fabricate Rolling/Weekly/Monthly rows, a Monthly
// breakdown, or a booster action.
func TestKimiCardErrorStateFabricatesNoMetrics(t *testing.T) {
	dto := map[string]any{"name": "broken-account", "success": false, "error": "凭证已过期，请重新登录"}
	out := renderKimiCard(t, dto)

	if !strings.Contains(out, "qerr") || !strings.Contains(out, "凭证已过期，请重新登录") {
		t.Fatalf("error card must render the actionable error message; got:\n%s", out)
	}
	for _, fabricated := range []string{"Rolling", "Weekly", "Monthly", "kimiAddon", "购买加油包"} {
		if strings.Contains(out, fabricated) {
			t.Fatalf("error card must not fabricate %q; got:\n%s", fabricated, out)
		}
	}
	// The re-login affordance must stay present AND actually be wired: the
	// kimiCards delegated listener acts on .kimiLogin, so the affordance
	// must carry that class (a pre-existing bug rendered .olLogin, which
	// the kimi container never handles — a dead control).
	rel := regexp.MustCompile(`<(button|span)[^>]*kimiLogin[^>]*data-name="broken-account"[^>]*>`)
	if !rel.MatchString(out) {
		t.Fatalf("error card must keep a working kimiLogin re-login affordance for the account; got:\n%s", out)
	}
	if strings.Contains(out, "olLogin") {
		t.Fatal("Kimi error affordance must not reuse the ollama olLogin class — the kimiCards delegation never matches it (dead control)")
	}
}

// TestKimiRefreshAndDeleteWiringIntact is defined in delete_flow_test.go
// (moved there with expanded registry-driven delete-flow assertions for the
// unify-quota-card-template change).
