package auth

import "sync"

// OpenPageReady 在账户页面完成已认证导航并就绪时被调用（通知展示层或父进程解除加载等待）。
var OpenPageReady func()

// OpenPageError 在打开页面遇到无法恢复的认证/网络错误时被调用（至多触发一次）。
var OpenPageError func(msg string)

var openPageErrorOnce sync.Once

// ResetOpenPageHooks 重置错误单次触发守卫，供新的页面会话使用。
func ResetOpenPageHooks() {
	openPageErrorOnce = sync.Once{}
}

// SignalOpenPageReady 安全调用 OpenPageReady 回调。
func SignalOpenPageReady() {
	if OpenPageReady != nil {
		OpenPageReady()
	}
}

// SignalOpenPageErrorOnce 至多触发一次 OpenPageError 回调。
func SignalOpenPageErrorOnce(msg string) {
	openPageErrorOnce.Do(func() {
		if OpenPageError != nil {
			OpenPageError(msg)
		}
	})
}
