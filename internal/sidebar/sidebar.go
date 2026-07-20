// Package sidebar shows the monitor panel in a desktop window.
//
// The implementation is selected at build time and all variants expose the
// same API — New(port) and (*Sidebar).Run():
//   - windows (default):                   Win32 auto-hiding docking sidebar
//   - darwin/linux (default):              plain webview window
//   - any platform with -tags nogui, or
//     an unlisted GOOS:                    no-GUI stub (no webview dependency)
package sidebar

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

// signalOpenPageReady fires OpenPageReady once, nil-safe. The runXPage
// functions call it right before browser.Wait() so the ready signal
// reaches the /api/open handler the moment the page is usable.
func signalOpenPageReady() {
	if OpenPageReady != nil {
		OpenPageReady()
	}
}
