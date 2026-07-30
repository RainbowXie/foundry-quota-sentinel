## Context

Foundry Quota Sentinel has provider-specific account records and query implementations, a shared temporary system-browser/CDP layer, and an integer `QuotaUsage` type designed for rolling/weekly/monthly allowance cards. Kimi's authoritative user-facing quota surface is now defined as `https://www.kimi.com/membership/subscription?tab=quota`, not `/code/console`.

The membership page presents three quota groups containing four percentages:

- total usage with separate `Kimi` and `Code` percentages, one of which is `2.19%` in the supplied example, with a full-date reset such as `2026-08-27`;
- 5-hour Code usage, for example `0%`, with a reset such as `07-29 19:58`;
- 7-day Code usage, for example `10.42%`, with a reset such as `08-04 23:58`.

The membership response exposes `subscriptionBalance.amountUsedRatio` and `subscriptionBalance.kimiCodeUsedRatio`; direct page-to-response evidence must establish which maps to the visible total `Kimi` versus `Code` label and whether the two totals share one expiration/reset instant. `ratelimitCode5h` and `ratelimitCode7d` provide the two Code windows. The earlier `Weekly`/`RateLimit` integer aggregate loses the total Kimi/Code distinction, the third group, and decimal precision and must be replaced.

The captured Kimi access token is short-lived (approximately 15 minutes). A saved account therefore also needs the minimum evidence-backed durable refresh state; treating a captured access token as a permanent credential would make quota cards and account-page replay fail shortly after login.

## Goals / Non-Goals

**Goals:**

- Treat Kimi Code as a first-class isolated multi-account provider.
- Query and display total Kimi usage, total Code usage, 5-hour Code usage, and 7-day Code usage from the membership quota contract.
- Preserve decimal percentages and independent absolute reset instants.
- Refresh an expired access token using the minimum durable Kimi session state and atomically persist rotated state when required.
- Open the authenticated membership quota page in the shared temporary browser and keep it open until manual close.
- Provide deterministic loading, success, refresh-required, malformed-response, and browser-error behavior in CLI and sidebar flows.
- Base credential, refresh, response-field, and readiness decisions on sanitized real-browser evidence.

**Non-Goals:**

- Automating purchase, payment, plan changes, or booster checkout.
- Using `/code/console` as the primary quota/account page for this provider.
- Saving a complete browser profile or collecting unrelated Kimi session data.
- Sharing credentials between accounts or syncing them remotely.
- Changing OpenCode, DeepSeek, or Ollama quota JSON semantics.

## Decisions

### Use a Kimi-specific grouped decimal model

Replace the temporary two-meter aggregate with a provider-specific structure equivalent to:

```text
KimiQuotaData {
  Total:     KimiTotalUsage
  FiveHour:  KimiQuotaUsage
  SevenDay:  KimiQuotaUsage
  FetchedAt: timestamp
}

KimiTotalUsage {
  KimiPercent: decimal number
  CodePercent: decimal number
  ResetAt / ResetInSec / ResetDisplay / Status
}

KimiQuotaUsage {
  UsagePercent: decimal number
  ResetAt:      absolute timestamp
  ResetInSec:   derived seconds
  ResetDisplay: formatted absolute time
  Status:       state
}
```

The implementation may use `float64` for the normalized percentage, but parsing must reject NaN, infinity, and ratios outside `0..1`; rendering preserves up to two decimal places and trims unnecessary trailing zeros (`2.19%`, `10.42%`, `0%`). It must not round the stored result to an integer.

The total group renders both labeled percentages and its confirmed reset using local `YYYY-MM-DD`; 5-hour and 7-day displays use local `MM-DD HH:mm`. `ResetAt` remains the source of truth, while `ResetInSec` is derived at fetch time. Existing `QuotaUsage` remains untouched for other providers.

Alternatives considered:

- Add a third field but keep integer `QuotaUsage`. Rejected because it destroys values explicitly required by the user.
- Generalize every provider to arbitrary decimal meters. Deferred because it would unnecessarily migrate stable existing APIs and templates.

### Use the membership statistics response as the quota contract

The production parser binds only to evidence-confirmed fields:

- total `Kimi` and `Code` percentages: `subscriptionBalance.amountUsedRatio × 100` and `subscriptionBalance.kimiCodeUsedRatio × 100`, assigned to labels only after direct membership-page binding evidence;
- total reset: the evidence-confirmed subscription expiration/reset timestamp and its confirmed association across both total values;
- 5-hour percentage/reset: `ratelimitCode5h.ratio × 100` and `resetTime`, with an absent ratio accepted only for the observed zero-use shape;
- 7-day percentage/reset: `ratelimitCode7d.ratio × 100` and `resetTime`.

The querier requires the exact protected endpoint, completed response body, HTTP success, Connect business success, both total ratios, both Code-window objects, valid ratios, and future parseable reset instants. Missing or ambiguous fields fail closed; one value is never reused for another.

The membership page itself is used for real-browser field-to-label verification. DOM scraping remains a fallback only if a required field cannot be tied to a stable protected response.

### Persist a minimal versioned durable session, not only an access token

