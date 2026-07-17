# Ollama Provider Design

## Goal

Add Ollama Cloud as a third multi-account provider. Users can add an account from the existing `+` flow, sign in through an application-owned system browser window, and see Session and Weekly usage with reset times.

## Authentication model

Ollama does not expose a public account-quota API. Usage is rendered in the authenticated `https://ollama.com/settings` page, so each configured account stores:

- the account name;
- the Ollama cookie header, including `__Secure-session` and any available Cloudflare cookies needed by the request;
- the User-Agent reported by the login browser.

The configuration file is written with mode `0600`. Credentials are never included in API responses, rendered HTML, errors, or logs.

## Login flow

WebKitGTK cannot export Ollama's HttpOnly session, so Ollama login does not use the application's WebView. `RunOllamaLogin` instead:

1. Finds Chrome, Chromium, or Edge.
2. Creates a private temporary browser profile.
3. Reserves a nonzero loopback debugging port and launches `https://ollama.com/settings` in a visible browser window.
4. Polls browser-level CDP `Storage.getCookies` until a secure HttpOnly `__Secure-session` for `ollama.com` exists.
5. Reads the browser User-Agent, returns the credentials, closes the browser, waits for exit, and removes the temporary profile.
6. Saves the account before the Go quota request starts. A temporary quota/network failure therefore does not discard a successfully captured login.

The nonzero port is required because Edge exposes `navigator.webdriver` when `--remote-debugging-port=0`, which causes Ollama's Cloudflare challenge to reject the login window. Because Chromium does not create `DevToolsActivePort` for an explicitly selected nonzero port, the application carries the reserved address directly and discovers the browser WebSocket through `/json/version`.

Opening an existing Ollama account page uses another temporary browser profile, injects the saved `__Secure-session` through CDP, navigates to Settings, and cleans up when the user closes the window.

## Usage retrieval

`OllamaQuerier.FetchQuota` sends the saved Cookie and browser User-Agent to `/settings` with a bounded timeout and response-size limit. The server-rendered HTML contains Session and Weekly usage meters plus reset timestamps. The parser maps Session to `QuotaData.Rolling`, Weekly to `QuotaData.Weekly`, and leaves Monthly absent.

Each account is queried independently. A request or parser failure affects only that card and exposes a re-login action.

## Sidebar and local API

- `GET /api/ollama` concurrently queries configured Ollama accounts and returns cards sorted by name.
- `GET /api/ollama/login?name=...` starts the one-shot browser login child process.
- `/api/open` and `/api/delete` accept `provider=ollama`.
- The add-account modal and card renderer use the same interaction patterns as OpenCode Go.

## Verification

Automated tests cover HTML parsing, request headers, Cookie filtering, browser/CDP connection, process cleanup, login lifecycle, configuration, and server routes. The final flow was also verified with a real Edge process: a secure HttpOnly test session was captured, the browser closed, the temporary profile was deleted, and credentials were saved before the Go quota request.
