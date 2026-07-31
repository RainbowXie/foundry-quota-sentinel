## ADDED Requirements

### Requirement: Kimi cards follow the shared responsive account-card layout
The sidebar SHALL lay out Kimi account cards with the same grid sizing, wrapping, gap, and per-card margin behavior as OpenCode Go account cards. A Kimi card MUST occupy one responsive grid cell rather than an unconstrained full-width row.

#### Scenario: Kimi and OpenCode cards render in a wide sidebar
- **WHEN** the sidebar has enough width for multiple account-card columns
- **THEN** Kimi cards use the same minimum column width and automatic column filling as OpenCode Go cards

#### Scenario: The sidebar narrows
- **WHEN** the available sidebar width falls below the multi-column threshold
- **THEN** Kimi cards wrap and shrink according to the same responsive grid behavior as OpenCode Go cards without horizontal overflow or clipped content

#### Scenario: Multiple Kimi accounts render
- **WHEN** two or more Kimi accounts are displayed
- **THEN** each account occupies an independent grid cell with the same gap and outer margin behavior as the other provider cards

### Requirement: Kimi metrics use shared period labels with exact semantics
Each successful Kimi card SHALL present exactly three primary quota rows in this order: `Rolling`, `Weekly`, and `Monthly`. `Rolling` SHALL display the 5-hour Code percentage and reset, `Weekly` SHALL display the 7-day Code percentage and reset, and `Monthly` SHALL display the total percentage and monthly reset. Decimal percentage formatting SHALL preserve up to two decimal places and trim only unnecessary trailing zeros.

#### Scenario: A successful grouped quota is rendered
- **WHEN** Kimi quota data contains 5-hour Code, 7-day Code, and total monthly usage
- **THEN** the card maps them respectively to `Rolling`, `Weekly`, and `Monthly` without swapping or reusing values

#### Scenario: Decimal values are displayed
- **WHEN** a metric value is `0`, `7.9`, or `11.89`
- **THEN** it is rendered respectively as `0%`, `7.9%`, or `11.89%` without integer rounding

#### Scenario: Monthly usage has separate contributors
- **WHEN** the monthly total contains Kimi usage `0.03` and Code usage `11.89`
- **THEN** the card renders secondary text directly below the `Monthly` row as `Kimi 0.03%` and `Code 11.89%`, while the primary `Monthly` row retains the total percentage and reset

### Requirement: The booster affordance opens the authenticated membership page
Each successful Kimi card SHALL expose `购买加油包` as a semantic interactive control associated with that card's account. Activating it by pointer or keyboard SHALL invoke the existing authenticated Kimi page-opening flow for the named account and target exactly `https://www.kimi.com/membership/subscription?tab=quota`. The application MUST NOT automate a purchase, checkout, subscription, booster, or payment action.

#### Scenario: Activate the booster control with a pointer
- **WHEN** the user clicks `购买加油包` on a named Kimi card
- **THEN** the sidebar invokes the authenticated Kimi open-page action for that account and surfaces any existing open-page error state

#### Scenario: Activate the booster control with a keyboard
- **WHEN** the control has keyboard focus and the user activates it with the platform-standard button or link key
- **THEN** the same authenticated Kimi open-page action runs for that account

#### Scenario: Navigate to the membership page
- **WHEN** the open-page action succeeds
- **THEN** the temporary authenticated browser opens the exact Kimi membership quota URL and leaves all purchase decisions to direct user interaction in that browser

#### Scenario: Inspect the booster control visually
- **WHEN** the control is idle, hovered, or keyboard-focused
- **THEN** it has a visible interactive affordance and a visible focus indicator without relying only on inline cursor styling

### Requirement: Existing Kimi card states remain intact
The presentation change SHALL preserve per-account loading, refreshing, success, expired-session, error, re-login, refresh, and delete behavior. Failed or incomplete quota data MUST NOT be presented as successful Rolling, Weekly, or Monthly values.

#### Scenario: Kimi quota retrieval fails
- **WHEN** a Kimi account returns an error or requires re-login
- **THEN** only that card shows its existing actionable error state and no successful metric rows or booster action are fabricated

#### Scenario: Kimi quota refresh succeeds
- **WHEN** a manual or automatic refresh replaces a card's quota data
- **THEN** the responsive card updates all three rows and the monthly Kimi/Code breakdown without losing its account identity or interaction wiring
