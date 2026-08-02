package web

// Delete-flow regression tests for the shared quota-card template change
// (unify-quota-card-template) AND the delete-failure surfacing change
// (delete-failure-surfacing). Earlier fixes: the confirm label comes from
// the provider registry (Kimi Code, never an Ollama fallback), the
// post-delete refresh targets only the deleted provider's container from a
// confirm-time snapshot (race-safe). This change adds response parsing:
// {success:false} / malformed body / network failure keep the modal open
// with the right error text, and refresh nothing; a stale in-flight
// response must not operate on a modal that now belongs to another
// confirmation (generation ownership).

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestKimiRefreshAndDeleteWiringIntact proves the non-renderer behaviors
// the presentation changes must not replace: periodic kimi refresh, the
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
	// The delete handler must parse the /api/delete response and branch on
	// success (failure-surfacing change): the modal must NOT close on a
	// non-success response, and errors are shown in the confirm region.
	if !strings.Contains(html, `success !== true`) {
		t.Fatal("delete handler must branch on success !== true (non-success keeps modal open)")
	}
	if !strings.Contains(html, `删除失败`) {
		t.Fatal("delete handler must surface 删除失败 with the server error")
	}
	// Concurrency: the delete handler tracks per-account in-flight deletes
	// (provider+name keyed, so closing and reopening the SAME account cannot
	// bypass the re-entrancy guard), and a JSON parse failure must not be
	// reported as a network error.
	if !strings.Contains(html, `inFlightDeletes`) {
		t.Fatal("delete handler must track per-account in-flight deletes (inFlightDeletes)")
	}
	if !strings.Contains(html, `r.json().catch`) {
		t.Fatal("JSON parse failure must be converted to null, not a network error")
	}
}

