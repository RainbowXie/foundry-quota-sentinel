<div align="center">

# ocgt-monitor

**OpenCode Go 套餐额度 &amp; Token 监控工具**

*Desktop sidebar monitor for OpenCode Go quota, DeepSeek balance &amp; token usage.*

<p align="center">
  <img src="https://img.shields.io/badge/version-0.4.0-4466FF?style=flat-square" alt="version">
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go" alt="go">
  <img src="https://img.shields.io/badge/platform-Windows-0078D4?style=flat-square&logo=windows" alt="windows">
  <img src="https://img.shields.io/badge/build-CGO-FF2D78?style=flat-square" alt="cgo">
  <img src="https://img.shields.io/badge/license-MIT-22c55e?style=flat-square" alt="license">
</p>

<br>

<img src="screenshots/sidebar.png" width="340" alt="ocgt-monitor 侧边栏截图">

<br><br>

**双击即用** &nbsp;&middot;&nbsp; 桌面侧边栏 &nbsp;&middot;&nbsp; 实时刷新

</div>

<br>

---

## 功能一览

<table>
<tr>
  <td width="50%"><strong>套餐额度</strong><br><span style="color:#5A5A7A;font-size:13px">Rolling / Weekly / Monthly 进度条，80%/95% 自动变色预警</span></td>
  <td width="50%"><strong>账户余额</strong><br><span style="color:#5A5A7A;font-size:13px">DeepSeek 可用余额、赠送金额、充值金额明细</span></td>
</tr>
<tr>
  <td><strong>今日消耗</strong><br><span style="color:#5A5A7A;font-size:13px">输入/输出 Tokens + 请求次数，数字滚动动画</span></td>
  <td><strong>模型分析</strong><br><span style="color:#5A5A7A;font-size:13px">按模型汇总消耗，1日/7日/30日/自定义日期筛选</span></td>
</tr>
<tr>
  <td><strong>趋势图表</strong><br><span style="color:#5A5A7A;font-size:13px">7 日堆叠柱状图 + 30 日模型分布环形图</span></td>
  <td><strong>双主题</strong><br><span style="color:#5A5A7A;font-size:13px">亮色「灵动卡片」与暗色「深色专业」一键切换</span></td>
</tr>
<tr>
  <td><strong>自动刷新</strong><br><span style="color:#5A5A7A;font-size:13px">2 秒轮询，数据实时更新</span></td>
  <td><strong>多账户</strong><br><span style="color:#5A5A7A;font-size:13px">多 Profile 管理，配置文件独立存储</span></td>
</tr>
</table>

## 快速开始

<div>
  <table>
  <tr>
    <td width="30" align="center" valign="top"><strong>1</strong></td>
    <td><strong>下载</strong><br><span style="color:#5A5A7A;font-size:13px">从 Releases 下载 <code>ocgt-monitor.exe</code>，放入任意文件夹。</span></td>
  </tr>
  <tr>
    <td width="30" align="center" valign="top"><strong>2</strong></td>
    <td><strong>配置凭证</strong><br><span style="color:#5A5A7A;font-size:13px">在终端运行 <code>ocgt-monitor config init</code>，按提示输入 Cookie 和 Workspace ID。</span></td>
  </tr>
  <tr>
    <td width="30" align="center" valign="top"><strong>3</strong></td>
    <td><strong>双击运行</strong><br><span style="color:#5A5A7A;font-size:13px">双击 <code>ocgt-monitor.exe</code>，桌面侧边栏即刻启动，无需终端。</span></td>
  </tr>
  </table>
</div>

> **PowerShell 用户：** 运行命令时需加 `.\` 前缀，如 `.\ocgt-monitor config init`

## 使用模式

[在线文档](https://xxtt-01.github.io/ocgt-monitor/) — 详细使用说明

### 桌面侧边栏
半透明面板固定在屏幕右侧边缘，鼠标移上滑出，移开自动隐藏。支持拖拽定位、固定、主题切换。

```bash
# 双击 exe 直接启动，或在终端运行：
ocgt-monitor serve --sidebar
```

### 网页面板
浏览器访问 `http://127.0.0.1:8788`，查看完整仪表盘。

```bash
ocgt-monitor serve
```

### 命令行
快速查询，适合脚本或远程终端。

| 命令 | 用途 |
|------|------|
| `quota` | 套餐额度 |
| `balance` | DeepSeek 余额 |
| `history` | 7 日消耗历史 |
| `config` | 查看配置 |
| `config init` | 配置向导 |
| `config list` | 列出所有账户 |

## 配置

配置存储在 `C:\Users\<用户名>\.ocgt-monitor\config.json`，每个 Windows 用户独立。

环境变量优先级高于配置文件：

```bash
set OPENCODE_GO_AUTH_COOKIE=session=xxx;.....
set OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxxxxxx
set DEEPSEEK_API_KEY=sk-xxxxxxxxxxxxxxxx
```

## 自行构建

需要 **Go 1.22+**、**MSYS2 MinGW64**（CGO 依赖）。

```bash
set CGO_ENABLED=1
set PATH=C:\msys64\mingw64\bin;%PATH%

:: 构建 GUI 版本（双击无终端窗口）
go build -ldflags="-s -w -H windowsgui" -o ocgt-monitor.exe .
```

## 项目结构

```
ocgt-monitor/
  main.go                     CLI 入口（11 个命令）
  build.bat                   构建脚本
  internal/
    sidebar/sidebar.go        桌面侧边栏 WebView2 + 自动隐藏
    web/server.go             HTTP 服务（API）
    web/static/sidebar.html   侧边栏 UI（双主题）
    web/static/help.html      使用手册
    quota/                    数据查询器
    config/config.go          配置管理
    state/state.go            全局状态
    formatter/format.go       格式化工具
    storage/reader.go         日志读取与统计
```

## 技术栈

**Go 1.22+** · **WebView2** · **OpenCode Go RPC** · **DeepSeek API**

---

<div align="center">
  <sub>Built with Go &middot; WebView2 &middot; Windows</sub>
</div>
