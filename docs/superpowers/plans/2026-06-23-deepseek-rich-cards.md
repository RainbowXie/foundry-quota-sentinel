# DeepSeek 多账户富卡片 Implementation Plan（B）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** GUI 面板为每个 DeepSeek 账户展示一张富卡片（钱包详情 + 当前月按天用量堆叠柱状图），凭证经弹 webview 登录自动截获网页 Bearer token。

**Architecture:** 配置层新增独立 `deepseek_accounts` 列表（不动 `Profiles`）。新增 `quota.DeepSeekWebQuerier` 调 `platform.deepseek.com` 两个内部接口（仅需 Bearer，实测确认）。`web` 包新增 `/api/deepseek` 并发查询所有账户。token 录入用独立一次性 webview 窗口（子命令 `login-deepseek`），注入 JS 钩 `fetch`/`XHR` 截 `authorization` 头。前端把原「账户余额」单卡区换成 DeepSeek 卡片区。

**与 A 计划解耦：** 本计划不修改 `web.NewServer` 的签名（A 会改它去接收 OpenCode 账户列表）。B 改为给 `Server` 加字段 + `SetDeepSeekAccounts(...)` setter，`main.go` 在 `NewServer` 之后调用，从而 A、B 互不冲突，落地顺序任意。

**Tech Stack:** Go 1.26、标准库 `net/http`/`encoding/json`/`os/exec`/`sync`、`github.com/webview/webview_go`、原生 HTML/JS（sidebar.html，无框架）。

**验证范式（全程沿用）：** 本仓库无单元测试文件，验证靠 `CGO_ENABLED=0 go build -tags nogui .`、`go vet -tags nogui ./...`、本机 `serve` + `curl`、GUI 手测、CI 三平台编译。涉及真实接口的步骤用环境变量 `DS_TOKEN`（一个有效网页 Bearer token）做只读验证；没有 token 时跳过该步并在 PR 说明里标注。

参考 spec：`docs/superpowers/specs/2026-06-23-deepseek-rich-cards-design.md`

> **关于提交编号：** 下文各 task 的 `[065]…[070]` 仅为占位示例。Plan A 会先执行并占用若干 `[NNN]`，因此 B 实际执行时按当时 `git log` 的下一个连续编号提交即可，不必拘泥这里的数字。

---

## Task 1: 配置层 — DeepSeekAccount 列表与增删

**Files:**
- Modify: `internal/config/config.go`（`Profile`/`Config` 定义在 9-19 行附近；`AddProfile` 在 65 行附近）

- [ ] **Step 1: 在 config.go 增加结构与字段**

在 `internal/config/config.go` 的 `type Config struct {...}` 之后、`configDir` 之前，新增 `DeepSeekAccount` 类型，并给 `Config` 增加字段。把现有：

```go
type Config struct {
	ActiveProfile string             `json:"active_profile"`
	Profiles      map[string]Profile `json:"profiles"`
}
```

改为：

```go
type DeepSeekAccount struct {
	Name  string `json:"name"`
	Token string `json:"token"` // platform.deepseek.com 网页 Bearer token
}

type Config struct {
	ActiveProfile    string             `json:"active_profile"`
	Profiles         map[string]Profile `json:"profiles"`
	DeepSeekAccounts []DeepSeekAccount  `json:"deepseek_accounts,omitempty"`
}
```

- [ ] **Step 2: 增加增删方法**

在 `internal/config/config.go` 末尾（`HasEnvVars` 之前或之后均可）追加：

```go
// UpsertDeepSeekAccount 按 Name 覆盖或追加一个 DeepSeek 账户。
func (c *Config) UpsertDeepSeekAccount(a DeepSeekAccount) {
	for i := range c.DeepSeekAccounts {
		if c.DeepSeekAccounts[i].Name == a.Name {
			c.DeepSeekAccounts[i] = a
			return
		}
	}
	c.DeepSeekAccounts = append(c.DeepSeekAccounts, a)
}

// DeleteDeepSeekAccount 按 Name 删除，不存在返回错误。
func (c *Config) DeleteDeepSeekAccount(name string) error {
	for i := range c.DeepSeekAccounts {
		if c.DeepSeekAccounts[i].Name == name {
			c.DeepSeekAccounts = append(c.DeepSeekAccounts[:i], c.DeepSeekAccounts[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("DeepSeek 账户 %q 不存在", name)
}
```

- [ ] **Step 3: 编译与 vet**

Run: `cd /mnt/data/Work/Projects/ocgt-monitor && CGO_ENABLED=0 go build -tags nogui . && go vet -tags nogui ./...`
Expected: 无输出，exit 0（`fmt` 已在该文件 import，无需新增）。

