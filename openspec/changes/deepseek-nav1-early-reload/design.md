## Context

`runDeepSeekPage` 的 nav1 阶段调用 `deepSeekWaitForAuthDecision`（5s 哨兵超时）。
SPA 用默认短 token 覆盖回放的 userToken 后不会发起受保护接口，于是"等满 5s
无信号"成为预期结局（typed sentinel `deepSeekAuthTimeoutError`），随后才做
post-load 重放 + reload。拒绝发生时 SPA 会**立即**重定向到 `/sign_in`
（diag2 实证），即真正的拒绝信号在几百毫秒内就可观测，5s 纯属等待浪费。
tmpfs 修复（1fef513）后准备阶段已亚秒，这 5s 是打开耗时中最大的固定项。

约束：sentinel 类型契约不得退化（禁止 strings.Contains 分类）；nav2 语义
（超时即 fatal）不得改变；`failAndWait` 失败语义不变。

## Goals / Non-Goals

**Goals:**

- nav1 观测到 SPA 拒绝（`/sign_in` 跳转）时提前进入 reload，节省约 4~5s
- 拒绝证据类型化，与超时哨兵并列、可区分日志
- 无拒绝信号时行为与现状完全一致（5s 哨兵兜底）

**Non-Goals:**

- 不改变 nav2 的判定规则与错误语义
- 不改动鉴权通过判据（受保护接口 2xx + loaderId 匹配 + 业务 code=0 + /usage）
- 不引入新的页面判据函数（复用既有 `isDeepSeekLoginPage`）
- 不处理其他 provider 的等待路径

## Decisions

### D1：用 PageURL 轮询做 `/sign_in` 检测，而非 Page 域事件

等待循环内增加 ~300ms 轮询 tick，调 `cdp.PageURL` 读 `location.href`。

- 备选：`Page.frameNavigated` / `Page.navigatedWithinDocument` 事件。SPA 的
  客户端跳转（pushState / location / hash）产生的事件形态依实现而异，且需要
  新增事件解码器；事件形态对 SPA 内部路由方式脆弱。
- 轮询直接读地址栏终态，机制无关，与代码库既有 `PageURL` 用法一致；
  每 300ms 一次 `Runtime.evaluate` 的开销可忽略。

### D2：连续 2 次轮询观测到 `/sign_in` 才判定拒绝

防 SPA 启动瞬时态（路由初始化经过 `/sign_in` 再离开）造成误判。连续 2 次
（约 600ms 窗口）即视为稳定决定。只观测到 1 次则清零计数继续等。

- 备选：单次即判定——最快，但瞬时态风险不可控；拒绝后 reload 是幂等的
  标准恢复，误判代价低，但会平白多一次整页加载。

### D3：拒绝信号用新的 typed error 表达

新增 `deepSeekAuthRejectedError`（与 `deepSeekAuthTimeoutError` 同型），
配套 `isDeepSeekExpectedNavRejection`（errors.As）。`runDeepSeekPage` 对
两种 typed 结局都走 post-load 重放 + reload，日志区分"观测到 /sign_in 跳转"
与"5s 哨兵超时"。错误分类继续完全基于类型，不引入字符串匹配。

### D4：早检仅对 nav1 启用

`deepSeekWaitForAuthDecision` 增加显式开关（如 `allowSignInEarlyExit bool`），
nav1 传 true，nav2 传 false。nav2 出现 `/sign_in` 不产生早退、不改变其
超时即 fatal 的语义——nav2 的 document-start 脚本应先于 SPA 鉴权检查生效，
`/sign_in` 在 nav2 属异常形态，维持原 fatal 路径便于暴露。

### D5：轮询错误不升级

轮询 `PageURL` 的瞬时失败（CDP 抖动）记为"无观测"、继续等待，不进入 fatal
分类；既有 fatal 路径（事件通道关闭、ctx 取消、phase 2 的 URL/响应体读取
失败）原样保留。

## Risks / Trade-offs

- [DeepSeek SPA 改版后不再跳 `/sign_in`] → 早检永不触发，行为退化为现状
  （等满 5s 哨兵兜底），无功能回归，仅失去加速效果
- [瞬时 `/sign_in` 被误判为拒绝] → D2 双次确认把窗口压到 ~600ms；即使误判，
  reload 也是幂等标准恢复，代价为一次多余整页加载
- [轮询与事件处理相互挤占] → 轮询 tick 合入现有 select 循环，事件分支
  优先语义不变；phase 2 的原有 PageURL 调用保持不变

## Migration Plan

纯行为优化，无数据/配置迁移。回滚即还原本 change 的代码改动，恢复固定 5s
等待，无副作用。

## Open Questions

- 轮询间隔取 300ms 是否需随实测定档（250ms/500ms）？——实现任务中以
  常量定义，实测后可单点调整
