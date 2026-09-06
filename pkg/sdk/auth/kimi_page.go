package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

// RunKimiPage 打开 Kimi 会员订阅页并回放鉴权状态，同时启动页面内 Token 自动轮换监听。
func RunKimiPage(pageURL, envelopeJSON string) error {
	if err := validateKimiPageURL(pageURL); err != nil {
		return err
	}
	var env kimi.KimiAuthEnvelope
	if err := env.Decode([]byte(envelopeJSON)); err != nil {
		return err
	}
	browser, err := launchKimiBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return runKimiPage(ctx, browser, pageURL, &env)
}

// RunKimiAddonPage 兼容旧版入口，直接定位到 membership 页面。
func RunKimiAddonPage(envelopeJSON string) error {
	return RunKimiPage(kimiMembershipURL, envelopeJSON)
}

func runKimiPage(ctx context.Context, browser kimiLoginBrowser, pageURL string, env *kimi.KimiAuthEnvelope) error {
	failAndWait := func(errMsg error) error {
		SignalOpenPageErrorOnce(errMsg.Error())
		_ = browser.Wait()
		return errMsg
	}

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return failAndWait(fmt.Errorf("连接 Kimi 账户页浏览器失败: %w", err))
	}
	defer cdp.Close()

	cookies := kimiEnvelopeCookies(env)
	if len(cookies) > 0 {
		result := cdp.SetCookiesBestEffort(ctx, cookies)
		log.Printf("kimi: 账户页 cookie 回放完成，注入 %d 个，失败 %d 个", result.Injected, len(result.Failed))
	}
	if script := kimiStorageRestoreScript(env); script != "" {
		if err := cdp.AddScriptOnNewDocument(ctx, script); err != nil {
			return failAndWait(fmt.Errorf("准备 Kimi 登录态脚本失败: %w", err))
		}
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 Kimi 账户页网络事件失败: %w", err))
	}
	if err := cdp.EnablePage(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 Kimi 账户页页面事件失败: %w", err))
	}
	events := cdp.Events()

	log.Printf("kimi: 账户页导航")
	loader, err := cdp.NavigateWithLoader(ctx, pageURL, kimiHost)
	if err != nil {
		return failAndWait(fmt.Errorf("打开 Kimi 账户页失败: %w", err))
	}
	log.Printf("kimi: 导航已发送（loader %s）", loader)
	if err := kimiWaitForAuthDecision(ctx, cdp, events, loader, kimiSettleTimeout); err != nil {
		if !isKimiExpectedTimeout(err) {
			return failAndWait(fmt.Errorf("等待 Kimi 账户页鉴权决定失败: %w", err))
		}
		return failAndWait(fmt.Errorf("Kimi 账户页未观测到受保护接口响应（鉴权未通过），请重新登录"))
	}

	postURL, _ := cdp.PageURL(ctx, kimiHost)
	log.Printf("kimi: 最终 URL host=%s path=%s", hostOnly(postURL), pathOnly(postURL))
	if !isKimiMembershipPage(postURL) {
		return failAndWait(fmt.Errorf("Kimi 登录态恢复失败：页面未停留在 membership（path=%s），请重新登录", pathOnly(postURL)))
	}

	log.Printf("kimi: 账户页已认证（membership 页，受保护接口 200，三 metric 有效）")
	SignalOpenPageReady()

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	stopWatch := make(chan struct{})
	var watcherWG sync.WaitGroup
	if KimiPageRotationSave != nil {
		watcherWG.Add(1)
		go func() {
			defer watcherWG.Done()
			kimiWatchInPageRotation(watchCtx, cdp, events, env.AccessToken(), env.RefreshToken(), stopWatch, KimiPageRotationSave)
		}()
	}
	waitErr := browser.Wait()
	close(stopWatch)
	watcherWG.Wait()
	cancelWatch()
	if waitErr != nil {
		return fmt.Errorf("Kimi 账户页浏览器异常退出: %w", waitErr)
	}
	return nil
}

type kimiRequestFacts struct {
	url      string
	token    string
	status   int
	finalURL string
}

