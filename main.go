package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/formatter"
	"foundry-quota-sentinel/internal/quota"
	"foundry-quota-sentinel/internal/sidebar"
	"foundry-quota-sentinel/internal/storage"
	"foundry-quota-sentinel/internal/web"
)

var currencySymbols = map[string]string{"CNY": "¥", "USD": "$", "EUR": "€", "JPY": "¥", "GBP": "£"}
var cfg *config.Config
var inputReader = bufio.NewScanner(os.Stdin)

var version = "0.10.5"

func init() { cfg = config.Load() }

func currencySymbol(code string) string {
	if s, ok := currencySymbols[code]; ok {
		return s
	}
	return code + " "
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }
func ocgtPort() string {
	if p := os.Getenv("FQS_PORT"); p != "" {
		return p
	}
	return "8788"
}

func makeQuotaQuerier() *quota.OpenCodeGoQuerier {
	q := quota.NewOpenCodeGoQuerier()
	if q.Cookie == "" || q.WorkspaceID == "" {
		if p, ok := cfg.GetActiveProfile(); ok {
			if q.Cookie == "" {
				q.Cookie = p.Cookie
			}
			if q.WorkspaceID == "" {
				q.WorkspaceID = p.WorkspaceID
			}
		}
	}
	return q
}

func makeDeepSeekQuerier() *quota.DeepSeekQuerier {
	q := quota.NewDeepSeekQuerier()
	if q.APIKey == "" {
		if p, ok := cfg.GetActiveProfile(); ok && q.APIKey == "" {
			q.APIKey = p.DeepSeekAPIKey
		}
	}
	return q
}

func mask(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:4] + "****" + s[len(s)-4:]
}

