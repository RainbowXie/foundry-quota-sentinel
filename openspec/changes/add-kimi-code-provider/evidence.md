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