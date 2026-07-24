// Package sidebar shows the monitor panel in a desktop window.
//
// The implementation is selected at build time and all variants expose the
// same API — New(port) and (*Sidebar).Run():
//   - windows (default):                   Win32 auto-hiding docking sidebar
//   - darwin/linux (default):              plain webview window
//   - any platform with -tags nogui, or
//     an unlisted GOOS:                    no-GUI stub (no webview dependency)
package sidebar

import "sync"

const (
	panelWidth  = 360
	panelHeight = 370
)

// OpenPageReady, when set, is called by each provider's runXPage once the
// account page has been opened, credentials replayed, and the post-
// navigation auth-state check passed — i.e. the page is ready and the
// browser is about to block on the user closing it. The caller (the
// open-page CLI subprocess invoked by /api/open) installs a hook that
// writes a ready/error handshake file, so the sidebar can observe the
// page actually opened (or a runtime failure) instead of guessing with
// a fixed timeout. nil = no handshake (tests / direct CLI use).
var OpenPageReady func()

// OpenPageError, when set, is called by a provider's runXPage when the
// page flow fails. signalOpenPageError fires it at most once per process
// (sync.Once) so a late WriteOpenHandshake cannot leave a stale error
// file after the user closes the browser.
var OpenPageError func(err string)
var openPageErrorOnce sync.Once

// resetOpenPageErrorOnce resets the sync.Once so a new test or a new
// subprocess invocation can fire signalOpenPageError again.
func resetOpenPageErrorOnce() {
	openPageErrorOnce = sync.Once{}
}

// signalOpenPageReady fires OpenPageReady once, nil-safe.
func signalOpenPageReady() {
	if OpenPageReady != nil {
		OpenPageReady()
	}
}

// signalOpenPageError fires OpenPageError at most once (sync.Once),
// nil-safe. Called on auth failure BEFORE browser.Wait() blocks.
func signalOpenPageError(err string) {
	openPageErrorOnce.Do(func() {
		if OpenPageError != nil {
			OpenPageError(err)
		}
	})
}
