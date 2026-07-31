## Why

The Kimi sidebar card currently stretches differently from the existing OpenCode Go cards, uses provider-specific metric labels that do not match the shared card vocabulary, and can render the Monthly progress bar as effectively empty even when total usage is non-zero. The card also contains an unwanted `购买加油包` affordance that should not be part of the compact quota summary.

## What Changes

- Make each Kimi card use the same responsive sizing and wrapping behavior as an OpenCode Go account card, including multi-card layouts and narrow viewports.
- Present the Kimi metrics as `Rolling`, `Weekly`, and `Monthly`, mapped respectively to 5-hour Code usage, 7-day Code usage, and total monthly usage.
- Show the separate decimal Kimi and Code contributions as secondary text directly below `Monthly`, for example `Kimi 0.03%   Code 11.89%`.
- Render the Monthly progress fill from the real total percentage (`total.total_percent`) as one continuous bar; the Kimi/Code values remain an informational breakdown and MUST NOT be used as vertically stacked progress fills.
- Remove `购买加油包` from Kimi cards entirely, including its card-specific event wiring and styles.
- Preserve existing decimal precision, reset displays, loading, refreshing, expired-session, error, and manual refresh behavior.

## Capabilities

### New Capabilities

- `kimi-card-presentation`: Responsive Kimi sidebar-card layout, shared Rolling/Weekly/Monthly labels, a truthful Monthly total progress bar, a monthly Kimi/Code text breakdown, and removal of the booster affordance from the card.

### Modified Capabilities

None. The underlying `kimi-code-provider` capability remains in its existing active change and is not synced or archived by this proposal.

## Impact

- Kimi-specific sidebar card markup, styles, and obsolete booster-action wiring in `internal/sidebar`.
- Sidebar rendering tests covering responsive card layout, exact label/value mapping, truthful Monthly fill width, decimal breakdown formatting, absence of `购买加油包`, and loading/error states.
- Quota DTOs, parsing, refresh, authentication storage, the generic account-page/context-menu flow, and non-Kimi cards are unchanged.
