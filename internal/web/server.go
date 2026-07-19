package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
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
}

type OllamaAccount struct {
	Name      string
	Cookie    string
	UserAgent string
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
	deepseek       *quota.DeepSeekQuerier
	onWinSize      func(w, h int)
	onDelete       func(provider, name string) error
	// deepseekRevision reports a non-credential config revision (e.g.
	// the config file's mtime) so the sidebar can detect that a login
	// subprocess wrote a NEW config without comparing tokens. A re-login
	// for an account that already exists must be detected by revision
	// change, not by name presence, or the poll stops on the stale
	// config and the new token only shows up on the 30s interval.
	deepseekRevision func() int64
	// spawnDeepSeekLogin launches the login-deepseek subprocess. The
	// default uses os.Executable + exec.Command; tests inject a
	// failure to prove /api/deepseek/login returns success=false.
	spawnDeepSeekLogin func(name string) error
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

// SetDeepSeekRevision sets the non-credential config revision source
// used by /api/deepseek/accounts and /api/deepseek/login so the sidebar
// can detect a re-login save by revision change rather than name match.
func (s *Server) SetDeepSeekRevision(fn func() int64) { s.deepseekRevision = fn }

// SetOllamaProvider 设置动态 Ollama 账户来源，每次请求实时读取。
func (s *Server) SetOllamaProvider(fn func() []OllamaAccount) { s.ollamaFn = fn }

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

// deepSeekRevision returns the non-credential config revision, or 0
// when no provider is wired (e.g. in tests that do not exercise
// revision-aware polling).
func (s *Server) deepSeekRevision() int64 {
	if s.deepseekRevision != nil {
		return s.deepseekRevision()
	}
	return 0
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
	// as loading card shells without any remote fetch, plus a non-
	// credential config revision. The sidebar polls this after a login
	// so a new card appears the moment the account is written to config,
	// independent of the (slow) FetchSummary/FetchUsage round trips. A
	// cancelled or failed login never writes the account, so no ghost
	// card can appear here. The revision lets a re-login for an account
	// that already exists be detected by the NEW save, not by name.
	mux.HandleFunc("/api/deepseek/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name    string `json:"name"`
			Pending bool   `json:"pending"`
		}
		accs := s.curDeepSeek()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"data":     shells,
			"revision": s.deepSeekRevision(),
		})
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
		// Capture the config revision BEFORE spawning the login
		// subprocess. The sidebar holds this pre-login revision and
		// polls /api/deepseek/accounts until the revision changes, so a
		// re-login for an account that already exists is detected by
		// the new save rather than by a stale name match.
		preRev := s.deepSeekRevision()
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
			writeJSON(w, 200, map[string]any{
				"success":  false,
				"revision": preRev,
				"error":    err.Error(),
			})
			return
		}
		writeJSON(w, 200, map[string]any{
			"success":  true,
			"revision": preRev,
		})
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

	// /api/open 拉起一个子进程，弹出 app 内窗口展示该账户对应服务商页面（注入登录态）。
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		name := r.URL.Query().Get("name")
		if provider != "opencode" && provider != "deepseek" && provider != "ollama" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "bad provider"})
			return
		}
		exe, err := os.Executable()
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		cmd := exec.Command(exe, "open-page", provider, name)
		if err := cmd.Start(); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	// /api/delete 删除某个账户（前端右键菜单二次确认后调用）。
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		name := r.URL.Query().Get("name")
		if provider != "opencode" && provider != "deepseek" && provider != "ollama" {
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
