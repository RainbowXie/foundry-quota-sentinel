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

## Real-browser acceptance (task 6.4) — partial, blocked on interactive login

The acceptance driver was exercised against a canonical `go build` binary
launched as `login-kimi <name>` under a disposable throwaway `HOME` (config
lived only in `/tmp/fqs-accept-home.*`; the real user config was never written
or read; isolated at the OS level). Two isolated-browser runs were attempted:

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

Verified by the acceptance driver (start-to-launch path, isolated):
- `login-kimi` launches an isolated one-shot browser on the loopback CDP with a
  private temp profile; the protected-response capture + `refresh_token`
  durable-state pipeline is wired; after teardown fix, teardown no longer aborts
  capture.

Not yet run end-to-end (require a completed interactive Kimi login to produce
a saved account first):
- `quota-kimi` four-value fetch through Go-HTTP replay after saving.
- Wait ~900s for `access_token` expiry, then re-run `quota-kimi` to prove the
  durable `refresh_token` auto-refreshes with rotated tokens and no re-login.
- `open-page kimi` membership-page DOM four-value visual compare and manual
  close; confirm no purchase control is automated.

Open blockers / artifacts: every disposable HOME, temp profile, and canonical
binary used for acceptance was shredded after each run; the real user config is
unchanged (0 Kimi accounts). The remaining acceptance steps are gated on a
completed interactive Kimi login in the isolated one-shot window; no secret
artifacts were retained.