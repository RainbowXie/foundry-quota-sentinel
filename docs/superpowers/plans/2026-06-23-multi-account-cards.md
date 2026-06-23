# OpenCode Go 多账户卡片 Implementation Plan（A）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** GUI 面板同时展示所有 OpenCode Go 账户，每账户一张完整卡片（账号名 + Rolling/Weekly/Monthly 进度条 + 重置时间），逐卡独立成败。

**Architecture:** 新增 `GET /api/accounts` 并发查询所有账户额度返回数组；`main.go` 从 `cfg.Profiles` 构建账户列表传入 `NewServer`；前端把写死单账户的额度区改为按返回动态渲染卡片。配置 schema 不变，DeepSeek 与全局 token 图表不动。

**Tech Stack:** Go（net/http、sync、sort）、原生 JS、embed 的静态 HTML。

**关于验证：** 后端可在本机用 nogui 二进制起 `serve` + 配置隔离（`HOME` 指向临时目录）实测 `/api/accounts`，无需真实凭证（用无效 cookie 验证逐卡报错 + 排序 + JSON 形态）。前端窗口视觉效果由用户确认；编译由 CI 三平台保证。

**参考 spec：** `docs/superpowers/specs/2026-06-23-multi-account-cards-design.md`

---

## File Structure

- `internal/web/server.go` — 新增 `Account` 结构、改 `Server`/`NewServer`、新增 `/api/accounts`、`/api/quota` 改用首个账户。
- `main.go` — 新增 `buildAccounts()`，两处 `web.NewServer(...)` 调用改传账户列表。
- `internal/web/static/sidebar.html` — 新增卡片 CSS、额度区标记改为容器、JS 改为渲染卡片。

---

## Task 1: 后端多账户（server.go + main.go）

**Files:**
- Modify: `internal/web/server.go`
- Modify: `main.go`

> 说明：`NewServer` 签名变更会同时影响 server.go 与 main.go，二者必须一起改完才能编译，故合为一个任务，任务末尾验证编译。

- [ ] **Step 1: 创建并切到 feature 分支**

Run:
```bash
git checkout master && git pull --ff-only origin master 2>/dev/null; git checkout -b feature/multi-account-cards
git branch --show-current
```
Expected: 输出 `feature/multi-account-cards`

- [ ] **Step 2: server.go 增加 `sync` 导入**

把 import 块里的：
```go
	"sort"
```
改为：
```go
	"sort"
	"sync"
```

- [ ] **Step 3: server.go 改 `Account`/`Server`/`NewServer`**

将：
```go
type Server struct {
	addr     string
	querier  *quota.OpenCodeGoQuerier
	deepseek *quota.DeepSeekQuerier
}

func NewServer(q *quota.OpenCodeGoQuerier) *Server {
	return &Server{addr: ":8788", querier: q, deepseek: quota.NewDeepSeekQuerier()}
}
```
替换为：
```go
type Account struct {
	Name        string
	Cookie      string
	WorkspaceID string
}

type Server struct {
	addr     string
	accounts []Account
	deepseek *quota.DeepSeekQuerier
}

func NewServer(accounts []Account) *Server {
	return &Server{addr: ":8788", accounts: accounts, deepseek: quota.NewDeepSeekQuerier()}
}
```

- [ ] **Step 4: server.go 改 `/api/quota` 用首个账户**

将：
```go
	mux.HandleFunc("/api/quota", func(w http.ResponseWriter, r *http.Request) {
		d, e := s.querier.FetchQuota()
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})
```
替换为：
```go
	mux.HandleFunc("/api/quota", func(w http.ResponseWriter, r *http.Request) {
		if len(s.accounts) == 0 {
			writeJSON(w, 200, map[string]any{"success": false, "error": "no account configured"})
			return
		}
		a := s.accounts[0]
		q := &quota.OpenCodeGoQuerier{Cookie: a.Cookie, WorkspaceID: a.WorkspaceID}
		d, e := q.FetchQuota()
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		type result struct {
			Name    string           `json:"name"`
			Success bool             `json:"success"`
			Quota   *quota.QuotaData `json:"quota,omitempty"`
			Error   string           `json:"error,omitempty"`
		}
		results := make([]result, len(s.accounts))
		var wg sync.WaitGroup
		for i, a := range s.accounts {
			wg.Add(1)
			go func(i int, a Account) {
				defer wg.Done()
				q := &quota.OpenCodeGoQuerier{Cookie: a.Cookie, WorkspaceID: a.WorkspaceID}
				d, e := q.FetchQuota()
				if e != nil {
					results[i] = result{Name: a.Name, Success: false, Error: e.Error()}
				} else {
					results[i] = result{Name: a.Name, Success: true, Quota: d}
				}
			}(i, a)
		}
		wg.Wait()
		sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": results})
	})
```

