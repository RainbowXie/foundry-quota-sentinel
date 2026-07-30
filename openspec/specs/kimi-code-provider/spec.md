# kimi-code-provider Specification

## Purpose

Kimi Code is a first-class quota provider: multiple named Kimi accounts are
added via an evidence-gated interactive login in an application-owned
temporary browser, their durable sessions refresh short-lived access tokens
without user interaction, and the grouped membership quota (total Kimi/Code,
5-hour Code, 7-day Code) is exposed through the CLI, the local web API, and
the sidebar. Saved accounts open the authenticated membership quota page for
user-controlled viewing; the application never automates purchases.

## Requirements


### Requirement: Kimi accounts are first-class isolated configuration records
The system SHALL allow multiple named Kimi Code accounts to be added, replaced, listed, and deleted independently, SHALL preserve all non-Kimi configuration when saving, and SHALL keep persisted authentication material readable only by the local user under the existing configuration permissions.

#### Scenario: Add a Kimi account to an existing configuration
- **WHEN** a user successfully logs in to Kimi using a new account name
- **THEN** the system saves that Kimi account without changing OpenCode, DeepSeek, Ollama, window, or profile settings

#### Scenario: Replace one named Kimi account
- **WHEN** a user successfully logs in using the name of an existing Kimi account
- **THEN** the system replaces only that account's durable authentication state and advances its non-secret completion generation

#### Scenario: Load a pre-Kimi configuration
- **WHEN** the system loads a valid configuration with no Kimi account field
- **THEN** it treats the Kimi account list as empty and preserves existing configuration on the next save

### Requirement: Kimi login captures a durable refreshable session
The system SHALL perform Kimi login in an application-owned temporary system-browser profile, SHALL accept login only after a correlated protected membership-statistics response succeeds, and SHALL persist the minimum evidence-proven state needed to refresh the short-lived access token and restore the membership page. URL presence, a credential-shaped value, document load, or a fixed delay alone MUST NOT constitute login success.

#### Scenario: Successful interactive login
- **WHEN** the user completes Kimi authentication and the correlated membership-statistics response completes with valid total, 5-hour, and 7-day quota data
- **THEN** the system captures and validates the allowlisted durable session, saves the named account, closes and reaps the owned browser, and removes its temporary profile

#### Scenario: Access token expires after login
- **WHEN** a saved Kimi access token has expired but its durable refresh state is valid
- **THEN** the system refreshes the token without user interaction, retries the protected operation once, and atomically persists any rotated state for that account

#### Scenario: Durable refresh fails
- **WHEN** the access token is expired and the evidence-backed refresh request rejects the saved durable state
- **THEN** the system preserves the last saved account record, reports a re-login-required state, and does not overwrite the account with partial credentials

#### Scenario: Browser closes before login completes
- **WHEN** the user closes the temporary login browser before the protected response and durable state are verified
- **THEN** the system reports login not completed, reaps the process, removes the temporary profile, and leaves the saved account unchanged

### Requirement: Kimi authentication state is minimized and never exposed
The system SHALL persist a versioned closed-allowlist authentication envelope containing only values proven necessary for refresh and membership-page replay, SHALL reject unsafe or unsupported values, and MUST NOT include access tokens, refresh tokens, cookies, storage values, or identifying headers in logs, errors, CLI output, local web API responses, rendered HTML, URLs, child-process arguments, fixtures, or commits.

#### Scenario: Account data is returned to the sidebar
- **WHEN** the local web API returns Kimi account or card data
- **THEN** it includes only account identity, grouped total Kimi/Code values, 5-hour/7-day Code values, fetched time, and status/error fields and excludes every authentication-envelope field

#### Scenario: Stored envelope version is unsupported
- **WHEN** a saved Kimi account contains an unknown authentication-envelope version
- **THEN** quota and membership-page operations fail with a re-login-required error without partial replay

#### Scenario: Unsafe credential value is captured or loaded
- **WHEN** a value violates the evidence-derived storage/header validation rules or could inject control characters
- **THEN** the system rejects it before persistence or transmission

#### Scenario: Concurrent requests refresh one account
- **WHEN** multiple quota/card operations observe the same account's expired access token concurrently
- **THEN** refresh is serialized for that account and no stale refresh result overwrites a newer rotated session

### Requirement: Kimi quota exposes the complete grouped membership summary
The system SHALL retrieve separate total `Kimi` and total `Code` percentages, 5-hour Code usage, and 7-day Code usage for each authenticated Kimi account. Every percentage SHALL preserve decimal precision, every group SHALL retain its evidence-confirmed absolute reset relationship, and no value SHALL be substituted for another.

#### Scenario: Parse the representative membership-page values
- **WHEN** a valid sanitized response contains distinct total `Kimi` and `Code` ratios, a displayed total value `2.19%` resetting on `2026-08-27`, 5-hour Code usage `0%` resetting at `07-29 19:58`, and 7-day Code usage `10.42%` resetting at `08-04 23:58`
- **THEN** the result preserves both separately labeled total percentages plus `0` and `10.42`, retains the evidence-confirmed resets, and renders the specified date/time forms in the page's local timezone

#### Scenario: Total Kimi and Code values differ
- **WHEN** a sanitized fixture assigns different ratios to the evidence-confirmed total `Kimi` and `Code` fields
- **THEN** parser, CLI, API, and sidebar preserve both values under the correct labels and never merge, overwrite, or swap them

