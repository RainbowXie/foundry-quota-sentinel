//go:build (windows || darwin || linux) && !nogui

package sidebar

import (
	"fmt"

	"github.com/webview/webview_go"
)

// captureJS 在每个页面加载前注入，monkey-patch fetch 与 XHR，截获请求里的
// Authorization: Bearer <token>。用户登录后页面自动发带 Bearer 的请求即被捕获。
const captureJS = `
(function(){
  function send(t){ try{ if(window.__ocgtCaptureToken) window.__ocgtCaptureToken(t); }catch(e){} }
  function pick(v){ if(!v) return; var m=/Bearer\s+([A-Za-z0-9._\-]+)/.exec(String(v)); if(m && m[1] && m[1].length>=20) send(m[1]); }
  try{
    var sh = XMLHttpRequest.prototype.setRequestHeader;
    XMLHttpRequest.prototype.setRequestHeader = function(k,v){ try{ if(String(k).toLowerCase()==='authorization') pick(v); }catch(e){} return sh.apply(this, arguments); };
  }catch(e){}
  try{
    var of = window.fetch;
    if(of){ window.fetch = function(input, init){ try{
      if(init && init.headers){ var h=init.headers;
        if(h.get){ pick(h.get('authorization')); }
        else { for(var k in h){ if(String(k).toLowerCase()==='authorization') pick(h[k]); } }
      }
    }catch(e){} return of.apply(this, arguments); }; }
  }catch(e){}
})();
`

// RunDeepSeekLogin 打开一个登录窗口指向 DeepSeek 平台，截获登录后请求中的
// Bearer token 后关窗返回。用户直接关窗（未登录）则返回错误。
func RunDeepSeekLogin() (string, error) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("登录 DeepSeek（登录后自动获取凭证，随后自动关闭）")
	w.SetSize(480, 720, webview.HintNone)

	var captured string
	w.Bind("__ocgtCaptureToken", func(t string) {
		if t == "" || captured != "" {
			return
		}
		captured = t
		w.Dispatch(func() { w.Terminate() })
	})
	w.Init(captureJS)
	w.Navigate("https://platform.deepseek.com/sign_in")
	w.Run()

	if captured == "" {
		return "", fmt.Errorf("未捕获到登录凭证（窗口已关闭）")
	}
	return captured, nil
}
