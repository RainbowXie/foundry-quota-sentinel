package sidebar

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/webview/webview_go"
	"golang.org/x/sys/windows"
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	panelWidth    = 300
	triggerZonePx = 6
	gap           = 2
	animSteps     = 12
	animStepMs    = 8
	pollMs        = 80
	hideDelayMs   = 1800
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	// Window style constants
	wsCaption     = 0x00C00000
	wsThickFrame  = 0x00040000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000
	wsPopUp       = 0x80000000
	wsClipChildren = 0x02000000
	// Extended style constants
	wsExToolWindow  = 0x00000080
	wsExTopMost     = 0x00000008
	wsExNoActivate  = 0x08000000
)

type POINT struct{ X, Y int32 }

func getScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0)  // SM_CXSCREEN
	h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	return int(w), int(h)
}

func getCursorPos() (int, int) {
	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return int(pt.X), int(pt.Y)
}

// intToPtr converts a signed int to uintptr for Windows API calls.
// Go 1.25+ requires explicit conversion for negative constants.
func intToPtr(n int) uintptr {
	return uintptr(int32(n))
}

type Sidebar struct {
	wv       webview.WebView
	hwnd     uintptr
	screenW  int
	screenH  int
	hiddenX  int
	shownX   int
	hidden   bool
	shown    bool
	animating bool
}

func New(port int) *Sidebar {
	runtime.LockOSThread()

	screenW, screenH := getScreenSize()
	hiddenX := screenW - triggerZonePx
	shownX := screenW - panelWidth

	wv := webview.New(false)
	wv.SetTitle("")
	wv.SetSize(panelWidth, screenH, webview.HintFixed)
	wv.Navigate(fmt.Sprintf("http://127.0.0.1:%d/sidebar", port))

	hwnd := uintptr(wv.Window())

	// Remove window chrome
	gwlStyle := intToPtr(-16)
	getWindowLong := user32.NewProc("GetWindowLongW")
	setWindowLong := user32.NewProc("SetWindowLongW")
	style, _, _ := getWindowLong.Call(hwnd, gwlStyle)
	style = style & ^uintptr(wsCaption) & ^uintptr(wsThickFrame)
	style = style & ^uintptr(wsSysMenu) & ^uintptr(wsMinimizeBox) & ^uintptr(wsMaximizeBox)
	style |= wsPopUp | wsClipChildren
	setWindowLong.Call(hwnd, gwlStyle, style)

	// Extended styles: tool window, topmost, no activate
	gwlExStyle := intToPtr(-20)
	exStyle, _, _ := getWindowLong.Call(hwnd, gwlExStyle)
	exStyle |= wsExToolWindow | wsExTopMost | wsExNoActivate
	setWindowLong.Call(hwnd, gwlExStyle, exStyle)

	// Apply frame changes and position at right edge (hidden)
	procSetWindowPos.Call(hwnd, 0, uintptr(hiddenX), 0, panelWidth, uintptr(screenH),
		swpNoZOrder|swpNoActivate|0x0020) // SWP_FRAMECHANGED

	return &Sidebar{
		wv: wv, hwnd: hwnd,
		screenW: screenW, screenH: screenH,
		hiddenX: hiddenX, shownX: shownX,
		hidden: true, shown: false,
	}
}

func (s *Sidebar) Run() {
	go s.edgeLoop()
	s.wv.Run()
	s.wv.Destroy()
}

// slide moves the window smoothly from current position to targetX.
func (s *Sidebar) slide(targetX int) {
	s.animating = true
	defer func() { s.animating = false }()

	x, _, _, _ := s.getWindowRect()
	dx := (targetX - x) / animSteps
	if dx == 0 {
		if targetX > x {
			dx = 1
		} else {
			dx = -1
		}
	}

	for i := 0; i < animSteps; i++ {
		x += dx
		if (dx > 0 && x > targetX) || (dx < 0 && x < targetX) {
			x = targetX
		}
		procSetWindowPos.Call(s.hwnd, 0, uintptr(x), 0, panelWidth, uintptr(s.screenH),
			swpNoZOrder|swpNoActivate)
		time.Sleep(animStepMs * time.Millisecond)
		if x == targetX {
			break
		}
	}
	// Ensure final position
	procSetWindowPos.Call(s.hwnd, 0, uintptr(targetX), 0, panelWidth, uintptr(s.screenH),
		swpNoZOrder|swpNoActivate)
}

func (s *Sidebar) getWindowRect() (int, int, int, int) {
	proc := user32.NewProc("GetWindowRect")
	var rect [4]int32
	proc.Call(s.hwnd, uintptr(unsafe.Pointer(&rect)))
	return int(rect[0]), int(rect[1]), int(rect[2]), int(rect[3])
}

// edgeLoop polls mouse position to auto-show/hide the panel.
func (s *Sidebar) edgeLoop() {
	ticker := time.NewTicker(pollMs * time.Millisecond)
	defer ticker.Stop()

	var hideTimer int

	for range ticker.C {
		mx, my := getCursorPos()
		inTrigger := mx >= s.screenW-triggerZonePx-4 && mx <= s.screenW &&
			my >= 0 && my <= s.screenH

		inPanel := mx >= s.shownX-2 && mx <= s.screenW+panelWidth &&
			my >= -10 && my <= s.screenH+10

		if !s.shown && inTrigger && !s.animating {
			s.shown = true
			s.hidden = false
			s.wv.Dispatch(func() { go s.slide(s.shownX) })
		} else if s.shown && !inPanel && !s.animating {
			hideTimer++
			if hideTimer*pollMs >= hideDelayMs {
				hideTimer = 0
				s.shown = false
				s.hidden = true
				s.wv.Dispatch(func() { go s.slide(s.hiddenX) })
			}
		} else if s.shown && inPanel {
			hideTimer = 0
		}
	}
}
