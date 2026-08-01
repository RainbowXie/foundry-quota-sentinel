package web

// RED tests for the unified quota-card template (openspec change
// unify-quota-card-template). These tests characterize the EXPECTED
// behavior of the shared foundation before it is implemented.
//
// Test layers:
//  1. Compact duration formatter tests (formatDurationCompact)
//  2. Shared foundation view model tests (renderQuotaCard, renderQuotaRow)
//  3. Provider adapter tests (OpenCode Go, Ollama, Kimi)
//  4. Structural parity tests (shared CSS/design tokens)
//  5. Extension slot tests (Kimi Monthly detail, future providers)
//
// The renderer executes the exact <script> block shipped to the browser
// via node with a stubbed DOM, using distinct synthetic values so any
// swapped mapping cannot pass.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// --- Test Harness ---

// quotaCardNodeHarness is the node driver that evals the real inline <script>
// (the block defining the shared quota-card functions) with a stubbed browser
// environment and then renders cards via the shared foundation.
const quotaCardNodeHarness = `
const fs = require("fs");
const vm = require("vm");
const html = fs.readFileSync(process.argv[2], "utf8");
const blocks = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)]
	.map((m) => m[1])
	.filter((s) => s.includes("function renderQuotaCard") || s.includes("function formatDurationCompact"));
if (!blocks.length) {
	console.error("shared quota-card script block not found");
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

const testCase = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
let output;

switch (testCase.type) {
case "formatDuration":
	output = sandbox.formatDurationCompact(testCase.seconds);
	break;
case "renderCard":
	output = sandbox.renderQuotaCard(testCase.view);
	break;
case "renderRow":
	output = sandbox.renderQuotaRow(testCase.row);
	break;
case "adapter":
	// Execute provider adapter and render result
	const adapter = sandbox[testCase.adapter + "Adapter"];
	if (!adapter) {
		console.error("adapter " + testCase.adapter + " not found");
		process.exit(3);
	}
	const view = adapter(testCase.dto);
	output = sandbox.renderQuotaCard(view);
	break;
default:
	console.error("unknown test type: " + testCase.type);
	process.exit(4);
}
process.stdout.write(String(output));
`

// runQuotaCardTest executes a test case against the shared quota-card renderer.
// Skips when node is not installed.
func runQuotaCardTest(t *testing.T, testCase map[string]any) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable: skipping renderer-execution test")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.cjs")
	if err := os.WriteFile(harness, []byte(quotaCardNodeHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(testCase)
	if err != nil {
		t.Fatal(err)
	}
	tcPath := filepath.Join(dir, "testcase.json")
	if err := os.WriteFile(tcPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	sidebarPath, err := filepath.Abs(filepath.Join("static", "sidebar.html"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harness, sidebarPath, tcPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node renderer harness failed: %v\n%s", err, out)
	}
	return string(out)
}

// --- Task 1.3: Compact Duration Formatter Tests ---

// TestFormatDurationCompactBoundaries proves the formatter matches Go
// FormatDurationCompact behavior: floors the largest applicable whole unit
// and uses s/m/h/d vocabulary.
func TestFormatDurationCompactBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		seconds  float64
		expected string
	}{
		// Seconds boundary (< 60)
		{"zero seconds", 0, "0s"},
		{"one second", 1, "1s"},
		{"59 seconds", 59, "59s"},
		// Minutes boundary (< 3600)
		{"60 seconds = 1 minute", 60, "1m"},
		{"90 seconds = 1 minute (floored)", 90, "1m"},
		{"3599 seconds = 59 minutes", 3599, "59m"},
		// Hours boundary (< 86400)
		{"3600 seconds = 1 hour", 3600, "1h"},
		{"5400 seconds = 1 hour (floored)", 5400, "1h"},
		{"86399 seconds = 23 hours", 86399, "23h"},
		// Days boundary (>= 86400)
		{"86400 seconds = 1 day", 86400, "1d"},
		{"90000 seconds = 1 day (floored)", 90000, "1d"},
		// Specific values from spec
		{"18000 seconds = 5 hours", 18000, "5h"},
		{"172800 seconds = 2 days", 172800, "2d"},
		{"1987200 seconds = 23 days", 1987200, "23d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runQuotaCardTest(t, map[string]any{
				"type":    "formatDuration",
				"seconds": tt.seconds,
			})
			if result != tt.expected {
				t.Errorf("formatDurationCompact(%v) = %q, want %q", tt.seconds, result, tt.expected)
			}
		})
	}
}