func kimiWatchInPageRotation(ctx context.Context, cdp kimiCDP, events <-chan browserauth.Event, initAccess, initRefresh string, stop <-chan struct{}, save func(prevAccess, prevRefresh, newAccess, newRefresh string) (bool, error)) {
	facts := map[string]*kimiRequestFacts{}
	lastAccess, lastRefresh := initAccess, initRefresh
	fact := func(rid string) *kimiRequestFacts {
		f, ok := facts[rid]
		if !ok {
			f = &kimiRequestFacts{}
			facts[rid] = f
		}
		return f
	}

	persist := func(newAccess, newRefresh, source string) {
		persisted, err := save(lastAccess, lastRefresh, newAccess, newRefresh)
		switch {
		case err != nil:
			log.Printf("kimi: 页面内 token 轮换持久化失败（%s）: %v", source, err)
		case !persisted:
			log.Printf("kimi: 页面内 token 轮换保存 skipped（%s，磁盘已前进；页面 access 长度 %d，新 access 长度 %d）", source, len(lastAccess), len(newAccess))
		default:
			log.Printf("kimi: 页面内 token 轮换已捕获并持久化（%s，access 长度 %d→%d）", source, len(lastAccess), len(newAccess))
			lastAccess, lastRefresh = newAccess, newRefresh
		}
	}

	handle := func(ev browserauth.Event) {
		if rh, ok := browserauth.DecodeRequestHeadersEvent(ev); ok {
			if rh.RequestID == "" {
				return
			}
			f := fact(rh.RequestID)
			if rh.URL != "" {
				f.url = rh.URL
			}
			if tok := browserauth.BearerToken(rh.Headers); tok != "" {
				f.token = tok
			}
			return
		}
		if rr, ok := browserauth.DecodeResponseReceivedEvent(ev); ok {
			if rr.RequestID != "" {
				fr := fact(rr.RequestID)
				fr.status = rr.Status
				if rr.URL != "" {
					fr.finalURL = rr.URL
				}
			}
			return
		}
		if ev.Method == "Network.loadingFailed" {
			var p struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(ev.Params, &p) == nil && p.RequestID != "" {
				delete(facts, p.RequestID)
			}
			return
		}
		lf, ok := browserauth.DecodeLoadingFinishedEvent(ev)
		if !ok {
			return
		}
		f, ok := facts[lf.RequestID]
		if !ok {
			return
		}
		delete(facts, lf.RequestID)

		if isKimiRefreshTokenURL(f.url) {
			if f.status < 200 || f.status >= 300 {
				log.Printf("kimi: RefreshToken 响应状态 %d，不计为轮换", f.status)
				return
			}
			if f.finalURL == "" || !isKimiRefreshTokenURL(f.finalURL) {
				log.Printf("kimi: RefreshToken 最终响应 URL 偏离精确 allowlist（重定向），不计为签发证据")
				return
			}
			pair, ok := kimiParseRefreshResponseBody(ctx, cdp, lf.RequestID)
			if !ok {
				log.Printf("kimi: RefreshToken 响应体未通过严格校验（事件止于体校验），不计为轮换")
				return
			}
			if pair.access == lastAccess {
				log.Printf("kimi: RefreshToken 签发的 access（长度 %d）与当前一致，非轮换，跳过", len(pair.access))
				return
			}
			log.Printf("kimi: RefreshToken 轮换签发已观测（新 access 长度 %d），立即 CAS 持久化", len(pair.access))
			persist(pair.access, pair.refresh, "RefreshToken 签发证据")
			return
		}

		if f.token == "" || f.token == lastAccess {
			return
		}
		hostPath := "?"
		if u, err := url.Parse(f.url); err == nil {
			hostPath = u.Host + u.Path
		}
		if !isKimiProtectedURL(f.url) {
			log.Printf("kimi: 新 token（长度 %d）出现在非 quota 端点 %s，不计为轮换证据", len(f.token), hostPath)
			return
		}
		if f.status < 200 || f.status >= 300 {
			log.Printf("kimi: 新 token（长度 %d）的 quota 请求状态 %d，不计为轮换证据", len(f.token), f.status)
			return
		}
		if f.finalURL == "" || !isKimiProtectedURL(f.finalURL) {
			log.Printf("kimi: 新 token（长度 %d）的 quota 最终响应 URL 偏离精确 allowlist（重定向），不计为轮换证据", len(f.token))
			return
		}
		if !kimiResponseBodyValid(ctx, cdp, lf.RequestID) {
			log.Printf("kimi: 新 token（长度 %d）的 quota 响应体无效，不计为轮换证据", len(f.token))
			return
		}
		at := kimiReadLocalStorage(ctx, cdp, "access_token")
		rt := kimiReadLocalStorage(ctx, cdp, "refresh_token")
		if at == "" || rt == "" {
			log.Printf("kimi: quota 新 token 已证据，但 localStorage 读取失败（页面可能已关闭）")
			return
		}
		if at != f.token {
			log.Printf("kimi: localStorage access（长度 %d）与证据 token（长度 %d）不一致，跳过（可能再次轮换）", len(at), len(f.token))
			return
		}
		persist(at, rt, "quota 证据+localStorage 一致")
	}

	stopped := false
	for {
		if stopped {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				handle(ev)
			case <-ctx.Done():
				return
			}
			continue
		}
		select {
		case <-stop:
			stopped = true
		case ev, ok := <-events:
			if !ok {
				return
			}
			handle(ev)
		case <-ctx.Done():
			return
		}
	}
}