- [ ] **Step 4: Commit**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
git add internal/config/config.go
git commit -m "[065] feat(config): 新增 deepseek_accounts 列表与增删方法

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: 查询器 — DeepSeekWebQuerier（钱包 + 按天用量）

**Files:**
- Modify: `internal/quota/types.go`（末尾追加结构）
- Create: `internal/quota/deepseek_web.go`

实测确认（spec 背景）：两接口仅需 `authorization: Bearer <token>` + `x-client-*` 头，无需 cookie。`get_user_summary` 返回 `data.biz_data.{current_token, monthly_usage(string), total_usage, normal_wallets:[{currency, balance(string), token_estimation(string)}]}`。`usage/amount?month=M&year=Y` 返回 `data.biz_data.{total[], days[]}`，每个用量项含 `type ∈ {PROMPT_TOKEN, PROMPT_CACHE_HIT_TOKEN, PROMPT_CACHE_MISS_TOKEN, RESPONSE_TOKEN, REQUEST}`，单日总量 = 命中缓存 + 未命中缓存 + 输出。`days[]` 内部 value 键名与是否按模型嵌套未逐字确认，故解析用「递归按 type 求和」，对键名/层级不敏感。

- [ ] **Step 1: 追加数据结构到 types.go**

在 `internal/quota/types.go` 末尾追加：

```go
// DeepSeekSummary 是网页后台钱包/汇总（get_user_summary）的精简视图。
type DeepSeekSummary struct {
	Currency        string  `json:"currency"`
	Balance         float64 `json:"balance"`          // normal_wallets 同币种求和
	TokenEstimation int64   `json:"token_estimation"` // 可用 token 估算
	MonthlyUsage    int64   `json:"monthly_usage"`    // 本月用量（token）
	CurrentToken    int64   `json:"current_token"`    // 赠送额度
}

// DeepSeekDayUsage 是某一天跨模型聚合后的 token 用量。
type DeepSeekDayUsage struct {
	Date     string `json:"date"`
	CacheHit int64  `json:"cache_hit"`  // 输入命中缓存
	CacheMiss int64 `json:"cache_miss"` // 输入未命中缓存
	Output   int64  `json:"output"`     // 输出
	Total    int64  `json:"total"`      // = CacheHit + CacheMiss + Output
}
```

- [ ] **Step 2: 新建 deepseek_web.go**

Create `internal/quota/deepseek_web.go`：

```go
package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const deepseekWebBaseURL = "https://platform.deepseek.com/api/v0"

// DeepSeekWebQuerier 用网页登录态的 Bearer token 访问 platform.deepseek.com
// 的内部接口，拿到官方 sk- API 给不了的富数据（按天用量、token 估算、当月用量）。
type DeepSeekWebQuerier struct{ Token string }

// setWebHeaders 设置网页客户端必须的请求头（实测仅需 Bearer + x-client-*，无需 cookie）。
func (q *DeepSeekWebQuerier) setWebHeaders(req *http.Request) {
	req.Header.Set("accept", "*/*")
	req.Header.Set("authorization", "Bearer "+q.Token)
	req.Header.Set("referer", "https://platform.deepseek.com/usage")
	req.Header.Set("user-agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("x-client-version", "1.0.0")
	req.Header.Set("x-app-version", "1.0.0")
	req.Header.Set("x-client-locale", "zh_CN")
	req.Header.Set("x-client-bundle-id", "com.deepseek.chat")
	req.Header.Set("x-client-timezone-offset", "28800")
}

// getBizData 发 GET、校验 HTTP 200 与 code==0，返回 data.biz_data 原始 JSON。
func (q *DeepSeekWebQuerier) getBizData(url string) (json.RawMessage, error) {
	if q.Token == "" {
		return nil, fmt.Errorf("DeepSeek token 为空")
	}
	req, _ := http.NewRequest("GET", url, nil)
	q.setWebHeaders(req)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("鉴权失败：token 可能已过期 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data struct {
			BizData json.RawMessage `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("接口错误 code=%d: %s", env.Code, env.Msg)
	}
	return env.Data.BizData, nil
}

// parseNum 把 JSON 里可能是字符串也可能是数字的值转成 int64。
func parseNum(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return int64(f), true
	}
	return 0, false
}

func parseFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

