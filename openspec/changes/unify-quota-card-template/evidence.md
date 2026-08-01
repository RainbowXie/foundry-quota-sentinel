# Evidence — unify-quota-card-template

Implementation evidence for the unified quota-card sidebar template change.
The change (1) introduces a shared data-driven quota-card foundation for
allowance-style providers, (2) migrates OpenCode Go / Ollama / Kimi cards to
it, (3) replaces Kimi's absolute reset-date presentation with compact
relative durations derived from `reset_in_sec`, and (4) makes the Add Account
provider selector a single data-driven registry.

All edits live in `internal/web/static/sidebar.html` (embedded static asset)
plus test files; zero production Go changes.

## Task 1.1 — Impact analysis (GitNexus attempted; JS symbols not indexed → manual)

GitNexus MCP was attempted for the mandated pre-edit impact analysis. The
edited symbols are JavaScript functions inside the embedded
`internal/web/static/sidebar.html`, which GitNexus does not index —
`impact`/`context` queries for `qrow`, `acard`, `olcard`, `kcard`, `krow`,
`ktotal` all returned target-not-found / risk UNKNOWN. A manual call-chain
impact analysis was therefore performed and is recorded below.

| Symbol | Callers | Blast radius |
|--------|---------|--------------|
| `qrow(label,u,cl)` (JS) | `acard`, `olcard` (both migrated to shared foundation, then removed) | OpenCode/Ollama row rendering |
| `acard(a)` (JS) | `fq()` only (OpenCode fetch/render loop) → `#accountCards` | OpenCode Go card |
| `olcard(a)` (JS) | `fo()` only (Ollama fetch/render loop) → `#ollamaCards` | Ollama card |
| `kcard(a)` (JS) | `fk()` only (Kimi fetch/render loop) → `#kimiCards` | Kimi card |
| `krow`/`ktotal`/`kpct` (JS) | `kcard()` only | Kimi decimal rows (removed; superseded by shared renderer + adapter) |
| `renderQuotaCard`/`renderQuotaRow` (new) | `acard`/`olcard`/`kcard` adapters | shared card/row shell |
| `formatDurationCompact` (new) | `renderQuotaRow` | shared reset column |
| provider registry (`quotaProviders`) (new) | Add Account modal step-one + step-two dispatch | selector data source |
| `.olLogin`/`.kimiLogin` delegation | `#ollamaCards`/`#kimiCards` click listeners | re-login actions (now rendered as `<button>`) |
| shared grid/CSS selectors | `#accountCards`/`#ollamaCards`/`#kimiCards` containers | card layout (unchanged selectors) |

NOT touched: server handlers (`/api/accounts`, `/api/ollama`, `/api/kimi`,
`/api/open`, `/api/delete`), quota parsers/DTOs, authentication, refresh
persistence, credential storage, CLI output, DeepSeek's specialized
chart/balance renderer, or the archived `add-kimi-code-provider` /
`fix-kimi-card-layout` changes. Risk: LOW (presentation-only; no shared
renderer symbols indexed, manual map shows containment).

## RED evidence (all against the pre-change renderer / before shared foundation)

Executed via `go test ./internal/web/ -run 'TestFormatDurationCompact|TestOpenCodeGoCard|TestOllamaCard|TestKimiCard|TestSharedFoundation|TestAllowanceProviders|TestProviderAdapters|TestSyntheticFuture|TestAddAccount'`:

