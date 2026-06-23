//go:build (darwin || linux) && !nogui

package sidebar

import (
	"fmt"

	"github.com/webview/webview_go"
)

// Sidebar wraps a plain webview window. On macOS/Linux there is no OS-level
// edge docking or auto-hide; this is a normal window showing the same panel
// UI that the local HTTP server serves.
type Sidebar struct {
	wv webview.WebView
}

// winMinHeight is the standalone window's default/minimum height. The Windows
// docking panel's panelHeight (370) is too short for a normal window, so we
// open taller and let the user enlarge from there.
const winMinHeight = 640

// New creates the window pointing at the local panel server on the given port.
func New(port int) *Sidebar {
	wv := webview.New(false)
	wv.SetTitle("ocgt-monitor")
	// The panel HTML (static/sidebar.html) is a fixed 360px-wide column laid
	// out to the full window height. HintMin keeps panelWidth as the floor so
	// the content is never horizontally clipped, while still letting the user
	// resize the window larger — unlike HintFixed, which locks the size.
	wv.SetSize(panelWidth, winMinHeight, webview.HintMin)
	wv.Navigate(fmt.Sprintf("http://127.0.0.1:%d/sidebar.html", port))
	return &Sidebar{wv: wv}
}

// Run shows the window and blocks until it is closed.
func (s *Sidebar) Run() {
	s.wv.Run()
	s.wv.Destroy()
}
