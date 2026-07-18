package browserauth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBrowserUsesInjectedLookupOrder(t *testing.T) {
	got, err := resolveBrowser(func(name string) (string, error) {
		if name == "chromium" {
			return "/usr/bin/chromium", nil
		}
		return "", fs.ErrNotExist
	})
	if err != nil || got != "/usr/bin/chromium" {
		t.Fatalf("resolveBrowser() = %q, %v", got, err)
	}
}

func TestResolveBrowserRejectsAllMissingCandidates(t *testing.T) {
	_, err := resolveBrowser(func(string) (string, error) { return "", fs.ErrNotExist })
	if err == nil || !strings.Contains(err.Error(), "Chrome") {
		t.Fatalf("resolveBrowser() error = %v, want missing-browser message", err)
	}
}

func TestResolveBrowserSkipsMissingCandidateUntilFound(t *testing.T) {
	calls := []string{}
	got, err := resolveBrowser(func(name string) (string, error) {
		calls = append(calls, name)
		if name == "chromium" {
			return "/usr/bin/chromium", nil
		}
		return "", fs.ErrNotExist
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/bin/chromium" {
		t.Fatalf("got=%q", got)
	}
	if len(calls) < 2 {
		t.Fatalf("lookup not iterated, calls=%v", calls)
	}
	if calls[0] == "chromium" {
		t.Fatalf("lookup returned the first candidate without iterating earlier names, calls=%v", calls)
	}
}

func TestBrowserCloseKillsWaitsAndRemovesProfile(t *testing.T) {
	profile := t.TempDir()
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	killed, waited := false, false
	b := &Browser{
		profileDir: profile,
		kill:       func() error { killed = true; return nil },
		wait:       func() error { waited = true; return nil },
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if !killed || !waited {
		t.Fatalf("kill=%v wait=%v", killed, waited)
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile still exists: %v", err)
	}
}

func TestBrowserCloseToleratesAlreadyExitedProcess(t *testing.T) {
	profile := t.TempDir()
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	killed := false
	b := &Browser{
		profileDir: profile,
		kill:       func() error { killed = true; return os.ErrProcessDone },
		wait:       func() error { return nil },
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil for already-exited process", err)
	}
	if !killed {
		t.Fatal("kill was not invoked")
	}
}

func TestBrowserCloseRemovesNestedProfileContents(t *testing.T) {
	profile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Default", "Cookies"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Browser{
		profileDir: profile,
		kill:       func() error { return nil },
		wait:       func() error { return nil },
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile still exists: %v", err)
	}
}
