package web

// Test harness for the shared quota-card template renderer.
// The renderer executes the inline <script> block shipped in static/sidebar.html
// using node with a stubbed DOM environment.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
