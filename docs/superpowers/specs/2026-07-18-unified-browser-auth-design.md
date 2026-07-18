# Unified System-Browser Authentication Design

Date: 2026-07-18  
Status: Pending user review

## Goal

Replace the remaining provider-specific WebView authentication and account-page implementations with the system-browser/CDP model proven by Ollama.

After this change, OpenCode Go, DeepSeek, and Ollama will all use an application-owned Chrome, Chromium, or Edge process with a private temporary profile for:

- interactive login and credential capture;
- opening an authenticated provider account page;
- deterministic browser termination and profile cleanup.

The desktop WebView remains responsible only for the local sidebar UI.

## Non-goals

- Do not install or require a browser extension.
- Do not read, copy, or decrypt a user's normal browser profile.
- Do not force the three providers to use the same credential type.
- Do not change provider quota APIs, parsing semantics, card layout, or configuration file location.
- Do not keep a background browser process alive after credential capture.

## Current state

Ollama already uses a one-shot system browser and CDP. OpenCode Go still uses Linux-only WebKitGTK Cookie persistence through C/CGO, while DeepSeek injects JavaScript into a WebView to scan storage and request headers. Opening OpenCode and DeepSeek account pages also depends on those WebView-specific mechanisms.

This leaves duplicated lifecycle code, different platform support, and two credential paths that cannot be used by portable/nogui builds.

## Architecture

### Shared browser/CDP package

Create a provider-independent package under `internal/browserauth`:

```text
internal/browserauth/
  process.go       browser discovery, private profile, launch, wait, close
  cdp.go           DevTools discovery, WebSocket command/event transport
  cookies.go       Cookie retrieval, filtering primitives, and injection
  page.go          page target selection, URL, navigation, Runtime operations
  network.go       Network enablement and request-header event capture
```

The package owns only browser and CDP mechanics. It does not import configuration, quota, web server, or provider packages.

Provider coordinators remain in `internal/sidebar`:

```text
login_ollama.go
login_opencode.go
login_deepseek.go
```

They compose shared primitives instead of implementing their own browser process or WebView lifecycle. The design intentionally avoids a single generic Provider interface with `any` credentials: Ollama, OpenCode, and DeepSeek have different capture results and validation rules, while the reusable browser operations are already a clean boundary.

### CDP transport

The CDP client must have one WebSocket read loop that dispatches:

- command responses to pending request IDs;
- asynchronous protocol events to subscribers.

This replaces the current Ollama client's command-local read loop, which can ignore unrelated events but cannot safely support Network event subscriptions. Command calls remain concurrency-safe and context-bound. Closing a connection fails all pending calls and stops event delivery.

Only loopback DevTools HTTP and WebSocket endpoints are accepted.

## Common lifecycle

Login and account-page operations continue to run in child processes started by the local web API or directly through existing CLI commands.

The login lifecycle is:

```text
sidebar API / CLI
  -> launch child command
  -> create private temporary profile
  -> launch visible system browser on a nonzero loopback CDP port
  -> provider-specific credential capture
  -> close and reap browser
  -> delete temporary profile
  -> provider-specific validation and configuration update
  -> ordinary Go quota refresh
```

The account-page lifecycle is:

```text
sidebar context menu
  -> launch open-page child command
  -> create private temporary profile
  -> inject provider credentials through CDP
  -> navigate to the account page
  -> wait for the user to close the browser
  -> reap browser and delete the temporary profile
```

No Go quota request or configuration write may keep the login browser open unnecessarily.

## Provider flows

### Ollama

Login remains based on browser-level `Storage.getCookies` and `Browser.getVersion`:

1. Open `https://ollama.com/settings`.
2. Poll browser Cookies until a secure HttpOnly `__Secure-session` for an Ollama domain exists.
3. Retain supported ancillary Cookies such as `cf_clearance` and `aid` plus the browser User-Agent.
4. Close the browser and clean the profile.
5. Save the structurally complete credentials, then perform the Go Settings request.

Opening Settings injects all saved supported Cookies, applies the saved User-Agent through CDP when present, and navigates to Settings.

### OpenCode Go

1. Open the existing OpenCode OAuth authorization URL.
2. Read browser-level Cookies and retain Cookies whose effective domain is `opencode.ai`.
3. Observe page targets and current URLs until an authenticated workspace URL exposes a `wrk_*` Workspace ID.
4. When both Cookie and Workspace ID exist, close the browser and clean the profile.
5. Save the credentials, then perform the ordinary Go quota request.

Opening an OpenCode account page parses the saved Cookie header into individual Cookie parameters, injects them for `https://opencode.ai/`, and navigates to the saved workspace Go page.

This removes the Linux-only WebKit Cookie file, the C helper, and the non-Linux manual-login stub.

### DeepSeek

DeepSeek uses a Bearer Token and storage state rather than an exported Cookie header.

During login the coordinator concurrently gathers:

- Bearer Tokens observed in CDP Network request headers;
- token-shaped strings discovered through `Runtime.evaluate` scans of `localStorage` and `sessionStorage`;
- a complete JSON snapshot of both storage areas for the authenticated platform origin.

