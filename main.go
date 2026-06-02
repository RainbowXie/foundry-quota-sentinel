package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ocgt-monitor/internal/config"
	"ocgt-monitor/internal/formatter"
	"ocgt-monitor/internal/quota"
	"ocgt-monitor/internal/storage"
	"ocgt-monitor/internal/web"
)

var currencySymbols = map[string]string{"CNY": "¥", "USD": "$", "EUR": "€", "JPY": "¥", "GBP": "£"}
var cfg *config.Config
var inputReader = bufio.NewScanner(os.Stdin)

func init() { cfg = config.Load() }

func currencySymbol(code string) string {
	if s, ok := currencySymbols[code]; ok { return s }
	return code + " "
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }
func ocgtLogDir() string { h := homeDir(); for _, d := range []string{"logs", "history", "log"} { p := filepath.Join(h, ".ocgt", d); if info, err := os.Stat(p); err == nil && info.IsDir() { return p } }; return filepath.Join(h, ".ocgt", "logs") }

func makeQuotaQuerier() *quota.OpenCodeGoQuerier {
	q := quota.NewOpenCodeGoQuerier()
	if q.Cookie == "" || q.WorkspaceID == "" {
		if p, ok := cfg.GetActiveProfile(); ok {
			if q.Cookie == "" { q.Cookie = p.Cookie }
			if q.WorkspaceID == "" { q.WorkspaceID = p.WorkspaceID }
		}
	}
	return q
}

func makeDeepSeekQuerier() *quota.DeepSeekQuerier {
	q := quota.NewDeepSeekQuerier()
	if q.APIKey == "" {
		if p, ok := cfg.GetActiveProfile(); ok && q.APIKey == "" { q.APIKey = p.DeepSeekAPIKey }
	}
	return q
}

func mask(s string) string {
	if len(s) <= 8 { return s }
	return s[:4] + "****" + s[len(s)-4:]
}

func readLineDefault(label, defaultVal string) string {
	masked := defaultVal
	if len(masked) > 8 { masked = mask(masked) }
	fmt.Printf("  %s [%s]: ", label, masked)
	if inputReader.Scan() {
		val := strings.TrimSpace(inputReader.Text())
		if val == "" { return defaultVal }
		return val
	}
	return defaultVal
}

func main() {
	if len(os.Args) < 2 { printUsage(); return }
	switch os.Args[1] {
	case "quota": cmdQuota()
	case "balance": cmdBalance()
	case "history": cmdHistory()
	case "watch": cmdWatch()
	case "config": cmdConfigMain()
	case "serve": cmdServe()
	case "help", "-h", "--help": printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
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
	} else { fmt.Println("  Monthly: 无限额度") }
	fmt.Println("========================================")
	fmt.Printf("\n查询时间: %s\n", data.FetchedAt.Format("2006-01-02 15:04:05"))
}

func cmdBalance() {
	q := makeDeepSeekQuerier()
	data, err := q.FetchBalance()
	if err != nil { fmt.Fprintf(os.Stderr, "错误: %v\n", err); os.Exit(1) }
	sym := currencySymbol(data.Currency)
	fmt.Printf("\nDeepSeek 账户余额: %s%.2f (%s)\n", sym, data.TotalBalance, data.Currency)
	if data.GrantedBalance > 0 { fmt.Printf("  赠送余额:      %s%.2f\n", sym, data.GrantedBalance) }
	if data.ToppedUpBalance > 0 { fmt.Printf("  充值余额:      %s%.2f\n", sym, data.ToppedUpBalance) }
	fmt.Println("\n(此为 DeepSeek 独立账户余额，与 OpenCode Go 套餐无关)")
}

