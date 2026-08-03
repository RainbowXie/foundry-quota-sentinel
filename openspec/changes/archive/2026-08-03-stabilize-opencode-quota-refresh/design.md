## Context

The sidebar currently uses `fa()` for two unrelated jobs: it awaits the OpenCode Go `/api/accounts` refresh and then updates the visible clock. `setInterval(fa, 2000)` therefore starts a new OpenCode refresh every two seconds without waiting for the previous invocation to finish; an upstream request may take up to fifteen seconds, so several requests can overlap and the clock itself stalls or updates out of order when the network is slow. DeepSeek, Ollama, and Kimi use separate 30-second `setInterval` loops, but those loops can also overlap if a refresh lasts longer than their interval.

OpenCode quota responses use a seroval-like serialization rather than plain JSON. `parseQuotaResponse` currently matches each entire usage object with one exact regular expression: fixed field order, no whitespace, integer fields, and no additional properties. Reference-number drift was addressed previously, but any other evidence-backed layout variation still becomes `failed to parse rollingUsage`. The parser is shared by the Web API and the OpenCode `quota`, `watch`, and login validation paths; GitNexus reports CRITICAL blast radius across five process groups.

## Goals / Non-Goals

**Goals:**

- Parse all observed valid OpenCode seroval quota shapes without depending on reference numbers, property order, insignificant whitespace, or unrelated additional properties.
- Continue to fail closed when required quota windows or required values are absent, duplicated, malformed, negative, or unsupported.
- Update the visible clock once per second independently of network I/O.
- Give OpenCode Go, DeepSeek, Ollama, and Kimi the same immediate-first-load and non-overlapping 30-second automatic refresh policy.
- Coalesce concurrent automatic and explicit refresh triggers for the same provider while preserving independent refreshes across providers.
- Prove the parser and scheduler with deterministic RED-to-GREEN tests that execute production code or the real embedded script.

**Non-Goals:**

- No change to quota percentages, reset calculations, card markup, provider DTOs, credentials, or API response schemas.
- No background refresh while the sidebar page is not running and no user-configurable cadence in this change.
- No suppression of genuine authentication, transport, or malformed-response errors.
- No full general-purpose seroval decoder; only the bounded quota subset evidenced by sanitized fixtures is supported.

## Decisions

### Parse bounded usage objects structurally instead of matching one exact object string

Implementation will first capture sanitized structural fixtures for the currently failing response and existing known-good variants. A helper will locate `rollingUsage`, `weeklyUsage`, or `monthlyUsage` at a field boundary, skip an optional `$R[n]=` assignment, and extract the following bounded object while respecting quoted strings and brace boundaries. Required properties will then be parsed independently inside that object, so their order and surrounding whitespace do not matter and unknown properties can be ignored.

For every required window, `status`, `resetInSec`, and `usagePercent` must occur exactly once with supported types and non-negative numeric values. Rolling and weekly remain required. Monthly remains optional and retains the existing `unlimited` omission behavior, but a present monthly object must be valid. Duplicate or malformed required fields, a missing required object, truncated input, or a non-quota response returns a specific parse error and never fabricates zeros or reuses stale values.

This is preferred over progressively widening the whole-object regular expression: each upstream field addition or reorder would otherwise create another fragile branch. A full seroval decoder is rejected because the application needs only three small primitive objects and should not introduce a large dependency or interpret arbitrary serialized values.

Fixtures must be minimal and synthetic/sanitized: retain only quota structure and representative numeric values. Cookies, workspace identifiers, response headers, unrelated account data, and raw private responses must never enter tests, logs, evidence, or commits.

### Separate the one-second clock from provider refresh scheduling

The visible time will have a dedicated `updateClock()` function that performs no fetch and runs immediately plus every 1,000 milliseconds. Provider health or quota errors remain card concerns; a slow or failed provider request cannot delay the clock.

All four provider refresh functions will be registered with one scheduler using a 30,000-millisecond delay. Each provider performs an immediate initial refresh. After that refresh settles, the scheduler waits 30 seconds before starting the next one. Recursive `setTimeout` after settlement is chosen over `setInterval`, because fixed intervals can create overlapping requests when a network call is slow.

Thirty seconds is the chosen cadence: quota values do not require near-real-time polling, and it matches the existing DeepSeek/Ollama/Kimi policy while reducing OpenCode traffic by a factor of fifteen relative to the current two-second loop. Ten or fifteen seconds would provide little visible benefit while producing two to three times as many upstream requests.

### Make each provider refresh single-flight and let explicit triggers join it

The scheduler will track one in-flight promise and one next timer per provider. If an automatic timer, login completion, delete completion, or other explicit action requests a refresh while that provider is already running, it receives/joins the existing promise instead of starting another request. Providers use separate state, so a slow Kimi refresh does not block OpenCode, DeepSeek, or Ollama.

Any completed refresh attempt—success or failure—arms that provider's next automatic attempt for 30 seconds later. An explicit refresh when idle runs immediately and resets the next automatic deadline from its settlement. Failures remain visible through existing card error behavior and do not permanently stop polling.

The public registry refresh handlers must route through this single-flight boundary; keeping raw `fq`/`fd`/`fo`/`fk` calls outside it would allow login/delete/manual paths to bypass the no-overlap guarantee.

### Test time and concurrency deterministically

Go tests will exercise `parseQuotaResponse` with synthetic fixtures for reference drift, optional assignment, reordered fields, whitespace, extra properties, monthly optional/unlimited behavior, and fail-closed malformed inputs. At least one RED fixture must reproduce the observed `rollingUsage` failure before parser changes.

The sidebar test harness will execute the real embedded script with fake timers and deferred promises. It will assert:

- the clock updates at 0, 1, 2, and 3 seconds without provider fetches;
- every provider loads immediately and does not run again before 30 seconds after settlement;
- a pending refresh cannot overlap with timer or explicit triggers;
- one provider's pending request does not block another provider;
- failure schedules the next attempt and explicit idle refresh resets the automatic deadline.

## Risks / Trade-offs

- **[Parser widening accepts an unrelated object]** → Anchor extraction to exact quota field names and object boundaries; require unique typed fields and add negative near-match fixtures.
- **[Parser change breaks CLI consumers]** → The CRITICAL-impact parser is covered by focused fixtures plus full `quota`, `watch`, login, Web, race, vet, and build regression gates.
- **[Fake-timer tests diverge from browser behavior]** → Execute the production sidebar script and complement deterministic tests with DevTools observation of clock ticks and network requests over at least 65 seconds.
- **[A hung request holds single-flight state]** → Existing HTTP/context timeouts remain authoritative; promise settlement always clears in-flight state and rearms polling.
- **[Thirty-second data latency]** → Explicit login/delete/manual triggers remain immediate, while normal background quota drift is acceptable at a 30-second cadence.

## Migration Plan

No persisted-data or API migration is required. Deploy parser and scheduler changes together, verify the clock and request cadence in the sidebar, and roll back the commit if an unsupported upstream shape is discovered. Sanitized failing structure should then become a new RED fixture before any further parser widening.

## Open Questions

None. The automatic quota cadence is fixed at 30 seconds for this change.