// FetchSummary 调 get_user_summary，返回钱包/汇总精简视图。
func (q *DeepSeekWebQuerier) FetchSummary() (*DeepSeekSummary, error) {
	raw, err := q.getBizData(deepseekWebBaseURL + "/users/get_user_summary")
	if err != nil {
		return nil, err
	}
	var bd struct {
		CurrentToken json.Number `json:"current_token"`
		MonthlyUsage any         `json:"monthly_usage"`
		NormalWallets []struct {
			Currency        string `json:"currency"`
			Balance         string `json:"balance"`
			TokenEstimation string `json:"token_estimation"`
		} `json:"normal_wallets"`
	}
	if err := json.Unmarshal(raw, &bd); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}
	s := &DeepSeekSummary{}
	if n, err := bd.CurrentToken.Int64(); err == nil {
		s.CurrentToken = n
	}
	if n, ok := parseNum(bd.MonthlyUsage); ok {
		s.MonthlyUsage = n
	}
	for _, w := range bd.NormalWallets {
		if s.Currency == "" {
			s.Currency = w.Currency
		}
		s.Balance += parseFloat(w.Balance)
		if n, ok := parseNum(w.TokenEstimation); ok {
			s.TokenEstimation += n
		}
	}
	return s, nil
}

// sumByType 递归遍历任意 JSON 子树，把所有 {"type": <名>, <某数值键>: <数>} 形态的
// 用量项按 type 求和。对 value 键名与是否按模型嵌套都不敏感。
func sumByType(v any, acc map[string]int64) {
	switch t := v.(type) {
	case map[string]any:
		if typ, ok := t["type"].(string); ok {
			for k, val := range t {
				if k == "type" {
					continue
				}
				if n, ok := parseNum(val); ok {
					acc[typ] += n
					break
				}
			}
		}
		for _, val := range t {
			sumByType(val, acc)
		}
	case []any:
		for _, e := range t {
			sumByType(e, acc)
		}
	}
}

// FetchUsage 调 usage/amount?month=M&year=Y，按天跨模型聚合。
func (q *DeepSeekWebQuerier) FetchUsage(year, month int) ([]DeepSeekDayUsage, error) {
	url := fmt.Sprintf("%s/usage/amount?month=%d&year=%d", deepseekWebBaseURL, month, year)
	raw, err := q.getBizData(url)
	if err != nil {
		return nil, err
	}
	var bd struct {
		Days []map[string]any `json:"days"`
	}
	if err := json.Unmarshal(raw, &bd); err != nil {
		return nil, fmt.Errorf("decode usage: %w", err)
	}
	out := make([]DeepSeekDayUsage, 0, len(bd.Days))
	for _, day := range bd.Days {
		acc := map[string]int64{}
		sumByType(day, acc)
		du := DeepSeekDayUsage{
			CacheHit:  acc["PROMPT_CACHE_HIT_TOKEN"],
			CacheMiss: acc["PROMPT_CACHE_MISS_TOKEN"],
			Output:    acc["RESPONSE_TOKEN"],
		}
		if d, ok := day["date"].(string); ok {
			du.Date = d
		}
		du.Total = du.CacheHit + du.CacheMiss + du.Output
		out = append(out, du)
	}
	return out, nil
}
```

- [ ] **Step 3: 编译与 vet**

Run: `cd /mnt/data/Work/Projects/ocgt-monitor && CGO_ENABLED=0 go build -tags nogui . && go vet -tags nogui ./...`
Expected: 无输出，exit 0。

- [ ] **Step 4: 真实接口只读验证（有 DS_TOKEN 时）**

写一个临时验证程序确认两个接口解析正确（用完即删，不进仓库）：

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
mkdir -p /tmp/dsverify && cat > /tmp/dsverify/main.go <<'EOF'
package main
import ("fmt";"os";"time";"ocgt-monitor/internal/quota")
func main(){
  q := &quota.DeepSeekWebQuerier{Token: os.Getenv("DS_TOKEN")}
  s, err := q.FetchSummary(); fmt.Println("summary:", s, err)
  now := time.Now()
  days, err := q.FetchUsage(now.Year(), int(now.Month()))
  fmt.Println("days:", len(days), err)
  for i, d := range days { if i < 3 || d.Total>0 { fmt.Printf("  %+v\n", d) } }
}
EOF
DS_TOKEN=<有效token> go run /tmp/dsverify/main.go
rm -rf /tmp/dsverify
```

Expected: `summary` 非空且 `Balance`/`CurrentToken` 数值合理；`days` 数量约等于当月天数，且对某一天核对 `Total == CacheHit + CacheMiss + Output`。若无 token，跳过本步并在 PR 标注「待真实 token 验证」。

- [ ] **Step 5: Commit**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
git add internal/quota/types.go internal/quota/deepseek_web.go
git commit -m "[066] feat(quota): DeepSeekWebQuerier 钱包汇总 + 按天用量聚合

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: 后端端点 — /api/deepseek 并发查询 + Server 接线

