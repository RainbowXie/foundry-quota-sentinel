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

### Parse the response and branch before closing, with per-account modal ownership

The handler SHALL track in-flight deletes PER ACCOUNT (provider + name),
parse the response, and only act on the modal while it still shows that
account:

```js
// inFlightDeletes keyed by "provider\u0000name": a per-account guard.
var inFlightDeletes = {};
function deleteKey(provider, name) { return provider + "\u0000" + name; }

confirmOk.addEventListener("click", function () {
    if (!pend.prov) return;
    var deletedProvider = pend.prov;    // confirm-time snapshot
    var deletedName = pend.name;
    var key = deleteKey(deletedProvider, deletedName);
    if (inFlightDeletes[key]) return;   // same account already in flight
    inFlightDeletes[key] = true;
    fetch(url)
        .then(function (r) {
            // JSON parse failure -> null (unknown failure), NOT network error
            return r.json().catch(function () { return null; });
        })
        .then(function (res) {
            // Only touch the modal while it still shows THIS account;
            // pend holds the last opened confirm.
            var ownsDialog = pend.prov === deletedProvider &&
                             pend.name === deletedName;
            if (!res || res.success !== true) {
                delete inFlightDeletes[key];   // failure: release NOW, retry allowed
                if (ownsDialog) {
                    ctext.textContent = "删除失败：" + ((res && res.error) || "未知错误");
                }
                return;
            }
            if (ownsDialog) { closeConfirm(); }
            setTimeout(function () {          // success: hold guard until refresh settles
                var p = quotaProviderByType(deletedProvider);
                var fn = p ? window[p.refresh] : null;
                function release() {
                    // Invalidate a stale dialog still pointing at the
                    // deleted account, then release the guard.
                    if (pend.prov === deletedProvider &&
                        pend.name === deletedName) {
                        closeConfirm();
                        pend.prov = ""; pend.name = "";
                    }
                    delete inFlightDeletes[key];
                }
                if (typeof fn === "function") {
                    Promise.resolve(fn()).then(release, release);
                } else {
                    release();
                }
            }, 300);
        })
        .catch(function () {                      // real network failure only
            delete inFlightDeletes[key];          // release NOW, retry allowed
            if (pend.prov === deletedProvider && pend.name === deletedName) {
                ctext.textContent = "删除失败：网络错误";
            }
        });
});
```

Reading the response as JSON and checking `success !== true` (rather than
`=== false`) treats a malformed/absent body as a failure too — the modal
must never close on an uncertain outcome. `delItem` (opening a confirm
dialog) sets `pend` and shows the modal but MUST NOT clear any in-flight
state — a per-account map, not a dialog-level boolean, is what prevents a
closed-and-reopened same-account dialog from bypassing the guard.

### Guard release timing (lifecycle)

The per-account guard is released at DIFFERENT points depending on the
outcome:

- **Failure / network error / malformed body:** release immediately on the
  response, so the user can retry the delete right away (nothing was
  deleted).
- **Success:** hold the guard until the provider refresh SETTLES (the
  async refresh promise resolves or rejects after the 300ms timer), so the
  old card has left the DOM before the same account can be deleted again.
  Releasing on success-response arrival would reopen the race: the user
  could confirm the same account again between the success response and the
  refresh, sending a second `/api/delete` that fails with
  account-not-found and wrongly overwrites the success. The refresh is
  always issued from the confirm-time snapshot regardless of which dialog
  is current, and the guard release does not depend on modal ownership.
- **Refresh settle also invalidates a stale dialog.** Simply releasing the
  guard at refresh settle is not enough: a confirm dialog reopened for the
  SAME account before the refresh ran is still open and its `pend` still
  points at the deleted account. Releasing the guard alone would let the
  user confirm that stale dialog and delete a now-missing account. The
  release therefore FIRST checks `pend` — if it still matches the deleted
  provider+name, close the dialog and clear `pend` (making its confirm
  action inert), THEN release the guard. A dialog for a different account
  is untouched. A recreated same-named account is NOT blocked: the
  re-appearing card's `delItem` opens a fresh confirm that resets `pend`,
  so the recreated account is deletable again.

### Three independent concerns, each with its own guard

1. **Modal ownership (concurrency).** A response only closes the modal or
   writes error text while the currently-open dialog still shows the SAME
   account as the response (`pend` matches the confirm-time snapshot). A
   stale in-flight response — success or failure — must not close, rewrite,
   or otherwise operate on a modal that now belongs to a different account.
   If the SAME account is reopened before its response arrives, the response
   still owns that account's dialog: success closes it (no stale confirm
   box left behind), failure shows the error there.
2. **Malformed body vs. network failure.** `r.json()` can reject for two
   unrelated reasons: an unparseable body (server misbehavior) and a
   network failure (transport). Converting the JSON parse rejection to
   `null` routes malformed bodies into the failure branch (`未知错误`),
   leaving the outer `.catch` reserved for real network failures
   (`网络错误`).
3. **Per-account in-flight guard (idempotency).** The server delete is NOT
   idempotent (`internal/config/kimi.go:171` returns an error for an
   unknown account). A second confirm for the SAME account — double-click,
   or close-and-reopen of that account's dialog — must not send a duplicate
   request: the second attempt would fail after the first succeeded and
   would wrongly overwrite the success outcome with
   `删除失败：account not found`. The guard is keyed by provider+name, so
   different accounts remain independently deletable while the same
   account is blocked until the outcome's release point: a FAILURE releases
   the guard immediately (retry allowed), a SUCCESS holds it until the
   refresh settles and the stale dialog is invalidated (then a recreated
   same-named account may be deleted again via a fresh confirm).
   A dialog-level boolean is deliberately NOT used: resetting it on every
   dialog open is exactly how the close-and-reopen bypass worked.

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
- **[Stale response races a newer dialog]** → Modal mutations are gated by
  `pend` account matching (the response only touches the dialog while it
  still shows the same account). The failure path no longer claims to be
  safe merely because it "only writes text" — that reasoning was proven
  wrong (a stale failure used to rewrite the newer dialog's confirm text).
  Ownership is enforced explicitly, and both stale-success and
  stale-failure are covered by deferred-promise stub-DOM tests.
- **[Double-confirm / close-and-reopen sends a duplicate delete]** → The
  per-account `inFlightDeletes` map blocks a second request for the SAME
  account while one is in flight, so exactly one `/api/delete` is sent per
  account confirmation; the deterministic tests confirm both a double-click
  and a close-and-reopen of the same account send one request and end in
  the success state.
- **[Regression to the success path]** → The stub-DOM harness keeps
  asserting: success closes the modal, refreshes only `/api/kimi`, and the
  refresh honors the snapshot; failure/malformed/network/stale scenarios
  run the same harness with the respective response shapes.
- **[Server error strings are raw text]** → They are written with
  `textContent`, never `innerHTML`, so no injection; existing `qesc` is not
  needed for text assignment.
