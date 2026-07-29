## 1. Revised evidence and RED contracts

- [x] 1.1 Run GitNexus context/impact analysis for every existing symbol selected for modification, record direct callers and affected flows, and warn before every HIGH or CRITICAL-risk edit.
- [ ] 1.2 Update the sanitized real-browser evidence for `https://www.kimi.com/membership/subscription?tab=quota`: prove the field-to-label mapping for total/5-hour/7-day usage, absolute reset instants and timezone formatting, exact durable refresh request/rotation, minimum SPA replay state, and allowed hosts without retaining secrets.
- [ ] 1.3 Replace old two-meter fixtures with synthetic three-metric fixtures representing `2.19% / 2026-08-27`, `0% / 07-29 19:58`, and `10.42% / 08-04 23:58`, using evidence-confirmed response fields and absolute timestamps.
- [ ] 1.4 Add RED tests that fail on the current implementation for three-metric parsing, decimal preservation/formatting, missing/invalid metrics, refresh success/failure/rotation, post-expiry quota, membership-page SPA replay, strict URL correlation, and purchase non-automation.
- [ ] 1.5 Remove all new diagnostic commands, captured payloads, browser profiles, temporary configs/binaries, and credential-bearing artifacts after evidence is sanitized.

## 2. Durable account and three-metric domain model

- [x] 2.1 Preserve and rerun backward-compatible configuration round-trip coverage for pre-Kimi and provisional Kimi records without changing other providers/profile/window data.
- [ ] 2.2 Revise the versioned Kimi authentication envelope to hold only the evidence-proven durable refresh and membership-SPA replay state; provide an explicit migration/re-login outcome for provisional access-token-only records.
- [ ] 2.3 Add strict envelope tests for versioning, closed allowlist, refresh-token rotation, atomic per-account save, control-character/storage validation, concurrent refresh serialization, and secret exclusion.
- [ ] 2.4 Replace `Weekly`/`RateLimit` with a Kimi-specific `Total`/`FiveHour`/`SevenDay` aggregate and decimal usage leaf containing absolute reset instant, derived seconds, reset display, and status, without altering existing provider models.
- [ ] 2.5 Implement evidence-backed parsing: total from the confirmed subscription balance ratio/reset, 5-hour and 7-day from their confirmed rate-limit objects, exact `0..1` validation, absent-ratio zero shape, decimal preservation, future timestamp validation, and local absolute-time formatting.

## 3. Refreshable authenticated quota transport

- [ ] 3.1 Add injectable request/refresh transports with exact endpoint/host/path/header/body assertions, bounded timeout/body, redirect rejection, completed-body handling, and response closure.
- [ ] 3.2 Implement account-scoped token refresh and one safe retry on authentication expiry, including refresh-token rotation and atomic persistence through a caller-owned credential update boundary.
- [ ] 3.3 Implement three-metric membership statistics retrieval and distinct expired-session, refresh, timeout, transport, and unsupported-response errors.
- [ ] 3.4 Add tests proving concurrent requests do not race refresh rotation, one account's failure cannot affect another, stale tokens cannot overwrite fresh state, and no secret appears in any error/log/result.

## 4. Shared-browser login and membership-page replay

- [ ] 4.1 Reassess shared `internal/browserauth` primitives for document-start storage restoration, exact host/path navigation, and correlated membership response events; run upstream impact before adding only provider-neutral primitives with focused tests.
- [ ] 4.2 Update narrow Kimi browser/CDP interfaces and fakes, then add RED login tests for durable state capture, protected three-metric validation, validation-before-save, cancellation, generation advance, process reap, and profile cleanup.
- [ ] 4.3 Implement login capture of the minimum refreshable Kimi session and validate it through the production refreshable three-metric path before saving.
- [ ] 4.4 Add RED membership-page tests for expired-access-token refresh, storage/cookie restoration before navigation, three-metric readiness, exact membership URL, ready/error handshake ordering, no flash-close, account isolation, and manual-close cleanup.
- [ ] 4.5 Implement saved account opening at exactly `https://www.kimi.com/membership/subscription?tab=quota`, restoring the authenticated SPA state and waiting for a correlated valid three-metric response before readiness.
- [ ] 4.6 Remove or redirect obsolete `/code/console` and separate add-on-page assumptions so the user-controlled membership quota page is the account/details destination and no purchase control is automated.

## 5. CLI and local web/sidebar integration

- [ ] 5.1 Update Kimi web DTO/provider conversion to return total, 5-hour, and 7-day decimal metrics plus absolute reset displays while excluding the complete durable authentication envelope.
- [ ] 5.2 Update the concurrently fetched, name-sorted Kimi cards endpoint for refreshing/success/expired/error states and prove one account failure does not suppress other results.
- [ ] 5.3 Update login, quota refresh, membership-page open, and delete dispatch for durable credential rotation and exact page targeting, including subprocess handshake failure tests.
- [ ] 5.4 Update `quota-kimi [name]` output and help to print `总使用量`, `5 小时用量`, and `7 天用量`, preserving `2.19%`, `10.42%`, and `0%` formatting plus absolute reset displays.
- [ ] 5.5 Replace the old two-meter sidebar card with three rows, decimal-safe rendering, refresh/re-login/error states, and an account/details action that opens the authenticated membership quota page.
- [ ] 5.6 Update GUI and `nogui` route/rendering/browser tests and remove stale assertions for weekly/frequency-only labels, `/code/console`, or automated/separate add-on navigation.

## 6. Security, documentation, and acceptance

- [ ] 6.1 Re-audit access/refresh tokens, cookies, storage state, rotated credentials, child processes, errors, API DTOs, HTML, and logs with synthetic-secret boundary tests.
- [ ] 6.2 Update README.md, README_EN.md, CLI examples/help, support tables, and account-page documentation to describe the membership quota page and three decimal metrics.
- [ ] 6.3 Run fresh `gofmt`, default tests, `-race -tags nogui` tests, default/nogui vet, strict OpenSpec validation, and project-standard GUI/WebKit and nogui canonical builds; record exact counts, versions, paths, and SHA256 values.
- [ ] 6.4 Perform canonical real-browser acceptance: save one isolated account, wait until/force the original access token to expire, prove automatic durable refresh, compare all three percentages and absolute resets with the visible membership page, keep the page open until manual close, confirm purchase controls are untouched, and retain only redacted evidence.
- [ ] 6.5 Run GitNexus `detect_changes` against the implementation baseline before each new commit, review every changed symbol/flow, clean diagnostics/secrets, and finish with a clean worktree except local supervisor/agent files.
