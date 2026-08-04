## ADDED Requirements

### Requirement: nav1 鉴权通过判据保持不变

nav1 等待 SHALL 仅以既有判据认定鉴权通过：受保护接口响应 2xx、loaderId
与本次导航 epoch 匹配、响应体顶层业务 code 存在且等于 0、且页面 URL 在
`/usage`。该判据不得因早检机制引入而改变。

#### Scenario: nav1 直接鉴权通过

- **WHEN** nav1 期间受保护接口 2xx（loaderId 匹配、业务 code=0）且页面在 `/usage`
- **THEN** 流程判定 nav1 鉴权通过，不执行 post-load 重放与 reload

### Requirement: nav1 SPA 拒绝登录态的早期检测

nav1 等待 SHALL 在等待周期内轮询页面 URL，并将**连续 2 次**观测到
`/sign_in`（既有 `isDeepSeekLoginPage` 判据）认定为 SPA 拒绝登录态，
立即返回类型化的拒绝信号，而非等满 5s 哨兵超时。

#### Scenario: 稳定 /sign_in 跳转触发提前 reload

- **WHEN** nav1 等待期间连续 2 次轮询观测到页面 URL 为 `/sign_in`
- **THEN** 等待以类型化拒绝信号提前返回（早于 5s 哨兵超时）
- **AND** 流程进入 post-load 重放 + reload，日志标明"观测到 /sign_in 跳转"

#### Scenario: 瞬时 /sign_in 不误判

- **WHEN** 轮询仅 1 次观测到 `/sign_in`、随后页面离开该 URL
- **THEN** 拒绝计数清零，等待继续，不触发提前返回

#### Scenario: 无信号时哨兵超时兜底

- **WHEN** nav1 期间既未出现鉴权通过信号、也未连续观测到 `/sign_in`，直至 5s
- **THEN** 等待以 `deepSeekAuthTimeoutError` 哨兵返回（与现状一致）
- **AND** 流程进入 post-load 重放 + reload，日志标明"哨兵超时"

#### Scenario: 轮询失败不升级

- **WHEN** 某次 URL 轮询因瞬时 CDP 错误失败
- **THEN** 该次记为无观测，等待继续，不进入 fatal 错误路径

### Requirement: 拒绝与超时均为预期 nav1 结局且类型可区分

拒绝信号与超时哨兵 SHALL 以不同类型（typed error）表达，调用方通过
`errors.As` 分类；两种结局都 MUST 触发相同的 post-load 重放 + reload
恢复路径。禁止以错误字符串匹配做结局分类。

#### Scenario: 两种预期结局殊途同归

- **WHEN** nav1 等待返回类型化拒绝信号或超时哨兵
- **THEN** 流程执行 post-load 重放登录态脚本并发起 reload（nav2）

### Requirement: 早检仅适用于 nav1

nav2（reload 后的鉴权等待）SHALL 不启用 `/sign_in` 早检；nav2 期间出现
`/sign_in` 不改变其既有错误语义（等待超时或既有 fatal 判据）。

#### Scenario: nav2 不做早退

- **WHEN** nav2 等待期间页面 URL 为 `/sign_in` 且无鉴权通过信号直至超时
- **THEN** 按既有 nav2 语义返回 fatal 错误（failAndWait），不触发再次 reload
