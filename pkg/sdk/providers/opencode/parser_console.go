package opencode

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// consoleStatusPayload 只声明配额计算真正需要的字段：上游还有 renewalPending、
// cancelAtPeriodEnd 等合同字段，encoding/json 默认忽略未知字段，无需跟随上游增删而改解析器。
type consoleStatusPayload struct {
	Access json.RawMessage `json:"access"`
}

type consoleAccess struct {
	Meters consoleMeters `json:"meters"`
}

// consoleMeters 中 fiveHour 与 week 是必填窗口，month 可缺省（部分订阅没有月窗口）。
type consoleMeters struct {
	FiveHour *consoleMeter `json:"fiveHour"`
	Week     *consoleMeter `json:"week"`
	Month    *consoleMeter `json:"month"`
}

// consoleMeter 的 resetsAt 允许为 null：5 小时窗口在首次调用前尚未开始计时，
// 上游此时不给出重置时间。usedMicroCents/limitMicroCents 保留原始 JSON token，
// 因为上游以 BigInt 的十进制字符串传输（个别环境退化为数值字面量）。
type consoleMeter struct {
	ResetsAt        *string         `json:"resetsAt"`
	LimitMicroCents json.RawMessage `json:"limitMicroCents"`
	UsedMicroCents  json.RawMessage `json:"usedMicroCents"`
}

// isConsoleStatusJSON 判别输入是新版 Console REST JSON 还是旧版 Seroval 文本。
// 旧版 Seroval 的字段名是裸标识符（如 rollingUsage:{...}），不是合法 JSON，
// 因此用 json.Valid 判别比关键字匹配更严格：带引号的字段名只可能来自 REST 响应。
func isConsoleStatusJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "null" {
		return true
	}
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	return json.Valid([]byte(trimmed))
}

// parseConsoleStatusJSON 解析 `GET /console/api/go/status` 的响应体。
// 无订阅时上游返回顶层 null 或 `{"access": null}`；有订阅时 access.meters 必须给出
// fiveHour（Rolling）与 week（Weekly）。access 字段整个缺失属于非预期形状，必须报错：
// 若当成失效订阅处理，上游改为返回 `{"error": ...}` 时会被静默渲染成“订阅失效”。
func parseConsoleStatusJSON(text string) (*QuotaData, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "null" {
		return lapsedQuotaData(), nil
	}
	var payload consoleStatusPayload
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil, fmt.Errorf("parse console status json: %w", err)
	}
	if len(payload.Access) == 0 {
		return nil, fmt.Errorf("console status response missing access")
	}
	if string(payload.Access) == "null" {
		return lapsedQuotaData(), nil
	}
	var access consoleAccess
	if err := json.Unmarshal(payload.Access, &access); err != nil {
		return nil, fmt.Errorf("parse console access: %w", err)
	}

	now := time.Now()
	rolling, err := consoleUsage(access.Meters.FiveHour, "meters.fiveHour", true, now)
	if err != nil {
		return nil, err
	}
	weekly, err := consoleUsage(access.Meters.Week, "meters.week", false, now)
	if err != nil {
		return nil, err
	}
	var monthly *QuotaUsage
	if access.Meters.Month != nil {
		usage, err := consoleUsage(access.Meters.Month, "meters.month", false, now)
		if err != nil {
			return nil, err
		}
		monthly = &usage
	}
	return &QuotaData{Rolling: rolling, Weekly: weekly, Monthly: monthly, FetchedAt: now}, nil
}

// consoleUsage 把一个 meter 换算为 QuotaUsage。
// allowNullResets 只对 fiveHour 开放：只有 5 小时窗口存在“尚未开始计时”的合法状态，
// week/month 缺失 resetsAt 属于数据异常，必须报错而不是猜一个倒计时。
func consoleUsage(meter *consoleMeter, label string, allowNullResets bool, now time.Time) (QuotaUsage, error) {
	if meter == nil {
		return QuotaUsage{}, fmt.Errorf("console status missing %s", label)
	}
	used, err := parseMicroCents(meter.UsedMicroCents)
	if err != nil {
		return QuotaUsage{}, fmt.Errorf("%s usedMicroCents: %w", label, err)
	}
	limit, err := parseMicroCents(meter.LimitMicroCents)
	if err != nil {
		return QuotaUsage{}, fmt.Errorf("%s limitMicroCents: %w", label, err)
	}
	if limit == 0 {
		return QuotaUsage{}, fmt.Errorf("%s limitMicroCents must be positive", label)
	}
	resetInSec, err := consoleResetInSec(meter.ResetsAt, label, allowNullResets, now)
	if err != nil {
		return QuotaUsage{}, err
	}
	usage := QuotaUsage{ResetInSec: resetInSec, ResetDisplay: formatDurationCompact(resetInSec)}
	if used >= limit {
		// 超额与打满都封顶 100%，否则 UI 进度条会溢出 100%。
		usage.Status, usage.UsagePercent = "exhausted", 100
		return usage, nil
	}
	usage.Status = "active"
	usage.UsagePercent = math.Round(float64(used)*100.0/float64(limit)*100) / 100
	return usage, nil
}

// consoleResetInSec 计算距重置的剩余秒数：已过期或未开始计时一律归零，
// 避免下游把负值当成“刚刚重置”以外的含义。
func consoleResetInSec(resetsAt *string, label string, allowNull bool, now time.Time) (int, error) {
	if resetsAt == nil {
		if !allowNull {
			return 0, fmt.Errorf("%s resetsAt is required", label)
		}
		return 0, nil
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(*resetsAt))
	if err != nil {
		return 0, fmt.Errorf("%s resetsAt is not a valid RFC3339 timestamp", label)
	}
	remaining := ts.Sub(now).Seconds()
	if remaining < 0 {
		return 0, nil
	}
	// Go 的 int 宽度随平台变化：远期 resetsAt（如夹具里的 2099 年）约 2.5×10⁹ 秒，
	// 在 32 位平台上会溢出成负值并翻转“倒计时非负”的契约，因此统一钳位到 int32 上界。
	if remaining > math.MaxInt32 {
		return math.MaxInt32, nil
	}
	return int(remaining), nil
}

// parseMicroCents 解析微美分额度。上游以 BigInt 的十进制字符串传输该值，
// 因此先取出原始 token 再严格按十进制非负整数解析：拒绝负号、小数、科学计数法、
// 空值与 null。宽松解析（例如先转 float64）会把超界金额静默截断成错误的配额。
func parseMicroCents(raw json.RawMessage) (uint64, error) {
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return 0, fmt.Errorf("missing value")
	}
	if token[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, fmt.Errorf("not a valid string")
		}
		token = strings.TrimSpace(s)
	}
	if token == "" {
		return 0, fmt.Errorf("empty value")
	}
	n, err := strconv.ParseUint(token, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not a non-negative decimal integer")
	}
	return n, nil
}
