//go:build linux && !nogui

package sidebar

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0
#include <stdlib.h>
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

// ocgt_set_cookie_storage 从 webview 的 GtkWindow 找到 WebKitWebView 子控件，
// 给它的 cookie manager 设置文本持久化文件，使所有 cookie（含 httpOnly）落盘。
// 必须在导航前调用。
static void ocgt_set_cookie_storage(void* window, const char* path) {
    if (!window || !GTK_IS_BIN(window)) return;
    GtkWidget* child = gtk_bin_get_child(GTK_BIN(window));
    if (!child || !WEBKIT_IS_WEB_VIEW(child)) return;
    WebKitWebView* wv = WEBKIT_WEB_VIEW(child);
    WebKitWebContext* ctx = webkit_web_view_get_context(wv);
    WebKitCookieManager* cm = webkit_web_context_get_cookie_manager(ctx);
    webkit_cookie_manager_set_persistent_storage(cm, path, WEBKIT_COOKIE_PERSISTENT_STORAGE_TEXT);
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"unsafe"

	"github.com/webview/webview_go"
)

// locWatchJS 每次导航注入，定时把当前 URL 回传 Go，用于提取 workspaceID。
const locWatchJS = `
(function(){
  function send(){ try{ if(window.__ocgtLoc) window.__ocgtLoc(location.href); }catch(e){} }
  send(); setInterval(send, 2000);
})();
`

var ocWsRe = regexp.MustCompile(`wrk_[a-zA-Z0-9]+`)

// opencode.ai 走 OAuth，登录页在独立域名 auth.opencode.ai。
const openCodeAuthURL = "https://auth.opencode.ai/authorize?client_id=app&redirect_uri=https%3A%2F%2Fopencode.ai%2Fauth%2Fcallback&response_type=code"

// readOpenCodeCookies 解析 WebKit 文本格式 cookie 文件，拼出 opencode.ai 的 cookie 串。
// 文件为 Netscape/curl 格式，每行 7 列 tab 分隔；httpOnly 行以 #HttpOnly_ 前缀标记。
func readOpenCodeCookies(path string) string {
	fp, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fp.Close()
	var parts []string
	sc := bufio.NewScanner(fp)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_") {
			continue
		}
		line = strings.TrimPrefix(line, "#HttpOnly_")
		cols := strings.Split(line, "\t")
		if len(cols) < 7 {
			continue
		}
		domain, name, value := cols[0], cols[5], cols[6]
		// 精确匹配主域 opencode.ai（含 "opencode.ai" / ".opencode.ai"），
		// 排除 auth.opencode.ai 等子域的 cookie（那些不发给 opencode.ai）。
		if strings.TrimPrefix(domain, ".") == "opencode.ai" {
			parts = append(parts, name+"="+value)
		}
	}
	return strings.Join(parts, "; ")
}

// RunOpenCodeLogin 打开 opencode.ai 登录窗口，把 cookie 持久化到临时文件，
// 监听 URL 取 workspaceID，读文件拼 cookie 串后用 validate 真实验证，命中即返回。
func RunOpenCodeLogin(validate func(cookie, wsid string) bool) (string, string, error) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("登录 OpenCode Go（登录后进入你的 workspace 用量页，自动获取凭证）")
	w.SetSize(560, 760, webview.HintNone)

	f, err := os.CreateTemp("", "ocgt-oc-cookies-*.txt")
	if err != nil {
		return "", "", fmt.Errorf("创建临时 cookie 文件失败: %w", err)
	}
	cookiePath := f.Name()
	f.Close()
	defer os.Remove(cookiePath)

	cpath := C.CString(cookiePath)
	C.ocgt_set_cookie_storage(w.Window(), cpath)
	C.free(unsafe.Pointer(cpath))

	var mu sync.Mutex
	gotCookie, gotWsid := "", ""
	inflight := false
	var once sync.Once

	w.Bind("__ocgtLoc", func(href string) {
		wsid := ocWsRe.FindString(href)
		if wsid == "" {
			return
		}
		mu.Lock()
		if gotCookie != "" || inflight {
			mu.Unlock()
			return
		}
		inflight = true
		mu.Unlock()
		go func() {
			defer func() { mu.Lock(); inflight = false; mu.Unlock() }()
			cookie := readOpenCodeCookies(cookiePath)
			if cookie == "" || !validate(cookie, wsid) {
				return
			}
			mu.Lock()
			if gotCookie == "" {
				gotCookie, gotWsid = cookie, wsid
			}
			mu.Unlock()
			once.Do(func() { w.Dispatch(func() { w.Terminate() }) })
		}()
	})

	w.Init(locWatchJS)
	w.Navigate(openCodeAuthURL)
	w.Run()

	mu.Lock()
	ck, ws := gotCookie, gotWsid
	mu.Unlock()
	if ck == "" {
		return "", "", fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
	}
	return ck, ws, nil
}
