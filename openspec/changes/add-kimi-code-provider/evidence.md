# Kimi Code provider — sanitized evidence note

This note records only endpoint paths, field names/types, event ordering,
status codes, lengths, counts, and redacted/synthetic observations. It
NEVER records live cookie/token/Authorization/storage values, account
identifiers, response bodies, or their hashes.

Evidence confidence is tagged **OBSERVED** (verified by direct probe / bundle
decode, no login) or **EVIDENCE-GATED** (needs one real authenticated
disposable browser session to confirm). The single remaining human action is
listed at the bottom.

## GitNexus impact analysis (task 1.1)

Run before any edit to an existing symbol. All changes are **additive** —
no existing field, route, or JSON shape is altered — so the HIGH/CRITICAL
risk ratings are structural (many transitive callers), not behavioral.

| Symbol | File | Risk | Callers | Change | Why safe |
|--------|------|------|---------|--------|----------|
| `config.Config` | `internal/config/config.go:38` | CRITICAL | Load, SaveWindowSize, init, deleteAccountFromConfig, startSidebar, cmdServe, main | add `KimiAccounts []KimiAccount` field, `json:"kimi_accounts,omitempty"` | omitempty → pre-Kimi configs load with empty list; Save preserves all other fields |
| `quota.QuotaData` | `internal/quota/types.go:12` | CRITICAL | parseOllamaQuota, parseQuotaResponse, FetchQuota, cmdQuota, cmdWatch, cmdLoginOpenCode, Handler | **NOT MODIFIED** | new `KimiQuotaData` aggregate reuses `QuotaUsage` leaves; existing provider JSON untouched |
| `web.Server.Handler` | `internal/web/server.go:136` | HIGH | Start → startSidebar/cmdServe → main | add `/api/kimi`, `/api/kimi/accounts`, `/api/kimi/login` routes inside Handler | existing routes unchanged; new routes are additive |
| `deleteAccountFromConfig` | `main.go:172` | LOW | (onDelete) | add `case "kimi"` → `c.DeleteKimiAccount(name)` | branch addition |
| `cmdOpenPage` | `main.go:452` | LOW | main | add `case "kimi"` branch | branch addition |

No HIGH/CRITICAL edit alters existing behavior; the rating reflects the
call-graph fan-out, not a semantic break. Regression guard: the existing
provider tests (config round-trip, ollama/opencode/deepseek parsers,
sidebar route tests) must stay green.

## Public/structural investigation (task 1.2)

Performed without a logged-in session, over plain HTTPS from a sandbox. No
cookies/tokens/auth values were captured; no user browser profile was read;
no non-loopback DevTools. All findings below are direct observations.

### Console and product surface (OBSERVED)

- Console URL: `https://www.kimi.com/code/console` — confirmed 200, SPA shell
  (`x-render: mode=csr`). Vue 3 + Vite + TanStack Query; auth is a CLIENT-SIDE
  gate (no HTTP redirect for unauthenticated visitors — the server returns
  the same shell to everyone). Entry chunk `index-CNJB2DbG.js`; console UI is
  a lazy chunk `KimiConsole-esHS5unV.js`.
- `https://www.kimi.com/code` is the marketing/landing page (SSR, install
  script `curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash`).
- The `/api` prefix is a legacy Express router that 302s to `/` for `/api`
  and returns 404 `{"error_type":"internal.error",...}` for any `/api/...`
  path. **The console does NOT use REST `/api/...` paths.**

### API protocol and endpoint (OBSERVED)

Kimi's console uses **Buf Connect (connect-es) gRPC-Web over JSON**, not
REST. The path template is:

```
POST https://www.kimi.com/apiv2/{fully.qualified.Service}/{Method}
Content-Type: application/json
Connect-Protocol-Version: 1
Authorization: Bearer <accessToken>   (added by the SPA when logged in)
```