// deleteFlowHarnessScript is the shared stub-DOM driver. Scenario (argv[3]):
//
//   - "success":        /api/delete resolves {success:true}; modal closes,
//     only the snapshotted deleted provider (/api/kimi) refreshes; the
//     refresh survives an Ollama confirm opened during the delay window.
//   - "failure":        /api/delete resolves {success:false, error:"delete
//     not supported"}; modal stays open, #confirmText shows
//     "删除失败：delete not supported", NO provider refreshes.
//   - "network":        /api/delete rejects; modal stays open, #confirmText
//     shows "删除失败：网络错误", NO provider refreshes.
//   - "malformed":      /api/delete body fails JSON parse; modal stays open,
//     #confirmText shows "删除失败：未知错误" (NOT 网络错误), NO refresh.
//   - "stale-success":  /api/delete is deferred. After confirming Kimi A a
//     new Ollama B confirm is opened, THEN A resolves {success:true}. The B
//     modal must stay open with its own text, and the refresh must still
//     target /api/kimi (confirm-time snapshot, not generation-gated).
//   - "stale-failure":  /api/delete deferred; after opening Ollama B, A
//     resolves {success:false, error:"kimi delete failed"}. B's modal and
//     text must be untouched; NO provider refreshes.
//   - "double-confirm":  #confirmOk is clicked twice. Exactly ONE
//     /api/delete must be sent (per-account in-flight guard — the server
//     delete is not idempotent); the success response closes the modal and
//     refreshes /api/kimi, and the success state is not overwritten by a
//     would-be second failure.
//   - "reopen":  Kimi A delete is deferred; the user closes the modal and
//     reopens Kimi A's confirm, then confirms again. The per-account
//     in-flight guard must still block the second request (exactly ONE
//     /api/delete), and the ORIGINAL success must close the reopened
//     account's dialog and refresh /api/kimi.
//   - "reopen-before-refresh":  Kimi A's delete SUCCEEDS (immediate) and
//     the modal closes, but the 300ms refresh timer has NOT run yet. The
//     user reopens the same account and confirms again: the per-account
//     guard must still block a duplicate (the key is only released after
//     the refresh settles), so exactly ONE /api/delete total. After the
//     refresh runs, /api/kimi is fetched, the STALE reopened dialog for the
//     deleted account is closed/invalidated, and confirming it must NOT
//     send a second delete.
//   - "recreated":  after the successful refresh settles (old card gone), a
//     NEW card for the same provider+name appears (account re-added) and a
//     fresh confirm is opened via delItem. The recreated account's delete
//     MUST be allowed (the guard is released and pend is reset by the new
//     delItem): a new /api/delete is sent.
const deleteFlowHarnessScript = `
const fs = require("fs");
const vm = require("vm");
const scenario = process.argv[3];
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
			_s: new Set(),
			add(c) { this._s.add(c); },
			remove(c) { this._s.delete(c); },
			toggle(c, force) { if (force === true) this._s.add(c); else if (force === false) this._s.delete(c); else if (this._s.has(c)) this._s.delete(c); else this._s.add(c); },
			contains(c) { return this._s.has(c); },
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
const pendingDeletes = []; // resolvers for deferred /api/delete responses

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
		const isDelete = String(u).startsWith("/api/delete");
		if (isDelete && (scenario === "stale-success" || scenario === "stale-failure" || scenario === "reopen")) {
			return new Promise((resolve) => pendingDeletes.push(resolve));
		}
		if (isDelete && scenario === "malformed") {
			return Promise.resolve({ json: () => Promise.reject(new SyntaxError("bad json")) });
		}
		if (isDelete && scenario === "failure") {
			return Promise.resolve({ json: () => Promise.resolve({ success: false, error: "delete not supported" }) });
		}
		if (isDelete && scenario === "network") {
			return Promise.reject(new Error("network down"));
		}
		return Promise.resolve({ json: () => Promise.resolve({ success: true }) });
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

// openConfirm opens the context-menu delete confirm for a card.
function openConfirm(prov, name) {
	const cardEl = makeEl("card-" + prov);
	cardEl.setAttribute("data-prov", prov);
	cardEl.setAttribute("data-name", name);
	(docListeners["contextmenu"] || []).forEach((fn) =>
		fn({ target: { closest: () => cardEl }, clientX: 5, clientY: 5, preventDefault: () => {} }));
	(docListeners["click"] || []); // no-op: ctx menu item handled below
	const delItem = getEl("ctxDelete");
	delItem._listeners["click"][0]();
}

function fail(msg, code) {
	console.error(msg);
	process.exit(code);
}

// --- Step 1: confirm deleting Kimi A ---
openConfirm("kimi", "Kimi A");
const confirmText = getEl("confirmText").textContent;
if (!confirmText.includes("Kimi Code") || confirmText.includes("Ollama")) {
	fail("confirm text must name Kimi Code, got: " + confirmText, 8);
}

const okBtn = getEl("confirmOk");
const okClick = (okBtn._listeners["click"] || []).pop();
if (!okClick) { fail("confirmOk has no click listener", 9); }
okClick();
if (scenario === "double-confirm") {
	// Double-click: the second click must be inert (per-account in-flight
	// guard; the server delete is not idempotent, so a duplicate request
	// would fail with "account not found" and wrongly overwrite the
	// success).
	okClick();
}
if (!fetchCalls.includes("/api/delete?provider=kimi&name=Kimi%20A")) {
	fail("must delete /api/delete?provider=kimi&name=Kimi A; calls=" + JSON.stringify(fetchCalls), 10);
}
if (scenario === "double-confirm") {
	const deletes = fetchCalls.filter((u) => u.startsWith("/api/delete"));
	if (deletes.length !== 1) {
		fail("double-confirm must send exactly ONE /api/delete; got " + deletes.length + ": " + JSON.stringify(deletes), 26);
	}
}

// --- Step 2: scenario-specific continuation ---
setImmediate(() => {
	if (scenario === "success") {
		// Response resolved; modal must already be closed (microtasks flushed).
		if (!getEl("confirmModal").classList.contains("hide")) {
			fail("success must close the confirm modal", 11);
		}		// RACE: during the 300ms refresh-delay window the user opens another
		// provider's confirm, overwriting internal pending state.
		openConfirm("ollama", "Ollama B");
		const pre = fetchCalls.length;
		timers.forEach((fn) => fn());
		const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
		if (!after.includes("/api/kimi")) {
			fail("post-delete refresh must call /api/kimi (snapshotted deleted provider); after=" + JSON.stringify(after), 12);
		}
		for (const ep of ["/api/accounts", "/api/deepseek", "/api/ollama"]) {
			if (after.includes(ep)) {
				fail("post-delete refresh must NOT call unrelated " + ep + "; after=" + JSON.stringify(after), 13);
			}
		}
	} else if (scenario === "failure" || scenario === "network" || scenario === "malformed") {
		if (getEl("confirmModal").classList.contains("hide")) {
			fail("non-success must keep the confirm modal open", 14);
		}
		const text = getEl("confirmText").textContent;
		if (scenario === "failure") {
			if (!text.includes("删除失败") || !text.includes("delete not supported")) {
				fail("failure must show 删除失败：delete not supported, got: " + text, 15);
			}
		} else if (scenario === "network") {
			if (!text.includes("删除失败") || !text.includes("网络错误")) {
				fail("network failure must show 删除失败：网络错误, got: " + text, 16);
			}
		} else { // malformed
			if (!text.includes("删除失败") || !text.includes("未知错误") || text.includes("网络错误")) {
				fail("malformed body must show 删除失败：未知错误 (not 网络错误), got: " + text, 18);
			}
		}
		const pre = fetchCalls.length;
		timers.forEach((fn) => fn());
		const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
		if (after.length !== 0) {
			fail("non-success must NOT refresh any provider; after=" + JSON.stringify(after), 17);
		}
	} else if (scenario === "double-confirm") {
		// The single request resolves success: the modal closes and the
		// deleted provider refreshes; the success state must NOT be
		// overwritten by a phantom second failure.
		if (!getEl("confirmModal").classList.contains("hide")) {
			fail("double-confirm success must close the confirm modal", 27);
		}
		if (getEl("confirmText").textContent.includes("删除失败")) {
			fail("double-confirm must end in success, not 删除失败; text=" + getEl("confirmText").textContent, 28);
		}
		const pre = fetchCalls.length;
		timers.forEach((fn) => fn());
		const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
		if (!after.includes("/api/kimi")) {
			fail("double-confirm success must refresh /api/kimi; after=" + JSON.stringify(after), 29);
		}
		for (const ep of ["/api/accounts", "/api/deepseek", "/api/ollama"]) {
			if (after.includes(ep)) {
				fail("double-confirm must NOT refresh unrelated " + ep + "; after=" + JSON.stringify(after), 30);
			}
		}
	} else if (scenario === "reopen") {
		// RACE BEFORE RESPONSE: the user closes A's dialog, then reopens the
		// SAME Kimi A confirm and confirms again. The per-account in-flight
		// guard must block the second request (exactly ONE /api/delete), and
		// the ORIGINAL success must close the reopened dialog and refresh
		// /api/kimi.
		getEl("confirmClose")._listeners["click"][0](); // close A's dialog
		openConfirm("kimi", "Kimi A");                  // reopen same account
		okClick();                                       // confirm again → blocked
		const deletes = fetchCalls.filter((u) => u.startsWith("/api/delete"));
		if (deletes.length !== 1) {
			fail("reopen must still send exactly ONE /api/delete; got " + deletes.length + ": " + JSON.stringify(deletes), 31);
		}
		if (pendingDeletes.length !== 1) { fail("expected 1 deferred delete, got " + pendingDeletes.length, 20); }
		pendingDeletes[0]({ json: () => Promise.resolve({ success: true }) });
		setImmediate(() => {
			// The reopened dialog for the SAME account must be closed by the
			// original success (no stale confirm box left behind).
			if (!getEl("confirmModal").classList.contains("hide")) {
				fail("original success must close the reopened same-account dialog", 32);
			}
			const pre = fetchCalls.length;
			timers.forEach((fn) => fn());
			const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
			if (!after.includes("/api/kimi")) {
				fail("reopen success must refresh /api/kimi; after=" + JSON.stringify(after), 33);
			}
			for (const ep of ["/api/accounts", "/api/deepseek", "/api/ollama"]) {
				if (after.includes(ep)) {
					fail("reopen must NOT refresh unrelated " + ep + "; after=" + JSON.stringify(after), 34);
				}
			}
		});
	} else if (scenario === "reopen-before-refresh") {
		// Success already arrived and the modal closed, but the 300ms
		// refresh timer has NOT run yet. Reopening the SAME account and
		// confirming again must be blocked: the per-account guard is held
		// until the refresh settles (the old card is still in the DOM).
		if (!getEl("confirmModal").classList.contains("hide")) {
			fail("success must close the confirm modal", 11);
		}
		openConfirm("kimi", "Kimi A"); // reopen same account before refresh
		okClick();                        // confirm again -> must be blocked
		const deletesBeforeRefresh = fetchCalls.filter((u) => u.startsWith("/api/delete"));
		if (deletesBeforeRefresh.length !== 1) {
			fail("reopen before refresh must NOT send a 2nd delete; got " + deletesBeforeRefresh.length + ": " + JSON.stringify(deletesBeforeRefresh), 40);
		}
		// Now run the refresh timer: /api/kimi refreshes and the guard
		// releases after the refresh settles.
		const pre = fetchCalls.length;
		timers.forEach((fn) => fn());
		setImmediate(() => {
			const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
			if (!after.includes("/api/kimi")) {
				fail("refresh must call /api/kimi after success; after=" + JSON.stringify(after), 41);
			}
			for (const ep of ["/api/accounts", "/api/deepseek", "/api/ollama"]) {
				if (after.includes(ep)) {
					fail("refresh must NOT call unrelated " + ep + "; after=" + JSON.stringify(after), 42);
				}
			}
			// BUG (to be fixed): after the refresh settles, the stale
			// reopened dialog for the now-deleted account must be closed and
			// invalidated. Calling the old okClick must NOT send a second
			// /api/delete (the account no longer exists server-side; a second
			// request would return account-not-found and wrongly show
			// 删除失败). The old dialog's confirm action must be inert.
			if (!getEl("confirmModal").classList.contains("hide")) {
				fail("refresh settle must close the stale reopened dialog for the deleted account; modal still open", 44);
			}
			okClick();
			const deletesAfter = fetchCalls.filter((u) => u.startsWith("/api/delete"));
			if (deletesAfter.length !== 1) {
				fail("stale dialog confirm after refresh must NOT send a 2nd delete; got " + deletesAfter.length + ": " + JSON.stringify(deletesAfter), 43);
			}
		});
	} else if (scenario === "recreated") {
		// Success resolves; wait for the refresh timer to run and settle
		// (old card gone, stale dialog invalidated, guard released).
		setImmediate(() => {
			const pre = fetchCalls.length;
			timers.forEach((fn) => fn());
			setImmediate(() => {
				const refreshed = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
				if (!refreshed.includes("/api/kimi")) {
					fail("refresh must call /api/kimi after success; refreshed=" + JSON.stringify(refreshed), 41);
				}
				// A NEW card for the same provider+name appears (account
				// re-added) and a fresh confirm is opened via delItem: this
				// MUST reset pend and allow a NEW delete.
				openConfirm("kimi", "Kimi A");
				const newText = getEl("confirmText").textContent;
				if (!newText.includes("Kimi Code") || !newText.includes("Kimi A")) {
					fail("recreated confirm must show Kimi A text, got: " + newText, 45);
				}
				okClick();
				const deletes = fetchCalls.filter((u) => u.startsWith("/api/delete"));
				if (deletes.length !== 2) {
					fail("recreated account must be deletable again (2nd /api/delete); got " + deletes.length + ": " + JSON.stringify(deletes), 46);
				}
			});
		});
	} else if (scenario === "stale-success" || scenario === "stale-failure") {
		// RACE BEFORE RESPONSE: the user closes A's dialog and opens a new
		// Ollama B confirm while A's request is still in flight.
		openConfirm("ollama", "Ollama B");
		const bText = getEl("confirmText").textContent;
		if (!bText.includes("Ollama") || !bText.includes("Ollama B")) {
			fail("B confirm must show Ollama B text, got: " + bText, 19);
		}
		// Now A's response arrives.
		if (pendingDeletes.length !== 1) { fail("expected 1 deferred delete, got " + pendingDeletes.length, 20); }
		if (scenario === "stale-success") {
			pendingDeletes[0]({ json: () => Promise.resolve({ success: true }) });
		} else {
			pendingDeletes[0]({ json: () => Promise.resolve({ success: false, error: "kimi delete failed" }) });
		}
		setImmediate(() => {
			// B's modal must survive A's response.
			if (getEl("confirmModal").classList.contains("hide")) {
				fail("stale response must not close the newer B modal", 21);
			}
			if (getEl("confirmText").textContent !== bText) {
				fail("stale response must not rewrite the newer B confirm text; got: " + getEl("confirmText").textContent, 22);
			}
			const pre = fetchCalls.length;
			timers.forEach((fn) => fn());
			const after = fetchCalls.slice(pre).map((u) => u.split("?")[0]);
			if (scenario === "stale-success") {
				// The deleted provider refresh is NOT generation-gated: A was
				// really deleted, so /api/kimi refreshes.
				if (!after.includes("/api/kimi")) {
					fail("stale success must still refresh /api/kimi (snapshot); after=" + JSON.stringify(after), 23);
				}
				for (const ep of ["/api/accounts", "/api/deepseek", "/api/ollama"]) {
					if (after.includes(ep)) {
						fail("stale success must NOT refresh unrelated " + ep + "; after=" + JSON.stringify(after), 24);
					}
				}
			} else {
				if (after.length !== 0) {
					fail("stale failure must NOT refresh any provider; after=" + JSON.stringify(after), 25);
				}
			}
		});
	}
	console.log("delete flow ok (" + scenario + ")");
});
`

