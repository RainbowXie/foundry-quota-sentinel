package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"foundry-quota-sentinel/internal/quota"
	"foundry-quota-sentinel/internal/state"
	"foundry-quota-sentinel/internal/storage"
)

//go:embed static/*
var webAssets embed.FS

type Account struct {
	Name        string
	Cookie      string
	WorkspaceID string
}

type DeepSeekAccount struct {
	Name  string
	Token string
	// Generation is a non-sensitive, per-account login counter bumped on
	// every successful login save. A token-derived signal cannot detect a
	// same-token re-login (DeepSeek may return the same long-lived token
	// while Cookie/WebStore is refreshed), so the poll would wait 5
	// minutes and never refresh. Generation moves on every real login save
	// regardless of token value, and is untouched by window-size /
	// other-provider saves, so only a real save of THIS account completes
	// the poll.
	Generation int
}

type OllamaAccount struct {
	Name      string
	Cookie    string
	UserAgent string
}

// KimiAccount is the web-layer view of a saved Kimi account. It carries the
// access token + saved browser headers for the fetcher, but the cards/accounts
// endpoints NEVER serialize them — only the name, quota meters, generation,
// and status/error leave the API.
type KimiAccount struct {
	Name         string
	AccessToken  string
	RefreshToken string
	// Headers are the saved browser headers (cookie + x-msh-* + user-agent)
	// the querier replays alongside the Bearer token. Keys are HTTP header
	// names. Never serialized to the API.
	Headers    map[string]string
	Generation int
}

type Server struct {
	addr           string
	accounts       []Account
	accountsFn     func() []Account
	dsAccounts     []DeepSeekAccount
	dsFn           func() []DeepSeekAccount
	ollamaAccounts []OllamaAccount
	ollamaFn       func() []OllamaAccount
	ollamaFetch    func(OllamaAccount) (*quota.QuotaData, error)
	kimiAccounts   []KimiAccount
	kimiFn         func() []KimiAccount
	// kimiFetch retrieves one Kimi account's two-meter quota. Injected by
	// tests; the default builds a KimiQuerier from the account's token.
	kimiFetch func(KimiAccount) (*quota.KimiQuotaData, error)
	// kimiFetchWithRefresh is the production per-account fetch path used when
	// kimiFetch is nil: it returns the quota data plus any rotated tokens so
	// the caller can atomically persist them under the per-account refresh
	// lock. Tests inject this to exercise refresh rotation + persistence
	// failure without a live Kimi server.
	kimiFetchWithRefresh func(ctx context.Context, a KimiAccount) (*quota.KimiQuotaData, *quota.RefreshResult, error)
	// kimiReloadAccount returns the LATEST saved KimiAccount by name, read
	// inside the per-account refresh lock. nil in tests (the closure falls
	// back to the request-time snapshot); production wires it to a fresh
	// config read so a concurrent rotation that already saved a new token is
	// observed instead of refreshing with a stale snapshot.
	kimiReloadAccount func(name string) (KimiAccount, bool)
	// kimiAccountLock acquires a cross-process per-account exclusive lock for
	// the named Kimi account, serializing reload→refresh→persist across
	// processes (a CLI quota-kimi and a web request for the same account cannot
	// double-rotate). It returns a release func. nil in tests (the in-process
	// per-account mutex is used); production wires it to a file flock.
	kimiAccountLock func(name string) (release func(), err error)
	// kimiRefreshSave persists rotated access/refresh tokens for a named Kimi
	// account after a successful auto-refresh. nil in tests (no persistence);
	// production wires it to a config save of the rotated envelope.
	kimiRefreshSave func(name, accessToken, refreshToken string) error
	// kimiRefreshLocks serializes refresh+persist per Kimi account so two
	// concurrent card requests for the SAME account cannot race the
	// RefreshToken endpoint (double rotation, one rotated refresh_token
	// invalidated, partial overwrites). Each account name maps to its own
	// mutex; the map itself is guarded by kimiLocksMu.
	kimiRefreshLocksMu sync.Mutex
	kimiRefreshLocks   map[string]*sync.Mutex
	deepseek           *quota.DeepSeekQuerier
	onWinSize          func(w, h int)
	onDelete           func(provider, name string) error
	// spawnDeepSeekLogin launches the login-deepseek subprocess. The
	// default uses os.Executable + exec.Command; tests inject a
	// failure to prove /api/deepseek/login returns success=false.
	spawnDeepSeekLogin func(name string) error
	// spawnKimiLogin launches the login-kimi subprocess. The default uses
	// os.Executable + exec.Command; tests inject a failure to prove
	// /api/kimi/login returns success=false.
	spawnKimiLogin func(name string) error
	// spawnOpenPage launches the "open-page <provider> <name>" subprocess
	// with FQS_OPEN_SESSION=<session> in its environment, then returns a
	// function that reports the subprocess exit. The handler does NOT
	// guess with a fixed timeout: it waits on an explicit ready/error
	// handshake file the subprocess writes once the page is ready (or on
	// failure). Tests inject a spawn that drives the handshake file.
	spawnOpenPage func(provider, name, session string) (wait func() error, err error)
	// openHandshakeTimeout is how long /api/open waits for the ready/error
	// handshake before returning an explicit timeout failure. Defaults to
	// 60s in the handler — comfortably above the 45s page-flow budgets so
	// a slow-but-successful open never loses the race; tests inject a
	// short value to cover the real timeout branch quickly.
	openHandshakeTimeout time.Duration
}

