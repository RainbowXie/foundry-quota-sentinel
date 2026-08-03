# Evidence — stabilize-opencode-quota-refresh

Implementation evidence for stabilizing the OpenCode Go quota response
parser and the sidebar quota-refresh scheduler.

Two independent defects were addressed:

1. **Parser**: `parseQuotaResponse` matched each usage object with ONE exact
   whole-object regular expression (`rollingUsage:(?:\$R\[\d+\]=)?\{status:"...",resetInSec:...,usagePercent:...\}`).
   Reference-number drift (`$R[n]`) had been tolerated before, but any
   other evidence-backed seroval layout variation — field reordering,
   insignificant whitespace, or additional properties inside the object —
   still produced the intermittent `failed to parse rollingUsage`.

2. **Scheduler**: the sidebar clock (`fa()`) awaited the OpenCode
   `/api/accounts` refresh, so `setInterval(fa, 2000)` started a NEW
   OpenCode refresh every two seconds without waiting for the previous one
   to settle. DeepSeek/Ollama/Kimi used raw 30s `setInterval` loops that
   could overlap when a refresh outlived the interval. The clock itself
   was coupled to network I/O.

Scope: `internal/quota/opencode.go` + `internal/quota/opencode_test.go`,
`internal/web/static/sidebar.html`, `internal/web/sidebar_schedule_test.go`
(new node-execution scheduler harness), and a wiring assertion update in
`internal/web/delete_flow_test.go`. No API response schema, persisted
config, credentials, or displayed quota semantics changed. All fixtures
are minimal synthetic stand-ins with NO cookies, workspace IDs, headers,
raw private bodies, or account material.

## Impact analysis (task 1.1)

`gitnexus_impact` (upstream, `parseQuotaResponse`, repo
`foundry-quota-sentinel`):

- Risk: **CRITICAL** (exact); 7 impacted symbols; d=1 (WILL BREAK):
  `OpenCodeGoQuerier.FetchQuota`; d=2: `Server.Handler` (web),
  `cmdQuota`, `cmdWatch`, `cmdLoginOpenCode`.
- 5 affected process groups: `cmdQuota`, `cmdWatch`, `cmdLoginOpenCode`,
  `main`, and the web `Handler` serving `/api/accounts` (+ name-filtered
  variant).
- Modules: Quota (direct), Web (indirect).

Manual sidebar map (GitNexus cannot index the embedded script): `fa()`
(2s loop → `fq()` + clock), `fq` (`/api/accounts`), `fd`
(`/api/deepseek`), `fo` (`/api/ollama`), `fk` (`/api/kimi`); callers:
`fa`, `setInterval(fa,2000)`, `setInterval(fd/fo/fk,30000)`, login
completion polls (`kimiLoginPoll`→`fk`, `dsLoginPoll`→`fd`),
`olDoLogin`→`setTimeout(fo,1500)`, post-delete registry
`window[p.refresh]`, and the provider registry `quotaProviders[].refresh`.

## RED characterization (tasks 1.2–1.6)

Parser (current implementation, pre-change):

- `reordered fields` fixture → `failed to parse rollingUsage`
- `whitespace` fixture → `failed to parse rollingUsage`
- `additional properties` fixture → `failed to parse rollingUsage`
- malformed `monthlyUsage:{status:"ok",resetInSec:-1,...}` accepted
  (negative value slipped through the old regex as valid monthly)
- duplicate `rollingUsage` object accepted (old regex took first match)

Scheduler (current implementation, pre-change, node harness executing the
REAL embedded script with fake clock + deferred fetch):

- `TestSidebarClockTicksIndependentlyEverySecond` — FAIL (clock writes `[]`
  with network pending; the clock is coupled to the quota fetch)
- `TestSidebarImmediateLoadThenThirtySecondPostSettlement` — FAIL
- `TestSidebarNoOverlappingProviderRequests` — FAIL (`/api/accounts`
  started **18 requests** in 35s while the first was still pending)
- `TestSidebarExplicitRefreshJoinsInFlightRequest` — FAIL
- `TestSidebarExplicitIdleRefreshResetsDeadline` — FAIL
- `TestSidebarFailureRearmsNextAutomaticAttempt` — FAIL
- `TestSidebarSlowProviderDoesNotBlockOthers` — FAIL

## Implementation (tasks 2.1–2.3, 3.1–3.4)

Parser (`internal/quota/opencode.go`):

- Bounded structural extraction: locate `rollingUsage`/`weeklyUsage`/
  `monthlyUsage` only at a field boundary, skip the optional seroval
  reference assignment `$R[n]=`, and extract the exact bounded `{...}`
  object with quoted-string/brace-aware scanning (escapes and nested
  braces cannot truncate it).
- Order-independent field parsing: `status` (quoted string),
  `resetInSec`/`usagePercent` (non-negative integer, digits only) are read
  in any order; unknown properties are ignored; missing, duplicate,
  negative, non-numeric, or unsupported values fail closed with
  window-specific errors (`failed to parse rollingUsage: ...`).
