# Kimi Code provider — sanitized evidence note

This note records only endpoint paths, field names/types, event ordering,
status codes, lengths, counts, and redacted observations. It NEVER records
live access/refresh tokens, cookies, Authorization/storage values, account
identifiers, response bodies, or their hashes. The contract below was
captured from a REAL authenticated disposable browser session (task 1.2) on
the membership quota page and the live refresh flow, and verified by plain
Go-HTTP replay.

## GitNexus impact analysis (task 1.1)

Run before any edit to an existing symbol. All code changes are **additive**
or **provider-scoped Kimi replacements** — no existing OpenCode/DeepSeek/Ollama
field, route, or JSON shape is altered — so the HIGH/CRITICAL risk ratings are
structural (many transitive callers), not behavioral.

| Symbol | File | Risk | Change | Why safe |
|--------|------|------|--------|----------|
| `config.Config` | `internal/config/config.go:38` | CRITICAL | Kimi field already additive | backward-compatible load |
| `quota.QuotaData` / `QuotaUsage` | `internal/quota/types.go` | CRITICAL | NOT MODIFIED | Kimi uses its own decimal leaf + aggregate |
| `web.Server.Handler` | `internal/web/server.go:136` | HIGH | existing `/api/kimi*` routes updated to three-metric | existing routes unchanged in shape |
| `main.go` CLI | `main.go` | LOW | `kimi` branches + `printKimiQuota` three-metric | branch/text update |

Regression guard: existing provider tests must stay green.

## Real authenticated capture (task 1.2)

Performed via a real disposable browser session. The user authenticated by
SMS; the SPA served `https://www.kimi.com/membership/subscription?tab=quota`;
the protected membership-statistics request fired. No credentials, response
bodies, or account identifiers were recorded — only paths, field names/types,
status codes, and lengths. The live refresh flow was captured separately by
removing the access token and reloading (the SPA auto-refreshed).

### Membership data page (OBSERVED)

- Authoritative data page: `https://www.kimi.com/membership/subscription?tab=quota`
  (NOT `/code/console`). Authenticated SPA shows three metrics with decimal
  percentages and absolute reset times in the page's local timezone
  (Asia/Shanghai, UTC+8):
  - 总使用量 (total usage): `2.19%`, reset `2026-08-27 后重置`.
  - 5 小时用量 (5-hour Code usage): `0%`, reset `07-29 19:58 后重置`.
  - 7 天用量 (7-day Code usage): `10.42%`, reset `08-04 23:58 后重置`.

### API protocol + protected endpoint (OBSERVED)

Kimi uses **Buf Connect gRPC-Web over JSON**, NOT REST `/api/*` (those 404).

- Protected quota endpoint (OBSERVED, returned 200 authenticated):
  `POST https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats`
  with body `{}`, `content-type: application/json`,
  `connect-protocol-version: 1`.
- Subscription metadata endpoint (OBSERVED): `POST .../MembershipService/GetSubscription`
  (carries `subscription.currentEndTime` = total-cycle end; equals
  `subscriptionBalance.expireTime` in the stats response).
- Auth host: `https://auth.kimi.com/api` (login + refresh).

### Success discriminator (OBSERVED)

Connect-JSON: success = HTTP 200 + a response body with NO top-level `code`
string. Failure (unauthenticated) is a non-2xx with
`{"code":"unauthenticated",...}`. Confirmed: the captured 200 body has no
`code` field; a protected call with a cleared access token returns 401.

### Three-metric response structure (OBSERVED — the fields the parser binds to)

`GetSubscriptionStats` 200 body top-level keys (exactly three):

```
ratelimitCode5h:    { enabled: bool, resetTime: string(ISO-8601) }   // ratio ABSENT at 0%
ratelimitCode7d:    { ratio: number(0..1), enabled: bool, resetTime: string(ISO-8601) }
subscriptionBalance: { ..., amountUsedRatio: number(0..1), kimiCodeUsedRatio: number, expireTime: string(ISO-8601) }
```

Field-to-label map (verified against the visible membership page):

- **总使用量 (total)** = `subscriptionBalance.amountUsedRatio` (0..1) × 100.
  Captured `0.0219` → page `2.19%`. Reset =
  `subscriptionBalance.expireTime` (absolute ISO, e.g.
  `2026-08-28T00:00:00Z`); page renders the cycle-end date `2026-08-27`.
- **5 小时用量 (5-hour)** = `ratelimitCode5h.ratio` × 100, with an ABSENT
  ratio treated as `0%` (the observed zero-use shape: the 5h object carries
  only `enabled` + `resetTime` when usage is 0). Captured ratio absent →
  page `0%`. Reset = `ratelimitCode5h.resetTime` (absolute ISO, e.g.
  `2026-07-29T11:58:03.964...Z` → Shanghai `07-29 19:58`).
- **7 天用量 (7-day)** = `ratelimitCode7d.ratio` (0..1) × 100. Captured
  `0.1042` → page `10.42%`. Reset = `ratelimitCode7d.resetTime` (absolute
  ISO, e.g. `2026-08-04T15:58:02.964...Z` → Shanghai `08-04 23:58`).

Key facts:
- Percentages are **decimal** (2.19, 10.42), NOT integers — the data model
  must preserve fractional precision. `amountUsedRatio`/`ratio` are 0..1
  numbers; percentage = ratio×100. Display: up to 2 decimals, trim trailing
  zeros (`2.19%`, `10.42%`, `0%`).
- `resetTime`/`expireTime` are **absolute future ISO-8601 timestamps**
  (nanosecond precision). The reset countdown is `resetAt − now` seconds;
  the display is the timestamp rendered in Asia/Shanghai local time:
  total as `YYYY-MM-DD`, 5h/7d as `MM-DD HH:mm`.