// runDeleteFlowScenario executes the shared stub-DOM harness for a scenario.
func runDeleteFlowScenario(t *testing.T, scenario string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable: skipping renderer-execution test")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.cjs")
	if err := os.WriteFile(harness, []byte(deleteFlowHarnessScript), 0o600); err != nil {
		t.Fatal(err)
	}
	sidebarPath, err := filepath.Abs(filepath.Join("static", "sidebar.html"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, harness, sidebarPath, scenario).CombinedOutput()
	if err != nil {
		t.Fatalf("delete flow harness (%s) failed: %v\n%s", scenario, err, out)
	}
}

// TestDeleteFlowRefreshesOnlyDeletedProvider (success scenario) proves: the
// confirm text names Kimi Code, confirmOk deletes the Kimi account, the
// modal closes, only /api/kimi refreshes (from the confirm-time snapshot),
// and the snapshot survives an Ollama confirm opened mid-refresh-delay.
func TestDeleteFlowRefreshesOnlyDeletedProvider(t *testing.T) {
	runDeleteFlowScenario(t, "success")
}

// TestDeleteFailureKeepsModalOpenAndShowsError proves a {success:false}
// response keeps the confirm modal open, shows "删除失败：<server error>" in
// the confirm region, and refreshes NO provider.
func TestDeleteFailureKeepsModalOpenAndShowsError(t *testing.T) {
	runDeleteFlowScenario(t, "failure")
}