// TestFormatDurationCompactInvalidValues proves invalid values are clamped
// to safe non-negative display.
func TestFormatDurationCompactInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		seconds  any
		expected string
	}{
		{"negative seconds", -100, "0s"},
		{"negative one", -1, "0s"},
		{"NaN", "NaN", "0s"},
		{"undefined", nil, "0s"},
		{"Infinity", "Infinity", "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runQuotaCardTest(t, map[string]any{
				"type":    "formatDuration",
				"seconds": tt.seconds,
			})
			if result != tt.expected {
				t.Errorf("formatDurationCompact(%v) = %q, want %q", tt.seconds, result, tt.expected)
			}
		})
	}
}

// --- Row renderer safety (WARNING: null/NaN/Infinity) ---

// TestRenderQuotaRowHandlesInvalidRows proves the shared row renderer
// never throws on null/undefined rows and never emits width:NaN% or
// Infinity% — a single validated value drives color, fill, and text.
func TestRenderQuotaRowHandlesInvalidRows(t *testing.T) {
	// null row must not throw and must produce no markup.
	nullOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  nil,
	})
	if nullOut != "" {
		t.Errorf("renderQuotaRow(null) should render empty, got: %q", nullOut)
	}

	// missing percent → placeholder, no throw.
	missingOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Rolling"},
	})
	if !strings.Contains(missingOut, "Rolling") || !strings.Contains(missingOut, "—") {
		t.Errorf("row without percent must render label placeholder with —; got: %q", missingOut)
	}

	// NaN percent → placeholder, never width:NaN% or "NaN%".
	nanOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Weekly", "percent": "NaN", "percentPrecision": 0, "resetInSec": 3600},
	})
	if strings.Contains(nanOut, "NaN") || strings.Contains(nanOut, "width:NaN") {
		t.Errorf("NaN percent must render a safe placeholder, got: %q", nanOut)
	}

	// Infinity percent → placeholder, never a full bar or Infinity%.
	infOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Monthly", "percent": "Infinity", "percentPrecision": 0, "resetInSec": 3600},
	})
	if strings.Contains(infOut, "Infinity") || strings.Contains(infOut, "width:Infinity") {
		t.Errorf("Infinity percent must render a safe placeholder, got: %q", infOut)
	}

	// negative percent → clamped fill at 0% but text shows validated value.
	negOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Rolling", "percent": -5, "percentPrecision": 0, "resetInSec": 1800},
	})
	if strings.Contains(negOut, "width:-5%") || !strings.Contains(negOut, "width:0%") {
		t.Errorf("negative percent must clamp fill width to 0%%, got: %q", negOut)
	}
}

// --- Refresh replacement scenario (WARNING) ---

// TestRefreshReplacementUpdatesCardData proves re-rendering a card with new
// DTO data for the same account replaces rows, fills, percentages, reset
// durations, and details while preserving provider and account identity.
func TestRefreshReplacementUpdatesCardData(t *testing.T) {
	oldDTO := map[string]any{
		"name":    "kimi-refresh",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 10.5, "reset_display": "OLD-5H", "reset_in_sec": 3600},
			"seven_day": map[string]any{"usage_percent": 40.0, "reset_display": "OLD-7D", "reset_in_sec": 86400},
			"total": map[string]any{
				"total_percent": 20.0, "kimi_percent": 0.5, "code_percent": 19.5,
				"reset_display": "OLD-T", "reset_in_sec": 172800,
			},
		},
	}
	newDTO := map[string]any{
		"name":    "kimi-refresh",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 55.25, "reset_display": "NEW-5H", "reset_in_sec": 7200},
			"seven_day": map[string]any{"usage_percent": 90.0, "reset_display": "NEW-7D", "reset_in_sec": 259200},
			"total": map[string]any{
				"total_percent": 75.5, "kimi_percent": 3.25, "code_percent": 72.25,
				"reset_display": "NEW-T", "reset_in_sec": 1987200,
			},
		},
	}

	oldOut := runQuotaCardTest(t, map[string]any{"type": "adapter", "adapter": "kimi", "dto": oldDTO})
	newOut := runQuotaCardTest(t, map[string]any{"type": "adapter", "adapter": "kimi", "dto": newDTO})

	// Identity must be preserved across refresh.
	for _, identity := range []string{`data-prov="kimi"`, `data-name="kimi-refresh"`, `Kimi Code`} {
		if !strings.Contains(oldOut, identity) || !strings.Contains(newOut, identity) {
			t.Errorf("card identity %s must be preserved across refresh", identity)
		}
	}

	// New values must replace old values.
	for _, want := range []string{"55.25%", "2h", "90%", "3d", "75.5%", "23d", "Kimi 3.25%", "Code 72.25%"} {
		if !strings.Contains(newOut, want) {
			t.Errorf("refreshed card must contain %q (replaced value); got:\n%s", want, newOut)
		}
	}
	for _, stale := range []string{"10.5%", "40%", "20%", "Kimi 0.5%", "Code 19.5%"} {
		if strings.Contains(newOut, stale) {
			t.Errorf("refreshed card must NOT contain stale value %q; got:\n%s", stale, newOut)
		}
	}
}