func NewServer(accounts []Account) *Server {
	return &Server{addr: ":8788", accounts: accounts, deepseek: quota.NewDeepSeekQuerier()}
}

// SetDeepSeekAccounts 注入静态 DeepSeek 账户列表。
func (s *Server) SetDeepSeekAccounts(accs []DeepSeekAccount) { s.dsAccounts = accs }

// SetOllamaAccounts 注入静态 Ollama 账户列表。
func (s *Server) SetOllamaAccounts(accs []OllamaAccount) { s.ollamaAccounts = accs }

// SetAccountsProvider 设置动态账户来源，每次请求实时读取（反映 config 变更，
// 例如 GUI 弹窗登录新增账户后无需重启即可出现）。
func (s *Server) SetAccountsProvider(fn func() []Account) { s.accountsFn = fn }

// SetDeepSeekProvider 设置动态 DeepSeek 账户来源，每次请求实时读取。
func (s *Server) SetDeepSeekProvider(fn func() []DeepSeekAccount) { s.dsFn = fn }

// SetOllamaProvider 设置动态 Ollama 账户来源，每次请求实时读取。
func (s *Server) SetOllamaProvider(fn func() []OllamaAccount) { s.ollamaFn = fn }

// SetKimiProvider sets the dynamic Kimi account source, read on each request
// so a newly saved account appears without a restart.
func (s *Server) SetKimiProvider(fn func() []KimiAccount) { s.kimiFn = fn }

// SetKimiReloadAccount wires the per-account in-lock reloader that returns the
// latest saved KimiAccount by name, so the refresh path observes a concurrent
// rotation's saved token instead of a stale request-time snapshot.
func (s *Server) SetKimiReloadAccount(fn func(name string) (KimiAccount, bool)) {
	s.kimiReloadAccount = fn
}

// SetKimiAccountLock wires the cross-process per-account exclusive lock used to
// serialize reload→refresh→persist across processes. The release func drops
// the lock.
func (s *Server) SetKimiAccountLock(fn func(name string) (release func(), err error)) {
	s.kimiAccountLock = fn
}

// SetKimiRefreshSave wires the callback that persists rotated access/refresh
// tokens for a Kimi account after an auto-refresh (production: config save).
func (s *Server) SetKimiRefreshSave(fn func(name, accessToken, refreshToken string) error) {
	s.kimiRefreshSave = fn
}

// SetWinSizeHandler 设置窗口大小持久化回调（前端 resize 时上报）。
func (s *Server) SetWinSizeHandler(fn func(w, h int)) { s.onWinSize = fn }

// SetDeleteHandler 设置删除账户回调（前端右键菜单二次确认后调用）。
func (s *Server) SetDeleteHandler(fn func(provider, name string) error) { s.onDelete = fn }

func (s *Server) curAccounts() []Account {
	if s.accountsFn != nil {
		return s.accountsFn()
	}
	return s.accounts
}

