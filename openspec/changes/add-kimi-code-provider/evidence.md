# Kimi Code provider — sanitized evidence note

This note records only endpoint paths, field names/types, event ordering,
status codes, lengths, counts, and redacted observations. It NEVER records
live cookie/token/Authorization/header values, account identifiers, response
bodies, or their hashes. The contract below was captured from a REAL
authenticated disposable browser session (task 1.2) and verified by a plain
Go-HTTP replay probe returning 200.

## GitNexus impact analysis (task 1.1)

Run before any edit to an existing symbol. All code changes are **additive** —
no existing field, route, or JSON shape is altered — so the HIGH/CRITICAL risk
ratings are structural (many transitive callers), not behavioral.

| Symbol | File | Risk | Change | Why safe |
|--------|------|------|--------|----------|
| `config.Config` | `internal/config/config.go:38` | CRITICAL | add `KimiAccounts []KimiAccount` (omitempty) | backward-compatible load; pre-Kimi configs → empty list |
| `quota.QuotaData` | `internal/quota/types.go:12` | CRITICAL | NOT MODIFIED | new `KimiQuotaData` aggregate reuses `QuotaUsage` leaves |
| `web.Server.Handler` | `internal/web/server.go:136` | HIGH | add `/api/kimi*` routes | existing routes unchanged |
| `deleteAccountFromConfig` / `cmdOpenPage` | `main.go` | LOW | add `case kimi` branch | branch addition |

Regression guard: existing provider tests must stay green.

## Real authenticated capture (task 1.2)

Performed via a real disposable browser session (isolated profile, loopback
CDP). The user authenticated by SMS; the SPA routed to
`/code/console`; the protected quota request fired. No credentials, response
bodies, or account identifiers were recorded — only paths, field names/types,
status codes, and lengths.

### Console target (OBSERVED)

- Console URL: `https://www.kimi.com/code/console` (client-side auth gate; an
  unauthenticated visitor lands on the `/code` marketing page, NOT a redirect).
- Two meters rendered in the visible UI: 本周用量 `10%` · `6d 8h 25min后重置`;
  频限明细 `0%` · `4h 25min后重置`.

### API protocol + protected endpoint (OBSERVED)

Kimi uses **Buf Connect gRPC-Web over JSON**, NOT REST `/api/*` (those 404).

- Protected quota endpoint (OBSERVED, returned 200 authenticated):
  `POST https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats`
  with body `{}`, `content-type: application/json`,
  `connect-protocol-version: 1`.
- Auth host: `https://auth.kimi.com/api` (login uses
  `account.gateway.v1.AuthService/LoginWithSMS`, etc.).
- Sibling membership calls observed: `GetSubscription`, `ListSubscriptions`
  (`MembershipService`); billing via `BillingService/GetUsages`;
  usage records via `code.v1.UsageService/ListUnifiedRequests`.

### Success discriminator (OBSERVED)

NOT DeepSeek's `code==0`. Connect-JSON: **success = HTTP 200 + a response body
with NO top-level `code` string**. Failure (unauthenticated) is a non-2xx with
`{"code":"unauthenticated",...}`. Confirmed: the captured 200 body has no
`code` field; the unauthenticated probe (no token) returns 401 with a `code`
string.

### Real response structure (OBSERVED — the fields the parser binds to)

`GetSubscriptionStats` 200 body top-level keys (exactly three):

```
ratelimitCode5h: { enabled: bool, resetTime: string(len 30) }
ratelimitCode7d: { ratio: number (0..1), enabled: bool, resetTime: string(len 30) }
subscriptionBalance: { id, feature, type, unit, amountUsedRatio, kimiCodeUsedRatio, expireTime, domain }
```

Field semantics (mapped against the visible console):
- **本周用量 (weekly usage)** = `ratelimitCode7d.ratio`, a 0..1 ratio →
  percentage = round(ratio*100). Captured `ratio=0.1042` → console `10%`.
  Reset = `ratelimitCode7d.resetTime` (absolute ISO-8601 timestamp,
  nanosecond precision, e.g. `2026-08-04T15:58:03.138613843Z`).
- **频限明细 (frequency limit)** = `ratelimitCode5h`. It carries `enabled`
  + `resetTime`; the percentage comes from a `ratio` field present when usage
  > 0 (at 0% the field is absent → parser treats absent ratio as 0%). Reset =
  `ratelimitCode5h.resetTime` (same ISO-8601 absolute timestamp).
- `resetTime` is an ABSOLUTE future timestamp, NOT a duration. The parser
  computes `resetTime - now` → seconds (the countdown). Verified:
  `2026-08-04T15:58:03...` − now ≈ 548823s ≈ 6d 8h 25min ✓ (matches the
  visible countdown). Past/negative/missing resetTime is rejected.
- `subscriptionBalance` is account/wallet metadata (not the two meters); not
  required for the two-meter display but confirms the response is the
  authenticated quota source.

The two meters reset INDEPENDENTLY (7d window vs 5h window), confirming the
design decision: do NOT map frequency limit into `QuotaData.Rolling`. The
provider-specific `KimiQuotaData{Weekly, RateLimit, FetchedAt}` aggregate
reuses `QuotaUsage` leaves.

