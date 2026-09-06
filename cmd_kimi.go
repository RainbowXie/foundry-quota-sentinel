package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/formatter"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

func kimiPersistRotated(name string, rotated *kimi.RefreshResult) error {
	return config.SaveKimiTokens(name, rotated.AccessToken, rotated.RefreshToken)
}

func kimiEnvelopeHeaders(env *kimi.KimiAuthEnvelope) map[string]string {
	if env == nil {
		return nil
	}
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

func printKimiQuota(acc *config.KimiAccount) error {
	release, lerr := config.AcquireKimiAccountLock(acc.Name)
	if lerr != nil {
		return fmt.Errorf("Kimi 账户 %q 刷新锁失败: %v", acc.Name, lerr)
	}
	defer release()

	latest, ok := latestKimiAccount(acc.Name)
	if !ok {
		return fmt.Errorf("Kimi 账户 %q 已不存在", acc.Name)
	}
	token := latest.Auth.AccessToken()
	if token == "" {
		return fmt.Errorf("Kimi 账户 %q 缺少凭证，请重新登录", acc.Name)
	}
	q := &kimi.KimiQuerier{
		AccessToken:  token,
		RefreshToken: latest.Auth.RefreshToken(),
		Headers:      kimiEnvelopeHeaders(&latest.Auth),
	}
	data, rotated, err := q.FetchQuotaWithRefresh(context.Background())
	if err != nil {
		return err
	}
	if rotated != nil {
		if saveErr := kimiPersistRotated(acc.Name, rotated); saveErr != nil {
			return fmt.Errorf("Kimi 账户 %q token 轮换保存失败，请重新登录", acc.Name)
		}
	}
	fmt.Print(formatter.FormatKimiTable(acc.Name, data))
	return nil
}
