package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

var deepSeekSettleTimeout = 5 * time.Second
var deepSeekSignInPollInterval = 300 * time.Millisecond
var deepSeekSignInPollTimeout = 800 * time.Millisecond

type deepSeekAuthTimeoutError struct{}

func (e *deepSeekAuthTimeoutError) Error() string { return "等待鉴权决定超时" }

func isDeepSeekExpectedNavTimeout(err error) bool {
	var target *deepSeekAuthTimeoutError
	return errors.As(err, &target)
}

type deepSeekAuthRejectedError struct{}

func (e *deepSeekAuthRejectedError) Error() string { return "SPA 拒绝登录态（/sign_in）" }

func isDeepSeekExpectedNavRejection(err error) bool {
	var target *deepSeekAuthRejectedError
	return errors.As(err, &target)
}

// RunDeepSeekPage 恢复持久化的 WebStore 并打开 DeepSeek 用量页面，若遭遇鉴权决定失效则等待用户关闭。
func RunDeepSeekPage(pageURL, webStore string) error {
	if err := validateDeepSeekPageURL(pageURL); err != nil {
		return err
	}
	if _, _, err := deepSeekRestoreState(webStore); err != nil {
		return err
	}
	browser, err := launchDeepSeekBrowser(context.Background(), "about:blank")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return runDeepSeekPage(ctx, browser, pageURL, webStore)
}

func runDeepSeekPage(ctx context.Context, browser deepSeekLoginBrowser, pageURL, webStore string) error {
	script, cookies, err := deepSeekRestoreState(webStore)
	if err != nil {
		return err
	}
	authEntries := deepSeekAuthStorageEntries(deepSeekExpectedStorageEntries(webStore))
	if len(authEntries) == 0 {
		return fmt.Errorf("DeepSeek 登录态恢复失败：缺少 userToken 认证键")
	}

	failAndWait := func(errMsg error) error {
		SignalOpenPageErrorOnce(errMsg.Error())
		_ = browser.Wait()
		return errMsg
	}

	cdp, err := browser.CDP(ctx)
	if err != nil {
		return failAndWait(fmt.Errorf("连接 DeepSeek 账户页浏览器失败: %w", err))
	}
	defer cdp.Close()

	if len(cookies) > 0 {
		result := cdp.SetCookiesBestEffort(ctx, cookies)
		if result.Injected == 0 {
			return failAndWait(fmt.Errorf("恢复 DeepSeek 登录 cookie 失败：全部 %d 个注入失败（%d 个被过滤）", len(cookies), len(result.Failed)))
		}
		log.Printf("deepseek: 账户页 cookie 回放完成，注入 %d 个，失败 %d 个", result.Injected, len(result.Failed))
	}
	if script != "" {
		if err := cdp.AddScriptOnNewDocument(ctx, script); err != nil {
			return failAndWait(fmt.Errorf("准备 DeepSeek 登录态脚本失败: %w", err))
		}
	}
	if err := cdp.EnableNetwork(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 DeepSeek 账户页网络事件失败: %w", err))
	}
	if err := cdp.EnablePage(ctx); err != nil {
		return failAndWait(fmt.Errorf("启用 DeepSeek 账户页页面事件失败: %w", err))
	}
	events := cdp.Events()

	log.Printf("deepseek: 账户页首次导航")
	loader1, err := cdp.NavigateWithLoader(ctx, pageURL, deepSeekHost)
	if err != nil {
		return failAndWait(fmt.Errorf("打开 DeepSeek 账户页失败: %w", err))
	}
	log.Printf("deepseek: 首次导航已发送（loader %s）", loader1)

	nav1Err := deepSeekWaitForAuthDecision(ctx, cdp, events, loader1, true, deepSeekSettleTimeout)
	if nav1Err != nil {
		rejected := isDeepSeekExpectedNavRejection(nav1Err)
		if !rejected && !isDeepSeekExpectedNavTimeout(nav1Err) {
			return failAndWait(fmt.Errorf("等待 DeepSeek 账户页首次鉴权决定失败: %w", nav1Err))
		}
		if rejected {
			log.Printf("deepseek: 观测到 /sign_in 跳转（SPA 拒绝登录态），提前 reload")
		} else {
			log.Printf("deepseek: 首次导航未观测到受保护接口响应（哨兵超时：SPA 覆盖了 userToken）")
		}
		if script != "" {
			log.Printf("deepseek: post-load 重新应用登录态脚本")
			if _, err := cdp.Evaluate(ctx, script); err != nil {
				return failAndWait(fmt.Errorf("post-load 重新应用登录态脚本失败: %w", err))
			}
		}
		log.Printf("deepseek: 账户页重新导航（reload）")
		loader2, err := cdp.NavigateWithLoader(ctx, pageURL, deepSeekHost)
		if err != nil {
			return failAndWait(fmt.Errorf("重新打开 DeepSeek 账户页失败: %w", err))
		}
		log.Printf("deepseek: 重新导航已发送（loader %s）", loader2)
		if err := deepSeekWaitForAuthDecision(ctx, cdp, events, loader2, false, deepSeekSettleTimeout); err != nil {
			return failAndWait(fmt.Errorf("等待 DeepSeek 账户页重新加载鉴权决定失败: %w", err))
		}
	}

	postURL, _ := cdp.PageURL(ctx, deepSeekHost)
	log.Printf("deepseek: 最终 URL host=%s path=%s", hostOnly(postURL), pathOnly(postURL))
	if isDeepSeekLoginPage(postURL) || !deepSeekIsUsagePage(postURL) {
		mismatch := deepSeekStorageMismatch(ctx, cdp, authEntries)
		return failAndWait(fmt.Errorf("DeepSeek 登录态恢复失败：页面未停留在 usage（有 %d 个键不匹配），请重新登录", len(mismatch)))
	}
	if mismatch := deepSeekStorageMismatch(ctx, cdp, authEntries); len(mismatch) > 0 {
		return failAndWait(fmt.Errorf("DeepSeek 登录态恢复失败：页面在 usage 但 userToken 有 %d 个键长度不匹配", len(mismatch)))
	}

	log.Printf("deepseek: 账户页已认证（usage 页，userToken 长度匹配）")
	SignalOpenPageReady()
	if err := browser.Wait(); err != nil {
		return fmt.Errorf("DeepSeek 账户页浏览器异常退出: %w", err)
	}
	return nil
}

