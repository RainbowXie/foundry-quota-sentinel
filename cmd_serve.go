package main

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"time"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/sidebar"
	"foundry-quota-sentinel/internal/web"
)

// ocgtPort 解析并校验环境变量 FQS_PORT。
// 若未配置或为空，使用默认端口 8788；若配置了非法端口（非数字或超出 1~65535 范围），
// 打印明确警告并安全回退至 8788，防止服务端在无效端口绑定失败或绑定动态端口 (:0) 导致客户端与服务端端口分裂。
func ocgtPort() int {
	const defaultPort = 8788
	p := os.Getenv("FQS_PORT")
	if p == "" {
		return defaultPort
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "警告: 环境变量 FQS_PORT=%q 非法（有效范围 1-65535），回退至默认端口 %d\n", p, defaultPort)
		return defaultPort
	}
	return port
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
	port := ocgtPort()
	portStr := strconv.Itoa(port)
	go func() {
		if err := srv.Start(":" + portStr); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	}()
	fmt.Printf("API 服务已启动: http://127.0.0.1:%s\n", portStr)
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
	port := ocgtPort()
	portStr := strconv.Itoa(port)
	go func() {
		if err := srv.Start(":" + portStr); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	}()
	waitServerReady("127.0.0.1:"+portStr, 5*time.Second)

	// 服务端监听与 Webview 窗口严格使用同一校验后的端口，消除端口分裂与无效回退风险。
	sb := sidebar.New(port, cfg.WindowW, cfg.WindowH)
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
