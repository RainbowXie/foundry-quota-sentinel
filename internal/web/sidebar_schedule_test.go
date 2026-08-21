package web

// RED characterization tests for the shared sidebar refresh scheduler
// (openspec change stabilize-opencode-quota-refresh).
//
// The harness executes the REAL embedded sidebar <script> block in a node
// VM with a fake clock and deferred fetch promises, then drives explicit
// scenarios (advance time, resolve/reject requests, call public handlers).
// Assertions below encode the SPEC behavior; they fail (RED) against the
// current sidebar and must pass (GREEN) after the scheduler lands.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sidebarSchedulerNodeHarness is the node driver: it evals the exact
// inline <script> shipped in sidebar.html with stubbed browser/timer/fetch
// primitives and replays the scenario steps passed in testcase.json.
const sidebarSchedulerNodeHarness = `
const fs = require("fs");
const vm = require("vm");

const html = fs.readFileSync(process.argv[2], "utf8");
const scenario = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));

// Extract the real embedded sidebar <script> block (the one defining the
// provider refresh functions). echarts.min.js is an external src block and
// yields empty content, so filtering on fq/fa/quotaProviders is safe.
const blocks = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)]
  .map((m) => m[1])
  .filter(
    (s) =>
      s.includes("function fq") ||
      s.includes("function fa") ||
      s.includes("quotaProviders"),
  );
if (!blocks.length) {
  console.error("sidebar script block not found");
  process.exit(2);
}
const script = blocks.join("\n");

/* ---- fake clock ---- */
let now = 0;
let timers = [];
let nextTimerId = 1;
function fakeSetTimeout(fn, delay) {
  const id = nextTimerId++;
  timers.push({ id, fn, at: now + (delay || 0), interval: 0 });
  return id;
}
function fakeSetInterval(fn, delay) {
  const id = nextTimerId++;
  timers.push({ id, fn, at: now + (delay || 0), interval: delay || 0 });
  return id;
}
function fakeClearTimeout(id) {
  timers = timers.filter((t) => t.id !== id);
}
function fakeClearInterval(id) {
  timers = timers.filter((t) => t.id !== id);
}
function advance(ms) {
  const target = now + ms;
  let guard = 0;
  while (guard++ < 100000) {
    const due = timers
      .filter((t) => t.at <= target)
      .sort((a, b) => a.at - b.at);
    if (!due.length) break;
    const t = due[0];
    timers = timers.filter((x) => x.id !== t.id);
    now = t.at;
    t.fn();
    if (t.interval > 0) {
      timers.push({
        id: t.id,
        fn: t.fn,
        at: t.at + t.interval,
        interval: t.interval,
      });
    }
  }
  now = target;
}
const flush = () => new Promise((r) => setImmediate(r));

/* ---- deferred fetch ---- */
const requests = [];
let pending = [];
// shellData overrides the auto-resolved shell endpoint payloads per
// endpoint (e.g. after a login poll the account list grows). Tests set
// this between steps via the "setShellData" op. Default: empty list.
const shellData = {};
// Shell endpoints (/api/<provider>/accounts — note /api/accounts alone is
// the OpenCode FILL endpoint, NOT a shell) are LOCAL CONFIG READS in
// production — never slow. The harness auto-resolves them immediately so
// the scheduled fill (which follows the shell in the same chain) is what
// the timing assertions observe. Quota-fill endpoints stay deferred so
// tests can hold them pending / reject / settle explicitly.
const shellEndpoint = (u) => /\/api\/[a-z]+\/accounts(\?|$)/.test(u);
function deferredFetch(url) {
  const rec = { url: String(url), start: now, end: null };
  requests.push(rec);
  return new Promise((resolve, reject) => {
    if (shellEndpoint(String(url))) {
      rec.end = now;
      const data = shellData[String(url)] || [];
      resolve({ ok: true, json: () => Promise.resolve({ success: true, data: data }) });
      return;
    }
    pending.push({ rec, resolve, reject });
  });
}
function resolveOne(urlSub, data) {
  const idx = pending.findIndex((p) => p.rec.url.includes(urlSub));
  if (idx < 0) return false;
  const [p] = pending.splice(idx, 1);
  p.rec.end = now;
  p.resolve({ ok: true, json: () => Promise.resolve(data || { success: true, data: [] }) });
  return true;
}
function rejectOne(urlSub) {
  const idx = pending.findIndex((p) => p.rec.url.includes(urlSub));
  if (idx < 0) return false;
  const [p] = pending.splice(idx, 1);
  p.rec.end = now;
  p.reject(new Error("network error"));
  return true;
}

/* ---- minimal DOM stub (records #ht clock writes + card container html) ---- */
const containerIds = ["accountCards", "ollamaCards", "kimiCards", "dsCards", "commandCodeCards"];
const containerWrites = []; // { id, t, html }
function makeEl(id) {
  const el = {
    textContent: "",
    innerHTML: "",
    className: "",
    classList: { add() {}, remove() {}, toggle() {} },
    addEventListener() {},
    removeEventListener() {},
    appendChild() {},
    append() {},
    remove() {},
    setAttribute() {},
    getAttribute: () => null,
    focus() {},
    select() {},
    closest: () => null,
    contains: () => false,
    offsetWidth: 0,
    offsetHeight: 0,
    style: {},
  };
  if (id === "ht") {
    Object.defineProperty(el, "textContent", {
      get() { return this.__tc; },
      set(v) {
        this.__tc = v;
        clockLog.push(now);
      },
      configurable: true,
    });
  }
  if (containerIds.indexOf(id) >= 0) {
    Object.defineProperty(el, "innerHTML", {
      get() { return this.__ih; },
      set(v) {
        this.__ih = v;
        containerWrites.push({ id: id, t: now, html: String(v) });
      },
      configurable: true,
    });
  }
  return el;
}
const els = {};
const clockLog = [];
function getElementById(id) {
  if (!els[id]) els[id] = makeEl(id);
  return els[id];
}
const documentStub = {
  getElementById,
  createElement: () => makeEl(""),
  addEventListener() {},
  removeEventListener() {},
  documentElement: { setAttribute() {}, getAttribute: () => null },
};

/* ---- sandbox ---- */
const sandbox = {
  console,
  document: documentStub,
  fetch: deferredFetch,
  setTimeout: fakeSetTimeout,
  setInterval: fakeSetInterval,
  clearTimeout: fakeClearTimeout,
  clearInterval: fakeClearInterval,
  alert() {},
  confirm: () => false,
  localStorage: { getItem: () => null, setItem() {} },
  performance: { now: () => 0 },
  requestAnimationFrame: () => 0,
  addEventListener() {},
  removeEventListener() {},
  close() {},
  outerWidth: 0,
  outerHeight: 0,
};
sandbox.window = sandbox;
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
vm.runInContext(script, sandbox, { filename: "sidebar.html" });

(async function run() {
  await flush();
  for (const step of scenario.steps || []) {
    switch (step.op) {
      case "advance":
        advance(step.ms);
        break;
      case "resolve":
        if (!resolveOne(step.url, step.data)) {
          console.error("no pending request matching " + step.url);
          process.exit(3);
        }
        break;
      case "reject":
        if (!rejectOne(step.url)) {
          console.error("no pending request matching " + step.url);
          process.exit(4);
        }
        break;
      case "resolveAll":
        pending.forEach((p) => {
          p.rec.end = now;
          p.resolve({ ok: true, json: () => Promise.resolve({ success: true, data: [] }) });
        });
        pending = [];
        break;
      case "call":
        if (typeof sandbox[step.fn] !== "function") {
          console.error("sandbox function missing: " + step.fn);
          process.exit(5);
        }
        sandbox[step.fn]();
        break;
      case "registryRefresh": {
        const prov = (sandbox.quotaProviders || []).find(
          (p) => p.type === step.provider,
        );
        if (!prov || typeof sandbox[prov.refresh] !== "function") {
          console.error("registry refresh missing for " + step.provider);
          process.exit(6);
        }
        sandbox[prov.refresh]();
        break;
      }
      case "setShellData": {
        // Set the auto-resolved shell payload for one endpoint (e.g. after
        // a login the account list grows). step.url = shell endpoint,
        // step.data = array of {name,...} shells.
        shellData[String(step.url)] = step.data || [];
        break;
      }
      case "checkContainer": {
        // Assert the current innerHTML of a card container matches the
        // expected account presence. Fields:
        //   id        container id
        //   wantName  an account that MUST be present (data-name)
        //   wantName2 optional second account that MUST be present
        //   notWantName an account that MUST be absent
        //   noLoading  when true, "加载中" must be absent
        const el = getElementById(String(step.id));
        const html = String(el.innerHTML || "");
        const has = (n) => html.indexOf('data-name="' + n + '"') >= 0;
        if (step.wantName && !has(step.wantName)) {
          console.error("checkContainer " + step.id + ": missing " + step.wantName + " in " + html);
          process.exit(9);
        }
        if (step.wantName2 && !has(step.wantName2)) {
          console.error("checkContainer " + step.id + ": missing " + step.wantName2 + " in " + html);
          process.exit(10);
        }
        if (step.notWantName && has(step.notWantName)) {
          console.error("checkContainer " + step.id + ": unexpected " + step.notWantName + " in " + html);
          process.exit(11);
        }
        if (step.noLoading && html.indexOf("加载中") >= 0) {
          console.error("checkContainer " + step.id + ": unexpected 加载中 in " + html);
          process.exit(12);
        }
        break;
      }
      default:
        console.error("unknown op " + step.op);
        process.exit(7);
    }
    await flush();
  }
  const out = {
    now,
    requests: requests.map((r) => ({ url: r.url, start: r.start, end: r.end })),
    clockWrites: clockLog,
    containerWrites: containerWrites,
  };
  process.stdout.write(JSON.stringify(out));
})().catch((e) => {
  console.error(e);
  process.exit(8);
});
`

