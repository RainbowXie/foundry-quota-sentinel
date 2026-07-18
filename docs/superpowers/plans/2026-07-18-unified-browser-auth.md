# Unified System-Browser Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Migrate OpenCode Go, DeepSeek, and Ollama login/account-page flows to one private system-browser + CDP implementation while preserving existing credential schemas and provider APIs.

**Architecture:** Add a provider-independent `internal/browserauth` package that owns browser discovery, temporary profiles, nonzero loopback DevTools, CDP command/event transport, Cookies, page operations, Runtime evaluation, and Network events. Keep provider-specific credential logic in `internal/sidebar`; each coordinator captures or restores its native credential type and uses the shared lifecycle.

**Tech Stack:** Go 1.26, standard library, `github.com/gorilla/websocket`, Chrome DevTools Protocol, existing `webview_go` only for the sidebar window, Go test, CGO and nogui builds.

**Reference spec:** `docs/superpowers/specs/2026-07-18-unified-browser-auth-design.md`

---

## File map

Create:

- `internal/browserauth/process.go` — browser discovery, temporary profile, launch, wait, close, cleanup.
- `internal/browserauth/cdp.go` — DevTools discovery and multiplexed WebSocket command/event transport.
- `internal/browserauth/cookies.go` — Cookie types, parsing, validation, retrieval, and injection.
- `internal/browserauth/page.go` — URL, navigation, Runtime evaluation, and document-start scripts.
- `internal/browserauth/network.go` — Network enablement and request-header event decoding.
- `internal/sidebar/login_opencode.go` — platform-independent OpenCode system-browser flow.
- `internal/sidebar/login_deepseek.go` — platform-independent DeepSeek system-browser flow.

Modify:

- `internal/sidebar/login_ollama.go`
- `main.go`
- `README.md`, `README_EN.md`
- `docs/superpowers/specs/2026-07-17-ollama-cdp-login-design.md`

Delete after migration:

- `internal/sidebar/ollama_browser.go`, `ollama_browser_test.go`
- `internal/sidebar/ollama_cdp.go`, `ollama_cdp_test.go`
- `internal/sidebar/login_opencode_linux.go`, `login_opencode_stub.go`
- `internal/sidebar/login_webview.go`, `login_nogui.go`

Existing config structures, quota query functions, API routes, and frontend actions remain compatible.

---

### Task 1: Define the shared browser process contract

**Files:**
- Create: `internal/browserauth/process.go`
- Create: `internal/browserauth/process_test.go`

- [ ] **Step 1: Write failing resolver and cleanup tests**

```go
func TestResolveBrowserUsesInjectedLookupOrder(t *testing.T) {
	got, err := resolveBrowser(func(name string) (string, error) {
		if name == "chromium" {
			return "/usr/bin/chromium", nil
		}
		return "", fs.ErrNotExist
	})
	if err != nil || got != "/usr/bin/chromium" {
		t.Fatalf("resolveBrowser() = %q, %v", got, err)
	}
}

func TestBrowserCloseKillsWaitsAndRemovesProfile(t *testing.T) {
	profile := t.TempDir()
	killed, waited := false, false
	b := &Browser{
		profileDir: profile,
		kill: func() error { killed = true; return nil },
		wait: func() error { waited = true; return nil },
	}
	if err := b.Close(); err != nil { t.Fatal(err) }
	if !killed || !waited { t.Fatalf("kill=%v wait=%v", killed, waited) }
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile still exists: %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
rtk go test ./internal/browserauth -run 'TestResolveBrowser|TestBrowserClose' -count=1
```

Expected: package or symbol failures because `internal/browserauth` and `Browser` do not exist.

- [ ] **Step 3: Implement the process API**

