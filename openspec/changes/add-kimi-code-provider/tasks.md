## 1. Revised evidence and RED contracts

- [x] 1.1 Run GitNexus context/impact analysis for every existing symbol selected for modification, record direct callers and affected flows, and warn before every HIGH or CRITICAL-risk edit.
- [x] 1.2 Update sanitized membership-page evidence: prove which response ratio maps to total `Kimi` versus total `Code`, whether they share one reset, the 5-hour/7-day Code mappings, absolute reset/timezone formatting, durable refresh rotation, minimum SPA replay state, and exact allowed hosts without retaining secrets.
- [x] 1.3 Replace old fixtures with synthetic grouped fixtures containing distinct total Kimi/Code values plus `0% / 07-29 19:58` 5-hour Code and `10.42% / 08-04 23:58` 7-day Code values, using evidence-confirmed fields and absolute timestamps.
- [x] 1.4 Add RED tests that fail on the current implementation for grouped four-value parsing, label separation, decimal formatting, missing/invalid values, refresh success/failure/rotation, post-expiry quota, membership-page SPA replay, strict URL correlation, and purchase non-automation.
- [x] 1.5 Remove all new diagnostic commands, captured payloads, browser profiles, temporary configs/binaries, and credential-bearing artifacts after evidence is sanitized.

## 2. Durable account and grouped membership domain model

- [x] 2.1 Preserve and rerun backward-compatible configuration round-trip coverage for pre-Kimi and provisional Kimi records without changing other providers/profile/window data.
- [x] 2.2 Revise the versioned Kimi authentication envelope to hold only the evidence-proven durable refresh and membership-SPA replay state; provide an explicit migration/re-login outcome for provisional access-token-only records.
- [x] 2.3 Add strict envelope tests for versioning, closed allowlist, refresh-token rotation, atomic per-account save, control-character/storage validation, concurrent refresh serialization, and secret exclusion.
- [x] 2.4 Replace `Weekly`/`RateLimit` with a Kimi grouped model: `Total` containing distinct Kimi/Code decimals plus reset metadata, and `FiveHour`/`SevenDay` Code leaves, without altering existing provider models.
- [x] 2.5 Implement evidence-backed parsing for both subscription-balance ratios under their confirmed total labels and reset association, plus the 5-hour/7-day objects with exact `0..1` validation, observed zero shape, decimal preservation, future timestamp validation, and local absolute-time formatting.

## 3. Refreshable authenticated quota transport

- [x] 3.1 Add injectable request/refresh transports with exact endpoint/host/path/header/body assertions, bounded timeout/body, redirect rejection, completed-body handling, and response closure.
- [x] 3.2 Implement account-scoped token refresh and one safe retry on authentication expiry, including refresh-token rotation and atomic persistence through a caller-owned credential update boundary.
- [x] 3.3 Implement complete membership-summary retrieval (total Kimi + total Code + 5-hour Code + 7-day Code) and distinct expired-session, refresh, timeout, transport, and unsupported-response errors.
- [x] 3.4 Add tests proving concurrent requests do not race refresh rotation, one account's failure cannot affect another, stale tokens cannot overwrite fresh state, and no secret appears in any error/log/result.

## 4. Shared-browser login and membership-page replay

- [x] 4.1 Reassess shared `internal/browserauth` primitives for document-start storage restoration, exact host/path navigation, and correlated membership response events; run upstream impact before adding only provider-neutral primitives with focused tests.
- [x] 4.2 Update narrow Kimi browser/CDP interfaces and fakes, then add RED login tests for durable state capture, protected complete-summary validation, validation-before-save, cancellation, generation advance, process reap, and profile cleanup.
- [x] 4.3 Implement login capture of the minimum refreshable Kimi session and validate it through the production refreshable complete-summary path before saving.
- [x] 4.4 Add RED membership-page tests for expired-access-token refresh, storage/cookie restoration before navigation, all-four-value readiness, exact membership URL, ready/error handshake ordering, no flash-close, account isolation, and manual-close cleanup.
- [x] 4.5 Implement saved account opening at exactly `https://www.kimi.com/membership/subscription?tab=quota`, restoring the authenticated SPA state and waiting for a correlated valid complete-summary response before readiness.
- [x] 4.6 Remove or redirect obsolete `/code/console` and separate add-on-page assumptions so the user-controlled membership quota page is the account/details destination and no purchase control is automated.

## 5. CLI and local web/sidebar integration

- [x] 5.1 Update Kimi web DTO/provider conversion to return separately labeled total Kimi/Code decimals plus 5-hour/7-day Code values and reset displays while excluding the complete durable authentication envelope.
- [x] 5.2 Update the concurrently fetched, name-sorted Kimi cards endpoint for refreshing/success/expired/error states and prove one account failure does not suppress other results.
- [x] 5.3 Update login, quota refresh, membership-page open, and delete dispatch for durable credential rotation and exact page targeting, including subprocess handshake failure tests.
- [x] 5.4 Update `quota-kimi [name]` output/help to print `总使用量` with both `Kimi` and `Code`, followed by `5 小时用量 · Code` and `7 天用量 · Code`, preserving decimal and absolute reset formatting.
- [x] 5.5 Replace the old card with a total group containing two labeled values plus 5-hour/7-day Code rows, decimal-safe rendering, refresh/re-login/error states, and a membership-page action.
- [x] 5.6 Update GUI and `nogui` route/rendering/browser tests and remove stale assertions for weekly/frequency-only labels, `/code/console`, or automated/separate add-on navigation.

## 6. Security, documentation, and acceptance

- [x] 6.1 Re-audit access/refresh tokens, cookies, storage state, rotated credentials, child processes, errors, API DTOs, HTML, and logs with synthetic-secret boundary tests.
- [x] 6.2 Update README.md, README_EN.md, CLI examples/help, support tables, and account-page documentation to describe the membership quota page, total Kimi/Code pair, and 5-hour/7-day Code values.
- [x] 6.3 Run fresh `gofmt`, default tests, `-race -tags nogui` tests, default/nogui vet, strict OpenSpec validation, and project-standard GUI/WebKit and nogui canonical builds; record exact counts, versions, paths, and SHA256 values.
- [x] 6.4 Perform canonical real-browser acceptance: save one isolated account, prove durable refresh after token expiry, compare total Kimi, total Code, 5-hour Code, and 7-day Code percentages plus group resets with the visible membership page, keep it open until manual close, leave purchase controls untouched, and retain only redacted evidence.
- [x] 6.5 Run GitNexus `detect_changes` against the implementation baseline before each new commit, review every changed symbol/flow, clean diagnostics/secrets, and finish with a clean worktree except local supervisor/agent files.
