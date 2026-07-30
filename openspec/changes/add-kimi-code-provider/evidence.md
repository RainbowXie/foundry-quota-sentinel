# Kimi Code provider — sanitized evidence note

This note records only endpoint paths, field names/types, event ordering,
status codes, lengths, counts, and redacted observations. It NEVER records
live access/refresh tokens, cookies, Authorization/storage values, account
identifiers, response bodies, or their hashes.

## GitNexus impact analysis (task 1.1)

All code changes are additive or provider-scoped Kimi replacements — no existing
OpenCode/DeepSeek/Ollama field, route, or JSON shape is altered.

| Symbol | File | Risk | Change | Why safe |
|--------|------|------|--------|----------|
| `config.Config` | `internal/config/config.go:38` | CRITICAL | Kimi field additive | backward-compatible |
| `quota.QuotaData` / `QuotaUsage` | `internal/quota/types.go` | CRITICAL | NOT MODIFIED | Kimi uses own decimal model |
| `web.Server.Handler` | `internal/web/server.go:136` | HIGH | `/api/kimi*` routes updated | existing routes unchanged |
| `main.go` CLI | `main.go` | LOW | `kimi` branches + `printKimiQuota` | branch/text |

## Real authenticated capture (task 1.2)

### Membership data page (OBSERVED)

- Authoritative page: `https://www.kimi.com/membership/subscription?tab=quota`.
- Three quota groups with four percentage values:
  - **总使用量 (total usage)**: total `2.37%` with a usage bar split into a
    black (Kimi) segment and a blue (Code) segment; legend `[黑] kimi [蓝] code`.
    Reset `2026-08-27 后重置`.
  - **5 小时用量 · Code**: `3.79%`, reset `07-29 19:58 后重置`.
  - **7 天用量 · Code**: `11.18%`, reset `08-04 23:58 后重置`.

### Total Kimi/Code bar mapping (OBSERVED + page-DOM-confirmed)

The total-usage card renders a bar with two segments:
- `.primary` (black) segment width = **0.02%** = Kimi portion
- `.blue` segment width = **2.35%** = Code portion
- Total = **2.37%** = Kimi + Code

Response field mapping (confirmed with NOW-DIFFERING values, not guessed):
- **Total** = `subscriptionBalance.amountUsedRatio × 100` (0.0237 → 2.37%).
  This is the overall total (black + blue combined).
- **Code portion** = `subscriptionBalance.kimiCodeUsedRatio × 100`
  (0.0235 → 2.35%). This is the blue segment.
- **Kimi portion** = `amountUsedRatio − kimiCodeUsedRatio` (0.0237 − 0.0235 =
  0.0002 → 0.02%). This is the black segment. The response does NOT have a
  separate "kimiUsedRatio" field; the Kimi portion is the difference between
  the total and the Code portion.
- **Total reset** = `subscriptionBalance.expireTime` (absolute ISO,
  `2026-08-28T00:00:00Z` → Shanghai date `2026-08-27`). Both total portions
  share this single reset instant.

### 5h/7d Code window mapping (OBSERVED)

- **5 小时用量 · Code** = `ratelimitCode5h.ratio × 100` (0.0379 → 3.79%).
  Reset = `ratelimitCode5h.resetTime` (`2026-07-29T11:58:03...Z` → Shanghai
  `07-29 19:58`).
- **7 天用量 · Code** = `ratelimitCode7d.ratio × 100` (0.1118 → 11.18%).
  Reset = `ratelimitCode7d.resetTime` (`2026-08-04T15:58:03...Z` → Shanghai
  `08-04 23:58`).
- An absent `ratio` (observed at 0% usage) → 0%.

### API protocol + endpoints (OBSERVED)

- Protected quota: `POST https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats`,
  body `{}`, `connect-protocol-version: 1`, `content-type: application/json`.
- Success = HTTP 200 + no top-level `code` string. Failure = non-2xx with
  `{"code":"unauthenticated",...}`.
- Auth host: `https://auth.kimi.com/api`.

### Durable refresh contract (OBSERVED)

- Access token ~900s (15min). Durable session = `refresh_token` (607-char JWT)
  in `localStorage["refresh_token"]`; `access_token` in
  `localStorage["access_token"]` (606-char JWT).
- On 401, SPA POSTs
  `https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken`
  with body `{"refresh_token":"<jwt>"}` (NO Bearer header). Returns
  `{"accessToken":"<new>","refreshToken":"<new>"}` — BOTH rotate.
- Querier: on 401, refresh once, persist rotated tokens, retry once.

### SPA replay (OBSERVED)

