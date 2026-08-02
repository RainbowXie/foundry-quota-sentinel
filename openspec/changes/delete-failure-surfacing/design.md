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

### Parse the response and branch before closing

The handler SHALL do:

```js
fetch(url)
    .then(function (r) { return r.json(); })
    .then(function (res) {
        if (!res || res.success !== true) {
            // failure: show error, keep modal open, no refresh
            ctext.textContent =
                "删除失败：" + ((res && res.error) || "未知错误");
            return;
        }
        closeConfirm();
        setTimeout(function () {
            var p = quotaProviderByType(deletedProvider);
            var fn = p ? window[p.refresh] : null;
            if (typeof fn === "function") fn();
        }, 300);
    })
    .catch(function () {
        ctext.textContent = "删除失败：网络错误";
    });
```

Reading the response as JSON and checking `success !== true` (rather than
`=== false`) treats a malformed/absent body as a failure too — the modal
must never close on an uncertain outcome. The confirm-time snapshot is
captured before the fetch and used only on the success branch, preserving
the archived race-condition fix.

Alternatives considered:

- Fire-and-forget (current): rejected — it is the bug being fixed.
- Parse only `success === false` and treat anything else as success:
  rejected — a malformed body would falsely close the modal.
- Show errors via `alert()`: rejected — inconsistent with the modal-based
  confirm UX and harder to test in the stub-DOM harness.
- Close the modal on failure anyway: rejected — the user loses context and
  the error message.

## Risks / Trade-offs

- **[Failure text persists in the question region]** → The modal stays open
  with the error; the user can close via Cancel/✕ or retry. Acceptable and
  explicit. The success path always closes and clears the modal.
- **[Race with a second delete confirm]** → The confirm-time snapshot
  already decouples the in-flight request from `pend`; the failure path
  only writes text (no refresh), so it is safe under concurrent opens.
- **[Regression to the success path]** → The stub-DOM harness keeps
  asserting: success closes the modal, refreshes only `/api/kimi`, and the
  refresh honors the snapshot; new failure-path assertions run the same
  harness with a `{success:false}` response.
- **[Server error strings are raw text]** → They are written with
  `textContent`, never `innerHTML`, so no injection; existing `qesc` is not
  needed for text assignment.