- The three resets are INDEPENDENT absolute instants — one is never reused
  for another. Past/negative/missing/unparseable resets are rejected.
- ratio only allows finite 0..1; NaN/Infinity/>1/<0 must be rejected
  (never clamped).
- `subscriptionBalance` carries `amountUsedRatio`, `kimiCodeUsedRatio`
  (both 0.0219 for this account — Kimi Code usage equals total here),
  `expireTime`, plus `type=SUBSCRIPTION`, `feature=FEATURE_OMNI`,
  `unit=UNIT_CREDIT`, `domain=DOMAIN_NEXUS` (metadata, not meters).

### Durable refresh contract (OBSERVED — the chain the querier must replay)

The access token is short-lived (~900s / 15 min). The durable session is a
**refresh_token** stored in `localStorage["refresh_token"]` (a 607-char JWT);
`access_token` is `localStorage["access_token"]` (606-char JWT). The observed
auto-refresh flow (triggered by clearing the access token and reloading):

1. A protected call (`.../FeedService/ListFeeds`, `.../GetCurrentUser`)
   returns **401** with the stale access token.
2. The SPA calls `POST https://auth.kimi.com/api/account.gateway.v1.AuthService/RefreshToken`
   with request body `{"refresh_token": "<607-char JWT>"}` and headers
   `connect-protocol-version: 1`, `content-type: application/json`,
   `x-msh-device-id`, `x-traffic-id`, `x-msh-platform: web`, `x-msh-version`,
   `r-timezone`, `user-agent` (NO `authorization: Bearer` header — the
   refresh_token is in the body). Returns **200** with
   `{"accessToken": "<606>", "refreshToken": "<607>"}`.
3. **Both tokens rotate.** The response carries a NEW access token AND a NEW
   refresh token; the SPA stores both back into localStorage and retries the
   protected call → 200.

Production refresh contract for the Go querier:
- On 401 (or Connect `unauthenticated`), call `RefreshToken` with the saved
  `refresh_token`, persist the rotated `accessToken` + `refreshToken`
  atomically for that account, then retry the quota call once.
- Refresh is account-scoped and serialized (a per-account refresh mutex) so
  concurrent card/CLI requests cannot race rotation; a stale refresh result
  must not overwrite a newer rotated session.
- A failed refresh preserves the last saved envelope and reports a
  re-login-required state; it never emits the refresh token in errors.

### Auth credential + replay (OBSERVED + VERIFIED)

- The protected quota request sends `authorization: Bearer <accessToken>`
  plus browser headers: `x-msh-platform: web`, `x-msh-version: 2.0.0`,
  `x-language: zh-CN`, `r-timezone: Asia/Shanghai`, `x-msh-device-id`,
  `x-traffic-id`, `x-msh-session-id`, `x-msh-shield-data`, and a `cookie`
  header.
- **VERIFIED: plain Go-HTTP replay works.** A Go client replaying the Bearer
  token + cookie + browser headers against `GetSubscriptionStats` returned
  200 with all three metrics. So `KimiQuerier` stays pure Go-HTTP; refresh is
  also a plain Go-HTTP POST to `auth.kimi.com`.
- The durable envelope must carry: the `access_token` (Bearer), the
  `refresh_token` (durable), the `cookie` header, and the stable browser
  headers (`x-msh-device-id`, `x-traffic-id`, `x-msh-platform`,
  `x-msh-version`, `x-language`, `r-timezone`, `user-agent`). `x-msh-session-id`
  / `x-msh-shield-data` are per-session/risk tokens (best-effort). The envelope
  allowlist is CLOSED: only these named fields persist; unknown captured state
  is rejected at capture time.
- SPA replay for the account page: the membership SPA reads
  `localStorage["access_token"]` + `localStorage["refresh_token"]` to make
  authenticated calls. Restoring only cookies is NOT enough (confirmed: a
  cookie-only account page did not trigger the protected response). The
  account-page replay must install `access_token` + `refresh_token` into
  localStorage at document start (DeepSeek-style storage restore), then
  navigate to the membership quota page; the SPA then refreshes if needed and
  fires `GetSubscriptionStats`.

### Membership page as account/details page (OBSERVED)

- The membership quota page itself contains the user-controlled booster /
  purchase UI (额度加油包, 升级订阅). The application only NAVIGATES to
  `https://www.kimi.com/membership/subscription?tab=quota` and never clicks,
  submits, or invokes purchase/booster/subscription/payment actions. No
  separate automated purchase route is needed or desired. `/code/console` is
  no longer the account/data page for this provider.

## Synthetic fixtures (task 1.3)

Synthetic fixtures mirror the REAL captured structure (field names are real;
values are synthetic, NOT captured) and are used only for parser tests. All
reset timestamps are built at test time so they are always future. The
representative case matches the user's acceptance sample:

- 总使用量: `subscriptionBalance.amountUsedRatio = 0.0219` → `2.19%`,
  `expireTime` ~30d ahead → page date.
- 5 小时用量: `ratelimitCode5h` with ABSENT ratio → `0%`, `resetTime` ~5h
  ahead → `MM-DD HH:mm`.
- 7 天用量: `ratelimitCode7d.ratio = 0.1042` → `10.42%`, `resetTime` ~7d
  ahead → `MM-DD HH:mm`.

Parser tests assert the three metrics preserve decimal percentages and
independent absolute reset instants.

## Leaked-JWT handling

Earlier `get_network_request` tool outputs printed live Bearer JWTs. Those
tokens have a ~15-minute lifetime and are now expired; they were never
recorded, committed, or persisted (0 occurrences in repo files or git
history; captured response/request files were shredded). The tokens cannot
be replayed. This capture's files were likewise shredded immediately after
sanitized field extraction.