- The membership SPA reads `localStorage["access_token"]` +
  `localStorage["refresh_token"]` to make authenticated calls. Cookies alone
  are NOT enough. Account-page replay must install both tokens into
  localStorage at document start, then navigate to the membership page.

### Go-HTTP replay (VERIFIED)

A Go client replaying the Bearer token + cookie + browser headers against
`GetSubscriptionStats` returned 200 with all metrics. Pure Go-HTTP works for
both quota and refresh.

### Envelope allowlist (CLOSED)

`accessToken`, `refreshToken`, `cookie`, `x_msh_device_id`, `x_traffic_id`,
`x_msh_platform`, `x_msh_version`, `x_language`, `r_timezone`, `user_agent`.

### Membership page as account/details page (OBSERVED)

The membership page contains the user-controlled booster/purchase UI. The
application only navigates to it; no purchase/booster/subscription/payment
action is automated. No separate add-on route. `/code/console` is not the
data/account page.

## Synthetic fixtures (task 1.3)

Fixtures use REAL field names with SYNTHETIC values, built at test time so
resets are always future. Representative case:
- Total: `amountUsedRatio = 0.0219`, `kimiCodeUsedRatio = 0.0199` → total
  2.19%, Kimi 0.20%, Code 1.99% (distinct values to prove label separation).
- 5h Code: `ratelimitCode5h.ratio` absent → 0%.
- 7d Code: `ratelimitCode7d.ratio = 0.1042` → 10.42%.

## Leaked-JWT handling

All Bearer JWTs printed by `get_network_request` have ~15-minute lifetimes and
are expired. They were never recorded/committed/persisted; captured files
shredded immediately after sanitized extraction.

## Secret audit (task 6.1)

`internal/web/kimi_secret_audit_test.go` injects synthetic
`access_token`/`cookie` secrets into the cards, accounts, login, open, and
delete boundaries, plus an error path that would carry the token, and asserts
no synthesized secret surfaces in any JSON response, error message, or
dispatched HTML. TDD RED→GREEN verified the boundaries. The auth envelope
(`KimiAuthEnvelope`) never serializes `accessToken`/`refreshToken`/`cookie` to
the API DTO; only per-account `Generation` shells and the grouped
`KimiQuotaData` are returned. No access/refresh token, cookie, storage value,
or account identifier is logged or output.

## Credential-safety hardening (tasks 3.1/3.2/3.4/4.4) — RED→GREEN

Four hardening gaps found and fixed by RED→GREEN (commit `840d753`):

1. **Redirect rejection (3.1)** — `kimiHTTPClient` + `kimiEnforceNoRedirect`
   refuse all redirects (`CheckRedirect → http.ErrUseLastResponse`), enforced
   on any client (including an injected test client), so a 302 off
   www.kimi.com / auth.kimi.com can never carry the Bearer / refresh_token to a
   redirect target. RED: the default client followed the redirect with
   `Authorization` attached to `evil.example.com`; GREEN: refused.

## Credential-safety hardening round 2 (tasks 3.2/3.4/4.4) — commit `5936450`

Four more gaps found and fixed (RED→GREEN where deterministic):

1. **Atomic config write** — `Config.Save` now serializes to a temp file +
   rename (atomic for same-dir renames), so a crash or concurrent reader
   mid-write never sees a torn/partial config. Regression-guarded by
   `TestSaveAtomic` (concurrent save+read, race-clean). `configPathOverride`
   (RWMutex-guarded) redirects the path in tests only; empty in production
   (same `UserHomeDir` path).
2. **Stale-snapshot concurrency** — new `config.SaveKimiTokens(name, access,
   refresh)` is the shared production persistence path: under a config-wide
   mutex it re-loads the LATEST on-disk config, updates only the target
   account's tokens, then saves atomically. Concurrent different-account
   rotations no longer overwrite each other's freshly-rotated token with a
   stale snapshot (lost update). Regression-guarded by
   `TestSaveKimiTokensConcurrentNoLostUpdate` (200-iteration concurrent
   rotation, race-clean). The lost-update window is a check-then-act race not
   reliably reproducible at speed, so the guard ensures the serialized path
   stays correct; the fix is structural (lock + in-lock reload + atomic save).
3. **CLI/Web save hard-fail** — both the CLI (`kimiPersistRotated`) and the two
   web `SetKimiRefreshSave` callbacks delegate to `SaveKimiTokens`. A save
   failure is now a HARD failure: the CLI returns the error (was a stderr
   warning); the web card already surfaced re-login. Rotated tokens left
   unpersisted are never trusted.
