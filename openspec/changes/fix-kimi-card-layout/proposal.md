## Why

The Kimi sidebar card currently stretches differently from the existing OpenCode Go cards, uses provider-specific metric labels that do not match the shared card vocabulary, and presents `购买加油包` as non-interactive text. This makes the card visually inconsistent and prevents users from reaching the already-supported authenticated Kimi quota page from the purchase affordance.

## What Changes

- Make each Kimi card use the same responsive sizing and wrapping behavior as an OpenCode Go account card, including multi-card layouts and narrow viewports.
- Present the Kimi metrics as `Rolling`, `Weekly`, and `Monthly`, mapped respectively to 5-hour Code usage, 7-day Code usage, and total monthly usage.
- Show the separate decimal Kimi and Code contributions as secondary text directly below `Monthly`, for example `Kimi 0.03%   Code 11.89%`.
- Turn `购买加油包` into a semantic, keyboard-accessible action with visible hover and focus states that opens the named account's authenticated Kimi membership quota page.
- Preserve existing decimal precision, reset displays, loading, refreshing, expired-session, error, and manual refresh behavior.
- Keep purchase decisions entirely user-controlled: the application opens `https://www.kimi.com/membership/subscription?tab=quota` but never clicks or submits purchase, checkout, subscription, or payment controls.

## Capabilities

### New Capabilities

- `kimi-card-presentation`: Responsive Kimi sidebar-card layout, shared Rolling/Weekly/Monthly labels, monthly Kimi/Code breakdown, and an accessible authenticated membership-page action.

### Modified Capabilities

None. The underlying `kimi-code-provider` capability remains in its existing active change and is not synced or archived by this proposal.

## Impact

- Kimi-specific sidebar card markup, styles, and client-side account-page action wiring in `internal/sidebar`.
- Sidebar rendering and interaction tests covering responsive card layout, exact label/value mapping, decimal breakdown formatting, keyboard activation, and loading/error states.
- Existing authenticated Kimi open-page route and membership-page lifecycle are reused; quota DTOs, parsing, refresh, authentication storage, and non-Kimi cards are unchanged.
