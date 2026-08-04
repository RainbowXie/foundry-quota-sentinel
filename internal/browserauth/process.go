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
	"sync"
	"time"
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

// resolveBrowser returns the first supported browser on the current
// platform. The production entry point builds platform-specific
// candidate paths (macOS .app binaries, Windows Program Files
// installs) and falls back to command-path lookups; tests inject
// lookPath and stat to drive each branch without touching the host.
func resolveBrowser(lookPath func(string) (string, error)) (string, error) {
	return resolveBrowserProduction(lookPath, os.Stat, runtime.GOOS)
}

// resolveBrowserProduction is the wiring entry the package tests
// against. It dispatches on the supplied platform string and builds
// the per-platform candidate list itself, so callers do not have to
// know which names belong to which OS. The lookPath and stat arguments
// make every filesystem interaction mockable. Windows and macOS code
// paths read environment variables (ProgramFiles, HOME, ...) through
// os.Getenv, which the package tests drive via t.Setenv.
func resolveBrowserProduction(
	lookPath func(string) (string, error),
	stat func(string) (fs.FileInfo, error),
	platform string,
) (string, error) {
	switch platform {
	case "darwin":
		return resolveMacOS(lookPath, stat)
	case "windows":
		return resolveWindows(lookPath, stat)
	default:
		// linux, freebsd, openbsd, netbsd, dragonfly, solaris, and
		// anything unknown — command-path is the only portable lookup.
		return resolveLinux(lookPath)
	}
}

// resolveMacOS first looks for the canonical /Applications bundle
// binaries (Chrome, then Edge). If none of those bundle binaries are
// present, the function falls back to command-name lookups so a
// developer with Chromium on $PATH still gets a working browser.
func resolveMacOS(
	lookPath func(string) (string, error),
	stat func(string) (fs.FileInfo, error),
) (string, error) {
	bundles := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	}
	for _, bundle := range bundles {
		if _, err := stat(bundle); err == nil {
			return bundle, nil
		}
	}
	// Per-user Applications is the documented install path for
	// non-system installs. Only check under $HOME so a missing
	// $HOME does not produce a stat error.
	if home := os.Getenv("HOME"); home != "" {
		userBundles := []string{
			filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
			filepath.Join(home, "Applications", "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"),
			filepath.Join(home, "Applications", "Chromium.app", "Contents", "MacOS", "Chromium"),
		}
		for _, bundle := range userBundles {
			if _, err := stat(bundle); err == nil {
				return bundle, nil
			}
		}
	}
	return resolveLinux(lookPath)
}

