# kimi-card-presentation Specification

## Purpose

The Kimi sidebar card presents each account with the shared responsive
account-card layout, the shared Rolling/Weekly/Monthly metric vocabulary, a
truthful Monthly progress fill derived from the real total percentage, and a
secondary textual Kimi/Code breakdown — while omitting any purchase or
booster affordance from the compact card and preserving all existing
loading, error, re-login, refresh, and delete behavior.

## Requirements


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
Each successful Kimi card SHALL present exactly three primary quota rows in this order: `Rolling`, `Weekly`, and `Monthly`. `Rolling` SHALL display the 5-hour Code percentage and reset, `Weekly` SHALL display the 7-day Code percentage and reset, and `Monthly` SHALL display the real total percentage and monthly reset. Decimal percentage formatting SHALL preserve up to two decimal places and trim only unnecessary trailing zeros.

#### Scenario: A successful grouped quota is rendered
- **WHEN** Kimi quota data contains 5-hour Code, 7-day Code, and total monthly usage
- **THEN** the card maps them respectively to `Rolling`, `Weekly`, and `Monthly` without swapping or reusing values

#### Scenario: Decimal values are displayed
- **WHEN** a metric value is `0`, `7.9`, or `11.89`
- **THEN** it is rendered respectively as `0%`, `7.9%`, or `11.89%` without integer rounding

#### Scenario: Monthly usage has separate contributors
- **WHEN** the monthly total contains Kimi usage `0.03` and Code usage `11.89`
- **THEN** the card renders secondary text directly below the `Monthly` row as `Kimi 0.03%` and `Code 11.89%`, while the primary `Monthly` row retains the total percentage and reset

### Requirement: Monthly progress visualizes the real total usage
The `Monthly` progress track SHALL contain one visible progress fill whose width is derived from `total.total_percent`. Separate `total.kimi_percent` and `total.code_percent` values SHALL be rendered only as the secondary textual breakdown and MUST NOT be placed as vertically stacked fills that hide either contribution.

#### Scenario: Total usage is visibly non-zero
- **WHEN** `total.total_percent` is `11.92`, composed of Kimi `0.03` and Code `11.89`
- **THEN** the `Monthly` progress fill visibly occupies `11.92%` of its track and the text breakdown still displays the two contributor values separately

#### Scenario: One contributor is visually tiny
- **WHEN** Kimi usage is `0.03%` and Code usage supplies most of the non-zero total
- **THEN** the Monthly bar reflects the complete total rather than appearing empty or showing only the tiny Kimi contribution

#### Scenario: Total usage is zero
- **WHEN** `total.total_percent` is `0`
- **THEN** the Monthly fill width is `0%` while the row and its correctly formatted value remain present

### Requirement: Kimi cards omit the booster affordance
The Kimi card SHALL NOT render `购买加油包` or any replacement purchase, booster, checkout, subscription, or payment control. Removing the card affordance MUST NOT remove or change unrelated generic account-page actions provided elsewhere in the sidebar.

#### Scenario: A successful Kimi card renders
- **WHEN** valid Kimi quota data is displayed
- **THEN** the card contains the three quota rows and Monthly breakdown but contains no `购买加油包` text or booster-action element

#### Scenario: A Kimi card is in an error state
- **WHEN** quota retrieval fails or the account requires re-login
- **THEN** the card contains its existing actionable error state but no purchase or booster affordance

### Requirement: Existing Kimi card states remain intact
The presentation change SHALL preserve per-account loading, refreshing, success, expired-session, error, re-login, refresh, and delete behavior. Failed or incomplete quota data MUST NOT be presented as successful Rolling, Weekly, or Monthly values.

#### Scenario: Kimi quota retrieval fails
- **WHEN** a Kimi account returns an error or requires re-login
- **THEN** only that card shows its existing actionable error state and no successful metric rows are fabricated

#### Scenario: Kimi quota refresh succeeds
- **WHEN** a manual or automatic refresh replaces a card's quota data
- **THEN** the responsive card updates all three rows, the truthful Monthly fill, and the monthly Kimi/Code breakdown without losing its account identity
