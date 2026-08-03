## ADDED Requirements

### Requirement: Delete failures surface an error without closing the modal
When the sidebar's account-delete confirm flow calls `/api/delete` and the server responds with `success: false` (bad provider, delete not supported, or an `onDelete` storage/state error), the sidebar SHALL keep the confirm modal open and SHALL display the server-provided error message as text in the modal's confirmation region. It MUST NOT close the modal, MUST NOT refresh any provider card, and MUST NOT present the failed delete as a success.

#### Scenario: Server rejects the delete
- **WHEN** the user confirms deleting an account and `/api/delete` responds with `{success: false, error: "delete not supported"}`
- **THEN** the confirm modal remains open showing `删除失败：delete not supported`, no provider card is refreshed, and the user can still close the modal or retry

#### Scenario: Deletion fails at the storage/state layer
- **WHEN** the account exists but `onDelete` returns an error
- **THEN** the modal remains open showing the returned error message and no provider cards are refreshed

#### Scenario: The response is malformed or missing
- **WHEN** the delete request fails at the network level or returns a body that cannot be parsed as the expected envelope
- **THEN** the modal remains open showing a generic `删除失败` error (with `网络错误` for network failure) and no provider cards are refreshed

### Requirement: Delete success preserves the existing refresh behavior
When `/api/delete` responds with `success: true`, the sidebar SHALL close the confirm modal and refresh only the deleted provider's card container, using the provider/name snapshot captured when the delete was confirmed (so a concurrently-opened delete confirm for another provider cannot redirect the refresh). The modal MUST NOT close and MUST NOT refresh on any non-success response.

#### Scenario: Deletion succeeds
- **WHEN** the user confirms deleting a Kimi account and `/api/delete` responds with `{success: true}`
- **THEN** the modal closes and only `/api/kimi` is refetched, even if the user has since opened a delete confirm for another provider

#### Scenario: Another provider's confirm is opened mid-flight
- **WHEN** a Kimi delete is in flight and the user opens a delete confirm for an Ollama account before the response arrives
- **THEN** the Kimi delete's success path still refreshes `/api/kimi` (from the confirm-time snapshot) and does not refresh Ollama