func cmdHistory() {
	logs, err := storage.ReadOCGTLogs(ocgtLogDir())
	if err != nil { fmt.Fprintf(os.Stderr, "读取历史日志失败: %v\n", err); fmt.Fprintln(os.Stderr, "请确认 ocgt 已运行并有请求记录"); os.Exit(1) }
	daily := storage.CalculateDailyStats(logs, 7)
	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("  最近 7 天 Token 消耗")
	fmt.Println("----------------------------------------------")
	dates := make([]string, 0, len(daily))
	for d := range daily { dates = append(dates, d) }
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
		for m := range modelStats { sorted = append(sorted, m) }
		sort.Slice(sorted, func(i, j int) bool { return modelStats[sorted[i]].TotalTokens > modelStats[sorted[j]].TotalTokens })
		for _, m := range sorted {
			s := modelStats[m]; n := m; if len(n) > 28 { n = n[:25] + "..." }
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
			if qd.Monthly != nil { fmt.Printf("  Monthly: %s  reset in %s\n", formatter.ProgressBar(qd.Monthly.UsagePercent, 25), qd.Monthly.ResetDisplay) } else { fmt.Println("  Monthly: 无限额度") }
		} else { fmt.Printf("\n【套餐额度】查询失败: %v\n", err) }
		if b, err := dq.FetchBalance(); err == nil {
			sym := currencySymbol(b.Currency)
			fmt.Printf("\n【DeepSeek 余额】%s%.2f (%s)\n", sym, b.TotalBalance, b.Currency)
		} else { fmt.Printf("\n【DeepSeek 余额】查询失败: %v\n", err) }
		if logs, err := storage.ReadOCGTLogs(ocgtLogDir()); err == nil {
			daily := storage.CalculateDailyStats(logs, 1)
			today := time.Now().Format("2006-01-02")
			if stat, ok := daily[today]; ok {
				fmt.Printf("\n【今日消耗】输入:%-8s 输出:%-8s 总计:%-8s (请求%d次)\n", formatter.FormatNumber(stat.InputTokens), formatter.FormatNumber(stat.OutputTokens), formatter.FormatNumber(stat.TotalTokens), stat.RequestCount)
			} else { fmt.Printf("\n【今日消耗】暂无数据\n") }
		}
		fmt.Printf("\n--- 下次刷新: 60秒后 (Ctrl+C 退出) ---\n")
		time.Sleep(60 * time.Second)
	}
}

func cmdServe() {
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
	fmt.Println("========================================")
	fmt.Println("  OpenCode Go 额度监控面板")
	fmt.Println("  打开浏览器访问:")
	fmt.Println("  -> http://127.0.0.1:8788")
	fmt.Println("========================================")
	if err := srv.Start(":8788"); err != nil { fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err); os.Exit(1) }
}

func showConfigHint() {
	fmt.Println("\n---")
	hasCookie, hasWS, _ := config.HasEnvVars()
	if hasCookie && hasWS { fmt.Println("环境变量已设置，但查询失败，请检查值是否有效。"); return }
	if p, ok := cfg.GetActiveProfile(); ok && p.Cookie != "" && p.WorkspaceID != "" { fmt.Println("配置文件已有凭证，但查询失败，请检查值是否有效。"); return }
	fmt.Println("还没有配置凭证！请任选一种方式：")
	fmt.Println("  方式一：设置环境变量"); fmt.Println("    set OPENCODE_GO_AUTH_COOKIE=你的cookie"); fmt.Println("    set OPENCODE_GO_WORKSPACE_ID=工作区ID")
	fmt.Println("  方式二：交互式配置（推荐）"); fmt.Println("    ocgt-monitor config init")
}

func printUsage() {
	fmt.Println("ocgt-monitor — OpenCode Go 额度 & Token 监控工具")
	fmt.Println()
	fmt.Println("常用命令:")
	fmt.Println("  config                查看当前配置")
	fmt.Println("  config init           交互式配置向导")
	fmt.Println("  config list           列出所有账户")
	fmt.Println("  config add <名称>     添加账户")
	fmt.Println("  config use <名称>     切换账户")
	fmt.Println("  config delete <名称>  删除账户")
	fmt.Println("  quota                 查询套餐额度")
	fmt.Println("  balance               查询 DeepSeek 余额")
	fmt.Println("  history               查看 Token 消耗历史")
	fmt.Println("  watch                 持续监控")
	fmt.Println("  serve                 启动 Web 面板")
	fmt.Println()
	fmt.Println("环境变量（优先级高于配置文件）:")
	fmt.Println("  OPENCODE_GO_AUTH_COOKIE")
	fmt.Println("  OPENCODE_GO_WORKSPACE_ID")
	fmt.Println("  DEEPSEEK_API_KEY")
}

// ---- 配置管理命令 ----

