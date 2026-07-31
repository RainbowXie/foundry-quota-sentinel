## Context

The sidebar builds account cards from embedded HTML, CSS, and JavaScript in `internal/web/static/sidebar.html`. OpenCode Go, Ollama, and DeepSeek containers share a responsive grid rule, but `#kimiCards` is omitted, so every Kimi card currently consumes a full row. The Kimi Monthly track currently receives separate Kimi and Code fill elements inside a fixed-height overflow-hidden container; because those block elements stack vertically, the Code contribution can be clipped and a non-zero total can look empty. The renderer also includes a `购买加油包` card affordance that is no longer wanted.

The underlying Kimi DTO is already grouped as total, five-hour, and seven-day data with decimal formatting and reset displays. This change is presentation-only: it must not alter parsing, refresh, credential persistence, API response shapes, authenticated page replay, or the completed but still-active `add-kimi-code-provider` change.

## Goals / Non-Goals

**Goals:**

- Give `#kimiCards` the same responsive grid contract as the OpenCode Go card container.
- Render 5-hour, 7-day, and total data as `Rolling`, `Weekly`, and `Monthly` in that order.
- Render one Monthly fill from the real total percentage and place the separate Kimi/Code decimal values immediately below it as text.
- Remove `购买加油包` and its Kimi-card-specific event/style wiring.
- Lock the mapping, progress width, absence of the booster affordance, and responsive behavior down with focused sidebar tests.

**Non-Goals:**

- Changing quota calculations, reset semantics, decimal precision, API DTOs, or authentication behavior.
- Generalizing every provider renderer or redesigning the full sidebar.
- Removing or changing the generic context-menu/account-page action available outside the Kimi card.
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

The Monthly primary percentage remains `total.total_percent`; its existing reset remains the Monthly reset. Its track contains one normal `.qf`-style fill with `width: total.total_percent%`, matching how other rows visualize their displayed percentage. The Kimi and Code contributor values are not additional fill elements: they appear only in a dedicated secondary text row using the existing decimal formatter. This avoids the current fixed-height/overflow clipping failure and makes a total such as 11.92% visibly occupy 11.92% even when Kimi alone is only 0.03%.

Changing the API field names to rolling/weekly/monthly was considered and rejected because the source windows are provider-specific, the existing names are semantically precise, and an API migration would add unnecessary risk to a visual fix.

### Remove the booster affordance from the card

Remove the `购买加油包` markup from both successful and error-state Kimi cards, delete any presentation class that exists only for that affordance, and remove the `kimiAddon` branch from the `#kimiCards` delegated click handler. Do not replace it with another purchase label or direct membership link.

The generic card context-menu/account-page action remains available and unchanged; it is outside the unwanted compact-card affordance. Keeping a hidden or icon-only replacement was considered and rejected because the requirement is to remove the button, not merely restyle it.

### Test rendered contracts rather than only token presence

Update sidebar tests to assert that Kimi participates in the shared grid selector, that old labels are absent from the Kimi renderer, that exact field-to-label mappings and Monthly breakdown are present, and that the Monthly track has one fill driven by `total.total_percent`. Assert that neither `购买加油包` nor `kimiAddon` is rendered or wired by the Kimi card. Retain refresh/error rendering coverage.

Substring-only assertions are insufficient for the value mapping, so focused tests should render distinct synthetic values for every field and inspect the resulting labels, order, text values, fill count, and fill width. Use a case such as total 11.92, Kimi 0.03, and Code 11.89 to expose the previous nearly-empty bar.

## Risks / Trade-offs

- **[A 320px minimum column can overflow an unusually narrow host window]** → Reuse the already accepted OpenCode Go grid behavior exactly and verify the narrowest supported sidebar viewport.
- **[Metric labels become less source-specific]** → Lock the mapping in the spec and tests: 5-hour Code is Rolling, 7-day Code is Weekly, and grouped total is Monthly.
- **[Monthly text and bar disagree]** → Derive both the displayed primary percentage and the single fill width from `total.total_percent`, and test them with distinct contributor values.
- **[Removing Kimi action wiring accidentally affects generic opening]** → Delete only the `kimiAddon` card branch and retain tests for the independent generic `openPage` path.
- **[Presentation edits regress loading/error states]** → Preserve the existing success/error branches and add explicit state assertions alongside the success-layout tests.

## Migration Plan

1. Add RED tests for grid parity, exact metric order/mapping, a single truthful Monthly fill, Monthly breakdown, absence of the booster affordance, and preserved error behavior.
2. Update the shared container selectors and Kimi-only presentation classes.
3. Recompose `kcard` from the existing DTO, render Monthly with one total-derived fill, and remove the booster markup and event branch.
4. Run sidebar/web tests, the full default and `-race -tags nogui` suites, formatting, vet, and strict OpenSpec validation.
5. Verify the canonical UI at wide and narrow widths, including a visibly correct Monthly total and the complete absence of `购买加油包`.

Rollback reverts only the Kimi markup/style/test changes; no stored account or API migration is required.

## Open Questions

None. The requested labels, field mapping, total-derived Monthly fill, secondary text, and removal of the booster affordance are explicit.