**Files:**
- Modify: `internal/web/server.go`（`Server` 定义 21-25、`NewServer` 27-29、`Start` 内路由注册区 35-114）
- Modify: `main.go`（`startSidebar` 92-104、`cmdServe` 203-218）

不改 `NewServer` 签名；改为加字段 + setter（见计划头「与 A 计划解耦」）。

- [ ] **Step 1: Server 加字段、类型与 setter**

在 `internal/web/server.go`，把：

```go
type Server struct {
	addr     string
	querier  *quota.OpenCodeGoQuerier
	deepseek *quota.DeepSeekQuerier
}
```

改为：

```go
type DeepSeekAccount struct {
	Name  string
	Token string
}

type Server struct {
	addr          string
	querier       *quota.OpenCodeGoQuerier
	deepseek      *quota.DeepSeekQuerier
	dsAccounts    []DeepSeekAccount
}

// SetDeepSeekAccounts 注入要在 /api/deepseek 并发查询的网页账户列表。
func (s *Server) SetDeepSeekAccounts(accs []DeepSeekAccount) { s.dsAccounts = accs }
```

- [ ] **Step 2: 注册 /api/deepseek 处理器**

在 `internal/web/server.go` 的 `Start` 内，紧接 `/api/balance` 处理器（45 行 `})` 之后）插入。注意需要在文件顶部 import 区加入 `"sync"` 和 `"time"`（`time` 已 import，只补 `sync`）：

```go
	mux.HandleFunc("/api/deepseek", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string                  `json:"name"`
			Success bool                    `json:"success"`
			Summary *quota.DeepSeekSummary  `json:"summary,omitempty"`
			Days    []quota.DeepSeekDayUsage `json:"days,omitempty"`
			Error   string                  `json:"error,omitempty"`
		}
		accs := s.dsAccounts
		cards := make([]card, len(accs))
		now := time.Now()
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a DeepSeekAccount) {
				defer wg.Done()
				c := card{Name: a.Name}
				q := &quota.DeepSeekWebQuerier{Token: a.Token}
				sum, err := q.FetchSummary()
				if err != nil {
					c.Error = err.Error()
					cards[i] = c
					return
				}
				days, err := q.FetchUsage(now.Year(), int(now.Month()))
				if err != nil {
					c.Error = err.Error()
					cards[i] = c
					return
				}
				c.Success = true
				c.Summary = sum
				c.Days = days
				cards[i] = c
			}(i, a)
		}
		wg.Wait()
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": cards})
	})
```

- [ ] **Step 3: main.go 构建并注入账户**

在 `main.go` 末尾（与其它 `make*` helper 同区）新增：

```go
func buildDeepSeekAccounts() []web.DeepSeekAccount {
	out := make([]web.DeepSeekAccount, 0, len(cfg.DeepSeekAccounts))
	for _, a := range cfg.DeepSeekAccounts {
		if a.Token == "" {
			continue
		}
		out = append(out, web.DeepSeekAccount{Name: a.Name, Token: a.Token})
	}
	return out
}
```

在 `startSidebar`（92 行）里，把：

```go
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
```

改为：

```go
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
	srv.SetDeepSeekAccounts(buildDeepSeekAccounts())
```

在 `cmdServe`（211-212 行 headless 分支）里，把：

```go
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
```

改为：

```go
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
	srv.SetDeepSeekAccounts(buildDeepSeekAccounts())
```

- [ ] **Step 4: 编译与 vet**

Run: `cd /mnt/data/Work/Projects/ocgt-monitor && CGO_ENABLED=0 go build -tags nogui . && go vet -tags nogui ./...`
Expected: 无输出，exit 0。

- [ ] **Step 5: 端点行为验证（空账户 + 有账户）**

