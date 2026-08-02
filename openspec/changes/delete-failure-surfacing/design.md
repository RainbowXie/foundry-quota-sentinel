## Context

The account-delete flow in `internal/web/static/sidebar.html` confirms via a
modal (`#confirmModal`, `#confirmText`, `#confirmOk`) and calls
`/api/delete?provider=…&name=…`. The server returns a consistent
`{success: bool, error: string}` envelope with three failure modes (bad
provider, delete not supported, `onDelete` storage/state error). The current
frontend `.then()` ignores the body entirely: it closes the modal and
refreshes the deleted provider's cards as if deletion succeeded. This is the
pre-existing gap recorded as a non-blocking follow-up in the archived
`unify-quota-card-template` change.

The archived change already hardened the success path: the request and the
post-response refresh both use a confirm-time snapshot (`deletedProvider` /
`deletedName`) so a concurrently-opened delete confirm for another provider
cannot redirect the refresh, and only the deleted provider's container is
refreshed (no redundant fetches). This change builds on that snapshot
contract without weakening it.

## Goals / Non-Goals

**Goals:**
- Parse the `/api/delete` JSON response and branch on `success`.
- On failure: surface the server error text in the confirm modal, keep the
  modal open, and do NOT refresh any provider (nothing was deleted).
- On success: preserve the existing behavior exactly — modal closes, only
  the deleted provider's container refreshes from the confirm-time snapshot.
- Cover both paths with the stub-DOM delete-flow regression harness so the
  response parsing cannot regress silently.

**Non-Goals:**
- No server-side changes: `/api/delete` already returns the right envelope
  and error strings.
- No retry/undo, no toast system, no blocking of the context-menu delete
  affordance.
- No change to the Add Account dialog, login flows, or other error
  presentation sites.

## Decisions

### Reuse the confirm modal's error region for failure text

The confirm modal already has `#confirmText` (the confirmation question). A
failure SHALL update that same region to show the server error while the
modal stays open, so the user sees why the delete did not happen in the
place they were already looking. This avoids introducing a new toast/alert
surface for a single error case and keeps the interaction in one dialog.
The error text is the server-provided `error` string (already user-facing),
rendered as text (no HTML injection). Because the modal stays open, the
Cancel/✕ affordances still close it, and the user can retry the delete.

### Parse the response and branch before closing, with generation ownership

The handler SHALL capture a confirmation generation, parse the response,
and only act on the modal while that generation still owns it:

```js
// confirmGen owns the modal; deletePending guards double-confirm.
var confirmGen = 0, deletePending = false;

confirmOk.addEventListener("click", function () {
    if (!pend.prov) return;
    if (deletePending) return;          // re-entrancy: no duplicate request
    var gen = ++confirmGen;
    deletePending = true;
    var deletedProvider = pend.prov;    // confirm-time snapshot
    var deletedName = pend.name;
    fetch(url)
        .then(function (r) {
            // JSON parse failure -> null (unknown failure), NOT network error
            return r.json().catch(function () { return null; });
        })
        .then(function (res) {
            if (!res || res.success !== true) {
                if (gen === confirmGen) {         // still owns the modal
                    ctext.textContent = "删除失败：" + ((res && res.error) || "未知错误");
                    deletePending = false;        // retry re-enabled on failure
                }
                return;
            }
            if (gen === confirmGen) {             // close only while owned
                closeConfirm();
                deletePending = false;
            }
            setTimeout(function () {              // refresh NOT gen-gated
                var p = quotaProviderByType(deletedProvider);
                var fn = p ? window[p.refresh] : null;
                if (typeof fn === "function") fn();
            }, 300);
        })
        .catch(function () {                      // real network failure only
            if (gen === confirmGen) {
                ctext.textContent = "删除失败：网络错误";
                deletePending = false;
            }
        });
});
```

Reading the response as JSON and checking `success !== true` (rather than
`=== false`) treats a malformed/absent body as a failure too — the modal
must never close on an uncertain outcome. `delItem` (opening a new confirm)
increments `confirmGen` and resets `deletePending`, so a response for a
superseded confirmation never touches the newer dialog.

### Three independent concerns, each with its own guard

1. **Modal ownership (concurrency).** `confirmGen` increments on every
   dialog open (`delItem`) and every confirm (`confirmOk`). A response only
   closes the modal or writes error text while `gen === confirmGen`. A
   stale in-flight response — success or failure — must not close, rewrite,
   or otherwise operate on a modal that now belongs to another account.
2. **Malformed body vs. network failure.** `r.json()` can reject for two
   unrelated reasons: an unparseable body (server misbehavior) and a
   network failure (transport). Converting the JSON parse rejection to
   `null` routes malformed bodies into the failure branch (`未知错误`),
   leaving the outer `.catch` reserved for real network failures
   (`网络错误`).
3. **Double-confirm re-entrancy.** The server delete is NOT idempotent
   (`internal/config/kimi.go:171` returns an error for an unknown account).
   A double-click on `#confirmOk` must not send two identical requests:
   the second attempt would fail after the first succeeded, and — because
   it owns the newest generation — would overwrite the success outcome
   with `删除失败：account not found`. `deletePending` makes the button
   inert while a request is in flight and re-enables retry only after the
   current generation's response (failure keeps the modal open).

Alternatives considered:

- Fire-and-forget (current): rejected — it is the bug being fixed.
- Parse only `success === false` and treat anything else as success:
  rejected — a malformed body would falsely close the modal.
- Show errors via `alert()`: rejected — inconsistent with the modal-based
  confirm UX and harder to test in the stub-DOM harness.
- Close the modal on failure anyway: rejected — the user loses context and
  the error message.
- Let a stale response close/rewrite the current modal: rejected — it
  would let one account's delete outcome corrupt another account's dialog.

## Risks / Trade-offs

- **[Failure text persists in the question region]** → The modal stays open
  with the error; the user can close via Cancel/✕ or retry. Acceptable and
  explicit. The success path always closes and clears the modal.
- **[Stale response races a newer dialog]** → `confirmGen` ownership guards
  every modal mutation. The failure path no longer claims to be safe merely
  because it "only writes text" — that reasoning was proven wrong (a stale
  failure used to rewrite the newer dialog's confirm text). Ownership is
  enforced explicitly, and both stale-success and stale-failure are covered
  by deferred-promise stub-DOM tests.
- **[Double-confirm sends a duplicate delete]** → `deletePending` blocks
  re-entry while a request is in flight, so exactly one `/api/delete` is
  sent per confirmation; the deterministic test confirms a double-click
  sends one request and ends in the success state.
- **[Regression to the success path]** → The stub-DOM harness keeps
  asserting: success closes the modal, refreshes only `/api/kimi`, and the
  refresh honors the snapshot; failure/malformed/network/stale scenarios
  run the same harness with the respective response shapes.
- **[Server error strings are raw text]** → They are written with
  `textContent`, never `innerHTML`, so no injection; existing `qesc` is not
  needed for text assignment.
