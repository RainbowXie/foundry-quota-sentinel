# Ollama Provider Design

## Goal

Add Ollama Cloud account monitoring to the desktop sidebar. A user can add one or more Ollama accounts through the existing `+` flow, complete sign-in in a WebView, and see Session and Weekly cloud-usage limits on cards that behave like OpenCode Go cards.

## Scope

- Add Ollama as a third provider in the account picker.
- Open an Ollama sign-in WebView, capture the authenticated Ollama cookie jar, and validate it before saving an account.
- Fetch and display Session and Weekly usage, including reset times.
- Support opening the authenticated Ollama settings page and deleting an Ollama account from the card context menu.
- Preserve existing OpenCode Go and DeepSeek behavior.

The feature does not add CLI quota/watch commands for Ollama, per-model usage charts, extra-usage billing controls, or manual cookie entry.

## Data and authentication

`Config` gains an independent `OllamaAccounts []OllamaAccount` collection. Each entry contains an account name and the complete `.ollama.com` cookie string. The configuration file is already written with mode `0600`; the UI and logs must never display cookie values.

The HTTP client sends the saved cookie in a request to `https://ollama.com/settings`. `__Secure-session` is the essential authentication cookie; the provider stores the complete host cookie string captured by WebKit so that the settings page can be reopened reliably. A successful parse of both usage meters validates a newly captured credential.

## Usage retrieval

Ollama does not expose a public account-quota API. The authenticated settings page is server-rendered HTML and contains:

- `aria-label="Session usage <percent>% used"`, followed by a reset element with `data-time="<RFC3339 timestamp>"`.
- `aria-label="Weekly usage <percent>% used"`, followed by its own reset `data-time` element.

`OllamaQuerier.FetchQuota` performs the request with a bounded timeout, rejects non-2xx responses, parses both meter/reset pairs, and maps them to the existing `QuotaData` shape: Session is shown as `Rolling`; Weekly is shown as `Weekly`; `Monthly` remains absent. Reset timestamps are formatted through the existing duration formatter.

## Login and account page

On Linux GUI builds, a new `RunOllamaLogin` uses the established WebKit cookie-storage technique from OpenCode:

1. Create a temporary Netscape-format cookie file and attach it to the WebView before navigation.
2. Navigate to Ollama sign-in.
3. Poll the cookie file after navigation and validate the extracted `.ollama.com` cookie string via `OllamaQuerier`.
4. On success, close the window and return the cookie string; otherwise return a clear cancellation/error message when the user closes it.

`RunOllamaPage` rehydrates that cookie string into a temporary WebKit cookie store and opens `https://ollama.com/settings`. Non-GUI builds expose matching stubs that explain the limitation. Platform-specific fallbacks follow the established OpenCode conventions.

## Sidebar and local API

`web.Server` receives an Ollama account provider and supplies:

- `GET /api/ollama` — concurrently queries each Ollama account and returns `{name, success, quota, error}` cards sorted by name.
- `GET /api/ollama/login?name=...` — spawns `login-ollama`.
- Extended `/api/open` and `/api/delete` provider validation for `ollama`.

The add-account modal presents Ollama alongside OpenCode Go and DeepSeek. The frontend renders a provider-labelled Ollama card using the same quota-row components as OpenCode Go, with `Session` and `Weekly` labels. Right-click actions use the existing generic context-menu flow.

## Error handling

- A failed settings request or parse makes only that card show its error and a “re-login” action.
- Login saves an account only after a live settings-page validation succeeds.
- Missing usage markers are an explicit parsing error, not interpreted as zero usage.
- No credentials appear in JSON responses, rendered HTML, standard output, or errors.

## Tests

Unit tests cover successful Ollama settings-page parsing, missing meter rejection, invalid percentage/timestamp rejection, and correct HTTP cookie usage. Configuration tests cover upsert/delete semantics. Server tests cover the Ollama cards endpoint’s response shape and provider validation. Existing Go tests must remain green.