4. **Exact pre-replay URL** — `validateKimiPageURL` now requires the EXACT
   membership page (host `www.kimi.com` + path `/membership/subscription` +
   `tab=quota`), not just scheme+host. RED: `/code/console` and arbitrary Kimi
   paths were accepted; GREEN: rejected.

## Credential-safety hardening round 3 (tasks 3.2/3.4) — commit `44628a2`

Two more gaps found and fixed (RED→GREEN where deterministic):

1. **Web per-account in-lock reload** — the `/api/kimi` refresh closure now
   re-reads the LATEST saved `KimiAccount` inside the per-account lock before
   refreshing (`kimiReloadAccount`, wired to a fresh config load), instead of
   using the request-time snapshot. A concurrent rotation that already
   rotated+saved a new token is observed; the stale snapshot would refresh
   with an already-rotated (now-stale) token and fail. RED: refresh used
   snapshot `v1-access`; GREEN: reloaded `v2-access`
   (`TestKimiCardsReloadsLatestTokenInLock`).
2. **Shared config-write transaction lock** — new `config.Mutate(fn)` takes a
   single config-wide lock (`configWriteMu`), reloads the latest on-disk
   config, applies `fn`, and saves atomically. ALL config writes now go through
   `Mutate`: token rotation (`SaveKimiTokens`), login upserts
   (OpenCode/DeepSeek/Ollama/Kimi), delete, window-size save, and config
   add/use/delete. A concurrent login, delete, or window-save can no longer
   overwrite a freshly-rotated token with a stale snapshot (and vice versa) —
   they serialize on one lock and each sees the other's prior write. The
   cross-overwrite window is a check-then-act race not reliably reproducible at
   speed; the fix is structural (single shared lock + reload) and guarded by
   `TestConfigWriteTransactionNoCrossOverwrite` (concurrent rotation +
   window-save, race-clean). `SaveKimiTokens`' per-account lost-update guard
   still passes (200-iteration concurrent).
2. **Per-account refresh serialization (3.4)** — `Server.kimiRefreshLock`
   returns a per-account mutex; the production Kimi fetch closure holds it
   across refresh+persist, so two concurrent card requests for the SAME account
   cannot race the `RefreshToken` endpoint (no double rotation / partial
   overwrite). RED: 4 concurrent requests overlapped (max in-flight 4); GREEN:
   serialized to 1. Different accounts stay independent.
3. **Persistence-failure propagation (3.2)** — a refresh that succeeds but
   whose rotated-token save FAILS surfaces a re-login-required card error
   instead of silently succeeding with unpersisted rotated tokens (which would
   leave the envelope stale). RED: card returned `success:true`; GREEN:
   surfaces a re-login error (no credential in the error).
4. **Strict membership page (4.4)** — `isKimiMembershipPage` now requires the
   EXACT host `www.kimi.com`, EXACT path `/membership/subscription`, AND
   `tab=quota`. RED: a missing tab and a trailing path were wrongly accepted;
   GREEN: rejected.

## Credential-safety hardening round 4 (tasks 3.2/3.4) — commit `0e73338`

Three more gaps found and fixed; two are deterministic two-process REDs:

1. **Per-account cross-process lock** — `config.AcquireKimiAccountLock(name)`
   takes a file `flock` (Unix `LOCK_EX`) per sanitized account name, serializing
   reload→refresh→persist for the SAME account across SEPARATE processes (CLI
   `quota-kimi` vs web request vs `open-page`). The web refresh closure now
   holds this lock (`kimiAccountLock`, wired in production) around
   reload→refresh→save. `TestCrossProcessAccountLockSerializes` forks two
   `_locktest` subprocesses for the same account and asserts the 2nd HELD
   timestamp ≥ the 1st DONE (serialized). RED proved by removing the file lock:
   2nd HELD (ms 1785338324946) < 1st DONE (ms 1785338325248) → both held
   concurrently; GREEN with the lock.
2. **Global cross-process config lock** — `config.Mutate` now goes through
   `WithConfigLock`, which holds BOTH the in-process `configWriteMu` AND a file
   `flock` on `config.lock`, so ALL config writes (rotation, login, delete,
   window-save, config add/use/delete) transactionalize across processes too.
   `TestCrossProcessConfigLockSerializes` forks two `_locktest` subprocesses
   writing window sizes and asserts serialization. RED proved by removing the
   file lock: 2nd HELD (ms 1785338341774) < 1st DONE (ms 1785338342103); GREEN
   with the lock.