Each Kimi account stores a provider-owned versioned authentication envelope and a non-secret login generation. The envelope contains only the evidence-proven minimum values needed to:

1. refresh an expired access token for direct quota requests; and
2. reconstruct the membership page's authenticated SPA session in a disposable browser.

Current evidence shows a short-lived access token and a durable refresh token in browser storage. Before implementation, the exact storage key, refresh endpoint/request, token-rotation behavior, required cookies/headers, and logout/revocation behavior must be recorded by name/type/length only. Unknown envelope versions and unsafe header/storage values fail with re-login-required errors.

Refresh is account-scoped and serialized so concurrent card/CLI requests cannot race refresh-token rotation. A successful refresh updates in-memory credentials and atomically saves the rotated envelope without touching other accounts. A failed refresh preserves the last saved envelope, reports an expired-session/re-login state, and never emits secrets.

Alternatives considered:

- Persist only the 15-minute access token. Rejected because the provider would stop working shortly after login.
- Save the whole temporary profile. Rejected for privacy, portability, size, and cleanup reasons.

### Make the membership quota page the saved account-page target

Kimi login and account-page coordination stays above `internal/browserauth`. Login captures and validates the minimum durable session only after an evidence-backed protected response succeeds.

Opening a saved account starts a fresh browser at `about:blank`, installs the proven cookie/storage state at document start, performs refresh if needed, and navigates to exactly `https://www.kimi.com/membership/subscription?tab=quota`. Readiness requires the membership page plus a correlated completed response whose total Kimi/Code and 5-hour/7-day Code values are valid. The browser then remains open until the user closes it.

Errors after launch follow error-handshake-before-wait, so an expired or malformed session produces a visible actionable error without flash-close. The membership page contains the user-controlled booster/purchase UI; Foundry Quota Sentinel only opens the page and never triggers purchase actions. A separate automated purchase route is unnecessary.

### Isolate requests, refresh, and UI state per account

Each account has an independent credential envelope, refresh serialization boundary, querier, browser profile, and result/error state. Concurrent aggregation returns cards sorted by account name; one account's refresh, timeout, expired session, or malformed response affects only that card.

The local web API returns only the account name, grouped total Kimi/Code values, 5-hour/7-day Code values, fetched time, and status/error. Authentication and refresh material must not appear in JSON, HTML, logs, errors, CLI output, URLs, or child-process arguments.

### Keep provider commands explicit

Retain `login-kimi <name>`, `quota-kimi [name]`, and internal `open-page kimi <name>` dispatch. CLI/sidebar render `总使用量` with separate `Kimi` and `Code` values, then `5 小时用量 · Code` and `7 天用量 · Code`, preserving decimal and reset formatting. The account-page action opens the membership quota page.

## Risks / Trade-offs

- **[Refresh-token rotation races]** → Serialize refresh per account and atomically persist the newest envelope.
- **[A 2xx body is a Connect business error]** → Require completed body, business-success shape, and valid parsing of all four grouped percentages.
- **[Percentage precision drifts]** → Keep normalized decimal values, test `0`, `2.19`, and `10.42`, and centralize formatting.
- **[Reset timezone differs from the page]** → Store the absolute instant and format using the same local timezone used for acceptance comparison.
- **[Credential capture stores too much]** → Maintain a closed allowlist derived from successful minimal replay and test every outward-facing boundary for synthetic-secret leakage.
- **[SPA restore races startup]** → Install storage at document start and use correlated network/page events, never fixed sleeps or navigation-count heuristics.
- **[Kimi changes undocumented contracts]** → Keep parsing/refresh isolated, fail closed, and cover sanitized fixtures for every observed shape.
- **[Membership page exposes purchasing controls]** → Only navigate to the validated HTTPS page; do not click or submit purchase/payment actions.

## Migration Plan

1. Update sanitized evidence for the membership page, both total-label mappings, both Code-window mappings, decimal precision, group reset timestamps, and durable refresh flow.
2. Replace the provisional two-meter model and fixtures with grouped total Kimi/Code plus 5-hour/7-day Code decimals; existing non-Kimi models remain unchanged.
3. Version/migrate the provisional Kimi authentication envelope. Because this provider has not been released, unsupported provisional records may require one Kimi re-login rather than unsafe partial conversion.
4. Implement durable refresh for direct quota queries and document-start session restoration for the membership page.
5. Update CLI, API DTOs, cards, labels, routes, tests, and bilingual documentation.
6. Run fresh default/nogui tests, race, vet, formatting, GUI/WebKit and nogui builds, then use the canonical build for login, post-expiry refresh, three-value comparison, and manual-close membership-page acceptance.

Rollback removes Kimi dispatch/UI while leaving optional Kimi config data harmless to older binaries. Existing providers require no migration.

## Open Questions

- What exact refresh request and rotation semantics are required after the 15-minute access token expires?
- Which storage keys/cookies are the minimal set for restoring the authenticated membership SPA?
- Which of `amountUsedRatio` and `kimiCodeUsedRatio` maps to the visible total `Kimi` versus `Code` label, and do both total values share `subscriptionBalance.expireTime` for every plan shape?
- Are ratios ever returned with more than four fractional digits, and should display continue to cap at two decimals to match the page?
