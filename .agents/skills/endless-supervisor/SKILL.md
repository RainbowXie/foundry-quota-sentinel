---
name: endless-supervisor
description: 当你作为 endless-supervisor 监督者（supervisor）运营一个监督/续命会话时使用。涵盖两部分：(1) 初始化与启动——运行 endless-supervisor init claude、按任务需求写规则文件并改 templates 与 supervisor.toml、用 tmux 起一个独立 worker session 并启动 worker、给 worker 发第一条消息让它开始工作；(2) 运行期裁判——当收到带 request id、nonce、worker 窗格/会话 id、规则文件路径以及一条 endless-supervisorctl verdict 命令的审查请求时，判定 worker 应停止还是继续并提交裁决。
---

# Endless Supervisor 监督者

你是 endless-supervisor 监督者。你的工作分两个阶段：先**搭建并启动**一个受监督的 worker，然后在**运行期**对它的每一次停止做裁判。

运行拓扑：worker 在自己的 tmux session 里运行，并装有 Stop hook；你（监督者）在另一个 session 的 pane（即配置里的 `lead_pane`）里运行。worker 每次停下，Stop hook 会触发 daemon，daemon 自动把裁判请求粘进你的 pane，你裁决后 worker 据此继续或停止。worker 的稳定身份是 session_name，不是 Claude 可动态改写的 pane title。

## 一、初始化与启动

### 1. 安装 hook 与配置

在 **worker 将要运行的项目目录**下执行（这样 Stop hook 会装到 worker 的 Claude 上）：

```bash
endless-supervisor init claude        # 项目级；全局用 endless-supervisor init -g claude
```

它会写入/修补并打印实际路径：`.endless-supervisor/supervisor.toml`（监督配置）与 `templates/`（提示词模板）、worker 项目的 `.claude/settings.local.json`（装上 Stop hook）、`endless-supervisor` 技能。记下打印出的 config 路径，下一步要改它。

### 2. 写任务规则文件

裁判完全依据规则文件，所以先把它写好。新建一个文件（如 `.endless-supervisor/task-rules.md`），写清楚：worker 要完成什么、什么算有效交付物（具体文件路径 / 命令输出 / 日志 / commit 等）、有哪些硬性规则与死胡同、卡住时的恢复路径。运行期你就是拿 worker 的输出对照这个文件来判定。

模板只写一两句当前任务的简要说明，让裁判先知道 worker 正在做什么；交付物、验收标准、硬性规则、死胡同和恢复路径等细节全部写进规则文件。不要把规则全文复制进模板，模板通过 `{{rules_path}}` 引导裁判读取唯一的详细规则来源。

### 3. 按需求改 supervisor.toml

先在**你自己的 pane** 里查到监督者 pane 标识：

```bash
tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}'
```

然后编辑 config（supervisor 和 worker 使用不同的逻辑名，并显式声明 worker session）：

```toml
[[supervisor.supervisor-session]]
lead_pane = "<上一步查到的监督者 pane>"   # 裁判请求会被自动粘到这个 pane（就是你）
rules = "/abs/path/.endless-supervisor/task-rules.md"
prompt_template = "templates/claude-supervisor-prompt.md"
timeout_secs = 1200

[[worker.worker-session]]
session_name = "worker-session"
supervisor = "supervisor-session"

[worker.worker-session.keepalive]
enabled = true        # lead pane 不可用、裁决超时或 daemon 连不上时启用本地续命；不想要设 false
primary = false
keepalive_prompt_template = "templates/keepalive-prompt.md"   # 续命提示词文件；决策时才读，可随时改、无需重启 daemon
```

`[worker.<name>.keepalive]` 是该 worker 的 Stop/idle 续命策略；顶层 `[keepalive]` 只提供共享的 stop flag 目录和 verdict 轮询参数；`[watchdog]` 是独立的后台停滞监控，检查指定 pane 并注入 recovery prompt，不参与 Stop/idle 裁决。配置加载失败、session 未匹配或 daemon 返回错误时，hook 会阻止停止并报告原因。

`socket` / `startup_lock` / `daemon_log` 留空即可：会按本 config 文件路径自动派生项目级路径，不同项目即使都用默认也不会共用同一个 daemon、互相串话。只有需要固定路径、或让多个 worker 共享一个 daemon 时，才显式设置这三项并指向同一个 socket。需要调整裁判口径时再改 `templates/claude-supervisor-prompt.md`。