// schedulerStep is one scenario step replayed against the sidebar script.
type schedulerStep struct {
	Op       string `json:"op"`                 // advance | resolve | reject | resolveAll | call | registryRefresh | setShellData | checkContainer
	Ms       int    `json:"ms"`                 // fake-clock milliseconds (0 serialized explicitly)
	URL      string `json:"url,omitempty"`      // endpoint substring for resolve/reject
	Data     any    `json:"data,omitempty"`     // response payload for resolve
	Fn       string `json:"fn,omitempty"`       // sandbox function for call
	Provider string `json:"provider,omitempty"` // provider type for registryRefresh
	ID       string `json:"id,omitempty"`       // container id for checkContainer
	// checkContainer assertions.
	WantName    string `json:"wantName,omitempty"`
	WantName2   string `json:"wantName2,omitempty"`
	NotWantName string `json:"notWantName,omitempty"`
	NoLoading   bool   `json:"noLoading,omitempty"`
}

// schedulerObservation is the JSON the node harness prints.
type schedulerObservation struct {
	Now      int `json:"now"`
	Requests []struct {
		URL   string `json:"url"`
		Start int    `json:"start"`
		End   *int   `json:"end"`
	} `json:"requests"`
	ClockWrites []int `json:"clockWrites"`
	// ContainerWrites records every innerHTML write to the provider card
	// containers with the fake-clock timestamp, for asserting that a
	// periodic refresh does NOT re-render loading shells over existing
	// cards (the shell guard).
	ContainerWrites []struct {
		ID   string `json:"id"`
		T    int    `json:"t"`
		HTML string `json:"html"`
	} `json:"containerWrites"`
}

