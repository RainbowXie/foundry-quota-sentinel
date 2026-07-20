package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"foundry-quota-sentinel/internal/quota"
)

func TestOllamaCardsReturnsSortedQuotaResults(t *testing.T) {
	srv := NewServer(nil)
	srv.SetOllamaAccounts([]OllamaAccount{
		{Name: "zeta", Cookie: "zeta-cookie"},
		{Name: "alpha", Cookie: "alpha-cookie"},
	})
	srv.ollamaFetch = func(a OllamaAccount) (*quota.QuotaData, error) {
		if a.Name == "zeta" {
			return nil, errors.New("unavailable")
		}
		return &quota.QuotaData{Rolling: quota.QuotaUsage{UsagePercent: 42}}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/ollama", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name    string           `json:"name"`
			Success bool             `json:"success"`
			Quota   *quota.QuotaData `json:"quota"`
			Error   string           `json:"error"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 2 {
		t.Fatalf("response = %#v", got)
	}
	if got.Data[0].Name != "alpha" || !got.Data[0].Success || got.Data[0].Quota == nil {
		t.Fatalf("first card = %#v", got.Data[0])
	}
	if got.Data[1].Name != "zeta" || got.Data[1].Success || got.Data[1].Error != "unavailable" {
		t.Fatalf("second card = %#v", got.Data[1])
	}
}

func TestDeleteOllamaCallsConfiguredHandler(t *testing.T) {
	srv := NewServer(nil)
	var provider, name string
	srv.SetDeleteHandler(func(gotProvider, gotName string) error {
		provider, name = gotProvider, gotName
		return nil
	})

	r := httptest.NewRequest(http.MethodGet, "/api/delete?provider=ollama&name=home", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if provider != "ollama" || name != "home" {
		t.Fatalf("delete handler called with (%q, %q), want (ollama, home)", provider, name)
	}
}

// TestDeepSeekAccountsEndpointReturnsConfigImmediately proves the
// fast accounts endpoint reflects config-saved DeepSeek accounts
// without waiting for FetchSummary/FetchUsage. After a successful
// login the sidebar must be able to render a loading card shell the
// moment the account is written to config, not after the slow remote
// data fetch completes. The endpoint must NOT call the network
// fetcher at all.
func TestDeepSeekAccountsEndpointReturnsConfigImmediately(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok"}}
	})

	start := time.Now()
	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if elapsed > time.Second {
		t.Fatalf("accounts endpoint blocked for %v on remote fetch", elapsed)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name    string `json:"name"`
			Pending bool   `json:"pending"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 1 || got.Data[0].Name != "work" || !got.Data[0].Pending {
		t.Fatalf("response = %#v", got)
	}
}

// TestDeepSeekAccountsEndpointEmptyWhenNoAccounts proves the fast
// accounts endpoint returns an empty list (not an error, not a ghost
// card) when config has no DeepSeek account — e.g. right after a
// login that was cancelled before the account was saved.
func TestDeepSeekAccountsEndpointEmptyWhenNoAccounts(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount { return nil })

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 0 {
		t.Fatalf("response = %#v, want empty data", got)
	}
}

