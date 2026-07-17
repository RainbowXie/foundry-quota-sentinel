//go:build linux && !nogui

package sidebar

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
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

static void ollama_cookie_snapshot_ready(GObject* source, GAsyncResult* result, gpointer user_data) {
    gchar* path = user_data;
    GError* error = NULL;
    GList* cookies = webkit_cookie_manager_get_cookies_finish(WEBKIT_COOKIE_MANAGER(source), result, &error);
    if (cookies) {
        FILE* file = fopen(path, "w");
        if (file) {
            fprintf(file, "# Netscape HTTP Cookie File\\n");
            for (GList* item = cookies; item; item = item->next) {
                SoupCookie* cookie = item->data;
                const char* domain = soup_cookie_get_domain(cookie);
                if (domain && (!strcmp(domain, "ollama.com") || !strcmp(domain, ".ollama.com"))) {
                    fprintf(file, "#HttpOnly_%s\\tTRUE\\t/\\tTRUE\\t2000000000\\t%s\\t%s\\n", domain, soup_cookie_get_name(cookie), soup_cookie_get_value(cookie));
                }
            }
            fclose(file);
        }
        g_list_free_full(cookies, (GDestroyNotify)soup_cookie_free);
    }
    if (error) g_error_free(error);
    g_free(path);
}

static void ollama_snapshot_cookies(void* window, const char* path) {
    if (!window || !GTK_IS_BIN(window)) return;
    GtkWidget* child = gtk_bin_get_child(GTK_BIN(window));
    if (!child || !WEBKIT_IS_WEB_VIEW(child)) return;
    WebKitWebContext* ctx = webkit_web_view_get_context(WEBKIT_WEB_VIEW(child));
    WebKitCookieManager* cm = webkit_web_context_get_cookie_manager(ctx);
    webkit_cookie_manager_get_cookies(cm, "https://ollama.com", NULL, ollama_cookie_snapshot_ready, g_strdup(path));
}

static void ollama_capture_settings_request(WebKitURIRequest* request, gpointer user_data) {
    const char* uri = webkit_uri_request_get_uri(request);
    if (!uri || !g_str_has_prefix(uri, "https://ollama.com/settings")) return;
    SoupMessageHeaders* headers = webkit_uri_request_get_http_headers(request);
    const char* cookie = headers ? soup_message_headers_get_one(headers, "Cookie") : NULL;
    FILE* file = fopen((const char*)user_data, "w");
    if (file) { fprintf(file, "FQS-Cookie: %s\\n", cookie ? cookie : ""); fclose(file); }
}

static gboolean ollama_capture_settings_resource_request(WebKitWebResource* resource, WebKitURIRequest* request, WebKitURIResponse* redirected_response, gpointer user_data) {
    ollama_capture_settings_request(request, user_data);
    return FALSE;
}

static void ollama_write_set_cookie_header(const char* name, const char* value, gpointer user_data) {
    if (g_ascii_strcasecmp(name, "Set-Cookie") != 0) return;
    FILE* file = (FILE*)user_data;
    fprintf(file, "FQS-Set-Cookie: %s\\n", value);
}

static void ollama_capture_callback_response(GObject* object, GParamSpec* pspec, gpointer user_data) {
    WebKitWebResource* resource = WEBKIT_WEB_RESOURCE(object);
    const char* uri = webkit_web_resource_get_uri(resource);
    if (!uri || !g_str_has_prefix(uri, "https://ollama.com/auth/callback")) return;
    WebKitURIResponse* response = webkit_web_resource_get_response(resource);
    if (!response) return;
    SoupMessageHeaders* headers = webkit_uri_response_get_http_headers(response);
    if (!headers) return;
    FILE* file = fopen((const char*)user_data, "w");
    if (!file) return;
    soup_message_headers_foreach(headers, ollama_write_set_cookie_header, file);
    fclose(file);
}

static void ollama_resource_load_started(WebKitWebView* web_view, WebKitWebResource* resource, WebKitURIRequest* request, gpointer user_data) {
    ollama_capture_settings_request(request, user_data);
    g_signal_connect(resource, "send-request", G_CALLBACK(ollama_capture_settings_resource_request), user_data);
    g_signal_connect(resource, "notify::response", G_CALLBACK(ollama_capture_callback_response), user_data);
}