func deepSeekWaitForAuthDecision(ctx context.Context, cdp deepSeekCDP, events <-chan browserauth.Event, _ string, allowSignInEarlyExit bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var epochLoaderID string
	var pendingRequestID string
	phase := 0
	signInStreak := 0
	var signInTicker <-chan time.Time
	if allowSignInEarlyExit {
		t := time.NewTicker(deepSeekSignInPollInterval)
		defer t.Stop()
		signInTicker = t.C
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return &deepSeekAuthTimeoutError{}
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
					log.Printf("deepseek: 导航 epoch 已确定（loaderId 长度 %d）", len(epochLoaderID))
				}
				continue
			}
			if phase == 1 {
				if rr, ok := browserauth.DecodeResponseReceivedEvent(ev); ok {
					if !(rr.Status >= 200 && rr.Status < 300) || !isProtectedAPIURL(rr.URL) || rr.RequestID == "" {
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
					signInTicker = nil
					log.Printf("deepseek: 受保护接口 2xx 已观测（loaderId 匹配），等待 loadingFinished")
					continue
				}
				continue
			}
			if phase == 2 {
				if lf, ok := browserauth.DecodeLoadingFinishedEvent(ev); ok {
					if lf.RequestID != pendingRequestID {
						continue
					}
					if !deepSeekResponseCodeOK(ctx, cdp, lf.RequestID) {
						return fmt.Errorf("受保护接口响应体业务 code 检查失败（缺失或非 0）")
					}
					postURL, err := cdp.PageURL(ctx, deepSeekHost)
					if err != nil {
						return fmt.Errorf("读取鉴权后 URL 失败: %w", err)
					}
					if !deepSeekIsUsagePage(postURL) {
						return fmt.Errorf("受保护接口业务 code=0 但页面未在 usage（path=%s）", pathOnly(postURL))
					}
					log.Printf("deepseek: 鉴权决定已观测（受保护接口 2xx，loaderId 匹配，业务 code=0，URL path=/usage）")
					return nil
				}
				continue
			}
		case <-signInTicker:
			if phase != 1 {
				continue
			}
			pollCtx, cancel := context.WithTimeout(ctx, deepSeekSignInPollTimeout)
			url, err := cdp.PageURL(pollCtx, deepSeekHost)
			cancel()
			if err != nil {
				log.Printf("deepseek: /sign_in 轮询失败（记为无观测）: %v", err)
				continue
			}
			if isDeepSeekLoginPage(url) {
				signInStreak++
				if signInStreak >= 2 {
					log.Printf("deepseek: 连续 %d 次观测到 /sign_in（SPA 拒绝登录态）", signInStreak)
					return &deepSeekAuthRejectedError{}
				}
			} else {
				signInStreak = 0
			}
		case <-time.After(remaining):
			return &deepSeekAuthTimeoutError{}
		case <-ctx.Done():
			return fmt.Errorf("等待鉴权决定被取消: %w", ctx.Err())
		}
	}
}

func deepSeekResponseCodeOK(ctx context.Context, cdp deepSeekCDP, requestID string) bool {
	body, err := cdp.GetResponseBody(ctx, requestID)
	if err != nil || body == "" {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return false
	}
	codeBytes, ok := raw["code"]
	if !ok {
		return false
	}
	var code int
	if err := json.Unmarshal(codeBytes, &code); err != nil {
		return false
	}
	return code == 0
}
