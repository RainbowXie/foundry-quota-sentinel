# foundry-quota-sentinel Kimi membership quota provider rules

## 当前唯一目标与来源

- 只在 `/mnt/data/Work/Projects/foundry-quota-sentinel` 主目录工作；不要创建 worktree。
- 完整实施 OpenSpec change `add-kimi-code-provider`，不得只解释或停在计划。
- 每轮开始完整读取并持续对照：`AGENTS.md`、该 change 的 `proposal.md`、`design.md`、`specs/kimi-code-provider/spec.md`、`tasks.md` 和 `evidence.md`。
- 最新用户需求覆盖此前双 meter 设计：主数据/账户页是 `https://www.kimi.com/membership/subscription?tab=quota`，不是 `/code/console`。
- 必须显示三个 quota group：`总使用量` 内同时展示 `Kimi` 与 `Code` 两个独立百分比，另展示 `5 小时用量 · Code` 和 `7 天用量 · Code`，保留小数与各组绝对重置时间。
- 用户给出的验收样例是：总使用量 `2.19%` / `2026-08-27`，5 小时 `0%` / `07-29 19:58`，7 天 `10.42%` / `08-04 23:58`。

## GitNexus 与仓库硬规则

- 修改任何已有 function/class/method/symbol 前必须运行 GitNexus upstream impact，报告 direct callers、affected processes 和 risk；HIGH/CRITICAL 必须先明确警告及隔离/回归策略。
- 探索陌生执行流先 query，再 context；重命名必须用 GitNexus rename，禁止文本全局替换。
- 每个 commit 前必须运行 GitNexus `detect_changes`，确认 affected symbols/flows 仅属于本 change；发现无关范围先收敛。
- 文件编辑使用编辑工具，不用 `cat >`、`echo >` 或 heredoc 覆盖文件；保留用户无关改动。
- `.agents/`、`.claude/`、`.codex/`、`.endless-supervisor/`、`.pi/`、`AGENTS.md`、`CLAUDE.md` 是本地配置，不提交、不删除。
- 未经用户另行授权，不 push、不 tag、不 release、不修改发布版本。

## 证据先行与凭证安全

- 禁止猜测 quota/refresh endpoint、refresh token/storage key、rotation 语义、字段映射、reset timezone 或 SPA replay 方法。
- 使用隔离临时 Chrome/Edge profile、loopback 非零 CDP 端口收集真实证据；不得读取用户默认 profile。
- `evidence.md` 只允许 host/path、字段名/类型、事件顺序、状态码、长度、数量和脱敏结论；不得记录、输出或提交 access/refresh token、Cookie、Authorization、storage 值、账户标识、真实响应正文或其哈希。
- `get_network_request` 等工具不得把真实 header/body直接输出进对话；使用页面内/临时诊断逻辑提取字段结构和长度，原始材料立即 shred。
- 已知 access token 约 15 分钟有效；生产实现必须保存最小 durable refresh state，自动刷新额度查询与账户页会话，不能要求用户每 15 分钟重登录。
- refresh 轮换必须按账户串行并原子持久化；失败保留旧记录、显示需重登录，不得把部分状态覆盖到配置。
- 所有 outward boundary（日志、错误、CLI、JSON、HTML、URL、子进程参数、测试失败文本）用 synthetic secret 扫描证明无泄漏。

## 新数据模型与解析契约

- 删除/迁移 provisional `Weekly`/`RateLimit` 双 meter，改为 Kimi grouped model：`Total{Kimi, Code, reset}` + `FiveHour Code` + `SevenDay Code`。
- 不得复用整数 `QuotaUsage.UsagePercent` 丢失精度；Kimi leaf 必须保存 decimal percentage、absolute `ResetAt`、derived seconds、absolute `ResetDisplay` 和 status。
- 百分比保留最多两位小数并去掉无意义尾零：必须显示 `2.19%`、`10.42%`、`0%`，不得显示成 `2%`、`10%` 或 `0.00%`。
- evidence-backed mapping：必须真实绑定 `subscriptionBalance.amountUsedRatio` 与 `kimiCodeUsedRatio` 分别对应总使用量的 `Kimi`/`Code` 哪个标签，并确认二者 reset 关系；当前两值恰好同为 0.0219，禁止按数值相等或字段名猜标签；5h/7d 分别来自经确认的 rate-limit 对象。
- ratio 仅允许有限数值 `0..1`；超界、NaN、Infinity 必须报错，禁止 clamp。只有 evidence-confirmed 的零用量缺省 ratio shape 可解释为 `0%`。
- 三个 reset 都是独立 absolute timestamp；总量以本地 `YYYY-MM-DD` 展示，5h/7d 以本地 `MM-DD HH:mm` 展示，同时保留绝对 instant 和 derived seconds。
- 缺任一 meter/reset、过期或无法解析时间、业务错误、未完成/过大响应都必须 fail closed，不能补零或借用其他值。
- 不修改 OpenCode/DeepSeek/Ollama 的现有 `QuotaUsage`/`QuotaData` JSON 语义。

