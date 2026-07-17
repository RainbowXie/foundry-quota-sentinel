//go:build nogui || (!windows && !darwin && !linux)

package sidebar

import "fmt"

// RunDeepSeekLogin 在无 GUI 构建下不可用。
func RunDeepSeekLogin(validate func(string) bool) (string, string, error) {
	_ = validate
	return "", "", fmt.Errorf("此版本未编译图形界面，无法弹窗登录；请用带 GUI 的版本运行 `foundry-quota-sentinel login-deepseek`")
}

// RunDeepSeekPage 在无 GUI 构建下不可用（调用方会回退到系统浏览器）。
func RunDeepSeekPage(pageURL, webStore string) error {
	_, _ = pageURL, webStore
	return fmt.Errorf("此版本未编译图形界面，无法打开内置窗口")
}