// TestDeleteNetworkFailureKeepsModalOpenAndShowsError proves a network
// failure keeps the confirm modal open, shows "删除失败：网络错误", and
// refreshes NO provider.
func TestDeleteNetworkFailureKeepsModalOpenAndShowsError(t *testing.T) {
	runDeleteFlowScenario(t, "network")
}

// TestDeleteMalformedBodyShowsUnknownError proves an unparseable response
// body is reported as "删除失败：未知错误" — NOT "网络错误" — with the modal
// open and no refresh.
func TestDeleteMalformedBodyShowsUnknownError(t *testing.T) {
	runDeleteFlowScenario(t, "malformed")
}

// TestStaleDeleteSuccessDoesNotTouchNewerModal proves a success response
// arriving AFTER a new Ollama B confirm was opened does not close or rewrite
// B's modal, while still refreshing the original deleted provider (/api/kimi)
// from the confirm-time snapshot.
func TestStaleDeleteSuccessDoesNotTouchNewerModal(t *testing.T) {
	runDeleteFlowScenario(t, "stale-success")
}

// TestStaleDeleteFailureDoesNotTouchNewerModal proves a failure response
// arriving after a new Ollama B confirm was opened leaves B's modal and text
// untouched and refreshes nothing.
func TestStaleDeleteFailureDoesNotTouchNewerModal(t *testing.T) {
	runDeleteFlowScenario(t, "stale-failure")
}