3. **kimiReloadAccount fails hard** — when the reloader is wired and the
   account is NOT found (deleted mid-flight), the refresh fails immediately
   instead of falling back to the stale request-time snapshot (no refreshing a
   just-deleted account). RED: refresh ran on the stale snapshot; GREEN: hard
   failure (`TestKimiCardsReloadAccountNotFoundFailsHard`).

OpenSpec proposal/design/spec committed (previously untracked, sanitized — no
credentials).

## Fresh canonical build verification (task 6.3)

- Toolchain: Go 1.26.4 linux/amd64; OpenSpec CLI 1.6.0.
- `gofmt -l` on all edited Kimi files + `main.go` + `main_kimi_test.go` +
  `sidebar_test.go`: clean (no output).
- Default `go test ./... -count=1`: 6 packages OK
  (`main`, `internal/browserauth`, `internal/config`, `internal/quota`,
  `internal/sidebar`, `internal/web`).
- `go test -race -tags nogui ./... -count=1`: same 6 packages OK, race-clean.
- Default and `-tags nogui` `go vet ./...`: clean.
- `openspec validate add-kimi-code-provider --strict`: valid.
- Canonical builds (record-flagged, binaries shredded after capture):
  - nogui: `CGO_ENABLED=0 go build -tags nogui -o <tmp> .` → OK.
  - GUI default: `go build -o <tmp> .` → OK (webkit2gtk dev headers present).
- Temp binary artifacts were shredded after SHA capture; no secrets were
  contained in any build output.

## Real-browser acceptance (task 6.4) — COMPLETED end-to-end

The acceptance driver was exercised against a canonical `go build` binary
launched as `login-kimi <name>` under a disposable throwaway `HOME` (config
lived only in `/tmp/fqs-accept-home.*`; the real user config was never written
or read; isolated at the OS level). Three isolated-browser runs were attempted:

1. The login window launched correctly (isolated Chrome, `--user-data-dir=<tmp>`
   temp profile, `--remote-debugging-pipe` loopback CDP only — the user's
   everyday profile is never read), reached the in-page login prompt, and the
   capture path reached validation. The run then aborted on post-capture
   temp-profile teardown with `unlinkat /tmp/fqs-browserauth-*/Default:
   directory not empty` — a real race in the shared `browserauth` teardown
   (Chrome helpers hold file handles briefly after the parent exits).
2. After that root-cause fix (commit `20db246`, `removeProfileDir` bounded
   retry, RED→GREEN proved) and a fresh disposable HOME + rebuilt binary, the
   login window launched correctly again but the Kimi interactive login was
   not completed within the idle window, so no account was saved and the
   downstream steps did not run.
3. A third run with the teardown fix confirmed it in production: the window
   launched and, on close without a captured login, exited cleanly with the
   `未捕获到有效凭证（窗口已关闭）` sentinel — **no `unlinkat: directory not
   empty` abort**. This proves the teardown-fix (1→2 regression, 2→3 clean) is
   effective in the real browser, not only under RED→GREEN unit simulation.

Verified by the acceptance driver (start-to-launch path, isolated):
- `login-kimi` launches an isolated one-shot browser on the loopback CDP with a
  private temp profile; the protected-response capture + `refresh_token`
  durable-state pipeline is wired; after teardown fix, teardown no longer aborts
  capture, even when the window closes without a login.

### Full end-to-end acceptance (COMPLETED)

A canonical `go build` binary was run under a disposable throwaway `HOME`
(config lived only in `/tmp/fqs-accept-home.*`; the real user config was never
written — 0 Kimi accounts before and after). The complete 6.4 chain ran:

1. **Save an isolated account** — `login-kimi acceptance` captured an
   interactive Kimi login in the isolated one-shot browser and saved a version-1
   `KimiAuthEnvelope` (10 allowlisted fields, generation 1). No secret retained.
2. **Four-value fetch through production Go-HTTP** — `quota-kimi acceptance`
   returned (redacted values, identical to the live membership page):
   - 总使用量 `2.37%` (Kimi `0.02%` / Code `2.35%`), reset `2026-08-28`
   - 5 小时用量 · Code `0%`, reset `07-30 00:58`
   - 7 天用量 · Code `11.18%`, reset `08-04 23:58`
3. **Durable refresh after token expiry** — waited ~20 min (the ~15-min
   `access_token` JWT had expired), then re-ran `quota-kimi`. It returned the
   SAME four values with NO re-login: `FetchQuotaWithRefresh` auto-refreshed
   once via the saved `refresh_token`, rotated both tokens (access/refresh
   lengths changed from 606/607 to 567/568 — rotation confirmed), and atomically
   persisted the rotated envelope. This is the core durable-refresh proof.