Capture becomes eligible to finish when the active target is on `platform.deepseek.com`, a complete storage snapshot has been obtained, and at least one Token candidate exists. After those conditions first hold, the coordinator keeps collecting for a two-second settling window so late Network events and storage writes are included, then closes the browser. A successful authenticated DeepSeek API request observed through CDP may satisfy the platform-state condition even before navigation reaches `/usage`.

Go then validates candidates against the existing DeepSeek summary endpoint until one succeeds. Only the validated Token and the final captured storage snapshot replace the configured account. If the login timeout expires without an eligible capture, or no candidate validates after the browser closes, the existing account remains unchanged.

Opening the DeepSeek usage page installs a document-start script with `Page.addScriptToEvaluateOnNewDocument`, restores the saved local/session storage in the page origin, and navigates to `https://platform.deepseek.com/usage`.

This removes the WebView JavaScript bindings, the WebView account-page window, and the nogui stubs.

## Configuration compatibility

No schema migration is required:

- OpenCode Go continues to store `Cookie` and `WorkspaceID`.
- DeepSeek continues to store `Token` and `WebStore`.
- Ollama continues to store `Cookie` and `UserAgent`.

Existing accounts remain usable. A user only needs to log in again if the existing credential has expired or its old DeepSeek account lacks enough storage state to restore the website.

## Browser discovery

The shared resolver returns the first available supported browser using platform-specific candidates.

Linux:

- `google-chrome`, `google-chrome-stable`
- `chromium`, `chromium-browser`
- `microsoft-edge`, `microsoft-edge-stable`

Windows:

- executable lookup for `msedge.exe`, `chrome.exe`, and Chromium variants;
- standard installations under Program Files, Program Files (x86), and LocalAppData.

macOS:

- Google Chrome and Microsoft Edge application bundle executables under `/Applications`;
- the corresponding user Applications paths;
- command-path Chromium variants when available.

Tests inject filesystem and executable lookup functions so platform resolution is deterministic without requiring every browser on the test host.

## Browser launch constraints

- The profile directory is newly created with mode `0700` where supported.
- DevTools binds only to `127.0.0.1` on a reserved nonzero port.
- The known debug address is carried directly; the implementation does not wait for `DevToolsActivePort`.
- Port zero must not be reintroduced because the tested Edge build exposes `navigator.webdriver`, causing Ollama Cloudflare verification to fail.
- The browser process is application-owned and is the only process the application may terminate.

## Error handling

- Missing browser: return an actionable Chrome/Chromium/Edge installation error.
- Browser closed before capture: return cancellation and do not create an empty account.
- DevTools unavailable while the browser is alive: retry until the operation context expires.
- CDP connection failure, timeout, or browser crash: close/reap when possible and remove the profile.
- Configuration writes happen only after the browser has closed.
- Ollama/OpenCode credentials are structurally definitive once the required Cookie and account identifier exist, so they may be saved before a transient quota refresh.
- DeepSeek storage scans can contain unrelated strings, so a candidate Token must pass the Go API validation before replacing an account.
- Re-login failure never deletes or overwrites an existing working account.

## Security boundary

- Never access the user's default browser profile.
- Never expose DevTools on a non-loopback address.
- Validate all returned DevTools WebSocket URLs as loopback endpoints.
- Restrict Cookie acceptance and injection to the provider's exact domain policy.
- Reject Cookie values, URLs, User-Agents, and headers containing control characters.
- Do not log credential values or full storage snapshots.
- Keep credentials only in the temporary profile, process memory, and the existing mode-`0600` configuration file.

## Testing

### Shared package

- browser candidate ordering and platform-specific paths;
- nonzero-port launch arguments and loopback validation;
- HTTP DevTools discovery through `/json/version` and `/json/list`;
- concurrent command response and event dispatch;
- cancellation and connection-close behavior;
- Cookie parsing, filtering, and injection;
- page navigation, current URL, Runtime evaluation, and document-start scripts;
- Network Authorization event capture;
- process kill, wait, and temporary-profile cleanup on every exit path.

### Provider coordinators

- Ollama Cookie and User-Agent capture;
- OpenCode Cookie plus Workspace ID capture;
- DeepSeek Network candidate, storage candidate, snapshot, and post-close validation;
- capture -> browser close -> configuration/Go request ordering;
- user cancellation and timeout without empty-account creation;
- re-login failure preserving existing configuration;
- account-page credential injection before navigation.

### Build and integration

- `go test ./...` in the GUI environment;
- `CGO_ENABLED=0 go test -tags nogui ./...`;
- Windows, macOS, Linux GUI, Linux portable, and both Linux deb builds in CI;
- one real login and one authenticated account-page smoke test for each provider.

## Expected file impact

Add:

- `internal/browserauth/*`
- shared package tests;
- provider coordinator tests required by the new flows.

Replace or substantially rewrite:

- `internal/sidebar/login_ollama.go`
- `internal/sidebar/login_opencode_linux.go`
- `internal/sidebar/login_opencode_stub.go`
- `internal/sidebar/login_webview.go`
- `internal/sidebar/login_nogui.go`
- the Ollama browser/CDP files after their reusable logic moves to `internal/browserauth`.

Update:

- `main.go` only where provider login finalization or saved-credential ordering changes;
- README platform/login documentation;
- the existing Ollama CDP design document to point to the shared implementation after migration.

The local web API routes and frontend interactions keep their current external behavior.
