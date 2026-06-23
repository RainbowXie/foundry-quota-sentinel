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

// New creates the window pointing at the local panel server on the given port.
func New(port int) *Sidebar {
	wv := webview.New(false)
	wv.SetTitle("ocgt-monitor")
	wv.SetSize(panelWidth, panelHeight, webview.HintFixed)
	wv.Navigate(fmt.Sprintf("http://127.0.0.1:%d/sidebar.html", port))
	return &Sidebar{wv: wv}
}

// Run shows the window and blocks until it is closed.
func (s *Sidebar) Run() {
	s.wv.Run()
	s.wv.Destroy()
}