- Rolling and weekly remain required; monthly remains optional and keeps
  the existing `unlimited`-omission behavior, but a PRESENT monthly object
  must be valid (the old parser silently accepted a negative monthly).
- PRESENT-BUT-MALFORMED occurrences fail closed (review follow-up): the
  lookup layer now distinguishes absent / present-valid / present-malformed
  instead of silently skipping unextractable fields. A truncated object, a
  malformed `$R[...]` reference assignment, or a non-object value after a
  boundary-anchored `windowName:` returns a window-specific error — so an
  optional monthly window that is present but broken (e.g.
  `monthlyUsage:{status:"ok",resetInSec:1` truncated at end-of-body) is
  never mistaken for absent, and a malformed+valid duplicate pair cannot
  bypass duplicate detection.
- `status` is validated against the confirmed upstream allowlist
  (`ok`, `unlimited`); any other value (e.g. `garbage`) fails closed for
  every window (review follow-up).
- Field location is a SINGLE-PASS STRING-AWARE LEXER (review follow-up):
  `findAllUsageObjects` runs one persistent-cursor scan that tracks
  inString/escaped state and only recognizes boundary-anchored field names
  OUTSIDE quoted strings, so a legitimate extra string property (e.g.
  `note:" monthlyUsage: unavailable"`) or quota-shaped text inside a
  string value is ignored — it can neither fabricate a window, trigger a
  duplicate, nor produce a malformed-occurrence error. A window name
  appearing ONLY inside a string still counts as genuinely absent. The
  second real occurrence is reported as a duplicate immediately, so
  many-duplicate inputs stay O(n) instead of degrading to O(n²) with a
  per-occurrence rescan.
- Errors are fixed-string, window-specific messages — they never embed the
  response body or any authentication/account material.

Scheduler (`internal/web/static/sidebar.html`):

- `updateClock()` is fetch-free, renders immediately and every 1,000 ms;
  provider latency/failure cannot delay, skip, or reorder clock updates.
- One shared per-provider scheduler (`scheduleProviderRefresh` +
  `rearmProviderRefresh`) with per-provider in-flight promise + next-run
  timer state: immediate first load, next automatic attempt 30,000 ms
  AFTER the preceding attempt settles (recursive `setTimeout`, never a
  fixed interval), success AND failure both clear in-flight state and
  rearm.
- Every public refresh handler (`fq`/`fd`/`fo`/`fk`) — and therefore the
  provider registry, login completion polls, delete flow, and manual
  callers — routes through the single-flight boundary: an explicit trigger
  while busy JOINS the pending promise (no duplicate request); while idle
  it runs immediately and resets that provider's next-run deadline from
  its own settlement, without touching other providers' timers.
- Old `fa()` 2s loop and all provider `setInterval` loops removed.

FetchQuota HTTP→parser boundary (`internal/quota/opencode.go`, review
follow-up):

- `io.ReadAll` errors are now CHECKED and propagated (`read response: %w`);
  a connection that breaks mid-body after rolling/weekly but before an
  optional monthly can no longer be mistaken for a complete response with
  monthly absent.
- The body is read as `maxSize+1` bytes; a body exceeding 1 MiB is
  rejected with a fixed oversized-response error instead of being silently
  truncated into a partial quota result. `openCodeGoMaxResponseSize` and
  `openCodeGoRequestTimeout` are named constants.
- NON-200 errors carry ONLY the status code (`opencode API returned HTTP
  %d`) — the upstream response body is never included, so private/account
  material cannot reach CLI output, Web cards, or logs (task 2.3).
- `Client` is injectable for tests (nil = production default with the 15s
  timeout); there is NO BaseURL override seam — the request URL is always
  the fixed `https://opencode.ai` host, so the cookie cannot be sent to an
  unvalidated host. The request is built with a checked `http.NewRequest`
  error.

## GREEN + verification (tasks 2.4, 3.5, 4.1–4.2)

- Parser: `TestParseQuotaResponseREDFixtureNowParses`,
  `TestParseQuotaResponseAcceptedShapes` (14 cases: canonical, reference
  drift, inline, reordered, whitespace, additional properties, monthly
  absent, monthly unlimited, plus 6 review-follow-up string-content cases:
  window-name text in a string ignored, quota-shaped text in a string
  ignored, string cannot duplicate rolling, malformed-ref-shaped string
  ignored, escaped-quote-then-window-text ignored, prefixed name
  `xmonthlyUsage` not an occurrence), `TestParseQuotaResponseRejectsMalformed`
  (23 cases incl. review follow-up: monthly truncated, malformed reference,
  non-object value, valid→malformed and malformed→valid duplicates,
  rolling-only-inside-string genuinely absent), `TestParseQuotaResponseRejectsUnsupportedStatus`
  (4 cases: unsupported status per window + unquoted value) — all GREEN.
  Total 41 table subtests + 3 table parent nodes + 1 standalone test =
  45 PASS lines under `-run TestParseQuotaResponse`; the field scan is a
  single-pass lexer (persistent cursor, duplicate reported on the second
  real occurrence) so many-duplicate inputs stay O(n) — a 5000-duplicate
  293 KB body is rejected in milliseconds.