- `TestFormatDurationCompactBoundaries` / `TestFormatDurationCompactInvalidValues` — FAIL: node harness reported "shared quota-card script block not found" (`formatDurationCompact` did not exist).
- `TestOpenCodeGoCardStructure` — FAIL: node harness could not find `opencodeAdapter`.
- `TestOllamaCardStructure` / `TestOllamaErrorWithReLogin` — FAIL (same missing-adapter cause).
- `TestKimiCardStructure` — FAIL (no `kimiAdapter`; old `kcard` used `krow`/`ktotal` with `reset_display`).
- `TestKimiPercentagePreservedWithCompactResets` — FAIL: absolute reset strings (`07-31 16:58` etc.) still present; no `5h`/`2d`/`23d`.
- `TestSharedFoundation*`, `TestSyntheticFutureProvider`, `TestAllowanceProvidersShareDesignTokens`, `TestProviderAdaptersReturnViewModels` — FAIL (shared foundation absent).
- `TestAddAccountOptionsAreNameOnly` — FAIL: `.prov-d` subtitle nodes and obsolete descriptions present; options not centered.
- Existing Kimi regression tests (grid parity, mapping, breakdown, truthful fill, booster absence) — PASS (retained behavior; they codify the pre-change contract that the shared foundation must preserve).

The node renderer-execution layer evals the exact inline `<script>` block
shipped to the browser (stubbed DOM via a universal absorbing proxy) and
renders distinct synthetic DTO values, so a swapped or reused field mapping
cannot pass undetected.

## GREEN + verification (sections 2-3)

Implementation (`internal/web/static/sidebar.html` + test files):

- **2.1** `formatDurationCompact(seconds)` — pure formatter matching Go
  `FormatDurationCompact`: floors the largest applicable whole unit
  (`<60`→s, `<3600`→m, `<86400`→h, else→d); clamps invalid/negative/expired
  values to `0s`.
- **2.2** Shared foundation: `renderQuotaCard(view)` owns the shell/header,
  error region, ordered rows, and typed extension slots (header badge,
  body blocks, footer, row details, declarative error/action slot);
  `renderQuotaRow(row)` owns only the standard label/track/fill/percentage/
  reset/detail placement. `formatPercent` applies declarative precision
  (0 = integer, 1-2 = decimals with trailing-zero trim). All text escaped
  at renderer boundaries (`qesc`). Row renderer is null/non-finite safe:
  `renderQuotaRow(null)` → empty, NaN/Infinity percent → `—` placeholder,
  and ONE validated numeric value drives color, fill width, and text.
- **2.3/2.4/2.5** Provider adapters `opencodeAdapter`/`ollamaAdapter`/
  `kimiAdapter` convert DTOs into view models: OpenCode Go → Rolling/Weekly/
  optional Monthly (integer); Ollama → Session/Weekly (integer, no Monthly);
  Kimi → five_hour→Rolling, seven_day→Weekly, total→Monthly (up to 2
  decimals), `reset_in_sec` drives the shared reset column, Monthly carries
  the `Kimi <v>%`/`Code <v>%` breakdown through the typed row-detail slot.
  `acard`/`olcard`/`kcard` are thin wrappers over the adapters.
- **2.6** Removed obsolete duplicated renderers `qrow`/`krow`/`ktotal`/`kpct`
  and the `.kbr` CSS family (replaced by shared `.qrd` row-detail rules);
  DeepSeek's specialized chart/balance renderer and namespaced extension
  styles retained.
- **2.7** Event delegation stays data-driven and per-container
  (`#ollamaCards`/`#kimiCards` click listeners match declared action classes
  and read `data-name`); shared renderers contain no provider-name branches;
  shared actions render only `data-name` (never credentials); adapters
  declare only their own extension content.
