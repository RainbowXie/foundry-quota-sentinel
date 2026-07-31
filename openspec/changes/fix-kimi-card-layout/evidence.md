# Evidence — fix-kimi-card-layout

Historical implementation evidence for the first revision of the Kimi
sidebar-card presentation change. The requirements were revised afterward;
the completed evidence below does not establish acceptance of the current
change.

## Current requirement revision — pending implementation

The current contract supersedes the earlier booster-button work:

- `购买加油包` must be removed from Kimi cards, including `kimiAddon`
  markup, card-specific styles, and delegated event wiring. The generic
  context-menu/account-page action remains outside this scope.
- Monthly must contain one visible fill whose width is derived from
  `total.total_percent`. For total `11.92`, Kimi `0.03`, and Code `11.89`,
  the bar must visibly fill `11.92%`; Kimi/Code remain text-only breakdown
  values.

Tasks 1.5, 1.6, 2.4, 2.5, and 3.2–3.5 in the revised `tasks.md` are open.
No GREEN implementation or visual acceptance evidence for this revision
has been recorded yet.

## Superseded first-revision evidence

## Task 1.1 — Impact analysis (GitNexus unavailable → manual)

GitNexus MCP was non-functional during this change (known outage, per
project convention established in `add-kimi-code-provider`); manual impact
analysis was performed instead. All edited symbols live inside the embedded
`internal/web/static/sidebar.html`:

| Symbol | Callers | Blast radius |
|--------|---------|--------------|
| `kcard(a)` (JS) | `fk()` only (Kimi fetch/render loop) | Kimi card rendering |
| `ktotal(t)` (JS) | `kcard()` only | Kimi card Monthly row |
| `krow(label,u,cl)` (JS) | `kcard()` only | Kimi card metric rows |
| `kpct(p)` (JS) | `krow`/`ktotal` only | Kimi decimal formatting (unchanged) |
| shared grid CSS selectors | `#accountCards`/`#ollamaCards`/`#dsCards` containers | adding `#kimiCards` to the EXISTING rule does not alter other providers' computed styles |
| `.kimiAddon` element | `kimiCards` delegated click listener → `openPage("kimi", name)` | booster activation path (unchanged endpoint) |
| error-branch re-login element | `kimiCards` delegated listener matches `.kimiLogin` | see finding below |

NOT touched: `qrow` (OpenCode Go rows), `olcard`/`dscard`/`acard`, server
handlers (`/api/kimi*`, `/api/open`, `/api/delete`), quota DTOs, parsing,
refresh, credential storage, or the archived `add-kimi-code-provider`
change. Risk: LOW (presentation-only; no shared renderer symbols).

### Pre-existing finding (fixed under 2.5)

The Kimi error-branch re-login affordance was rendered with class
`olLogin`, but the `#kimiCards` delegated listener matches `kimiLogin` —
and no element anywhere carried `kimiLogin`. The affordance was therefore
a DEAD control (clicking 重新登录 on a failed Kimi card did nothing).
Fixed by rendering the affordance with class `kimiLogin`, which activates
the intended existing delegation → `kimiDoLogin` evidence-gated flow.
Locked by `TestKimiCardErrorStateFabricatesNoMetrics`.

## RED evidence (all against the pre-change renderer)

Executed via `go test ./internal/web/ -run 'TestKimiCard|TestKimiBooster|TestKimiCardsShare|TestKimiRefresh'`:

- `TestKimiCardsShareResponsiveGrid` — FAIL: "#kimiCards is not part of
  the shared responsive grid selector group".
- `TestKimiCardRendersRollingWeeklyMonthlyMapping` — FAIL: "successful
  Kimi card must render Rolling/Weekly/Monthly rows" (node harness
  executed the real `kcard`; output contained the old Chinese labels).
- `TestKimiCardRendersMonthlyKimiCodeBreakdown` — FAIL: breakdown values
  not rendered in the required `Kimi <v>%`/`Code <v>%` form.
- `TestKimiBoosterIsSemanticKeyboardOperableControl` — FAIL: booster was
  an inline-styled `<span>`, no `<button>` found.
- `TestKimiCardErrorStateFabricatesNoMetrics` — FAIL: "error card must
  keep a working kimiLogin re-login affordance" (pre-existing dead
  `olLogin` control; no fabricated metrics detected, as required).
- `TestKimiRefreshAndDeleteWiringIntact` — PASS (retained behavior,
  per task 1.6 "add or retain").

The node renderer-execution layer evals the exact inline `<script>` block
shipped to the browser (stubbed DOM via a universal absorbing proxy;
never-settling fetch) and renders distinct synthetic DTO values, so a
swapped or reused field mapping cannot pass undetected.

## GREEN + verification (sections 2-3)

Implementation (`internal/web/static/sidebar.html` only; zero production Go
changes):

- `#kimiCards` added to BOTH shared selector groups (container grid rule +
  direct-child margin rule) — no Kimi-only width contract.