Verified four ways: (1) the only fully-qualified path string in the request
chunk is `"/apiv2/kimi.gateway.config.v1.ConfigService/GetConfig"`; (2) GET
to any `/apiv2/...Service/Method` returns 405 + `allow: POST`; (3) POST `{}`
to the membership endpoints returns 401 `{"code":"unauthenticated",...}`
(route exists, needs auth); (4) all legacy `/api/code/...` paths return 404.

- **Protected quota endpoint (OBSERVED)**:
  `POST https://www.kimi.com/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats`
  with body `{}`. 401 unauthenticated without a Bearer token; 200 with one.
  This is the authenticated quota source — the SPA's `useBalanceModel` calls
  `membershipService.getSubscriptionStats({})`.
- Sibling methods: `GetSubscription`, `ListSubscriptions`
  (`MembershipService`); `PrecheckWalletTopup` (`WalletService`); billing RPCs
  under `kimi.gateway.order.v1.SubscriptionService` (out of scope for
  read-only quota display).
- Auth host (OBSERVED): `https://auth.kimi.com/api` (token refresh / device
  register / logout, used by `AuthService`/`AuthRefreshClient`).

### Business-success discriminator (OBSERVED)

**This is NOT the DeepSeek `code==0` pattern.** Kimi Connect uses a STRING
`code` field for failures:

- Success: HTTP 200 + response body parses as the expected protobuf-JSON
  message (no top-level `code` string).
- Failure: non-2xx HTTP status with `{"code":"unauthenticated"|"permission_denied"|"internal", "details":[...]}`.
  A 401 `unauthenticated` = accessToken invalid/expired (the expired-auth
  signal).

So the parser must reject a body carrying a non-empty `code` string (Connect
error envelope) and accept a body with no `code` field that parses into the
two meters. The HTTP 200 check lives in the querier; the CDP login
auth-decision signal is: `responseReceived` 200 on `GetSubscriptionStats` +
`loadingFinished` + body parses as the quota message (no error `code`). This
is the Kimi analog of DeepSeek's `code==0` body check, inverted.

### Auth credential shape (OBSERVED + GATED)

- OBSERVED: the protected request uses `Authorization: Bearer <accessToken>`
  (same Bearer-header pattern as DeepSeek's `platform.deepseek.com` transport,
  NOT a cookie header like Ollama). The accessToken is what login must
  capture and the querier must replay.
- EVIDENCE-GATED: the minimum replay set — whether the accessToken alone
  suffices or a cookie (e.g. a session cookie on `www.kimi.com`) is also
  required, and whether the User-Agent is session-bound. The capture phase
  determines the envelope allowlist; it starts empty and adds only proven-
  necessary values. The envelope version/encoding is implemented and tested;
  only the allowlist contents are gated.

### Meter fields (OBSERVED proto schema, EVIDENCE-GATED exact 200 layout)

Proto `kimi.gateway.membership.v2.Balance` (decoded from FileDescriptorProto
in chunk `user-BIuo1Cpx.js` — these are the real schema field names, not
guesses): `uuidv8`, `feature`, `type` (enum
`UNSPECIFIED|SUBSCRIPTION|GIFT|BOOSTER`), `unit`, `amount`, `amountLeft`,
`amountUsedRatio`, `kimiCodeUsedRatio`, `expireTime` (google.protobuf.Timestamp),
`upcomingExpiration`, `domain`.

`Capability.Constraint`: `parallelism`, `cronNum`, `projectNum`,
`userCapacity`, `modelContextLength`, `modelretryTimes`, `rateLimits[]`
(each `RateLimit{total, window: Duration}`).