### Auth credential + replay (OBSERVED + VERIFIED)

- The protected request sends `authorization: Bearer <accessToken>` (a JWT),
  plus browser headers: `x-msh-platform: web`, `x-msh-version: 2.0.0`,
  `x-language: zh-CN`, `r-timezone`, `x-msh-device-id`, `x-traffic-id`,
  `x-msh-session-id`, `x-msh-shield-data`, and a `cookie` header.
- **VERDICT (VERIFIED): plain Go-HTTP replay works.** A Go client replaying the
  captured Bearer token + cookie + browser headers against
  `GetSubscriptionStats` returned HTTP 200 with both meters present — no bot
  block. So `KimiQuerier` stays pure Go-HTTP (no CDP fetch needed), matching
  `DeepSeekWebQuerier`.
- The replay envelope must carry: the Bearer accessToken, the cookie header,
  and the stable browser headers (`x-msh-device-id`, `x-msh-traffic-id`,
  `x-msh-platform`, `x-msh-version`, `x-language`, `r-timezone`,
  `user-agent`). `x-msh-session-id` / `x-msh-shield-data` are per-session/risk
  tokens; the replay probe succeeded even with a session-specific shield, so
  they are best-effort (sent when present, not fatal if absent). The envelope
  allowlist is CLOSED: only these named fields are persisted; unknown captured
  state is rejected at capture time.

### Add-on ("购买加油包") destination (OBSERVED)

- Console link target (OBSERVED in the SPA bundle): the membership/booster
  page on `www.kimi.com`. Canonical destination:
  `https://www.kimi.com/membership/subscription?tab=quota&from=kfc_console_booster`
  (HTTPS, host/path allowlisted). The add-on action opens this for the user
  WITHOUT submitting a purchase.

## Synthetic fixtures (task 1.3)

Synthetic fixtures mirror the REAL captured structure (field names are real;
values are synthetic, NOT captured) and are used only for parser tests:

- Weekly: `ratelimitCode7d.ratio = 0.10`, `resetTime` 6d ahead → 562800s → "6d".
- Frequency limit: `ratelimitCode5h.ratio = 0.52`, `resetTime` 3h 20min ahead
  → 12000s → "3h". (At real 0% the ratio field is absent; the fixture
  exercises the >0 path; a separate fixture exercises the absent-ratio=0%
  path.)

The parser tests assert the two meters retain independent percentage,
seconds, and compact display values via `formatter.FormatDurationCompact`.

## Acceptance status

- 1.2 (real capture): DONE — real authenticated session captured, sanitized
  structure recorded, Go-HTTP replay verified.
- 6.4 (real-browser acceptance with the canonical build): PARTIAL, honestly
  reported.
  - `login-kimi` (canonical nogui build): DONE — a fresh isolated browser
    captured a 9-field envelope (Bearer token + cookie + 7 browser headers)
    and saved the account.
  - `quota-kimi` (canonical nogui build, real Kimi server): DONE — printed
    本周用量 10% reset in 6d / 频率限制 0% reset in 3h, matching the visible
    console (10% / 0%). The pure-Go-HTTP querier is fully verified.
  - Account page + add-on page (open-page kimi / kimi-addon): NOT YET
    AUTHENTICATED. Cookie replay injected 6 cookies, the browser navigated,
    and it did NOT flash-close (the failAndWait→signalOpenPageError→Wait
    contract held — the window stayed open). But the protected
    GetSubscriptionStats response was not observed within the 8s settle
    window, so the page flow signalled an error instead of ready. Root cause:
    Kimi's console is a client-side SPA that needs the Bearer token +
    browser headers (or localStorage) replayed, not just cookies — and the
    access token has a ~15-minute lifetime, so a saved token is expired by
    the time open-page runs. The DeepSeek account-page replays storage
    (userToken) via a document-start script; the Kimi equivalent must replay
    the accessToken into the page context (and/or fetch via the CDP page
    context with the saved headers). This is the remaining real work for the
    account-page path. No flash-close / no profile leak confirmed.

### Out-of-range ratio + strict host/path (RED fixes)

- The parser previously CLAMPED out-of-range `ratio` to 0..100; it now
  REJECTS ratio < 0 or > 1 (an out-of-range ratio is a malformed/unsupported
  response, never a silently-clamped 100%). RED test added.
- The querier now enforces a CLOSED host allowlist (`www.kimi.com` only): a
  BaseURL on an unapproved host is rejected before the Bearer token is sent,
  and the endpoint path is the fixed OBSERVED protected path (never derived
  from BaseURL). RED tests added (non-Kimi host rejected; exact protected
  path asserted).

### Leaked-JWT handling

The Bearer JWT printed by the chrome-devtools get_network_request tool had
exp=1785311245 (2026-07-29 15:47:25 UTC), a ~15-minute lifetime. It is now
expired (verified) and was never recorded, committed, or persisted — no
occurrence in repo files or git history; captured response files were
shredded; throwaway config shredded after acceptance. The token cannot be
replayed.