- [ ] **Step 5: main.go 新增 `buildAccounts()`**

将：
```go
func main() {
```
替换为：
```go
func buildAccounts() []web.Account {
	var accs []web.Account
	for name, p := range cfg.Profiles {
		if p.Cookie != "" && p.WorkspaceID != "" {
			accs = append(accs, web.Account{Name: name, Cookie: p.Cookie, WorkspaceID: p.WorkspaceID})
		}
	}
	if len(accs) == 0 {
		c, wks := os.Getenv("OPENCODE_GO_AUTH_COOKIE"), os.Getenv("OPENCODE_GO_WORKSPACE_ID")
		if c != "" && wks != "" {
			accs = append(accs, web.Account{Name: "默认", Cookie: c, WorkspaceID: wks})
		}
	}
	sort.Slice(accs, func(i, j int) bool { return accs[i].Name < accs[j].Name })
	return accs
}

func main() {
```

- [ ] **Step 6: main.go 改 `startSidebar` 的 NewServer 调用**

将：
```go
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
	go func() {
		if err := srv.Start(":" + ocgtPort()); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	}()
	time.Sleep(500 * time.Millisecond)
```
替换为：
```go
	srv := web.NewServer(buildAccounts())
	go func() {
		if err := srv.Start(":" + ocgtPort()); err != nil {
			fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	}()
	time.Sleep(500 * time.Millisecond)
```

- [ ] **Step 7: main.go 改 `cmdServe` 的 NewServer 调用**

将：
```go
	q := makeQuotaQuerier()
	srv := web.NewServer(q)
	go func() {
		if err := srv.Start(":" + ocgtPort()); err != nil { fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err); os.Exit(1) }
	}()
	fmt.Println("API 服务已启动: http://127.0.0.1:8788")
```
替换为：
```go
	srv := web.NewServer(buildAccounts())
	go func() {
		if err := srv.Start(":" + ocgtPort()); err != nil { fmt.Fprintf(os.Stderr, "服务器启动失败: %v\n", err); os.Exit(1) }
	}()
	fmt.Println("API 服务已启动: http://127.0.0.1:8788")
```

- [ ] **Step 8: 编译验证**

Run: `CGO_ENABLED=0 go build -tags nogui -o /tmp/ocgt-nogui . && echo OK`
Expected: `OK`（main.go 与 server.go 一致，编译通过）

- [ ] **Step 9: go vet**

Run: `CGO_ENABLED=0 go vet -tags nogui ./...`
Expected: 无输出

- [ ] **Step 10: Commit**

```bash
git add internal/web/server.go main.go
git commit -m "[064] 多账户后端：/api/accounts 并发查询所有账户额度"
```

---

## Task 2: 前端卡片化（sidebar.html）

**Files:**
- Modify: `internal/web/static/sidebar.html`

- [ ] **Step 1: 新增卡片 CSS**

将这一行：
```
.tl{font-size:8px;color:var(--text-sec);font-weight:600;letter-spacing:.4px;text-transform:uppercase}
```
替换为：
```
.tl{font-size:8px;color:var(--text-sec);font-weight:600;letter-spacing:.4px;text-transform:uppercase}
.acard{background:var(--card);border:var(--card-bd);border-radius:10px;padding:8px 10px;margin-bottom:8px;box-shadow:var(--card-sh)}
.acard-h{display:flex;align-items:center;gap:6px;margin-bottom:6px}
.acard-dot{width:6px;height:6px;border-radius:50%;background:linear-gradient(135deg,var(--accent),var(--accent2));flex-shrink:0}
.acard-name{font-size:11px;font-weight:700;color:var(--text)}
.qerr{font-size:11px;color:var(--red);padding:4px 0;text-align:center}
```

- [ ] **Step 2: 额度区标记改为卡片容器**