- **2.8 + CRITICAL fix (post-review)** Add Account provider selector is now
  a single data-driven registry:

  ```js
  var quotaProviders = [
      { type: "opencode", label: "OpenCode Go", defaultName: "OpenCode", login: "ocDoLogin" },
      { type: "deepseek", label: "DeepSeek", defaultName: "DeepSeek", login: "dsDoLogin" },
      { type: "ollama", label: "Ollama", defaultName: "Ollama", login: "olDoLogin" },
      { type: "kimi", label: "Kimi Code", defaultName: "Kimi", login: "kimiDoLogin" }
  ];
  ```

  Step-one options are rendered FROM this registry as keyboard-focusable
  `<button type="button" class="prov" data-type=…>` elements into
  `#modalProviders`; step-two label (`p.label + " · 账户名称"`), default
  account name (`p.defaultName`), and login dispatch (`window[p.login]`)
  are all derived from the registry. `quotaProviderByType` returns null for
  unknown types and the UI surfaces an explicit `未知服务商` error — no
  silent fallback to Kimi. `.prov-d` subtitles and the four description
  strings were removed entirely; `.prov` centers content and adds
  `:focus-visible`. A future provider is one data entry — no new option
  layout, step-two branch, or dispatch code.

  Post-review WARNING fixes (also in this change):
  - Error/action slot now renders `<button type="button" class=… data-action="relogin" data-name=…>` instead of a non-focusable `<span>` (keyboard accessibility).
  - Row renderer hardened against null/NaN/Infinity view models (see 2.2).
  - Refresh-replacement behavior locked by `TestRefreshReplacementUpdatesCardData`.

Verification:

- Focused: all section-1 RED tests GREEN against the real renderer (node
  harness, distinct synthetic values), including hostile-text escaping,
  errors, optional rows/details, Kimi distinct labels, refresh replacement,
  and reset-duration boundaries; `go test ./internal/web/` green.
- `go test ./...` and `go test -race -tags nogui ./...` pass; touched Go
  files `gofmt`-clean; `go vet` (default AND `-tags nogui`) clean;
  GUI/nogui `go build` clean.
- OpenSpec: `openspec validate --changes` and `--specs` and `--all` strict —
  change and both specs valid.
- 3.4 visual: canonical mixed-provider page served from an isolated harness
  (stubbed fetch with OpenCode Go, Ollama, Kimi, DeepSeek DTOs) and
  inspected in Chrome DevTools (MCP) at wide 1280px, narrow ~500px, and
  mobile 360px. Confirmed:
  - OpenCode/Ollama/Kimi cards share uniform geometry (330px cells at wide;
    337px at 360px; no per-card or page horizontal overflow).
  - Optional Monthly correctly omitted for a two-row provider; errors render
    only on the affected card; re-login affordance is a real `<button>`.
  - Kimi percentages preserved exactly (`28.21%`, `74.86%`, `15.72%`);
    resets render as compact relative durations (`5h`, `2d`, `23d`, `1h`,
    `1d`, `30d`); `Kimi 0.03%` / `Code 15.69%` breakdown below Monthly.
  - Add Account dialog: four registry-rendered options are
    keyboard-focusable `<button>` elements (tabIndex 0), each showing only
    the centered provider name, uniform 232x38 at 360px, no `.prov-d`/empty
    subtitle nodes, no modal/page overflow; selecting Kimi advances to
    step two with label `Kimi Code · 账户名称` and default name `Kimi`;
    `quotaProviderByType('nonexistent')` returns null (explicit error path,
    no Kimi fallback).
  - No console errors (only the expected 404 for the optional
    UbuntuMono font not copied into the harness).
- 3.5 scope: GitNexus `detect_changes` on the working tree →
  changed_symbols: [`TestSidebarRendersKimiCardsAndAddon` (test, touched)],
  affected_processes: [], risk_level: low. Production Go untouched; the
  presentation edits live in the embedded static asset (not a
  GitNexus-indexed symbol surface), matching the planned
  sidebar-template/provider-adapter/tests-only scope. (Note: the new
  `internal/web/quota_card_test.go` and the change directory are untracked
  at the time of the run, so `detect_changes` sees only the 3 tracked
  modified files; scope remains contained.)

## Requirement revision (reviewer, post-implementation) — data-driven selector + a11y/safety fixes

A reviewer flagged that task 2.8's data-driven requirement was not fully
met (options were name-only but still four hand-written HTML blocks with an
independent if/else step-two chain, and an unknown provider type silently
fell back to Kimi), plus three warnings (non-focusable `<span>` re-login
action, unsafe row renderer for null/NaN/Infinity, no direct refresh-
replacement test) and an evidence/git-state note. All were fixed as recorded
above; this evidence supersedes the pre-fix state. The change remains
un-archived pending the reviewer's re-review.

