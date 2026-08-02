# Evidence — delete-failure-surfacing

Implementation evidence for the account-delete failure surfacing change.
The sidebar's `/api/delete` confirm flow previously ignored the server
response: the modal closed unconditionally, a failed delete looked like a
success, and no error was shown. This change parses the response, surfaces
failures in the confirm modal, and makes responses respect modal ownership
under concurrency.

All edits live in `internal/web/static/sidebar.html` (embedded static
asset) plus `internal/web/delete_flow_test.go`; zero production Go changes
(the `/api/delete` server handler already returns a consistent
`{success, error}` envelope).

## Task 1.1/1.2 — RED characterization (pre-change flow)

The pre-change handler was `fetch(...).then(function () { closeConfirm();
setTimeout(refresh, 300); }).catch(function () { closeConfirm(); })` — it
ignored the body entirely, closed the modal on any outcome, and never
showed an error. RED tests (all executed via the stub-DOM node harness that
evals the real inline `<script>` block):

- `TestDeleteFailureKeepsModalOpenAndShowsError` — FAIL before the change
  (modal closed, no error text on `{success:false}`).
- `TestDeleteNetworkFailureKeepsModalOpenAndShowsError` — FAIL before the
  change (network rejection also closed the modal silently).
- `TestDeleteMalformedBodyShowsUnknownError` — FAIL before the change
  (unparseable body was not distinguished from network failure).
- `TestStaleDeleteSuccessDoesNotTouchNewerModal` /
  `TestStaleDeleteFailureDoesNotTouchNewerModal` — FAIL before the change
  (an in-flight response operated on whichever modal was current, closing
  or rewriting a newer confirmation for another account).
- `TestDeleteFlowRefreshesOnlyDeletedProvider` — PASS before the change
  (archived unify-quota-card-template contract: confirm label, snapshot
  refresh, race-safe refresh).

## GREEN + verification

Implementation (`internal/web/static/sidebar.html`, `#confirmOk` handler
and `delItem` listener):

- Modal ownership by `pend` account matching: a response only closes the
  modal or writes error text while the currently-open dialog still shows
  the SAME account as the request (`pend` equals the confirm-time
  snapshot). A stale response for a different account never touches that
  account's dialog; a reopened same-account dialog is still owned by the
  original response (success closes it).
- `inFlightDeletes` per-account guard keyed by `provider\u0000name`: while
  an account's delete is in flight, a second confirm for the SAME account
  — double-click, or close-and-reopen of that account's dialog — is inert.
  The server delete is not idempotent (`internal/config/kimi.go:171`
  returns an error for an unknown account), so a duplicate would fail after
  the first succeeds and wrongly overwrite the success with
  `删除失败：account not found`. Different accounts remain independently
  deletable. The entry is removed at outcome-specific points: failures
  release immediately (retry allowed); SUCCESS holds the guard until the
  provider refresh SETTLES so the old card has left the DOM before the
  same account can be deleted again (releasing on success-response arrival
  would let a reopen-between-response-and-refresh send a second delete that
  fails with account-not-found). At refresh settle the guard release ALSO
  closes and invalidates a stale dialog that still points at the deleted
  account (`pend` matches → `closeConfirm()` + clear `pend`), so its
  confirm action is inert and cannot delete the now-missing account; a
  dialog for a different account is untouched, and a recreated same-named
  account gets a fresh `pend` via `delItem` so it remains deletable.
  `delItem` deliberately does NOT clear
  in-flight state — a dialog-level boolean reset on open was the
  close-and-reopen bypass.
  Modal mutations (close, error text) are gated by `pend` account matching:
  the response only touches the dialog while it still shows the same
  account as the request, and the ORIGINAL success closes a reopened
  same-account dialog (no stale confirm box left behind).
- Response parsing: `.then(r => r.json().catch(() => null))` converts a
  JSON parse failure to `null` so the failure branch reports
  `删除失败：未知错误`; the outer `.catch` is reserved for real network
  failures and reports `删除失败：网络错误`. Both keep the modal open and
  refresh nothing.
- Success: closes the modal only while owned, and always refreshes ONLY
  the deleted provider's container from the confirm-time snapshot
  (`deletedProvider`), regardless of which dialog is current — a
  server-confirmed delete must still remove the card even if the user has
  moved on. No unrelated provider fetches.
- `#confirmText` is written via `textContent` (no HTML injection).

Verification:

- Focused: all 10 delete-flow scenarios GREEN via the stub-DOM harness
  (success / failure / network / malformed / stale-success / stale-failure /
  double-confirm / reopen-same-account / reopen-before-refresh /
  recreated-account-deletable-again).
- Full `internal/web` suite: 79 tests green. `go test ./...` green.
- Touched Go files `gofmt`-clean (repo-wide `gofmt -l` still lists 5
  pre-existing historical files this change does not touch:
  `internal/formatter/format.go`, `internal/quota/deepseek.go`,
  `internal/quota/opencode.go`, `internal/quota/types.go`,
  `internal/storage/reader.go`). `go test -race -tags nogui ./...` green;
  `go vet` (default AND `-tags nogui`) clean; GUI/nogui `go build` clean;
  `git diff --check` clean; `openspec validate --all` valid (6 items).
- 3.4 visual (Chrome DevTools, isolated harness with stubbed fetch):
  - Failure: confirm text becomes `删除失败：delete not supported`, modal
    stays open, no delete-triggered refresh.
  - Network: `删除失败：网络错误`, modal stays open, no refresh.
  - Malformed body: `删除失败：未知错误` (NOT 网络错误), modal stays open.
  - Stale success (reviewer repro): confirm Kimi A (deferred), close its
    modal, open Ollama B confirm, then resolve A → B's modal stays open
    with untouched text, yet `/api/kimi` still refreshes (snapshot).
  - Stale failure: B's modal and text untouched, error not written to B,
    no refresh.
  - Success: modal closes, `/api/delete` fires, only `/api/kimi`
    refreshes, no other provider fetches.
  - No JS console errors (only optional font/favicon 404s in the harness).

## Follow-up (not in this change)

None. The concurrency ownership, malformed-body semantics, and success
snapshot refresh are all covered by deterministic stub-DOM tests plus real
browser reproduction of the reviewer's exact stale-response scenarios.
