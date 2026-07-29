## 1. Evidence and test contracts

- [x] 1.1 Run GitNexus context/impact analysis for every existing symbol selected for modification, record direct callers and affected flows, and stop for user review before any HIGH or CRITICAL-risk edit.
- [x] 1.2 Use a disposable real Chrome/Edge profile and loopback CDP to capture the sanitized Kimi console navigation/network sequence, identifying the protected quota request, completed-response success discriminator, minimum replay credentials, meter/reset fields, allowed login hosts, and canonical add-on destination without recording live secret values.
- [x] 1.3 Write a checked-in evidence note containing only endpoint paths, event ordering, field names/types, redacted observations, and acceptance conclusions; create synthetic response fixtures for the representative `10% / 6d 12h 20min` weekly and `52% / 3h 20min` frequency-limit case.
- [x] 1.4 Add RED parser/auth-correlation tests that fail on the pre-Kimi implementation for a valid response, business failure inside 2xx, missing success discriminator, missing meter, invalid percentage/reset, unfinished body, stale navigation response, unrelated endpoint, and oversized response.
- [x] 1.5 Remove all diagnostic commands, captured browser profiles, raw traces, and credential-bearing temporary files after the sanitized fixtures/evidence are sufficient.

## 2. Configuration and quota domain model

- [x] 2.1 Add backward-compatibility tests that load and round-trip pre-Kimi config fixtures without changing existing provider/profile/window data.
- [x] 2.2 Add a named Kimi account configuration record, optional account collection, versioned minimal authentication envelope, generation-based successful-login marker, and isolated upsert/delete helpers with unit tests.
- [x] 2.3 Add strict envelope encoding/decoding and validation tests covering supported version, unsupported version, minimum allowlisted values, header control-character rejection, and omission of unknown/unneeded captured state.
- [x] 2.4 Add a provider-specific Kimi quota aggregate with `Weekly`, `RateLimit`, and `FetchedAt`, reusing `QuotaUsage` without changing existing `QuotaData` JSON semantics.
- [x] 2.5 Implement the evidence-backed Kimi response parser and reset normalization so both meters retain independent percentage, seconds, and compact display values; make all RED parser tests green.

## 3. Authenticated quota transport

- [x] 3.1 Add an injectable Kimi querier/client with tests for the exact protected request, required replay state, User-Agent behavior if proven necessary, bounded timeout/body size, HTTPS/host/redirect validation, and response-body closure.
- [x] 3.2 Implement quota retrieval that requires the observed transport and business success signals before parsing and returns distinct expired-auth, timeout, transport, and unsupported-response errors.
- [x] 3.3 Add tests proving secrets never appear in querier errors and that a 2xx business failure, incomplete response, public endpoint, or stale/cross-account response cannot be accepted as current quota.

## 4. Shared-browser Kimi login and account page

- [x] 4.1 Identify any missing provider-neutral CDP primitives; add them to `internal/browserauth` only with upstream impact analysis and focused transport/decode tests, leaving all Kimi URL and credential rules outside the shared package.
- [x] 4.2 Define narrow injectable Kimi browser/CDP interfaces and fakes, then add RED login lifecycle tests for protected-response correlation, manual cancellation, validation-before-save, same-name relogin generation, browser reap, and temporary-profile cleanup.
- [x] 4.3 Implement Kimi system-browser login using the real evidence sequence, allowlisted minimal auth capture, production quota validation, save-after-validation behavior, and deterministic close/wait cleanup.
- [x] 4.4 Add RED account-page tests for credential application before protected navigation, authenticated console verification, ready handshake ordering, error-handshake-before-wait, no flash-close, unsupported envelope, cross-account isolation, and manual-close cleanup.
- [x] 4.5 Implement saved Kimi console replay at `https://www.kimi.com/code/console`, including strict URL validation, ready/error handshake integration, and wait-until-user-close behavior.
- [x] 4.6 Implement the add-on page action using only the evidence-verified canonical Kimi HTTPS host/path and test that it opens the authenticated page without submitting any purchase action.

## 5. CLI and local web API integration

- [x] 5.1 Add Kimi config-to-web DTO conversion and a provider callback that returns no authentication fields, with tests that scan serialized responses for all synthetic secret values.
- [x] 5.2 Add a concurrently fetched, name-sorted Kimi cards endpoint with per-account loading/success/error/re-login status and tests proving one account failure does not suppress other account results.
- [x] 5.3 Extend login, open-page, delete, and refresh dispatch for provider `kimi`, including subprocess handshake/timeouts and tests for valid, missing-account, unknown-provider, and failed-browser cases.
- [x] 5.4 Add `login-kimi <name>` and `quota-kimi [name]` CLI flows, explicit weekly/frequency-limit labels, all-account isolation, fetch timestamps, exit/error behavior, and updated command help tests.
- [x] 5.5 Add Kimi to the sidebar add-account flow and card renderer with both percentages/resets, loading/error/expired states, re-login, refresh, console-open, add-on-open, and delete actions.
- [x] 5.6 Add GUI and `nogui` route/rendering tests that verify Kimi behavior is identical apart from the GUI shell and that no WebView-specific authentication path is introduced.

## 6. Security, documentation, and acceptance

- [ ] 6.1 Audit Kimi credential flow from capture through config, HTTP headers, child processes, errors, API DTOs, HTML, and logs; add regression assertions for every outward-facing boundary.
- [ ] 6.2 Update README.md, README_EN.md, CLI examples/help, provider support tables, and account-page documentation with Kimi weekly/frequency semantics and the non-automated “购买加油包” behavior.
- [ ] 6.3 Run `gofmt`, fresh default tests, fresh `-race -tags nogui` tests, both default and `nogui` vet, and the project-standard GUI/WebKit and `nogui` canonical builds; record exact test counts, binary paths, versions, and SHA256 values.
- [ ] 6.4 Perform a fresh real-browser acceptance using the canonical build: login and save one isolated Kimi account, retrieve both meters and compare them to the visible console, open the authenticated console until manual close, open the verified add-on page without purchasing, confirm no flash-close/profile leak, and retain only redacted evidence.
- [ ] 6.5 Run GitNexus `detect_changes` against the default branch before committing, review every changed symbol and affected execution flow, and resolve any scope outside the Kimi provider change.