Console UI identifiers (OBSERVED in `KimiConsole` + `balance` + `UsageTabs`
chunks): `rateLimits`, `firstRateLimit`, `usagePercentage`,
`rateLimitPercentage`, `isGlobalQuotaExceeded`, `monthlyChargeLimit`,
`boosterWallets`, `creditUsage`, `limit5hUsage`, `limit7dUsage`,
`ratelimit5h`, `ratelimit7d` — i.e. **two rate-limit windows: 5-hour and
7-day**. i18n keys confirm: `code.console.statsWeeklyUsage`,
`code.console.statsRateLimitDetail`, `code.console.statsResetIn` with
day/hour/minute/second components, `subscription.quota.fivehours`,
`subscription.quota.sevendays`.

Interpretation: the console shows a **weekly usage %** (`statsWeeklyUsage`,
derived from `amountUsedRatio`/`kimiCodeUsedRatio` via `ratioToPercentage`),
a **rate-limit detail** with 5h & 7d windows, a **reset countdown** in
d/h/m/s, and a **booster** (加油包) spend limit. The `%` values are derived
client-side from `*_ratio` proto fields.

- EVIDENCE-GATED: the exact 200-response JSON layout (which Balance fields
  populate, how `rateLimits` serializes, whether the response carries
  pre-computed `usagePercentage`/`rateLimitPercentage` or only ratios). The
  proto field names are solid (connect-es serializes proto→JSON in camelCase
  deterministically); the populated shape needs one real 200 body to confirm.
  The parser uses the observed proto field names; its CONTRACT (two
  independent meters, percentage 0..100, reset in seconds, Connect success
  discriminator) is what the tests pin, so a confirmed layout updates only the
  parser's struct tags, not the tested output.

### Add-on ("购买加油包") destination (OBSERVED — no longer gated)

- OBSERVED link string in the console chunk:
  `https://www.kimi.com/membership/subscription?tab=quota&from=kfc_console_booster`
  (and `?from=kfc_console_upgrade` for upgrade). This is the canonical
  booster/加油包 destination on `www.kimi.com`, HTTPS, host/path allowlisted.
  The add-on action opens this URL for the user without submitting a purchase.

## Synthetic fixtures (task 1.3)

Synthetic fixtures represent the proposal sample values (NOT captured), used
only for parser tests. They model the observed Connect-JSON success shape
(no `code` string on success) with EVIDENCE-GATED meter field names; the
parser's contract is what the tests pin.

- Weekly: usage `10`, reset `6d 12h 20min` = 518400 + 43200 + 1200 = **562800s**.
- Frequency limit: usage `52`, reset `3h 20min` = 10800 + 1200 = **12000s**.
  (3h 20min maps to the remaining time in the observed 5h rate-limit window.)

The parser tests assert the two meters retain independent percentage,
seconds, and compact display values (`562800s` → `6d`, `12000s` → `3h`) via
the shared `formatter.FormatDurationCompact`.

## Remaining human action (task 1.2 completion + 6.4 acceptance)

The non-interactive work is completed against the OBSERVED contract
(endpoint, protocol, Bearer auth, Connect success discriminator, add-on URL,
console URL, proto field names). The ONLY remaining human action is a single
real login in an isolated disposable Kimi browser, to confirm the one
EVIDENCE-GATED item — the exact 200-response JSON layout of
`GetSubscriptionStats`:

1. Run `login-kimi <name>` (canonical build) to open the temporary browser
   at `https://www.kimi.com/code/console`.
2. Complete Kimi authentication manually in that window.
3. The implementation captures the allowlisted auth envelope (Bearer
   accessToken + any proven-necessary cookie), waits for the protected
   `GetSubscriptionStats` 200 response with no error `code`, and saves the
   account. The capture records only field names/ordering/redacted shapes —
   never credential values.
4. Run `quota-kimi <name>` and compare the two meters to the visible console.
5. Open the authenticated console until manual close; open the verified
   add-on page (`/membership/subscription?tab=quota&from=kfc_console_booster`)
   without purchasing.

Before that run, if the real 200 body differs from the inferred layout, only
the parser's struct tags in `internal/quota/kimi.go` and the envelope
allowlist in `internal/config/kimi.go` change — the tested contract and all
non-parser layers stay intact.