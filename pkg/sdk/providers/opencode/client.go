package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"
)

const (
	openCodeConsoleAPIURL = "https://opencode.ai/console/api"
	// openCodeGoMaxResponseSize 限制配额响应体积上限为 1MB。
	// 读取 maxSize+1 字节以严格检测超出边界的超大恶意或异常响应，避免被静默截断造成解析不完整。
	openCodeGoMaxResponseSize = 1 << 20
	openCodeGoRequestTimeout  = 15 * time.Second
)

// openCodeOrgIDRe 白名单校验 Console 返回的组织/工作区标识：org_ 或 wrk_ 前缀加字母数字主体。
// 该值会被写进 x-org-id 请求头、持久化到配置并拼进账户页 URL，因此每个使用点都必须先过同一判定。
var openCodeOrgIDRe = regexp.MustCompile(`^(?:org|wrk)_[a-zA-Z0-9]+$`)

// ValidWorkspaceID 判定标识是否为受支持的 Console 工作区/组织形式。
// 请求头白名单与账户页 URL 拼接共用它，避免两处规则各自演进后出现白名单缺口。
func ValidWorkspaceID(id string) bool {
	return openCodeOrgIDRe.MatchString(id)
}

// OpenCodeQuerier 负责与 opencode.ai 官方后端服务通信并获取原生配额数据。
type OpenCodeQuerier struct {
	Cookie      string
	WorkspaceID string
	// Client 允许注入自定义 http.Client（测试或特殊代理场景），为 nil 时采用默认超时客户端。
	Client *http.Client
}

// NewOpenCodeQuerier 从环境变量中读取凭据并创建 OpenCodeQuerier 实例。
func NewOpenCodeQuerier() *OpenCodeQuerier {
	return &OpenCodeQuerier{
		Cookie:      os.Getenv("OPENCODE_GO_AUTH_COOKIE"),
		WorkspaceID: os.Getenv("OPENCODE_GO_WORKSPACE_ID"),
	}
}

// FetchQuota 请求 Console v2 的 Go 订阅状态接口并解析出 OpenCode 原生 QuotaData。
// OpenCode 已将旧版 /_server Seroval RPC 替换为 Console REST API，旧接口会 302 到鉴权页，
// 因此这里必须走 /console/api/go/status 并用 x-org-id 指定工作区。
func (q *OpenCodeQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	body, err := openCodeConsoleGET(q.Cookie, q.WorkspaceID, "/go/status", q.Client)
	if err != nil {
		return nil, err
	}
	return ParseQuotaResponse(string(body))
}

// FetchOrganizations 用已捕获的会话 Cookie 查询 Console 组织列表，返回所有合法的组织元信息。
func FetchOrganizations(cookie string, client *http.Client) ([]Organization, error) {
	if cookie == "" {
		return nil, fmt.Errorf("OpenCode 登录状态无效")
	}
	body, err := openCodeConsoleGET(cookie, "", "/orgs", client)
	if err != nil {
		return nil, err
	}
	var orgs []Organization
	if err := json.Unmarshal(body, &orgs); err != nil {
		return nil, fmt.Errorf("parse orgs response: %w", err)
	}
	validOrgs := make([]Organization, 0, len(orgs))
	for _, org := range orgs {
		if org.ID == "" {
			continue
		}
		if !ValidWorkspaceID(org.ID) {
			return nil, fmt.Errorf("orgs response contains a malformed org id")
		}
		validOrgs = append(validOrgs, org)
	}
	if len(validOrgs) == 0 {
		return nil, fmt.Errorf("orgs response contains no org id")
	}
	return validOrgs, nil
}

// FetchDefaultOrgID 用已捕获的会话 Cookie 查询 Console 组织列表，返回首个非空组织 ID。
// 新版登录页落地在 /console/login?next=/console/go，前端路由可能停留在 /console 或 /console/go
// 而不带 org/wrk 路径段，此时该接口是唯一可确定工作区标识的来源。
func FetchDefaultOrgID(cookie string, client *http.Client) (string, error) {
	orgs, err := FetchOrganizations(cookie, client)
	if err != nil {
		return "", err
	}
	return orgs[0].ID, nil
}

// FetchAllSubscribedQuotas 并发拉取当前账号所有合法组织的工作空间配额，滤除未开通 Go 订阅的组织。
func FetchAllSubscribedQuotas(cookie string, client *http.Client) ([]WorkspaceQuota, error) {
	orgs, err := FetchOrganizations(cookie, client)
	if err != nil {
		return nil, err
	}
	type fetchRes struct {
		wq  WorkspaceQuota
		ok  bool
		err error
	}
	resCh := make(chan fetchRes, len(orgs))
	for _, org := range orgs {
		go func(o Organization) {
			q := &OpenCodeQuerier{Cookie: cookie, WorkspaceID: o.ID, Client: client}
			d, err := q.FetchQuota()
			if err != nil {
				if errors.Is(err, ErrNoGoSubscription) {
					resCh <- fetchRes{ok: false}
					return
				}
				resCh <- fetchRes{err: err}
				return
			}
			resCh <- fetchRes{wq: WorkspaceQuota{Organization: o, Quota: d}, ok: true}
		}(org)
	}
	var results []WorkspaceQuota
	var firstErr error
	for i := 0; i < len(orgs); i++ {
		r := <-resCh
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		if r.ok {
			results = append(results, r.wq)
		}
	}
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// openCodeConsoleGET 发起 Console API GET 请求；orgID 为空时不带 x-org-id 头，
// 因为 /orgs 接口不接受该头，而 /go/status 必须携带它来选定工作区。
func openCodeConsoleGET(cookie, orgID, path string, client *http.Client) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, openCodeConsoleAPIURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	if orgID != "" {
		req.Header.Set("x-org-id", orgID)
	}
	if client == nil {
		client = &http.Client{Timeout: openCodeGoRequestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 诊断信息严禁输出 body，防止泄露私人账号敏感凭据。
		return nil, fmt.Errorf("opencode API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, openCodeGoMaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > openCodeGoMaxResponseSize {
		return nil, fmt.Errorf("opencode response exceeds %d bytes", openCodeGoMaxResponseSize)
	}
	return body, nil
}

func (q *OpenCodeQuerier) validate() error {
	if q.Cookie == "" {
		return fmt.Errorf("OPENCODE_GO_AUTH_COOKIE not set")
	}
	if q.WorkspaceID == "" {
		return fmt.Errorf("OPENCODE_GO_WORKSPACE_ID not set")
	}
	// 配置/环境变量里的 ID 同样要走白名单：它会被写进 x-org-id 并拼进账户页 URL，
	// 不能只校验 /console/api/orgs 的响应，否则手工粘贴的 URL 会被原样发往上游。
	if !ValidWorkspaceID(q.WorkspaceID) {
		return fmt.Errorf("OPENCODE_GO_WORKSPACE_ID 格式非法：需要 org_ 或 wrk_ 前缀加字母数字")
	}
	return nil
}
