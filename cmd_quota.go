package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"foundry-quota-sentinel/internal/formatter"
	"foundry-quota-sentinel/internal/storage"
	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

func makeQuotaQuerier() *opencode.OpenCodeQuerier {
	q := opencode.NewOpenCodeQuerier()
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

func makeDeepSeekQuerier() *deepseek.DeepSeekQuerier {
	q := deepseek.NewDeepSeekQuerier()
	if q.APIKey == "" {
		if p, ok := cfg.GetActiveProfile(); ok && q.APIKey == "" {
			q.APIKey = p.DeepSeekAPIKey
		}
	}
	return q
}

func cmdQuota() {
	q := makeQuotaQuerier()
	data, err := q.FetchQuota()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		showConfigHint()
		os.Exit(1)
	}
	fmt.Print(formatter.FormatOpenCodeTable(data))
}

func cmdBalance() {
	q := makeDeepSeekQuerier()
	data, err := q.FetchBalance()
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
	sym := currencySymbol(data.Currency)
	fmt.Print(formatter.FormatDeepSeekBalanceTable(data, sym))
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
