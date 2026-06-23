# 跨平台编译 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 ocgt-monitor 在 Windows / macOS / Linux 三平台都能编译运行：Windows 保留 Win32 停靠侧边栏，mac/Linux 提供纯 webview 独立窗口，CLI 与 `serve` 全平台可用，构建走 GitHub Actions。

**Architecture:** 用 build tag 把 `internal/sidebar` 拆成四个同包文件（共享常量 + windows / unix / nogui 三套互斥实现，导出相同的 `New(port) *Sidebar` 与 `(*Sidebar).Run()`），`main.go` 零改动。跨平台构建在三种原生 GitHub runner 上各自编译。

**Tech Stack:** Go 1.26 + CGO，`github.com/webview/webview_go`，`golang.org/x/sys/windows`（仅 Windows），GitHub Actions。

**关于验证方式：** 本变更是构建系统/平台隔离改造，没有可做单元测试的纯逻辑，验证手段是「特定 build tag 下能否编译 + 运行二进制冒烟」。本机（Linux，无 webkit/mingw）只能编译 `nogui` 变体；`windows`/`unix` 路径由 CI（Task 4）编译验证。这是该类改动的诚实验证方式，并非遗漏测试。

**参考 spec：** `docs/superpowers/specs/2026-06-23-cross-platform-build-design.md`

---

## File Structure

- `internal/sidebar/sidebar.go` —（重写）无 build tag，仅含包注释 + 共享常量 `panelWidth` / `panelHeight`。
- `internal/sidebar/sidebar_windows.go` —（由原 `sidebar.go` 重命名而来）`//go:build windows && !nogui`，现有 Win32 停靠侧边栏实现，删除其中重复的 `panelWidth`/`panelHeight` 常量。
- `internal/sidebar/sidebar_unix.go` —（新建）`//go:build (darwin || linux) && !nogui`，纯 webview 独立窗口。
- `internal/sidebar/sidebar_nogui.go` —（新建）`//go:build nogui || (!windows && !darwin && !linux)`，无 GUI 桩，不 import webview。
- `.github/workflows/build.yml` —（新建）三平台 CI + tag 发版。
- `README.md` —（更新）平台差异与构建说明。
- `main.go` —**不改动**。

---

## Task 1: 拆出共享常量并给 Windows 实现加 build tag

**Files:**
- Rename: `internal/sidebar/sidebar.go` → `internal/sidebar/sidebar_windows.go`
- Modify: `internal/sidebar/sidebar_windows.go`（加 build tag、删重复常量）
- Create: `internal/sidebar/sidebar.go`（共享常量）

- [ ] **Step 1: git 重命名现有文件**

Run:
```bash
git mv internal/sidebar/sidebar.go internal/sidebar/sidebar_windows.go
```

- [ ] **Step 2: 给 `sidebar_windows.go` 加 build tag**

在文件最顶部（`package sidebar` 之前）插入 build 约束行和一个空行。文件开头从：

```go
package sidebar
```

改为：

```go
//go:build windows && !nogui

package sidebar
```

- [ ] **Step 3: 从 `sidebar_windows.go` 删除重复的共享常量**

该文件的 const 块开头当前为：

```go
const (
	panelWidth    = 360
	panelHeight   = 370
	// panelY is dynamic via state.PanelY
	triggerZonePx = 15
```

删除 `panelWidth` 和 `panelHeight` 两行，改为：

```go
const (
	// panelY is dynamic via state.PanelY
	triggerZonePx = 15
```

（其余常量 `triggerZonePx`、`animSteps`、`swp*`、`ws*` 等保持不变。）

- [ ] **Step 4: 创建共享 `sidebar.go`**

```go
// Package sidebar shows the monitor panel in a desktop window.
//
// The implementation is selected at build time and all variants expose the
// same API — New(port) and (*Sidebar).Run():
//   - windows (default):                   Win32 auto-hiding docking sidebar
//   - darwin/linux (default):              plain webview window
//   - any platform with -tags nogui, or
//     an unlisted GOOS:                    no-GUI stub (no webview dependency)
package sidebar

const (
	panelWidth  = 360
	panelHeight = 370
)
```

- [ ] **Step 5: 格式检查**

Run: `gofmt -l internal/sidebar/`
Expected: 无输出（无文件需要格式化）。

> 说明：此时项目在本机仍无法编译（尚无 linux/nogui 实现，windows 文件被排除）。可编译性在 Task 2 验证。

- [ ] **Step 6: Commit**

```bash
git add internal/sidebar/sidebar.go internal/sidebar/sidebar_windows.go
git commit -m "[053] sidebar: 拆出共享常量并给 Windows 实现加 build tag"
```

---

## Task 2: 新增 nogui 桩并验证本机可编译运行

**Files:**
- Create: `internal/sidebar/sidebar_nogui.go`

- [ ] **Step 1: 创建 `sidebar_nogui.go`**