## Requirement revision (reviewer, second pass) — registry-driven delete flow + shared action styling

The second review found a CRITICAL: the delete flow was not registry-driven.
The confirm text hard-coded `opencode/deepseek/→Ollama` with no Kimi case
(deleting a Kimi account asked to delete an "Ollama 账户「Kimi A」"), and the
post-delete refresh hard-coded `fq(); fo(); fd();` — no `fk()`, so a deleted
Kimi card lingered until the next 30s poll. It also flagged that the shared
error action was a semantic `<button>` but only `.kimiLogin` had themed
styles, leaving Ollama's "重新登录" as a browser-native gray button.

Fixes (this pass):

- `quotaProviders` entries now carry a `refresh` handler
  (`opencode→fq`, `deepseek→fd`, `ollama→fo`, `kimi→fk`).
- Delete confirm label comes from `quotaProviderByType(cur.prov).label`, so
  Kimi renders `确定删除 Kimi Code 账户「…」` — no hard-coded provider chain,
  no Ollama fallback; unknown types fall back to the raw type string.
- Post-delete refresh targets ONLY the deleted provider's container using
  the provider snapshot taken at confirm time
  (`deletedProvider`/`deletedName` captured before the fetch), so deleting
  a Kimi account fires `fk()` immediately without redundant network
  requests to unrelated providers — and the refresh stays on the right
  provider even if the user opens a new delete confirm while the request is
  in flight.
- Shared `.qact` action class owns the visual contract (idle/hover/active/
  `:focus-visible`) for ALL provider error actions; `.kimiLogin`/`.olLogin`
  remain as delegation hooks only. `renderQuotaCard` emits
  `class="qact <declared>"`.
- Login dispatch guards `typeof window[p.login] === "function"` with an
  explicit error before calling (no raw `window[p.login](nm)` throw).

New/changed tests: `delete_flow_test.go` — `TestKimiRefreshAndDeleteWiringIntact`
(moved from kimi_card_test.go, expanded: registry confirm label + `p.refresh`
wiring + `refresh: "fk"`) and `TestDeleteFlowRefreshesOnlyDeletedProvider`
(executes the real script block with a stubbed DOM, drives a Kimi account
through context-menu → delete-confirm → confirmOk, asserts the confirm text
names Kimi Code, the delete URL is `/api/delete?provider=kimi&name=…`, the
post-delete refresh calls ONLY `/api/kimi` (no unrelated provider fetches),
and the refresh honors the confirm-time snapshot: after confirming the Kimi
delete the harness opens a new delete confirm for Ollama before running the
refresh timer, yet the refresh still targets `/api/kimi`).
`TestSharedFoundationErrorActionIsKeyboardAccessible` updated
for `class="qact kimiLogin"`; new `TestSharedQactActionStylingExists` locks the
shared hover/active/focus-visible contract.

Gates re-run after this pass: gofmt clean, `go test ./...`, `go test -race
-tags nogui ./...`, default/nogui `go vet`, GUI/nogui builds,
`git diff --check`, `openspec validate --all` — all green. Browser check of
the delete flow: deleting the Kimi account showed confirm text
`确定删除 Kimi Code 账户「kimi-main」？此操作不可撤销。`, then
`/api/delete?provider=kimi&name=kimi-main` followed by exactly one refresh
call to `/api/kimi` (no unrelated provider fetches). The change remains
un-archived pending the reviewer's re-review.

## Follow-up (not blocking; explicitly out of scope)

The existing `/api/delete` frontend still does not parse `{success:false}`:
on a failed delete the confirm modal closes with no error surfaced. The
design lists this as a pre-existing non-goal of this change; it should be
addressed in a separate change.
