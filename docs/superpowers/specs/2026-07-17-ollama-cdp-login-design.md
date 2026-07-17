# Ollama Chromium CDP 登录设计

## 目标

将 Ollama 登录从 Linux WebKitGTK 的 Cookie 捕获改为一次性系统 Chromium 浏览器登录。用户在可见的 Chrome、Chromium 或 Edge 窗口完成 Ollama 登录；应用通过 Chrome DevTools Protocol（CDP）读取仅限 `ollama.com` 的 `__Secure-session` HttpOnly Cookie，验证其能请求 `/settings` 后立即退出浏览器。后续额度刷新继续使用已有的 `OllamaQuerier` 解析 `/settings`。

本设计不需要常驻 WebKit、浏览器扩展、读取用户日常浏览器 profile，或让用户复制 Cookie。

## 已确认的根因

Ollama 登录完成后 WebKit 内部有有效会话，Settings 页面可显示额度；但 WebKitGTK 的 Cookie Manager、持久 cookie store、页面 JavaScript、资源请求和响应头 API 都不会向宿主导出该 HttpOnly Cookie。现有多个捕获 hook 均得到空值，因此问题是 WebKit 的权限模型，不是回调 URL、时机或解析问题。

Chromium 的 CDP `Network.getCookies` 是浏览器调试协议的特权接口，能够读取 HttpOnly Cookie。它只在本应用启动的、绑定回环地址的临时浏览器实例中使用。

## 方案比较

1. **临时 Chromium + CDP（采用）**：不依赖 WebKit；无需安装扩展；登录后浏览器立即退出。要求系统有 Chrome、Chromium 或 Edge。
2. **浏览器扩展**：能从用户已有浏览器读取 Cookie，但要求安装/维护扩展，并需要 `cookies` 权限和浏览器商店分发。
3. **读取浏览器 Cookie 数据库**：Firefox 可相对直接读取；Chromium Cookie 依赖桌面密钥环解密，且会触及用户日常 profile，兼容性和隐私边界都较差。

## 交互与数据流

1. 用户点击 Ollama 的添加或重新登录操作。
2. 应用按 Chrome、Chromium、Edge 的顺序寻找系统可执行文件。
3. 找到后，以新的临时 profile 启动浏览器，并传入 `--remote-debugging-address=127.0.0.1`、`--remote-debugging-port=0` 和 Ollama Settings URL。
4. Settings 会将未认证用户带到 Ollama/WorkOS 登录页。用户在此窗口完成密码、MFA、CAPTCHA 等正常登录步骤。
5. 应用轮询临时 profile 中的 DevTools 端点，并通过 CDP 对 `https://ollama.com` 调用 `Network.getCookies`。
6. 应用仅接受名称为 `__Secure-session`、域名属于 Ollama 的 Cookie；把它作为 `Cookie` 请求头交给现有验证函数。
7. 验证成功后，保存既有 `OllamaAccount.Cookie` 值，关闭浏览器、等待进程退出并删除临时 profile。验证失败不保存账号。
8. 用户关闭登录窗口、浏览器退出或超时则显示明确错误，不新建空账号卡片。

打开账户页时，应用同样启动短生命周期的临时 Chromium，使用 CDP 将已保存 Cookie 注入临时 profile 后导航到 Settings。该窗口由用户关闭；不是后台常驻会话。

## 组件边界

`internal/sidebar/ollama_browser.go`

- 发现 Chrome、Chromium 和 Edge 的可执行文件。
- 启动/停止临时 profile，读取 `DevToolsActivePort`。
- 不知道 Ollama 的配额或配置格式。

`internal/sidebar/ollama_cdp.go`

- 通过 CDP 建立回环 WebSocket 连接。
- 提供获取、筛选和注入 Ollama Cookie 的最小操作。
- 不记录 Cookie 的值，也不访问非 Ollama 域名。

`internal/sidebar/login_ollama.go`

- 保持 `RunOllamaLogin(validate func(string) bool)` 与 `RunOllamaPage(pageURL, cookie string)` 调用边界。
- 协调浏览器生命周期、Cookie 验证和错误转换。

现有 `internal/quota/ollama.go`、配置持久化、Web API 和卡片 UI 不改变其语义。

## 浏览器与错误处理

- 依次探测 `google-chrome`、`google-chrome-stable`、`chromium`、`chromium-browser`、`microsoft-edge`、`microsoft-edge-stable`。
- 没有可用浏览器时提示安装 Chrome、Chromium 或 Edge；不会回退到无法读取 Cookie 的 WebKit 实现。
- CDP 仅监听 `127.0.0.1`，临时 profile 目录权限为私有，并在退出后删除。
- 登录流程有有限超时；用户可直接关闭浏览器取消。
- Cookie 仅在内存、短生命周期浏览器 profile 和既有 Ollama 配置存储中出现；日志只能记录 Cookie 名称和长度，不能记录值。
- 生产登录无法由自动化测试提供真实账号凭据，因此自动化测试覆盖协议与生命周期；最终由用户在真实 Ollama 登录流程中验证。

## 测试策略

先写失败测试，再实现：

1. 浏览器发现按 Chrome、Chromium、Edge 优先级选择；无浏览器时返回可操作错误。
2. CDP Cookie 筛选只返回 `ollama.com` 的 `__Secure-session`，拒绝其它域名、名称和空值。
3. 协调器在验证成功时关闭浏览器并清理 profile；验证失败或用户关闭时不返回 Cookie。
4. 已保存 Cookie 的账户页流程会先注入 Cookie，再导航到指定 Ollama URL。
5. 保持 `go test -tags nogui ./...` 通过，并在 Linux GUI 环境编译后进行一次真实登录验证。

## 非目标

- 不读取或解密用户默认 Chrome/Edge/Firefox profile。
- 不实现浏览器扩展或 native messaging。
- 不尝试把 Ollama API key 或 `ollama signin` 设备密钥伪装成 Settings Cookie；它们没有公开的 Session/Weekly 额度接口。
- 不改变现有 `/settings` HTML 解析和额度卡片展示。