func cmdConfigMain() {
	if len(os.Args) < 3 { cmdConfigShow(); return }
	sub := os.Args[2]
	switch sub {
	case "init": cmdConfigInit()
	case "list": cmdConfigList()
	case "add": cmdConfigAdd()
	case "use": cmdConfigUse()
	case "delete", "del", "rm": cmdConfigDelete()
	case "show": cmdConfigShow()
	default: fmt.Fprintf(os.Stderr, "未知子命令: %s\n", sub); fmt.Println("可用命令: init, list, add, use, delete, show")
	}
}

func cmdConfigShow() {
	fmt.Println(); fmt.Println("========================================"); fmt.Println("  配置状态"); fmt.Println("----------------------------------------")
	hasC, hasW, hasD := config.HasEnvVars()
	fmt.Println("  [环境变量]")
	if hasC { fmt.Println("    OPENCODE_GO_AUTH_COOKIE    已设置") } else { fmt.Println("    OPENCODE_GO_AUTH_COOKIE    未设置") }
	if hasW { fmt.Println("    OPENCODE_GO_WORKSPACE_ID   已设置") } else { fmt.Println("    OPENCODE_GO_WORKSPACE_ID   未设置") }
	if hasD { fmt.Println("    DEEPSEEK_API_KEY          已设置") } else { fmt.Println("    DEEPSEEK_API_KEY          未设置") }
	fmt.Println(); fmt.Println("  [配置文件]")
	if len(cfg.Profiles) == 0 { fmt.Println("    暂无配置，请运行 ocgt-monitor config init") } else { fmt.Printf("    当前账户: %s\n", cfg.ActiveProfile); fmt.Printf("    账户总数: %d\n", len(cfg.Profiles)) }
	fmt.Println(); fmt.Println("  [ocgt 集成]")
	if _, err := os.Stat(filepath.Join(homeDir(), ".ocgt", "config.json")); err == nil { fmt.Println("    ocgt 配置: 已找到") } else { fmt.Println("    ocgt 配置: 未找到（仅 history 命令需要）") }
	if entries, err := os.ReadDir(ocgtLogDir()); err == nil { c := 0; for _, e := range entries { if !e.IsDir() { c++ } }; fmt.Printf("    日志文件: %d 个\n", c) } else { fmt.Println("    日志目录: 未找到（启动 ocgt 后自动生成）") }
	fmt.Println("========================================")
}

func cmdConfigList() {
	if len(cfg.Profiles) == 0 { fmt.Println("暂无配置。请运行 ocgt-monitor config init 添加。"); return }
	names := cfg.ProfileNames(); sort.Strings(names)
	fmt.Println(); fmt.Println("========================================"); fmt.Println("  账户列表"); fmt.Println("----------------------------------------")
	for _, name := range names {
		p := cfg.Profiles[name]
		mark := " "; if name == cfg.ActiveProfile { mark = ">" }
		c := ""; if p.Cookie != "" { c = "已设置" } else { c = "未设置" }
		w := ""; if p.WorkspaceID != "" { w = "已设置" } else { w = "未设置" }
		d := ""; if p.DeepSeekAPIKey != "" { d = "已设置" } else { d = "未设置" }
		fmt.Printf("  %s %-16s Cookie:%-6s  Workspace:%-6s  DeepSeek:%-6s\n", mark, name, c, w, d)
	}
	fmt.Println("----------------------------------------"); fmt.Printf("  当前: %s   总数: %d\n", cfg.ActiveProfile, len(cfg.Profiles)); fmt.Println("  切换: ocgt-monitor config use <名称>"); fmt.Println("========================================")
}

func cmdConfigInit() {
	fmt.Println(); fmt.Println("========================================"); fmt.Println("  配置向导"); fmt.Println("  输入各账户信息，直接回车保留默认值"); fmt.Println("----------------------------------------")
	name := cfg.ActiveProfile
	if len(cfg.Profiles) > 0 { name = readLineDefault("账户名称", name) } else { fmt.Println("  首次配置，将创建默认账户。"); fmt.Print("  按回车继续..."); inputReader.Scan() }
	p, exists := cfg.Profiles[name]; if !exists { p = config.Profile{} }
	fmt.Println(); fmt.Println("  [OpenCode Go 凭证]"); fmt.Println("  从浏览器登录 opencode.ai，F12 -> 应用 -> Cookie 复制完整值")
	p.Cookie = readLineDefault("Cookie（完整cookie字符串）", p.Cookie)
	fmt.Println("  从浏览器地址栏 /workspace/<workspaceId>/usage 获取")
	p.WorkspaceID = readLineDefault("Workspace ID（wrk_xxx）", p.WorkspaceID)
	fmt.Println(); fmt.Println("  [DeepSeek API Key]（可选，仅查余额需要）")
	p.DeepSeekAPIKey = readLineDefault("DeepSeek API Key", p.DeepSeekAPIKey)
	cfg.AddProfile(name, p); cfg.ActiveProfile = name
	if err := cfg.Save(); err != nil { fmt.Fprintf(os.Stderr, "\n保存失败: %v\n", err); os.Exit(1) }
	fmt.Println(); fmt.Println("========================================"); fmt.Printf("  配置已保存！当前账户: %s\n", name); fmt.Println("  试试运行: ocgt-monitor quota"); fmt.Println("========================================")
}

