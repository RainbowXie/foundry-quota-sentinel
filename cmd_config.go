package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/storage"
)

var inputReader = bufio.NewScanner(os.Stdin)

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

func cmdLockTest() {
	mode := os.Getenv("LOCKTEST_MODE")
	hold := 0
	fmt.Sscanf(os.Getenv("LOCKTEST_HOLD_MS"), "%d", &hold)
	pid := os.Getpid()
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