4. **Membership-page authenticated open + four-value compare** — `open-page kimi`
   replayed the saved SPA state (6 cookies injected, document-start storage
   restore), navigated to exactly `https://www.kimi.com/membership/subscription?tab=quota`,
   observed the correlated protected `GetSubscriptionStats` 200
   (loaderId-matched) → `loadingFinished` → parsed three metrics valid → final
   URL `host=www.kimi.com path=/membership/subscription` → signalled ready.
   The four values matched across: initial capture, post-expiry refresh, and the
   membership-page authenticated state.
5. **Manual close (no flash-close)** — the page stayed open on `browser.Wait()`
   until the user closed the window; the binary then exited cleanly.
6. **No purchase automation** — `RunKimiPage` only navigates + signals ready +
   waits; there is no click/submit/purchase/booster code path. The membership
   page's purchase controls are user-controlled; the program only opens the page.

Artifacts: every disposable HOME, temp profile, canonical binary, and log was
shredded after the run; no secret artifacts were retained; the real user config
is unchanged (0 Kimi accounts).

### Round-2 hardening re-acceptance (post `5936450`)

After the round-2 credential-safety hardening (atomic `Save`, serialized
`SaveKimiTokens` reload, CLI/Web hard-fail, exact pre-replay URL), the full
6.4 chain was re-run under a fresh disposable HOME to prove the hardening did
not regress the real path:

- `login-kimi acceptance2` saved an isolated version-1 envelope (gen 1, initial
  access/refresh 606/607 chars) from an interactive login.
- `quota-kimi acceptance2` returned the same four values via the production
  Go-HTTP path (hardened save path active).
- After ~16 min (access_token expired), `quota-kimi` returned the SAME four
  values with NO re-login — `FetchQuotaWithRefresh` auto-refreshed, the rotated
  tokens (606/607 → 567/568 chars) were persisted through the serialized
  atomic `SaveKimiTokens` path. Hardened durable refresh proven.
- `open-page kimi` passed the tightened `validateKimiPageURL` exact membership
  check, replayed SPA state, reached the authenticated membership page
  (protected 200 + three-metric valid), and held open until manual close
  (no flash-close).

All disposable artifacts shredded; real config unchanged (0 Kimi accounts); no
secret artifacts retained.

### Round-4 hardening re-acceptance (post `0e73338`)

After the round-4 credential-safety hardening (per-account + global cross-process
file locks, in-lock reload, reload-fail-hard, exact pre-replay URL), the full
6.4 chain was re-run under a fresh disposable HOME:

- `login-kimi acceptance4` saved an isolated version-1 envelope (gen 1, initial
  access/refresh 606/607 chars) from an interactive login.
- `quota-kimi acceptance4` returned the same four values via the production
  Go-HTTP path (cross-process locks active).
- After ~16 min (access_token expired), `quota-kimi` returned the SAME four
  values with NO re-login — `FetchQuotaWithRefresh` auto-refreshed, the rotated
  tokens (606/607 → 567/568 chars) persisted through the cross-process
  `Mutate`/`SaveKimiTokens` (global file flock + in-process lock + atomic
  temp+rename). Cross-process durable refresh proven.
- `open-page kimi` passed the tightened `validateKimiPageURL` exact membership
  check, replayed SPA state, reached the authenticated membership page
  (protected 200 + three-metric valid), and held open until close (no
  flash-close).

All disposable artifacts shredded; real config unchanged (0 Kimi accounts); no
secret artifacts retained. Task 6.4 re-ticked.

## Credential-safety hardening round 5 (tasks 3.2/3.4) — commit `96c43d3`

Four more gaps found and fixed (RED→GREEN where deterministic):

1. **Full per-account lock chain across production paths** — `quota-kimi` CLI,
   Kimi login save, `open-page` (envelope read), and web refresh now ALL hold
   `AcquireKimiAccountLock` across their reload→refresh/capture→persist span,
   with lock order account→global (a concurrent refresh cannot rotate while
   login overwrites the envelope, etc.). `open-page` releases before the long
   browser replay so it does not block concurrent refreshes for the page-open
   duration.
2. **Windows real cross-process lock** — `flock_windows.go` now uses
   `LockFileEx`/`UnlockFileEx` (`golang.org/x/sys/windows`), not a no-op.
3. **Unix release does NOT remove the lock file** — `fileLock.Close` only
   unlocks + closes the fd. Removing the file would unlink the inode a waiter
   is blocked on; the waiter's `OpenFile` would create a NEW inode it then
   flocks — two separate locks, no mutual exclusion (the classic flock inode
   race). The lock file is a tiny persistent sentinel.
