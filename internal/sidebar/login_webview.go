//go:build (windows || darwin || linux) && !nogui

package sidebar

import (
	"fmt"
	"os"
	"sync"

	"github.com/webview/webview_go"
)

// captureJS 在每个页面加载前注入（WebKitGTK 上是 document-start 的持久 user script，
// 每次导航都重跑）。它做两件事：
//  1. 扫 localStorage / sessionStorage（含 JSON 内嵌字段），把所有像 token 的字符串
//     作为候选发给 Go 端验证——因为网页用 cookie 鉴权、不发 Authorization 头，token
//     是存在 storage 里的，key 名未知故全扫。
//  2. 兜底再钩一下 fetch/XHR 的 Authorization 头（万一某些请求带）。
//
// 每个候选交给 Go 端用真实接口验证，命中即保存；同时打日志便于诊断 token 到底在哪。
const captureJS = `
(function(){
  function log(m){try{if(window.__ocgtLog)window.__ocgtLog(String(m))}catch(e){}}
  function cand(k,t){try{if(t&&window.__ocgtCandidate)window.__ocgtCandidate(String(k||''),String(t))}catch(e){}}
  var RE=/^[A-Za-z0-9._\-]{30,800}$/;
  function pushIf(s,k){ if(typeof s==='string'&&RE.test(s)) cand(k,s); }
  function walk(v){ if(typeof v==='string'){pushIf(v,'');} else if(v&&typeof v==='object'){for(var k in v){try{walk(v[k])}catch(e){}}} }
  function scanStore(store,label){
    try{ for(var i=0;i<store.length;i++){ var k=store.key(i); var v=store.getItem(k);
      log(label+' key='+k+' len='+(v?v.length:0));
      pushIf(v, label==='LS'?k:'');
      if(v && (v.charAt(0)==='{'||v.charAt(0)==='[')){ try{ walk(JSON.parse(v)); }catch(e){} }
    }}catch(e){ log(label+' scan err '+e); }
  }
  function dump(){
    var l={},s={};
    try{ for(var i=0;i<localStorage.length;i++){ var k=localStorage.key(i); l[k]=localStorage.getItem(k); } }catch(e){}
    try{ for(var i=0;i<sessionStorage.length;i++){ var k=sessionStorage.key(i); s[k]=sessionStorage.getItem(k); } }catch(e){}
    try{ if(window.__ocgtStore) window.__ocgtStore(JSON.stringify({l:l,s:s})); }catch(e){}
  }
  function scanAll(){ scanStore(localStorage,'LS'); scanStore(sessionStorage,'SS'); dump(); }
  function pickAuth(v){ if(!v)return; var m=/Bearer\s+([A-Za-z0-9._\-]+)/.exec(String(v)); if(m&&m[1]){log('hdr auth len='+m[1].length); cand('',m[1]);} }
  try{ var sh=XMLHttpRequest.prototype.setRequestHeader; XMLHttpRequest.prototype.setRequestHeader=function(k,v){ try{ if(String(k).toLowerCase()==='authorization') pickAuth(v); }catch(e){} return sh.apply(this,arguments); }; }catch(e){}
  try{ var of=window.fetch; if(of){ window.fetch=function(input,init){ try{ if(init&&init.headers){var h=init.headers; if(h.get)pickAuth(h.get('authorization')); else for(var k in h){if(String(k).toLowerCase()==='authorization')pickAuth(h[k]);}} }catch(e){} return of.apply(this,arguments); }; } }catch(e){}
  log('hook installed @ '+location.href);
  scanAll();
  var n=0,iv=setInterval(function(){ n++; scanAll(); if(n>=10)clearInterval(iv); },2000);
})();
`

// RunDeepSeekLogin 打开登录窗口指向 DeepSeek 平台，扫描页面存储里的候选凭证，
// 用 validate 逐个验证（validate 返回 true 表示该 token 能正常调接口），命中即返回。
// validate 会在独立 goroutine 调用，不阻塞 UI。
// 返回 token（供卡片调接口）及登录态快照 webStore（local/sessionStorage 的 JSON 转储
// {"l":{},"s":{}}），webStore 用于后续「打开账户页」时整份回放以恢复登录态——避免猜
// DeepSeek 把 token 存在哪个键/什么结构。
func RunDeepSeekLogin(validate func(string) bool) (string, string, error) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("登录 DeepSeek（登录后自动获取凭证，随后自动关闭）")
	w.SetSize(480, 720, webview.HintNone)

	var mu sync.Mutex
	captured, lastStore := "", ""
	capturedStore := ""
	tried := map[string]bool{}
	var once sync.Once

	w.Bind("__ocgtLog", func(msg string) {
		fmt.Fprintln(os.Stderr, "[capJS] "+msg)
	})
	w.Bind("__ocgtStore", func(js string) {
		mu.Lock()
		lastStore = js
		mu.Unlock()
	})
	w.Bind("__ocgtCandidate", func(key, t string) {
		if t == "" {
			return
		}
		mu.Lock()
		if captured != "" || tried[t] {
			mu.Unlock()
			return
		}
		tried[t] = true
		mu.Unlock()
		go func() {
			if !validate(t) {
				return
			}
			mu.Lock()
			if captured == "" {
				captured, capturedStore = t, lastStore
			}
			mu.Unlock()
			once.Do(func() { w.Dispatch(func() { w.Terminate() }) })
		}()
	})

	w.Init(captureJS)
	w.Navigate("https://platform.deepseek.com/sign_in")
	w.Run()

	mu.Lock()
	tok, store := captured, capturedStore
	mu.Unlock()
	if tok == "" {
		return "", "", fmt.Errorf("未捕获到有效凭证（窗口已关闭）")
	}
	return tok, store, nil
}

// RunDeepSeekPage 打开一个指向 DeepSeek 页面的窗口，在每次导航前把登录时快照的
// local/sessionStorage 整份回放，使页面以该账户登录态展示。窗口保持打开直到用户手动关闭。
func RunDeepSeekPage(pageURL, webStore string) error {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("DeepSeek · 账户页")
	w.SetSize(1100, 760, webview.HintNone)
	if webStore == "" {
		webStore = "{}" // 旧账户没有快照：不注入，可能需重新登录
	}
	// webStore 本身是 JSON.stringify 的结果，作为 JS 对象字面量内嵌安全。
	inject := "(function(){try{var o=" + webStore + "||{};var l=o.l||{},s=o.s||{};" +
		"for(var k in l){try{localStorage.setItem(k,l[k])}catch(e){}}" +
		"for(var k in s){try{sessionStorage.setItem(k,s[k])}catch(e){}}}catch(e){}})();"
	w.Init(inject)
	w.Navigate(pageURL)
	w.Run()
	return nil
}
