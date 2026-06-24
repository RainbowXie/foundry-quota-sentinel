//go:build nogui || (!windows && !darwin && !linux)

package sidebar

import "fmt"

// Sidebar is a no-op placeholder for builds compiled without a GUI backend
// (the `nogui` tag) or for operating systems that have no sidebar
// implementation. It pulls in no CGO/webview dependency.
type Sidebar struct{ port int }

// New returns a stub; no window is created. w/h ignored.
func New(port, w, h int) *Sidebar {
	_, _ = w, h
	return &Sidebar{port: port}
}

// Run prints guidance instead of opening a window.
func (s *Sidebar) Run() {
	fmt.Printf("此版本未编译图形界面。请运行 `foundry-quota-sentinel serve`，再用浏览器打开 http://127.0.0.1:%d\n", s.port)
}
