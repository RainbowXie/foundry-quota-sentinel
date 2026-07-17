package sidebar

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

var ollamaBrowserCandidates = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"microsoft-edge",
	"microsoft-edge-stable",
}

var connectOllamaCDP = func(ctx context.Context, debugAddress string) (ollamaCDP, error) {
	return newOllamaCDP(ctx, debugAddress)
}

func findOllamaBrowser(lookPath func(string) (string, error)) (string, error) {
	for _, name := range ollamaBrowserCandidates {
		if path, err := lookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 Chrome、Chromium 或 Edge；请安装其中之一后重试")
}

func reserveOllamaDebugPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("分配 Ollama 浏览器调试端口失败: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	if port == 0 {
		return 0, fmt.Errorf("分配 Ollama 浏览器调试端口失败")
	}
	return port, nil
}

func ollamaBrowserArgs(profileDir, pageURL string, debugPort int) []string {
	return []string{
		"--user-data-dir=" + profileDir,
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(debugPort),
		"--no-first-run",
		"--no-default-browser-check",
		"--new-window",
		pageURL,
	}
}

type ollamaBrowserProcess struct {
	profileDir   string
	debugAddress string
	kill         func() error
	wait         func() error
	exited       func() bool

	waitOnce  sync.Once
	waitErr   error
	cleanOnce sync.Once
	cleanErr  error
	closeOnce sync.Once
	closeErr  error
}

func launchOllamaBrowserProcess(ctx context.Context, pageURL string) (*ollamaBrowserProcess, error) {
	browser, err := findOllamaBrowser(exec.LookPath)
	if err != nil {
		return nil, err
	}
	profileDir, err := os.MkdirTemp("", "fqs-ollama-cdp-*")
	if err != nil {
		return nil, fmt.Errorf("创建 Ollama 临时浏览器配置失败: %w", err)
	}
	if err := os.Chmod(profileDir, 0o700); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("保护 Ollama 临时浏览器配置失败: %w", err)
	}
	// Edge exposes navigator.webdriver when remote-debugging-port=0, which makes
	// Ollama's Cloudflare challenge reject the login window. A reserved nonzero
	// port avoids that marker, so carry its address directly instead of waiting
	// for DevToolsActivePort (Chromium only writes that file for port zero).
	debugPort, err := reserveOllamaDebugPort()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}

	cmd := exec.CommandContext(ctx, browser, ollamaBrowserArgs(profileDir, pageURL, debugPort)...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("启动 Ollama 登录浏览器失败: %w", err)
	}

	done := make(chan struct{})
	var processErr error
	var processMu sync.Mutex
	go func() {
		processMu.Lock()
		processErr = cmd.Wait()
		processMu.Unlock()
		close(done)
	}()

	return &ollamaBrowserProcess{
		profileDir:   profileDir,
		debugAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(debugPort)),
		kill:         cmd.Process.Kill,
		wait: func() error {
			<-done
			processMu.Lock()
			defer processMu.Unlock()
			return processErr
		},
		exited: func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		},
	}, nil
}

func (p *ollamaBrowserProcess) Exited() bool {
	return p.exited != nil && p.exited()
}

func (p *ollamaBrowserProcess) CDP(ctx context.Context) (ollamaCDP, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := connectOllamaCDP(ctx, p.debugAddress)
		if err == nil {
			return client, nil
		}
		if p.Exited() {
			return nil, fmt.Errorf("登录浏览器已关闭")
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("等待登录浏览器就绪超时: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *ollamaBrowserProcess) Wait() error {
	p.waitOnce.Do(func() {
		if p.wait != nil {
			p.waitErr = p.wait()
		}
		p.cleanOnce.Do(func() { p.cleanErr = os.RemoveAll(p.profileDir) })
	})
	return errors.Join(p.waitErr, p.cleanErr)
}

func (p *ollamaBrowserProcess) Close() error {
	p.closeOnce.Do(func() {
		var killErr error
		killedByUs := false
		if p.kill != nil {
			killErr = p.kill()
			killedByUs = killErr == nil
			if errors.Is(killErr, os.ErrProcessDone) {
				killErr = nil
			}
		}
		waitErr := p.Wait()
		if killedByUs {
			p.closeErr = p.cleanErr
			return
		}
		p.closeErr = errors.Join(killErr, waitErr)
	})
	return p.closeErr
}
