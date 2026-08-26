package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/formatter"
)

const (
	openCodeGoBaseURL   = "https://opencode.ai"
	openCodeGoServiceID = "c7389bd0e731f80f49593e5ee53835475f4e28594dd6bd83eb229bab753498cd"
	// openCodeGoMaxResponseSize bounds the quota response body. The reader
	// reads maxSize+1 bytes so a response that exactly fits is accepted and
	// one that exceeds the bound is rejected as oversized (never silently
	// truncated into a partial quota result).
	openCodeGoMaxResponseSize = 1 << 20
	openCodeGoRequestTimeout  = 15 * time.Second
)

type OpenCodeGoQuerier struct {
	Cookie      string
	WorkspaceID string
	// Client is injectable for tests; nil constructs a default client with
	// openCodeGoRequestTimeout. The request URL is ALWAYS the fixed
	// openCodeGoBaseURL host — there is no BaseURL override seam, so the
	// cookie can never be sent to an unvalidated host.
	Client *http.Client
}

func NewOpenCodeGoQuerier() *OpenCodeGoQuerier {
	return &OpenCodeGoQuerier{Cookie: os.Getenv("OPENCODE_GO_AUTH_COOKIE"), WorkspaceID: os.Getenv("OPENCODE_GO_WORKSPACE_ID")}
}

func (q *OpenCodeGoQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	args := buildRPCArgs(q.WorkspaceID)
	reqURL := fmt.Sprintf("%s/_server?id=%s&args=%s", openCodeGoBaseURL, openCodeGoServiceID, url.QueryEscape(args))
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("cookie", q.Cookie)
	req.Header.Set("x-server-id", openCodeGoServiceID)
	req.Header.Set("x-server-instance", "server-fn:3")
	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: openCodeGoRequestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Diagnostics carry ONLY the status code — never the upstream
		// response body, which may contain private/account material.
		return nil, fmt.Errorf("opencode API returned HTTP %d", resp.StatusCode)
	}
	// Read maxSize+1 bytes and propagate any transport/read error: a
	// connection that breaks mid-body (e.g. after rolling/weekly but before
	// an optional monthly) must NOT be mistaken for a complete response with
	// monthly absent. A body exceeding the bound is rejected as oversized
	// rather than silently truncated.
	body, err := io.ReadAll(io.LimitReader(resp.Body, openCodeGoMaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > openCodeGoMaxResponseSize {
		return nil, fmt.Errorf("opencode quota response exceeds %d bytes", openCodeGoMaxResponseSize)
	}
	return parseQuotaResponse(string(body))
}

func (q *OpenCodeGoQuerier) validate() error {
	if q.Cookie == "" {
		return fmt.Errorf("OPENCODE_GO_AUTH_COOKIE not set")
	}
	if q.WorkspaceID == "" {
		return fmt.Errorf("OPENCODE_GO_WORKSPACE_ID not set")
	}
	return nil
}

func buildRPCArgs(workspaceID string) string {
	data, _ := json.Marshal(map[string]any{
		"t": map[string]any{"t": 9, "i": 0, "l": 1, "a": []any{map[string]any{"t": 1, "s": workspaceID}}, "o": 0},
		"f": 31, "m": []any{},
	})
	return string(data)
}

func parseQuotaResponse(text string) (*QuotaData, error) {
	// 结构化解析（非整对象正则）：先按字段边界定位每个 usage 对象的
	// 起始位置，跳过可选的 seroval 引用赋值 $R[n]=，再以引号/花括号
	// 感知的方式取出完整对象；随后在对象内部独立解析每个必需字段，
	// 因此引用编号漂移、字段重排、空白和附加属性都不会破坏解析。
	rolling, err := extractUsageWindow(text, "rollingUsage", false)
	if err != nil {
		return nil, err
	}
	weekly, err := extractUsageWindow(text, "weeklyUsage", false)
	if err != nil {
		return nil, err
	}
	if rolling == nil && weekly == nil {
		// 订阅失效/无配额（OBSERVED 2026-08-19：opencode 配额 RPC
		// server-fn:3 对失效订阅返回 null——seroval 占位结构 +
		// null 值，整个响应无任何 usage 对象）。这是合法状态：卡片
		// 显式标记为失效，而非报错让整卡消失。空串/无内容的畸形
		// 响应不在此列——它没有任何 seroval 占位结构，直接报错。
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("failed to parse rollingUsage")
		}
		// 单窗口缺失（一个在、一个不在）仍视为数据畸形，继续走
		// 下面的 required 校验路径报错。
		if !strings.Contains(text, "null") {
			// 非 null 哨兵且无任何窗口：不是已知的失效形态（OBSERVED 的
			// 失效响应是配额 RPC 返回 null），保守地按畸形处理而非猜测。
			return nil, fmt.Errorf("failed to parse rollingUsage")
		}
		return &QuotaData{
			Rolling: QuotaUsage{Status: "unavailable", ResetDisplay: formatter.FormatDurationCompact(0)},
			Weekly:  QuotaUsage{Status: "unavailable", ResetDisplay: formatter.FormatDurationCompact(0)},
			Lapsed:  true,
			FetchedAt: time.Now(),
		}, nil
	}
	if rolling == nil {
		return nil, fmt.Errorf("failed to parse rollingUsage")
	}
	if weekly == nil {
		return nil, fmt.Errorf("failed to parse weeklyUsage")
	}
	monthlyRaw, err := extractUsageWindow(text, "monthlyUsage", false)
	if err != nil {
		return nil, err
	}
	var monthly *QuotaUsage
	if monthlyRaw != nil && monthlyRaw.Status != "unlimited" {
		monthly = monthlyRaw
	}
	return &QuotaData{Rolling: *rolling, Weekly: *weekly, Monthly: monthly, FetchedAt: time.Now()}, nil
}

