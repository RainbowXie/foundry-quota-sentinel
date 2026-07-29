//go:build nogui

package sidebar

import (
	"context"
	"strings"
	"testing"

	"foundry-quota-sentinel/internal/browserauth"
)

// TestRunKimiLoginWithoutGUIUsesExternalBrowserFlow (task 5.6) proves the
// Kimi login flow works under -tags nogui via the shared external browser,
// with no WebView-specific auth path. The behavior is identical to the GUI
// build apart from the shell.
func TestRunKimiLoginWithoutGUIUsesExternalBrowserFlow(t *testing.T) {
	oldLaunch := launchKimiBrowser
	t.Cleanup(func() { launchKimiBrowser = oldLaunch })
	launchKimiBrowser = func(context.Context, string) (kimiLoginBrowser, error) {
		cdp := &fakeKimiCDP{pageURL: "about:blank", events: make(chan browserauth.Event, 1)}
		browser := &fakeKimiBrowser{cdp: cdp, exited: true}
		return browser, nil
	}

	_, _, err := RunKimiLogin(func(string) bool { return false })
	if err == nil || strings.Contains(err.Error(), "图形界面") {
		t.Fatalf("RunKimiLogin() error = %v, want external-browser result (no GUI dependency)", err)
	}
}

// TestRunKimiPageWithoutGUIUsesExternalBrowserFlow proves the Kimi account
// page opens via the shared external browser under nogui, with no WebView
// auth path.
func TestRunKimiPageWithoutGUIUsesExternalBrowserFlow(t *testing.T) {
	oldLaunch := launchKimiBrowser
	t.Cleanup(func() { launchKimiBrowser = oldLaunch })
	kimiSettleTimeout = 0
	launchKimiBrowser = func(context.Context, string) (kimiLoginBrowser, error) {
		cdp := &fakeKimiCDP{pageURL: kimiConsoleURL, events: make(chan browserauth.Event, 1)}
		return &fakeKimiBrowser{cdp: cdp}, nil
	}

	err := RunKimiPage(kimiConsoleURL, kimiTestEnvelope())
	if err == nil {
		t.Fatal("RunKimiPage must surface a real error, not silently succeed without the protected response")
	}
	if strings.Contains(err.Error(), "图形界面") {
		t.Fatalf("RunKimiPage must not depend on a GUI: %v", err)
	}
}
