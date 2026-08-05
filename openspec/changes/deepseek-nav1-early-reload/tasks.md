## 1. 类型化拒绝信号

- [x] 1.1 `internal/sidebar/login_deepseek.go` 新增 `deepSeekAuthRejectedError` typed error 与 `isDeepSeekExpectedNavRejection`（errors.As 分类），风格对齐既有 `deepSeekAuthTimeoutError`/`isDeepSeekExpectedNavTimeout`

## 2. 等待函数与调用侧改造

- [x] 2.1 `deepSeekWaitForAuthDecision` 增加 `allowSignInEarlyExit bool` 参数；等待循环合入 ~300ms 轮询 tick（常量定义间隔），开关开启时连续 2 次 `PageURL` 观测到 `/sign_in`（复用 `isDeepSeekLoginPage`）即返回 `deepSeekAuthRejectedError`；单次观测后离开则计数清零；轮询错误记为无观测不升级
- [x] 2.2 `runDeepSeekPage`：nav1 传 true、nav2 传 false；nav1 结局分类改为 `isDeepSeekExpectedNavRejection || isDeepSeekExpectedNavTimeout` 均进入 post-load 重放 + reload，日志区分"观测到 /sign_in 跳转，提前 reload"与"哨兵超时"；其余错误仍 fatal

## 3. 测试

- [x] 3.1 单测覆盖 spec 场景：连续 2 次 /sign_in 提前拒绝、单次瞬时不误判、无信号 5s 哨兵兜底、轮询错误容错、nav2 不早退（fake CDP 驱动 PageURL 序列与事件）
- [x] 3.2 `go build -tags nogui ./... && go test -tags nogui ./internal/... .` 全绿

## 4. 验证与收尾

- [x] 4.1 `detect_changes()` 核对改动仅限预期符号（等待函数、调用侧、新类型）
- [x] 4.2 实机回归（worktree + 真实 Edge + 真实 DeepSeek 账户，fake HOME 隔离）：有效 token nav1 ~1.2s 直接通过（无盲等）；篡改 token 走业务 code!=0 既有 fatal（与 v0.10.2 基线一致）；**当前线上 SPA 对非法 token 仍发起受保护请求，/sign_in 早检路径实机不可复现**，由确定性单测 + fake CDP 端到端覆盖（TestRunDeepSeekPageEarlyReloadOnSignInRejection），作为 SPA 行为回摆时的安全网保留。回归顺带修复轮询 stall bug（deepSeekSignInPollTimeout 800ms 时间盒 + TestDeepSeekWaitForAuthDecisionBlockingPollDoesNotStallDeadline）

  > 实机回归结果（本 change 已执行，见会话记录）：
  > - 有效 token：nav1 直接鉴权成功，~1.2s 打开 /usage，无 reload、无 5s 盲等（run1）✓
  > - 篡改 token（长/短）：SPA 仍发起受保护接口且返回业务 code!=0 → 走既有 fatal 路径（~1s，与 v0.10.2 基线一致，spec 要求保留）✓ 无回归
  > - 空 token：账户校验层拒绝（既有行为）✓
  > - 当前线上 SPA 对非法 token 总是完成受保护请求，2×300ms /sign_in 早检窗口内即被业务 code!=0 fatal 抢先；
  >   早检路径（受保护信号缺失 + /sign_in 跳转）由确定性单测 + fake CDP 端到端流验证，未能在实机复现触发
  > - 回归中发现并修复：轮询 PageURL 同步阻塞可拖死哨兵 deadline（加 deepSeekSignInPollTimeout 时间盒，新增对应单测）

  > 审查修复（commit ce53d02 之后的 follow-up）：
  > - WARNING（phase 1→2 分类竞态）：受保护 2xx 观测后 signInTicker = nil，判定权归 phase 状态机；
  >   新增 TestDeepSeekWaitForAuthDecisionPhase2DisarmsEarlyExit 回归测试
  > - SUGGESTION 1（off-host 轮询噪音）：poll 决策窗口限定 phase 1（phase 0 导航前 about:blank 静默跳过），
  >   噪音从源头消除，零字符串匹配、零 browserauth 新表面
  > - SUGGESTION 2（phase 2 读取未加时间盒，既有问题）：审查建议后续硬化时一并加盒——记录为本 change 之外
  >   的 follow-up 硬化项（deepSeekResponseCodeOK 与 phase 2 PageURL 加短 ctx 超时）