```go
type LaunchOptions struct {
	StartURL string
}

type Browser struct {
	profileDir   string
	debugAddress string
	kill         func() error
	wait         func() error
	exited       func() bool
	waitOnce, cleanOnce, closeOnce sync.Once
	waitErr, cleanErr, closeErr error
}

func Launch(ctx context.Context, options LaunchOptions) (*Browser, error)
func (b *Browser) DebugAddress() string
func (b *Browser) Exited() bool
func (b *Browser) Wait() error
func (b *Browser) Close() error
```

`Launch` creates a mode-`0700` temporary directory, reserves a nonzero `127.0.0.1` port, and starts the browser with:

```text
--user-data-dir=<profile>
--remote-debugging-address=127.0.0.1
--remote-debugging-port=<reserved nonzero port>
--no-first-run
--no-default-browser-check
--new-window <StartURL>
```

It stores `127.0.0.1:<port>` directly and never reads `DevToolsActivePort` or uses the user's normal profile. Preserve the existing Ollama kill/wait/cleanup semantics, including ignoring `signal: killed` after an application-initiated kill.

- [ ] **Step 4: Run tests and verify GREEN**

```bash
rtk gofmt -w internal/browserauth/process.go internal/browserauth/process_test.go
rtk go test ./internal/browserauth -run 'TestResolveBrowser|TestBrowserClose' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/browserauth/process.go internal/browserauth/process_test.go
rtk git commit -m "feat(browserauth): own private system browser lifecycle"
```

---

### Task 2: Build a multiplexed CDP transport

**Files:**
- Create: `internal/browserauth/cdp.go`
- Create: `internal/browserauth/cdp_test.go`

- [ ] **Step 1: Write a failing response/event dispatch test**

Use `httptest.Server` and a Gorilla WebSocket upgrader. The fake page target sends `Network.requestWillBeSentExtraInfo` before the matching `Runtime.evaluate` response:

