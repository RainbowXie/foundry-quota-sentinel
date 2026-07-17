# Ollama Chromium CDP Login Design

## Decision

Use an application-owned Chrome, Chromium, or Edge process with a private temporary profile. Retrieve Ollama's HttpOnly login session through Chrome DevTools Protocol, then terminate the browser before the Go quota request and configuration refresh continue.

This replaces the rejected WebKitGTK approach. It does not install an extension, read the user's normal browser profile, decrypt a browser Cookie database, or keep a browser process alive after capture.

## Confirmed constraints

- Ollama's Settings page can be authenticated in WebKit, but WebKitGTK does not expose the HttpOnly session to the host APIs used by this application.
- A visible system Edge window can pass Cloudflare when started with an explicitly selected nonzero loopback debugging port.
- `--remote-debugging-port=0` makes `navigator.webdriver` observable in the tested Edge build and fails Cloudflare verification.
- An explicit nonzero port does not produce `DevToolsActivePort`; connection code must use the known address and query `/json/version`.
- Browser-level `Storage.getCookies` returns `__Secure-session`, `cf_clearance`, and `aid` after a successful login.
- The saved Cookie header plus the login browser's User-Agent can request `/settings` from Go and retrieve Session and Weekly usage.

## Components

### `internal/sidebar/ollama_browser.go`

- Resolves Chrome, Chromium, or Edge from the executable path.
- Creates a mode-`0700` temporary profile.
- Reserves a nonzero `127.0.0.1` port and passes the address to the CDP client.
- Owns browser termination, process reaping, and profile cleanup.

### `internal/sidebar/ollama_cdp.go`

- Accepts only loopback DevTools HTTP and WebSocket addresses.
- Reads the browser WebSocket URL from `/json/version` and page targets from `/json/list`.
- Uses browser-level `Storage.getCookies` so redirects and page-target replacement cannot hide the session.
- Uses `Browser.getVersion` for the matching User-Agent.
- Filters accepted Cookie names and Ollama domains without logging values.
- Retains page-target operations only for injecting a saved session and navigating an account page.

### `internal/sidebar/login_ollama.go`

- Polls until a valid Ollama session appears, the user closes the window, or the five-minute context expires.
- Returns captured Cookie and User-Agent without performing an HTTP quota request while the browser is open.
- Always closes the application-owned browser on return.

### Go quota/config flow

The caller stores the captured credentials first, then performs the ordinary `OllamaQuerier.FetchQuota` request. This separation guarantees that browser work is finished before network parsing begins and preserves a valid login during transient quota failures.

## Security boundary

- DevTools binds only to loopback.
- The browser uses a new private profile rather than the user's default profile.
- Only secure HttpOnly Ollama-domain cookies are considered; `__Secure-session` is mandatory.
- Cookie values are stored only in the existing mode-`0600` configuration and the temporary browser profile.
- Browser and WebSocket endpoints are validated as loopback before use.

## Failure behavior

- Missing browser: return an actionable install error.
- DevTools not ready: retry while the owned browser is running.
- Browser closed before capture: return a login-cancelled error and do not create an account.
- Timeout or CDP error: close the browser and remove the temporary profile.
- Quota request failure after capture: keep the saved account and report the refresh failure separately.

## Verification

Regression tests specifically cover the nonzero-port path without `DevToolsActivePort`, browser-level Cookie reads, User-Agent propagation, duplicate ancillary Cookies, process cleanup, and login return ordering. A process-level integration test used a real Edge instance and a secure HttpOnly test Cookie to prove automatic capture, browser shutdown, profile deletion, and credential persistence.
