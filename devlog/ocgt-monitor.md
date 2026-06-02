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