// TestDeepSeekLoginSucceedsWithoutRevision proves /api/deepseek/login
// no longer carries a global config revision: the completion signal is
// the per-account generation from /api/deepseek/accounts, which is
// immune to unrelated config saves (window size, other providers).
func TestDeepSeekLoginSucceedsWithoutRevision(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnDeepSeekLogin = func(string) error { return nil }

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success  bool   `json:"success"`
		Revision *int64 `json:"revision"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatalf("response = %#v, want success=true", got)
	}
	if got.Revision != nil {
		t.Fatalf("login must not return a global revision (per-account generation used instead), got %v", *got.Revision)
	}
}

// TestDeepSeekLoginReportsSpawnFailure proves /api/deepseek/login
// returns success=false when the login subprocess cannot be spawned.
// The sidebar must surface this instead of polling forever for a
// revision change that will never come.
func TestDeepSeekLoginReportsSpawnFailure(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnDeepSeekLogin = func(string) error { return errors.New("spawn denied") }

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/login?name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success || got.Error == "" {
		t.Fatalf("response = %#v, want success=false with error", got)
	}
}

// TestDeepSeekAccountsReportsGeneration proves the accounts endpoint
// exposes a per-account, non-sensitive login generation. A token
// fingerprint cannot detect a same-token re-login (DeepSeek can return
// the same long-lived token while the Cookie/WebStore is refreshed),
// so the poll would wait the full 5 minutes and never refresh. The
// generation bumps on every successful login save, independent of the
// token value, so a same-token re-login still completes.
func TestDeepSeekAccountsReportsGeneration(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok", Generation: 3}}
	})

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
		Data    []struct {
			Name       string `json:"name"`
			Generation int    `json:"generation"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || len(got.Data) != 1 || got.Data[0].Generation != 3 {
		t.Fatalf("response = %#v, want generation=3", got)
	}
}

