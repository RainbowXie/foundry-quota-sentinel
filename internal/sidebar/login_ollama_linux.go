//go:build linux && !nogui

package sidebar

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0
#include <stdlib.h>
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

// ollama_set_cookie_storage configures WebKit's persistent text cookie store
// before navigation, so it includes httpOnly cookies as well.
static void ollama_set_cookie_storage(void* window, const char* path) {
    if (!window || !GTK_IS_BIN(window)) return;
    GtkWidget* child = gtk_bin_get_child(GTK_BIN(window));
    if (!child || !WEBKIT_IS_WEB_VIEW(child)) return;
    WebKitWebView* wv = WEBKIT_WEB_VIEW(child);
    WebKitWebContext* ctx = webkit_web_view_get_context(wv);
    WebKitCookieManager* cm = webkit_web_context_get_cookie_manager(ctx);
    webkit_cookie_manager_set_persistent_storage(cm, path, WEBKIT_COOKIE_PERSISTENT_STORAGE_TEXT);
}

static void ollama_set_user_agent(void* window, const char* user_agent) {
    if (!window || !GTK_IS_BIN(window)) return;
    GtkWidget* child = gtk_bin_get_child(GTK_BIN(window));
    if (!child || !WEBKIT_IS_WEB_VIEW(child)) return;
    WebKitSettings* settings = webkit_web_view_get_settings(WEBKIT_WEB_VIEW(child));
    webkit_settings_set_user_agent(settings, user_agent);
}
*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"github.com/webview/webview_go"
)

const ollamaSignInURL = "https://ollama.com/signin"

const ollamaUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const ollamaLocationWatchJS = `
(function(){
  function send(){ try{ if(window.__ocgtOllamaLocation) window.__ocgtOllamaLocation(location.href); }catch(e){} }
  send(); setInterval(send, 2000);
})();
`

// readOllamaCookies extracts only cookies sent to ollama.com from WebKit's
// Netscape-format persistent store. #HttpOnly_ entries are request cookies too.
func readOllamaCookies(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var parts []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || (strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_")) {
			continue
		}
		line = strings.TrimPrefix(line, "#HttpOnly_")
		columns := strings.Split(line, "\t")
		if len(columns) < 7 {
			continue
		}
		domain := columns[0]
		if domain != "ollama.com" && domain != ".ollama.com" {
			continue
		}
		parts = append(parts, columns[5]+"="+columns[6])
	}
	return strings.Join(parts, "; ")
}

// writeOllamaCookies serializes a Cookie header into WebKit's Netscape store.
func writeOllamaCookies(f *os.File, cookie string) {
	const expiry = "2000000000"
	w := bufio.NewWriter(f)
	_, _ = w.WriteString("# Netscape HTTP Cookie File\n")
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "#HttpOnly_.ollama.com\tTRUE\t/\tTRUE\t%s\t%s\t%s\n", expiry, part[:eq], part[eq+1:])
	}
	_ = w.Flush()
}

func setOllamaCookieStorage(w webview.WebView, cookiePath string) {
	cookiePathC := C.CString(cookiePath)
	C.ollama_set_cookie_storage(w.Window(), cookiePathC)
	C.free(unsafe.Pointer(cookiePathC))
}

func setOllamaUserAgent(w webview.WebView) {
	userAgentC := C.CString(ollamaUserAgent)
	C.ollama_set_user_agent(w.Window(), userAgentC)
	C.free(unsafe.Pointer(userAgentC))
}

// RunOllamaPage restores the saved cookie jar before opening the requested page.
func RunOllamaPage(pageURL, cookie string) error {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Ollama · 账户页")
	w.SetSize(1100, 760, webview.HintNone)

	f, err := os.CreateTemp("", "ocgt-ollama-open-*.txt")
	if err != nil {
		return fmt.Errorf("创建临时 Ollama cookie 文件失败: %w", err)
	}
	cookiePath := f.Name()
	writeOllamaCookies(f, cookie)
	_ = f.Close()
	defer os.Remove(cookiePath)

	setOllamaCookieStorage(w, cookiePath)
	setOllamaUserAgent(w)
	w.Navigate(pageURL)
	w.Run()
	return nil
}

// RunOllamaLogin captures the authenticated ollama.com cookie jar and only
// closes the window after the caller verifies it against the settings page.
func RunOllamaLogin(validate func(string) bool) (string, error) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("登录 Ollama（登录成功后自动获取凭证）")
	w.SetSize(560, 760, webview.HintNone)

	f, err := os.CreateTemp("", "ocgt-ollama-cookies-*.txt")
	if err != nil {
		return "", fmt.Errorf("创建临时 Ollama cookie 文件失败: %w", err)
	}
	cookiePath := f.Name()
	_ = f.Close()
	defer os.Remove(cookiePath)
	setOllamaCookieStorage(w, cookiePath)
	setOllamaUserAgent(w)

	lifecycle := newOllamaLoginLifecycle()
	w.Bind("__ocgtOllamaLocation", func(string) {
		if !lifecycle.startValidation() {
			return
		}

		go func() {
			captured := readOllamaCookies(cookiePath)
			if captured != "" && validate(captured) {
				lifecycle.finishValidation(captured, func() {
					w.Dispatch(func() { w.Terminate() })
				})
				return
			}
			lifecycle.finishValidation("", func() {})
		}()
	})

	w.Init(ollamaLocationWatchJS)
	w.Navigate(ollamaSignInURL)
	w.Run()

	// A user-close ends Run without cancelling validation. Mark it closed and
	// wait before deferred Destroy/remove make the WebView or cookie file unsafe.
	lifecycle.closeAndWait()
	captured := lifecycle.cookieValue()
	if captured == "" {
		return "", fmt.Errorf("未捕获到有效 Ollama 凭证（窗口已关闭）")
	}
	return captured, nil
}
