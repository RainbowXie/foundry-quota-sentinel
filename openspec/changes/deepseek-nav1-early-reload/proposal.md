## Why

DeepSeek 账户页的成功路径固定多花 5 秒：首次导航（nav1）后流程盲等一个 5s
哨兵超时——SPA 覆盖 userToken 时不会触发受保护接口，"等满 5s 没信号"被当作
"SPA 已拒绝登录态"的预期证据。但 SPA 拒绝时会立即重定向到 `/sign_in`，这是
一个可即时观测的事件，无需等满 5s。tmpfs 修复（1fef513）让准备阶段降到亚秒
后，这 5s 盲等成为账户页打开耗时中最大的固定项。

## What Changes

- nav1 鉴权等待期间新增"SPA 拒绝登录态"早期检测：观测到页面跳转到
  `/sign_in` 即认定 nav1 未通过鉴权，立即进入 post-load 重放 + reload，
  不再等满 5s 哨兵超时
- 早检结果是新增的**第二类预期 nav1 结局**，与现有 `deepSeekAuthTimeoutError`
  哨兵并列，同样使用 typed error/类型化信号表达，不得退化为字符串匹配；
  其余错误（CDP 通道关闭、ctx 取消、PageURL 失败、业务 code!=0）仍为 fatal
- 5s 哨兵超时保留为兜底（SPA 既未鉴权通过也未跳转 `/sign_in` 的异常形态）
- nav1 直接鉴权成功（少见但存在）的路径与判据完全不变

## Capabilities

### New Capabilities

- `deepseek-account-page-auth`: DeepSeek 账户页登录态恢复中的鉴权决定观测——
  nav1 鉴权通过信号（受保护接口 2xx + loaderId 匹配 + 业务 code=0）、SPA 拒绝
  信号（`/sign_in` 跳转早检与 5s 哨兵兜底）、以及据此触发 post-load 重放 +
  reload 的决策规则

### Modified Capabilities

（无）

## Impact

- `internal/sidebar/login_deepseek.go`：`runDeepSeekPage` 的 nav1 等待路径、
  `deepSeekWaitForAuthDecision` 或其调用侧新增 `/sign_in` 早检
- 无配置、API、存储格式变更；`failAndWait` 失败语义不变
- 预期效果：DeepSeek 账户页成功路径打开耗时减少约 5 秒
