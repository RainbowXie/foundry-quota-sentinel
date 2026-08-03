## Why

OpenCode Go cards are currently fetched every two seconds because quota I/O is coupled to the sidebar clock, creating unnecessary upstream load and overlapping requests; users also intermittently see `failed to parse rollingUsage` because the response parser assumes one exact seroval object layout. The sidebar needs a stable parser and an explicit refresh policy that keeps the clock responsive without polling quota providers aggressively.

## What Changes

- Make OpenCode Go quota parsing tolerant of evidence-backed seroval variations such as reference-number drift, field reordering, insignificant whitespace, and additional fields while still rejecting missing, malformed, or unsupported required metrics.
- Add sanitized parser fixtures and RED-to-GREEN tests for every accepted response shape and for fail-closed malformed responses; do not log or commit authentication material or raw private payloads.
- Decouple the visible sidebar clock from provider network requests: update the clock once per second without fetching quota data.
- Standardize automatic quota polling for OpenCode Go, DeepSeek, Ollama, and Kimi to an immediate initial load followed by a 30-second delay after the preceding refresh settles, so one provider cannot accumulate overlapping requests.
- Preserve immediate, explicitly triggered refreshes such as successful login or account deletion while preventing them from starting a duplicate request for the same provider when one is already in flight.

## Capabilities

### New Capabilities

- `opencode-quota-response-parsing`: Evidence-backed, fail-closed parsing of OpenCode Go quota responses across supported seroval layout variations.
- `sidebar-refresh-scheduling`: Independent one-second clock updates and shared non-overlapping 30-second automatic quota polling for all providers.

### Modified Capabilities

None.

## Impact

- `internal/quota/opencode.go` and new focused tests: replace the exact whole-object regex assumption with a structured, bounded parser for the three quota windows.
- `internal/web/static/sidebar.html` and executable sidebar-script tests: separate clock rendering from provider polling and introduce a shared no-overlap scheduler.
- OpenCode Go CLI commands (`quota`, `watch`, `login-opencode`) and Web `/api/accounts` all consume the parser; GitNexus reports CRITICAL blast radius across five process groups, so fixture-based parser regression and full CLI/Web test gates are required.
- No API response schema, persisted configuration, credentials, or displayed quota semantics change.
