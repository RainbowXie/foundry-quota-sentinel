## 1. Scope and RED Coverage

- [x] 1.1 Run GitNexus impact analysis for every renderer, handler, or shared sidebar symbol that will be edited; record direct callers, affected flows, and any HIGH/CRITICAL warning before implementation.
- [x] 1.2 Add a failing sidebar layout test proving `#kimiCards` is governed by the same responsive grid and direct-child margin selectors as OpenCode Go cards.
- [x] 1.3 Add a failing distinct-value rendering test proving the primary row order and mapping: 5-hour Code → `Rolling`, 7-day Code → `Weekly`, and total usage/reset → `Monthly`.
- [x] 1.4 Add a failing rendering test proving the secondary Monthly line preserves separate decimal values in `Kimi <value>%` and `Code <value>%` form and trims only unnecessary trailing zeros.
- [x] 1.5 Add a failing distinct-value DOM/rendering test proving Monthly has exactly one fill whose width comes from `total.total_percent` (for example 11.92), not separate stacked Kimi/Code fills.
- [x] 1.6 Add a failing test proving successful and error-state Kimi cards contain neither `购买加油包` nor a `kimiAddon` action branch while the unrelated generic account-page action remains intact.
- [x] 1.7 Retain state tests proving loading, refreshing, failed/re-login, manual refresh, and delete behavior are not replaced by fabricated successful metrics.

## 2. Responsive Kimi Card Presentation

- [x] 2.1 Extend the established shared card-container and direct-child selectors to `#kimiCards` without introducing a separate Kimi width contract.
- [x] 2.2 Recompose the successful Kimi renderer into `Rolling`, `Weekly`, and `Monthly` rows using the existing five-hour, seven-day, and total DTO fields and reset displays.
- [x] 2.3 Render the Kimi/Code decimal breakdown immediately below `Monthly` using dedicated presentation classes and the existing bounded percentage formatter.
- [x] 2.4 Replace the Monthly split-fill markup with one standard progress fill driven by `total.total_percent`, keeping Kimi/Code only in the secondary text breakdown.
- [x] 2.5 Remove the `购买加油包` markup, Kimi-card-only styles, and `kimiAddon` delegated click branch without changing the generic context-menu/account-page flow.

## 3. Regression and Visual Verification

- [x] 3.1 Run the original focused sidebar/web tests and confirm the responsive layout, period mapping, and decimal breakdown tests are GREEN with distinct synthetic metric values.
- [x] 3.2 Run the revised focused tests and confirm the single total-derived Monthly fill and complete booster-affordance removal are GREEN.
- [x] 3.3 Run `gofmt` for touched Go files, `go test ./...`, `go test -race -tags nogui ./...`, default/nogui `go vet`, and strict OpenSpec validation after the revised implementation.
- [x] 3.4 Build and inspect the canonical GUI at wide and narrow supported widths, confirming the Monthly fill visibly matches the displayed real total, Kimi/Code text remains correct, and `购买加油包` is absent.
- [x] 3.5 Run GitNexus `detect_changes` before the revised commit and confirm the affected symbols and execution flows are limited to the planned Kimi/sidebar presentation and tests; investigate and document any wider impact.
