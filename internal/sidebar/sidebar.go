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
