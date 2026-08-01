## MODIFIED Requirements

### Requirement: Kimi metrics use shared period labels with exact semantics
Each successful Kimi card SHALL reuse the shared allowance-card shell and standard meter-row primitive while retaining a Kimi-specific Monthly row-detail extension. It SHALL present exactly three primary quota rows in this order: `Rolling`, `Weekly`, and `Monthly`. `Rolling` SHALL display the 5-hour Code percentage and relative remaining reset duration, `Weekly` SHALL display the 7-day Code percentage and relative remaining reset duration, and `Monthly` SHALL display the real total percentage and relative remaining monthly reset duration. Reset text SHALL be derived from each metric's `reset_in_sec` value through the shared compact formatter rather than using Kimi's absolute `reset_display`. Decimal percentage formatting SHALL preserve up to two decimal places and trim only unnecessary trailing zeros; changing reset presentation MUST NOT round, replace, or otherwise alter any percentage.

#### Scenario: A successful grouped quota is rendered
- **WHEN** Kimi quota data contains 5-hour Code, 7-day Code, and total monthly usage
- **THEN** the shared template maps them respectively to `Rolling`, `Weekly`, and `Monthly` without swapping or reusing percentage or reset values

#### Scenario: Decimal values are displayed
- **WHEN** a Kimi metric value is `0`, `7.9`, `11.89`, or `28.21`
- **THEN** it is rendered respectively as `0%`, `7.9%`, `11.89%`, or `28.21%` without integer rounding or any reset-related transformation

#### Scenario: Relative reset durations replace absolute dates
- **WHEN** Rolling, Weekly, and Monthly have remaining reset durations of 5 hours, 2 days, and 23 days
- **THEN** the card displays `5h`, `2d`, and `23d` instead of absolute forms such as `07-31 16:58`, `08-04 23:58`, or `2026-08-28`

#### Scenario: Monthly usage has separate contributors
- **WHEN** the monthly total contains Kimi usage `0.03` and Code usage `11.89`
- **THEN** Kimi's typed Monthly detail extension renders secondary text directly below the shared `Monthly` meter row as `Kimi 0.03%` and `Code 11.89%`, while the primary row retains the real total percentage, total-derived progress fill, and compact relative reset duration and other providers remain unaffected