#### Scenario: Render percentage precision
- **WHEN** a Kimi metric is displayed in CLI, API-backed sidebar, or account card
- **THEN** the system shows up to two decimal places without integer rounding and trims unnecessary trailing zeros, producing forms such as `2.19%`, `10.42%`, and `0%`

#### Scenario: A required metric is missing
- **WHEN** an otherwise successful response omits total `Kimi`, total `Code`, the 5-hour window, or the 7-day window
- **THEN** the system returns a malformed/unsupported response error and does not fabricate zero or reuse another metric

#### Scenario: Zero-use short window omits its ratio
- **WHEN** the evidence-confirmed 5-hour or 7-day object is present and uses the observed zero-use shape with no ratio field
- **THEN** the system treats that metric's percentage as `0%` while still requiring its enabled/reset fields

#### Scenario: Metric data is invalid
- **WHEN** a ratio is NaN, infinite, outside `0..1`, or a reset timestamp is missing, past, or unparseable
- **THEN** the system rejects the response with an error identifying the invalid metric

#### Scenario: Response is incomplete or too large
- **WHEN** the protected response does not finish, exceeds the configured size bound, or cannot be read before timeout
- **THEN** the system returns a bounded fetch error and does not expose partial data as current quota

### Requirement: Membership quota requests are correlated and host-restricted
The system SHALL correlate authentication, refresh, and quota responses to the current account and request/navigation epoch, SHALL use an exact HTTPS host/path allowlist, and SHALL ignore stale, cross-account, subframe, public, or unrelated successful responses.

#### Scenario: A stale response arrives during a new operation
- **WHEN** a successful response from a previous navigation, token generation, or account arrives while the current operation is waiting
- **THEN** the system ignores it unless its request identity, navigation epoch where applicable, and completed response all correlate to the current operation

#### Scenario: An unrelated Kimi request succeeds
- **WHEN** a public or unrelated Kimi endpoint returns a successful response
- **THEN** the system does not treat that response as login, refresh, membership-page readiness, or quota success

#### Scenario: A URL leaves the exact allowlist
- **WHEN** a protected request or account-page navigation targets a non-HTTPS URL, a different host, or an unapproved path
- **THEN** the system rejects it before sending credentials or navigating

### Requirement: Saved account opening targets the membership quota page
The system SHALL open `https://www.kimi.com/membership/subscription?tab=quota` in the shared temporary system browser, restore or refresh the saved Kimi session before protected page activity, verify an authenticated membership-page boundary with valid total Kimi/Code plus 5-hour/7-day Code data, signal readiness, and keep the browser open until the user closes it.

#### Scenario: Open a valid saved account
- **WHEN** the user opens a Kimi account with valid durable authentication state
- **THEN** the temporary browser reaches the authenticated membership quota page, shows the account's quota/purchase controls, signals ready, and remains open until manual close before profile removal

#### Scenario: Saved access token is expired before page opening
- **WHEN** the membership page is opened after the saved access token expires
- **THEN** the system uses the durable session to refresh or restore the SPA state before declaring readiness and does not require a fresh login while the durable session remains valid

#### Scenario: Replay fails after browser launch
- **WHEN** storage/cookie restoration, refresh, navigation, protected-response validation, or page verification fails after launch
- **THEN** the system signals a redacted actionable error before waiting for manual browser close and does not flash-close the window

#### Scenario: Two Kimi account operations overlap
- **WHEN** a membership page is open for one account while another account is queried or refreshed
- **THEN** each operation uses isolated browser/request/credential state and neither account's secrets or results reach the other

### Requirement: CLI and sidebar present membership quota semantics
The system SHALL provide Kimi login, quota, membership-page open, refresh, and delete dispatch; SHALL render `总使用量` with separate `Kimi` and `Code` values plus `5 小时用量 · Code` and `7 天用量 · Code`; and SHALL provide per-account loading, success, refreshing, expired-session, and error states.

#### Scenario: Query one saved Kimi account from the CLI
- **WHEN** the user runs `quota-kimi` for a valid named account
- **THEN** the CLI prints both total percentages and both Code-window percentages with absolute reset displays, fetch time, and no authentication material

#### Scenario: Query all Kimi accounts with one failure
- **WHEN** the sidebar or CLI refreshes multiple Kimi accounts and one account fails
- **THEN** successful accounts retain their results and only the failing account shows its actionable error or re-login state

#### Scenario: Open quota details from a card
- **WHEN** the user activates the Kimi account-page or quota-details action
- **THEN** the system opens the authenticated membership subscription page with `tab=quota` and leaves any booster purchase decision entirely to the user

#### Scenario: Delete a Kimi account
- **WHEN** the user confirms deletion of a named Kimi card
- **THEN** only that Kimi configuration record is removed and its card disappears

### Requirement: The application does not automate Kimi purchases
The system SHALL treat the authenticated membership quota page as an informational/user-controlled destination and MUST NOT click, submit, or invoke purchase, booster, subscription, or payment operations.

#### Scenario: Membership page contains a purchase action
- **WHEN** the opened membership quota page displays `购买加油包` or another purchase control
- **THEN** the system leaves the control untouched and any subsequent action requires direct user interaction in the browser
