package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/internal/web"
	"foundry-quota-sentinel/pkg/sdk/auth"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

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

func latestKimiAccount(name string) (config.KimiAccount, bool) {
	for _, a := range config.Load().KimiAccounts {
		if a.Name == name {
			return a, true
		}
	}
	return config.KimiAccount{}, false
}

var kimiReplayRefresh = func(acc *config.KimiAccount) (*kimi.RefreshResult, error) {
	q := &kimi.KimiQuerier{
		AccessToken:  acc.Auth.AccessToken(),
		RefreshToken: acc.Auth.RefreshToken(),
		Headers:      kimiEnvelopeHeaders(&acc.Auth),
	}
	_, rotated, err := q.FetchQuotaWithRefresh(context.Background())
	return rotated, err
}

func kimiReplayEnvelope(name string) (string, error) {
	release, lerr := config.AcquireKimiAccountLock(name)
	if lerr != nil {
		return "", fmt.Errorf("Kimi 账户 %q 页面锁失败: %v", name, lerr)
	}
	defer release()

	acc, ok := latestKimiAccount(name)
	if !ok {
		return "", fmt.Errorf("Kimi 账户 %q 不存在", name)
	}

	rotated, rerr := kimiReplayRefresh(&acc)
	if rerr != nil {
		return "", fmt.Errorf("Kimi 账户 %q 刷新失败: %v", name, rerr)
	}
	if rotated != nil {
		if saveErr := config.SaveKimiTokens(name, rotated.AccessToken, rotated.RefreshToken); saveErr != nil {
			return "", fmt.Errorf("Kimi 账户 %q token 轮换保存失败，请重新登录", name)
		}
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
			return false, nil
		}
		if err := config.SaveKimiTokens(name, newAccess, newRefresh); err != nil {
			return false, fmt.Errorf("Kimi 账户 %q 页面轮换保存失败，请重新登录", name)
		}
		return true, nil
	}
}

func cmdOpenPage() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "用法: open-page <opencode|deepseek|ollama|kimi|commandcode> <账户名>")
		os.Exit(1)
	}
	provider, name := os.Args[2], strings.TrimSpace(os.Args[3])
	log.Printf("open-page: 开始 provider=%s name=%s", provider, name)

	session := os.Getenv("FQS_OPEN_SESSION")
	if session != "" {
		auth.OpenPageReady = func() { web.WriteOpenHandshake(session, "ready", "") }
		auth.OpenPageError = func(msg string) { web.WriteOpenHandshake(session, "error", msg) }
	}

	pageErr := func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
		if session != "" {
			auth.SignalOpenPageErrorOnce(msg)
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
		if err := auth.RunOpenCodePage(url, p.Cookie); err != nil {
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
		if err := auth.RunDeepSeekPage(url, acc.WebStore); err != nil {
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
		if err := auth.RunOllamaPage(url, acc.Cookie, acc.UserAgent); err != nil {
			pageErr(fmt.Sprintf("Ollama 账户页浏览器不可用: %v", err))
		}
	case "kimi":
		envJSON, err := kimiReplayEnvelope(name)
		if err != nil {
			pageErr(err.Error())
		}
		auth.KimiPageRotationSave = kimiPageRotationSaver(name)
		url := "https://www.kimi.com/membership/subscription?tab=quota"
		if err := auth.RunKimiPage(url, envJSON); err != nil {
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
		if acc == nil || acc.Cookie == "" {
			pageErr(fmt.Sprintf("CommandCode 账户 %q 不存在或缺少凭证", name))
		}
		url := "https://commandcode.ai/" + acc.UserName + "/settings/usage"
		if err := auth.RunCommandCodePage(url, acc.Cookie); err != nil {
			pageErr(fmt.Sprintf("CommandCode 账户页浏览器不可用: %v", err))
		}
	default:
		pageErr(fmt.Sprintf("未知 provider: %s", provider))
	}
}