```go
//go:build nogui || (!windows && !darwin && !linux)

package sidebar

import "fmt"

// Sidebar is a no-op placeholder for builds compiled without a GUI backend
// (the `nogui` tag) or for operating systems that have no sidebar
// implementation. It pulls in no CGO/webview dependency.
type Sidebar struct{}

// New returns a stub; no window is created. port is accepted to match the
// signature of the other build variants.
func New(port int) *Sidebar {
	return &Sidebar{}
}

// Run prints guidance instead of opening a window.
func (s *Sidebar) Run() {
	fmt.Println("此版本未编译图形界面。请运行 `ocgt-monitor serve`，再用浏览器打开 http://127.0.0.1:8788")
}
```

- [ ] **Step 2: 验证 nogui 变体可编译（本机关键验证）**

Run: `CGO_ENABLED=0 go build -tags nogui -o /tmp/ocgt-nogui .`
Expected: 成功，无输出，`/tmp/ocgt-nogui` 生成。

- [ ] **Step 3: 冒烟运行 CLI 子命令**

Run: `/tmp/ocgt-nogui version`
Expected: 打印 `ocgt-monitor v0.4.0`

- [ ] **Step 4: 冒烟运行无参（nogui 桩提示）**

Run: `/tmp/ocgt-nogui 2>/dev/null | tail -1`
Expected: 打印 `此版本未编译图形界面。请运行 ...`（来自桩的 `Run`；无参会先短暂启动 web 服务再调用桩，属预期）。

- [ ] **Step 5: go vet（nogui 标签下）**

Run: `CGO_ENABLED=0 go vet -tags nogui ./...`
Expected: 无输出（通过）。

- [ ] **Step 6: Commit**

```bash
git add internal/sidebar/sidebar_nogui.go
git commit -m "[054] sidebar: 新增 nogui 桩，打通本机跨平台编译"
```

---

## Task 3: 新增 macOS/Linux 纯 webview 窗口实现

**Files:**
- Create: `internal/sidebar/sidebar_unix.go`

> 本机无 webkit2gtk 开发库，无法在本地编译此文件；编译验证由 CI（Task 4）完成。本任务仅做语法/格式检查。

- [ ] **Step 1: 创建 `sidebar_unix.go`**

```go
//go:build (darwin || linux) && !nogui

package sidebar

import (
	"fmt"

	"github.com/webview/webview_go"
)

// Sidebar wraps a plain webview window. On macOS/Linux there is no OS-level
// edge docking or auto-hide; this is a normal window showing the same panel
// UI that the local HTTP server serves.
type Sidebar struct {
	wv webview.WebView
}

// New creates the window pointing at the local panel server on the given port.
func New(port int) *Sidebar {
	wv := webview.New(false)
	wv.SetTitle("ocgt-monitor")
	wv.SetSize(panelWidth, panelHeight, webview.HintFixed)
	wv.Navigate(fmt.Sprintf("http://127.0.0.1:%d/sidebar.html", port))
	return &Sidebar{wv: wv}
}

// Run shows the window and blocks until it is closed.
func (s *Sidebar) Run() {
	s.wv.Run()
	s.wv.Destroy()
}
```

- [ ] **Step 2: 格式检查**

Run: `gofmt -l internal/sidebar/sidebar_unix.go`
Expected: 无输出。

- [ ] **Step 3: 确认 nogui 变体仍不受影响**

Run: `CGO_ENABLED=0 go build -tags nogui -o /tmp/ocgt-nogui .`
Expected: 仍然成功（`sidebar_unix.go` 在 nogui 标签下被排除，不参与编译）。

- [ ] **Step 4: Commit**

```bash
git add internal/sidebar/sidebar_unix.go
git commit -m "[055] sidebar: 新增 macOS/Linux 纯 webview 窗口实现"
```

---

## Task 4: 新增 GitHub Actions 三平台构建 + 发版

**Files:**
- Create: `.github/workflows/build.yml`

- [ ] **Step 1: 创建 `.github/workflows/build.yml`**

```yaml
name: build

on:
  push:
    branches: [ master ]
    tags: [ 'v*' ]
  pull_request:
    branches: [ master ]

jobs:
  build:
    name: build (${{ matrix.os }})
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        include:
          - os: windows-latest
            artifact: ocgt-monitor.exe
            ldflags: "-s -w -H windowsgui"
          - os: macos-latest
            artifact: ocgt-monitor-macos
            ldflags: "-s -w"
          - os: ubuntu-22.04
            artifact: ocgt-monitor-linux
            ldflags: "-s -w"
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Install Linux deps
        if: runner.os == 'Linux'
        run: |
          sudo apt-get update
          sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.0-dev

      - name: Build
        env:
          CGO_ENABLED: "1"
        run: go build -ldflags="${{ matrix.ldflags }}" -o ${{ matrix.artifact }} .

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: ${{ matrix.artifact }}
          path: ${{ matrix.artifact }}

  release:
    name: release
    needs: build
    if: startsWith(github.ref, 'refs/tags/v')
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          path: dist

      - name: Create GitHub Release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GH_REPO: ${{ github.repository }}
        run: gh release create "${{ github.ref_name }}" $(find dist -type f) --generate-notes
```

