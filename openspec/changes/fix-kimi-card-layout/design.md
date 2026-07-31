## Context

The sidebar builds account cards from embedded HTML, CSS, and JavaScript in `internal/web/static/sidebar.html`. OpenCode Go, Ollama, and DeepSeek containers share a responsive grid rule, but `#kimiCards` is omitted, so every Kimi card currently consumes a full row. The Kimi renderer also uses membership-specific Chinese labels and an inline-styled `<span>` for `购买加油包`, even though the existing delegated action already calls `openPage("kimi", accountName)` and the backend already opens the authenticated membership quota page.

The underlying Kimi DTO is already grouped as total, five-hour, and seven-day data with decimal formatting and reset displays. This change is presentation-only: it must not alter parsing, refresh, credential persistence, API response shapes, authenticated page replay, or the completed but still-active `add-kimi-code-provider` change.

## Goals / Non-Goals

**Goals:**

- Give `#kimiCards` the same responsive grid contract as the OpenCode Go card container.
- Render 5-hour, 7-day, and total data as `Rolling`, `Weekly`, and `Monthly` in that order.
- Keep the Monthly total as the primary row and place its separate Kimi/Code decimal values immediately below it.
- Make `购买加油包` a discoverable, keyboard-operable control that reuses the authenticated Kimi page-opening path.
- Lock the mapping, interaction, and responsive behavior down with focused sidebar tests.

**Non-Goals:**

- Changing quota calculations, reset semantics, decimal precision, API DTOs, or authentication behavior.
- Generalizing every provider renderer or redesigning the full sidebar.
- Clicking any purchase control or automating booster checkout, payment, or subscription changes.
- Archiving, syncing, or otherwise modifying `add-kimi-code-provider`.

## Decisions

### Extend the existing shared grid selector to Kimi

Add `#kimiCards` to the same container and direct-child selectors already used by `#accountCards`, `#ollamaCards`, and `#dsCards`. This gives Kimi the established `repeat(auto-fill, minmax(320px, 1fr))`, gap, and margin behavior without duplicating layout values.

An independent Kimi-only width rule was considered and rejected because it would allow the two layouts to drift again and would not satisfy exact visual parity with OpenCode Go.

### Recompose the existing grouped DTO in the renderer

Keep the server response unchanged and map existing fields at render time:

- `five_hour` → `Rolling`
- `seven_day` → `Weekly`
- `total` → `Monthly`
- `total.kimi_percent` and `total.code_percent` → the secondary Monthly breakdown

The Monthly primary percentage remains `total.total_percent`; its existing reset remains the Monthly reset. The secondary breakdown uses the existing decimal formatter so values retain up to two decimal places. The three primary rows should continue using the established quota-row structure and bar styles, with a dedicated class for the breakdown instead of ad hoc inline layout.

Changing the API field names to rolling/weekly/monthly was considered and rejected because the source windows are provider-specific, the existing names are semantically precise, and an API migration would add unnecessary risk to a visual fix.

### Use a semantic button for the booster action

Render `购买加油包` as `button type="button"` with the existing account name in data attributes. Continue using event delegation to invoke `openPage("kimi", name)`, including its existing JSON response/error handling. Native button semantics provide Enter/Space activation; CSS supplies an explicit pointer, hover state, and `:focus-visible` indicator consistent with the sidebar theme.

An anchor was considered, but the action starts an authenticated application workflow rather than navigating the sidebar document, so a button expresses the behavior more accurately. A direct external link was rejected because it would bypass saved-account session replay. Purchase automation remains prohibited: the handler only opens the membership quota page.

### Test rendered contracts rather than only token presence

Update sidebar tests to assert that Kimi participates in the shared grid selector, that old labels are absent from the Kimi renderer, that exact field-to-label mappings and Monthly breakdown are present, and that the booster element is a semantic control wired to `openPage("kimi", ...)`. Add or extend browser/DOM-oriented coverage where available to exercise click and keyboard activation plus refresh/error rendering.

Substring-only assertions are insufficient for the value mapping, so focused tests should render distinct synthetic values for every field and inspect the resulting labels, order, values, and account action.

## Risks / Trade-offs

- **[A 320px minimum column can overflow an unusually narrow host window]** → Reuse the already accepted OpenCode Go grid behavior exactly and verify the narrowest supported sidebar viewport.
- **[Metric labels become less source-specific]** → Lock the mapping in the spec and tests: 5-hour Code is Rolling, 7-day Code is Weekly, and grouped total is Monthly.
- **[Native button defaults disturb compact styling]** → Reset only the necessary native appearance properties in a dedicated class while retaining semantic focus and activation behavior.
- **[Event delegation reads a nested target]** → Keep the button label un-nested or resolve the closest matching booster control before reading the account name, and cover the rendered interaction in tests.
- **[Presentation edits regress loading/error states]** → Preserve the existing success/error branches and add explicit state assertions alongside the success-layout tests.

## Migration Plan

1. Add RED tests for grid parity, exact metric order/mapping, Monthly breakdown, semantic interaction, and preserved error behavior.
2. Update the shared container selectors and Kimi-only presentation classes.
3. Recompose `kcard` from the existing DTO and replace the non-interactive span with the semantic action.
4. Run sidebar/web tests, the full default and `-race -tags nogui` suites, formatting, vet, and strict OpenSpec validation.
5. Verify the canonical UI at wide and narrow widths, including pointer and keyboard activation of `购买加油包` and open-page failure feedback.

Rollback reverts only the Kimi markup/style/test changes; no stored account or API migration is required.

## Open Questions

None. The requested labels, field mapping, secondary text, target URL, and purchase-safety boundary are explicit.
