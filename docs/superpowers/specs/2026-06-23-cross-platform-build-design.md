# 跨平台编译设计

日期：2026-06-23
状态：已批准，待实现

## 目标

让 ocgt-monitor 在 Windows / macOS / Linux 三个平台都能编译运行：

- **Windows**：保留现有 Win32 停靠侧边栏（贴边自动隐藏、置顶、无边框、圆角、滑动动画），行为零变化。
- **macOS / Linux**：提供一个带系统标题栏的固定大小独立窗口，显示现有面板 UI（不停靠、不置顶、不自动隐藏）。
- **全平台**：CLI 命令（`quota` / `balance` / `history` / `watch`）与 `serve` 网页面板照常工作。

非目标：在 macOS / Linux 上复刻停靠侧边栏的贴边自动隐藏行为（属于平台原生能力，不在本期范围）。

## 背景

当前唯一阻挡跨平台编译的是 `internal/sidebar/sidebar.go`：它 import `golang.org/x/sys/windows`，并通过 user32/kernel32/gdi32 调用 Win32 API 实现停靠侧边栏。`main.go` 无条件 import 该包，导致整个项目在非 Windows 平台无法编译。

其余代码已是跨平台纯 Go：

- `main.go` — CLI 调度
- `internal/web` — HTTP API + 网页面板服务
- `internal/config` / `internal/quota` / `internal/storage` / `internal/formatter` / `internal/state`

`webview_go` 库的公开 API 极小（`Run/Terminate/Destroy/Dispatch/Window/SetTitle/SetSize/Navigate/SetHtml/Init/Eval/Bind/Unbind`），不含位置、置顶、无边框、透明、托盘、鼠标等接口。停靠侧边栏的所有"侧边栏手感"均由 Win32 实现，不是 webview 特性。因此非 Windows 平台只能用纯 webview 得到一个普通独立窗口。

## 方案：build tag 拆分 `internal/sidebar`

`main.go` 不改动，继续调用 `sidebar.New(8788)` 与 `sb.Run()`。将 `internal/sidebar` 按平台拆为同包、同导出签名、互斥编译的多个文件：

| 文件 | build 约束 | 内容 |
|---|---|---|
| `sidebar.go` | 无 | 共享常量 `panelWidth` / `panelHeight`，包注释 |
| `sidebar_windows.go` | `//go:build windows && !nogui` | 现有完整实现搬入（Win32 停靠侧边栏，import `x/sys/windows`） |
| `sidebar_unix.go` | `//go:build (darwin \|\| linux) && !nogui` | 纯 webview 独立窗口实现 |
| `sidebar_nogui.go` | `//go:build nogui \|\| (!windows && !darwin && !linux)` | 桩实现，不 import webview；并兜底未列出的 OS |

约束设计要点：

- 四个变体对任意 `GOOS` × `nogui` 组合都恰好命中一个实现文件，避免符号重复声明或缺失：
  - `windows` 且非 nogui → windows 实现
  - `darwin`/`linux` 且非 nogui → unix 实现
  - 任意平台 + `nogui` → 桩实现
  - 其余未列出 OS（非 nogui）→ 桩实现兜底
- 共享常量只在无约束的 `sidebar.go` 定义；从 `sidebar_windows.go` 移除其原有重复定义。
- 四个变体都导出相同签名：`func New(port int) *Sidebar` 与 `func (s *Sidebar) Run()`，保证 `main.go` 与 `startSidebar()` 无需任何分支。

### `sidebar_unix.go` 行为

```
New(port):  wv = webview.New(false)
            wv.SetTitle("ocgt-monitor")
            wv.SetSize(panelWidth, panelHeight, webview.HintFixed)
            wv.Navigate("http://127.0.0.1:<port>/sidebar.html")
Run():      wv.Run(); wv.Destroy()
```

### `sidebar_nogui.go` 行为

`New` 返回一个空 `Sidebar`；`Run` 打印提示：GUI 未编译，请改用 `ocgt-monitor serve` 后在浏览器打开面板。该变体不 import `webview_go`，因此可在无 CGO / 无原生库的环境编译。

## 构建依赖与命令

`webview_go` 走 CGO：

- Windows：MinGW64（现有 `build.bat`）。
- macOS：Xcode Command Line Tools（系统自带 WebKit）。
- Linux：`libwebkit2gtk` 开发库 + GTK3 开发库。

构建命令：

```
# Windows GUI（现有）
build.bat

# macOS / Linux 原生 GUI
go build -o ocgt-monitor .

# 纯 CLI，无原生依赖
CGO_ENABLED=0 go build -tags nogui -o ocgt-monitor .
```

新增 `build.sh` 用于 mac/Linux 宿主平台原生构建。

## 文档

README 增补：

- macOS / Linux 的使用方式与构建依赖（webkit2gtk）。
- 平台差异说明：停靠侧边栏仅 Windows；macOS / Linux 为普通独立窗口；`serve` 网页面板与 CLI 全平台可用。
- `nogui` 纯 CLI 构建方式。

## 验证

改动前后在同一输入（本机 Linux）下对照：

- 改动前：`go build .` 失败于 `imports golang.org/x/sys/windows: build constraints exclude all Go files`（已实测）。
- 改动后：
  - `CGO_ENABLED=0 go build -tags nogui .` 成功，产出可运行的 CLI 二进制 —— 证明拆分后跨平台地基打通。
  - `go vet` 在各平台 build tag 下通过静态检查。
  - Windows 停靠侧边栏逻辑零改动（仅文件改名 + 加 build tag），不引入行为回归。

将并排展示"改动前失败 / 改动后成功"的实际输出作为验证证据。

## 影响范围

- 改动文件：`internal/sidebar/sidebar.go`（拆分），新增 `sidebar_unix.go` / `sidebar_nogui.go` / `build.sh`，更新 `README.md`。
- 不改动：`main.go` 及其余所有包。
