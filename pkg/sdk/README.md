# quota-sdk-go

`quota-sdk-go` 是一个用于多 Provider 配额数据获取、浏览器 CDP 自动化登录凭据拦截及跨进程并发凭据存储的纯 Go SDK 核心库。

## 特性

- **零 Cgo 纯 Go 实现**：无需系统 WebKit、GTK 或任何 Cgo 依赖，具备完整的跨平台编译与运行能力。
- **原生领域模型**：不强加损耗性的归一化单一大对象，忠实保留 OpenCode（三时间窗口）、DeepSeek（钱包与按天消耗）、Kimi（双重比例与长效轮换）、CommandCode、Ollama 各服务商的原生数据语义。
- **CDP 浏览器凭据拦截**：纯 Go WebSocket 实现 Chrome DevTools Protocol（CDP）交互，支持跨平台发现本地浏览器并拦截 Cookie、Bearer Token 与 LocalStorage 认证状态。
- **带并发锁的凭据持久化（参考实现）**：定义 `TokenStore` 接口并内建跨进程 OS 文件锁（Unix flock / Windows LockFileEx），支持多进程并发 Token 自动轮换与原子更新。
  - *说明*：当前阶段主仓展示端及桌面端（foundry-quota-sentinel）继续统一采用 `internal/config` 作为多账号配置中心与持久化中枢，`pkg/sdk/store.TokenStore` 与 `JSONStore` 为 SDK 独立形态及未来第三方集成提供标准化的持久化契约与参考实现。

## 防御与安全策略定位

- **HTTP 客户端与重定向防护**：Kimi 原生具备严格的域名白名单限制（`kimiAllowedHosts`）与强行拦截重定向（`http.ErrUseLastResponse`），防止凭据外泄；其余各 Provider 专注于标准 API/Web 数据的直接获取，消费端可按需注入定制化 `http.Client`。
- **Token 轮换机制**：各 Provider 客户端聚焦于纯内存中的配额获取与协议轮换计算（例如 Kimi 的 `FetchQuotaWithRefresh` 返回新的 Token 结果）。凭据的落盘持久化由消费方（如应用层 `internal/config` 事务或 `TokenStore` 驱动）负责，解耦计算与存储。

## 包结构

```text
pkg/sdk/
├── auth/
│   ├── browserauth/      # 纯 Go CDP 协议客户端、浏览器发现与进程生命周期管理
│   ├── opencode.go       # OpenCode 登录拦截与账户页
│   ├── deepseek_*.go     # DeepSeek 登录拦截、凭据提取与 Storage 回放
│   ├── kimi_*.go         # Kimi 登录拦截、凭据信封与页面内 Token 轮换监听
│   ├── commandcode.go    # CommandCode 登录拦截与凭据回放
│   └── ollama.go         # Ollama 登录拦截与凭据回放
├── providers/
│   ├── opencode/         # OpenCode 原生配额 RPC 获取与 Seroval 解析器
│   ├── deepseek/         # DeepSeek API 余额与 Web 钱包/按天明细获取
│   ├── kimi/             # Kimi 原生配额获取、自动双 Token 轮换与信封模型
│   ├── commandcode/      # CommandCode 原生配额与月度计算
│   └── ollama/           # Ollama HTML 页面配额解析器
└── store/
    ├── store.go          # TokenStore 存储与锁接口定义
    ├── flock*.go         # Unix/Windows 跨进程排他文件锁
    └── json_store.go     # 基于本地 JSON 的原子并发存储实现
```

## 导出为独立仓库

运行根目录下的导出脚本：
```bash
./scripts/export-sdk.sh [输出路径]
```
该脚本会读取 `pkg/sdk/export-manifest.json`，自动完成目录结构复制、独立 `go.mod` 生成、import 依赖路径映射转换（将 `foundry-quota-sentinel/pkg/sdk/...` 映射为 `github.com/ethan/quota-sdk-go/...`），并运行 `go test ./...` 确保独立发布的 SDK 自包含且测试全绿。

