package browserauth

import (
	"io/fs"
	"path/filepath"
	"testing"
)

// TestResolveBrowserProductionLinuxUsesCommandPath pins the production
// resolveBrowser to command-name lookup on Linux. The test injects a
// lookPath that returns the supplied path only for "chromium"; the
// production function must find it without the test hand-crafting the
// candidate list.
func TestResolveBrowserProductionLinuxUsesCommandPath(t *testing.T) {
	lp := func(name string) (string, error) {
		if name == "chromium" {
			return "/usr/bin/chromium", nil
		}
		return "", fs.ErrNotExist
	}
	st := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	got, err := resolveBrowserProduction(lp, st, "linux")
	if err != nil || got != "/usr/bin/chromium" {
		t.Fatalf("got %q err %v", got, err)
	}
}

// TestResolveBrowserProductionMacOSResolvesBundle pins the production
// resolveBrowser to a macOS bundle binary lookup. The test injects a
// stat that pretends the bundle inner binary is present; lookPath is
// never called because the bundle is found first.
func TestResolveBrowserProductionMacOSResolvesBundle(t *testing.T) {
	bundle := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) {
		if name == bundle {
			return nil, nil
		}
		return nil, fs.ErrNotExist
	}
	got, err := resolveBrowserProduction(lp, st, "darwin")
	if err != nil || got != bundle {
		t.Fatalf("got %q err %v", got, err)
	}
}

// TestResolveBrowserProductionMacOSRejectsMissingInnerBinary makes sure
// the macOS branch reports an error when no bundle binary is present
// and no command-name fallback resolves.
func TestResolveBrowserProductionMacOSRejectsMissingInnerBinary(t *testing.T) {
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	if _, err := resolveBrowserProduction(lp, st, "darwin"); err == nil {
		t.Fatal("expected error when macOS bundle binaries are missing")
	}
}

// TestResolveBrowserProductionMacOSReadsHomeForUserBundles makes sure
// the per-user Applications path is actually consulted.
func TestResolveBrowserProductionMacOSReadsHomeForUserBundles(t *testing.T) {
	home := "/Users/alice"
	bundle := filepath.Join(home, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome")
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) {
		if name == bundle {
			return nil, nil
		}
		return nil, fs.ErrNotExist
	}
	t.Setenv("HOME", home)
	got, err := resolveBrowserProduction(lp, st, "darwin")
	if err != nil || got != bundle {
		t.Fatalf("got %q err %v", got, err)
	}
}

// TestResolveBrowserProductionWindowsFindsExe pins the production
// resolveBrowser to a Windows .exe lookup. The expected path is built
// with filepath.Join from t.Setenv roots so the test is host-portable.
func TestResolveBrowserProductionWindowsFindsExe(t *testing.T) {
	programFiles := `C:\Program Files`
	t.Setenv("ProgramFiles", programFiles)
	exe := filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe")
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) {
		if name == exe {
			return nil, nil
		}
		return nil, fs.ErrNotExist
	}
	got, err := resolveBrowserProduction(lp, st, "windows")
	if err != nil || got != exe {
		t.Fatalf("got %q err %v", got, err)
	}
}

// TestResolveBrowserProductionWindowsPrefersChromeBeforeEdge makes sure
// the production Windows resolver orders Chrome ahead of Edge when both
// are present.
func TestResolveBrowserProductionWindowsPrefersChromeBeforeEdge(t *testing.T) {
	programFiles := `C:\Program Files`
	programFilesX86 := `C:\Program Files (x86)`
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("ProgramFiles(x86)", programFilesX86)
	chrome := filepath.Join(programFiles, "Google", "Chrome", "Application", "chrome.exe")
	edge := filepath.Join(programFilesX86, "Microsoft", "Edge", "Application", "msedge.exe")
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) {
		if name == chrome || name == edge {
			return nil, nil
		}
		return nil, fs.ErrNotExist
	}
	got, err := resolveBrowserProduction(lp, st, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if got != chrome {
		t.Fatalf("got %q, want Chrome ahead of Edge", got)
	}
}

// TestResolveBrowserProductionWindowsLocalAppDataFallback covers the
// LocalAppData path: when neither Program Files root contains the
// browser, the resolver falls back to %LocalAppData%.
func TestResolveBrowserProductionWindowsLocalAppDataFallback(t *testing.T) {
	local := `C:\Users\alice\AppData\Local`
	t.Setenv("LocalAppData", local)
	exe := filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe")
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) {
		if name == exe {
			return nil, nil
		}
		return nil, fs.ErrNotExist
	}
	got, err := resolveBrowserProduction(lp, st, "windows")
	if err != nil || got != exe {
		t.Fatalf("got %q err %v", got, err)
	}
}

// TestResolveBrowserProductionWindowsFallsBackToPathExe covers the
// fallback path when no Program Files install is present but the
// browser is on $PATH (e.g. a portable build).
func TestResolveBrowserProductionWindowsFallsBackToPathExe(t *testing.T) {
	lp := func(name string) (string, error) {
		switch name {
		case "msedge.exe":
			return `C:\Tools\msedge.exe`, nil
		case "chrome.exe":
			return `C:\Tools\chrome.exe`, nil
		}
		return "", fs.ErrNotExist
	}
	st := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	got, err := resolveBrowserProduction(lp, st, "windows")
	if err != nil {
		t.Fatal(err)
	}
	// Chrome must come before Edge when both are available on PATH.
	if got != `C:\Tools\chrome.exe` {
		t.Fatalf("got %q, want Chrome over Edge from PATH", got)
	}
}

// TestResolveBrowserProductionWindowsFallsBackToPathExeEdge covers
// the edge-only-on-PATH case so the production fallback truly consults
// every name in the Linux command list.
func TestResolveBrowserProductionWindowsFallsBackToPathExeEdge(t *testing.T) {
	lp := func(name string) (string, error) {
		if name == "msedge.exe" {
			return `C:\Tools\msedge.exe`, nil
		}
		return "", fs.ErrNotExist
	}
	st := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	got, err := resolveBrowserProduction(lp, st, "windows")
	if err != nil || got != `C:\Tools\msedge.exe` {
		t.Fatalf("got %q err %v", got, err)
	}
}

// TestResolveBrowserProductionUnknownPlatformFallsBackToCommand makes
// sure an unrecognised GOOS still walks the command-name list so
// development hosts (e.g. freebsd) do not fail outright.
func TestResolveBrowserProductionUnknownPlatformFallsBackToCommand(t *testing.T) {
	lp := func(name string) (string, error) {
		if name == "chromium" {
			return "/usr/local/bin/chromium", nil
		}
		return "", fs.ErrNotExist
	}
	st := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	got, err := resolveBrowserProduction(lp, st, "freebsd")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/local/bin/chromium" {
		t.Fatalf("got %q", got)
	}
}