func (s *Server) curDeepSeek() []DeepSeekAccount {
	if s.dsFn != nil {
		return s.dsFn()
	}
	return s.dsAccounts
}

func (s *Server) curOllama() []OllamaAccount {
	if s.ollamaFn != nil {
		return s.ollamaFn()
	}
	return s.ollamaAccounts
}

func (s *Server) curKimi() []KimiAccount {
	if s.kimiFn != nil {
		return s.kimiFn()
	}
	return s.kimiAccounts
}

// kimiRefreshLock returns the per-account mutex that serializes refresh +
// persist for one Kimi account. Concurrent card requests for the same account
// share one mutex, so only one can call RefreshToken + save at a time (no
// double rotation, no partial overwrites). Different accounts get different
// mutexes, so one account's refresh never blocks another.
func (s *Server) kimiRefreshLock(name string) *sync.Mutex {
	s.kimiRefreshLocksMu.Lock()
	defer s.kimiRefreshLocksMu.Unlock()
	if s.kimiRefreshLocks == nil {
		s.kimiRefreshLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := s.kimiRefreshLocks[name]
	if !ok {
		mu = &sync.Mutex{}
		s.kimiRefreshLocks[name] = mu
	}
	return mu
}

func (s *Server) Start(addr string) error {
	if addr != "" {
		s.addr = addr
	}
	return http.ListenAndServe(s.addr, s.Handler())
}

// Handler returns the server routes for embedding and tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/quota", func(w http.ResponseWriter, r *http.Request) {
		accs := s.curAccounts()
		if len(accs) == 0 {
			writeJSON(w, 200, map[string]any{"success": false, "error": "no account configured"})
			return
		}
		a := accs[0]
		q := &quota.OpenCodeGoQuerier{Cookie: a.Cookie, WorkspaceID: a.WorkspaceID}
		d, e := q.FetchQuota()
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		type result struct {
			Name    string           `json:"name"`
			Success bool             `json:"success"`
			Quota   *quota.QuotaData `json:"quota,omitempty"`
			Error   string           `json:"error,omitempty"`
		}
		accs := s.curAccounts()
		results := make([]result, len(accs))
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a Account) {
				defer wg.Done()
				q := &quota.OpenCodeGoQuerier{Cookie: a.Cookie, WorkspaceID: a.WorkspaceID}
				d, e := q.FetchQuota()
				if e != nil {
					results[i] = result{Name: a.Name, Success: false, Error: e.Error()}
				} else {
					results[i] = result{Name: a.Name, Success: true, Quota: d}
				}
			}(i, a)
		}
		wg.Wait()
		sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": results})
	})

	mux.HandleFunc("/api/ollama", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string           `json:"name"`
			Success bool             `json:"success"`
			Quota   *quota.QuotaData `json:"quota,omitempty"`
			Error   string           `json:"error,omitempty"`
		}
		accs := s.curOllama()
		cards := make([]card, len(accs))
		fetch := s.ollamaFetch
		if fetch == nil {
			fetch = func(a OllamaAccount) (*quota.QuotaData, error) {
				return (&quota.OllamaQuerier{Cookie: a.Cookie, UserAgent: a.UserAgent}).FetchQuota()
			}
		}
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a OllamaAccount) {
				defer wg.Done()
				d, err := fetch(a)
				if err != nil {
					cards[i] = card{Name: a.Name, Error: err.Error()}
					return
				}
				cards[i] = card{Name: a.Name, Success: true, Quota: d}
			}(i, a)
		}
		wg.Wait()
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": cards})
	})

	mux.HandleFunc("/api/balance", func(w http.ResponseWriter, r *http.Request) {
		d, e := s.deepseek.FetchBalance()
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})

	// /api/deepseek/accounts returns the config-saved DeepSeek accounts
	// as loading card shells without any remote fetch, each carrying a
	// per-account, non-sensitive login Generation. The sidebar polls
	// this after a login so a new card appears the moment the account is
	// written to config, independent of the (slow) FetchSummary/FetchUsage
	// round trips. A cancelled or failed login never writes the account,
	// so no ghost card can appear. Generation lets a re-login for an
	// account that already exists be detected by the NEW save of THIS
	// account — including a same-token re-login. The response exposes no
	// credential, only an integer counter.
	mux.HandleFunc("/api/deepseek/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name       string `json:"name"`
			Pending    bool   `json:"pending"`
			Generation int    `json:"generation"`
		}
		accs := s.curDeepSeek()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true, Generation: a.Generation})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": shells})
	})

	mux.HandleFunc("/api/deepseek", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string                     `json:"name"`
			Success bool                       `json:"success"`
			Summary *quota.DeepSeekSummary     `json:"summary,omitempty"`
			Models  []quota.DeepSeekModelUsage `json:"models,omitempty"`
			Error   string                     `json:"error,omitempty"`
		}
		accs := s.curDeepSeek()
		cards := make([]card, len(accs))
		now := time.Now()
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a DeepSeekAccount) {
				defer wg.Done()
				c := card{Name: a.Name}
				q := &quota.DeepSeekWebQuerier{Token: a.Token}
				sum, err := q.FetchSummary()
				if err != nil {
					c.Error = err.Error()
					cards[i] = c
					return
				}
				models, err := q.FetchUsage(now.Year(), int(now.Month()))
				if err != nil {
					c.Error = err.Error()
					cards[i] = c
					return
				}
				c.Success = true
				c.Summary = sum
				c.Models = models
				cards[i] = c
			}(i, a)
		}
		wg.Wait()
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": cards})
	})

	mux.HandleFunc("/api/deepseek/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		spawn := s.spawnDeepSeekLogin
		if spawn == nil {
			spawn = func(n string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"login-deepseek"}
				if n != "" {
					args = append(args, n)
				}
				return exec.Command(exe, args...).Start()
			}
		}
		if err := spawn(name); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	mux.HandleFunc("/api/opencode/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		exe, err := os.Executable()
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		args := []string{"login-opencode"}
		if name != "" {
			args = append(args, name)
		}
		cmd := exec.Command(exe, args...)
		if err := cmd.Start(); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	mux.HandleFunc("/api/ollama/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		exe, err := os.Executable()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		args := []string{"login-ollama"}
		if name != "" {
			args = append(args, name)
		}
		if err := exec.Command(exe, args...).Start(); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})

	// /api/kimi/accounts returns the config-saved Kimi accounts as loading
	// card shells with a per-account, non-sensitive login Generation — no
	// remote fetch, no access token. The sidebar polls this after a login so
	// a card appears the moment the account is saved.
	mux.HandleFunc("/api/kimi/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name       string `json:"name"`
			Pending    bool   `json:"pending"`
			Generation int    `json:"generation"`
		}
		accs := s.curKimi()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true, Generation: a.Generation})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": shells})
	})

	// /api/kimi concurrently fetches per-account two-meter quota, sorted by
	// name. One account failure produces an error only for that card and
	// does NOT suppress successful cards. The response excludes the access
	// token and all auth envelope values.
	mux.HandleFunc("/api/kimi", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string               `json:"name"`
			Success bool                 `json:"success"`
			Quota   *quota.KimiQuotaData `json:"quota,omitempty"`
			Error   string               `json:"error,omitempty"`
		}
		accs := s.curKimi()
		cards := make([]card, len(accs))
		fetch := s.kimiFetch
		if fetch == nil {
			// Production path: per-account refresh serialization + atomic
			// persist with failure propagation. The per-account mutex stops
			// two concurrent requests for the same account from racing the
			// RefreshToken endpoint (double rotation / partial overwrite). If
			// the refresh succeeded but persisting the rotated tokens failed,
			// the rotated state is NOT trusted and the card surfaces a
			// re-login-required error (old record preserved) instead of a
			// silent success with unpersisted rotated tokens.
			fetch = func(a KimiAccount) (*quota.KimiQuotaData, error) {
				// Serialize reload→refresh→persist for this account. The
				// cross-process file lock (when wired) stops a CLI quota-kimi
				// and a web request for the SAME account from racing the
				// RefreshToken endpoint / double-rotating across processes.
				// The in-process per-account mutex covers concurrent web
				// requests when no file lock is wired (tests).
				if s.kimiAccountLock != nil {
					release, lerr := s.kimiAccountLock(a.Name)
					if lerr != nil {
						return nil, fmt.Errorf("Kimi 账户 %q 刷新锁失败: %v", a.Name, lerr)
					}
					defer release()
				} else {
					mu := s.kimiRefreshLock(a.Name)
					mu.Lock()
					defer mu.Unlock()
				}
				// Re-read the LATEST saved credential inside the per-account
				// lock. A concurrent rotation may have already rotated+saved
				// this account's token since this request started; using the
				// request-time snapshot would refresh with a now-stale token
				// and fail. When the reloader is wired and the account is NOT
				// found (deleted mid-flight), FAIL HARD — do not fall back to
				// the stale snapshot and refresh a just-deleted account's
				// credential. When no reloader is wired (tests without a live
				// config), use the snapshot.
				acc := a
				if s.kimiReloadAccount != nil {
					latest, ok := s.kimiReloadAccount(a.Name)
					if !ok {
						return nil, fmt.Errorf("Kimi 账户 %q 已不存在", a.Name)
					}
					acc = latest
				}
				fetchRefresh := s.kimiFetchWithRefresh
				if fetchRefresh == nil {
					fetchRefresh = func(ctx context.Context, acc KimiAccount) (*quota.KimiQuotaData, *quota.RefreshResult, error) {
						q := &quota.KimiQuerier{AccessToken: acc.AccessToken, RefreshToken: acc.RefreshToken, Headers: acc.Headers}
						return q.FetchQuotaWithRefresh(ctx)
					}
				}
				data, rotated, err := fetchRefresh(r.Context(), acc)
				if err != nil {
					return nil, err
				}
				// Persist rotated tokens if the refresh ran and a save callback
				// is wired. A persist failure must surface, not be swallowed:
				// rotated tokens left unpersisted would make the saved envelope
				// stale, so the card reports re-login-required rather than a
				// success backed by un-saved credentials. The error never
				// carries the token.
				if rotated != nil && s.kimiRefreshSave != nil {
					if saveErr := s.kimiRefreshSave(a.Name, rotated.AccessToken, rotated.RefreshToken); saveErr != nil {
						return nil, fmt.Errorf("Kimi 账户 %q token 轮换保存失败，请重新登录", a.Name)
					}
				}
				return data, nil
			}
		}
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a KimiAccount) {
				defer wg.Done()
				d, err := fetch(a)
				if err != nil {
					cards[i] = card{Name: a.Name, Error: err.Error()}
					return
				}
				cards[i] = card{Name: a.Name, Success: true, Quota: d}
			}(i, a)
		}
		wg.Wait()
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": cards})
	})

	mux.HandleFunc("/api/kimi/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		spawn := s.spawnKimiLogin
		if spawn == nil {
			spawn = func(n string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"login-kimi"}
				if n != "" {
					args = append(args, n)
				}
				return exec.Command(exe, args...).Start()
			}
		}
		if err := spawn(name); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})

	// /api/open 拉起一个子进程，弹出 app 内窗口展示该账户对应服务商页面（注入登录态）。
	// 旧实现 cmd.Start() 后立即返回 success：子进程在浏览器启动前失败（如 OpenCode cookie
	// 被拒、DeepSeek 登录态恢复失败）时前端无任何反馈。现在用显式 ready/error 握手：子进程
	// 在页面就绪（导航+状态检查通过）后写握手文件 ready，在失败退出前写 error。/api/open
	// 等待握手文件（含子进程提前退出的兜底），而非任意固定秒数。
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		name := r.URL.Query().Get("name")
		if provider != "opencode" && provider != "deepseek" && provider != "ollama" && provider != "kimi" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "bad provider"})
			return
		}
		session := newOpenSession()
		spawn := s.spawnOpenPage
		if spawn == nil {
			spawn = func(p, n, sess string) (func() error, error) {
				exe, err := os.Executable()
				if err != nil {
					return nil, err
				}
				cmd := exec.Command(exe, "open-page", p, n)
				cmd.Env = append(os.Environ(), "FQS_OPEN_SESSION="+sess)
				// 观测（仅 debuglog 构建；release 编译期剔除，不写文件）：
				// 子进程 stdout/stderr（含浏览器 stderr）落盘，用于事后
				// 定位账户页流程卡在哪个阶段。句柄随包装后的 wait 关闭；
				// Start 失败时立即关闭。
				var logFile *os.File
				if openLogCapture {
					logPath := openLogPath(sess)
					if f, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); logErr == nil {
						logFile = f
						cmd.Stdout = f
						cmd.Stderr = f
						log.Printf("open-page: 子进程日志写入 %s", logPath)
					}
				}
				if err := cmd.Start(); err != nil {
					if logFile != nil {
						_ = logFile.Close()
					}
					return nil, err
				}
				if logFile != nil {
					wait := cmd.Wait
					return func() error {
						defer logFile.Close()
						return wait()
					}, nil
				}
				return cmd.Wait, nil
			}
		}
		wait, err := spawn(provider, name, session)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		waitErr := make(chan error, 1)
		go func() { waitErr <- wait() }()
		timeout := s.openHandshakeTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		status, errMsg, ok := waitForOpenHandshake(session, waitErr, timeout)
		_ = os.Remove(openHandshakePath(session))
		if ok && status == "ready" {
			writeJSON(w, 200, map[string]any{"success": true})
			return
		}
		if ok && status == "error" {
			msg := errMsg
			if msg == "" {
				msg = "账户页子进程报告失败"
			}
			writeJSON(w, 200, map[string]any{"success": false, "error": msg})
			return
		}
		// Handshake timed out — the page never signalled ready or error.
		// This is a real failure (e.g. older binary without the hook, or
		// the page flow hung before the ready point), NOT a silent
		// success. Surface it so the user is not left waiting.
		writeJSON(w, 200, map[string]any{"success": false, "error": "打开账户页超时：未收到就绪或错误信号"})
	})

	// /api/delete 删除某个账户（前端右键菜单二次确认后调用）。
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		name := r.URL.Query().Get("name")
		if provider != "opencode" && provider != "deepseek" && provider != "ollama" && provider != "kimi" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "bad provider"})
			return
		}
		if s.onDelete == nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "delete not supported"})
			return
		}
		if err := s.onDelete(provider, name); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		logs, e := storage.ReadOCGTLogs(storage.OCGTLogDir())
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		daily := storage.CalculateDailyStats(logs, 7)
		type DayStat struct {
			Date         string `json:"date"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
			TotalTokens  int    `json:"total_tokens"`
			RequestCount int    `json:"request_count"`
		}
		var list []DayStat
		for _, s := range daily {
			list = append(list, DayStat{s.Date, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})

	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		logs, e := storage.ReadOCGTLogs(storage.OCGTLogDir())
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		r.ParseForm()
		var models map[string]storage.TokenStatsByModel
		if from := r.Form.Get("from"); from != "" {
			fromT, err1 := time.Parse("2006-01-02", from)
			toT, err2 := time.Parse("2006-01-02", r.Form.Get("to"))
			if err1 == nil && err2 == nil {
				toT = toT.Add(24*time.Hour - time.Second)
				models = storage.CalculateModelStatsByRange(logs, fromT, toT)
			} else {
				days := 7
				if d := r.Form.Get("days"); d != "" {
					if n, err := fmt.Sscanf(d, "%d", &days); err != nil || n != 1 || days < 1 {
						days = 7
					}
				}
				if days == 1 {
					now := time.Now()
					start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
					models = storage.CalculateModelStatsByRange(logs, start, start.Add(24*time.Hour-time.Second))
				} else {
					models = storage.CalculateModelStats(logs, days)
				}
			}
		} else {
			days := 7
			if d := r.Form.Get("days"); d != "" {
				if n, err := fmt.Sscanf(d, "%d", &days); err != nil || n != 1 || days < 1 {
					days = 7
				}
			}
			if days == 1 {
				now := time.Now()
				start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
				models = storage.CalculateModelStatsByRange(logs, start, start.Add(24*time.Hour-time.Second))
			} else {
				models = storage.CalculateModelStats(logs, days)
			}
		}
		type MStat struct {
			Model        string `json:"model"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
			TotalTokens  int    `json:"total_tokens"`
			RequestCount int    `json:"request_count"`
		}
		var list []MStat
		for _, s := range models {
			list = append(list, MStat{s.Model, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].TotalTokens > list[j].TotalTokens })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now()})
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "bye"})
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }()
	})
	mux.HandleFunc("/api/pin", func(w http.ResponseWriter, r *http.Request) {
		state.Pinned = !state.Pinned
		writeJSON(w, 200, map[string]any{"pinned": state.Pinned})
	})
	mux.HandleFunc("/api/pin-state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"pinned": state.Pinned})
	})
	mux.HandleFunc("/api/position", func(w http.ResponseWriter, r *http.Request) {
		if yStr := r.URL.Query().Get("y"); yStr != "" {
			var y int
			if _, err := fmt.Sscanf(yStr, "%d", &y); err == nil && y >= 0 && y < 5000 {
				state.PanelY = y
			}
		}
		writeJSON(w, 200, map[string]any{"y": state.PanelY})
	})

	mux.HandleFunc("/api/winsize", func(w http.ResponseWriter, r *http.Request) {
		var ww, hh int
		fmt.Sscanf(r.URL.Query().Get("w"), "%d", &ww)
		fmt.Sscanf(r.URL.Query().Get("h"), "%d", &hh)
		if ww > 0 && hh > 0 && s.onWinSize != nil {
			s.onWinSize(ww, hh)
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	sub, _ := fs.Sub(webAssets, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func writeJSON(w http.ResponseWriter, s int, d any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	json.NewEncoder(w).Encode(d)
}

// openSessionSeq makes session ids unique within a process without
// Date/random. It is incremented atomically so concurrent /api/open
// requests (and the -race detector) never collide or race on it.
var openSessionSeq int64

// newOpenSession returns a unique handshake session id. It is monotonically
// increasing (a process counter) so two concurrent opens never collide. The
// counter is atomic; time.Now is read only here (this is a server runtime,
// not the workflow sandbox).
func newOpenSession() string {
	seq := atomic.AddInt64(&openSessionSeq, 1)
	return fmt.Sprintf("fqs-open-%d-%d", time.Now().UnixNano(), seq)
}

// openHandshakePath returns the ready/error handshake file for a session.
// The open-page subprocess writes it; /api/open reads it.
func openHandshakePath(session string) string {
	return filepath.Join(os.TempDir(), session+".json")
}

// openLogPath 返回会话对应的子进程日志文件。仅 debuglog 构建
// （openLogCapture=true）的 /api/open 默认 spawn 使用：把 open-page
// 子进程（及其拉起浏览器的 stderr）输出写到该文件，账户页卡住或
// 不跳转时可据此定位阶段。
func openLogPath(session string) string {
	return filepath.Join(os.TempDir(), session+".log")
}

// WriteOpenHandshake writes a ready/error handshake record for a session. The
// open-page CLI calls it from the sidebar.OpenPageReady hook (ready) or on
// failure exit (error). status is "ready" or "error"; errMsg carries a
// credential-free error message for the "error" case.
func WriteOpenHandshake(session, status, errMsg string) {
	if session == "" {
		return
	}
	path := openHandshakePath(session)
	data, _ := json.Marshal(map[string]any{"status": status, "error": errMsg})
	_ = os.WriteFile(path, data, 0o600)
}

// waitForOpenHandshake polls the session handshake file until it appears
// (ready/error) or the deadline passes. If the subprocess exits (waitErr
// delivers) before a handshake, that is treated as an error so a runtime
// failure (cookie rejected, restore failed) is surfaced — not swallowed.
// Returns (status, errMsg, ok). ok=false means the handshake timed out.
func waitForOpenHandshake(session string, waitErr <-chan error, timeout time.Duration) (status, errMsg string, ok bool) {
	path := openHandshakePath(session)
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(path); err == nil {
			var h struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if json.Unmarshal(data, &h) == nil && h.Status != "" {
				return h.Status, h.Error, true
			}
		}
		select {
		case err := <-waitErr:
			// Subprocess exited before ready — surface the runtime failure.
			msg := "账户页子进程已退出"
			if err != nil {
				msg = err.Error()
			}
			return "error", msg, true
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return "", "", false
		}
	}
}
