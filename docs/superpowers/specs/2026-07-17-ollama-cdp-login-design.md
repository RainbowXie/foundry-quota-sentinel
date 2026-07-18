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

The browser process, CDP transport, Cookie / page / Network operations, and lifecycle plumbing now live in the shared `internal/browserauth` package. This document describes the Ollama-specific coordinator on top of that shared layer; for the underlying mechanism see [`docs/superpowers/specs/2026-07-18-unified-browser-auth-design.md`](../specs/2026-07-18-unified-browser-auth-design.md).

### `internal/sidebar/login_ollama.go`

- Launches `https://ollama.com/settings` through `browserauth.Launch`, which resolves the system browser, reserves a nonzero loopback DevTools port, creates a private temporary profile, and owns browser termination.
- Polls `Browser().BrowserCookies` and `Browser().BrowserUserAgent` until a secure `__Secure-session` for an Ollama domain appears, the user closes the window, or the five-minute context expires.
- Returns captured Cookie header and User-Agent without performing an HTTP quota request while the browser is open.
- Always closes the application-owned browser on return.

### Account-page flow

`RunOllamaPage(pageURL, cookieHeader, userAgent)` launches `about:blank`, calls `Browser().SetCookies` and `Browser().SetUserAgent`, then navigates to the requested Ollama page through `Page().Navigate`. The browser stays open until the user closes it.

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
