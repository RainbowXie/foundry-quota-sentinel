## Why

Kimi Code users currently cannot save an account or monitor the complete quota summary shown at `https://www.kimi.com/membership/subscription?tab=quota` from Foundry Quota Sentinel. That page contains three quota groups with four percentage values: total usage has separate `Kimi` and `Code` values, followed by 5-hour Code usage and 7-day Code usage, all with decimal precision and evidence-defined absolute reset times.

## What Changes

- Add backward-compatible, isolated Kimi Code multi-account configuration.
- Add system-browser login and durable saved-session replay using the shared temporary browser/CDP lifecycle.
- Use the authenticated membership quota contract from `https://www.kimi.com/membership/subscription?tab=quota`; `/code/console` is not the primary data/account page.
- Parse and expose both `Kimi` and `Code` percentages under total usage, plus 5-hour Code usage and 7-day Code usage, preserving their evidence-confirmed reset relationships.
- Preserve fractional percentages such as `2.19%` and `10.42%`; do not round the data model to integers.
- Preserve absolute reset instants and present page-consistent local formatting: total usage as a date and 5-hour/7-day windows as month-day plus time.
- Refresh short-lived access tokens through an evidence-backed durable Kimi session without requiring login every 15 minutes.
- Show all four percentages in CLI/sidebar with loading, refreshing, error, expired-session, delete, re-login, and authenticated membership-page opening flows.
- Add parser, transport, refresh, browser-lifecycle, GUI/nogui, secret-boundary, and real-browser acceptance coverage plus bilingual documentation.

## Capabilities

### New Capabilities

- `kimi-code-provider`: Kimi Code account authentication and durable refresh, grouped membership quota retrieval/display (total Kimi + total Code + 5-hour Code + 7-day Code), authenticated quota-page lifecycle, CLI/sidebar integration, and isolated failure behavior.

### Modified Capabilities

None.

## Impact

- Configuration and provider dispatch in `internal/config` and `main.go`.
- A Kimi-specific grouped decimal quota model and authenticated querier in `internal/quota`; existing providers' integer `QuotaUsage` and `QuotaData` JSON semantics remain unchanged.
- Shared browser mechanics in `internal/browserauth` only for missing provider-neutral CDP primitives; Kimi capture/refresh/replay remains provider-specific.
- Sidebar/web routes, grouped cards, authenticated membership-page actions, and rendering tests in `internal/web` and `internal/sidebar`.
- CLI output/help, README/README_EN provider matrices, and canonical GUI/nogui verification.
- Network interaction with evidence-verified Kimi authentication and membership endpoints; secrets remain local and excluded from API/UI/log boundaries.
