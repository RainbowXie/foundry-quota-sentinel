//go:build !(linux && !nogui)

package sidebar

import "fmt"

// RunOpenCodeLogin 仅在 Linux(WebKitGTK) 上实现（需读 cookie store 的 httpOnly cookie）。
// 其它平台与无 GUI 构建回退到手动配置。
func RunOpenCodeLogin(validate func(cookie, wsid string) bool) (string, string, error) {
	_ = validate
	return "", "", fmt.Errorf("OpenCode Go 弹窗登录目前仅支持 Linux(WebKitGTK)；其它平台请用 `foundry-quota-sentinel config add <名称>` 手动配置")
}
