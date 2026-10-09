package opencode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseQuotaResponse 解析 upstream 返回的配额响应。
// OpenCode 已迁移到 Console v2 的 REST JSON，因此优先按新格式解析；
// 旧版 Seroval 文本仍是历史响应与回归测试的输入，保留同一入口解析以免调用方感知差异。
func ParseQuotaResponse(text string) (*QuotaData, error) {
	if isConsoleStatusJSON(text) {
		return parseConsoleStatusJSON(text)
	}
	return parseSerovalQuotaResponse(text)
}

// parseSerovalQuotaResponse 解析旧版 Seroval 序列化文本（如 `{rollingUsage:$R[1]={...}}`）。
func parseSerovalQuotaResponse(text string) (*QuotaData, error) {
	rolling, err := extractUsageWindow(text, "rollingUsage", false)
	if err != nil {
		return nil, err
	}
	weekly, err := extractUsageWindow(text, "weeklyUsage", false)
	if err != nil {
		return nil, err
	}
	if rolling == nil && weekly == nil {
		// 只有真正的 Seroval 文本才允许推断“无 quota 记录=订阅失效”：Seroval 响应一定带 `$R` 引用表
		// 标记。任何非 JSON 的 200 响应体（截断输出、BOM 前缀、Cloudflare 拦截页）都可能含 "null"
		// 字样，若仅凭它返回 Lapsed 就会把可用账号误报成“订阅已失效”，所以必须要求两个标记同时存在。
		if !strings.Contains(text, "$R") || !strings.Contains(text, "null") {
			return nil, fmt.Errorf("failed to parse rollingUsage")
		}
		return lapsedQuotaData(), nil
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

// lapsedQuotaData 表示订阅失效（无 quota 记录）时的统一结果：
// Rolling/Weekly 标记 unavailable，Monthly 留空，Lapsed 为 true 供 UI 渲染失效卡片。
func lapsedQuotaData() *QuotaData {
	unavailable := QuotaUsage{Status: "unavailable", ResetDisplay: formatDurationCompact(0)}
	return &QuotaData{
		Rolling:   unavailable,
		Weekly:    unavailable,
		Lapsed:    true,
		FetchedAt: time.Now(),
	}
}

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
			i += len(windowName)
			continue
		}
		if len(out) > 0 {
			return nil, fmt.Errorf("failed to parse %s: duplicate %s object", windowName, windowName)
		}
		j = skipSpace(text, j+1)
		next, matched := matchRefAssignment(text, j)
		if matched {
			j = skipSpace(text, next)
		} else if j < len(text) && text[j] == '$' {
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
			pct, ok := parseNonNegPercent(value)
			if !ok {
				return QuotaUsage{}, fmt.Errorf("invalid usagePercent")
			}
			usagePct, hasUsagePct = pct, true
		}
	}
	if !hasStatus {
		return QuotaUsage{}, fmt.Errorf("missing status")
	}
	if !hasUsagePct {
		return QuotaUsage{}, fmt.Errorf("missing usagePercent")
	}
	if !hasReset {
		if status == "ok" || status == "unlimited" {
			return QuotaUsage{}, fmt.Errorf("missing resetInSec")
		}
		resetInSec = 0
	}
	return QuotaUsage{
		Status:       status,
		UsagePercent: usagePct,
		ResetInSec:   resetInSec,
		ResetDisplay: formatDurationCompact(resetInSec),
	}, nil
}

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

func formatDurationCompact(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	return fmt.Sprintf("%dd", seconds/86400)
}