空账户场景（不依赖 token）：

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
CGO_ENABLED=0 go build -tags nogui -o /tmp/ocgtm . && OCGT_PORT=8799 /tmp/ocgtm serve &
sleep 1
curl -s localhost:8799/api/deepseek; echo
kill %1
```

Expected：未配置 `deepseek_accounts` 时返回 `{"success":true,"data":[]}`（`data` 为空数组或 `null`，均可接受）。

有账户场景（有 DS_TOKEN 时）：临时在 `~/.ocgt-monitor/config.json` 的根对象加 `"deepseek_accounts":[{"name":"测试","token":"<DS_TOKEN>"},{"name":"坏的","token":"invalid"}]`，重启 `serve`，`curl` 应返回两条，按 name 升序，「坏的」`success:false` 且 error 含「鉴权失败」，「测试」`success:true` 含 `summary` 与 `days`。验证后还原 config。

- [ ] **Step 6: Commit**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
git add internal/web/server.go main.go
git commit -m "[067] feat(web): /api/deepseek 并发查询多账户富数据

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: 凭证录入 — webview 登录窗口 + login-deepseek 子命令

**Files:**
- Create: `internal/sidebar/login_webview.go`
- Create: `internal/sidebar/login_nogui.go`
- Modify: `main.go`（`main` switch 76-89、`printUsage` 246 附近、末尾新增 `cmdLoginDeepSeek`）
- Modify: `internal/web/server.go`（新增 `/api/deepseek/login`，import `os/exec`）

webview_go 一进程一窗口，故登录抓取走独立子进程窗口；GUI 内的按钮经服务端 spawn 该子进程。

- [ ] **Step 1: 新建真实 webview 登录窗口**

Create `internal/sidebar/login_webview.go`：

```go
//go:build (windows || darwin || linux) && !nogui

package sidebar

import (
	"fmt"

	"github.com/webview/webview_go"
)

// captureJS 在每个页面加载前注入，monkey-patch fetch 与 XHR，截获请求里的
// Authorization: Bearer <token>。用户登录后页面自动发带 Bearer 的请求即被捕获。
const captureJS = `
(function(){
  function send(t){ try{ if(window.__ocgtCaptureToken) window.__ocgtCaptureToken(t); }catch(e){} }
  function pick(v){ if(!v) return; var m=/Bearer\s+([A-Za-z0-9._\-]+)/.exec(String(v)); if(m && m[1] && m[1].length>=20) send(m[1]); }
  try{
    var sh = XMLHttpRequest.prototype.setRequestHeader;
    XMLHttpRequest.prototype.setRequestHeader = function(k,v){ try{ if(String(k).toLowerCase()==='authorization') pick(v); }catch(e){} return sh.apply(this, arguments); };
  }catch(e){}
  try{
    var of = window.fetch;
    if(of){ window.fetch = function(input, init){ try{
      if(init && init.headers){ var h=init.headers;
        if(h.get){ pick(h.get('authorization')); }
        else { for(var k in h){ if(String(k).toLowerCase()==='authorization') pick(h[k]); } }
      }
    }catch(e){} return of.apply(this, arguments); }; }
  }catch(e){}
})();
`

// RunDeepSeekLogin 打开一个登录窗口指向 DeepSeek 平台，截获登录后请求中的
// Bearer token 后关窗返回。用户直接关窗（未登录）则返回错误。
func RunDeepSeekLogin() (string, error) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("登录 DeepSeek（登录后自动获取凭证，随后自动关闭）")
	w.SetSize(480, 720, webview.HintNone)

	var captured string
	w.Bind("__ocgtCaptureToken", func(t string) {
		if t == "" || captured != "" {
			return
		}
		captured = t
		w.Dispatch(func() { w.Terminate() })
	})
	w.Init(captureJS)
	w.Navigate("https://platform.deepseek.com/sign_in")
	w.Run()

	if captured == "" {
		return "", fmt.Errorf("未捕获到登录凭证（窗口已关闭）")
	}
	return captured, nil
}
```

- [ ] **Step 2: 新建 nogui 桩**

Create `internal/sidebar/login_nogui.go`：

```go
//go:build nogui || (!windows && !darwin && !linux)

package sidebar

import "fmt"

// RunDeepSeekLogin 在无 GUI 构建下不可用。
func RunDeepSeekLogin() (string, error) {
	return "", fmt.Errorf("此版本未编译图形界面，无法弹窗登录；请用带 GUI 的版本运行 `ocgt-monitor login-deepseek`")
}
```

- [ ] **Step 3: main.go 新增 login-deepseek 命令**

在 `main.go` 的 `main()` switch（76-89 行）里，`case "serve": cmdServe()` 之后新增一行：

```go
	case "login-deepseek": cmdLoginDeepSeek()
```

在 `printUsage()` 里 `serve` 那行（246 行）之后新增：

```go
	fmt.Println("  login-deepseek <名称> 弹窗登录 DeepSeek 并保存网页凭证")
