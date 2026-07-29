<div align="center">

# foundry-quota-sentinel

**Multi-provider LLM quota &amp; usage monitor**

*One desktop sidebar to watch OpenCode Go, DeepSeek, Ollama Cloud, and Kimi Code usage — multi-account, browser login.*

<p align="center">
  <img src="https://img.shields.io/badge/version-0.9.0-4466FF?style=flat-square" alt="version">
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go" alt="go">
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-0078D4?style=flat-square" alt="platform">
  <img src="https://img.shields.io/badge/build-CGO-FF2D78?style=flat-square" alt="cgo">
  <img src="https://img.shields.io/badge/license-MIT-22c55e?style=flat-square" alt="license">
</p>

<br>

<img src="screenshots/sidebar.png" width="340" alt="foundry-quota-sentinel sidebar screenshot">

<br><br>

**English** &nbsp;|&nbsp; [**中文**](README.md)

</div>

<br>

---

## Features

<table>
<tr>
  <td width="50%"><strong>OpenCode Go · Multi-account</strong><br><span style="color:#5A5A7A;font-size:13px">All accounts at once — Rolling / Weekly / Monthly quota bars with 80%/95% color warnings; a failing account only affects its own card.</span></td>
  <td width="50%"><strong>DeepSeek · Multi-account</strong><br><span style="color:#5A5A7A;font-size:13px">Balance + this month's per-model daily token usage (echarts stacked bars: cache hit / miss / output).</span></td>
</tr>
<tr>
  <td><strong>Ollama Cloud · Multi-account</strong><br><span style="color:#5A5A7A;font-size:13px">Session / Weekly usage and reset times, with credentials captured from a one-shot Chrome, Chromium, or Edge login.</span></td>
  <td><strong>Browser login</strong><br><span style="color:#5A5A7A;font-size:13px">Automatically captures OpenCode Cookies, DeepSeek Tokens, Ollama sessions, and Kimi Tokens — no manual F12 copy-paste.</span></td>
</tr>
<tr>
  <td><strong>Kimi Code · Multi-account</strong><br><span style="color:#5A5A7A;font-size:13px">Total usage splits into a black bar segment (Kimi) and a blue segment (Code), plus separate 5-hour and 7-day Code usage each with an absolute reset time; the access token auto-refreshes, so no re-login every ~15 min. "购买加油包" opens the official page (no automatic purchase).</span></td>
  <td><strong>Right-click → account page</strong><br><span style="color:#5A5A7A;font-size:13px">Right-click any card → open its OpenCode workspace, DeepSeek usage page, Ollama Settings, or Kimi membership quota page with that account's login state injected.</span></td>
</tr>
<tr>
  <td><strong>Dual theme</strong><br><span style="color:#5A5A7A;font-size:13px">Light "glassy cards" and dark "pro" themes, one-click switch.</span></td>
  <td><strong>Auto refresh</strong><br><span style="color:#5A5A7A;font-size:13px">Account quota polled every 2s; DeepSeek usage on a timer.</span></td>
</tr>
</table>

## Quick Start

1. **Download** the binary for your platform from Releases.
2. **Add an account** — click the "Add account" card → choose OpenCode, DeepSeek, Ollama, or Kimi → sign in and the credentials are saved automatically. The matching `login-*` commands are also available.
3. **Run** — double-click the binary; the desktop sidebar starts, no terminal needed.

> **PowerShell:** prefix commands with `.\`, e.g. `.\foundry-quota-sentinel login-deepseek myacct`

> **Which Linux artifact?** The GUI needs the system's webkit2gtk; pick the deb for your distro and `apt` pulls the deps automatically:
> - Newer distros (Ubuntu 24.04 / Mint 22) → `*-webkit41.deb`
> - Older distros (Ubuntu 22.04 / Mint 21) → `*-webkit40.deb`
> - Install: `sudo apt install ./foundry-quota-sentinel_*_amd64-webkit4X.deb`
> - No webkit / non-Debian / unsure → `*-linux-portable` (pure-Go static, zero deps); run it and open `http://127.0.0.1:8788/sidebar.html` in a browser.

## Platform Support

| Platform | GUI form | OpenCode browser login | DeepSeek browser login | Ollama browser login | Kimi browser login | CLI / web panel |
|---|---|---|---|---|---|---|
| Windows | Edge-docked auto-hiding sidebar | ✅ | ✅ | Chrome / Chromium / Edge must be on PATH | Chrome / Chromium / Edge must be on PATH | ✅ |
| macOS | Standalone window | ✅ | ✅ | Chrome / Chromium / Edge must be on PATH | Chrome / Chromium / Edge must be on PATH | ✅ |
| Linux | Standalone window | ✅ | ✅ | ✅ (verified with Edge) | Chrome / Chromium / Edge must be on PATH | ✅ |

