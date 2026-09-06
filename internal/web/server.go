package web

import (
	"context"
	"embed"
	"net/http"
	"sync"
	"time"

	"foundry-quota-sentinel/pkg/sdk/providers/commandcode"
	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
	"foundry-quota-sentinel/pkg/sdk/providers/ollama"
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

//go:embed static/*
var webAssets embed.FS

// Account 记录 OpenCode 账号视图。
type Account struct {
	Name        string
	Cookie      string
	WorkspaceID string
}

// DeepSeekAccount 记录 DeepSeek 账号视图。
type DeepSeekAccount struct {
	Name       string
	Token      string
	Generation int
}

// OllamaAccount 记录 Ollama 账号视图。
type OllamaAccount struct {
	Name      string
	Cookie    string
	UserAgent string
}

// KimiAccount 记录 Kimi 账号视图。
type KimiAccount struct {
	Name         string
	AccessToken  string
	RefreshToken string
	Headers      map[string]string
	Generation   int
}

// CommandCodeAccount 记录 CommandCode 账号视图。
type CommandCodeAccount struct {
	Name     string
	Cookie   string
	UserName string
}

// Server 负责处理所有 HTTP API 请求与嵌入式静态资源托管。
type Server struct {
	addr                string
	accounts            []Account
	accountsFn          func() []Account
	dsAccounts          []DeepSeekAccount
	dsFn                func() []DeepSeekAccount
	ollamaAccounts      []OllamaAccount
	ollamaFn            func() []OllamaAccount
	ollamaFetch         func(OllamaAccount) (*ollama.QuotaData, error)
	kimiAccounts        []KimiAccount
	kimiFn              func() []KimiAccount
	commandCodeAccounts []CommandCodeAccount
	commandCodeFn       func() []CommandCodeAccount
	commandCodeFetch    func(CommandCodeAccount) (*commandcode.QuotaData, error)
	openCodeFetch       func(Account) (*opencode.QuotaData, error)
	kimiFetch           func(KimiAccount) (*kimi.KimiQuotaData, error)
	kimiFetchWithRefresh func(ctx context.Context, a KimiAccount) (*kimi.KimiQuotaData, *kimi.RefreshResult, error)
	kimiReloadAccount   func(name string) (KimiAccount, bool)
	kimiAccountLock     func(name string) (release func(), err error)
	kimiRefreshSave     func(name, accessToken, refreshToken string) error
	kimiRefreshLocksMu  sync.Mutex
	kimiRefreshLocks    map[string]*sync.Mutex
	deepseek            *deepseek.DeepSeekQuerier
	onWinSize           func(w, h int)
	onDelete            func(provider, name string) error
	spawnDeepSeekLogin    func(name string) error
	spawnKimiLogin        func(name string) error
	spawnCommandCodeLogin func(name string) error
	spawnOpenCodeLogin    func(name string) error
	spawnOpenPage         func(provider, name, session string) (wait func() error, err error)
	openHandshakeTimeout  time.Duration
}

// NewServer 创建 Server 实例。
func NewServer(accounts []Account) *Server {
	return &Server{
		addr:     ":8788",
		accounts: accounts,
		deepseek: deepseek.NewDeepSeekQuerier(),
	}
}

func (s *Server) SetDeepSeekAccounts(accs []DeepSeekAccount) { s.dsAccounts = accs }
func (s *Server) SetOllamaAccounts(accs []OllamaAccount)     { s.ollamaAccounts = accs }
func (s *Server) SetAccountsProvider(fn func() []Account)    { s.accountsFn = fn }
func (s *Server) SetDeepSeekProvider(fn func() []DeepSeekAccount) { s.dsFn = fn }
func (s *Server) SetOllamaProvider(fn func() []OllamaAccount)     { s.ollamaFn = fn }
func (s *Server) SetKimiProvider(fn func() []KimiAccount)         { s.kimiFn = fn }
func (s *Server) SetCommandCodeProvider(fn func() []CommandCodeAccount) { s.commandCodeFn = fn }
func (s *Server) SetKimiReloadAccount(fn func(name string) (KimiAccount, bool)) {
	s.kimiReloadAccount = fn
}
func (s *Server) SetKimiAccountLock(fn func(name string) (release func(), err error)) {
	s.kimiAccountLock = fn
}
func (s *Server) SetKimiRefreshSave(fn func(name, accessToken, refreshToken string) error) {
	s.kimiRefreshSave = fn
}
func (s *Server) SetWinSizeHandler(fn func(w, h int)) { s.onWinSize = fn }
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

func (s *Server) curCommandCode() []CommandCodeAccount {
	if s.commandCodeFn != nil {
		return s.commandCodeFn()
	}
	return s.commandCodeAccounts
}

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

// Handler 初始化并注册各 Provider 的路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	s.registerCommonRoutes(mux)
	s.registerOpenPageRoutes(mux)
	s.registerOpenCodeRoutes(mux)
	s.registerDeepSeekRoutes(mux)
	s.registerKimiRoutes(mux)
	s.registerCommandCodeRoutes(mux)
	s.registerOllamaRoutes(mux)

	return mux
}
