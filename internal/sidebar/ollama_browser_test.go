package sidebar

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestFindOllamaBrowserPrefersChromeThenChromiumThenEdge(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "chromium" || name == "microsoft-edge" {
			return "/usr/bin/" + name, nil
		}
		return "", fs.ErrNotExist
	}

	got, err := findOllamaBrowser(lookPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/usr/bin/chromium" {
		t.Fatalf("browser = %q, want Chromium before Edge", got)
	}
}

func TestFindOllamaBrowserExplainsMissingBrowser(t *testing.T) {
	_, err := findOllamaBrowser(func(string) (string, error) {
		return "", fs.ErrNotExist
	})
	if err == nil || !strings.Contains(err.Error(), "Chrome、Chromium 或 Edge") {
		t.Fatalf("error = %v, want install guidance", err)
	}
}

func TestOllamaBrowserCloseWaitsAndRemovesPrivateProfile(t *testing.T) {
	profile := t.TempDir()
	var killed, waited bool
	p := &ollamaBrowserProcess{
		profileDir: profile,
		kill: func() error {
			killed = true
			return nil
		},
		wait: func() error {
			waited = true
			return nil
		},
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !killed || !waited {
		t.Fatalf("killed=%t waited=%t, want both true", killed, waited)
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("profile stat error = %v, want removed profile", err)
	}
}