将：
```
<div class=qn id=quotaLoad>
<div class=qr><span class=ql>Rolling</span><div class=qbw><div class="qf g1" id=qfR style=width:0%></div></div><span class="qp g1" id=qpR>--</span><span class=qtm id=qtR></span></div>
<div class=qr><span class=ql>Weekly</span><div class=qbw><div class="qf g2" id=qfW style=width:0%></div></div><span class="qp g2" id=qpW>--</span><span class=qtm id=qtW></span></div>
<div class=qr id=qrM><span class=ql>Monthly</span><div class=qbw id=qbwM><div class="qf g3" id=qfM style=width:0%></div></div><span class="qp g3" id=qpM>--</span><span class=qtm id=qtM></span></div>
<div class="hide" id=quotaErr style="font-size:11px;color:var(--red);padding:4px 0;text-align:center"></div>
</div>
```
替换为：
```
<div class=qn id=accountCards><div class=emp>加载中...</div></div>
```

- [ ] **Step 3: JS 改为渲染卡片**

将（`bg()` 之后的 `fq()`/`sq()` 整段）：
```
async function fq(){try{
var r=await(await fetch('/api/quota')).json();if(!r.success)throw new Error(r.error);
var d=r.data;
function sq(id,pct,tid,rid,rt,cl){
var b=document.getElementById(id);if(b){b.style.width=pct+'%';var w=bg(pct);b.className='qf';if(w)b.classList.add('qf',w);else b.classList.add('qf',cl)}
var p=document.getElementById(tid);if(p){p.textContent=pct+'%';var c=bg(pct);p.className='qp';if(c)p.classList.add('qp',c);else p.classList.add('qp',cl)}
var r=document.getElementById(rid);if(r)r.textContent=rt}
sq('qfR',d.rolling.usage_percent,'qpR','qtR',d.rolling.reset_display,'g1');
sq('qfW',d.weekly.usage_percent,'qpW','qtW',d.weekly.reset_display,'g2');
if(d.monthly){var _m=document.getElementById('qbwM');if(_m)_m.style.display='block';sq('qfM',d.monthly.usage_percent,'qpM','qtM',d.monthly.reset_display,'g3')}
else{var _m=document.getElementById('qbwM');if(_m)_m.style.display='none';var pm=document.getElementById('qpM');if(pm){pm.textContent='∞';pm.className='qp g3'}var tm=document.getElementById('qtM');if(tm)tm.textContent='无限'}
var _e=document.getElementById('quotaErr');if(_e)_e.classList.add('hide')
}catch(e){var _e=document.getElementById('quotaErr');if(_e){_e.textContent=e.message;_e.classList.remove('hide')}}}
```
替换为：
```
function qesc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function qrow(label,u,cl){
if(!u)return '<div class=qr><span class=ql>'+label+'</span><div class=qbw><div class="qf '+cl+'" style="width:0%"></div></div><span class="qp '+cl+'">∞</span><span class=qtm>无限</span></div>';
var pct=u.usage_percent,w=bg(pct),bc=w||cl;
return '<div class=qr><span class=ql>'+label+'</span><div class=qbw><div class="qf '+bc+'" style="width:'+pct+'%"></div></div><span class="qp '+bc+'">'+pct+'%</span><span class=qtm>'+(u.reset_display||'')+'</span></div>';
}
function acard(a){
var body;
if(!a.success){body='<div class=qerr>'+qesc(a.error||'查询失败')+'</div>';}
else{var d=a.quota;body=qrow('Rolling',d.rolling,'g1')+qrow('Weekly',d.weekly,'g2')+qrow('Monthly',d.monthly,'g3');}
return '<div class=acard><div class=acard-h><span class=acard-dot></span><span class=acard-name>'+qesc(a.name)+'</span></div>'+body+'</div>';
}
async function fq(){try{
var r=await(await fetch('/api/accounts')).json();if(!r.success)throw new Error(r.error||'failed');
var h='';if(!r.data||!r.data.length){h='<div class=emp>未配置账户</div>';}else{r.data.forEach(function(a){h+=acard(a)});}
document.getElementById('accountCards').innerHTML=h;
}catch(e){document.getElementById('accountCards').innerHTML='<div class=qerr>'+qesc(e.message)+'</div>';}}
```

> 入口函数仍叫 `fq()`，故 `fa()` 里的 `Promise.all([fq(),...])` 无需改动。

- [ ] **Step 4: 编译验证（确认 HTML 仍能 embed）**

Run: `CGO_ENABLED=0 go build -tags nogui -o /tmp/ocgt-nogui . && echo OK`
Expected: `OK`