// extractUsageWindow locates every boundary-anchored occurrence of the given
// usage field name, extracts the bounded object after it, and validates the
// object. It distinguishes three states:
//   - absent:           no boundary-anchored occurrence (required windows
//     error; optional monthly returns nil)
//   - present-valid:    exactly one extractable, parseable object
//   - present-malformed:an occurrence whose value cannot be extracted or
//     parsed (truncated object, malformed reference
//     assignment, non-object value, or more than one
//     occurrence) — ALWAYS a window-specific error, even
//     for the optional monthly window
//
// required=true rejects absence; a present-but-malformed window always
// errors so a truncated response never yields a partial quota result.
// Duplicate detection happens inside the single-pass scan
// (findAllUsageObjects): the second real occurrence is reported
// immediately, so this function only ever sees 0 or 1 object.
func extractUsageWindow(text, windowName string, required bool) (*QuotaUsage, error) {
	objects, err := findAllUsageObjects(text, windowName)
	if err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		if required {
			return nil, fmt.Errorf("failed to parse %s", windowName)
		}
		return nil, nil
	}
	usage, err := parseUsageObject(objects[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", windowName, err)
	}
	return &usage, nil
}

// findAllUsageObjects returns the raw bounded object text for every
// boundary-anchored occurrence of windowName, skipping an optional seroval
// reference assignment ($R[n]=) between the field name and the object.
//
// It is a SINGLE-PASS lexer scan (persistent inString/escaped cursor, no
// per-occurrence rescan), so pathological inputs with many window-name
// occurrences stay O(n) instead of degrading to O(n²). The second real
// occurrence is reported as a duplicate error immediately; a
// present-but-malformed occurrence (truncated input, a malformed reference
// assignment, or a non-object value) returns a window-specific error, so an
// optional window is never mistaken for absent when it is actually present
// but broken.
func findAllUsageObjects(text, windowName string) ([]string, error) {
	var out []string
	inString := false
	escaped := false
	for i := 0; i < len(text); {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			i++
			continue
		}
		// Only a boundary-anchored name OUTSIDE a string can be a quota
		// field: start of text, '{', ',', or whitespace before it, so names
		// embedded inside other identifiers or string values are skipped.
		prev := byte(0)
		if i > 0 {
			prev = text[i-1]
		}
		atBoundary := i == 0 || prev == '{' || prev == ',' || prev == ' ' || prev == '\t' || prev == '\n' || prev == '\r'
		if !atBoundary || !strings.HasPrefix(text[i:], windowName) {
			i++
			continue
		}
		j := skipSpace(text, i+len(windowName))
		if j >= len(text) || text[j] != ':' {
			// A boundary-anchored name NOT followed by ':' is a different
			// field whose name merely starts with windowName (e.g.
			// monthlyUsageExtra) — not an occurrence of the quota window.
			i += len(windowName)
			continue
		}
		if len(out) > 0 {
			// Second real occurrence: duplicate. Reported immediately so
			// the scan never has to keep collecting.
			return nil, fmt.Errorf("failed to parse %s: duplicate %s object", windowName, windowName)
		}
		j = skipSpace(text, j+1)
		next, matched := matchRefAssignment(text, j)
		if matched {
			j = skipSpace(text, next)
		} else if j < len(text) && text[j] == '$' {
			// A reference token that is not a well-formed $R[n]= assignment
			// is a malformed value, not a missing window.
			return nil, fmt.Errorf("failed to parse %s: malformed reference assignment", windowName)
		}
		if j >= len(text) || text[j] != '{' {
			return nil, fmt.Errorf("failed to parse %s: expected object value", windowName)
		}
		raw, ok := extractBoundedObject(text, j)
		if !ok {
			return nil, fmt.Errorf("failed to parse %s: truncated object", windowName)
		}
		out = append(out, raw)
		i = j + len(raw)
	}
	return out, nil
}

