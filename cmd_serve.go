package main

import (
	"fmt"
	"net"
	"os"
	"sort"
	"time"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/sidebar"
	"foundry-quota-sentinel/internal/web"
)

func ocgtPort() string {
	if p := os.Getenv("FQS_PORT"); p != "" {
		return p
	}
	return "8788"
}

func accountsFromConfig(conf *config.Config) []web.Account {
	var accs []web.Account
	for name, p := range conf.Profiles {
		if p.Cookie != "" && p.WorkspaceID != "" {
			accs = append(accs, web.Account{Name: name, Cookie: p.Cookie, WorkspaceID: p.WorkspaceID})
		}
	}
	if len(accs) == 0 {
		ck, wks := os.Getenv("OPENCODE_GO_AUTH_COOKIE"), os.Getenv("OPENCODE_GO_WORKSPACE_ID")
		if ck != "" && wks != "" {
			accs = append(accs, web.Account{Name: "默认", Cookie: ck, WorkspaceID: wks})
		}
	}
	sort.Slice(accs, func(i, j int) bool { return accs[i].Name < accs[j].Name })
	return accs
}

func deepseekFromConfig(conf *config.Config) []web.DeepSeekAccount {
	out := make([]web.DeepSeekAccount, 0, len(conf.DeepSeekAccounts))
	for _, a := range conf.DeepSeekAccounts {
		if a.Token == "" {
			continue
		}
		out = append(out, web.DeepSeekAccount{
			Name:       a.Name,
			Token:      a.Token,
			Generation: a.Generation,
		})
	}
	return out
}

func ollamaFromConfig(conf *config.Config) []web.OllamaAccount {
	out := make([]web.OllamaAccount, 0, len(conf.OllamaAccounts))
	for _, a := range conf.OllamaAccounts {
		if a.Cookie == "" {
			continue
		}
		out = append(out, web.OllamaAccount{Name: a.Name, Cookie: a.Cookie, UserAgent: a.UserAgent})
	}
	return out
}

func commandcodeFromConfig(conf *config.Config) []web.CommandCodeAccount {
	out := make([]web.CommandCodeAccount, 0, len(conf.CommandCodeAccounts))
	for _, a := range conf.CommandCodeAccounts {
		if a.Cookie == "" || a.UserName == "" {
			continue
		}
		out = append(out, web.CommandCodeAccount{Name: a.Name, Cookie: a.Cookie, UserName: a.UserName})
	}
	return out
}

func kimiFromConfig(conf *config.Config) []web.KimiAccount {
	out := make([]web.KimiAccount, 0, len(conf.KimiAccounts))
	for _, a := range conf.KimiAccounts {
		token := a.Auth.AccessToken()
		if token == "" {
			continue
		}
		out = append(out, web.KimiAccount{
			Name:         a.Name,
			AccessToken:  token,
			RefreshToken: a.Auth.RefreshToken(),
			Headers:      kimiEnvelopeHeaders(&a.Auth),
			Generation:   a.Generation,
		})
	}
	return out
}

func kimiAccountFromConfig(name string) (web.KimiAccount, bool) {
	for _, a := range kimiFromConfig(config.Load()) {
		if a.Name == name {
			return a, true
		}
	}
	return web.KimiAccount{}, false
}

func cmdServe() {
	if len(os.Args) > 2 && os.Args[2] == "--sidebar" {
		startSidebar()
		return
	}

	srv := web.NewServer(accountsFromConfig(cfg))
	srv.SetAccountsProvider(func() []web.Account { return accountsFromConfig(config.Load()) })
	srv.SetDeepSeekProvider(func() []web.DeepSeekAccount { return deepseekFromConfig(config.Load()) })
	srv.SetOllamaProvider(func() []web.OllamaAccount { return ollamaFromConfig(config.Load()) })
	srv.SetKimiProvider(func() []web.KimiAccount { return kimiFromConfig(config.Load()) })
	srv.SetCommandCodeProvider(func() []web.CommandCodeAccount { return commandcodeFromConfig(config.Load()) })
	srv.SetKimiReloadAccount(kimiAccountFromConfig)
	srv.SetKimiAccountLock(config.AcquireKimiAccountLock)
	srv.SetKimiRefreshSave(func(name, accessToken, refreshToken string) error {
		return config.SaveKimiTokens(name, accessToken, refreshToken)
	})
	srv.SetDeleteHandler(deleteAccountFromConfig)
	go func() {
		if err := srv.Start(":" + ocgtPort()); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	}()
	fmt.Println("API 服务已启动: http://127.0.0.1:8788")
	select {}
}

func startSidebar() {
	srv := web.NewServer(accountsFromConfig(cfg))
	srv.SetAccountsProvider(func() []web.Account { return accountsFromConfig(config.Load()) })
	srv.SetDeepSeekProvider(func() []web.DeepSeekAccount { return deepseekFromConfig(config.Load()) })
	srv.SetOllamaProvider(func() []web.OllamaAccount { return ollamaFromConfig(config.Load()) })
	srv.SetKimiProvider(func() []web.KimiAccount { return kimiFromConfig(config.Load()) })
	srv.SetCommandCodeProvider(func() []web.CommandCodeAccount { return commandcodeFromConfig(config.Load()) })
	srv.SetKimiReloadAccount(kimiAccountFromConfig)
	srv.SetKimiAccountLock(config.AcquireKimiAccountLock)
	srv.SetKimiRefreshSave(func(name, accessToken, refreshToken string) error {
		return config.SaveKimiTokens(name, accessToken, refreshToken)
	})
	srv.SetWinSizeHandler(func(w, h int) { config.SaveWindowSize(w, h) })
	srv.SetDeleteHandler(deleteAccountFromConfig)
	go func() {
		if err := srv.Start(":" + ocgtPort()); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	}()
	waitServerReady("127.0.0.1:"+ocgtPort(), 5*time.Second)
	sb := sidebar.New(8788, cfg.WindowW, cfg.WindowH)
	sb.Run()
}

func waitServerReady(addr string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