// runSchedulerScenario executes the real sidebar script under a fake clock
// and deferred fetches. Skips when node is unavailable.
func runSchedulerScenario(t *testing.T, steps []schedulerStep) schedulerObservation {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable: skipping renderer-execution test")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.cjs")
	if err := os.WriteFile(harness, []byte(sidebarSchedulerNodeHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"steps": steps})
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
		t.Fatalf("node scheduler harness failed: %v\n%s", err, out)
	}
	var obs schedulerObservation
	if err := json.Unmarshal(out, &obs); err != nil {
		t.Fatalf("harness output not JSON: %v\n%s", err, out)
	}
	return obs
}

// requestCounts groups request starts by endpoint, preserving order.
func requestCounts(obs schedulerObservation) map[string]int {
	m := map[string]int{}
	for _, r := range obs.Requests {
		key := r.URL
		if len(key) > 0 && key[0] == '/' {
			key = r.URL
		}
		m[key]++
	}
	return m
}

// startsOf returns the fake-clock start times of requests matching urlSub.
func startsOf(obs schedulerObservation, urlSub string) []int {
	var out []int
	for _, r := range obs.Requests {
		if pathMatches(r.URL, urlSub) {
			out = append(out, r.Start)
		}
	}
	return out
}

// pathMatches reports whether the request URL's PATH exactly equals urlSub
// (or equals urlSub + "/..." only when urlSub already has a trailing path
// segment boundary). A substring match would wrongly count a shell endpoint
// (/api/ollama/accounts) as the fill endpoint (/api/ollama).
func pathMatches(u, sub string) bool {
	// Strip any query string.
	if q := strings.Index(u, "?"); q >= 0 {
		u = u[:q]
	}
	return u == sub
}