// resolveWindows first looks for the canonical Program Files
// installs (Chrome before Edge), then LocalAppData, then falls back
// to a command-name lookup for portable builds that put the binary
// on $PATH directly. The resolver does not assume the host has any
// particular environment variable set.
func resolveWindows(
	lookPath func(string) (string, error),
	stat func(string) (fs.FileInfo, error),
) (string, error) {
	leafs := []string{"chrome.exe", "msedge.exe"}
	roots := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application"),
		filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application"),
		filepath.Join(os.Getenv("LocalAppData"), "Microsoft", "Edge", "Application"),
	}
	// Chrome before Edge: walk roots in order, but prefer Chrome's
	// leaf across all roots before falling back to Edge. The
	// resulting preference is: any Program Files Chrome, then any
	// Program Files Edge, then any LocalAppData Chrome, etc.
	for _, leaf := range leafs {
		for _, root := range roots {
			if root == "" || filepath.Base(root) == "." {
				continue
			}
			candidate := filepath.Join(root, leaf)
			if _, err := stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	// Portable build fallback: $PATH may carry msedge.exe or chrome.exe
	// directly. Walk the Windows command-name list before falling back
	// to the generic Linux list so a portable Windows build resolves
	// to its own .exe name.
	for _, name := range windowsCommandCandidates() {
		if path, err := lookPath(name); err == nil && path != "" {
			return path, nil
		}
	}
	return resolveLinux(lookPath)
}

// resolveLinux (and any other *nix platform) walks a fixed list of
// command names and returns the first one resolvable on $PATH. This
// is the original behaviour the project shipped before platform
// dispatch was introduced.
func resolveLinux(lookPath func(string) (string, error)) (string, error) {
	for _, name := range browserCandidates {
		if path, err := lookPath(name); err == nil && path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 Chrome、Chromium 或 Edge；请安装其中之一后重试")
}

// windowsCommandCandidates is the command-name list the Windows
// resolver walks when no Program Files install is present. Chrome and
// Edge both ship with their .exe suffix on $PATH for portable builds.
func windowsCommandCandidates() []string {
	return []string{"chrome", "chrome.exe", "msedge", "msedge.exe", "chromium"}
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
		// 临时 profile 用不到任何扩展；企业策略强装的扩展会在每次启动时
		// 重新加载并阻塞首个浏览器级 CDP 命令（实测 8~19s），直接禁用。
		"--disable-extensions",
		// Linux 无桌面密钥环（WSL/容器/裸 X）时，Chromium 首次写 cookie 前
		// 会等 D-Bus 密钥环超时（实测 9~19s）才回落明文存储；临时 profile
		// 随用随删，直接用 basic 明文存储跳过该等待。
		"--password-store=basic",
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

// profileParentDir returns the parent directory for the temporary browser
// profile. On Linux it prefers a tmpfs ($XDG_RUNTIME_DIR, then /dev/shm):
// a fresh profile's first run issues hundreds of tiny SQLite commits, and
// on slow/virtualised disks (WSL vhdx) each fdatasync costs ~0.3s — the
// resulting sync storm used to stall the first cookie write for 9~19s.
// tmpfs makes those syncs free. Falls back to the OS default ("") when no
// tmpfs candidate is usable. A var so tests can inject.
var profileParentDir = func() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	for _, dir := range []string{os.Getenv("XDG_RUNTIME_DIR"), "/dev/shm"} {
		if dir == "" {
			continue
		}
		probe, err := os.MkdirTemp(dir, ".fqs-probe-*")
		if err != nil {
			continue
		}
		_ = os.RemoveAll(probe)
		return dir
	}
	return ""
}

// Launch starts a fresh visible system browser with a private temporary
// profile and DevTools bound to a reserved loopback port. The browser is the
// only process the application may terminate.
func Launch(ctx context.Context, options LaunchOptions) (*Browser, error) {
	browser, err := resolveBrowser(exec.LookPath)
	if err != nil {
		return nil, err
	}
	profileDir, err := os.MkdirTemp(profileParentDir(), "fqs-browserauth-*")
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
		b.cleanOnce.Do(func() { b.cleanErr = removeProfileDir(b.profileDir) })
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

// profileRemoveAttempts and profileRemoveInterval bound the retry loop for
// removing the temporary Chrome profile after the main process exits. Chrome
// forks helpers that can hold file handles and finish writing/removing files
// briefly after the parent returns from Wait, which otherwise races
// os.RemoveAll and fails with "unlinkat: directory not empty".
var (
	profileRemoveAttempts = 20
	profileRemoveInterval = 100 * time.Millisecond
	// osRemoveAll is the removable used by removeProfileDir, made injectable
	// so tests can count retries deterministically without a real Chrome.
	osRemoveAll = os.RemoveAll
)

// removeProfileDir removes dir, retrying for a short window when the OS still
// reports it busy (Chrome helpers releasing handles after the parent exits,
// which otherwise races os.RemoveAll and fails with "directory not empty").
// It returns nil once the directory is gone — whether on the first attempt or
// after the handles settle — so a transient teardown race never leaks the
// profile or surfaces a spurious failure. A real, persistent I/O error returns
// immediately for diagnosis.
func removeProfileDir(dir string) error {
	var lastErr error
	for attempt := 0; attempt < profileRemoveAttempts; attempt++ {
		err := osRemoveAll(dir)
		if err == nil {
			return nil
		}
		if _, statErr := os.Stat(dir); errors.Is(statErr, fs.ErrNotExist) {
			return nil // gone despite the RemoveAll error
		}
		lastErr = err
		time.Sleep(profileRemoveInterval)
	}
	return lastErr
}