## TDD、刷新与浏览器生命周期

- 严格 RED→GREEN：每项新行为先写当前实现必失败的最小测试并实际运行确认 RED，再实现并运行 GREEN；报告必须给出命令和失败/成功要点。
- Go querier 在 token 过期时使用真实 refresh contract 刷新、最多安全重试一次并保存 rotated state；区分 expired-session、refresh、timeout、transport、business 和 malformed errors。
- 多账户的请求、refresh mutex/state、browser profile、卡片和错误完全隔离；一账户失败不得阻塞或污染其他账户。
- login 必须以当前关联的 membership protected response 完成、HTTP/业务成功、三 metric 解析有效和 durable state 可刷新共同证明；URL、document load、token-shaped value、固定 sleep 或单独 2xx 不算成功。
- 账户页必须从 `about:blank` 在 document start 前恢复 evidence-proven cookie/storage state，必要时先 refresh，再精确导航 `https://www.kimi.com/membership/subscription?tab=quota`。
- readiness 必须来自精确 membership URL + 当前 epoch/request 的 completed protected response + 三 metric 有效；不得用固定 sleep、轮询次数或“第 N 次导航”。
- 成功后 signal ready 并 `browser.Wait()` 到用户手动关闭；启动后失败先 signal redacted error 再 Wait，禁止 flash-close 或吞错。
- membership 页面内的 `购买加油包` 由用户控制；程序只打开页面，禁止自动点击、提交购买/订阅/支付，也不需要单独的自动 purchase route。
- `internal/browserauth` 只允许增加通用 CDP/process primitive，Kimi host/path、refresh/storage 和字段语义留在 provider 层。

## 必须交付的用户表面

- 配置：多账户、版本化 durable envelope、backward compatibility、generation、upsert/delete、refresh rotation 与迁移/重登录策略。
- 额度：三项 decimal model、真实 membership parser、absolute reset、refreshable transport、边界与 secret tests。
- 浏览器：真实 login、durable capture、membership SPA replay、严格 host/path、ready/error/Wait、nogui 和跨平台测试。
- CLI：`login-kimi <name>`、`quota-kimi [name]`、`open-page kimi <name>`，总量 Kimi/Code 双值 + 5h/7d Code、小数百分比、绝对 reset。
- Web/sidebar：总使用量分组内 Kimi/Code 双值及 5h/7d Code rows、loading/refreshing/success/expired/error、refresh/re-login/open/delete，账户页按钮打开 membership quota 页面。
- 文档：README.md、README_EN.md、help/examples/support table 与 sanitized `evidence.md` 全部更新，不残留 console 双 meter 语义。
- OpenSpec：实时勾选更新后的 `tasks.md`；未通过真实验收的任务不得标完成。

## Fresh 验证与批准标准

- fresh 运行 `gofmt`/`gofmt -l`、`go test ./... -count=1`、`go test -race -tags nogui ./... -count=1`、default/nogui `go vet`、OpenSpec strict validation、GUI WebKit 4.1 与 nogui canonical builds。
- canonical real acceptance 必须：隔离登录并保存账号；证明 token 过期后自动刷新；CLI/sidebar 与 membership 页面逐项对照总量 Kimi、总量 Code、5h Code、7d Code 四个百分比和三个 group reset；打开 membership 页面保持到手动关闭；确认未自动触碰购买控件；只保留脱敏证据。
- 所有更新任务完成、代码/架构审查无已知 bug、worktree 除本地 agent/supervisor 文件外干净、commits/测试/构建/evidence 具体可验证后，才可批准停止。
- 若必须等待用户在隔离浏览器登录，先完成全部非交互工作并直接启动可操作浏览器，再报告唯一具体用户动作；不得停在环境安装或泛泛“需要实测”。

## 死胡同与恢复

- refresh 未知：继续捕获 `auth.kimi.com` 的真实 refresh 请求、storage key 名称/长度与 rotation 时序，不得只保存短期 access token。
- SPA replay 失败：比较成功登录与 throwaway replay 的 cookie/storage key 元数据和 protected request presence，不得注入任意 header 或猜 localStorage key。
- 直接 Go refresh 被 browser-bound state 拒绝：基于证据使用隔离 CDP page context，不得关闭安全校验或读取默认 profile。
- GUI 环境受限：先完成通用/nogui 抽象和测试，再用项目 WebKit shim；不得回退 WebView auth。
- 发现现有 Kimi commit 与新需求冲突：写 RED 后迁移/替换，不保留兼容旧双 meter 的死代码，也不得重置覆盖其他 provider 改动。