// countAt returns how many of the given start times equal t.
func countAt(times []int, t int) int {
	n := 0
	for _, x := range times {
		if x == t {
			n++
		}
	}
	return n
}

// TestSidebarClockTicksIndependentlyEverySecond (task 1.5) proves the clock
// updates at 0s and every 1s WITHOUT any provider fetch resolving and
// WITHOUT creating provider requests. RED: current sidebar couples the
// clock to the fq() network call inside fa().
func TestSidebarClockTicksIndependentlyEverySecond(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 3000}, // nothing resolves: network pending forever
	})
	// Clock must render at 0, 1000, 2000, 3000.
	want := []int{0, 1000, 2000, 3000}
	if len(obs.ClockWrites) != len(want) {
		t.Fatalf("clock writes = %v, want exactly %v (clock must tick 1/s independent of network)", obs.ClockWrites, want)
	}
	for i, w := range obs.ClockWrites {
		if w != want[i] {
			t.Fatalf("clock writes = %v, want %v", obs.ClockWrites, want)
		}
	}
	// The clock must not create QUOTA-FILL requests. The immediate first
	// load fires one /api/accounts (plus its config shell), but the clock
	// itself never initiates any provider request, so after the initial
	// burst no further fill requests may appear.
	acc := startsOf(obs, "/api/accounts")
	if len(acc) > 1 {
		t.Fatalf("clock must not initiate /api/accounts fetches, saw %d requests at %v", len(acc), acc)
	}
	if len(acc) != 1 {
		t.Fatalf("expected exactly the immediate first load of /api/accounts, got %d at %v", len(acc), acc)
	}
}