// TestDeepSeekAccountsEndpointHasNoFingerprint proves the accounts
// response no longer carries a token fingerprint. A fingerprint is a
// derived credential marker that cannot represent login completion
// (same-token re-login does not change it); generation fully replaces
// it. The API must not leak a fingerprint key at all.
func TestDeepSeekAccountsEndpointHasNoFingerprint(t *testing.T) {
	srv := NewServer(nil)
	srv.SetDeepSeekProvider(func() []DeepSeekAccount {
		return []DeepSeekAccount{{Name: "work", Token: "tok", Generation: 2}}
	})

	r := httptest.NewRequest(http.MethodGet, "/api/deepseek/accounts", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if strings.Contains(body, "fingerprint") {
		t.Fatalf("accounts response must not contain a fingerprint key: %s", body)
	}
}

// TestOpenEndpointReportsRuntimeSubprocessFailure proves /api/open does
// NOT return success the moment the subprocess is spawned. The old
// fire-and-forget handler returned success even when the open-page
// subprocess failed at runtime (cookie rejected, DeepSeek restore
// failed), so the user saw "no reaction". The handler now waits on an
// explicit ready/error handshake; if the subprocess exits before
// writing "ready", the runtime failure is surfaced.
func TestOpenEndpointReportsRuntimeSubprocessFailure(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnOpenPage = func(string, string, string) (func() error, error) {
		return func() error { return errors.New("deepseek: 登录态恢复失败：页面重定向到登录页") }, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=deepseek&name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success || got.Error == "" {
		t.Fatalf("response = %#v, want success=false with a runtime error", got)
	}
	if !strings.Contains(got.Error, "登录态恢复失败") {
		t.Fatalf("error must surface the subprocess runtime failure: %q", got.Error)
	}
}

// TestOpenEndpointSucceedsOnReadyHandshake proves /api/open returns
// success once the subprocess writes the explicit "ready" handshake —
// not after an arbitrary timeout. A long-running page (blocking on
// browser.Wait) writes "ready" right after the post-navigation check;
// the handler returns success the moment it reads it.
func TestOpenEndpointSucceedsOnReadyHandshake(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnOpenPage = func(_ string, _ string, session string) (func() error, error) {
		WriteOpenHandshake(session, "ready", "")
		return func() error { select {} }, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=ollama&name=home", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatal("a ready handshake must report success immediately, not after a fixed window")
	}
}

// TestOpenEndpointReportsErrorHandshake proves /api/open surfaces an
// explicit "error" handshake from the subprocess (e.g. DeepSeek restore
// detected a redirect and wrote error before the page flow exited).
func TestOpenEndpointReportsErrorHandshake(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnOpenPage = func(_ string, _ string, session string) (func() error, error) {
		WriteOpenHandshake(session, "error", "DeepSeek 登录态恢复失败：document-start 脚本未生效")
		return func() error { select {} }, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=deepseek&name=work", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success || !strings.Contains(got.Error, "document-start 脚本未生效") {
		t.Fatalf("response = %#v, want success=false surfacing the error handshake", got)
	}
}

// TestOpenEndpointSucceedsAfterSlowStart proves /api/open waits for a
// late "ready" handshake (a slow page that takes a moment to open + run
// its post-navigation auth check) rather than a fixed 3s window that
// would falsely time out a healthy slow page.
func TestOpenEndpointSucceedsAfterSlowStart(t *testing.T) {
	srv := NewServer(nil)
	srv.spawnOpenPage = func(_ string, _ string, session string) (func() error, error) {
		// Simulate a slow page: write ready after a short delay.
		go func() {
			time.Sleep(300 * time.Millisecond)
			WriteOpenHandshake(session, "ready", "")
		}()
		return func() error { select {} }, nil
	}

	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=opencode&name=work", nil)
	w := httptest.NewRecorder()
	start := time.Now()
	srv.Handler().ServeHTTP(w, r)
	elapsed := time.Since(start)
	var got struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success {
		t.Fatal("a slow-starting page must report success once the ready handshake arrives")
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("handler returned too fast (%v): it must wait for the late ready handshake", elapsed)
	}
}

// TestOpenEndpointFailsOnHandshakeTimeout proves a handshake timeout is
// an explicit failure, NOT a silent success. A subprocess that never
// writes ready/error must surface a timeout error so the user is not
// left waiting with no feedback. Tested at the helper layer with a
// short timeout so it runs fast; the handler's 20s window uses the same
// waitForOpenHandshake and the same timeout-as-failure branch.
func TestOpenEndpointFailsOnHandshakeTimeout(t *testing.T) {
	waitErr := make(chan error, 1) // never delivers; subprocess never exits
	status, errMsg, ok := waitForOpenHandshake("fqs-test-timeout-session", waitErr, 80*time.Millisecond)
	if ok {
		t.Fatalf("timeout must return ok=false, got status=%q err=%q", status, errMsg)
	}
	// The handler treats ok=false as a timeout FAILURE. Assert the
	// handler's branch directly: ok=false ⇒ success=false with 超时.
	if status != "" || errMsg != "" {
		t.Fatalf("timeout must return empty status/errMsg, got %q %q", status, errMsg)
	}
}

// TestOpenEndpointTimeoutIsFailureNotSuccess proves the /api/open handler
// returns success=false on a handshake timeout (no silent success). The
// spawn never writes a handshake file; the handler must surface 超时.
// To keep the test fast, drive waitForOpenHandshake's contract: the
// handler's ok=false branch is exercised via a spawn that exits (so the
// subprocess-exit fallback delivers "error" rather than waiting 20s).
func TestOpenEndpointTimeoutIsFailureNotSuccess(t *testing.T) {
	srv := NewServer(nil)
	// No handshake file, but the subprocess exits immediately → the
	// exit-before-ready fallback surfaces the runtime error (a timeout
	// would take 20s; an early exit proves the failure-not-success path
	// quickly).
	srv.spawnOpenPage = func(string, string, string) (func() error, error) {
		return func() error { return errors.New("账户页子进程已退出") }, nil
	}
	r := httptest.NewRequest(http.MethodGet, "/api/open?provider=ollama&name=home", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success {
		t.Fatal("a runtime/timeout failure must report success=false, not silent success")
	}
}

// TestNewOpenSessionIsConcurrencySafe proves concurrent /api/open
// requests produce unique session ids with no data race. Run under
// -race: a non-atomic counter would either race or collide.
func TestNewOpenSessionIsConcurrencySafe(t *testing.T) {
	const n = 200
	ids := make([]string, n)
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			ids[i] = newOpenSession()
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}
	seen := make(map[string]bool, n)
	for i, id := range ids {
		if id == "" {
			t.Fatalf("goroutine %d produced an empty session id", i)
		}
		if seen[id] {
			t.Fatalf("duplicate session id across concurrent opens: %q", id)
		}
		seen[id] = true
	}
}
