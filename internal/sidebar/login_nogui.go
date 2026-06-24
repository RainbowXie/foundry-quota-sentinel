//go:build nogui || (!windows && !darwin && !linux)

package sidebar

import "fmt"

// RunDeepSeekLogin 在无 GUI 构建下不可用。
func RunDeepSeekLogin(validate func(string) bool) (string, error) {
	_ = validate
	return "", fmt.Errorf("此版本未编译图形界面，无法弹窗登录；请用带 GUI 的版本运行 `ocgt-monitor login-deepseek`")
}
