// Package browserauth owns the private, one-shot system browser and its
// DevTools Protocol transport used by every interactive provider login and
// account-page flow. It exposes only browser mechanics; provider-specific
// credential capture lives in the sidebar package.
package browserauth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// LaunchOptions configures a fresh browser process.
//
// StartURL is the first URL the browser opens. Pass about:blank for
// account-page flows that inject credentials before navigation.
type LaunchOptions struct {
	StartURL string
}

// browserCandidates is the lookup order used to find a system browser on the
// current platform. Tests inject their own lookPath so platform differences
// do not require every binary on the test host.
var browserCandidates = []string{
	"google-chrome",
	"google-chrome-stable",
	"chromium",
	"chromium-browser",
	"microsoft-edge",
	"microsoft-edge-stable",
}

// resolveBrowser returns the first supported browser that the injected
// lookPath can locate. It is split out from Launch so tests can drive the
// ordering without invoking a real exec.
func resolveBrowser(lookPath func(string) (string, error)) (string, error) {
	return resolveForPlatform(lookPath, os.Stat, runtime.GOOS, linuxCommandCandidates())
}

// resolveForPlatform finds a supported browser on the requested platform.
// lookPath resolves a command name on $PATH; stat checks that a fully
// qualified file (e.g. a Windows .exe or macOS bundle binary) is
// present. The platform string is the GOOS value; pass runtime.GOOS for
// the host. The candidate list is the platform's fully-qualified
// fallback paths.
func resolveForPlatform(
	lookPath func(string) (string, error),
	stat func(string) (fs.FileInfo, error),
	platform string,
	candidates []string,
) (string, error) {
	switch platform {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris":
		return firstExistingCommand(lookPath, candidates)
	case "darwin":
		for _, bundle := range candidates {
			if _, err := stat(bundle); err == nil {
				return bundle, nil
			}
		}
		// Last resort: fall back to command-path for development hosts
		// where the application bundle is not installed in /Applications.
		return firstExistingCommand(lookPath, macOSCommandCandidates())
	case "windows":
		for _, exe := range candidates {
			if _, err := stat(exe); err == nil {
				return exe, nil
			}
		}
		return firstExistingCommand(lookPath, windowsCommandCandidates())
	}
	return firstExistingCommand(lookPath, candidates)
}