- [ ] **Step 5: 确认服务出的页面含新容器**

Run:
```bash
HOME=/tmp/ocgttest OCGT_PORT=8799 /tmp/ocgt-nogui serve >/tmp/srv.log 2>&1 &
SRV=$!; sleep 1
curl -s localhost:8799/sidebar.html | grep -c 'id=accountCards'
kill $SRV 2>/dev/null
```
Expected: `1`（页面包含新的卡片容器）；若目录不存在先 `mkdir -p /tmp/ocgttest`

- [ ] **Step 6: Commit**

```bash
git add internal/web/static/sidebar.html
git commit -m "[065] 多账户前端：额度区卡片化，逐卡独立渲染与错误"
```

---

## Task 3: 端到端验证并推送 PR

**Files:** 无（验证 + 推送）

- [ ] **Step 1: 用隔离配置造两个账户实测 `/api/accounts`**

Run:
```bash
mkdir -p /tmp/ocgttest/.ocgt-monitor
cat > /tmp/ocgttest/.ocgt-monitor/config.json <<'JSON'
{"active_profile":"alpha","profiles":{"alpha":{"cookie":"bad","workspace_id":"wrk_bad"},"beta":{"cookie":"bad2","workspace_id":"wrk_bad2"}}}
JSON
HOME=/tmp/ocgttest OCGT_PORT=8799 /tmp/ocgt-nogui serve >/tmp/srv.log 2>&1 &
SRV=$!; sleep 1
curl -s localhost:8799/api/accounts | python3 -m json.tool
kill $SRV 2>/dev/null
```
Expected: `data` 为含两条的数组，`name` 依次为 `alpha`、`beta`（已排序）；两条均 `success:false` 且带 `error`（无效 cookie 导致 FetchQuota 失败）。证明端点并发、排序、JSON 形态、逐账户独立成败均正确。

- [ ] **Step 2: 推送分支并开 PR**

Run:
```bash
git push -u origin feature/multi-account-cards
gh pr create --repo RainbowXie/ocgt-monitor --base master --head RainbowXie:feature/multi-account-cards \
  --title "多账户卡片(A)：OpenCode Go 多账户同时展示" \
  --body "GUI 同时展示所有 OpenCode Go 账户，每账户一张卡片(账号名+Rolling/Weekly/Monthly)，逐卡独立成败。新增 /api/accounts 并发查询。配置 schema 不变，DeepSeek 与 token 图表不动。设计见 docs/superpowers/specs/2026-06-23-multi-account-cards-design.md"
```
Expected: 输出 PR URL

- [ ] **Step 3: 监视 CI 三平台**

Run:
```bash
sleep 8
RID=$(gh run list --repo RainbowXie/ocgt-monitor --branch feature/multi-account-cards --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RID" --repo RainbowXie/ocgt-monitor --exit-status >/dev/null 2>&1; echo "exit: $?"
gh run view "$RID" --repo RainbowXie/ocgt-monitor --json conclusion,jobs --jq '{conclusion, jobs:[.jobs[]|{name,conclusion}]}'
```
Expected: 三个 `build (...)` 均 success。

> 合并到 master 与发版（用户视觉确认卡片效果后）由协调者与用户确认后进行，不在本计划自动执行。

---

## Self-Review 记录

- **Spec coverage**：`/api/accounts`（Task1 Step4）、并发+排序+逐卡成败（Task1 Step4）、`NewServer` 改签名+账户来源+env 回退（Task1 Step3/5）、`/api/quota` 保留（Task1 Step4）、前端卡片化+逐卡错误（Task2）、DeepSeek/token 图表不动（未触碰）、配置不改（未触碰）—— 均覆盖。
- **类型/字段一致**：后端返回 `{name, success, quota, error}`；前端 `acard(a)` 读 `a.name/a.success/a.quota/a.error`，`qrow` 读 `u.usage_percent/u.reset_display`，与 `QuotaData` 的 `rolling/weekly/monthly` 及 `QuotaUsage` 的 json tag 一致；monthly 可为 null → `qrow` 的 `!u` 分支处理。
- **签名一致**：`web.Account{Name,Cookie,WorkspaceID}` 在 server.go 定义、main.go 构造一致；`NewServer([]Account)` 两处调用一致。
- **无占位符**：所有步骤含完整代码与确切命令、预期输出。