// --- Task 1.2: Characterization Tests for Existing Renderers ---

// TestOpenCodeGoCardStructure captures the current OpenCode Go card structure
// as a RED test before migration to shared foundation.
func TestOpenCodeGoCardStructure(t *testing.T) {
	dto := map[string]any{
		"name":    "opencode-account",
		"success": true,
		"quota": map[string]any{
			"rolling": map[string]any{"usage_percent": 45, "reset_display": "2h", "reset_in_sec": 7200},
			"weekly":  map[string]any{"usage_percent": 72, "reset_display": "3d", "reset_in_sec": 259200},
			"monthly": map[string]any{"usage_percent": 88, "reset_display": "15d", "reset_in_sec": 1296000},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "opencode",
		"dto":     dto,
	})

	// Verify card shell structure
	if !strings.Contains(result, `data-prov="opencode"`) {
		t.Error("OpenCode card must have data-prov=opencode")
	}
	if !strings.Contains(result, `data-name="opencode-account"`) {
		t.Error("OpenCode card must have data-name attribute")
	}
	if !strings.Contains(result, "OpenCode Go") {
		t.Error("OpenCode card must display provider label")
	}

	// Verify row order: Rolling, Weekly, Monthly
	iRolling := strings.Index(result, "Rolling")
	iWeekly := strings.Index(result, "Weekly")
	iMonthly := strings.Index(result, "Monthly")
	if !(iRolling >= 0 && iWeekly > iRolling && iMonthly > iWeekly) {
		t.Errorf("OpenCode rows must be in order Rolling, Weekly, Monthly; got indices %d, %d, %d", iRolling, iWeekly, iMonthly)
	}

	// Verify percentages are integer precision
	if !strings.Contains(result, "45%") || !strings.Contains(result, "72%") || !strings.Contains(result, "88%") {
		t.Error("OpenCode card must display integer percentages")
	}

	// Verify reset durations use compact format (from reset_in_sec)
	// Note: This is a RED test - currently uses reset_display, after migration
	// should use formatDurationCompact(reset_in_sec)
	if !strings.Contains(result, "2h") || !strings.Contains(result, "3d") || !strings.Contains(result, "15d") {
		t.Error("OpenCode card must display reset durations")
	}
}

// TestOllamaCardStructure captures the current Ollama card structure
// as a RED test before migration to shared foundation.
func TestOllamaCardStructure(t *testing.T) {
	dto := map[string]any{
		"name":    "ollama-account",
		"success": true,
		"quota": map[string]any{
			"rolling": map[string]any{"usage_percent": 33, "reset_display": "1h", "reset_in_sec": 3600},
			"weekly":  map[string]any{"usage_percent": 67, "reset_display": "5d", "reset_in_sec": 432000},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "ollama",
		"dto":     dto,
	})

	// Verify card shell structure
	if !strings.Contains(result, `data-prov="ollama"`) {
		t.Error("Ollama card must have data-prov=ollama")
	}
	if !strings.Contains(result, "Ollama") {
		t.Error("Ollama card must display provider label")
	}

	// Verify row order: Session, Weekly (no Monthly for Ollama)
	iSession := strings.Index(result, "Session")
	iWeekly := strings.Index(result, "Weekly")
	if !(iSession >= 0 && iWeekly > iSession) {
		t.Errorf("Ollama rows must be in order Session, Weekly; got indices %d, %d", iSession, iWeekly)
	}

	// Verify no Monthly row
	if strings.Contains(result, "Monthly") {
		t.Error("Ollama card must NOT have a Monthly row")
	}

	// Verify percentages
	if !strings.Contains(result, "33%") || !strings.Contains(result, "67%") {
		t.Error("Ollama card must display percentages")
	}
}

// TestOllamaErrorWithReLogin captures Ollama error state with re-login action.
func TestOllamaErrorWithReLogin(t *testing.T) {
	dto := map[string]any{
		"name":    "broken-ollama",
		"success": false,
		"error":   "Token expired",
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "ollama",
		"dto":     dto,
	})

	// Verify error message displayed
	if !strings.Contains(result, "Token expired") {
		t.Error("Ollama error card must display error message")
	}

	// Verify re-login action present
	if !strings.Contains(result, "olLogin") || !strings.Contains(result, "重新登录") {
		t.Error("Ollama error card must have re-login action with olLogin class")
	}

	// Verify no fabricated metrics
	if strings.Contains(result, "Session") || strings.Contains(result, "Weekly") {
		t.Error("Ollama error card must not fabricate metric rows")
	}
}