```go
func TestClientDispatchesResponsesAndEvents(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	client, err := Connect(context.Background(), server.DebugAddress())
	if err != nil { t.Fatal(err) }
	defer client.Close()
	result, err := client.Call(context.Background(), "Runtime.evaluate", map[string]any{"expression": "1"})
	if err != nil || string(result) != `{"result":{"type":"number","value":1}}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	select {
	case event := <-client.Events():
		if event.Method != "Network.requestWillBeSentExtraInfo" { t.Fatalf("event=%s", event.Method) }
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

```bash
rtk go test ./internal/browserauth -run TestClientDispatchesResponsesAndEvents -count=1
```

Expected: FAIL because `Connect`, `Call`, and event dispatch do not exist.

- [ ] **Step 3: Implement DevTools discovery and transport**

```go
type Event struct {
	Method string
	Params json.RawMessage
}

type Client struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan response
	events    chan Event
	done      chan struct{}
	closeOnce sync.Once
}

func Connect(ctx context.Context, debugAddress string) (*Client, error)
func (c *Client) BrowserClient(ctx context.Context) (*Client, error)
func (c *Client) PageClient(ctx context.Context) (*Client, error)
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error)
func (c *Client) Events() <-chan Event
func (c *Client) Close() error
```

`Connect` validates a loopback host/port, reads the browser endpoint from `/json/version`, selects a loopback page target from `/json/list`, and retains both endpoints. Each WebSocket connection has exactly one read goroutine. Responses with IDs go to pending calls; messages with a method become events. Context cancellation removes its pending ID. Connection close fails every pending request and closes `done`. Use a bounded event channel so an event consumer cannot block command responses.

- [ ] **Step 4: Run tests and verify GREEN**

```bash
rtk gofmt -w internal/browserauth/cdp.go internal/browserauth/cdp_test.go
rtk go test ./internal/browserauth -run TestClientDispatchesResponsesAndEvents -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/browserauth/cdp.go internal/browserauth/cdp_test.go
rtk git commit -m "feat(browserauth): multiplex CDP commands and events"
```

---

### Task 3: Add Cookie, page, Runtime, and Network operations

**Files:**
- Create: `internal/browserauth/cookies.go`
- Create: `internal/browserauth/page.go`
- Create: `internal/browserauth/network.go`
- Create: `internal/browserauth/operations_test.go`

- [ ] **Step 1: Write failing operation tests**

```go
func TestSetCookiesRejectsUnsafeValues(t *testing.T) {
	for _, cookie := range []Cookie{
		{Name: "session\r\n", Value: "safe", Domain: "ollama.com"},
		{Name: "session", Value: "bad;value", Domain: "ollama.com"},
		{Name: "session", Value: "safe", Domain: "example.com"},
	} {
		if err := validateCookie(cookie); err == nil {
			t.Fatalf("validateCookie(%#v) = nil", cookie)
		}
	}
}

func TestDecodeAuthorizationEvent(t *testing.T) {
	event := Event{Method: "Network.requestWillBeSentExtraInfo", Params: json.RawMessage(`{"headers":{"authorization":"Bearer valid.token-12345678901234567890"}}`)}
	decoded, ok := DecodeRequestHeadersEvent(event)
	if !ok || BearerToken(decoded.Headers) != "valid.token-12345678901234567890" {
		t.Fatalf("decoded=%#v ok=%v", decoded, ok)
	}
}
```

Add two fake-server tests that call `BrowserCookies`/`BrowserUserAgent` and `PageURL`/`Navigate`/`AddScriptOnNewDocument`; the fake server must record the WebSocket endpoint for each method and fail if browser operations arrive on the page endpoint or page operations arrive on the browser endpoint.

Implement the test bodies with the fake server from Task 2; fail if an operation is sent to the wrong target.

- [ ] **Step 2: Run tests and verify RED**

```bash
rtk go test ./internal/browserauth -run 'TestBrowserOperations|TestPageOperations|TestSetCookies|TestDecodeAuthorization' -count=1
```

Expected: FAIL because the operation methods do not exist.

- [ ] **Step 3: Implement the provider-neutral operations**

```go
type Cookie struct {
	Name, Value, Domain, Path string
	Secure, HTTPOnly bool
}

func validateCookie(cookie Cookie) error
func (c *Client) BrowserCookies(ctx context.Context) ([]Cookie, error)
func (c *Client) BrowserUserAgent(ctx context.Context) (string, error)
func (c *Client) SetCookies(ctx context.Context, cookies []Cookie) error
func (c *Client) SetUserAgent(ctx context.Context, userAgent string) error
func (c *Client) PageURL(ctx context.Context) (string, error)
func (c *Client) Navigate(ctx context.Context, pageURL string) error
func (c *Client) Evaluate(ctx context.Context, expression string) (json.RawMessage, error)
func (c *Client) AddScriptOnNewDocument(ctx context.Context, script string) error
func (c *Client) EnableNetwork(ctx context.Context) error
```

Browser Cookies use `Storage.getCookies`; injection uses `Storage.setCookies`; User-Agent uses `Browser.getVersion` and `Emulation.setUserAgentOverride`. Page URL uses `Runtime.evaluate` with `location.href`. Navigation permits only HTTPS URLs accepted by the provider coordinator. Network event decoding supports `Network.requestWillBeSent` and `Network.requestWillBeSentExtraInfo` and returns normalized headers without logging values.

- [ ] **Step 4: Run all shared tests**

```bash
rtk gofmt -w internal/browserauth/*.go
rtk go test ./internal/browserauth -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/browserauth/cookies.go internal/browserauth/page.go internal/browserauth/network.go internal/browserauth/operations_test.go
rtk git commit -m "feat(browserauth): expose shared CDP operations"
```

---

### Task 4: Migrate Ollama to `internal/browserauth`

**Files:**
- Modify: `internal/sidebar/login_ollama.go`
- Modify: `internal/sidebar/login_ollama_test.go`
- Delete: `internal/sidebar/ollama_browser.go`, `ollama_browser_test.go`
- Delete: `internal/sidebar/ollama_cdp.go`, `ollama_cdp_test.go`

- [ ] **Step 1: Write a failing close-order test**

```go
func TestRunOllamaLoginClosesBrowserBeforeReturning(t *testing.T) {
	closed := false
	browser := newFakeBrowserauthBrowser([]browserauth.Cookie{{
		Name: "__Secure-session", Value: "good", Domain: "ollama.com", Secure: true, HTTPOnly: true,
	}})
	browser.onClose = func() { closed = true }
	credentials, err := runOllamaLogin(context.Background(), browser)
	if err != nil { t.Fatal(err) }
	if !closed || credentials.Cookie != "__Secure-session=good" {
		t.Fatalf("closed=%v credentials=%#v", closed, credentials)
	}
}
```

Retain tests for ancillary Cookie deduplication, missing Session, cancellation, and User-Agent propagation, but use `browserauth.Cookie` instead of local `cdpCookie`.

- [ ] **Step 2: Run tests and verify RED**

```bash
rtk go test ./internal/sidebar -run 'TestRunOllama|TestOllamaSession' -count=1
```

Expected: compile failures because the adapter still depends on local Ollama browser/CDP types.

- [ ] **Step 3: Implement the Ollama adapter**

`RunOllamaLogin` launches `browserauth.Launch`, polls `BrowserCookies` and `BrowserUserAgent`, builds the existing Cookie header, closes the browser, and returns `OllamaLoginCredentials`.

Change account-page restoration to accept the saved User-Agent:

```go
func RunOllamaPage(pageURL, cookieHeader, userAgent string) error
```

Parse all supported saved Cookies (`__Secure-session`, `cf_clearance`, `aid`) into `browserauth.Cookie` values, inject them for `ollama.com`, apply `SetUserAgent` when non-empty, navigate, wait, and clean up. Update the single `main.go` caller in Task 7.

- [ ] **Step 4: Run Ollama and shared tests**

```bash
rtk gofmt -w internal/sidebar/login_ollama.go internal/sidebar/login_ollama_test.go
rtk go test ./internal/browserauth ./internal/sidebar -count=1
```

Expected: PASS.

- [ ] **Step 5: Delete duplicate Ollama infrastructure and commit**

```bash
rtk git rm internal/sidebar/ollama_browser.go internal/sidebar/ollama_browser_test.go internal/sidebar/ollama_cdp.go internal/sidebar/ollama_cdp_test.go
rtk git add internal/sidebar/login_ollama.go internal/sidebar/login_ollama_test.go
rtk git commit -m "refactor(ollama): use shared browser authentication"
```

---

### Task 5: Migrate OpenCode login and account pages

**Files:**
- Create: `internal/sidebar/login_opencode.go`
- Create: `internal/sidebar/login_opencode_test.go`
- Delete: `internal/sidebar/login_opencode_linux.go`
- Delete: `internal/sidebar/login_opencode_stub.go`

- [ ] **Step 1: Write failing OpenCode flow tests**

```go
func TestOpenCodeCookieHeaderKeepsOnlyMainDomain(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "session", Value: "good", Domain: ".opencode.ai", Secure: true, HTTPOnly: true},
		{Name: "oauth", Value: "skip", Domain: "auth.opencode.ai", Secure: true, HTTPOnly: true},
	}
	if got := openCodeCookieHeader(cookies); got != "session=good" { t.Fatalf("header=%q", got) }
}

func TestOpenCodeWorkspaceIDFromURL(t *testing.T) {
	if got := openCodeWorkspaceID("https://opencode.ai/workspace/wrk_abc123/go"); got != "wrk_abc123" {
		t.Fatalf("workspace=%q", got)
	}
}

func TestRunOpenCodeLoginValidatesAfterClose(t *testing.T) {
	closed := false
	browser := newFakeOpenCodeBrowser("session=good", "wrk_abc123", func() { closed = true })
	_, _, err := runOpenCodeLogin(context.Background(), browser, func(_, _ string) bool { return closed })
	if err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Run tests and verify RED**

```bash
rtk go test ./internal/sidebar -run 'TestOpenCode|TestRunOpenCode' -count=1
```

Expected: FAIL because the platform-independent coordinator does not exist.

- [ ] **Step 3: Implement OpenCode capture and restoration**

Preserve the public signatures:

```go
func RunOpenCodeLogin(validate func(cookie, workspaceID string) bool) (string, string, error)
func RunOpenCodePage(pageURL, cookie string) error
```

Launch the existing OAuth authorization URL. Poll `PageURL` and `BrowserCookies` until a `wrk_*` Workspace ID and at least one valid effective `opencode.ai` Cookie exist. Exclude `auth.opencode.ai` Cookies. Close the browser before calling `validate`; return an error without a config write when validation fails.

For account pages, parse the saved Cookie header into secure `browserauth.Cookie` values for `opencode.ai`, inject them, navigate to the supplied workspace URL, wait for user close, and clean the profile.

- [ ] **Step 4: Run OpenCode tests in GUI and nogui modes**

```bash
rtk gofmt -w internal/sidebar/login_opencode.go internal/sidebar/login_opencode_test.go
rtk go test ./internal/sidebar -run 'TestOpenCode|TestRunOpenCode' -count=1
rtk env CGO_ENABLED=0 go test -tags nogui ./internal/sidebar -count=1
```

Expected: PASS in both modes.

- [ ] **Step 5: Delete WebKit files and commit**

```bash
rtk git rm internal/sidebar/login_opencode_linux.go internal/sidebar/login_opencode_stub.go
rtk git add internal/sidebar/login_opencode.go internal/sidebar/login_opencode_test.go
rtk git commit -m "refactor(opencode): use shared system-browser authentication"
```

---

### Task 6: Migrate DeepSeek token and storage capture

**Files:**
- Create: `internal/sidebar/login_deepseek.go`
- Create: `internal/sidebar/login_deepseek_test.go`
- Delete: `internal/sidebar/login_webview.go`
- Delete: `internal/sidebar/login_nogui.go`

- [ ] **Step 1: Write failing DeepSeek tests**

```go
func TestDeepSeekBearerCandidateFromNetworkEvent(t *testing.T) {
	event := browserauth.Event{Method: "Network.requestWillBeSentExtraInfo", Params: json.RawMessage(`{"headers":{"authorization":"Bearer valid.token-12345678901234567890"}}`)}
	if got := deepSeekTokenFromEvent(event); got != "valid.token-12345678901234567890" { t.Fatalf("token=%q", got) }
}

func TestDeepSeekStorageSnapshotProducesCandidates(t *testing.T) {
	snapshot := `{"l":{"auth":"{\"token\":\"candidate_abcdefghijklmnopqrstuvwxyz\"}"},"s":{}}`
	got := deepSeekStorageCandidates(snapshot)
	if len(got) != 1 || got[0] != "candidate_abcdefghijklmnopqrstuvwxyz" { t.Fatalf("candidates=%v", got) }
}

func TestRunDeepSeekLoginValidatesAfterBrowserClose(t *testing.T) {
	closed := false
	browser := newFakeDeepSeekBrowser(func() { closed = true })
	_, _, err := runDeepSeekLogin(context.Background(), browser, func(string) bool { return closed })
	if err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Run tests and verify RED**

```bash
rtk go test ./internal/sidebar -run 'TestDeepSeek|TestRunDeepSeek' -count=1
```

Expected: FAIL because the CDP-based coordinator does not exist.

- [ ] **Step 3: Implement candidate collection and the settling window**

Preserve the public signatures:

```go
func RunDeepSeekLogin(validate func(string) bool) (token, webStore string, err error)
func RunDeepSeekPage(pageURL, webStore string) error
```

The coordinator must:

1. Launch `https://platform.deepseek.com/sign_in`.
2. Enable Network events and collect Bearer candidates from platform requests.
3. Poll `Runtime.evaluate` on a `platform.deepseek.com` target to snapshot local/session storage and recursively collect token-shaped strings.
4. Become eligible when the target is on the platform origin, a complete `{l:{},s:{}}` snapshot exists, and at least one candidate exists.
5. Continue collecting for exactly two seconds after first eligibility.
6. Close/reap the browser before validating candidates.
7. Return the first validated Token with the final snapshot; return an error if none validate.

For `RunDeepSeekPage`, JSON-encode `webStore` as a string argument to a document-start script, parse it in the page, restore local/session storage, navigate to Usage, wait for close, and clean up. Do not concatenate raw storage values into executable JavaScript.

- [ ] **Step 4: Run DeepSeek tests in GUI and nogui modes**

```bash
rtk gofmt -w internal/sidebar/login_deepseek.go internal/sidebar/login_deepseek_test.go
rtk go test ./internal/sidebar -run 'TestDeepSeek|TestRunDeepSeek' -count=1
rtk env CGO_ENABLED=0 go test -tags nogui ./internal/sidebar -count=1
```

Expected: PASS; DeepSeek login no longer imports `webview_go`.

- [ ] **Step 5: Delete WebView login files and commit**

```bash
rtk git rm internal/sidebar/login_webview.go internal/sidebar/login_nogui.go
rtk git add internal/sidebar/login_deepseek.go internal/sidebar/login_deepseek_test.go
rtk git commit -m "refactor(deepseek): capture token through system-browser CDP"
```

---

### Task 7: Align command wiring and documentation

**Files:**
- Modify: `main.go`
- Modify: `README.md`
- Modify: `README_EN.md`
- Modify: `docs/superpowers/specs/2026-07-17-ollama-cdp-login-design.md`

- [ ] **Step 1: Write command/account-page regression checks**

Add a focused test around the provider account-page dispatcher. At minimum, build the nogui binary and assert all login commands remain documented:

```bash
rtk env CGO_ENABLED=0 go build -tags nogui -o /tmp/fqs-unified-auth .
rtk /tmp/fqs-unified-auth help | rtk rg 'login-(opencode|deepseek|ollama)'
```

Expected before wiring changes: command names exist, while source inspection still finds old WebView/manual fallback messages.

- [ ] **Step 2: Update account-page calls and errors**

Update the Ollama call to include the stored User-Agent:

```go
if err := sidebar.RunOllamaPage(url, acc.Cookie, acc.UserAgent); err != nil {
	fmt.Fprintf(os.Stderr, "Ollama 账户页浏览器不可用: %v\n", err)
	os.Exit(1)
}
```

For OpenCode and DeepSeek, report system-browser injection errors directly. Remove the fallback that opens an unauthenticated default-browser URL after credential injection fails.

Keep all existing command names, API routes, account names, and config writes unchanged.

- [ ] **Step 3: Update documentation**

README platform tables must state that OpenCode, DeepSeek, and Ollama use a temporary Chrome/Chromium/Edge process when a supported browser is installed. Remove statements that OpenCode login is Linux/WebKit-only or that DeepSeek opens an app WebView. State that the main WebView is only the local sidebar UI.

Update `2026-07-17-ollama-cdp-login-design.md` so its component section points to `internal/browserauth` rather than standalone Ollama browser/CDP files.

- [ ] **Step 4: Verify commands and stale-text cleanup**

```bash
rtk gofmt -w main.go
rtk env CGO_ENABLED=0 go build -tags nogui -o /tmp/fqs-unified-auth .
rtk /tmp/fqs-unified-auth version
rtk /tmp/fqs-unified-auth help | rtk rg 'login-(opencode|deepseek|ollama)'
rtk rg -n '仅支持 Linux.*WebKit|内置窗口注入 cookie|DeepSeek.*WebView' README.md README_EN.md main.go internal/sidebar || true
```

Expected: the current version prints, all login commands are listed, and obsolete provider-login limitations have no matches.

- [ ] **Step 5: Commit**

```bash
rtk git add main.go README.md README_EN.md docs/superpowers/specs/2026-07-17-ollama-cdp-login-design.md
rtk git commit -m "docs: align provider login with shared system browser"
```

---

### Task 8: Full verification and real-browser acceptance

**Files:**
- No planned production changes; only correct a test/build issue found by the commands in this task before rerunning them.

- [ ] **Step 1: Run all local automated checks**

```bash
rtk gofmt -l internal/browserauth internal/sidebar main.go
rtk git diff --check
rtk go test ./... -count=1
rtk env CGO_ENABLED=0 go test -tags nogui ./... -count=1
rtk env CGO_ENABLED=0 go vet -tags nogui ./...
```

Expected: no formatting output, no diff errors, and all tests/vet pass.

- [ ] **Step 2: Build the configured Linux GUI package**

```bash
rtk env PKG_CONFIG_PATH=/tmp/fqs-webkit41-pcshim CGO_ENABLED=1 go build -ldflags='-s -w' -o build/foundry-quota-sentinel-unified-browser-auth .
rtk build/foundry-quota-sentinel-unified-browser-auth version
```

Expected: successful build and the current application version.

- [ ] **Step 3: Perform real login acceptance for every provider**

For OpenCode, DeepSeek, and Ollama:

1. Run the matching `login-*` command with a named test account.
2. Complete authentication in the system browser.
3. Verify the browser closes before terminal-side validation/configuration output.
4. Verify the existing provider config fields are written.
5. Cancel one login and verify no empty account is created.
6. Verify the provider card refreshes after the child command exits.

Use isolated temporary HOME directories where possible. Never print or commit credential values; record only lifecycle stages and success/failure.

- [ ] **Step 4: Perform account-page acceptance for every provider**

Trigger the card context-menu open action for one saved account per provider and verify:

- the system browser uses a temporary profile;
- credentials are injected before navigation;
- OpenCode opens the saved workspace;
- DeepSeek restores the Usage session;
- Ollama opens authenticated Settings;
- browser close removes the temporary profile and leaves no owned process.

- [ ] **Step 5: Push the feature branch and verify CI**

```bash
rtk git push -u origin feature/unified-browser-auth
rtk gh run list --branch feature/unified-browser-auth --limit 5
```

Expected: Windows, macOS, Linux GUI, WebKit 4.0/4.1 deb, and portable build jobs conclude `success`.

- [ ] **Step 6: Commit verification corrections only when files changed**

```bash
rtk git status --short
rtk git diff --check
rtk git add internal/browserauth internal/sidebar main.go README.md README_EN.md docs
rtk git commit -m "test: verify unified provider browser authentication"
```

If verification produces no file changes, skip the commit and record the successful commands in the handoff. Do not create a release tag; release versioning requires a separate explicit user request after real-browser acceptance.

---

## Plan self-review

- Tasks 1–3 cover the shared process, CDP transport, event dispatch, Cookie, page, Runtime, Network, loopback, and nonzero-port requirements.
- Task 4 preserves Ollama behavior while moving reusable code.
- Task 5 replaces OpenCode WebKit/CGO and supports GUI/nogui builds.
- Task 6 replaces DeepSeek WebView capture, implements the two-second settling rule, and validates after browser close.
- Task 7 preserves public commands/config while removing unauthenticated account-page fallbacks and stale documentation.
- Task 8 covers automated tests, configured GUI build, three real login tests, three account-page tests, profile cleanup, and the full CI matrix.
- Existing config field names and exported login function signatures remain consistent, except `RunOllamaPage` intentionally adds the saved User-Agent argument.
- No implementation task introduces a release version or tag.
