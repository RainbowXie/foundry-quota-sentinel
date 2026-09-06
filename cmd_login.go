package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"foundry-quota-sentinel/internal/config"
	"foundry-quota-sentinel/pkg/sdk/auth"
	"foundry-quota-sentinel/pkg/sdk/providers/commandcode"
	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
	"foundry-quota-sentinel/pkg/sdk/providers/ollama"
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

func cmdLoginOpenCode() {
	name := ""
	if len(os.Args) > 2 {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 OpenCode Go 登录窗口，请登录后进入你的 workspace 用量页…")
	validate := func(cookie, wsid string) bool {
		q := &opencode.OpenCodeQuerier{Cookie: cookie, WorkspaceID: wsid}
		_, err := q.FetchQuota()
		return err == nil
	}
	cookie, wsid, err := auth.RunOpenCodeLogin(validate)
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

func cmdLoginCommandCode() {
	name := "CommandCode"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 CommandCode 登录窗口，请登录后进入你的用量页…")
	validate := func(cookie, userName string) bool {
		q := &commandcode.CommandCodeQuerier{Cookie: cookie, UserName: userName}
		_, err := q.FetchQuota()
		return err == nil
	}
	cookie, userName, err := auth.RunCommandCodeLogin(validate)
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
		q := &deepseek.DeepSeekWebQuerier{Token: t}
		_, err := q.FetchSummary()
		return err == nil
	}
	token, webStore, err := auth.RunDeepSeekLogin(validate)
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
	credentials, err := auth.RunOllamaLogin()
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
	if _, err := (&ollama.OllamaQuerier{Cookie: credentials.Cookie, UserAgent: credentials.UserAgent}).FetchQuota(); err != nil {
		fmt.Fprintf(os.Stderr, "Ollama 账户已保存，但读取额度失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK Ollama 账户 %q 已保存\n", name)
}

func cmdLoginKimi() {
	name := "Kimi"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开 Kimi 登录窗口，请在窗口内完成 Kimi 登录…")
	validate := func(accessToken string) bool {
		q := &kimi.KimiQuerier{AccessToken: accessToken}
		_, err := q.FetchQuota(context.Background())
		return err == nil
	}
	env, err := auth.RunKimiLogin(validate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}

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