4. **3-process inode-race RED** — `TestCrossProcessAccountLockSerializes-
   ThreeProcesses` forks THREE concurrent `_locktest` account processes,
   asserts all serialize (3rd HELD ≥ 2nd DONE), and asserts the lock file
   STILL EXISTS afterward (waiters reused the same inode). RED proved by
   restoring `os.Remove` in Close: test fails "lock file removed after release
   — waiters must reuse the same inode, not a new file"; GREEN without remove.

CLI/Web/login concurrency: the real `quota-kimi`/login/`open-page`/web paths
all call the SAME `AcquireKimiAccountLock` proven by the 2/3-process `_locktest`
REDs (which fork the production binary). A direct CLI-vs-`_locktest` RED was
attempted but is non-deterministic (synthetic-token fetch latency is
unpredictable), so the 2/3-process `_locktest` REDs remain the deterministic
proof of the shared lock.
## Credential-safety hardening round 6 (tasks 3.2/3.4) — commit `f0ca2d7`

Two more gaps found in the open-page replay path and fixed (RED→GREEN):

1. **Stale-snapshot replay token** — `cmdOpenPage`'s kimi branch read the
   envelope from the process-start `cfg` snapshot under the account lock. If a
   concurrent Web/CLI rotation already rotated+persisted the token after
   process start, the replay encoded the STALE snapshot token. Fix:
   `kimiReplayEnvelope` reloads the LATEST on-disk account
   (`latestKimiAccount`) inside the lock before encoding.
2. **In-flight page rotation never persisted** — if the access token was
   expired, the page's own rotation happened in the browser but was never
   written back, so the on-disk credential stayed expired/invalid for later
   CLI/Web runs. Fix: `kimiReplayEnvelope` runs
   `FetchQuotaWithRefresh`→`SaveKimiTokens` inside the lock so an expired
   token is rotated AND persisted BEFORE the envelope is encoded; the encoded
   envelope is then reloaded from the just-persisted state.

The lock is released before the long browser replay runs, so it does not block
concurrent refreshes for the page-open duration. `kimiReplayRefresh` is an
injectable var so tests avoid real network calls.

