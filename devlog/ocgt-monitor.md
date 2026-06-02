## 2026-06-02 14:30: 允许删除最后一个 Profile
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
