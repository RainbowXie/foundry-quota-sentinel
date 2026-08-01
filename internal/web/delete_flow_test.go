package web

// Delete-flow regression tests for the shared quota-card template change
// (unify-quota-card-template). The pre-fix delete flow hard-coded the
// Ollama label in the confirm text (so deleting a Kimi account asked to
// delete an "Ollama 账户") and refreshed only fq/fo/fd after deletion,
// leaving the deleted Kimi card stale until the next 30s poll. The flow
// must be registry-driven: confirm label from quotaProviders, refresh via
// each provider's refresh handler (including fk).

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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
	// Delete confirm label must come from the registry (quotaProviderByType)
	// so Kimi shows "Kimi Code", never the hard-coded Ollama fallback.
	if !strings.Contains(html, `quotaProviderByType(cur.prov)`) {
		t.Fatal("delete confirm label must be registry-driven (quotaProviderByType(cur.prov))")
	}
	// Post-delete refresh must target the deleted provider's refresh handler
	// via the registry (p.refresh), so deleting a Kimi account immediately
	// calls fk() without touching unrelated providers.
	if !strings.Contains(html, `p.refresh`) {
		t.Fatal("post-delete refresh must use registry refresh handlers (p.refresh)")
	}
	if !strings.Contains(html, `refresh: "fk"`) {
		t.Fatal("registry must map kimi to its fk refresh handler")
	}
}

// TestDeleteFlowRefreshesOnlyDeletedProvider executes the real script
// block with a stubbed DOM, drives a Kimi account through the delete
// context-menu flow, and proves: (1) the confirm text names Kimi Code (not
// Ollama), (2) confirmOk deletes /api/delete?provider=kimi&name=..., (3) the
// post-delete refresh calls ONLY the deleted provider's refresh handler
// (/api/kimi, no unrelated provider fetches), and (4) the refresh uses the
// provider snapshot taken at confirm time — even if the user opens a new
// delete confirm for another provider while the request is in flight, the
// deleted Kimi card is still the one refreshed.
func TestDeleteFlowRefreshesOnlyDeletedProvider(t *testing.T) {
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
if (!blocks.length) { console.error("kcard script block not found"); process.exit(2); }

function makeEl(id) {
	return {
		id: id,
		_listeners: {},
		_attrs: {},
		classList: {
			add() {}, remove() {}, toggle() {},
			contains() { return false; },
		},
		addEventListener(ev, fn) {
			(this._listeners[ev] = this._listeners[ev] || []).push(fn);
		},
		removeEventListener() {},
		getAttribute(k) { return this._attrs[k] !== undefined ? this._attrs[k] : null; },
		setAttribute(k, v) { this._attrs[k] = String(v); },
		appendChild() {},
		textContent: "",
		innerHTML: "",
		style: {},
		value: "",
		focus() {}, select() {},
		getBoundingClientRect() { return { left: 0, top: 0, width: 0, height: 0 }; },
	};
}

const els = {};
function getEl(id) { return (els[id] = els[id] || makeEl(id)); }
const docListeners = {};
const fetchCalls = [];
const timers = [];

const document = {
	getElementById: getEl,
	querySelectorAll: () => [],
	createElement: () => makeEl("created"),
	documentElement: makeEl("docEl"),
	body: makeEl("body"),
	addEventListener: (ev, fn) => {
		(docListeners[ev] = docListeners[ev] || []).push(fn);
	},
};

const sandbox = {
	console,
	document,
	fetch: (u) => {
		fetchCalls.push(String(u));
		return Promise.resolve({ json: () => Promise.resolve({ success: true, data: [] }) });
	},
	alert: () => {},
	setTimeout: (fn) => { timers.push(fn); return timers.length; },
	setInterval: () => 0,
	clearTimeout: () => {},
	clearInterval: () => {},
	requestAnimationFrame: () => 0,
	localStorage: { getItem: () => null, setItem: () => {} },
	innerWidth: 1280,
};
sandbox.window = sandbox;
sandbox.addEventListener = () => {};
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
try {
	vm.runInContext(blocks[0], sandbox);
} catch (e) {
	console.error("script eval failed: " + e.message);
	process.exit(5);
}

// Drive the context-menu flow: right-click a Kimi card.
const ctxHandlers = docListeners["contextmenu"] || [];
if (!ctxHandlers.length) { console.error("no contextmenu listener"); process.exit(6); }
const cardEl = makeEl("card");
cardEl.setAttribute("data-prov", "kimi");
cardEl.setAttribute("data-name", "Kimi A");
ctxHandlers.forEach((fn) => fn({ target: { closest: () => cardEl }, clientX: 5, clientY: 5, preventDefault: () => {} }));

// Open delete confirm.
const delItem = getEl("ctxDelete");
const delListeners = delItem._listeners["click"] || [];
if (!delListeners.length) { console.error("ctxDelete has no click listener"); process.exit(7); }
const delClick = delListeners[0];
delClick();
const confirmText = getEl("confirmText").textContent;
if (!confirmText.includes("Kimi Code") || confirmText.includes("Ollama")) {
	console.error("confirm text must name Kimi Code, got: " + confirmText);
	process.exit(8);
}

// Confirm deletion.
const okBtn = getEl("confirmOk");
const okClick = (okBtn._listeners["click"] || []).pop();
if (!okClick) { console.error("confirmOk has no click listener"); process.exit(9); }
okClick();
if (!fetchCalls.includes("/api/delete?provider=kimi&name=Kimi%20A")) {
	console.error("must delete /api/delete?provider=kimi&name=Kimi A; calls=" + JSON.stringify(fetchCalls));
	process.exit(10);
}

// The refresh callback is scheduled inside fetch().then(), i.e. after the
// promise microtask resolves; give it a tick before running timers.
setImmediate(() => {
	// RACE: while the Kimi delete request is in flight (before its refresh
	// timer fires), the user opens a delete confirm for another provider
	// (Ollama), which overwrites the internal pending provider/name.
	const ollamaCard = makeEl("ocard");
	ollamaCard.setAttribute("data-prov", "ollama");
	ollamaCard.setAttribute("data-name", "Ollama B");
	ctxHandlers.forEach((fn) => fn({ target: { closest: () => ollamaCard }, clientX: 5, clientY: 5, preventDefault: () => {} }));
	delClick(); // pend.prov now ollama; NOT confirmed

	const pre = fetchCalls.length;
	timers.forEach((fn) => fn());
	const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
	// The refresh must target the SNAPSHOTTED deleted provider (kimi → fk),
	// not the newly-pending Ollama.
	if (!after.includes("/api/kimi")) {
		console.error("post-delete refresh must call /api/kimi (snapshotted deleted provider); after=" + JSON.stringify(after));
		process.exit(13);
	}
	for (const ep of ["/api/accounts", "/api/deepseek", "/api/ollama"]) {
		if (after.includes(ep)) {
			console.error("post-delete refresh must NOT call unrelated " + ep + "; after=" + JSON.stringify(after));
			process.exit(14);
		}
	}
	console.log("delete flow ok");
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
		t.Fatalf("delete flow harness failed: %v\n%s", err, out)
	}
}