// TestSidebarImmediateLoadThenThirtySecondPostSettlement (task 1.5) proves
// every provider loads immediately once, and the next automatic request
// starts only 30s after settlement — not on a fixed interval. RED: the
// current 2s OpenCode loop and 30s setInterval loops violate both.
func TestSidebarImmediateLoadThenThirtySecondPostSettlement(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0}, // immediate first loads fire
		{Op: "resolveAll"},     // settle at t=0
		{Op: "advance", Ms: 29999},
	})
	// Before 30s no provider may request again.
	for _, ep := range []string{"/api/accounts", "/api/deepseek", "/api/ollama", "/api/kimi"} {
		if n := len(startsOf(obs, ep)); n != 1 {
			t.Fatalf("%s requested %d times by t=29999, want exactly 1 (immediate load only)", ep, n)
		}
	}
	obs2 := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"},
		{Op: "advance", Ms: 30000},
	})
	// At t=30s exactly one post-settlement request per provider starts.
	for _, ep := range []string{"/api/accounts", "/api/deepseek", "/api/ollama", "/api/kimi"} {
		times := startsOf(obs2, ep)
		if len(times) != 2 {
			t.Fatalf("%s requested %d times in 30s window, want exactly 2 (immediate + one at +30s): %v", ep, len(times), times)
		}
		if times[0] != 0 || times[1] != 30000 {
			t.Fatalf("%s start times = %v, want [0 30000]", ep, times)
		}
	}
}

// TestSidebarNoOverlappingProviderRequests (task 1.5) proves a provider
// whose refresh exceeds the polling delay never starts a second request
// while the first is pending. RED: the current code starts a fresh request
// every 2s for OpenCode regardless of in-flight state.
func TestSidebarNoOverlappingProviderRequests(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 35000}, // nothing resolves: every provider stays in flight
	})
	for _, ep := range []string{"/api/accounts", "/api/deepseek", "/api/ollama", "/api/kimi"} {
		times := startsOf(obs, ep)
		if len(times) != 1 {
			t.Fatalf("%s started %d requests while first was pending, want exactly 1 (no overlap): %v", ep, len(times), times)
		}
	}
}

// TestSidebarExplicitRefreshJoinsInFlightRequest (task 1.6) proves an
// explicit trigger while a provider is busy joins the pending refresh
// instead of starting a duplicate request.
func TestSidebarExplicitRefreshJoinsInFlightRequest(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0}, // automatic first load pending
		{Op: "call", Fn: "fq"}, // explicit trigger while busy
		{Op: "advance", Ms: 0},
	})
	times := startsOf(obs, "/api/accounts")
	if len(times) != 1 {
		t.Fatalf("explicit fq() while busy must join the pending request, got %d /api/accounts requests at %v", len(times), times)
	}
}

// TestSidebarExplicitIdleRefreshResetsDeadline (task 1.6) proves an
// explicit refresh while idle runs immediately and the next automatic
// attempt is 30s after ITS settlement, not the previous schedule.
func TestSidebarExplicitIdleRefreshResetsDeadline(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"}, // settle initial at t=0; auto deadline now t=30000
		{Op: "advance", Ms: 5000},
		{Op: "call", Fn: "fq"}, // explicit idle refresh at t=5000
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"},         // settle explicit at t=5000; new auto deadline t=35000
		{Op: "advance", Ms: 29000}, // now at t=34000 < 35000
	})
	times := startsOf(obs, "/api/accounts")
	if len(times) != 2 {
		t.Fatalf("expected immediate load (0) + explicit refresh (5000) only, got %d requests at %v", len(times), times)
	}
	if times[0] != 0 || times[1] != 5000 {
		t.Fatalf("/api/accounts start times = %v, want [0 5000]", times)
	}
	// A 3rd request must not fire before the reset deadline (35000).
	obs2 := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"},
		{Op: "advance", Ms: 5000},
		{Op: "call", Fn: "fq"},
		{Op: "advance", Ms: 0},
		{Op: "resolveAll"},
		{Op: "advance", Ms: 30000}, // now at t=35000: deadline reached
	})
	times2 := startsOf(obs2, "/api/accounts")
	if len(times2) != 3 || times2[2] != 35000 {
		t.Fatalf("auto deadline after explicit idle refresh must be t=35000, got %v", times2)
	}
}

