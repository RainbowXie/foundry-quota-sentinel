package sidebar

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"ocgt-monitor/internal/state"

	"github.com/webview/webview_go"
	"golang.org/x/sys/windows"
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
	procSetWindowPos    = user32.NewProc("SetWindowPos")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	panelWidth    = 280
	panelHeight   = 370
	// panelY is dynamic via state.PanelY
	triggerZonePx = 15
	animSteps     = 12
	animStepMs    = 8
	pollMs        = 40
	hideDelayMs   = 600
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	hwndTopMost   = ^uintptr(1-1)

	wsCaption      = 0x00C00000
	wsThickFrame   = 0x00040000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsMaximizeBox  = 0x00010000
	wsPopUp        = 0x80000000
	wsClipChildren = 0x02000000

	wsExToolWindow = 0x00000080
	wsExTopMost    = 0x00000008
	wsExNoActivate = 0x08000000
)

type POINT struct{ X, Y int32 }

func getScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0)
	h, _, _ := procGetSystemMetrics.Call(1)
	return int(w), int(h)
}

func getCursorPos() (int, int) {
	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return int(pt.X), int(pt.Y)
}

func intToPtr(n int) uintptr { return uintptr(int32(n)) }

type Sidebar struct {
	wv        webview.WebView
	hwnd      uintptr
	screenW   int
	hiddenX   int
	shownX    int
	shown     bool
	hidden    bool
	animating bool
	lastY     int
}

func New(port int) *Sidebar {
	runtime.LockOSThread()

	screenW, _ := getScreenSize()
	hiddenX := screenW - triggerZonePx + 4
	shownX := screenW - panelWidth

	wv := webview.New(false)
	wv.SetTitle("")
	wv.SetSize(panelWidth, panelHeight, webview.HintFixed)
	wv.Navigate(fmt.Sprintf("http://127.0.0.1:%d/sidebar.html", port))

	hwnd := uintptr(wv.Window())

	gwlStyle := intToPtr(-16)
	getWindowLong := user32.NewProc("GetWindowLongW")
	setWindowLong := user32.NewProc("SetWindowLongW")
	style, _, _ := getWindowLong.Call(hwnd, gwlStyle)
	style = style & ^uintptr(wsCaption) & ^uintptr(wsThickFrame)
	style = style & ^uintptr(wsSysMenu) & ^uintptr(wsMinimizeBox) & ^uintptr(wsMaximizeBox)
	style |= wsPopUp | wsClipChildren
	setWindowLong.Call(hwnd, gwlStyle, style)

	gwlExStyle := intToPtr(-20)
	exStyle, _, _ := getWindowLong.Call(hwnd, gwlExStyle)
	exStyle |= wsExToolWindow | wsExTopMost | wsExNoActivate
	setWindowLong.Call(hwnd, gwlExStyle, exStyle)

	procSetWindowPos.Call(hwnd, hwndTopMost, uintptr(hiddenX), uintptr(state.PanelY),
		panelWidth, panelHeight, swpNoActivate|0x0020)

	return &Sidebar{
		wv: wv, hwnd: hwnd,
		screenW: screenW,
		hiddenX: hiddenX, shownX: shownX,
		hidden: true,
		lastY: state.PanelY,
	}
}

func (s *Sidebar) Run() {
	if hideConsole := user32.NewProc("ShowWindow"); hideConsole != nil {
		if getConsoleWin := kernel32.NewProc("GetConsoleWindow"); getConsoleWin != nil {
			hwndConsole, _, _ := getConsoleWin.Call()
			if hwndConsole != 0 {
				hideConsole.Call(hwndConsole, 0)
			}
		}
	}
	go s.edgeLoop()
	s.wv.Run()
	s.wv.Destroy()
}

func (s *Sidebar) slide(targetX int) {
	s.animating = true
	defer func() { s.animating = false }()

	x, _, _, _ := s.getWindowRect()
	dx := (targetX - x) / animSteps
	if dx == 0 {
		if targetX > x { dx = 1 } else { dx = -1 }
	}
	for i := 0; i < animSteps; i++ {
		x += dx
		if (dx > 0 && x > targetX) || (dx < 0 && x < targetX) { x = targetX }
		procSetWindowPos.Call(s.hwnd, hwndTopMost, uintptr(x), uintptr(state.PanelY),
			panelWidth, panelHeight, swpNoActivate)
		time.Sleep(animStepMs * time.Millisecond)
		if x == targetX { break }
	}
	procSetWindowPos.Call(s.hwnd, hwndTopMost, uintptr(targetX), uintptr(state.PanelY),
		panelWidth, panelHeight, swpNoActivate)
}

func (s *Sidebar) getWindowRect() (int, int, int, int) {
	proc := user32.NewProc("GetWindowRect")
	var rect [4]int32
	proc.Call(s.hwnd, uintptr(unsafe.Pointer(&rect)))
	return int(rect[0]), int(rect[1]), int(rect[2]), int(rect[3])
}

func (s *Sidebar) reTopMost() {
	procSetWindowPos.Call(s.hwnd, hwndTopMost, 0, 0, 0, 0,
		swpNoActivate|swpNoZOrder|swpNoSize|swpNoMove)
}

func (s *Sidebar) edgeLoop() {
	ticker := time.NewTicker(pollMs * time.Millisecond)
	defer ticker.Stop()

	var hideTimer int
	var topMostTicker int

	for range ticker.C {
		// Real-time drag: move window when PanelY changes
		if state.PanelY != s.lastY {
			s.lastY = state.PanelY
			x, _, _, _ := s.getWindowRect()
			s.wv.Dispatch(func() {
				procSetWindowPos.Call(s.hwnd, hwndTopMost, uintptr(x), uintptr(state.PanelY),
					panelWidth, panelHeight, swpNoActivate)
			})
		}
		if state.Pinned {
			if !s.shown {
				s.shown = true
				s.hidden = false
				s.wv.Dispatch(func() { go s.slide(s.shownX) })
			}
			hideTimer = 0
		}
		mx, my := getCursorPos()

		inTrigger := mx >= s.screenW-triggerZonePx && mx <= s.screenW+panelWidth &&
			my >= state.PanelY-40 && my <= state.PanelY+panelHeight+60

		inPanel := mx >= s.shownX-10 && mx <= s.screenW+panelWidth &&
			my >= state.PanelY-20 && my <= state.PanelY+panelHeight+20

		if !s.shown && inTrigger && !s.animating {
			s.shown = true
			s.hidden = false
			hideTimer = 0
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

		topMostTicker++
		if topMostTicker >= 50 {
			topMostTicker = 0
			if s.shown { s.reTopMost() }
		}
	}
}
