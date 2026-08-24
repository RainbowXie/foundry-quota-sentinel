<div align="center">

# foundry-quota-sentinel

**Multi-provider LLM quota &amp; usage monitor**

<p align="center">
  <img src="https://img.shields.io/badge/version-0.11.0-4466FF?style=flat-square" alt="version">
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go" alt="go">
  <img src="https://img.shields.io/badge/license-MIT-22c55e?style=flat-square" alt="license">
</p>

<br>

<img src="screenshots/sidebar.png" width="340" alt="foundry-quota-sentinel sidebar screenshot">

<br><br>

**English** &nbsp;|&nbsp; [**中文**](README.md)

</div>

---

## What is it

A desktop sidebar that watches quota and usage across **OpenCode Go, CommandCode, DeepSeek, Ollama Cloud, Kimi Code** — multi-account, with browser-automated login to capture credentials. Also provides CLI quota queries and a local web panel.

## GUI

Run the binary to launch the desktop sidebar (auto-hide dock on Windows; standalone window on macOS / Linux), or:

```bash
foundry-quota-sentinel serve --sidebar
```

Open `http://127.0.0.1:8788` in a browser for the web panel.

Add an account: click "Add Account" at the bottom → pick a provider → log in via browser. Right-click a card to open that account's official usage page.

## CLI

| Command | Purpose |
|---------|---------|
| `quota` | OpenCode Go plan quota |
| `balance` | DeepSeek balance |
| `history` | Local 7-day token usage history |
| `login-deepseek <name>` | Pop-up login for DeepSeek |
| `login-opencode <name>` | Pop-up login for OpenCode |
| `login-commandcode <name>` | Pop-up login for CommandCode |
| `login-ollama <name>` | One-shot browser login for Ollama |
| `login-kimi <name>` | One-shot browser login for Kimi Code |
| `quota-kimi [name]` | Kimi Code usage (all accounts by default) |
| `open-page kimi <name>` | Open Kimi membership quota page with login state |
| `config init` / `config add <name>` | Interactive config / add account |
| `config list` / `config use <name>` | List / switch accounts |

## Build

```bash
# GUI (requires CGO + system webkit2gtk / WKWebView / WebView2)
go build -ldflags="-s -w" -o foundry-quota-sentinel .
./scripts/build-linux.sh            # Linux GUI binary via Docker

# CLI / no GUI (pure Go static, includes serve web panel)
go build -tags nogui -o foundry-quota-sentinel .
```

Config lives in `~/.foundry-quota-sentinel/config.json` (Windows: `%USERPROFILE%\.foundry-quota-sentinel\`).

---

<div align="center">
  <sub>Built with Go &middot; system WebView &middot; echarts</sub>
</div>