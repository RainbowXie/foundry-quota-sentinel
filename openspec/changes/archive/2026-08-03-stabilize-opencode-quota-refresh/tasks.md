## 1. Impact analysis and RED characterization

- [x] 1.1 Run GitNexus upstream impact on `parseQuotaResponse` before editing it, record the CRITICAL blast radius and the affected CLI/Web processes, and manually map the embedded sidebar functions and every direct `fq`/`fd`/`fo`/`fk` caller that GitNexus cannot index.
- [x] 1.2 Reproduce or capture the structural shape behind the intermittent `failed to parse rollingUsage`, reduce it to a minimal synthetic fixture with no cookie, workspace ID, headers, raw private body, or account data, and demonstrate a focused RED parser test against the current implementation.
- [x] 1.3 Add table-driven RED parser tests for reference drift, optional `$R[n]=`, reordered fields, whitespace, additional properties, monthly absent/unlimited, and canonical existing behavior.
- [x] 1.4 Add negative parser tests for missing rolling/weekly objects, truncated objects, duplicate or malformed required properties, negative numeric values, and quota-shaped text under unrelated field names; assert no partial or fabricated quota result.
- [x] 1.5 Add a deterministic fake-clock/deferred-promise harness that executes the real embedded sidebar script and demonstrate RED for an independent 1-second clock, immediate provider loads, 30-second post-settlement scheduling, and no overlap.
- [x] 1.6 Add RED scheduler cases for explicit refresh coalescing, independent providers, failure recovery, and resetting the next automatic deadline after an explicit idle refresh.

## 2. OpenCode quota parser stabilization

- [x] 2.1 Replace the exact whole-object quota regular expressions with bounded extraction of exact `rollingUsage`, `weeklyUsage`, and optional `monthlyUsage` objects, including optional seroval reference assignment and safe quoted-string/brace handling.
- [x] 2.2 Parse `status`, `resetInSec`, and `usagePercent` independently of field order, reject missing/duplicate/unsupported values, ignore only unrelated properties, and preserve the existing `QuotaData` and `QuotaUsage` semantics.
- [x] 2.3 Preserve required rolling/weekly behavior and optional/unlimited monthly behavior, return window-specific fail-closed errors, and ensure diagnostics never contain response bodies or authentication/account material.
- [x] 2.4 Run all focused parser tests GREEN and regression-test every `FetchQuota` consumer path identified by the CRITICAL GitNexus impact report.

## 3. Shared sidebar refresh scheduling

- [x] 3.1 Extract a fetch-free `updateClock` path that renders immediately and every 1,000 milliseconds, and remove quota I/O from the clock callback.
- [x] 3.2 Implement one shared per-provider scheduler with immediate first load, a 30,000-millisecond delay measured after settlement, recursive `setTimeout`, per-provider in-flight promise state, and independent timers.
- [x] 3.3 Route the public OpenCode, DeepSeek, Ollama, and Kimi refresh handlers—and all registry/login/delete/manual callers—through the single-flight boundary so an explicit trigger joins rather than duplicates a pending provider request.
- [x] 3.4 Ensure success and failure both clear in-flight state and rearm the next automatic attempt, while an explicit idle refresh cancels/resets that provider's previous next-run timer without affecting other providers.
- [x] 3.5 Remove the old `fa` two-second OpenCode loop and the provider `setInterval` loops, then run the executable sidebar scheduling tests GREEN with exact request counts and timing assertions.

## 4. Verification and evidence

- [x] 4.1 Run focused quota and Web tests, `go test ./...`, `go test -race -tags nogui ./...`, default/nogui `go vet`, GUI/nogui builds, touched-file `gofmt`, and `git diff --check`.
- [x] 4.2 Validate the change and all main specs strictly with OpenSpec, and record sanitized RED-to-GREEN parser/scheduler evidence without secrets.
- [x] 4.3 Inspect the real sidebar with Chrome DevTools for at least 65 seconds: confirm the clock advances once per second, each provider makes one immediate request and no more than one post-settlement request per 30 seconds, no same-provider requests overlap, and card/login/delete behavior remains intact.
- [x] 4.4 Run a repository secret scan over fixtures, evidence, diffs, and generated artifacts, then run GitNexus `detect_changes` against the default branch and verify only the expected quota parsing and sidebar refresh flows are affected before any commit.