func readLineDefault(label, defaultVal string) string {
	masked := defaultVal
	if len(masked) > 8 {
		masked = mask(masked)
	}
	fmt.Printf("  %s [%s]: ", label, masked)
	if inputReader.Scan() {
		val := strings.TrimSpace(inputReader.Text())
		if val == "" {
			return defaultVal
		}
		return val
	}
	return defaultVal
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

// commandcodeFromConfig converts saved commandcode accounts to the web-layer
// view. An account without a cookie is skipped. The cookie stays server-side
// — the cards/accounts endpoints never serialize it.
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

// kimiFromConfig converts saved Kimi accounts to the web-layer view. The
// access token is read from the versioned auth envelope; an account whose
// envelope carries no token is skipped. The token stays server-side — the
// cards endpoint never serializes it.
func kimiFromConfig(conf *config.Config) []web.KimiAccount {
	out := make([]web.KimiAccount, 0, len(conf.KimiAccounts))
	for _, a := range conf.KimiAccounts {
		token := a.Auth.AccessToken()
		if token == "" {
			continue
		}
		out = append(out, web.KimiAccount{Name: a.Name, AccessToken: token, RefreshToken: a.Auth.RefreshToken(), Headers: kimiEnvelopeHeaders(&a.Auth), Generation: a.Generation})
	}
	return out
}

// kimiAccountFromConfig returns the latest saved KimiAccount (web-layer view)
// by name, read from a fresh config load. Used as the per-account in-lock
// reloader so a concurrent rotation's saved token is observed instead of a
// stale request-time snapshot. Returns ok=false if the account no longer
// exists (deleted mid-flight) — the caller then skips/refreshes with the
// snapshot, which will fail closed if the token is gone.
func kimiAccountFromConfig(name string) (web.KimiAccount, bool) {
	for _, a := range kimiFromConfig(config.Load()) {
		if a.Name == name {
			return a, true
		}
	}
	return web.KimiAccount{}, false
}

// latestKimiAccount returns the latest saved config.KimiAccount by name (config
// layer, with the full envelope), read from a fresh config load. Used by the
// CLI printKimiQuota inside the per-account cross-process lock so it reloads
// the latest credential (not a stale snapshot) before refreshing. ok=false if
// the account no longer exists.
func latestKimiAccount(name string) (config.KimiAccount, bool) {
	for _, a := range config.Load().KimiAccounts {
		if a.Name == name {
			return a, true
		}
	}
	return config.KimiAccount{}, false
}

// kimiReplayRefresh is the refresh step used by kimiReplayEnvelope, injectable
// for tests so no real network call is made. It receives the latest account
// and returns a non-nil RefreshResult if the access token was expired and
// rotation happened (caller persists it). Default builds a KimiQuerier from the
// account and runs FetchQuotaWithRefresh.
var kimiReplayRefresh = func(acc *config.KimiAccount) (*quota.RefreshResult, error) {
	q := &quota.KimiQuerier{AccessToken: acc.Auth.AccessToken(), RefreshToken: acc.Auth.RefreshToken(), Headers: kimiEnvelopeHeaders(&acc.Auth)}
	_, rotated, err := q.FetchQuotaWithRefresh(context.Background())
	return rotated, err
}

// kimiReplayEnvelope prepares the authenticated envelope for the open-page
// replay, under the per-account cross-process lock. It reloads the LATEST
// on-disk account (not the process-start snapshot), runs
// FetchQuotaWithRefresh→SaveKimiTokens so an expired access token is rotated
// and persisted BEFORE the envelope is encoded, then encodes the latest
// envelope. This guarantees:
//   - if a concurrent Web/CLI rotation already rotated the token, the replay
//     uses the rotated token (not a stale snapshot);
//   - if the access token is expired, the page's in-flight rotation is
//     persisted to disk so the on-disk credential is not invalidated.
//
// The lock is released before returning; the long browser replay runs after,
// so it does not block concurrent refreshes for the page-open duration.
func kimiReplayEnvelope(name string) (string, error) {
	release, lerr := config.AcquireKimiAccountLock(name)
	if lerr != nil {
		return "", fmt.Errorf("Kimi 账户 %q 页面锁失败: %v", name, lerr)
	}
	defer release()
	// Reload the latest on-disk account inside the lock.
	acc, ok := latestKimiAccount(name)
	if !ok {
		return "", fmt.Errorf("Kimi 账户 %q 不存在", name)
	}
	// Refresh (and rotate+persist if expired) before encoding, so the replayed
	// envelope carries the rotated token and disk stays current.
	rotated, rerr := kimiReplayRefresh(&acc)
	if rerr != nil {
		return "", fmt.Errorf("Kimi 账户 %q 刷新失败: %v", name, rerr)
	}
	if rotated != nil {
		if saveErr := config.SaveKimiTokens(name, rotated.AccessToken, rotated.RefreshToken); saveErr != nil {
			return "", fmt.Errorf("Kimi 账户 %q token 轮换保存失败，请重新登录", name)
		}
		// Reload the persisted envelope so the encoded JSON carries the rotated
		// tokens just written.
		acc, ok = latestKimiAccount(name)
		if !ok {
			return "", fmt.Errorf("Kimi 账户 %q 已不存在", name)
		}
	}
	envJSON, err := acc.Auth.Encode()
	if err != nil {
		return "", fmt.Errorf("Kimi 账户 %q 凭证编码失败: %v", name, err)
	}
	return string(envJSON), nil
}

// kimiPageRotationSaver returns the sidebar.KimiPageRotationSave closure for
// open-page: it persists the membership SPA's OWN in-page rotation (the SPA
// refreshes an expired access token itself, rotating BOTH tokens). The whole
// compare-and-swap runs under the per-account cross-process lock: the
// SPA-rotated pair is persisted via the shared config.SaveKimiTokens ONLY
// when the on-disk pair still equals (prevAccess, prevRefresh) — BOTH fields
// are compared, so a concurrent CLI/Web rotation that moved disk ahead makes
// the save SKIP (persisted=false, never a false persisted) instead of
// regressing disk to the page's older pair. The tokens never appear in errors.
func kimiPageRotationSaver(name string) func(prevAccess, prevRefresh, newAccess, newRefresh string) (bool, error) {
	return func(prevAccess, prevRefresh, newAccess, newRefresh string) (bool, error) {
		release, err := config.AcquireKimiAccountLock(name)
		if err != nil {
			return false, fmt.Errorf("Kimi 账户 %q 页面轮换锁失败: %v", name, err)
		}
		defer release()
		latest, ok := latestKimiAccount(name)
		if !ok {
			return false, fmt.Errorf("Kimi 账户 %q 已不存在", name)
		}
		if latest.Auth.AccessToken() != prevAccess || latest.Auth.RefreshToken() != prevRefresh {
			// Disk moved ahead (a concurrent rotation already persisted newer
			// tokens) — skip; do not regress disk to the page's older pair.
			return false, nil
		}
		if err := config.SaveKimiTokens(name, newAccess, newRefresh); err != nil {
			return false, fmt.Errorf("Kimi 账户 %q 页面轮换保存失败，请重新登录", name)
		}
		return true, nil
	}
}

// kimiPersistRotated atomically persists rotated access/refresh tokens for a
// named Kimi account after an auto-refresh. It delegates to the shared,
// config-wide-locked config.SaveKimiTokens (serialized reload + atomic save),
// so concurrent different-account rotations never overwrite each other's
// freshly-rotated token with a stale snapshot. The caller (printKimiQuota)
// treats a returned error as a HARD failure per the credential-safety rule:
// rotated tokens left unpersisted must not be trusted, so the CLI surfaces the
// error rather than printing a quota backed by un-saved credentials.
func kimiPersistRotated(name string, rotated *quota.RefreshResult) error {
	return config.SaveKimiTokens(name, rotated.AccessToken, rotated.RefreshToken)
}
func kimiEnvelopeHeaders(env *config.KimiAuthEnvelope) map[string]string {
	if env == nil {
		return nil
	}
	// envelope field name → HTTP header name.
	f2h := map[string]string{
		"cookie":          "cookie",
		"x_msh_device_id": "x-msh-device-id",
		"x_traffic_id":    "x-traffic-id",
		"x_msh_platform":  "x-msh-platform",
		"x_msh_version":   "x-msh-version",
		"x_language":      "x-language",
		"r_timezone":      "r-timezone",
		"user_agent":      "user-agent",
	}
	out := map[string]string{}
	for field, header := range f2h {
		if v, ok := env.Field(field); ok && v != "" {
			out[header] = v
		}
	}
	return out
}

func main() {
	if len(os.Args) < 2 {
		startSidebar()
		return
	}
	switch os.Args[1] {
	case "quota":
		cmdQuota()
	case "balance":
		cmdBalance()
	case "history":
		cmdHistory()
	case "watch":
		cmdWatch()
	case "config":
		cmdConfigMain()
	case "serve":
		cmdServe()
	case "login-deepseek":
		cmdLoginDeepSeek()
	case "login-opencode":
		cmdLoginOpenCode()
	case "login-commandcode":
		cmdLoginCommandCode()
	case "login-ollama":
		cmdLoginOllama()
	case "login-kimi":
		cmdLoginKimi()
	case "quota-kimi":
		cmdQuotaKimi()
	case "open-page":
		cmdOpenPage()
	case "_locktest":
		// Hidden cross-process-lock test helper. Usage: _locktest <mode> <name>
		//   account: acquire the per-account cross-process lock, hold <holdMs>,
		//           then write a marker field into the account's token. Used by
		//   TestCrossProcessAccountLockSerializes to prove two processes do not
		//   double-rotate the same account.
		//   global: acquire the global config lock, hold, then Mutate a window
		//           size. Used by TestCrossProcessConfigLockSerializes.
		cmdLockTest()
	case "version", "-v", "--version":
		fmt.Println("foundry-quota-sentinel v" + version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// deleteAccountFromConfig 按 provider 从配置文件删除账户并保存（供前端 /api/delete 调用）。
// Routed through config.Mutate so the delete shares the config-wide write
// transaction lock with token rotation / login / window-save — a concurrent
// rotation cannot be overwritten by this delete's stale snapshot.
func deleteAccountFromConfig(provider, name string) error {
	return config.Mutate(func(c *config.Config) error {
		switch provider {
		case "opencode":
			return c.DeleteProfile(name)
		case "deepseek":
			return c.DeleteDeepSeekAccount(name)
		case "ollama":
			return c.DeleteOllamaAccount(name)
		case "kimi":
			return c.DeleteKimiAccount(name)
		case "commandcode":
			return c.DeleteCommandCodeAccount(name)
		default:
			return fmt.Errorf("未知 provider: %s", provider)
		}
	})
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
		// Delegated to the shared, config-wide-locked config.SaveKimiTokens:
		// serialized reload + atomic save, so concurrent different-account
		// rotations never overwrite each other's freshly-rotated token.
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
	// 轮询等服务器真正起来再开窗口，避免 webview 抢跑导致 "connection refused"
	waitServerReady("127.0.0.1:"+ocgtPort(), 5*time.Second)
	sb := sidebar.New(8788, cfg.WindowW, cfg.WindowH)
	sb.Run()
}

// waitServerReady 轮询直到本地服务器开始监听（或超时）。
func waitServerReady(addr string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func cmdQuota() {
	q := makeQuotaQuerier()
	data, err := q.FetchQuota()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		showConfigHint()
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  OpenCode Go 套餐额度")
	fmt.Println("  (涵盖所有通过套餐使用的模型)")
	fmt.Println("----------------------------------------")
	fmt.Printf("  Rolling: %s  reset in %s\n", formatter.ProgressBar(data.Rolling.UsagePercent, 18), data.Rolling.ResetDisplay)
	fmt.Printf("  Weekly:  %s  reset in %s\n", formatter.ProgressBar(data.Weekly.UsagePercent, 18), data.Weekly.ResetDisplay)
	if data.Monthly != nil {
		fmt.Printf("  Monthly: %s  reset in %s\n", formatter.ProgressBar(data.Monthly.UsagePercent, 18), data.Monthly.ResetDisplay)
	} else {
		fmt.Println("  Monthly: 无限额度")
	}
	fmt.Println("========================================")
	fmt.Printf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05"))
}

func cmdBalance() {
	q := makeDeepSeekQuerier()
	data, err := q.FetchBalance()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
	sym := currencySymbol(data.Currency)
	fmt.Printf("\nDeepSeek 账户余额: %s%.2f (%s)\n", sym, data.TotalBalance, data.Currency)
	if data.GrantedBalance > 0 {
		fmt.Printf("  赠送余额:      %s%.2f\n", sym, data.GrantedBalance)
	}
	if data.ToppedUpBalance > 0 {
		fmt.Printf("  充值余额:      %s%.2f\n", sym, data.ToppedUpBalance)
	}
	fmt.Println("\n(此为 DeepSeek 独立账户余额，与 OpenCode Go 套餐无关)")
}

func cmdHistory() {
	logs, err := storage.ReadOCGTLogs(storage.OCGTLogDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取历史日志失败: %v\n", err)
		fmt.Fprintln(os.Stderr, "请确认 ocgt 已运行并有请求记录")
		os.Exit(1)
	}
	daily := storage.CalculateDailyStats(logs, 7)
	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("  最近 7 天 Token 消耗")
	fmt.Println("----------------------------------------------")
	dates := make([]string, 0, len(daily))
	for d := range daily {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		fmt.Println("  (暂无数据)")
	} else {
		for _, date := range dates {
			s := daily[date]
			fmt.Printf("  %s | 输入:%-8s 输出:%-8s 总计:%-8s 请求:%-4d\n", date, formatter.FormatNumber(s.InputTokens), formatter.FormatNumber(s.OutputTokens), formatter.FormatNumber(s.TotalTokens), s.RequestCount)
		}
	}
	fmt.Println("==============================================")
	modelStats := storage.CalculateModelStats(logs, 7)
	if len(modelStats) > 0 {
		fmt.Println()
		fmt.Println("==============================================")
		fmt.Println("  各模型消耗明细")
		fmt.Println("----------------------------------------------")
		sorted := make([]string, 0, len(modelStats))
		for m := range modelStats {
			sorted = append(sorted, m)
		}
		sort.Slice(sorted, func(i, j int) bool { return modelStats[sorted[i]].TotalTokens > modelStats[sorted[j]].TotalTokens })
		for _, m := range sorted {
			s := modelStats[m]
			n := m
			if len(n) > 28 {
				n = n[:25] + "..."
			}
			fmt.Printf("  %-28s | 输入:%-8s 输出:%-8s 总计:%-8s\n", n, formatter.FormatNumber(s.InputTokens), formatter.FormatNumber(s.OutputTokens), formatter.FormatNumber(s.TotalTokens))
		}
		fmt.Println("==============================================")
	}
}

func cmdWatch() {
	q, dq := makeQuotaQuerier(), makeDeepSeekQuerier()
	for {
		fmt.Print("\033[H\033[2J")
		fmt.Printf("\n[%s] OpenCode Go 实时监控\n", time.Now().Format("15:04:05"))
		if qd, err := q.FetchQuota(); err == nil {
			fmt.Println("\n【套餐额度】（涵盖所有模型）")
			fmt.Printf("  Rolling: %s  reset in %s\n", formatter.ProgressBar(qd.Rolling.UsagePercent, 25), qd.Rolling.ResetDisplay)
			fmt.Printf("  Weekly:  %s  reset in %s\n", formatter.ProgressBar(qd.Weekly.UsagePercent, 25), qd.Weekly.ResetDisplay)
			if qd.Monthly != nil {
				fmt.Printf("  Monthly: %s  reset in %s\n", formatter.ProgressBar(qd.Monthly.UsagePercent, 25), qd.Monthly.ResetDisplay)
			} else {
				fmt.Println("  Monthly: 无限额度")
			}
		} else {
			fmt.Printf("\n【套餐额度】查询失败: %v\n", err)
		}
		if b, err := dq.FetchBalance(); err == nil {
			sym := currencySymbol(b.Currency)
			fmt.Printf("\n【DeepSeek 余额】%s%.2f (%s)\n", sym, b.TotalBalance, b.Currency)
		} else {
			fmt.Printf("\n【DeepSeek 余额】查询失败: %v\n", err)
		}
		if logs, err := storage.ReadOCGTLogs(storage.OCGTLogDir()); err == nil {
			daily := storage.CalculateDailyStats(logs, 1)
			today := time.Now().Format("2006-01-02")
			if stat, ok := daily[today]; ok {
				fmt.Printf("\n【今日消耗】输入:%-8s 输出:%-8s 总计:%-8s (请求%d次)\n", formatter.FormatNumber(stat.InputTokens), formatter.FormatNumber(stat.OutputTokens), formatter.FormatNumber(stat.TotalTokens), stat.RequestCount)
			} else {
				fmt.Printf("\n【今日消耗】暂无数据\n")
			}
		}
		fmt.Printf("\n--- 下次刷新: 60秒后 (Ctrl+C 退出) ---\n")
		time.Sleep(60 * time.Second)
	}
}

func cmdServe() {
	// Sidebar mode: desktop panel with auto-hide
	if len(os.Args) > 2 && os.Args[2] == "--sidebar" {
		startSidebar()
		return
	}

	// Headless mode: just start the API server (for CLI/curl access)
	srv := web.NewServer(accountsFromConfig(cfg))
	srv.SetAccountsProvider(func() []web.Account { return accountsFromConfig(config.Load()) })
	srv.SetDeepSeekProvider(func() []web.DeepSeekAccount { return deepseekFromConfig(config.Load()) })
	srv.SetOllamaProvider(func() []web.OllamaAccount { return ollamaFromConfig(config.Load()) })
	srv.SetKimiProvider(func() []web.KimiAccount { return kimiFromConfig(config.Load()) })
	srv.SetCommandCodeProvider(func() []web.CommandCodeAccount { return commandcodeFromConfig(config.Load()) })
	srv.SetKimiReloadAccount(kimiAccountFromConfig)
	srv.SetKimiAccountLock(config.AcquireKimiAccountLock)
	srv.SetKimiRefreshSave(func(name, accessToken, refreshToken string) error {
		// Delegated to the shared, config-wide-locked config.SaveKimiTokens:
		// serialized reload + atomic save, so concurrent different-account
		// rotations never overwrite each other's freshly-rotated token.
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

func cmdLoginOpenCode() {
	name := ""
	if len(os.Args) > 2 {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 OpenCode Go 登录窗口，请登录后进入你的 workspace 用量页…")
	validate := func(cookie, wsid string) bool {
		q := &quota.OpenCodeGoQuerier{Cookie: cookie, WorkspaceID: wsid}
		_, err := q.FetchQuota()
		return err == nil
	}
	cookie, wsid, err := sidebar.RunOpenCodeLogin(validate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}
	if name == "" {
		name = "OpenCode"
	}
	if err := config.Mutate(func(c *config.Config) error {
		c.AddProfile(name, config.Profile{Cookie: cookie, WorkspaceID: wsid})
		c.ActiveProfile = name
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK OpenCode Go 账户 %q 已保存 (workspace %s)\n", name, wsid)
}

// cmdLoginCommandCode opens the commandcode.ai sign-in browser, captures
// the HttpOnly session cookie pair plus the GitHub login from the usage
// page URL after the user authenticates, validates the pair through the
// production quota path, then saves the named account.
func cmdLoginCommandCode() {
	name := "CommandCode"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 CommandCode 登录窗口，请登录后进入你的用量页…")
	validate := func(cookie, userName string) bool {
		q := &quota.CommandCodeQuerier{Cookie: cookie, UserName: userName}
		_, err := q.FetchQuota()
		return err == nil
	}
	cookie, userName, err := sidebar.RunCommandCodeLogin(validate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}
	if err := config.Mutate(func(c *config.Config) error {
		c.UpsertCommandCodeAccount(config.CommandCodeAccount{Name: name, Cookie: cookie, UserName: userName})
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK CommandCode 账户 %q 已保存 (user %s)\n", name, userName)
}

func cmdLoginDeepSeek() {
	name := "DeepSeek"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开登录窗口，请在窗口内完成 DeepSeek 登录…")
	validate := func(t string) bool {
		q := &quota.DeepSeekWebQuerier{Token: t}
		_, err := q.FetchSummary()
		return err == nil
	}
	token, webStore, err := sidebar.RunDeepSeekLogin(validate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}
	if err := config.Mutate(func(c *config.Config) error {
		c.UpsertDeepSeekAccount(config.DeepSeekAccount{Name: name, Token: token, WebStore: webStore})
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK DeepSeek 账户 %q 已保存\n", name)
}

func cmdLoginOllama() {
	name := "Ollama"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 Ollama 系统浏览器临时窗口，请在窗口内完成登录…")
	credentials, err := sidebar.RunOllamaLogin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}
	if err := config.Mutate(func(c *config.Config) error {
		c.UpsertOllamaAccount(config.OllamaAccount{Name: name, Cookie: credentials.Cookie, UserAgent: credentials.UserAgent})
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		os.Exit(1)
	}
	if _, err := (&quota.OllamaQuerier{Cookie: credentials.Cookie, UserAgent: credentials.UserAgent}).FetchQuota(); err != nil {
		fmt.Fprintf(os.Stderr, "Ollama 账户已保存，但读取额度失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK Ollama 账户 %q 已保存\n", name)
}

// cmdLoginKimi opens the shared Kimi browser, captures the Bearer accessToken
// after the user authenticates, validates it through the production quota
// path (the protected GetSubscriptionStats response with both meters), then
// saves the named account with a versioned auth envelope.
func cmdLoginKimi() {
	name := "Kimi"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 Kimi 登录窗口，请在窗口内完成 Kimi 登录…")
	validate := func(accessToken string) bool {
		q := &quota.KimiQuerier{AccessToken: accessToken}
		_, err := q.FetchQuota(context.Background())
		return err == nil
	}
	env, err := sidebar.RunKimiLogin(validate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}
	// Persist the captured session under the per-account cross-process lock,
	// then the global config lock (lock order: account → global). This stops a
	// concurrent quota-kimi / web refresh for the SAME account from reloading
	// a half-saved envelope or rotating while the login overwrites it.
	release, lerr := config.AcquireKimiAccountLock(name)
	if lerr != nil {
		fmt.Fprintf(os.Stderr, "登录保存锁失败: %v\n", lerr)
		os.Exit(1)
	}
	if err := config.Mutate(func(c *config.Config) error {
		c.UpsertKimiAccount(config.KimiAccount{Name: name, Auth: env})
		return nil
	}); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		os.Exit(1)
	}
	release()
	fmt.Printf("OK Kimi 账户 %q 已保存\n", name)
}

// cmdQuotaKimi prints the Kimi membership metrics for one named account or
// all saved Kimi accounts: total usage split into Kimi + Code, plus 5-hour
// and 7-day Code usage, each with an absolute reset display and no
// credentials. Each account is fetched independently (with durable refresh);
// one failure does not suppress the others.
func cmdQuotaKimi() {
	accounts := cfg.KimiAccounts
	name := ""
	if len(os.Args) > 2 {
		name = strings.TrimSpace(os.Args[2])
	}
	if name != "" {
		var found *config.KimiAccount
		for i := range accounts {
			if accounts[i].Name == name {
				found = &accounts[i]
				break
			}
		}
		if found == nil {
			fmt.Fprintf(os.Stderr, "Kimi 账户 %q 不存在\n", name)
			os.Exit(1)
		}
		if err := printKimiQuota(found); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "尚未配置 Kimi 账户。请运行 foundry-quota-sentinel login-kimi <名称>")
		os.Exit(1)
	}
	names := cfg.KimiAccountNames()
	failed := false
	for _, n := range names {
		var acc *config.KimiAccount
		for i := range accounts {
			if accounts[i].Name == n {
				acc = &accounts[i]
				break
			}
		}
		if err := printKimiQuota(acc); err != nil {
			fmt.Fprintf(os.Stderr, "Kimi 账户 %q 查询失败: %v\n", n, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// printKimiQuota fetches and prints one Kimi account's two meters. The whole
// reload→refresh→persist span is held under the per-account cross-process lock
// (config.AcquireKimiAccountLock) so a CLI run and a concurrent web request /
// open-page for the SAME account cannot race the RefreshToken endpoint or
// double-rotate. Inside the lock the LATEST saved credential is reloaded (a
// concurrent rotation's saved token is observed, not a stale snapshot).
func printKimiQuota(acc *config.KimiAccount) error {
	release, lerr := config.AcquireKimiAccountLock(acc.Name)
	if lerr != nil {
		return fmt.Errorf("Kimi 账户 %q 刷新锁失败: %v", acc.Name, lerr)
	}
	defer release()
	// Reload the latest saved credential inside the lock.
	latest, ok := latestKimiAccount(acc.Name)
	if !ok {
		return fmt.Errorf("Kimi 账户 %q 已不存在", acc.Name)
	}
	token := latest.Auth.AccessToken()
	if token == "" {
		return fmt.Errorf("Kimi 账户 %q 缺少凭证，请重新登录", acc.Name)
	}
	q := &quota.KimiQuerier{AccessToken: token, RefreshToken: latest.Auth.RefreshToken(), Headers: kimiEnvelopeHeaders(&latest.Auth)}
	data, rotated, err := q.FetchQuotaWithRefresh(context.Background())
	if err != nil {
		return err
	}
	// Persist rotated tokens if the access token was auto-refreshed. A save
	// failure is a HARD failure: rotated tokens left unpersisted would make the
	// saved envelope stale, so the CLI must surface the error rather than print
	// a quota backed by un-saved credentials. The error never carries the token.
	if rotated != nil {
		if saveErr := kimiPersistRotated(acc.Name, rotated); saveErr != nil {
			return fmt.Errorf("Kimi 账户 %q token 轮换保存失败，请重新登录", acc.Name)
		}
	}
	fmt.Println()
	fmt.Println("========================================")
	fmt.Printf("  Kimi Code 账户 %q\n", acc.Name)
	fmt.Println("----------------------------------------")
	fmt.Printf("  总使用量:   %s  (Kimi %s / Code %s)  reset %s\n", quota.FormatKimiPercent(data.Total.TotalPercent), quota.FormatKimiPercent(data.Total.KimiPercent), quota.FormatKimiPercent(data.Total.CodePercent), data.Total.ResetDisplay)
	fmt.Printf("  5 小时用量 · Code: %s  reset %s\n", quota.FormatKimiPercent(data.FiveHour.UsagePercent), data.FiveHour.ResetDisplay)
	fmt.Printf("  7 天用量 · Code:   %s  reset %s\n", quota.FormatKimiPercent(data.SevenDay.UsagePercent), data.SevenDay.ResetDisplay)
	fmt.Println("========================================")
	fmt.Printf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05"))
	return nil
}

// cmdOpenPage 打开对应账户的服务商页面，并注入该账户登录态。
// 用法: open-page <opencode|deepseek|ollama|kimi> <账户名>。由侧边栏右键菜单经 /api/open 拉起。
func cmdOpenPage() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "用法: open-page <opencode|deepseek|ollama|kimi> <账户名>")
		os.Exit(1)
	}
	provider, name := os.Args[2], strings.TrimSpace(os.Args[3])
	log.Printf("open-page: 开始 provider=%s name=%s", provider, name)
	// When launched by /api/open, FQS_OPEN_SESSION names a handshake file.
	// We write "ready" once the page is opened + auth-state-checked, or
	// "error" if the page flow fails before that. This lets /api/open
	// observe the page actually opened (or a runtime failure) instead of
	// guessing with a fixed timeout.
	session := os.Getenv("FQS_OPEN_SESSION")
	if session != "" {
		sidebar.OpenPageReady = func() { web.WriteOpenHandshake(session, "ready", "") }
		sidebar.OpenPageError = func(msg string) { web.WriteOpenHandshake(session, "error", msg) }
	}
	// pageErr is the early-failure path (before the browser launches):
	// it writes the error handshake and exits. For the post-launch path,
	// sidebar.OpenPageError (wrapped in sync.Once) handles it so a stale
	// error file is not written after the user closes the browser.
	pageErr := func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
		if session != "" {
			// Mark the session as handled so OpenPageError's sync.Once
			// won't write a second error file for the same session.
			sidebar.SignalOpenPageErrorOnce(msg)
		}
		os.Exit(1)
	}
	switch provider {
	case "opencode":
		p, ok := cfg.Profiles[name]
		if !ok || p.Cookie == "" || p.WorkspaceID == "" {
			pageErr(fmt.Sprintf("OpenCode 账户 %q 不存在或缺少凭证", name))
		}
		url := "https://opencode.ai/workspace/" + p.WorkspaceID + "/go"
		if err := sidebar.RunOpenCodePage(url, p.Cookie); err != nil {
			pageErr(fmt.Sprintf("OpenCode 账户页浏览器不可用: %v", err))
		}
	case "deepseek":
		var acc *config.DeepSeekAccount
		for i := range cfg.DeepSeekAccounts {
			if cfg.DeepSeekAccounts[i].Name == name {
				acc = &cfg.DeepSeekAccounts[i]
				break
			}
		}
		if acc == nil || acc.Token == "" {
			pageErr(fmt.Sprintf("DeepSeek 账户 %q 不存在或缺少凭证", name))
		}
		url := "https://platform.deepseek.com/usage"
		if err := sidebar.RunDeepSeekPage(url, acc.WebStore); err != nil {
			pageErr(fmt.Sprintf("DeepSeek 账户页浏览器不可用: %v", err))
		}
	case "ollama":
		var acc *config.OllamaAccount
		for i := range cfg.OllamaAccounts {
			if cfg.OllamaAccounts[i].Name == name {
				acc = &cfg.OllamaAccounts[i]
				break
			}
		}
		if acc == nil || acc.Cookie == "" {
			pageErr(fmt.Sprintf("Ollama 账户 %q 不存在或缺少凭证", name))
		}
		url := "https://ollama.com/settings"
		if err := sidebar.RunOllamaPage(url, acc.Cookie, acc.UserAgent); err != nil {
			pageErr(fmt.Sprintf("Ollama 账户页浏览器不可用: %v", err))
		}
	case "kimi":
		// Prepare the replay envelope under the per-account cross-process lock:
		// reload the LATEST on-disk account, run FetchQuotaWithRefresh→
		// SaveKimiTokens so an expired token is rotated+persisted before
		// encoding, then encode the latest envelope. This stops a concurrent
		// rotation from leaving the replay with a stale token, and stops the
		// page's in-flight rotation from invalidating the on-disk credential.
		// The lock is released inside kimiReplayEnvelope before the long browser
		// replay runs.
		envJSON, err := kimiReplayEnvelope(name)
		if err != nil {
			pageErr(err.Error())
		}
		// Persist the membership SPA's OWN in-page token rotation (round-8/9
		// adjudicated design). While the page is open past the access-token
		// expiry, the SPA refreshes itself and rotates BOTH tokens. The
		// watcher's PRIMARY evidence is the exact RefreshToken response
		// (https://auth.kimi.com/.../AuthService/RefreshToken: request AND
		// final response URL exact, 2xx, loadingFinished, strictly-parsed
		// non-empty pair — CAS-persisted immediately); the SECONDARY path is
		// the exact GetSubscriptionStats response (request AND final response
		// URL exact, 2xx, loadingFinished, valid quota body) with a consistent
		// localStorage pair. Either way the rotated pair is handed here, and
		// this closure compare-and-swaps BOTH fields to disk under the
		// per-account lock (disk moved ahead → skip, never regress).
		sidebar.KimiPageRotationSave = kimiPageRotationSaver(name)
		url := "https://www.kimi.com/membership/subscription?tab=quota"
		if err := sidebar.RunKimiPage(url, envJSON); err != nil {
			pageErr(fmt.Sprintf("Kimi 账户页浏览器不可用: %v", err))
		}
	case "commandcode":
		var acc *config.CommandCodeAccount
		for i := range cfg.CommandCodeAccounts {
			if cfg.CommandCodeAccounts[i].Name == name {
				acc = &cfg.CommandCodeAccounts[i]
				break
			}
		}
		if acc == nil || acc.Cookie == "" || acc.UserName == "" {
			pageErr(fmt.Sprintf("CommandCode 账户 %q 不存在或缺少凭证", name))
		}
		url := "https://commandcode.ai/" + acc.UserName + "/settings/usage"
		if err := sidebar.RunCommandCodePage(url, acc.Cookie); err != nil {
			pageErr(fmt.Sprintf("CommandCode 账户页浏览器不可用: %v", err))
		}
	default:
		pageErr(fmt.Sprintf("未知 provider: %s（应为 opencode、deepseek、ollama、kimi 或 commandcode）", provider))
	}
}

func showConfigHint() {
	fmt.Println("\n---")
	hasCookie, hasWS, _ := config.HasEnvVars()
	if hasCookie && hasWS {
		fmt.Println("环境变量已设置，但查询失败，请检查值是否有效。")
		return
	}
	if p, ok := cfg.GetActiveProfile(); ok && p.Cookie != "" && p.WorkspaceID != "" {
		fmt.Println("配置文件已有凭证，但查询失败，请检查值是否有效。")
		return
	}
	fmt.Println("还没有配置凭证！请任选一种方式：")
	fmt.Println("  方式一：设置环境变量")
	fmt.Println("    set OPENCODE_GO_AUTH_COOKIE=你的cookie")
	fmt.Println("    set OPENCODE_GO_WORKSPACE_ID=工作区ID")
	fmt.Println("  方式二：交互式配置（推荐）")
	fmt.Println("    foundry-quota-sentinel config init")
}

func printUsage() {
	writeUsage(os.Stdout)
}

// writeUsage prints the help text to w. printUsage wraps it for the default
// os.Stdout call site; tests pass a buffer to assert the Kimi commands and
// meter labels appear.
func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "foundry-quota-sentinel — OpenCode Go 额度 & Token 监控工具")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "双击 exe 直接启动桌面侧边栏（无需命令）")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "命令行用法:")
	fmt.Fprintln(w, "  config                查看当前配置")
	fmt.Fprintln(w, "  config init           交互式配置向导")
	fmt.Fprintln(w, "  config list           列出所有账户")
	fmt.Fprintln(w, "  config add <名称>     添加账户")
	fmt.Fprintln(w, "  config use <名称>     切换账户")
	fmt.Fprintln(w, "  config delete <名称>  删除账户")
	fmt.Fprintln(w, "  quota                 查询套餐额度")
	fmt.Fprintln(w, "  balance               查询 DeepSeek 余额")
	fmt.Fprintln(w, "  history               查看 Token 消耗历史")
	fmt.Fprintln(w, "  watch                 持续监控")
	fmt.Fprintln(w, "  serve                 启动 API 服务 (--sidebar 桌面侧边栏模式)")
	fmt.Fprintln(w, "  login-deepseek <名称> 弹窗登录 DeepSeek 并保存网页凭证")
	fmt.Fprintln(w, "  login-opencode <名称> 弹窗登录 OpenCode Go 并保存 cookie 凭证")
	fmt.Fprintln(w, "  login-commandcode <名称> 弹窗登录 CommandCode 并保存 cookie 凭证")
	fmt.Fprintln(w, "  login-ollama <名称>   弹窗登录 Ollama 并保存 cookie 凭证")
	fmt.Fprintln(w, "  login-kimi <名称>     弹窗登录 Kimi Code 并保存网页凭证")
	fmt.Fprintln(w, "  quota-kimi [名称]     查询 Kimi Code 总使用量(Kimi/Code)及 5 小时/7 天 Code 用量")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "环境变量（优先级高于配置文件）:")
	fmt.Fprintln(w, "  OPENCODE_GO_AUTH_COOKIE")
	fmt.Fprintln(w, "  OPENCODE_GO_WORKSPACE_ID")
	fmt.Fprintln(w, "  DEEPSEEK_API_KEY")
}

// ---- 配置管理命令 ----

func cmdConfigMain() {
	if len(os.Args) < 3 {
		cmdConfigShow()
		return
	}
	sub := os.Args[2]
	switch sub {
	case "init":
		cmdConfigInit()
	case "list":
		cmdConfigList()
	case "add":
		cmdConfigAdd()
	case "use":
		cmdConfigUse()
	case "delete", "del", "rm":
		cmdConfigDelete()
	case "show":
		cmdConfigShow()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", sub)
		fmt.Println("可用命令: init, list, add, use, delete, show")
	}
}

func cmdConfigShow() {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  配置状态")
	fmt.Println("----------------------------------------")
	hasC, hasW, hasD := config.HasEnvVars()
	fmt.Println("  [环境变量]")
	if hasC {
		fmt.Println("    OPENCODE_GO_AUTH_COOKIE    已设置")
	} else {
		fmt.Println("    OPENCODE_GO_AUTH_COOKIE    未设置")
	}
	if hasW {
		fmt.Println("    OPENCODE_GO_WORKSPACE_ID   已设置")
	} else {
		fmt.Println("    OPENCODE_GO_WORKSPACE_ID   未设置")
	}
	if hasD {
		fmt.Println("    DEEPSEEK_API_KEY          已设置")
	} else {
		fmt.Println("    DEEPSEEK_API_KEY          未设置")
	}
	fmt.Println()
	fmt.Println("  [配置文件]")
	if len(cfg.Profiles) == 0 {
		fmt.Println("    暂无配置，请运行 foundry-quota-sentinel config init")
	} else {
		fmt.Printf("    当前账户: %s\n", cfg.ActiveProfile)
		fmt.Printf("    账户总数: %d\n", len(cfg.Profiles))
	}
	fmt.Println()
	fmt.Println("  [ocgt 集成]")
	if _, err := os.Stat(filepath.Join(homeDir(), ".ocgt", "config.json")); err == nil {
		fmt.Println("    ocgt 配置: 已找到")
	} else {
		fmt.Println("    ocgt 配置: 未找到（仅 history 命令需要）")
	}
	if entries, err := os.ReadDir(storage.OCGTLogDir()); err == nil {
		c := 0
		for _, e := range entries {
			if !e.IsDir() {
				c++
			}
		}
		fmt.Printf("    日志文件: %d 个\n", c)
	} else {
		fmt.Println("    日志目录: 未找到（启动 ocgt 后自动生成）")
	}
	fmt.Println("========================================")
}

func cmdConfigList() {
	if len(cfg.Profiles) == 0 {
		fmt.Println("暂无配置。请运行 foundry-quota-sentinel config init 添加。")
		return
	}
	names := cfg.ProfileNames()
	sort.Strings(names)
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  账户列表")
	fmt.Println("----------------------------------------")
	for _, name := range names {
		p := cfg.Profiles[name]
		mark := " "
		if name == cfg.ActiveProfile {
			mark = ">"
		}
		c := ""
		if p.Cookie != "" {
			c = "已设置"
		} else {
			c = "未设置"
		}
		w := ""
		if p.WorkspaceID != "" {
			w = "已设置"
		} else {
			w = "未设置"
		}
		d := ""
		if p.DeepSeekAPIKey != "" {
			d = "已设置"
		} else {
			d = "未设置"
		}
		fmt.Printf("  %s %-16s Cookie:%-6s  Workspace:%-6s  DeepSeek:%-6s\n", mark, name, c, w, d)
	}
	fmt.Println("----------------------------------------")
	fmt.Printf("  当前: %s   总数: %d\n", cfg.ActiveProfile, len(cfg.Profiles))
	fmt.Println("  切换: foundry-quota-sentinel config use <名称>")
	fmt.Println("========================================")
}

func cmdConfigInit() {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  配置向导")
	fmt.Println("  输入各账户信息，直接回车保留默认值")
	fmt.Println("----------------------------------------")
	name := cfg.ActiveProfile
	if len(cfg.Profiles) > 0 {
		name = readLineDefault("账户名称", name)
	} else {
		fmt.Println("  首次配置，将创建默认账户。")
		fmt.Print("  按回车继续...")
		inputReader.Scan()
	}
	p, exists := cfg.Profiles[name]
	if !exists {
		p = config.Profile{}
	}
	fmt.Println()
	fmt.Println("  [OpenCode Go 凭证]")
	fmt.Println("  从浏览器登录 opencode.ai，F12 -> 应用 -> Cookie 复制完整值")
	p.Cookie = readLineDefault("Cookie（完整cookie字符串）", p.Cookie)
	fmt.Println("  从浏览器地址栏 /workspace/<workspaceId>/usage 获取")
	p.WorkspaceID = readLineDefault("Workspace ID（wrk_xxx）", p.WorkspaceID)
	fmt.Println()
	fmt.Println("  [DeepSeek API Key]（可选，仅查余额需要）")
	p.DeepSeekAPIKey = readLineDefault("DeepSeek API Key", p.DeepSeekAPIKey)
	if err := config.Mutate(func(c *config.Config) error {
		c.AddProfile(name, p)
		c.ActiveProfile = name
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "\n保存失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("========================================")
	fmt.Printf("  配置已保存！当前账户: %s\n", name)
	fmt.Println("  试试运行: foundry-quota-sentinel quota")
	fmt.Println("========================================")
}

func cmdConfigAdd() {
	if len(os.Args) < 4 {
		fmt.Print("请输入新账户名称: ")
		inputReader.Scan()
		name := strings.TrimSpace(inputReader.Text())
		if name == "" {
			fmt.Println("名称不能为空")
			return
		}
		os.Args = append(os.Args[:3], os.Args[3:]...)
		os.Args[3] = name
	}
	name := os.Args[3]
	if name == "" {
		fmt.Println("名称不能为空")
		return
	}
	if _, exists := cfg.Profiles[name]; exists {
		fmt.Printf("账户 %q 已存在。如需修改请先运行 config delete %s 再重新添加。\n", name, name)
		return
	}
	p := config.Profile{}
	fmt.Println()
	fmt.Println("========================================")
	fmt.Printf("  添加账户: %s\n", name)
	fmt.Println("----------------------------------------")
	fmt.Println("  [OpenCode Go 凭证]")
	fmt.Print("  Cookie（从 opencode.ai 浏览器获取）: ")
	inputReader.Scan()
	p.Cookie = strings.TrimSpace(inputReader.Text())
	fmt.Print("  Workspace ID（wrk_xxx 格式）: ")
	inputReader.Scan()
	p.WorkspaceID = strings.TrimSpace(inputReader.Text())
	fmt.Println("  [DeepSeek API Key]（可选）")
	fmt.Print("  DeepSeek API Key（直接回车跳过）: ")
	inputReader.Scan()
	p.DeepSeekAPIKey = strings.TrimSpace(inputReader.Text())
	if err := config.Mutate(func(c *config.Config) error {
		c.AddProfile(name, p)
		c.ActiveProfile = name
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return
	}
	fmt.Printf("OK 账户 %q 已添加并切换为当前账户\n", name)
}

func cmdConfigUse() {
	if len(os.Args) < 4 {
		fmt.Println("请指定账户名称。用法: foundry-quota-sentinel config use <名称>")
		fmt.Println("现有账户:")
		for _, n := range cfg.ProfileNames() {
			fmt.Printf("  - %s\n", n)
		}
		return
	}
	name := os.Args[3]
	if _, ok := cfg.Profiles[name]; !ok {
		fmt.Printf("账户 %q 不存在。可用账户:\n", name)
		for _, n := range cfg.ProfileNames() {
			fmt.Printf("  - %s\n", n)
		}
		return
	}
	if err := config.Mutate(func(c *config.Config) error {
		c.ActiveProfile = name
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		return
	}
	fmt.Printf("OK 已切换到账户: %s\n", name)
}

func cmdConfigDelete() {
	if len(os.Args) < 4 {
		fmt.Println("请指定要删除的账户名称。用法: foundry-quota-sentinel config delete <名称>")
		fmt.Println("现有账户:")
		for _, n := range cfg.ProfileNames() {
			fmt.Printf("  - %s\n", n)
		}
		return
	}
	name := os.Args[3]
	if name == cfg.ActiveProfile {
		fmt.Printf("注意: 账户 %q 是当前使用中的账户。\n", name)
		fmt.Print("确认删除？(y/N): ")
		inputReader.Scan()
		if strings.ToLower(strings.TrimSpace(inputReader.Text())) != "y" {
			fmt.Println("已取消。")
			return
		}
	}
	if err := config.Mutate(func(c *config.Config) error {
		return c.DeleteProfile(name)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "删除失败: %v\n", err)
		return
	}
	fmt.Printf("OK 已删除账户: %s\n", name)
	fmt.Printf("当前账户: %s\n", cfg.ActiveProfile)
}

// cmdLockTest is a hidden cross-process-lock test helper invoked as
// `_locktest`. It is controlled by environment variables so the test can fork
// two concurrent copies and observe serialization. It is NOT user-facing.
//
//	LOCKTEST_MODE=account LOCKTEST_NAME=<name> LOCKTEST_HOLD_MS=<n> LOCKTEST_MARK=<token>
//	  acquires the per-account cross-process lock, holds it <holdMs>, then
//	  rotates the named account's accessToken to <mark> via SaveKimiTokens.
//	LOCKTEST_MODE=global LOCKTEST_HOLD_MS=<n> LOCKTEST_W=<w>
//	  holds the global config lock <holdMs> (via Mutate), then sets window size.
//
// It prints "HELD <pid>" once the lock is acquired, then "DONE <pid>" after the
// write, so a test can observe that the second process only HELD after the
// first DONE (serialized), proving the cross-process lock works.
func cmdLockTest() {
	mode := os.Getenv("LOCKTEST_MODE")
	hold := 0
	fmt.Sscanf(os.Getenv("LOCKTEST_HOLD_MS"), "%d", &hold)
	pid := os.Getpid()
	// nowMs is a monotonic-ish timestamp for the test to assert serialization
	// order: the second process's HELD time must be after the first's DONE.
	nowMs := func() int64 { return time.Now().UnixNano() / int64(time.Millisecond) }
	switch mode {
	case "account":
		name := os.Getenv("LOCKTEST_NAME")
		mark := os.Getenv("LOCKTEST_MARK")
		release, err := config.AcquireKimiAccountLock(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "LOCK-ERR %d %v\n", pid, err)
			os.Exit(1)
		}
		fmt.Printf("HELD %d %d\n", pid, nowMs())
		if hold > 0 {
			time.Sleep(time.Duration(hold) * time.Millisecond)
		}
		// Write the mark through the global config lock (SaveKimiTokens).
		if err := config.SaveKimiTokens(name, mark, mark+"-refresh"); err != nil {
			fmt.Fprintf(os.Stderr, "WRITE-ERR %d %v\n", pid, err)
			release()
			os.Exit(1)
		}
		release()
		fmt.Printf("DONE %d %d\n", pid, nowMs())
	case "global":
		w := 0
		fmt.Sscanf(os.Getenv("LOCKTEST_W"), "%d", &w)
		// Acquire the global lock via WithConfigLock but hold before saving.
		// We simulate hold by sleeping inside the mutate fn before the save.
		err := config.WithConfigLock(func(c *config.Config) error {
			fmt.Printf("HELD %d %d\n", pid, nowMs())
			if hold > 0 {
				time.Sleep(time.Duration(hold) * time.Millisecond)
			}
			c.WindowW = w
			c.WindowH = w
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "LOCK-ERR %d %v\n", pid, err)
			os.Exit(1)
		}
		fmt.Printf("DONE %d %d\n", pid, nowMs())
	default:
		fmt.Fprintln(os.Stderr, "unknown LOCKTEST_MODE")
		os.Exit(1)
	}
}