// matchRefAssignment matches an optional seroval reference assignment
// $R[n]= (with optional whitespace around '=') starting at i. It returns
// the index just past the '=' and matched=true on success, or (i, false)
// when there is no assignment (including a malformed '$'-prefixed token
// the caller must then reject).
func matchRefAssignment(text string, i int) (int, bool) {
	j := i
	if j+1 >= len(text) || text[j] != '$' || text[j+1] != 'R' {
		return i, false
	}
	j += 2
	if j >= len(text) || text[j] != '[' {
		return i, false
	}
	j++
	digits := 0
	for j < len(text) && text[j] >= '0' && text[j] <= '9' {
		digits++
		j++
	}
	if digits == 0 || j >= len(text) || text[j] != ']' {
		return i, false
	}
	j = skipSpace(text, j+1)
	if j >= len(text) || text[j] != '=' {
		return i, false
	}
	return skipSpace(text, j+1), true
}

// extractBoundedObject returns the text of the object starting at the '{'
// at index open, respecting quoted strings and nested braces so a '}' or
// '{' inside a string value or a nested object cannot truncate it.
func extractBoundedObject(text string, open int) (string, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := open; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open : i+1], true
			}
		}
	}
	return "", false
}

// status 枚举不参与解析决策（design D1）：上游除 ok/unlimited 外还有
// 额度耗尽态，且未来可能继续演进。只要结构合法且非空，任意 status
// 值都透传到 QuotaUsage.Status（前端只读 usage_percent/reset_in_sec，
// 唯一消费 status 的 monthly-unlimited 是精确字符串判断，与枚举无关）。

