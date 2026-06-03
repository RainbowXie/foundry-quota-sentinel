## 2026-06-03 12:45: 侧边栏改为紧凑悬浮小部件 250×370
- **形态变化:** 全高面板 → 固定在右上角的紧凑小部件
  - 面板尺寸: 全屏高度 → 250px × 370px 固定尺寸
  - 位置: 屏幕右上角，距顶部 40px
  - 触发区: 仍为右侧边缘 15px，但只在上 500px 范围内
  - 面板检测: Y 轴限制在 panelY±20～panelY+panelHeight+20
- **尺寸缩减:** 
  - 宽度 270px → 250px，Header 内边距 10px→6px
  - 图标 18px→16px，标题 11px→10px，卡圆角 16px→10px
  - 所有间距缩减 20-30%，模型行仅 3px 内边距
  - 动画帧 15→12，步长 6ms→8ms（更流畅）
- **修复:** `rgba(var(--accent),0.1)` → `rgba(var(--accent-rgb),0.1)`
  - hex 颜色不能直接用于 `rgba()`，新增 `--accent-rgb` 变量存 RGB 值
  - 修复 `.hb:hover` `.dr-in:focus` `.dr .go:hover` 三处硬编码
- **清理:** 移除未使用的 `--accent3` `--st-bar` `--sep-col` 变量
- **双主题切换:**
  - 亮色「灵动卡片」— 毛玻璃 + 大圆角 + 柔和渐变背景
  - 暗色「深色专业」— 深黑底 + 霓虹绿点缀 + 紧凑数据行
  - 通过 `[data-theme]` CSS 变量驱动，所有颜色一次切换
  - 主题按钮 🌙/☀️ 点击切换，`localStorage` 持久化
- **侧边栏收窄:** 面板宽度 300px→270px
  - 头部更紧凑：图标 22px、字体 12px、按钮 20px
  - 布局重构：配额合并为一张卡片、所有间距缩减 20-30%
- **CSS 重构:**
  - 全部颜色抽取为 CSS 变量（~40 个），覆盖背景/文字/卡片/边框/阴影
  - 主题间平滑过渡 `transition: background .3s,color .3s`
  - 清除 `var--cr` 语法错误，模型颜色变量正确引用
- **代码清理:**
  - `internal/state/state.go` 新增，管理固定状态
  - `storage/reader.go` 新增 `OCGTLogDir()`，移除 `main.go` 和 `web/server.go` 的重复函数
  - `quota/opencode.go` 移除本地 `fmtDurationCompact`，改用 `formatter.FormatDurationCompact()`
  - `quota/types.go` 移除未使用的 `QuotaResult` 和 `TokenStats`
  - `web/server.go` 已清理，新增 `/api/pin` `/api/pin-state` 端点
  - `go.mod` 更新到 1.26.0
- **新增功能:**
  - `version` 命令（`ocgt-monitor version` / `--version`）
  - `OCGT_PORT` 环境变量支持自定义端口
  - 固定按钮（📌/📍），点击后侧边栏保持显示不自动收回
- **侧边栏界面:**
  - 暗色模式：全部 CSS 颜色提取为变量，`prefers-color-scheme: dark` 自动切换
  - 刷新闪动动画（`@keyframes freshen`）：每次数据更新时区域闪烁提示
  - 按钮统一改为 `transparent` 背景，hover 时才显示背景色
  - 固定按钮 `#pinBtn` + 加载时从 `/api/pin-state` 同步状态
- **侧边栏交互:**
  - 触发区 10px→15px，更容易唤出
  - 滑出动画 8 帧→15 帧，更平滑
  - 固定状态下完全禁用自动隐藏逻辑
- **文件:** `internal/config/config.go`
- **原因:** 用户需要能完全清空配置，以便将工具转发给他人使用
- **决策:** 移除"不能删除唯一 Profile"的限制，删除最后一个后配置清空

## 2026-06-02 14:17: 新增多用户配置文件系统
- **文件:** `internal/config/config.go`（重写），`main.go`
- **原因:** 支持多用户多账户管理，替代环境变量方式
- **决策:** 配置文件 ~/.ocgt-monitor/config.json，多 Profile，环境变量优先，交互式配置向导

## 2026-06-02 13:52: 项目初始化
- **文件:** 全部项目文件
- **原因:** 创建独立的 OpenCode Go 额度监控工具

## 2026-06-02 15:23: 修复日志路径和文档误导
- **文件:** `main.go`, `internal/web/server.go`, `internal/web/static/help.html`
- **原因:** ocgt 默认日志目录是 ~/.ocgt/logs/ 而非 ~/.ocgt/history/
- **修复:** ocgtLogDir 自动检测 logs/history/log 三个目录，帮助文档删除了误导性描述

## 2026-06-02 16:10: 桌面面板化改造
- **文件:** `internal/web/static/index.html`, `internal/web/server.go`, `main.go`
- **原因:** 让 Web 面板更像桌面应用，降低终端使用门槛
- **变更:**
  - index.html 改为全屏面板布局，顶部状态栏+今日统计卡片+额度+模型表+趋势图
  - server.go 新增 /api/models 端点返回各模型消耗
  - cmdServe 自动打开浏览器

## 2026-06-02 17:20: 桌面侧边栏面板（WebView2 + 自动隐藏）
- **新增:** `internal/sidebar/sidebar.go` — WebView2 窗口 + 鼠标边缘检测 + 滑出动画
- **新增:** `internal/web/static/sidebar.html` — 侧边栏专属 UI（实时数据）
- **新增:** `serve --sidebar` 启动参数
- **依赖:** github.com/webview/webview_go, golang.org/x/sys/windows
- **构建:** CGO_ENABLED=1, GCC via MSYS2 MinGW64

