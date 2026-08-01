## 1. Impact and RED Characterization

- [x] 1.1 Before editing each renderer/helper symbol, run GitNexus upstream impact analysis for the current shared and provider-specific card paths (`qrow`, `acard`, `olcard`, `kcard`, `krow`, `ktotal`, refresh/event callers); report any HIGH/CRITICAL result, and if embedded JavaScript is not indexed record the failed lookup plus a manual caller/flow map before proceeding.
- [x] 1.2 Add RED characterization tests that execute the existing OpenCode Go, Ollama, and Kimi renderers with distinct fixtures and capture shared structure plus intentional differences: metric order, optional Monthly behavior, percentages, resets, Kimi/Code details, account identity, errors, re-login actions, refresh replacement, deletion wiring, and escaping.
- [x] 1.3 Add RED formatter tests for negative/zero values and the `s`/`m`/`h`/`d` boundaries, including 18,000 → `5h`, 172,800 → `2d`, and 1,987,200 → `23d`.
- [x] 1.4 Add a RED Kimi test proving `28.21%`, `74.86%`, and `15.72%` remain unchanged while absolute reset strings are replaced by compact durations from each metric's `reset_in_sec`.
- [x] 1.5 Add RED structural/DOM tests proving allowance providers share shell/standard-row primitives and common design tokens, provider adapters return normalized data plus typed extensions rather than duplicate common HTML, Kimi details use a row slot, and a synthetic future adapter can add unique extension content without a new shell/meter renderer.
- [x] 1.6 Add RED Add Account tests proving all four provider options show only centered provider names, contain no `.prov-d`/empty subtitle nodes or obsolete descriptions, and retain exact `data-type` → step-two/login mappings.

## 2. Shared Quota-Card Template

- [x] 2.1 Implement one pure sidebar compact-duration formatter matching Go `FormatDurationCompact`, flooring the largest applicable whole unit and clamping invalid or expired values to a safe non-negative display.
- [x] 2.2 Implement the shared quota-card foundation and quota-row view model/renderers with common shell/header, ordered/optional rows, progress fill, declarative percentage precision, relative reset column, typed header/body/row-detail/footer extension slots, escaped text, error region, and declarative action slot.
- [x] 2.3 Add an OpenCode Go adapter to the shared view model and remove its duplicated card/row composition without changing values, optional Monthly semantics, refresh, errors, or actions.
- [x] 2.4 Add an Ollama adapter to the shared view model and remove its duplicated card/row composition without changing Session/Weekly semantics, errors, re-login, refresh, or actions.
- [x] 2.5 Add a Kimi adapter mapping five-hour → Rolling, seven-day → Weekly, and total → Monthly; use existing `reset_in_sec`, preserve up-to-two-decimal percentages and the total-derived Monthly fill, and attach the provider-specific labeled Kimi/Code block through the typed Monthly detail slot.
- [x] 2.6 Remove obsolete duplicated provider shell/standard-row markup and layout/style rules while retaining provider-specific extension content/styles and the specialized DeepSeek chart/balance renderer.
- [x] 2.7 Keep account-level event delegation and extension dispatch data-driven; verify shared shell/row renderers contain no provider-name branches, shared actions never expose credentials, and provider adapters may declare only their own extension content.
- [x] 2.8 Replace the four subtitle-bearing Add Account options with one uniform centered name-only option primitive/data definition; remove obsolete subtitle CSS/content while preserving modal step-two labels, default account names, close behavior, and provider login dispatch.

## 3. Verification and Acceptance

- [x] 3.1 Run focused foundation/adapter/extension tests and confirm every RED test from section 1 is GREEN, including hostile text escaping, errors, optional rows/details, Kimi's distinct labels, refresh replacement, and reset-duration boundaries.
- [x] 3.2 Run existing provider sidebar/API regression tests and prove quota parsers, DTO JSON, CLI output, authentication, refresh persistence, deletion, and account-page routing remain unchanged.
- [x] 3.3 Run `gofmt` on touched Go files, `go test ./...`, `go test -race -tags nogui ./...`, default/nogui `go vet`, GUI/nogui builds, and strict OpenSpec change/spec validation.
- [x] 3.4 Inspect canonical mixed-provider cards and the Add Account dialog at wide and narrow supported widths; confirm shared common geometry/alignment, intentional provider-specific card extensions, optional rows/details, no clipping/overflow, Kimi relative resets with unchanged percentages/labels, and four uniformly centered name-only provider choices.
- [x] 3.5 Before committing, run GitNexus `detect_changes` against the implementation baseline and confirm affected symbols/flows are limited to the planned sidebar template, provider adapters, and tests; investigate and document any wider impact.