func cmdConfigAdd() {
	if len(os.Args) < 4 {
		fmt.Print("请输入新账户名称: "); inputReader.Scan()
		name := strings.TrimSpace(inputReader.Text()); if name == "" { fmt.Println("名称不能为空"); return }
		os.Args = append(os.Args[:3], os.Args[3:]...); os.Args[3] = name
	}
	name := os.Args[3]; if name == "" { fmt.Println("名称不能为空"); return }
	if _, exists := cfg.Profiles[name]; exists { fmt.Printf("账户 %q 已存在。如需修改请先运行 config delete %s 再重新添加。\n", name, name); return }
	p := config.Profile{}
	fmt.Println(); fmt.Println("========================================"); fmt.Printf("  添加账户: %s\n", name); fmt.Println("----------------------------------------")
	fmt.Println("  [OpenCode Go 凭证]")
	fmt.Print("  Cookie（从 opencode.ai 浏览器获取）: "); inputReader.Scan(); p.Cookie = strings.TrimSpace(inputReader.Text())
	fmt.Print("  Workspace ID（wrk_xxx 格式）: "); inputReader.Scan(); p.WorkspaceID = strings.TrimSpace(inputReader.Text())
	fmt.Println("  [DeepSeek API Key]（可选）")
	fmt.Print("  DeepSeek API Key（直接回车跳过）: "); inputReader.Scan(); p.DeepSeekAPIKey = strings.TrimSpace(inputReader.Text())
	cfg.AddProfile(name, p); cfg.ActiveProfile = name
	if err := cfg.Save(); err != nil { fmt.Fprintf(os.Stderr, "保存失败: %v\n", err); return }
	fmt.Printf("OK 账户 %q 已添加并切换为当前账户\n", name)
}

func cmdConfigUse() {
	if len(os.Args) < 4 { fmt.Println("请指定账户名称。用法: ocgt-monitor config use <名称>"); fmt.Println("现有账户:"); for _, n := range cfg.ProfileNames() { fmt.Printf("  - %s\n", n) }; return }
	name := os.Args[3]
	if _, ok := cfg.Profiles[name]; !ok { fmt.Printf("账户 %q 不存在。可用账户:\n", name); for _, n := range cfg.ProfileNames() { fmt.Printf("  - %s\n", n) }; return }
	cfg.ActiveProfile = name
	if err := cfg.Save(); err != nil { fmt.Fprintf(os.Stderr, "保存失败: %v\n", err); return }
	fmt.Printf("OK 已切换到账户: %s\n", name)
}

func cmdConfigDelete() {
	if len(os.Args) < 4 { fmt.Println("请指定要删除的账户名称。用法: ocgt-monitor config delete <名称>"); fmt.Println("现有账户:"); for _, n := range cfg.ProfileNames() { fmt.Printf("  - %s\n", n) }; return }
	name := os.Args[3]
	if name == cfg.ActiveProfile {
		fmt.Printf("注意: 账户 %q 是当前使用中的账户。\n", name); fmt.Print("确认删除？(y/N): "); inputReader.Scan()
		if strings.ToLower(strings.TrimSpace(inputReader.Text())) != "y" { fmt.Println("已取消。"); return }
	}
	if err := cfg.DeleteProfile(name); err != nil { fmt.Fprintf(os.Stderr, "删除失败: %v\n", err); return }
	if err := cfg.Save(); err != nil { fmt.Fprintf(os.Stderr, "保存失败: %v\n", err); return }
	fmt.Printf("OK 已删除账户: %s\n", name); fmt.Printf("当前账户: %s\n", cfg.ActiveProfile)
}
