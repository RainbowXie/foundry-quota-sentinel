## Why

The account-delete confirmation flow currently ignores the server response:
`/api/delete` can fail (bad provider, delete not supported, or an
`onDelete` storage/state error), but the frontend closes the confirm modal
regardless and never shows the failure. A failed delete therefore looks like
a success — the account card remains, and the user has no idea why. This is
the pre-existing non-goal gap recorded as a follow-up in the archived
`unify-quota-card-template` change.

## What Changes

- Parse the `/api/delete` JSON response in the confirm-flow handler.
- On `success: false`, keep the confirm modal open (or reopen it), display
  the server-provided error message instead of silently closing, and do NOT
  refresh any provider card (nothing was deleted).
- On `success: true`, keep the existing behavior: close the modal, then
  refresh only the deleted provider's container from the confirm-time
  snapshot (`deletedProvider`), preserving the race-condition fix from the
  archived change.
- Keep the server side unchanged: the handler already returns
  `{success, error}` consistently.

## Capabilities

### New Capabilities

- `delete-failure-surfacing`: The sidebar's account-delete confirmation
  flow surfaces server-side deletion failures to the user (error text shown,
  modal stays open, no refresh on failure) while preserving the success path
  (modal closes, deleted provider's cards refresh from the confirm-time
  snapshot).

### Modified Capabilities

None — the behavior is a frontend-only interaction-state fix; no existing
spec-level requirement changes (the archived change explicitly kept delete
wiring "unchanged" as a non-goal, so this is a new capability).

## Impact

- `internal/web/static/sidebar.html` — the `confirmOk` click handler: read
  the response, branch on `success`, surface errors via the existing error
  presentation vocabulary (e.g. the modal's confirm-text region), and only
  refresh on success.
- Tests in `internal/web/delete_flow_test.go` — the stub-DOM delete-flow
  harness currently resolves `{success:true}`; extend it to cover the
  `{success:false, error:...}` path (modal stays open, error shown, no
  provider refresh) and keep the success path assertions.
- No server, parser, DTO, storage, auth, or API contract changes.