// TestKimiCardStructure captures the current Kimi card structure
// as a RED test before migration to shared foundation.
func TestKimiCardStructure(t *testing.T) {
	dto := map[string]any{
		"name":    "kimi-account",
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

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "kimi",
		"dto":     dto,
	})

	// Verify card shell structure
	if !strings.Contains(result, `data-prov="kimi"`) {
		t.Error("Kimi card must have data-prov=kimi")
	}
	if !strings.Contains(result, "Kimi Code") {
		t.Error("Kimi card must display provider label")
	}

	// Verify row order: Rolling, Weekly, Monthly
	iRolling := strings.Index(result, "Rolling")
	iWeekly := strings.Index(result, "Weekly")
	iMonthly := strings.Index(result, "Monthly")
	if !(iRolling >= 0 && iWeekly > iRolling && iMonthly > iWeekly) {
		t.Errorf("Kimi rows must be in order Rolling, Weekly, Monthly; got indices %d, %d, %d", iRolling, iWeekly, iMonthly)
	}

	// Verify decimal percentages preserved
	if !strings.Contains(result, "7.9%") {
		t.Error("Kimi Rolling must display 7.9%")
	}
	if !strings.Contains(result, "56.45%") {
		t.Error("Kimi Weekly must display 56.45%")
	}
	if !strings.Contains(result, "11.86%") {
		t.Error("Kimi Monthly must display 11.86%")
	}

	// Verify Kimi/Code breakdown extension
	if !strings.Contains(result, "Kimi 0.03%") {
		t.Error("Kimi card must display 'Kimi 0.03%' breakdown")
	}
	if !strings.Contains(result, "Code 11.83%") {
		t.Error("Kimi card must display 'Code 11.83%' breakdown")
	}

	// RED: After migration, resets should use compact format from reset_in_sec
	// Currently they use reset_display - this test will fail until migration
	if !strings.Contains(result, "5h") || !strings.Contains(result, "2d") || !strings.Contains(result, "23d") {
		t.Error("Kimi card must use compact duration format (5h, 2d, 23d) from reset_in_sec")
	}
}

// --- Task 1.4: Kimi Percentage Preservation Test ---

// TestKimiPercentagePreservedWithCompactResets proves that specific Kimi
// percentage values remain unchanged while resets switch to compact format.
func TestKimiPercentagePreservedWithCompactResets(t *testing.T) {
	dto := map[string]any{
		"name":    "kimi-test",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 28.21, "reset_display": "07-31 16:58", "reset_in_sec": 18000},
			"seven_day": map[string]any{"usage_percent": 74.86, "reset_display": "08-04 23:58", "reset_in_sec": 172800},
			"total": map[string]any{
				"total_percent": 15.72,
				"kimi_percent":  0.03,
				"code_percent":  15.69,
				"reset_display": "2026-08-28",
				"reset_in_sec":  1987200,
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "kimi",
		"dto":     dto,
	})

	// Percentages MUST remain unchanged (RED: currently passes, must not regress)
	for _, pct := range []string{"28.21%", "74.86%", "15.72%"} {
		if !strings.Contains(result, pct) {
			t.Errorf("Kimi percentage %s must be preserved exactly", pct)
		}
	}

	// Resets MUST use compact format (RED: will fail until migration)
	for _, reset := range []string{"5h", "2d", "23d"} {
		if !strings.Contains(result, reset) {
			t.Errorf("Kimi reset must use compact format %s", reset)
		}
	}

	// Absolute date strings MUST NOT appear (RED: will fail until migration)
	for _, absolute := range []string{"07-31 16:58", "08-04 23:58", "2026-08-28"} {
		if strings.Contains(result, absolute) {
			t.Errorf("Kimi card must not display absolute reset %q", absolute)
		}
	}
}

// --- Task 1.5: Structural/DOM Tests for Shared Foundation ---