- `kcard` success branch recomposed: `krow("Rolling", d.five_hour)` →
  `krow("Weekly", d.seven_day)` → `ktotal(d.total)`; error branch keeps
  `qerr` + a now-WORKING `button.kimiLogin` re-login affordance (dead
  `olLogin` class fixed; kimiCards delegation matches `kimiLogin` →
  `kimiDoLogin`).
- `ktotal` renders the Monthly row (split bar + monthly reset preserved)
  followed immediately by `<div class="qr kbr">` with `Kimi <v>%` /
  `Code <v>%` via the existing bounded `kpct` decimal formatter.
- 购买加油包 is now `<button type="button" class=kimiAddon data-name=…>`;
  themed `.kimiAddon` base/hover/active/`:focus-visible` rules replace the
  inline cursor styling; delegation `kimiAddon → openPage("kimi", name)`
  unchanged (no purchase automation, no external link).
- Add-account modal Kimi description aligned to the shared vocabulary
  (`Rolling / Weekly / Monthly`) so no stale Chinese metric labels remain.
- Legacy `TestSidebarRendersKimiCardsAndAddon` updated (it codified the old
  labels); wiring assertions retained, stale-label assertions inverted.

Verification:

- Focused: all 6 section-1 tests GREEN against the real renderer (node
  harness, distinct synthetic values); full `internal/web` suite green.
- `go test ./...` + `go test -race -tags nogui ./...` pass; `gofmt` clean;
  `go vet` (default AND `-tags nogui`) clean; `openspec validate --strict`
  valid.
- 3.3 visual: canonical binary served on an isolated port (8799; the user's
  own GUI instance holds 8788 and was NOT touched) and inspected headlessly
  (isolated Edge profile) at 900px and 340px. Wide: Kimi card occupies one
  grid cell with the SAME width/sizing as the OpenCode Go card; Rolling
  46.21% / Weekly 62.35% / Monthly 13.1% with own resets, breakdown
  `Kimi 0.02%  Code 13.08%` directly below Monthly, booster bottom-right.
  Narrow: single-column wrap, no horizontal overflow, no clipped label,
  reset, percentage, or action. Screenshots (contain the real account name)
  were inspected and shredded, not committed.
- 3.5 scope: GitNexus `detect_changes` on the working tree →
  changed_symbols: [`TestSidebarRendersKimiCardsAndAddon` (test, touched)],
  affected_processes: [], risk_level: low. Production Go untouched; the
  presentation edits live in the embedded static asset (not a
  GitNexus-indexed symbol surface), matching the planned
  Kimi/sidebar-presentation-only scope.

The former Task 3.4 (interactive pointer + keyboard booster activation) was
removed when the user withdrew the booster affordance. It is not part of
the current acceptance criteria.

## Requirement revision (user, mid-implementation) — booster removal + truthful Monthly fill

After round 1 landed, the user revised the change: REMOVE the booster
affordance entirely (markup, kimiAddon CSS, delegated branch; generic
context-menu openPage flow stays), and fix a newly-visible Monthly bar
defect — the split Kimi+Code fills are block elements that stack
VERTICALLY inside the 6px overflow track, so the Code fill was clipped
and a non-zero total (13.1%) rendered as an effectively empty bar
(observed in the round-1 wide screenshot).

Also fixed during round-1 interactive testing (retained): `openPage` was
declared inside the ctx-menu IIFE while the `#kimiCards` delegated listener
is top-level — booster clicks died with a ReferenceError ("no reaction").
Hoisted `openPage`/`openPageError` to top level; locked by
`TestOpenPageReachableFromGlobalScope` (generic-path guard).

RED→GREEN (revised):
- `TestKimiCardMonthlySingleTruthfulFill` — RED (Monthly had 2 fills,
  none at 11.92%); GREEN: exactly one `.qf` fill at
  `width:11.92%` from `total.total_percent`; contributor values never
  rendered as fills; zero total → `width:0%` with the row present.
- `TestKimiCardOmitsBoosterAffordance` — RED (button rendered); GREEN:
  neither success nor error cards render 购买加油包/kimiAddon; zero
  kimiAddon refs in sidebar.html; ctxOpen → openPage(cur.prov, cur.name)
  generic wiring asserted intact; kimiLogin re-login retained.
- Legacy `TestSidebarRendersKimiCardsAndAddon` updated to assert booster
  absence + shared vocabulary.

Verification (revised): focused + full suites, `-race -tags nogui`,
`go vet` (default + nogui), `gofmt`, openspec strict — all green.
Visual (headless, isolated profile, real config read-only): wide 900px
and narrow 340px both show the Monthly fill VISIBLY occupying ~14.5%
(previously visually empty), breakdown `Kimi 0.02%  Code 14.48%` below
Monthly, no booster, grid parity with the OpenCode card, no clipping.
Screenshots inspected and shredded. `gitnexus_detect_changes`: 1 test
symbol touched, 0 affected processes, risk low.