## 2026-06-02 17:30: 更新使用说明文档
- **文件:** `internal/web/static/help.html`
- **原因:** 新增侧边栏模式、桌面面板化的功能说明
- **风格:** 欧美美学，全英文界面，清晰的信息层级

## 2026-06-02 18:15: 侧边栏优化：窗口置顶+灵敏度+中英双语+模型区分
- **文件:** `internal/sidebar/sidebar.go`, `internal/web/static/sidebar.html`
- **修复:**
  - 窗口被遮挡：所有 SetWindowPos 强制 HWND_TOPMOST，每 2s 重新置顶
  - 触发/回退灵敏度：触发区 10px，轮询 40ms，回退延迟 600ms
  - 中文为主英文为辅：全部标注改为中文+英文标注
  - DeepSeek 未配置时隐藏余额区域
  - 模型区分：按提供商着色（deepseek蓝/mimo绿/glm黄等），名称智能缩写

## 2026-06-02 18:25: 修复模型名称为空 + 字体放大
- **根因:** `CalculateModelStats` 缺少 `s.Model = l.Model`，struct 中 Model 字段始终为空
- **修复:** storage/reader.go 补上行赋值
- **改进:** sidebar.html 全部字体放大 2-3px，大数字更醒目

## 2026-06-02 18:35: Apple 美学 UI 重设计 + 移除网页端
- **重写:** sidebar.html — 毛玻璃效果、大圆角、柔和阴影、进度条内嵌百分比
- **删除:** index.html — 网页端因语言问题导致内容错误，移除
- **修复:** help.html — 移除网页端引用

## 2026-06-02 18:45: 多巴胺风格 UI 重设计
- **风格:** 渐变背景、霓虹渐变色进度条、圆润卡片、大字号数值
- **进度条:** 4 色渐变 (indigo→pink→rose), 显示剩余百分比
- **数值:** 22px 800w 渐变文字，大而醒目
- **模型区:** 每行带背景高光，排名第一紫色渐变、第二橙色渐变
- **删除:** 英文标注，纯中文展示

## 2026-06-02 18:55: 刷新5s+进度条重构+隐藏终端+关闭按钮
- **进度条:** 百分比移至进度条上方显示，不再被遮挡
- **刷新:** 60s → 5s，数据更实时
- **隐藏终端:** 启动后自动隐藏控制台窗口（GetConsoleWindow + SW_HIDE）
- **关闭按钮:** 标题栏右侧 ✕ 按钮 → /api/quit → os.Exit(0)

## 2026-06-02 23:55: 修复 CalculateDailyStats 日期字段为空
- **根因:** 精简代码时遗漏了 `s.Date = k` 赋值，导致 /api/history 返回的 date 始终为空字符串
- **影响:** 侧边栏"今日消耗"区域日期不显示
- **修复:** reader.go:72 补上 `s.Date = k`

## 2026-06-03 00:05: 移除浏览器自动打开 + 清理 web 端残留
- **删除:** cmdServe 中自动打开浏览器的 `exec.Command("cmd","/c","start",...)` 代码
- **删除:** 无用的 `os/exec` 导入
- **原因:** 网页端已移除，打开浏览器只有空白页

## 2026-06-03 00:15: 模型消耗添加时间筛选器
- **新增:** 侧边栏模型区域"今日"/"7日"切换按钮
- **新增:** /api/models?days=N 查询参数支持
- **交互:** 点击按钮即时切换数据，无需刷新

## 2026-06-03 00:20: 清理 help.html 中已删除的 index.html 引用

## 2026-06-03 03:19: 模型呼吸灯 + 日期选择器美化 + 离屏修复
- **文件:** `internal/web/static/sidebar.html`
- **呼吸灯:** `.mdot` 新增 `@keyframes breathe` 动画，圆点周期呼吸发光+缩放
- **对齐:** 模型名左侧间距从 8px 收窄至 6px，更靠近圆点
- **日期选择器:** 
  - `type=date` → `type=text` 修复日历弹出至屏幕外的离屏问题
  - 新增独立 `.dr` 容器（毛玻璃背景、紫色边框、渐变按钮）
  - 输入框占满全宽，不再溢出右边缘
- **筛选区重构:** 按钮和日期选择器放到独立 `.filter-area` 容器中，布局更清晰

## 2026-06-03 03:25: 模型霓虹呼吸灯增强 + 名称左对齐修正
- **文件:** `internal/web/static/sidebar.html`
- **呼吸灯统一绿色:**
  - 圆点 8px + 实心 `background:#22c55e`（移除 per-model 颜色）
  - 绿色多层 box-shadow 呼吸动画（2px/6px/14px → 4px/10px/22px/40px）
  - `currentColor` → 硬编码 `#22c55e`，所有模型共用绿色霓虹
- **模型卡片仿按钮样例:**
  - 移除静态 `.mi:nth-child(n)` 渐变背景
  - 每行动态 `--cr` RGB 变量驱动：`border`, `box-shadow`, `background`
  - `rgba(var(--cr),X)` 实现各模型自适应颜色
  - hover 增强 glow（仿按钮的 `0 0 8px + 0 0 20px` 双层发光）
- **名称左对齐:**
  - gap 4px→2px，`.mdot {margin-right:2px}`，padding-left 5px→4px
  - `.mn {text-align:left}` 显式声明
  - 文字起始距左侧 14px（之前 17px）
- **数字:** 去除 M/K 缩写，全部显示完整数字加逗号，字体 13px 适配百亿级
- **筛选:** 新增"30日"按钮 + "自定"日期区间选择器
- **API:** /api/models?from=YYYY-MM-DD&to=YYYY-MM-DD 支持
- **新增:** CalculateModelStatsByRange 函数
