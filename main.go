package main

import (
	"fmt"
	"io"
	"os"

	"foundry-quota-sentinel/internal/config"
)

var currencySymbols = map[string]string{"CNY": "¥", "USD": "$", "EUR": "€", "JPY": "¥", "GBP": "£"}
var cfg *config.Config

var version = "0.11.1"

func init() { cfg = config.Load() }

func currencySymbol(code string) string {
	if s, ok := currencySymbols[code]; ok {
		return s
	}
	return code + " "
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }

func mask(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:4] + "****" + s[len(s)-4:]
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

func printUsage() {
	writeUsage(os.Stdout)
}

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