// parseUsageObject parses one raw seroval object body. Every present
// object must contain exactly one non-empty status, resetInSec, and
// usagePercent; fields are parsed independently of order and whitespace,
// unknown properties are ignored, and missing/duplicate/negative/
// non-numeric/empty/truncated values fail closed. The status VALUE is
// accepted regardless of its enumerated content.
func parseUsageObject(raw string) (QuotaUsage, error) {
	body := strings.TrimSpace(raw)
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return QuotaUsage{}, fmt.Errorf("malformed object")
	}
		var (
			status      string
			hasStatus   bool
			resetInSec  int
			hasReset    bool
			usagePct    float64
			hasUsagePct bool
		)
	for _, field := range splitTopLevelFields(body[1 : len(body)-1]) {
		name, value := splitTopLevelKV(field)
		switch name {
		case "status":
			if hasStatus {
				return QuotaUsage{}, fmt.Errorf("duplicate status")
			}
			s, ok := parseSerovalString(value)
			// 非空字符串约束：既拒绝非字符串形态，也防缺失语义被空值绕过
			// （design D1）。具体枚举值一律放行。
			if !ok || s == "" {
				return QuotaUsage{}, fmt.Errorf("unsupported status value")
			}
			status, hasStatus = s, true
		case "resetInSec":
			if hasReset {
				return QuotaUsage{}, fmt.Errorf("duplicate resetInSec")
			}
			n, ok := parseNonNegInt(value)
			if !ok {
				return QuotaUsage{}, fmt.Errorf("invalid resetInSec")
			}
			resetInSec, hasReset = n, true
		case "usagePercent":
			if hasUsagePct {
				return QuotaUsage{}, fmt.Errorf("duplicate usagePercent")
			}
			// usagePercent 上游从整数演进为小数（OBSERVED 2026-08-25：
			// rollingUsage.usagePercent=19.3 等）。值原样保留精度（float64），
			// 前端 formatPercent 按 percentPrecision 控制小数位；
			// 非负数字形式（整数或小数）合法，负数/NaN/非数字/引号串 fail-closed。
			pct, ok := parseNonNegPercent(value)
			if !ok {
				return QuotaUsage{}, fmt.Errorf("invalid usagePercent")
			}
			usagePct, hasUsagePct = pct, true
		default:
			// Unrelated property: ignore.
		}
	}
	if !hasStatus {
		return QuotaUsage{}, fmt.Errorf("missing status")
	}
	if !hasUsagePct {
		return QuotaUsage{}, fmt.Errorf("missing usagePercent")
	}
	if !hasReset {
		// 非 ok/unlimited 状态（耗尽态）没有重置点：resetInSec 缺失合法，
		// 渲染为 reset 0m（design D1 延长，OBSERVED：耗尽对象缺 resetInSec）。
		// ok/unlimited 状态缺 resetInSec 仍视为畸形 fail-closed。
		if status == "ok" || status == "unlimited" {
			return QuotaUsage{}, fmt.Errorf("missing resetInSec")
		}
		resetInSec = 0
	}
	return QuotaUsage{
		Status:       status,
		UsagePercent: usagePct,
		ResetInSec:   resetInSec,
		ResetDisplay: formatter.FormatDurationCompact(resetInSec),
	}, nil
}

// splitTopLevelFields splits an object body on top-level commas, keeping
// commas inside quoted strings and nested braces together.
func splitTopLevelFields(s string) []string {
	var fields []string
	depth := 0
	inString := false
	escaped := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				fields = append(fields, s[start:i])
				start = i + 1
			}
		}
	}
	fields = append(fields, s[start:])
	return fields
}

// splitTopLevelKV splits one field on its first top-level ':' into the
// trimmed key and the raw value (which may itself be an object/array).
func splitTopLevelKV(field string) (string, string) {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(field); i++ {
		c := field[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
		case ':':
			if depth == 0 {
				return strings.TrimSpace(field[:i]), strings.TrimSpace(field[i+1:])
			}
		}
	}
	return strings.TrimSpace(field), ""
}

// parseSerovalString decodes a quoted seroval string value. It requires
// surrounding double quotes and unescapes backslash-escaped characters.
func parseSerovalString(v string) (string, bool) {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", false
	}
	var sb strings.Builder
	for i := 1; i < len(v)-1; i++ {
		c := v[i]
		if c == '\\' && i+1 < len(v)-1 {
			i++
			sb.WriteByte(v[i])
			continue
		}
		sb.WriteByte(c)
	}
	return sb.String(), true
}

// parseNonNegInt requires a plain non-negative integer (digits only);
// negative, fractional, quoted, or otherwise unsupported values are
// rejected rather than coerced.
func parseNonNegInt(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseNonNegPercent parses a non-negative numeric percent value that may
// be an integer (42) or a decimal (19.3) — the upstream opencode.ai payload
// switched usagePercent to fractional values (OBSERVED 2026-08-25). Empty,
// negative, NaN/Inf, quoted, or otherwise non-numeric forms are rejected
// rather than coerced. Values are returned with full precision (callers
// keep the float64); display precision is the frontend's concern.
func parseNonNegPercent(v string) (float64, bool) {
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	if f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}