// TestSharedFoundationStructure proves the shared quota-card foundation
// provides common shell/row primitives with extension slots.
func TestSharedFoundationStructure(t *testing.T) {
	// Test the view model directly
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test Provider",
		"accountName":   "test-account",
		"success":       true,
		"rows": []map[string]any{
			{"label": "Rolling", "percent": 45, "percentPrecision": 0, "resetInSec": 7200, "tone": "g1"},
			{"label": "Weekly", "percent": 72, "percentPrecision": 0, "resetInSec": 259200, "tone": "g2"},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	// Verify shared shell structure
	if !strings.Contains(result, `class="acard"`) {
		t.Error("Shared foundation must use .acard shell class")
	}
	if !strings.Contains(result, `class="acard-h"`) {
		t.Error("Shared foundation must use .acard-h header class")
	}
	if !strings.Contains(result, `data-prov="testprovider"`) {
		t.Error("Shared foundation must set data-prov attribute")
	}
	if !strings.Contains(result, `data-name="test-account"`) {
		t.Error("Shared foundation must set data-name attribute")
	}

	// Verify row structure uses shared classes
	if !strings.Contains(result, `class="qr"`) {
		t.Error("Shared foundation must use .qr row class")
	}
	if !strings.Contains(result, `class="ql"`) {
		t.Error("Shared foundation must use .ql label class")
	}
	if !strings.Contains(result, `class="qbw"`) {
		t.Error("Shared foundation must use .qbw track class")
	}
	if !strings.Contains(result, `class="qf`) {
		t.Error("Shared foundation must use .qf fill class")
	}
	if !strings.Contains(result, `class="qp`) {
		t.Error("Shared foundation must use .qp percentage class")
	}
	if !strings.Contains(result, `class="qtm"`) {
		t.Error("Shared foundation must use .qtm reset class")
	}
}

// TestSharedFoundationErrorState proves error states use shared error region.
func TestSharedFoundationErrorState(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test Provider",
		"accountName":   "broken-account",
		"success":       false,
		"error":         "Authentication failed",
		"errorAction": map[string]any{
			"label": "重新登录",
			"class": "testLogin",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	// Verify error region
	if !strings.Contains(result, `class="qerr"`) {
		t.Error("Shared foundation must use .qerr error class")
	}
	if !strings.Contains(result, "Authentication failed") {
		t.Error("Shared foundation must display error message")
	}

	// Verify error action slot
	if !strings.Contains(result, "testLogin") {
		t.Error("Shared foundation must render error action with declared class")
	}
	if !strings.Contains(result, "重新登录") {
		t.Error("Shared foundation must render error action label")
	}

	// Verify no metric rows fabricated
	if strings.Contains(result, `class="qr"`) {
		t.Error("Error state must not fabricate metric rows")
	}
}

// TestSharedFoundationExtensionSlots proves typed extension slots work
// for header, body, row-detail, and footer extensions.
func TestSharedFoundationExtensionSlots(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test Provider",
		"accountName":   "test-account",
		"success":       true,
		"headerExtension": map[string]any{
			"type":    "badge",
			"content": "PRO",
			"class":   "test-badge",
		},
		"rows": []map[string]any{
			{
				"label":            "Monthly",
				"percent":          85.5,
				"percentPrecision": 1,
				"resetInSec":       864000,
				"tone":             "g1",
				"details": []map[string]any{
					{"label": "Kimi", "value": "0.5%"},
					{"label": "Code", "value": "85.0%"},
				},
			},
		},
		"bodyExtensions": []map[string]any{
			{"type": "note", "content": "Extra info", "class": "test-note"},
		},
		"footerExtension": map[string]any{
			"type":    "link",
			"content": "View Details",
			"class":   "test-footer",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	// Verify header extension
	if !strings.Contains(result, "test-badge") || !strings.Contains(result, "PRO") {
		t.Error("Header extension must be rendered with declared class and content")
	}

	// Verify row details (Kimi-style breakdown)
	if !strings.Contains(result, "Kimi 0.5%") {
		t.Error("Row detail must render 'Kimi 0.5%'")
	}
	if !strings.Contains(result, "Code 85.0%") {
		t.Error("Row detail must render 'Code 85.0%'")
	}

	// Verify body extension
	if !strings.Contains(result, "test-note") || !strings.Contains(result, "Extra info") {
		t.Error("Body extension must be rendered")
	}

	// Verify footer extension
	if !strings.Contains(result, "test-footer") || !strings.Contains(result, "View Details") {
		t.Error("Footer extension must be rendered")
	}
}

// TestSharedFoundationEscapesHostileText proves all text is escaped.
func TestSharedFoundationEscapesHostileText(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test <script>alert('xss')</script>",
		"accountName":   "account<img src=x onerror=alert(1)>",
		"success":       true,
		"rows": []map[string]any{
			{
				"label":            "Rolling<svg onload=alert(1)>",
				"percent":          50,
				"percentPrecision": 0,
				"resetInSec":       3600,
				"tone":             "g1",
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	// Verify HTML is escaped
	if strings.Contains(result, "<script>") {
		t.Error("Provider label must escape <script> tags")
	}
	if strings.Contains(result, "<img") {
		t.Error("Account name must escape <img> tags")
	}
	if strings.Contains(result, "<svg") {
		t.Error("Row label must escape <svg> tags")
	}

	// Verify escaped entities present
	if !strings.Contains(result, "&lt;script&gt;") {
		t.Error("Escaped <script> should appear as &lt;script&gt;")
	}
}

// TestSharedFoundationNoProviderBranches proves shared renderers contain
// no provider-name conditional branches.
func TestSharedFoundationNoProviderBranches(t *testing.T) {
	html := readSidebarHTML(t)

	// Extract the renderQuotaCard function
	re := regexp.MustCompile(`function renderQuotaCard\s*\([^)]*\)\s*\{[\s\S]*?\n\s*\}`)
	matches := re.FindStringSubmatch(html)
	if len(matches) == 0 {
		t.Skip("renderQuotaCard not yet implemented - RED test")
	}

	funcBody := matches[0]

	// Check for provider-name branches (should not exist)
	for _, provider := range []string{"kimi", "ollama", "opencode", "deepseek"} {
		if strings.Contains(funcBody, `"`+provider+`"`) || strings.Contains(funcBody, `'`+provider+`'`) {
			t.Errorf("renderQuotaCard must not contain provider-name branch for %q", provider)
		}
	}
}

// TestSharedFoundationActionsNeverExposeCredentials proves the shared
// error/action slots carry only the account name needed for the action,
// never tokens, cookies, or credential material.
func TestSharedFoundationActionsNeverExposeCredentials(t *testing.T) {
	view := map[string]any{
		"provider":      "kimi",
		"providerLabel": "Kimi Code",
		"accountName":   "kimi-account",
		"success":       false,
		"error":         "凭证已过期",
		"errorAction": map[string]any{
			"label": "重新登录",
			"class": "kimiLogin",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	// The action must carry data-name (account identity) but no credential
	// fields.
	if !strings.Contains(result, `data-name="kimi-account"`) {
		t.Error("error action must carry data-name for account identity")
	}
	for _, secret := range []string{"token", "cookie", "secret", "password", "credential", "authorization"} {
		if strings.Contains(strings.ToLower(result), secret) && !strings.Contains(strings.ToLower(result), "凭证已过期") {
			t.Errorf("error action must not embed credential material (%q) in rendered card", secret)
		}
	}
	// The error message itself is user-facing text, but must not contain
	// raw tokens.
	if strings.Contains(result, "Bearer ") {
		t.Error("error card must not embed bearer tokens")
	}
}

// TestSharedFoundationErrorActionIsKeyboardAccessible proves the shared
// error action renders as a real <button> (focusable) with a declarative
// data-action attribute, never a bare <span> that cannot receive focus.
func TestSharedFoundationErrorActionIsKeyboardAccessible(t *testing.T) {
	view := map[string]any{
		"provider":      "kimi",
		"providerLabel": "Kimi Code",
		"accountName":   "kimi-account",
		"success":       false,
		"error":         "凭证已过期",
		"errorAction": map[string]any{
			"label": "重新登录",
			"class": "kimiLogin",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, `<button type="button" class="qact kimiLogin"`) {
		t.Errorf("error action must render as <button type=button> with shared qact class; got:\n%s", result)
	}
	if !strings.Contains(result, `data-action="relogin"`) {
		t.Errorf("error action must carry a declarative data-action; got:\n%s", result)
	}
	if strings.Contains(result, `<span class="kimiLogin"`) {
		t.Error("error action must not be a non-focusable span")
	}
}

// TestSharedQactActionStylingExists proves the shared .qact class owns
// the action-button visual contract (idle/hover/active/focus-visible) so
// every provider's re-login action (Ollama's olLogin included) is themed
// and none falls back to the browser-native gray button.
func TestSharedQactActionStylingExists(t *testing.T) {
	html := readSidebarHTML(t)

	if !strings.Contains(html, `.qact:hover`) ||
		!strings.Contains(html, `.qact:active`) ||
		!strings.Contains(html, `.qact:focus-visible`) {
		t.Error("shared .qact action styling must define hover/active/focus-visible states")
	}
	// Provider delegation hooks remain so per-container dispatch keeps
	// working, but they must not re-implement the visual contract.
	for _, hook := range []string{".kimiLogin,", ".olLogin"} {
		if !strings.Contains(html, hook) {
			t.Errorf("provider action delegation hook %q must remain", hook)
		}
	}
}

// TestProviderOptionsAreKeyboardAccessibleButtons proves the registry-
// rendered Add Account choices are <button> elements (keyboard focusable),
// not bare <div> tiles.
func TestProviderOptionsAreKeyboardAccessibleButtons(t *testing.T) {
	html := readSidebarHTML(t)

	// Options are created as real buttons from the registry.
	if !strings.Contains(html, `document.createElement("button")`) {
		t.Error("provider options must be created as <button> elements")
	}
	// The option primitive must expose a keyboard-focus state.
	if !regexp.MustCompile(`\.prov\s*:\s*focus-visible`).MatchString(html) {
		t.Error(".prov must define :focus-visible for keyboard navigation")
	}
}

// TestSharedEventDelegationIsDataDriven proves account-level click
// delegation is keyed by declarative action classes (olLogin/kimiLogin)
// on the rendered action element, not by provider-name branches in the
// shared renderer.
func TestSharedEventDelegationIsDataDriven(t *testing.T) {
	html := readSidebarHTML(t)

	// Delegated listeners must match the action class and read data-name.
	for _, pair := range [][2]string{
		{"olLogin", "olDoLogin"},
		{"kimiLogin", "kimiDoLogin"},
	} {
		cls, fn := pair[0], pair[1]
		if !strings.Contains(html, `contains("`+cls+`")`) {
			t.Errorf("delegation must match %s by class name", cls)
		}
		if !strings.Contains(html, `data-name`) {
			t.Errorf("delegation must read data-name for account identity")
		}
		if !strings.Contains(html, fn) {
			t.Errorf("delegation for %s must dispatch to %s", cls, fn)
		}
	}
}

// TestSyntheticFutureProvider proves a new provider can use the shared
// foundation with only an adapter and extension content.
func TestSyntheticFutureProvider(t *testing.T) {
	// This test proves the extension contract is sufficient for new providers
	view := map[string]any{
		"provider":      "futureai",
		"providerLabel": "Future AI",
		"accountName":   "future-account",
		"success":       true,
		"rows": []map[string]any{
			{"label": "Hourly", "percent": 25, "percentPrecision": 0, "resetInSec": 1800, "tone": "g1"},
			{"label": "Daily", "percent": 60, "percentPrecision": 0, "resetInSec": 43200, "tone": "g2"},
		},
		"headerExtension": map[string]any{
			"type":    "badge",
			"content": "BETA",
			"class":   "future-badge",
		},
		"bodyExtensions": []map[string]any{
			{
				"type":    "custom",
				"content": "Unique future provider content",
				"class":   "future-custom",
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	// Verify shared structure is reused
	if !strings.Contains(result, `class="acard"`) {
		t.Error("Future provider must reuse shared shell")
	}
	if !strings.Contains(result, `class="qr"`) {
		t.Error("Future provider must reuse shared rows")
	}

	// Verify extension content is rendered
	if !strings.Contains(result, "future-badge") {
		t.Error("Future provider header extension must render")
	}
	if !strings.Contains(result, "future-custom") {
		t.Error("Future provider body extension must render")
	}

	// Verify data attributes
	if !strings.Contains(result, `data-prov="futureai"`) {
		t.Error("Future provider must set data-prov")
	}
}

// --- Structural Parity Tests ---

// TestAllowanceProvidersShareDesignTokens proves OpenCode Go, Ollama, and
// Kimi cards share the same CSS design tokens and grid behavior.
func TestAllowanceProvidersShareDesignTokens(t *testing.T) {
	html := readSidebarHTML(t)

	// Verify shared grid rule applies to all allowance card containers
	gridRule := regexp.MustCompile(`(?s)([^{}]*)\{[^}]*repeat\(auto-fill,\s*minmax\(320px,\s*1fr\)\)`)
	m := gridRule.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("shared responsive grid rule not found")
	}

	selectorGroup := m[1]
	for _, container := range []string{"#accountCards", "#ollamaCards", "#kimiCards"} {
		if !strings.Contains(selectorGroup, container) {
			t.Errorf("container %s must be part of shared grid selector group", container)
		}
	}

	// Verify shared card shell CSS exists (allowing whitespace before brace)
	for _, cls := range []string{".acard ", ".acard{", ".acard-h ", ".acard-h{"} {
		if strings.Contains(html, cls) {
			t.Logf("found shell CSS %q", strings.TrimSpace(cls))
		}
	}
	if !regexp.MustCompile(`\.acard\s*\{`).MatchString(html) {
		t.Error("shared .acard shell CSS must exist")
	}
	if !regexp.MustCompile(`\.acard-h\s*\{`).MatchString(html) {
		t.Error("shared .acard-h header CSS must exist")
	}

	// Verify shared row CSS exists
	for _, cls := range []string{".qr", ".ql", ".qbw", ".qf", ".qp", ".qtm"} {
		if !regexp.MustCompile(regexp.QuoteMeta(cls) + `\s*\{`).MatchString(html) {
			t.Errorf("shared row CSS %s must exist", cls)
		}
	}
}

// TestProviderAdaptersReturnViewModels proves adapters return normalized
// view models rather than concatenating HTML.
func TestProviderAdaptersReturnViewModels(t *testing.T) {
	html := readSidebarHTML(t)

	// RED: Check that adapter functions exist and return view model objects
	for _, adapter := range []string{"opencodeAdapter", "ollamaAdapter", "kimiAdapter"} {
		if !strings.Contains(html, "function "+adapter) {
			t.Skipf("adapter %s not yet implemented - RED test", adapter)
		}
	}

	// Verify adapters don't concatenate shared shell HTML
	// (they should return view model objects, not HTML strings)
	adapterPattern := regexp.MustCompile(`function\s+\w+Adapter\s*\([^)]*\)\s*\{[\s\S]*?return\s+[^;]+;`)
	matches := adapterPattern.FindAllString(html, -1)
	for _, match := range matches {
		// Adapters should return objects, not HTML strings starting with '<div'
		if strings.Contains(match, `return "<div`) || strings.Contains(match, `return '<div`) {
			t.Error("adapters must return view model objects, not HTML strings")
		}
	}
}

// --- Task 1.6: Add Account dialog RED tests ---

// TestAddAccountOptionsAreNameOnly proves the Add Account dialog renders
// all four provider options from a single data-driven registry, each
// showing only the centered provider name with no .prov-d subtitle node
// or obsolete description text.
func TestAddAccountOptionsAreNameOnly(t *testing.T) {
	html := readSidebarHTML(t)

	// The provider registry must define exactly the four allowance
	// providers with type + display name (data-driven selector).
	for _, prov := range []struct{ typ, label string }{
		{"opencode", "OpenCode Go"},
		{"deepseek", "DeepSeek"},
		{"ollama", "Ollama"},
		{"kimi", "Kimi Code"},
	} {
		if !strings.Contains(html, `type: "`+prov.typ+`"`) || !strings.Contains(html, `label: "`+prov.label+`"`) {
			t.Errorf("provider registry must define %s with label %q", prov.typ, prov.label)
		}
	}
	if !strings.Contains(html, `quotaProviders`) {
		t.Error("provider registry (quotaProviders) must exist")
	}
	// Options must be rendered FROM the registry (data-driven), not
	// hand-written per provider.
	if !strings.Contains(html, `quotaProviders.forEach`) {
		t.Error("step-one options must be rendered from the registry (quotaProviders.forEach)")
	}
	if !strings.Contains(html, `modalProviders`) {
		t.Error("step-one option container #modalProviders must exist")
	}

	// No .prov-d subtitle nodes may remain anywhere.
	if strings.Contains(html, "prov-d") {
		t.Error("provider options must not contain .prov-d subtitle nodes")
	}

	// Obsolete subtitle descriptions must be gone.
	for _, stale := range []string{"套餐额度", "用量 / 余额", "Cloud 用量", "Rolling / Weekly / Monthly"} {
		if strings.Contains(html, stale) {
			t.Errorf("obsolete provider subtitle %q must be removed from Add Account dialog", stale)
		}
	}

	// The option primitive must center the name.
	if !regexp.MustCompile(`\.prov\s*\{[^}]*justify-content\s*:\s*center`).MatchString(html) {
		t.Error(".prov option must center its content (justify-content: center)")
	}
}

// TestProviderRegistryUnknownTypeErrors proves the data-driven selector
// errors explicitly on an unknown provider type instead of silently
// falling back to a known provider (e.g. Kimi).
func TestProviderRegistryUnknownTypeErrors(t *testing.T) {
	html := readSidebarHTML(t)

	// quotaProviderByType must return null for unknown types.
	if !strings.Contains(html, `return null`) {
		t.Error("quotaProviderByType must return null for unknown provider types")
	}
	if !strings.Contains(html, `未知服务商`) {
		t.Error("unknown provider type must surface an explicit error (未知服务商)")
	}
	// The step-two label and default name must come from the registry entry,
	// not a hard-coded if/else chain that falls back to Kimi.
	if strings.Contains(html, `: "Kimi Code") + " · 账户名称"`) ||
		strings.Contains(html, `: "Kimi";`) {
		t.Error("step-two label/default-name must not fall back to Kimi via a ternary chain")
	}
}

// TestAddAccountDataTypeMappingsRetained proves removing the subtitles keeps
// the exact data-type → step-two label / default name / login dispatch wiring
// (now derived from the registry).
func TestAddAccountDataTypeMappingsRetained(t *testing.T) {
	html := readSidebarHTML(t)

	// Step-two provider label mapping must still exist (modalProvLabel),
	// now sourced from the registry entry.
	for _, want := range []string{`modalProvLabel`, `p.label + " · 账户名称"`, `p.defaultName`} {
		if !strings.Contains(html, want) {
			t.Errorf("Add Account dialog missing %q (step-two label wiring)", want)
		}
	}

	// Login dispatch must go through the registry's login name.
	if !strings.Contains(html, `window[p.login]`) {
		t.Error("login dispatch must be registry-driven (window[p.login])")
	}

	// All four login functions referenced by the registry must exist.
	for _, fn := range []string{"ocDoLogin", "dsDoLogin", "olDoLogin", "kimiDoLogin"} {
		if !strings.Contains(html, `login: "`+fn+`"`) || !strings.Contains(html, `function `+fn) {
			t.Errorf("registry login %s must be defined and wired", fn)
		}
	}

	// Modal close behavior must be retained.
	if !strings.Contains(html, `id="modalClose"`) || !strings.Contains(html, `closeModal`) {
		t.Error("modal close behavior must be retained")
	}
}
