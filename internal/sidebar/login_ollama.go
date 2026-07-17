package sidebar

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const ollamaLoginPollInterval = 300 * time.Millisecond

var launchOllamaBrowser = func(ctx context.Context, pageURL string) (ollamaLoginBrowser, error) {
	return launchOllamaBrowserProcess(ctx, pageURL)
}

type ollamaCDP interface {
	Cookies(context.Context) ([]cdpCookie, error)
	SetSessionCookie(context.Context, string) error
	Navigate(context.Context, string) error
	Close() error
}

type ollamaLoginBrowser interface {
	CDP(context.Context) (ollamaCDP, error)
	Exited() bool
	Close() error
	Wait() error
}

func RunOllamaLogin(validate func(string) bool) (string, error) {
	if validate == nil {
		return "", fmt.Errorf("Ollama 登录验证函数不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	browser, err := launchOllamaBrowser(ctx, "https://ollama.com/settings")
	if err != nil {
		return "", err
	}
	return runOllamaLogin(ctx, browser, validate)
}

func RunOllamaPage(pageURL, cookieHeader string) error {
	if _, err := ollamaSessionValue(cookieHeader); err != nil {
		return err
	}
	browser, err := launchOllamaBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return runOllamaPage(ctx, browser, pageURL, cookieHeader)
}

func runOllamaLogin(ctx context.Context, browser ollamaLoginBrowser, validate func(string) bool) (cookie string, err error) {
	defer func() {
		if closeErr := browser.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("关闭 Ollama 登录浏览器失败: %w", closeErr)
		}
	}()

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return "", fmt.Errorf("连接 Ollama 登录浏览器失败: %w", err)
	}
	defer cdp.Close()

	for {
		cookies, err := cdp.Cookies(ctx)
		if err != nil {
			if browser.Exited() {
				return "", fmt.Errorf("未获取到有效 Ollama 凭证（登录窗口已关闭）")
			}
			return "", fmt.Errorf("读取 Ollama 登录状态失败: %w", err)
		}
		if candidate := ollamaSessionCookieHeader(cookies); candidate != "" && validate(candidate) {
			return candidate, nil
		}
		if browser.Exited() {
			return "", fmt.Errorf("未获取到有效 Ollama 凭证（登录窗口已关闭）")
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("未获取到有效 Ollama 凭证（登录超时或已取消）")
		case <-time.After(ollamaLoginPollInterval):
		}
	}
}

func runOllamaPage(ctx context.Context, browser ollamaLoginBrowser, pageURL, cookieHeader string) (err error) {
	defer func() {
		if err != nil {
			_ = browser.Close()
		}
	}()

	session, err := ollamaSessionValue(cookieHeader)
	if err != nil {
		return err
	}
	cdp, err := browser.CDP(ctx)
	if err != nil {
		return fmt.Errorf("连接 Ollama 账户页浏览器失败: %w", err)
	}
	defer cdp.Close()
	if err := cdp.SetSessionCookie(ctx, session); err != nil {
		return fmt.Errorf("注入 Ollama 登录状态失败: %w", err)
	}
	if err := cdp.Navigate(ctx, pageURL); err != nil {
		return fmt.Errorf("打开 Ollama 账户页失败: %w", err)
	}
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("Ollama 账户页浏览器异常退出: %w", err)
	}
	return nil
}

func ollamaSessionValue(cookieHeader string) (string, error) {
	var session string
	for _, part := range strings.Split(cookieHeader, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name != "__Secure-session" {
			continue
		}
		if !isSafeOllamaCookieValue(value) || session != "" {
			return "", fmt.Errorf("Ollama 登录状态无效")
		}
		session = value
	}
	if session == "" {
		return "", fmt.Errorf("Ollama 登录状态无效")
	}
	return session, nil
}