> OpenCode, DeepSeek, Ollama, and Kimi all use the same `internal/browserauth` path: a one-shot temporary profile launches an installed Chrome/Chromium/Edge and the DevTools Protocol captures credentials. The system WebView only powers the local sidebar UI. Edge-docked auto-hide is a Windows-native capability.

## Build from Source

Build dependencies (CGO):
- **Windows**: MinGW64 (gcc)
- **macOS**: Xcode Command Line Tools
- **Linux**: `libgtk-3-dev libwebkit2gtk-4.0-dev`

```bash
# macOS / Linux native GUI
go build -ldflags="-s -w" -o foundry-quota-sentinel .

# Windows GUI (see build.bat)
build.bat

# Build the Linux GUI binary via Docker (webkit deps inside the container)
./scripts/build-linux.sh

# No native GUI window, no CGO (still has CLI + serve web panel; for servers/debugging)
CGO_ENABLED=0 go build -tags nogui -o foundry-quota-sentinel .
```

Release binaries are built natively on three platforms by GitHub Actions and published to Releases on `v*` tag push.

## Usage

**Desktop sidebar** — a translucent panel shows a single column of account cards: OpenCode Go accounts on top, Ollama and Kimi accounts in the middle, DeepSeek accounts below, and an "Add account" card at the bottom. Supports drag-to-position, pin, theme switch.

```bash
foundry-quota-sentinel serve --sidebar   # or just double-click the binary
```

**Web panel** — open `http://127.0.0.1:8788` for the same panel in a browser.

```bash
foundry-quota-sentinel serve
```

**CLI** — quick queries & credential management.

| Command | Purpose |
|------|------|
| `quota` | OpenCode Go subscription quota (active account) |
| `balance` | DeepSeek balance (official API key) |
| `history` | Local 7-day token usage history |
| `login-deepseek <name>` | Pop-up login, save DeepSeek web Token |
| `login-opencode <name>` | Pop-up login, save OpenCode Cookie |
| `login-ollama <name>` | One-shot Chrome / Chromium / Edge login, save Ollama Cookie |
| `login-kimi <name>` | One-shot Chrome / Chromium / Edge login, save Kimi Code web Token (with durable refresh) |
| `quota-kimi [name]` | Query Kimi Code total usage (Kimi / Code) plus 5-hour / 7-day Code usage with absolute reset times (all Kimi accounts if name omitted) |
| `open-page kimi <name>` | Inject login state, open Kimi membership quota page `membership/subscription?tab=quota` (no automatic purchase) |
| `config init` / `config add <name>` | Interactive setup / add account |
| `config list` / `config use <name>` | List / switch accounts |

## Configuration

Config lives in `~/.foundry-quota-sentinel/config.json` (Windows: `%USERPROFILE%\.foundry-quota-sentinel\config.json`), per user. OpenCode, DeepSeek, Ollama, and Kimi accounts live under `profiles`, `deepseek_accounts`, `ollama_accounts`, and `kimi_accounts`; Kimi credentials are saved as a versioned minimal auth envelope containing only the fields proven necessary for replay. (A legacy `~/.ocgt-monitor` directory is migrated automatically on first run.)

Ollama login requires Chrome, Chromium, or Edge to be available as a system executable. The app launches a one-shot browser with a private temporary profile, reads the session through loopback-only Chrome DevTools Protocol, then closes the browser and removes the profile. It never reads the user's everyday browser profile. Kimi Code login likewise launches a one-shot temporary browser to capture the web accessToken and a durable refresh token, replays the protected API call to confirm authentication, then saves the account; the access token expires in ~15 minutes, so the production implementation uses the refresh token to auto-renew quota queries and the account-page session — no re-login every 15 minutes.

Environment variables override the config file (for the CLI's active-account queries only):

```bash
export OPENCODE_GO_AUTH_COOKIE='session=xxx; ...'
export OPENCODE_GO_WORKSPACE_ID='wrk_xxxxxxxxxxxx'
export DEEPSEEK_API_KEY='sk-xxxxxxxxxxxxxxxx'
```

## Tech Stack

**Go 1.26+** · system WebView (WebKitGTK / WKWebView / WebView2) · Chrome DevTools Protocol · **echarts** · OpenCode Go RPC · DeepSeek / Ollama / Kimi web APIs

---

<div align="center">
  <sub>Built with Go &middot; system WebView &middot; echarts</sub>
</div>