// TestDoubleConfirmSendsSingleRequest proves clicking #confirmOk twice in a
// row sends exactly ONE /api/delete (per-account in-flight re-entrancy
// guard — the server delete is not idempotent and a duplicate would fail
// with "account not found"), and the success state (modal closed, deleted
// provider refreshed) is not overwritten by a phantom second failure.
func TestDoubleConfirmSendsSingleRequest(t *testing.T) {
	runDeleteFlowScenario(t, "double-confirm")
}

// TestReopenSameAccountCannotBypassGuard proves closing and REOPENING the
// same account's delete confirm while the request is in flight cannot send
// a duplicate /api/delete (per-account in-flight guard keyed by
// provider+name), and the ORIGINAL success closes the reopened dialog and
// refreshes /api/kimi.
func TestReopenSameAccountCannotBypassGuard(t *testing.T) {
	runDeleteFlowScenario(t, "reopen")
}

// TestReopenBeforeRefreshStillBlocked proves that after a SUCCESS response
// the per-account guard is held until the provider refresh settles: a
// reopen+confirm of the same account between success and the refresh timer
// cannot send a duplicate /api/delete (which would fail with
// account-not-found), the stale dialog is closed/invalidated at refresh
// settle, and confirming it afterwards must NOT send a second request.
func TestReopenBeforeRefreshStillBlocked(t *testing.T) {
	runDeleteFlowScenario(t, "reopen-before-refresh")
}

// TestRecreatedAccountDeletableAgain proves that once the refresh has
// settled and the account is re-added (a new card appears and a fresh
// delItem confirm is opened), deleting the same provider+name again is
// allowed: the guard is released and pend is reset by the new delItem, so a
// new /api/delete is sent.
func TestRecreatedAccountDeletableAgain(t *testing.T) {
	runDeleteFlowScenario(t, "recreated")
}