static void ollama_capture_request_cookies(void* window, const char* path) {
    if (!window || !GTK_IS_BIN(window)) return;
    GtkWidget* child = gtk_bin_get_child(GTK_BIN(window));
    if (!child || !WEBKIT_IS_WEB_VIEW(child)) return;
    g_signal_connect(WEBKIT_WEB_VIEW(child), "resource-load-started", G_CALLBACK(ollama_resource_load_started), g_strdup(path));
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

const ollamaHomeURL = "https://ollama.com/"

const ollamaLocationWatchJS = `
(function(){
  function send(){ try{if(window.__ocgtOllamaLocation)window.__ocgtOllamaLocation(location.href)}catch(e){} }
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
	var metadata []ollamaCookieMeta
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "FQS-Set-Cookie: ") {
			part := strings.TrimPrefix(line, "FQS-Set-Cookie: ")
			if end := strings.IndexByte(part, ';'); end >= 0 {
				part = part[:end]
			}
			if eq := strings.IndexByte(part, '='); eq > 0 {
				parts = append(parts, part)
				metadata = append(metadata, ollamaCookieMeta{Name: part[:eq], ValueLength: len(part[eq+1:])})
			}
			continue
		}
		if strings.HasPrefix(line, "FQS-Cookie: ") {
			header := strings.TrimPrefix(line, "FQS-Cookie: ")
			for _, part := range strings.Split(header, ";") {
				part = strings.TrimSpace(part)
				if eq := strings.IndexByte(part, '='); eq > 0 {
					metadata = append(metadata, ollamaCookieMeta{Name: part[:eq], ValueLength: len(part[eq+1:])})
				}
			}
			logOllamaLogin("settings request cookie captured: count=%d cookies=%s", len(metadata), redactedOllamaCookieSummary(metadata))
			return header
		}
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
		metadata = append(metadata, ollamaCookieMeta{Name: columns[5], ValueLength: len(columns[6])})
	}
	logOllamaLogin("cookie snapshot read: count=%d cookies=%s", len(metadata), redactedOllamaCookieSummary(metadata))
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

func snapshotOllamaCookies(w webview.WebView, snapshotPath string) {
	snapshotPathC := C.CString(snapshotPath)
	C.ollama_snapshot_cookies(w.Window(), snapshotPathC)
	C.free(unsafe.Pointer(snapshotPathC))
}

func captureOllamaRequestCookies(w webview.WebView, snapshotPath string) {
	snapshotPathC := C.CString(snapshotPath)
	C.ollama_capture_request_cookies(w.Window(), snapshotPathC)
	C.free(unsafe.Pointer(snapshotPathC))
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
	logOllamaLogin("login window started")
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
	snapshot, err := os.CreateTemp("", "ocgt-ollama-snapshot-*.txt")
	if err != nil {
		return "", fmt.Errorf("创建 Ollama cookie 快照文件失败: %w", err)
	}
	snapshotPath := snapshot.Name()
	_ = snapshot.Close()
	defer os.Remove(snapshotPath)
	setOllamaCookieStorage(w, cookiePath)
	setOllamaUserAgent(w)
	captureOllamaRequestCookies(w, snapshotPath)

	lifecycle := newOllamaLoginLifecycle()
	w.Bind("__ocgtOllamaCandidate", func(source, candidate string) {
		if !lifecycle.startValidation() {
			return
		}
		logOllamaLogin("storage/request candidate observed: source=%s len=%d", source, len(candidate))
		go func() {
			if validate(candidate) {
				logOllamaLogin("storage/request candidate validation succeeded")
				lifecycle.finishValidation(candidate, func() {
					w.Dispatch(func() { w.Terminate() })
				})
				return
			}
			logOllamaLogin("storage/request candidate validation failed")
			lifecycle.finishValidation("", func() {})
		}()
	})
	snapshotRequested := false
	w.Bind("__ocgtOllamaLocation", func(href string) {
		if !isOllamaLoginCompleteURL(href) {
			return
		}
		logOllamaLogin("authentication-complete location observed")
		if !snapshotRequested {
			snapshotRequested = true
			logOllamaLogin("cookie snapshot requested; waiting for next location tick")
			return
		}
		if !lifecycle.startValidation() {
			return
		}
		go func() {
			captured := readOllamaCookies(snapshotPath)
			if captured != "" && validate(captured) {
				logOllamaLogin("settings validation succeeded")
				lifecycle.finishValidation(captured, func() {
					w.Dispatch(func() { w.Terminate() })
				})
				return
			}
			logOllamaLogin("settings validation failed or cookie snapshot was empty")
			lifecycle.finishValidation("", func() {})
		}()
	})

	// Loading the home page first establishes Ollama's first-party state and a
	// same-origin referrer before entering the authentication route. Direct
	// WebKitGTK navigation to /signin can otherwise receive a gateway timeout.
	w.Init(ollamaLoginBootstrapJS())
	w.Init(ollamaAuthCaptureJS())
	w.Init(ollamaLocationWatchJS)
	w.Navigate(ollamaHomeURL)
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
