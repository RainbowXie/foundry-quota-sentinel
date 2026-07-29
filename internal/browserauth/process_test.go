package browserauth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestRemoveProfileDirRetriesPersistentDirectory proves removeProfileDir
// actually retries when a directory stays busy (Chrome helpers holding
// handles after the parent exits can make os.RemoveAll race with "directory
// not empty"). It drives this deterministically with the injectable osRemoveAll:
// the first N attempts return a persisted busy error while the directory still
// exists, so a single-shot remover would return on attempt 1 while the retry
// loop must keep going for the whole budget — proving the loop exists, not
// just that removal happened to win a race.
func TestRemoveProfileDirRetriesPersistentDirectory(t *testing.T) {
	origAttempts := profileRemoveAttempts
	origInterval := profileRemoveInterval
	profileRemoveAttempts = 4
	profileRemoveInterval = time.Millisecond
	defer func() {
		profileRemoveAttempts = origAttempts
		profileRemoveInterval = origInterval
	}()

	dir := t.TempDir()
	calls := 0
	osRemoveAll = func(string) error {
		calls++
		return errors.New("unlinkat: directory not empty")
	}
	defer func() { osRemoveAll = os.RemoveAll }()

	err := removeProfileDir(dir)
	// The directory is intentionally persistent, so the loop must exhaust its
	// budget (4 calls) and return the last error rather than returning on
	// attempt 1.
	if err == nil {
		t.Fatal("removeProfileDir must report failure when removal keeps failing")
	}
	if calls != 4 {
		t.Fatalf("removeProfileDir made %d RemoveAll calls, want 4 (retry loop must run the whole budget when removal keeps failing)", calls)
	}
}

// TestRemoveProfileDirSettlesAfterRetry proves that when the busy error
// clears on the Kth attempt, removeProfileDir returns nil (the success case
// the retry loop exists to reach) rather than burning its whole budget.
func TestRemoveProfileDirSettlesAfterRetry(t *testing.T) {
	origAttempts := profileRemoveAttempts
	origInterval := profileRemoveInterval
	profileRemoveAttempts = 10
	profileRemoveInterval = time.Millisecond
	defer func() {
		profileRemoveAttempts = origAttempts
		profileRemoveInterval = origInterval
	}()

	dir := t.TempDir()
	calls := 0
	osRemoveAll = func(p string) error {
		calls++
		if calls < 3 {
			return errors.New("unlinkat: directory not empty")
		}
		return os.RemoveAll(p)
	}
	defer func() { osRemoveAll = os.RemoveAll }()

	if err := removeProfileDir(dir); err != nil {
		t.Fatalf("removeProfileDir: %v", err)
	}
	if calls != 3 {
		t.Fatalf("removeProfileDir made %d RemoveAll calls, want 3 (settle on 3rd attempt)", calls)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dir should be removed; stat err = %v", err)
	}
}

// TestRemoveProfileDirSucceedsImmediately proves the happy path needs no
// retry budget.
func TestRemoveProfileDirSucceedsImmediately(t *testing.T) {
	dir := t.TempDir()
	if err := removeProfileDir(dir); err != nil {
		t.Fatalf("removeProfileDir: %v", err)
	}
}
