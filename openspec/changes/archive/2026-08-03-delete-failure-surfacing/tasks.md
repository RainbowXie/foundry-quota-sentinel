## 1. RED Characterization (current flow)

- [x] 1.1 Add a RED stub-DOM test driving the delete-confirm flow against a `{success: false, error: "delete not supported"}` response, proving the current flow fails the new contract: modal closes and no error is surfaced (test must FAIL against the pre-change handler).
- [x] 1.2 Add a RED stub-DOM test proving the confirm modal stays open and shows the server error text on failure (fails against current code that closes unconditionally).
- [x] 1.3 Extend the existing success-path stub-DOM test (`TestDeleteFlowRefreshesOnlyDeletedProvider`) so it still passes after the change: `{success: true}` closes the modal, refreshes only the deleted provider's container from the confirm-time snapshot, and the race scenario (Ollama confirm opened mid-flight) still refreshes `/api/kimi`.

## 2. Implement failure surfacing

- [x] 2.1 Update the `#confirmOk` click handler in `internal/web/static/sidebar.html` to read the `/api/delete` response as JSON and branch on `success !== true`: on failure, write `删除失败：<error>` (or `网络错误` for network failure) into `#confirmText` via `textContent`, keep the modal open, and skip the refresh; on success, keep the existing close + snapshot-scoped refresh behavior. Keep the confirm-time `deletedProvider`/`deletedName` snapshot captured before the fetch on the success path only.
- [x] 2.2 Keep the handler's `.catch` path non-closing: a network failure shows the generic delete-failure text in the open modal instead of silently closing.

## 3. Verification

- [x] 3.1 Run the focused delete-flow tests and confirm the section-1 RED tests are GREEN (failure keeps modal open + shows error; malformed body treated as failure; success path unchanged with snapshot race assertion).
- [x] 3.2 Run the existing provider sidebar/API regression suites and prove delete wiring, account-page routing, refresh cadence, and all other provider flows remain unchanged.
- [x] 3.3 Run `gofmt` on touched files, `go test ./...`, `go test -race -tags nogui ./...`, default/nogui `go vet`, GUI/nogui builds, and strict OpenSpec change/spec validation.
- [x] 3.4 Inspect the delete-confirm flow in a browser: failure keeps the modal open with the server error text; success closes it and refreshes only the deleted provider's card; no console errors.