- FetchQuota boundary (review follow-up): `TestOpenCodeGoFetchQuotaPropagatesReadError`
  (mid-body reader failure after a valid rolling/weekly prefix must be a
  read error, never a success with monthly absent), `TestOpenCodeGoFetchQuotaRejectsOversizedResponse`
  (valid quota prefix followed by >1 MiB must be rejected with the
  oversized error, no partial result), `TestOpenCodeGoFetchQuotaParsesValidBody`
  (canonical body through the injectable transport parses correctly),
  `TestOpenCodeGoFetchQuotaNon200DoesNotLeakBody` (401 and 500 with a
  synthetic private marker: error includes the status code, never the
  marker) — all GREEN. RED-checked against the old read path: dropping the
  read error produced `err=<nil> monthly=<nil>` on the partial body; a 1
  MiB LimitReader silently truncated an oversized body into a parsed
  partial result; and the old non-200 error echoed the private body
  (`API returned 500: {…PRIVATE-MARKER…}`) — all three now fail closed.
- Scheduler: all 8 executable node-harness tests GREEN (7 original + review
  follow-up `TestSidebarRegistryExplicitRefreshAcrossBusyProvider`, which
  drives an explicit Ollama refresh through the public registry while
  OpenCode is busy and asserts Ollama refreshes immediately at t=0 with no
  OpenCode duplicate, and its automatic deadline resets to +30s).
- Consumer regression (CRITICAL blast radius): local nogui gates all
  green — `go test -tags nogui ./...` (ALL packages INCLUDING root),
  `go test -race -tags nogui ./...` (all packages), `go vet -tags nogui ./...`,
  `go build -tags nogui ./...`. The previously pre-existing
  `TestCrossProcess*` lock-test failures are fixed by propagating the
  parent test binary's build tags to the forked `_locktest` binary
  (`locktestBinaryTags`, defined in `main_locktest_nogui_test.go` /
  `main_locktest_gui_test.go`, consumed by `buildTestBinary`) so `-tags
  nogui` runs pass on webkit-less machines.
- DEFAULT (non-nogui) test/vet requires the webkit2gtk-4.0 GUI
  environment and CANNOT run on this dev machine (pkg-config reports the
  package missing; identical on the pristine tree). They are exercised
  indirectly via the Docker GUI build (`scripts/build-linux.sh`, webkit
  4.0), which succeeds end-to-end, and by `go vet -tags nogui`/`go test
  -tags nogui` for all logic. Exact commands for a GUI-equipped
  environment: `go test ./...`, `go vet ./...` (expected green there).
  touched Go files `gofmt`-clean (repo-wide `gofmt -l` still lists 4
  pre-existing historical files this change does not touch:
  `internal/formatter/format.go`, `internal/quota/deepseek.go`,
  `internal/quota/types.go`, `internal/storage/reader.go`); `git diff --check`
  clean.
- OpenSpec: `openspec validate --strict stabilize-opencode-quota-refresh`
  valid; `openspec validate --strict --all` → 6 passed, 0 failed.
- Repository secret scan over fixtures, evidence, and diffs: clean (no
  tokens, credentials, hex blobs, or account-specific values; grep exit 1
  = no matches).

## Live browser observation (task 4.3, Chrome DevTools)

Real sidebar served by the freshly-built binary at `http://127.0.0.1:8788/sidebar.html`
(one Kimi + one DeepSeek account configured). Observed for **99 seconds**
of instrumented fetch start/end times:

- Clock advances exactly once per second (`16:27:25→26→27→28` at
  t=0/1000/2000/3000ms; `16:29:23→24` at 1000ms) with no provider I/O in
  the clock callback.
- At startup the Network panel shows exactly ONE request per endpoint
  (`/api/pin-state`, `/api/accounts`, `/api/deepseek`, `/api/ollama`,
  `/api/kimi`) — immediate first loads only.
- Post-settlement cadence (start of request N minus settlement of N-1):
  - `/api/accounts` (OpenCode): 30001, 30001 ms — 3 requests, spans
    [10461,14691] [44692,50798] [80799,88222], **no overlap** (a 4.2s
    OpenCode response never collides with the next attempt).
  - `/api/ollama`: 30001, 30001 ms — 3 requests, no overlap.
  - `/api/kimi`: 30006, 30001 ms — 3 requests, no overlap.
  - `/api/deepseek`: 30012, 30014 ms — 3 requests, no overlap.
- No two same-provider requests were ever in flight simultaneously
  (interval-overlap check across all spans: `overlap:false` for every
  provider).
- Cards render (Kimi card innerHTML 828 chars incl. Rolling/Weekly/Monthly
  + Kimi/Code detail; DeepSeek balance + chart 13037 chars); add-account
  modal opens with all 4 provider options and closes; right-click on a
  provider card opens the context menu and the delete confirm modal —
  all intact.

## Follow-up (not in this change)

None. Any newly observed upstream seroval shape that fails should first
become a sanitized RED fixture before further parser widening (per the
change's migration plan).
