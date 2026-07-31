## 1. Scope and RED Coverage

- [x] 1.1 Run GitNexus impact analysis for every renderer, handler, or shared sidebar symbol that will be edited; record direct callers, affected flows, and any HIGH/CRITICAL warning before implementation.
- [x] 1.2 Add a failing sidebar layout test proving `#kimiCards` is governed by the same responsive grid and direct-child margin selectors as OpenCode Go cards.
- [x] 1.3 Add a failing distinct-value rendering test proving the primary row order and mapping: 5-hour Code → `Rolling`, 7-day Code → `Weekly`, and total usage/reset → `Monthly`.
- [x] 1.4 Add a failing rendering test proving the secondary Monthly line preserves separate decimal values in `Kimi <value>%` and `Code <value>%` form and trims only unnecessary trailing zeros.
- [x] 1.5 Add failing interaction tests proving `购买加油包` is a semantic keyboard-operable control for the named account, has hover/focus styling, and routes through the existing checked `openPage("kimi", name)` error-aware path.
- [x] 1.6 Add or retain failing state tests proving loading, refreshing, failed/re-login, manual refresh, and delete behavior are not replaced by fabricated successful metrics or purchase actions.

## 2. Responsive Kimi Card Presentation

- [x] 2.1 Extend the established shared card-container and direct-child selectors to `#kimiCards` without introducing a separate Kimi width contract.
- [x] 2.2 Recompose the successful Kimi renderer into `Rolling`, `Weekly`, and `Monthly` rows using the existing five-hour, seven-day, and total DTO fields and reset displays.
- [x] 2.3 Render the Kimi/Code decimal breakdown immediately below `Monthly` using dedicated presentation classes and the existing bounded percentage formatter.
- [x] 2.4 Replace the inline-styled `购买加油包` span with a `type="button"` semantic control and themed idle, hover, active, and `:focus-visible` styles.
- [x] 2.5 Keep delegated activation account-scoped and reuse `/api/open?provider=kimi&name=...`; surface failures through the existing open-page error handling and do not add purchase automation or an external unauthenticated link.

## 3. Regression and Visual Verification

- [x] 3.1 Run the focused sidebar/web tests and confirm every RED test from section 1 is GREEN with distinct synthetic metric values.
- [x] 3.2 Run `gofmt` for touched Go files, `go test ./...`, `go test -race -tags nogui ./...`, default/nogui `go vet`, and strict OpenSpec validation.
- [x] 3.3 Build and inspect the canonical GUI at wide and narrow supported widths, confirming Kimi and OpenCode Go cards share sizing/wrapping and no label, reset, percentage, or action is clipped.
- [ ] 3.4 Activate `购买加油包` by pointer and keyboard for a disposable named Kimi account; verify it opens exactly the authenticated membership quota page, remains user-controlled, and does not trigger purchase, checkout, subscription, or payment operations.
- [x] 3.5 Run GitNexus `detect_changes` before committing and confirm the affected symbols and execution flows are limited to the planned Kimi/sidebar presentation and tests; investigate and document any wider impact.