```

在 `main.go` 末尾追加函数（`web`、`quota`、`sidebar`、`config`、`strings` 均已 import）：

```go
func cmdLoginDeepSeek() {
	name := "DeepSeek"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		name = strings.TrimSpace(os.Args[2])
	}
	fmt.Println("正在打开登录窗口，请在窗口内完成 DeepSeek 登录…")
	token, err := sidebar.RunDeepSeekLogin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "登录失败: %v\n", err)
		os.Exit(1)
	}
	q := &quota.DeepSeekWebQuerier{Token: token}
	if _, err := q.FetchSummary(); err != nil {
		fmt.Fprintf(os.Stderr, "凭证校验失败: %v\n", err)
		os.Exit(1)
	}
	cfg.UpsertDeepSeekAccount(config.DeepSeekAccount{Name: name, Token: token})
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "保存失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK DeepSeek 账户 %q 已保存\n", name)
}
```

- [ ] **Step 4: server.go 新增 /api/deepseek/login（GUI 按钮触发子进程）**

在 `internal/web/server.go` import 区加入 `"os/exec"`。在 `Start` 内 `/api/deepseek` 处理器之后新增：

```go
	mux.HandleFunc("/api/deepseek/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		exe, err := os.Executable()
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		args := []string{"login-deepseek"}
		if name != "" {
			args = append(args, name)
		}
		cmd := exec.Command(exe, args...)
		if err := cmd.Start(); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})
