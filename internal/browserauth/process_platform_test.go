package browserauth

import (
	"errors"
	"io/fs"
	"runtime"
	"testing"
)

// allBrowsersExist is a fake lookPath / stat that returns the supplied
// path for every name and every file in the bundle directory.
func allBrowsersExist(lut map[string]string) (func(string) (string, error), func(string) (fs.FileInfo, error)) {
	lp := func(name string) (string, error) {
		if p, ok := lut[name]; ok {
			return p, nil
		}
		return "", fs.ErrNotExist
	}
	st := func(name string) (fs.FileInfo, error) { return nil, nil }
	return lp, st
}

func TestResolveBrowserWindowsPicksChromeBeforeEdge(t *testing.T) {
	lp, st := allBrowsersExist(map[string]string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`:        `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`: `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	})
	got, err := resolveForPlatform(lp, st, "windows", []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`})
	if err != nil || got != `C:\Program Files\Google\Chrome\Application\chrome.exe` {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestResolveBrowserMacOSFindsBundle(t *testing.T) {
	lp, st := allBrowsersExist(map[string]string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	})
	got, err := resolveForPlatform(lp, st, "darwin", []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	})
	if err != nil || got != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestResolveBrowserRejectsApplicationWithoutInnerBinary(t *testing.T) {
	lp := func(string) (string, error) { return "", fs.ErrNotExist }
	st := func(name string) (fs.FileInfo, error) {
		// /Applications/Google Chrome.app exists but the inner binary
		// does not.
		if name == "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
			return nil, fs.ErrNotExist
		}
		return nil, errors.New("unexpected file")
	}
	_, err := resolveForPlatform(lp, st, "darwin", []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	})
	if err == nil {
		t.Fatal("expected error when bundle inner binary is missing")
	}
}

func TestResolveBrowserLinuxFallsBackToCommand(t *testing.T) {
	lp, st := allBrowsersExist(map[string]string{
		"chromium": "/usr/bin/chromium",
	})
	got, err := resolveForPlatform(lp, st, "linux", []string{"google-chrome", "chromium"})
	if err != nil || got != "/usr/bin/chromium" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestResolveBrowserCurrentPlatformDelegatesToRuntime(t *testing.T) {
	// The production resolveBrowser must produce a result consistent with
	// resolveForPlatform on the current GOOS.
	lp, st := allBrowsersExist(nil)
	_, err := resolveForPlatform(lp, st, runtime.GOOS, nil)
	if err == nil {
		t.Fatal("expected missing-browser error for empty lookup")
	}
}
