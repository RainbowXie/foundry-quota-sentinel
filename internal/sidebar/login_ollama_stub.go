//go:build !linux && !nogui

package sidebar

import "fmt"

// RunOllamaLogin 仅在 Linux WebKitGTK 图形构建中可读取 httpOnly cookie。
func RunOllamaLogin(validate func(string) bool) (string, error) {
	_ = validate
	return "", fmt.Errorf("Ollama 弹窗登录目前仅支持 Linux(WebKitGTK)；请在 Linux GUI 版本中登录")
}

// RunOllamaPage 仅在 Linux WebKitGTK 图形构建中可注入 cookie。
func RunOllamaPage(pageURL, cookie string) error {
	_, _ = pageURL, cookie
	return fmt.Errorf("Ollama 内置账户页目前仅支持 Linux(WebKitGTK)")
}
