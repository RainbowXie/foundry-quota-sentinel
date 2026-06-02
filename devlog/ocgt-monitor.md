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