// TestSidebarFailureRearmsNextAutomaticAttempt (task 1.6) proves a failed
// refresh clears in-flight state and schedules the next attempt 30s later.
func TestSidebarFailureRearmsNextAutomaticAttempt(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "reject", URL: "api/accounts"}, // opencode fails at t=0
		{Op: "advance", Ms: 29999},
	})
	if n := len(startsOf(obs, "/api/accounts")); n != 1 {
		t.Fatalf("after failure no new attempt may start before 30s, got %d", n)
	}
	obs2 := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "reject", URL: "api/accounts"},
		{Op: "advance", Ms: 30000},
	})
	times := startsOf(obs2, "/api/accounts")
	if len(times) != 2 || times[1] != 30000 {
		t.Fatalf("failure must rearm the next automatic attempt at t=30000, got %v", times)
	}
}

// TestSidebarSlowProviderDoesNotBlockOthers (task 1.6) proves one
// provider's pending request never delays another provider's schedule.
func TestSidebarSlowProviderDoesNotBlockOthers(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// Resolve everything except kimi: opencode/deepseek/ollama settle at 0.
		{Op: "resolve", URL: "api/accounts"},
		{Op: "resolve", URL: "api/deepseek"},
		{Op: "resolve", URL: "api/ollama"},
		{Op: "advance", Ms: 30000}, // kimi still pending
	})
	// Kimi: still pending → no new request (no overlap).
	if n := len(startsOf(obs, "/api/kimi")); n != 1 {
		t.Fatalf("kimi pending must not start a second request, got %d", n)
	}
	// Others: one post-settlement request at t=30000 each.
	for _, ep := range []string{"/api/accounts", "/api/deepseek", "/api/ollama"} {
		times := startsOf(obs, ep)
		if len(times) != 2 || times[1] != 30000 {
			t.Fatalf("%s must refresh at t=30000 independent of kimi, got %v", ep, times)
		}
	}
}

// TestSidebarRegistryExplicitRefreshAcrossBusyProvider (review follow-up,
// sidebar-refresh-scheduling spec scenario) proves an explicit refresh for
// one provider issued through the public registry (quotaProviders[].refresh)
// runs immediately even while ANOTHER provider's automatic refresh is still
// in flight, without duplicating the busy provider's request.
func TestSidebarRegistryExplicitRefreshAcrossBusyProvider(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// OpenCode automatic refresh stays pending; Ollama settles at 0.
		{Op: "resolve", URL: "api/deepseek"},
		{Op: "resolve", URL: "api/kimi"},
		{Op: "resolve", URL: "api/ollama"},
		// Explicit Ollama refresh via the provider registry while OpenCode
		// is busy: must start immediately (t=0), joining nothing.
		{Op: "registryRefresh", Provider: "ollama"},
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/ollama"}, // settle the explicit refresh at 0
		{Op: "advance", Ms: 0},
	})
	// OpenCode: exactly ONE request, still pending — the explicit Ollama
	// action must not duplicate or disturb it.
	acc := startsOf(obs, "/api/accounts")
	if len(acc) != 1 {
		t.Fatalf("explicit ollama refresh must not duplicate the busy opencode request, got %d opencode requests at %v", len(acc), acc)
	}
	// Ollama: immediate load + explicit registry refresh = 2 at t=0.
	oll := startsOf(obs, "/api/ollama")
	if len(oll) != 2 || countAt(oll, 0) != 2 {
		t.Fatalf("ollama must refresh immediately via registry (t=0), got %v", oll)
	}
	// The explicit refresh resets ollama's automatic deadline to 30s after
	// ITS settlement (t=0 → t=30000), not sooner.
	obs2 := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/deepseek"},
		{Op: "resolve", URL: "api/kimi"},
		{Op: "resolve", URL: "api/ollama"},
		{Op: "registryRefresh", Provider: "ollama"},
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/ollama"},
		{Op: "advance", Ms: 29999},
	})
	if n := len(startsOf(obs2, "/api/ollama")); n != 2 {
		t.Fatalf("after explicit idle ollama refresh no auto attempt may fire before +30s, got %d", n)
	}
}