> 说明：Windows runner 自带 MinGW（gcc），CGO 直接可用；若 CI 报 gcc 未找到，再在 Windows job 前加一步 `egor-tensin/setup-mingw@v2`。Linux 固定 `ubuntu-22.04` 是因为 24.04 已移除 `libwebkit2gtk-4.0-dev`，而 `webview_go` 编译期硬依赖 `webkit2gtk-4.0`。

- [ ] **Step 2: 校验 YAML 语法**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/build.yml')); print('YAML OK')"`
Expected: 打印 `YAML OK`

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/build.yml
git commit -m "[056] ci: GitHub Actions 三平台构建 + tag 发版"
```

---

## Task 5: 更新 README 平台说明

**Files:**
- Modify: `README.md`

- [ ] **Step 1: 在 README 中「快速开始」之后插入跨平台小节**

在 `## 快速开始` 段落结束、`## 使用模式` 之前，插入以下内容：

```markdown
## 平台支持

| 平台 | GUI 形态 | CLI / 网页面板 |
|---|---|---|
| Windows | 贴边自动隐藏的停靠侧边栏 | ✅ |
| macOS / Linux | 普通独立窗口（不停靠、不自动隐藏） | ✅ |

> 停靠侧边栏的贴边自动隐藏是 Windows 平台原生能力；macOS/Linux 上为等价的独立窗口面板。CLI 命令与 `serve` 网页面板在所有平台一致可用。

## 从源码构建

构建依赖（CGO）：
- **Windows**：MinGW64（gcc）
- **macOS**：Xcode Command Line Tools
- **Linux**：`libgtk-3-dev libwebkit2gtk-4.0-dev`

```bash
# macOS / Linux 原生 GUI
go build -ldflags="-s -w" -o ocgt-monitor .

# Windows GUI（见 build.bat）
build.bat

# 纯 CLI，无原生依赖（服务器/排错用）
CGO_ENABLED=0 go build -tags nogui -o ocgt-monitor .
```

发行版二进制由 GitHub Actions 在三平台原生构建，推送 `v*` tag 时自动发布到 Releases。
```

- [ ] **Step 2: 确认 Markdown 渲染无明显错误**

Run: `grep -n "平台支持" README.md`
Expected: 命中新增的小节标题。

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "[057] docs: README 增补平台支持与源码构建说明"
```

---

## Task 6: 改动前/后对照验证并推送触发 CI

**Files:** 无（验证 + 推送）

- [ ] **Step 1: 记录「改动前」基线（在本计划首个 commit 的父提交上）**

Run:
```bash
# [051] 是本计划开工前的最后一个提交（spec 更新），即所有代码改动的基线
BASE=$(git log --grep='\[051\]' --format=%h -1)
echo "before-commit: $BASE"
git checkout -q "$BASE"
echo "--- before: CGO_ENABLED=0 go build . ---"
CGO_ENABLED=0 go build -o /tmp/before . 2>&1 | head -5
echo "--- before: CGO_ENABLED=0 go build -tags nogui . ---"
CGO_ENABLED=0 go build -tags nogui -o /tmp/before . 2>&1 | head -5
```
Expected（两条均失败）：
- 普通构建：`imports ocgt-monitor/internal/sidebar` → `golang.org/x/sys/windows: build constraints exclude all Go files`
- nogui 构建：同样失败（改动前不存在 nogui/unix 实现，linux 下无 `sidebar.New`）

- [ ] **Step 2: 回到改动后并验证「改动后」**

Run:
```bash
git checkout -q master
echo "--- after: CGO_ENABLED=0 go build -tags nogui . ---"
CGO_ENABLED=0 go build -tags nogui -o /tmp/after . && echo "BUILD OK" && /tmp/after version
```
Expected：`BUILD OK` + `ocgt-monitor v0.4.0`

> 对照结论：改动前 Linux 上任何方式都无法构建；改动后 `nogui` 变体可构建并运行。windows/unix 完整 GUI 由下一步 CI 验证。

- [ ] **Step 3: 推送到 fork 触发三平台 CI**

Run:
```bash
git push origin master
gh run watch --exit-status $(gh run list --branch master --limit 1 --json databaseId --jq '.[0].databaseId')
```
Expected：三个 `build (...)` job 全部成功（绿）。若某平台失败，按错误修复后重推。

- [ ] **Step 4: 确认 CI 全绿**

Run: `gh run list --branch master --limit 1`
Expected：状态为 `completed` / `success`。

---

## Self-Review 记录

- **Spec coverage**：build tag 拆分（Task 1-3）、nogui 桩（Task 2）、unix 窗口（Task 3）、ubuntu-22.04 + webkit2gtk-4.0（Task 4）、CI+发版（Task 4）、README（Task 5）、改动前后对照验证（Task 6）—— 均覆盖。
- **签名一致性**：四个变体统一 `func New(port int) *Sidebar` 与 `func (s *Sidebar) Run()`，与 `main.go:102` 的 `sidebar.New(8788)` / `sb.Run()` 一致。
- **常量一致性**：`panelWidth`/`panelHeight` 仅在共享 `sidebar.go` 定义；windows 文件删除重复定义；unix 文件引用之；nogui 文件不使用（包级未用常量不报错）。
- **无占位符**：所有代码步骤含完整代码与确切命令。