func kimiStorageRestoreScript(env *kimi.KimiAuthEnvelope) string {
	at := env.AccessToken()
	rt := env.RefreshToken()
	if at == "" && rt == "" {
		return ""
	}
	atJSON, _ := json.Marshal(at)
	rtJSON, _ := json.Marshal(rt)
	return `(function(){try{` +
		`if(localStorage){` +
		fmt.Sprintf(`localStorage.setItem("access_token",%s);`, string(atJSON)) +
		fmt.Sprintf(`localStorage.setItem("refresh_token",%s);`, string(rtJSON)) +
		`}}catch(e){}})();`
}

func kimiWaitForAuthDecision(ctx context.Context, cdp kimiCDP, events <-chan browserauth.Event, _ string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var epochLoaderID string
	var pendingRequestID string
	phase := 0
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &kimiAuthTimeoutError{}
		}
		select {
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("CDP 事件通道已关闭")
			}
			if phase == 0 {
				if fsn, ok := browserauth.DecodeFrameStartedNavigatingEvent(ev); ok && fsn.LoaderID != "" {
					epochLoaderID = fsn.LoaderID
					phase = 1
					log.Printf("kimi: 导航 epoch 已确定（loaderId 长度 %d）", len(epochLoaderID))
				}
				continue
			}
			if phase == 1 {
				if rr, ok := browserauth.DecodeResponseReceivedEvent(ev); ok {
					if !(rr.Status >= 200 && rr.Status < 300) || !isKimiProtectedURL(rr.URL) || rr.RequestID == "" {
						continue
					}
					var raw struct {
						LoaderID string `json:"loaderId"`
					}
					if json.Unmarshal(ev.Params, &raw) != nil || raw.LoaderID != epochLoaderID {
						continue
					}
					pendingRequestID = rr.RequestID
					phase = 2
					log.Printf("kimi: 受保护接口 2xx 已观测（loaderId 匹配），等待 loadingFinished")
					continue
				}
				continue
			}
			if phase == 2 {
				if lf, ok := browserauth.DecodeLoadingFinishedEvent(ev); ok {
					if lf.RequestID != pendingRequestID {
						continue
					}
					if !kimiResponseBodyValid(ctx, cdp, lf.RequestID) {
						return fmt.Errorf("受保护接口响应体校验失败（Connect 错误或缺少双 meter）")
					}
					postURL, err := cdp.PageURL(ctx, kimiHost)
					if err != nil {
						return fmt.Errorf("读取鉴权后 URL 失败: %w", err)
					}
					if !isKimiConsolePage(postURL) {
						return fmt.Errorf("受保护接口响应有效但页面未在 console（path=%s）", pathOnly(postURL))
					}
					log.Printf("kimi: 鉴权决定已观测（受保护接口 200，loaderId 匹配，双 meter 有效，console 页）")
					return nil
				}
				continue
			}
		case <-time.After(remaining):
			return &kimiAuthTimeoutError{}
		case <-ctx.Done():
			return fmt.Errorf("等待鉴权决定被取消: %w", ctx.Err())
		}
	}
}

func kimiResponseBodyValid(ctx context.Context, cdp kimiCDP, requestID string) bool {
	body, err := cdp.GetResponseBody(ctx, requestID)
	if err != nil || body == "" {
		return false
	}
	_, err = kimi.ParseKimiQuota(body, time.Now())
	return err == nil
}

type kimiIssuedPair struct {
	access  string
	refresh string
}

func kimiParseRefreshResponseBody(ctx context.Context, cdp kimiCDP, requestID string) (kimiIssuedPair, bool) {
	body, err := cdp.GetResponseBody(ctx, requestID)
	if err != nil || body == "" || len(body) > kimiRefreshBodyMaxBytes {
		return kimiIssuedPair{}, false
	}
	var parsed struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if json.Unmarshal([]byte(body), &parsed) != nil {
		return kimiIssuedPair{}, false
	}
	at, rt := parsed.AccessToken, parsed.RefreshToken
	if at == "" || rt == "" || len(at) > kimiIssuedTokenMaxLen || len(rt) > kimiIssuedTokenMaxLen {
		return kimiIssuedPair{}, false
	}
	if strings.ContainsAny(at+rt, " \t\r\n;") {
		return kimiIssuedPair{}, false
	}
	return kimiIssuedPair{access: at, refresh: rt}, true
}

func kimiEnvelopeCookies(env *kimi.KimiAuthEnvelope) []browserauth.Cookie {
	if env == nil {
		return nil
	}
	raw, ok := env.Field("cookie")
	if !ok || raw == "" {
		return nil
	}
	out := make([]browserauth.Cookie, 0)
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok || name == "" || value == "" {
			continue
		}
		if strings.ContainsAny(name+value, ";\r\n") {
			continue
		}
		out = append(out, browserauth.Cookie{
			Name: name, Value: value, Domain: kimiHost, Path: "/", Secure: true,
		})
	}
	return out
}
