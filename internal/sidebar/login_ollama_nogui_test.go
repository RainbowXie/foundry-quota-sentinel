//go:build nogui

package sidebar

import (
	"context"
	"strings"
	"testing"
)

func TestRunOllamaLoginWithoutGUIUsesExternalBrowserFlow(t *testing.T) {
	oldLaunch := launchOllamaBrowser
	t.Cleanup(func() { launchOllamaBrowser = oldLaunch })
	launchOllamaBrowser = func(context.Context, string) (ollamaLoginBrowser, error) {
		browser := newFakeOllamaBrowser(nil)
		browser.exited = true
		return browser, nil
	}

	_, err := RunOllamaLogin()
	if err == nil || strings.Contains(err.Error(), "图形界面") {
		t.Fatalf("RunOllamaLogin() error = %v, want external browser result", err)
	}
}

func TestRunOllamaPageWithoutGUIUsesExternalBrowserFlow(t *testing.T) {
	oldLaunch := launchOllamaBrowser
	t.Cleanup(func() { launchOllamaBrowser = oldLaunch })
	launchOllamaBrowser = func(context.Context, string) (ollamaLoginBrowser, error) {
		return newFakeOllamaBrowser(nil), nil
	}

	err := RunOllamaPage("https://ollama.com/settings", "__Secure-session=saved")
	if err != nil || strings.Contains(errString(err), "图形界面") {
		t.Fatalf("RunOllamaPage() error = %v, want external browser flow", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