// firstExistingCommand walks the candidate list and returns the first
// entry whose command name is resolvable on $PATH. The stat function is
// unused on Linux; it is accepted so callers can swap platforms without
// branching the test surface.
func firstExistingCommand(lookPath func(string) (string, error), candidates []string) (string, error) {
	for _, name := range candidates {
		if path, err := lookPath(name); err == nil && path != "" {
			return path, nil
		}
	}
	// As a final fallback, try the literal name as a fully-qualified
	// path. This keeps macOS bundle binaries working when they happen to
	// be present but the platform was mis-detected.
	for _, name := range candidates {
		if strings.HasPrefix(name, "/") || strings.Contains(name, string(os.PathSeparator)) {
			if path, err := lookPath(name); err == nil && path != "" {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 Chrome、Chromium 或 Edge；请安装其中之一后重试")
}

func linuxCommandCandidates() []string {
	return append([]string{}, browserCandidates...)
}

func macOSCommandCandidates() []string {
	return []string{"Google Chrome", "Microsoft Edge", "Chromium"}
}

func windowsCommandCandidates() []string {
	return []string{"msedge", "msedge.exe", "chrome", "chrome.exe", "chromium"}
}

// winExePath resolves a Windows .exe path by checking both
// %ProgramFiles% and %ProgramFiles(x86)% for the supplied leaf, then
// %LocalAppData%. Returns the first existing path. Production code
// passes os.Stat and the leaf names "msedge.exe" and "chrome.exe".
func winExePath(stat func(string) (fs.FileInfo, error), leafs ...string) []string {
	roots := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application"),
		filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application"),
		filepath.Join(os.Getenv("LocalAppData"), "Microsoft", "Edge", "Application"),
	}
	out := make([]string, 0, len(roots)*len(leafs))
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, leaf := range leafs {
			out = append(out, filepath.Join(root, leaf))
		}
	}
	// Always drop non-existent roots so the caller does not stat 50
	// missing paths per call. Stat is cheap but trivially avoidable.
	if stat == nil {
		return out
	}
	filtered := out[:0]
	for _, p := range out {
		if _, err := stat(filepath.Dir(p)); err == nil {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// reserveDebugPort returns a nonzero TCP port on the loopback interface. The
// caller must immediately hand the port to the browser so the OS does not
// reuse it. Edge exposes navigator.webdriver when --remote-debugging-port=0
// trips the Cloudflare challenge; a reserved nonzero port avoids that.
func reserveDebugPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("分配浏览器调试端口失败: %w", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if port == 0 {
		return 0, fmt.Errorf("分配浏览器调试端口失败")
	}
	return port, nil
}

// browserArgs builds the arguments we always pass so the browser uses a
// private profile, binds DevTools to loopback, and opens a single window.
func browserArgs(profileDir, pageURL string, debugPort int) []string {
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

// Browser represents the application-owned browser process. Callers obtain
// it through Launch, then drive CDP via the debug address. Close always kills
// the process and removes the temporary profile, even after a crash.
type Browser struct {
	profileDir   string
	debugAddress string
	kill         func() error
	wait         func() error
	exited       func() bool

	waitOnce, cleanOnce, closeOnce sync.Once
	waitErr, cleanErr, closeErr    error
}

// Launch starts a fresh visible system browser with a private temporary
// profile and DevTools bound to a reserved loopback port. The browser is the
// only process the application may terminate.
func Launch(ctx context.Context, options LaunchOptions) (*Browser, error) {
	browser, err := resolveBrowser(exec.LookPath)
	if err != nil {
		return nil, err
	}
	profileDir, err := os.MkdirTemp("", "fqs-browserauth-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时浏览器配置失败: %w", err)
	}
	if err := os.Chmod(profileDir, 0o700); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("保护临时浏览器配置失败: %w", err)
	}
	debugPort, err := reserveDebugPort()
	if err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, err
	}

	cmd := exec.CommandContext(ctx, browser, browserArgs(profileDir, options.StartURL, debugPort)...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("启动登录浏览器失败: %w", err)
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

	return &Browser{
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

// DebugAddress is the loopback host:port where DevTools is listening. The
// caller must verify the host is loopback before dialing.
func (b *Browser) DebugAddress() string {
	return b.debugAddress
}

// Exited reports whether the browser process has already terminated.
func (b *Browser) Exited() bool {
	return b.exited != nil && b.exited()
}

// Wait blocks until the browser process exits, then removes the temporary
// profile. Subsequent calls return the same cached error.
func (b *Browser) Wait() error {
	b.waitOnce.Do(func() {
		if b.wait != nil {
			b.waitErr = b.wait()
		}
		b.cleanOnce.Do(func() { b.cleanErr = os.RemoveAll(b.profileDir) })
	})
	return errors.Join(b.waitErr, b.cleanErr)
}

// Close kills the browser, waits for it to exit, and removes the profile.
// Safe to call multiple times and after a crash; os.ErrProcessDone is
// swallowed because it just means the OS already reaped the process.
func (b *Browser) Close() error {
	b.closeOnce.Do(func() {
		var killErr error
		killedByUs := false
		if b.kill != nil {
			killErr = b.kill()
			killedByUs = killErr == nil
			if errors.Is(killErr, os.ErrProcessDone) {
				killErr = nil
			}
		}
		waitErr := b.Wait()
		if killedByUs {
			b.closeErr = b.cleanErr
			return
		}
		b.closeErr = errors.Join(killErr, waitErr)
	})
	return b.closeErr
}
