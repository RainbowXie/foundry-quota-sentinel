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
const winMinHeight = 820

// New creates the window pointing at the local panel server on the given port.
// w/h are the restored window size (<=0 means use defaults). HintMin keeps
// panelWidth as the horizontal floor so content is never clipped; a second
// HintNone call applies the actual (restored or default) size on top.
func New(port, w, h int) *Sidebar {
	wv := webview.New(false)
	wv.SetTitle("foundry-quota-sentinel")
	if w <= 0 {
		w = 420
	}
	if h <= 0 {
		h = winMinHeight
	}
	wv.SetSize(panelWidth, 480, webview.HintMin)
	wv.SetSize(w, h, webview.HintNone)
	wv.Navigate(fmt.Sprintf("http://127.0.0.1:%d/sidebar.html", port))
	return &Sidebar{wv: wv}
}

// Run shows the window and blocks until it is closed.
func (s *Sidebar) Run() {
	s.wv.Run()
	s.wv.Destroy()
}