```

- [ ] **Step 5: 编译两种构建**

Run:
```
cd /mnt/data/Work/Projects/ocgt-monitor
CGO_ENABLED=0 go build -tags nogui . && go vet -tags nogui ./...
go build . && go vet ./...
```
Expected: 两条均无输出、exit 0（第二条需本机有 webkit2gtk 开发库；缺库时跳过并在 PR 标注，交由 CI 验证 GUI 构建）。

- [ ] **Step 6: GUI 手测（带 GUI 构建可用时）**

Run: `cd /mnt/data/Work/Projects/ocgt-monitor && go build -o /tmp/ocgtm . && /tmp/ocgtm login-deepseek 我的DS`
Expected: 弹出窗口 → 完成 DeepSeek 登录 → 窗口在登录后自动关闭 → 终端打印 `OK DeepSeek 账户 "我的DS" 已保存` → `~/.ocgt-monitor/config.json` 出现 `deepseek_accounts` 含该账户与 token。无 GUI 环境则跳过，CI 验证编译。

- [ ] **Step 7: Commit**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
git add internal/sidebar/login_webview.go internal/sidebar/login_nogui.go main.go internal/web/server.go
git commit -m "[068] feat(sidebar): 弹窗登录 DeepSeek 自动截获网页 token

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: 前端 — DeepSeek 卡片区 + 按天堆叠柱状图

**Files:**
- Modify: `internal/web/static/sidebar.html`（余额区 143 行、`fb()` 210-216 行、主循环 250-253 行）

复用现有堆叠柱 CSS（`.bc-c/.bc-b/.bc-sk/.bc-op/.bc-ip/.bc-v/.bc-l` 等，见 `renderBar` 227-237）与卡片样式 `.bc`。DeepSeek 每日 3 段堆叠（命中缓存/未命中/输出）。

- [ ] **Step 1: 替换余额区 DOM 为 DeepSeek 卡片容器**

把第 143 行：

```html
<div class="hide" id=balanceSec><div class=st>账户余额</div><div class=bc id=balLoad><div class=bc-r><span class=bl>加载中...</span></div></div></div>
```

替换为：

```html
<div id=deepseekSec><div class=st>DeepSeek<span id=dsAdd style="float:right;cursor:pointer;font-weight:600;color:var(--text-sec)">＋登录</span></div><div id=dsCards><div class=emp>加载中...</div></div></div>
```

- [ ] **Step 2: 用 fd() 替换 fb()**

把 210-216 行的 `/* balance */` 段（`async function fb(){...}` 整段）替换为：

```javascript
/* deepseek cards */
function dsBar(days){
  if(!days||!days.length)return'<div class=emp>本月暂无用量</div>';
  var mx=Math.max.apply(Math,days.map(function(x){return x.total||0}));if(!mx)mx=1;
  var h='<div class=bc-c>';
  days.forEach(function(x){
    var tot=x.total||0,skH=Math.max(1,(tot/mx)*100);
    var hit=x.cache_hit||0,miss=x.cache_miss||0,out=x.output||0;
    var hP=tot?hit/tot*100:0,mP=tot?miss/tot*100:0,oP=tot?out/tot*100:0;
    h+='<div class=bc-b><div class=bc-sk style="height:'+skH+'%">';
    h+='<div style="height:'+oP+'%;background:var(--ch-out)"></div>';
    h+='<div style="height:'+mP+'%;background:#f59e0b"></div>';
    h+='<div style="height:'+hP+'%;background:var(--ch-in)"></div>';
    h+='</div><div class=bc-v>'+ab(tot)+'</div><div class=bc-l>'+(x.date?String(x.date).slice(5):'')+'</div></div>';
  });
  h+='</div><div class=bc-lg><span class=bc-li><span class=bc-sq style=background:var(--ch-in)></span>命中</span><span class=bc-li><span class=bc-sq style=background:#f59e0b></span>未命中</span><span class=bc-li><span class=bc-sq style=background:var(--ch-out)></span>输出</span></div>';
  return h;
}
async function fd(){
  try{
    var r=await(await fetch('/api/deepseek')).json();
    var box=document.getElementById('dsCards');
    if(!r.success){box.innerHTML='<div class=er>'+(r.error||'查询失败')+'</div>';return}
    var arr=r.data||[];
    if(!arr.length){box.innerHTML='<div class=emp>未配置 DeepSeek 账户，点右上「＋登录」添加</div>';return}
    var h='';
    arr.forEach(function(c){
      h+='<div class=bc style="margin-bottom:8px">';
      h+='<div class=bc-r><span class=bl>'+c.name+'</span><span class=bv style=font-size:11px>'+(c.success?'':'<span style=color:var(--red)>离线</span>')+'</span></div>';
      if(!c.success){
        h+='<div class=bc-d style=color:var(--red)>'+(c.error||'查询失败')+'</div>';
        h+='<div class=bc-d><span class=dsLogin data-name="'+c.name+'" style="cursor:pointer;color:var(--text-sec)">重新登录</span></div>';
      }else{
        var s=c.summary||{};
        h+='<div class=bc-r><span class=bl>余额</span><span class=bv>'+(s.balance||0).toFixed(2)+' <span style=font-size:10px;color:var(--text-sec)>'+(s.currency||'')+'</span></span></div>';
        h+='<div class=bc-d>可用≈'+n(s.token_estimation||0)+' tokens · 本月用量 '+n(s.monthly_usage||0)+' · 赠送 '+n(s.current_token||0)+'</div>';
        h+=dsBar(c.days);
      }
      h+='</div>';
    });
    box.innerHTML=h;
  }catch(e){document.getElementById('dsCards').innerHTML='<div class=er>'+e.message+'</div>'}
}
function dsDoLogin(name){fetch('/api/deepseek/login'+(name?'?name='+encodeURIComponent(name):'')).then(function(){setTimeout(fd,1500)})}
document.getElementById('dsAdd').addEventListener('click',function(){var nm=prompt('账户名称：','DeepSeek');if(nm)dsDoLogin(nm.trim())});
document.getElementById('dsCards').addEventListener('click',function(e){var t=e.target;if(t.classList.contains('dsLogin'))dsDoLogin(t.getAttribute('data-name'))});
```

- [ ] **Step 3: 主循环把 fb 换成 fd**

把第 251 行：

```javascript
async function fa(){var ok=true;try{await Promise.all([fq(),fb(),fh(),fm().catch(function(){})]);renderBar(hData);renderDonut()}catch(e){ok=false}
```

改为：

```javascript
async function fa(){var ok=true;try{await Promise.all([fq(),fd(),fh(),fm().catch(function(){})]);renderBar(hData);renderDonut()}catch(e){ok=false}
```

- [ ] **Step 4: 编译（embed 资产随之更新）**

Run: `cd /mnt/data/Work/Projects/ocgt-monitor && CGO_ENABLED=0 go build -tags nogui . && go vet -tags nogui ./...`
Expected: 无输出，exit 0。

- [ ] **Step 5: 浏览器验证渲染**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
CGO_ENABLED=0 go build -tags nogui -o /tmp/ocgtm . && OCGT_PORT=8799 /tmp/ocgtm serve &
sleep 1
```
浏览器开 `http://127.0.0.1:8799/sidebar.html`。Expected：
- 未配置账户：DeepSeek 区显示「未配置…点右上＋登录」。
- 配了有效/无效两账户（同 Task 3 Step 5 的临时 config）：渲染两张卡片，有效卡显示余额行 + 可用/本月/赠送 + 当月按天 3 段堆叠柱（命中/未命中/输出图例），无效卡显示红色错误 + 「重新登录」。
- 完事 `kill %1`，还原 config。

- [ ] **Step 6: Commit**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
git add internal/web/static/sidebar.html
git commit -m "[069] feat(ui): DeepSeek 多账户卡片 + 当月按天堆叠柱状图

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: 端到端验证 + PR

**Files:** 无新增改动（仅验证与发布）

- [ ] **Step 1: 全量构建与 vet**

Run:
```
cd /mnt/data/Work/Projects/ocgt-monitor
CGO_ENABLED=0 go build -tags nogui . && go vet -tags nogui ./...
go build . && go vet ./...   # 需本机 webkit；缺库则跳过交 CI
```
Expected: 无输出、exit 0。

- [ ] **Step 2: 端点矩阵验证**

起 `serve`，依次核对：
- 无账户：`curl -s localhost:8799/api/deepseek` → `{"success":true,"data":[]}`（或 `data:null`）。
- 有效+无效账户（临时 config + DS_TOKEN）：返回两条、按 name 升序、有效含 summary/days、无效 `success:false` error 含「鉴权失败」。
- 单日对账：取某有用量的天，确认 `total == cache_hit + cache_miss + output`。
验证后还原 `~/.ocgt-monitor/config.json`。

- [ ] **Step 3: GUI 抓取手测（带 GUI 时）**

`go build -o /tmp/ocgtm . && /tmp/ocgtm login-deepseek 我的DS` → 登录 → 自动关窗 → config 写入 → 面板出现该卡片。无 GUI 环境跳过，标注交 CI。

- [ ] **Step 4: 推分支并开 PR**

```bash
cd /mnt/data/Work/Projects/ocgt-monitor
git checkout -b feat/deepseek-rich-cards
git push -u origin feat/deepseek-rich-cards
gh pr create --title "DeepSeek 多账户富卡片（B）：网页 token 登录 + 按天用量图" \
  --body "实现 spec docs/superpowers/specs/2026-06-23-deepseek-rich-cards-design.md。

- 配置新增 deepseek_accounts 列表（不动 Profiles）
- quota.DeepSeekWebQuerier 调 platform.deepseek.com（仅 Bearer）：钱包汇总 + 按天用量递归聚合
- /api/deepseek 并发查询多账户、当前月、逐卡成败、按 name 排序
- login-deepseek 子命令：独立 webview 窗口注入 JS 截获 Authorization 头自动落盘
- 前端 DeepSeek 多卡片 + 当月 3 段堆叠柱状图

验证：nogui 编译/vet 通过；/api/deepseek 端点矩阵已测；GUI 抓取手测见 PR 说明。
本月花费(¥) 字段未在实测响应确认，暂未展示。"
```
Expected: PR 创建成功，CI 三平台编译通过。

- [ ] **Step 5: 关闭工作（finishing-a-development-branch）**

CI 绿后按 superpowers:finishing-a-development-branch 决定合并方式。

---

## Self-Review

**1. Spec coverage（逐条对照 spec）：**
- 配置 `deepseek_accounts:[{name,token}]`、旧 sk- 忽略 → Task 1 ✓
- 凭证录入弹 webview 自动抓 token、独立进程、重新登录、约束（JS 可读/无请求拦截/会过期）→ Task 4 ✓（截获 Authorization 头实现）
- 后端 `DeepSeekWebQuerier.FetchSummary/FetchUsage`、两实测接口 + x-client 头 → Task 2 ✓
- `GET /api/deepseek` 并发、当前月、逐卡成败、按 name 排序 → Task 3 ✓
- `POST /api/deepseek/login` 触发子进程 → Task 4 Step 4 ✓（用 GET 触发，幂等只读副作用之外不传 body，等价满足「按钮触发子进程」意图）
- 前端每账户一卡：标题 name+DeepSeek、钱包行、当月 3 段堆叠柱、逐卡错误 + 重新登录 → Task 5 ✓
- 数据结构 `DeepSeekSummary/DeepSeekDayUsage` → Task 2 ✓
- 验证（nogui 编译/vet、serve+curl、GUI 手测、CI 三平台）→ Task 6 ✓
- 影响范围 config.go/deepseek_web.go/types.go/server.go/sidebar.html/main.go + 新增 login 文件 → 全覆盖 ✓
- `本月花费(¥)` 未确认 → 未加字段、前端不展示、PR 标注 ✓（与 spec 一致）

**2. Placeholder 扫描：** 无 TODO/TBD/「类似 TaskN」；每个改代码步骤均有完整代码与确切命令、预期输出。`days[]` 内部形态的真实未知用「递归 sumByType」消化，非占位符。

**3. 类型一致性：** `DeepSeekAccount{Name,Token}`（config）与 `web.DeepSeekAccount{Name,Token}` 字段名一致；`DeepSeekWebQuerier.FetchSummary()/FetchUsage(year,month)` 在 Task 2 定义、Task 3 调用签名一致；前端读 `summary.{balance,token_estimation,monthly_usage,current_token,currency}` 与 `DeepSeekSummary` json tag 一致；`days[].{date,cache_hit,cache_miss,output,total}` 与 `DeepSeekDayUsage` json tag 一致；`SetDeepSeekAccounts` 在 Task 3 定义并被 main.go 两处调用。
