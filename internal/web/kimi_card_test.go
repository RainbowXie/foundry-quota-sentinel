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
			"five_hour": map[string]any{"usage_percent": 7.9, "reset_display": "RESET-5H"},
			"seven_day": map[string]any{"usage_percent": 56.45, "reset_display": "RESET-7D"},
			"total": map[string]any{
				"total_percent": 11.86,
				"kimi_percent":  0.03,
				"code_percent":  11.83,
				"reset_display": "RESET-TOTAL",
			},
		},
	}
}

// qrSegments splits rendered card markup into its quota-row segments.
func qrSegments(html string) []string {
	parts := strings.Split(html, "<div class=qr")
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
		"Rolling": {"7.9%", "RESET-5H"},
		"Weekly":  {"56.45%", "RESET-7D"},
		"Monthly": {"11.86%", "RESET-TOTAL"},
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
	q["five_hour"] = map[string]any{"usage_percent": 7.90, "reset_display": "R1"}
	q["seven_day"] = map[string]any{"usage_percent": 0.0, "reset_display": "R2"}
	q["total"] = map[string]any{"total_percent": 11.0, "kimi_percent": 0.0, "code_percent": 11.0, "reset_display": "R3"}
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

// --- 1.5 semantic interaction ---

// TestKimiBoosterIsSemanticKeyboardOperableControl proves 购买加油包 renders
// as a real <button type="button"> (native Enter/Space activation), scoped
// to the card's account, with themed hover and :focus-visible styles, and
// that the existing delegated handler routes it through openPage("kimi",
// name) — no purchase automation, no external unauthenticated link.
func TestKimiBoosterIsSemanticKeyboardOperableControl(t *testing.T) {
	out := renderKimiCard(t, kimiSuccessDTO())

	btn := regexp.MustCompile(`<button[^>]*kimiAddon[^>]*>`)
	loc := btn.FindString(out)
	if loc == "" {
		t.Fatalf("购买加油包 must render as <button ... class=kimiAddon ...>; got:\n%s", out)
	}
	if !strings.Contains(loc, `type="button"`) {
		t.Fatalf("booster button must declare type=\"button\"; got: %s", loc)
	}
	if !strings.Contains(loc, `data-name="synthetic-account"`) {
		t.Fatalf("booster button must carry the card account in data-name; got: %s", loc)
	}
	if !strings.Contains(out, ">购买加油包</button>") {
		t.Fatalf("booster label must be the un-nested button text 购买加油包; got:\n%s", out)
	}
	if strings.Contains(out, "<span class=kimiAddon") {
		t.Fatal("non-interactive span booster must be replaced by the semantic button")
	}
	if regexp.MustCompile(`kimiAddon[^>]*style=`).MatchString(out) {
		t.Fatal("booster styling must come from a themed class, not inline styles")
	}

	html := readSidebarHTML(t)
	for _, rule := range []string{`.kimiAddon:hover`, `.kimiAddon:focus-visible`, `.kimiAddon:active`} {
		if !strings.Contains(html, rule) {
			t.Fatalf("booster needs a themed %s rule (visible hover and keyboard-focus indicator)", rule)
		}
	}
	base := regexp.MustCompile(`(?s)\.kimiAddon\s*\{([^}]*)\}`)
	bm := base.FindStringSubmatch(html)
	if bm == nil || !strings.Contains(bm[1], "cursor") || !strings.Contains(bm[1], "pointer") {
		t.Fatal("booster base class must set an explicit pointer cursor")
	}
	if !strings.Contains(html, `openPage("kimi"`) {
		t.Fatal("booster activation must route through openPage(\"kimi\", name)")
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

// TestKimiRefreshAndDeleteWiringIntact proves the non-renderer behaviors
// the presentation change must not replace: periodic kimi refresh, the
// fetch-failure error path, and the shared account delete flow.
func TestKimiRefreshAndDeleteWiringIntact(t *testing.T) {
	html := readSidebarHTML(t)
	if !regexp.MustCompile(`fk\(\);\s*\n?\s*setInterval\(fk,`).MatchString(html) {
		t.Fatal("Kimi cards must keep their periodic refresh (fk(); setInterval(fk, ...))")
	}
	if !regexp.MustCompile(`if \(!r\.success\) throw`).MatchString(html) {
		t.Fatal("Kimi fetch failures must surface as card errors, not fabricated metrics")
	}
	if !strings.Contains(html, `id="ctxDelete"`) {
		t.Fatal("shared delete affordance (ctxDelete) must remain")
	}
	if !strings.Contains(html, "/api/delete") && !regexp.MustCompile(`/api/[a-z]+/delete`).MatchString(html) {
		t.Fatal("account delete endpoint wiring must remain")
	}
}