RED→GREEN proof:
- `TestKimiReplayEnvelopeUsesRotatedTokenNotStaleSnapshot` — RED: replay
  encoded the stale startup token ("replay encoded the STALE startup snapshot
  token"); GREEN: reloads and encodes the concurrently-rotated token.
- `TestKimiReplayEnvelopePersistsInPageRotationNotInvalidateDisk` — RED: disk
  kept the expired token ("disk credential is still the EXPIRED token — the
  page's in-flight rotation was not persisted"); GREEN: `SaveKimiTokens`
  persists the rotated token before encoding, and the encoded envelope carries
  it.

Tests: 6/6 default + 6/6 `-race -tags nogui` pass; `go vet` clean; openspec
strict valid. Task 6.4 stays open pending a fresh real-browser re-acceptance
on the round-6 binary.

### Round-6 hardening re-acceptance (post `f0ca2d7`)

After the round-6 replay-envelope hardening (`kimiReplayEnvelope` — in-lock
reload+refresh+persist before replay), the full 6.4 chain was re-run under a
fresh disposable HOME on a canonical `go build` binary (`/tmp/fqs-accept`):

- `login-kimi acceptance6` saved an isolated version-1 envelope (gen 1, 10
  allowlisted fields, initial access/refresh 567/568 chars) from an interactive
  login.
- `quota-kimi acceptance6` returned the four values via the production Go-HTTP
  path: 总使用量 `2.46%` (Kimi `0.02%` / Code `2.44%`) reset `2026-08-28`;
  5 小时 Code `2.31%` reset `07-30 10:58`; 7 天 Code `11.64%` reset
  `08-04 23:58`.
- After ~16 min (access_token expired ~09:49), `quota-kimi` returned the four
  values again (`2.48%` / `2.71%` / `11.72%` — live values moved slightly) with
  NO re-login. Rotation proven by the JWT `exp` claim: the persisted access
  token expires `10:06:21` = issued exactly at the 09:51:21 query time, i.e.
  `FetchQuotaWithRefresh` auto-refreshed and persisted a fresh token. (Lengths
  coincided at 567/568 this issuance; `exp` is the rotation proof.)
- `open-page kimi acceptance6` exercised the round-6 `kimiReplayEnvelope` path:
  6 cookies injected (0 failed), navigation epoch loaderId-matched, protected
  interface 200 observed + loadingFinished, three metrics valid, final URL
  exactly `host=www.kimi.com path=/membership/subscription`, authenticated
  membership page reached. Page held open on `browser.Wait()` until the user
  closed the window manually — no flash-close, clean exit.

All disposable artifacts shredded (disposable HOME, temp profiles, canonical
binary, logs); real config unchanged (0 Kimi accounts); no secret artifacts
retained. Task 6.4 re-ticked.

## Credential-safety hardening round 7 (tasks 3.2/3.4) — commit `c53fd1e`

Gap (reported during review): while `open-page` holds the membership page open
past the access-token expiry (~15-min JWT), the Kimi SPA refreshes the token
ITSELF and rotates BOTH tokens in localStorage (OBSERVED in the provider
contract: `AuthService/RefreshToken` returns new `accessToken` + new
`refreshToken` — both rotate). The page session never wrote the rotated pair
back, so the on-disk refresh token was invalidated by the page's in-flight
rotation and the next CLI/Web run forced a re-login. Round 6 only covered the
pre-replay window (`kimiReplayEnvelope`), not the page session. Rotation
cannot be proven absent — the SPA stores `refresh_token` in localStorage
precisely to refresh — so capture+persist is the required behavior.

Fix (evidence-verified localStorage capture + atomic persist):

1. **`kimiWatchInPageRotation`** (sidebar) — after the auth decision, a
   watcher consumes the (buffered) CDP events channel for the rest of the
   page session. Evidence rule (no blind localStorage trust): a request on
   the protected `https://www.kimi.com/apiv2/` namespace carrying a Bearer
   token ≠ lastKnown, answered 2xx — the server accepted the new token.
   Only then is the localStorage pair read, and persisted ONLY when
   `access_token` equals the evidenced token and `refresh_token` is
   non-empty. Runs on its own context (the page setup ctx is a 20s
   deadline), stops cleanly on browser close, chains multiple rotations per
   session (lastKnown advances per successful save).
2. **`KimiPageRotationSave`** (sidebar package hook) — nil disables the
   watcher (no behavioral change for other callers; `RunKimiPage` signature
   unchanged). cmdOpenPage installs the per-account closure.
3. **`kimiPageRotationSaver(name)`** (main) — compare-and-swap under the
   per-account cross-process lock: persists via `config.SaveKimiTokens` ONLY
   when the on-disk access token still equals `prevAccess` (the token the
   page rotated FROM); skips cleanly when a concurrent CLI/Web rotation
   already moved disk ahead — disk NEVER regresses to a revoked pair. A
   missed intermediate rotation self-heals (a later evidenced rotation still
   CAS-matches the untouched disk and persists the newest pair).

RED→GREEN proof:
- `TestRunKimiPageCapturesSpaRotationAfterProtectedEvidence` — RED: "in-page
  rotation was NOT captured" (no watcher); GREEN: the watcher hands the
  SPA-rotated pair to the save hook with prev = the replayed token.
- `TestRunKimiPageChainsSequentialRotations` — RED: first rotation not
  captured; GREEN: two rotations captured in order, prev chaining correct.
- `TestRunKimiPageSkipsRotationWithoutProtectedEvidence` — no capture when
  the new token appears only on a non-protected URL, when the protected call
  is 401, or when localStorage disagrees with the evidenced token.
- `TestKimiPageRotationSaverPersistsWhenDiskMatchesPrev` — CAS persists the
  SPA-rotated pair when disk matches prev.
- `TestKimiPageRotationSaverSkipsWhenDiskMovedAhead` — CAS skips (disk
  unchanged) when a concurrent CLI rotation already landed; no regression.

Documented limitation: on a page RELOAD after an in-page rotation, the
document-start restore script reinstalls the replay-time (older) pair into
localStorage, so the page's next refresh may be rejected and the page session
degrades to a re-login prompt — the DISK credential is unaffected (the
persisted rotated pair stays valid; CLI/Web keep working). Re-installing the
restore script with rotated tokens is a possible future round.

Tests: full suite + `-race -tags nogui` pass; `go vet` clean; openspec strict
valid. Task 6.4 reopened for a fresh real-browser re-acceptance whose page
session must SPAN the access-token expiry (prove the real SPA rotation is
captured and persisted, then `quota-kimi` works with no re-login).

### Round-7 cross-expiry re-acceptance (post `c53fd1e`) — COMPLETED end-to-end

The full 6.4 chain was re-run under a fresh disposable HOME on a canonical
`go build` binary, with the page session SPANNING the access-token expiry:

- `login-kimi acceptance7` saved an isolated version-1 envelope (gen 1,
  baseline access JWT exp `10:38:23`, access/refresh 606/607 chars).
- `quota-kimi acceptance7` returned the four values via the production
  Go-HTTP path (总 `3.14%`, 5h `18.35%`, 7d `14.86%`).
- `open-page kimi acceptance7` replayed the envelope (6 cookies injected,
  protected 200 loaderId-matched, three metrics valid, exact membership
  URL) and held the authenticated page open.
- **Cross-expiry**: the replayed access token expired at `10:38:23` while
  the page stayed open. The idle membership SPA did not self-refresh; on
  user interaction at `10:51` the SPA called its own refresh, rotated BOTH
  tokens in localStorage, and retried a protected call carrying the NEW
  Bearer token. The round-7 watcher observed the protected /apiv2/ 2xx
  evidence, read the consistent localStorage pair, and persisted it —
  log: `10:51:57 页面内 token 轮换已捕获并持久化（access 长度 606→606）`
  (lengths coincided; the JWT exp is the rotation proof).
- **Disk proof**: the persisted access token's exp is `11:06:57` = issued
  exactly at `10:51:57` (the SPA's in-page rotation) — the on-disk pair IS
  the SPA-rotated pair, not the invalidated replay pair.
- **Post-close proof**: the user closed the page manually (clean exit, no
  flash-close); `quota-kimi acceptance7` then returned the four values
  (总 `3.81%`, 5h `34.38%`, 7d `18.08%`) with NO re-login — the
  page-rotated on-disk pair works directly. The page's in-flight rotation
  did NOT invalidate the saved credential: the round-7 gap is closed in
  the real browser.

All disposable artifacts shredded (disposable HOME, temp profiles, canonical
binary, logs); real config unchanged (0 Kimi accounts); no secret artifacts
retained. Task 6.4 re-ticked.

## Credential-safety hardening round 8 (tasks 3.2/3.4) — commit `880c25e`

Blocking review of round 7 found two gaps in the in-page rotation watcher;
both fixed with deterministic RED→GREEN:

1. **Evidence rule was too loose** — the watcher treated ANY 2xx on the
   `/apiv2/` namespace carrying a new Bearer token as rotation evidence,
   violating the correlation spec ("Membership quota requests are correlated
   and host-restricted … exact HTTPS host/path allowlist"). Tightened to the
   full membership auth-decision chain: the candidate request must target the
   EXACT protected GetSubscriptionStats URL (`isKimiProtectedURL`: https +
   www.kimi.com + exact path), answered 2xx, completed by `loadingFinished`,
   AND the response body must parse as the two-meter quota result
   (`kimiResponseBodyValid`); only then is the localStorage pair read and
   persisted (access_token == evidenced token, refresh_token non-empty). The
   `/apiv2/` prefix matcher is removed.
2. **Close-window race dropped queued rotation evidence** — `browser.Wait()`
   returning closed `stop`, and the watcher's `select` could take the stop
   branch while rotation events were still queued in the (buffered) events
   channel → the rotation was dropped. Fixed structurally: after stop is
   observed the watcher keeps processing until the events channel CLOSES
   (verified: `browserauth` `readLoop` `defer close(c.events)` on connection
   death; `Events()` documents "The channel closes when the connection
   ends"), or ctx is cancelled as an escape hatch. `runKimiPage` now waits
   for the watcher BEFORE cancelling its context (the old order killed
   in-flight body/localStorage reads). Evidence reads on a dead connection
   fail and skip — nothing garbage is persisted. The test fake now models
   the real death semantics (`Wait()` closes the events channel once).

RED→GREEN proof:
- `TestRunKimiPageSkipsRotationWithoutProtectedEvidence/unrelated_endpoint_
  inside_apiv2_namespace` — RED: save hook fired on an unrelated `/apiv2/`
  2xx (`GetProfile`); GREEN: exact-URL gate rejects it.
- `TestKimiWatcherDrainsQueuedRotationAfterStop` — deterministic RED: stop
  closed FIRST with an empty channel (the old return-on-stop path exits
  immediately), evidence chain delivered after, channel then closed; RED:
  "queued rotation dropped on stop"; GREEN: the drain processes the
  post-stop chain and the save fires before the watcher exits.
- Round-7 capture/chain tests updated to the full evidence chain
  (`loadingFinished` + valid body) and keep passing.

Also: the un-adjudicated OpenSpec archive + spec sync were reverted
(`325c8fb`); the change is active again with 6.4 reopened.

Tests: full suite + `-race -tags nogui` pass; `go vet` clean; openspec strict
valid. Task 6.4 requires a fresh real cross-expiry acceptance on the
round-8 binary (exact-URL evidence + close-race drain in the real browser).