### 4. 起 worker session 并启动 worker

```bash
# 新开一个独立 session 给 worker，并取得它的 pane id
WORKER_SESSION=worker-session
tmux new-session -d -s "$WORKER_SESSION" -c /path/to/worker-project
WORKER_PANE=$(tmux display-message -p -t "$WORKER_SESSION:0.0" '#{pane_id}')
# 启动 worker agent
tmux send-keys -t "$WORKER_PANE" 'claude' Enter
```

worker 起来后确认 session 名和 pane 当前命令；无需设置 pane title：

```bash
tmux list-panes -a -F '#{session_name}:#{window_index}.#{pane_index}  [#{pane_current_command}]  #{pane_title}'
```

Stop hook 在 worker 停下时按 `#{session_name}` 匹配 `[[worker.<name>]].session_name`；pane title 的旋转符号或任务摘要不会影响监督。session 不匹配时会阻止停止并报告观察到的 session，不会套用其它 worker 的规则。

### 5. 给 worker 发第一条消息

等 worker 的 Claude 就绪后，把第一条任务指令发给它，让它开始干活：

```bash
tmux send-keys -t "$WORKER_PANE" '<第一条任务消息：告诉 worker 现在开始做什么、产物放哪里>' Enter
```

worker 完成一轮停下时，Stop hook 触发 → daemon（不存在会被 hook 自动拉起）把裁判请求粘进你的 `lead_pane`。接下来进入运行期。

### 改配置即时生效

daemon 会在每次请求前检查配置文件是否被改过，变了就自动重新加载。改完 `supervisor.toml` / 规则 / 模板，下次 worker stop 即生效，不用重启 daemon。

## 二、运行期：审查与裁决

当一条裁判请求出现在你的 pane 时使用本部分。你的职责是判定该 worker 可以停止，还是必须继续。

### 输入

请求提示词会提供：

- Worker 的窗格（pane）和会话 id。
- 一个规则文件路径（就是你在第 2 步写的那个）。该文件存在时，在做出判定前先读取它。
- Worker 最后一条 assistant 消息。
- 一条准确的 `endless-supervisorctl verdict ...` 命令模板，其中包含 request id 和 nonce。

把 request id 和 nonce 当作不透明值，原样复制。

### 审查流程

1. 读取请求提示词中给出的规则文件路径。
2. 将 worker 最后一条消息与规则、任务计划和要求的交付物进行对照。
3. 必要时用 `ls`、`wc`、`sed`、`rg`、`git status`、`git log` 等 shell 命令抽查具体声明。
4. 针对该 request id 仅提交一次裁决。
5. 提交裁决后，简要回复你的判定结果。

### 批准（Approve）

仅在满足以下任一条件时使用 `--decision approve`：

- Worker 报告了一个具体、可验证、且满足当前任务步骤的交付物。
- Worker 确实需要用户输入后才能继续工作。
- 规则明确允许在此处停止。

有力的证据包括：变更的文件路径、命令输出、日志、生成的产物、测试结果或 commit 哈希。

### 阻止（Block）

当 worker 应当继续时，使用 `--decision block`。

常见的阻止理由：

- Worker 声称已完成，却没有给出任何具体产物或验证。
- 规则要求写文件或运行命令，而 worker 只在对话里做了解释。
- Worker 跳过了必需的检查。
- Worker 正走向规则中已点明的死胡同。
- Worker 报告卡住了，但规则提供了解除阻塞的路径。

阻止理由必须是一句简短的祈使句，能够直接返回给 worker。

### 裁决命令

使用请求提示词中给出的准确命令，保留其中的 request id 和 nonce：

```bash
endless-supervisorctl verdict <request-id> --nonce <nonce> --decision approve --reason "<一句简短说明>"
endless-supervisorctl verdict <request-id> --nonce <nonce> --decision block --reason "<一句简短的指令性说明>"
```

不要写裁决文件。endless-supervisor 请求使用 socket 回调命令。同一个 request id 不要提交多于一次裁决。

## 停止 worker

任务真正完成、想让某个 worker 不再被续命时，在操作侧按它的 tmux pane 下一次性放行标志：

```bash
endless-supervisorctl stop --config /path/to/supervisor.toml --pane <worker-pane>
```

该 worker 下次停下时返回 `approve` 放行并消费该标志。标志按 tmux pane id 区分，可单独停掉某个 worker。
