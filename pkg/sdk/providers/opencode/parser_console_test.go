package opencode

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// consoleStatusBody 是一份最小可用的 Console v2 `/go/status` 响应：
// fiveHour 尚未开始计时（resetsAt 为 null），week 有明确重置时间。
const consoleStatusBody = `{"subscriberUserId":"user_synthetic","product":"go","renewalProduct":"go","useBalance":false,"cancelAtPeriodEnd":false,"renewalPending":false,"access":{"startsAt":"2099-10-01T00:00:00.000Z","endsAt":"2099-11-01T00:00:00.000Z","meters":{"fiveHour":{"startsAt":null,"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"240000000"},"week":{"startsAt":"2099-10-05T00:00:00.000Z","resetsAt":"2099-10-12T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"600000000"}}}}`

const consoleRFC3339Millis = "2006-01-02T15:04:05.000Z07:00"

// consoleStatusFixture 用签名示例的字段顺序拼装响应体：顶层订阅字段与 access 包装层，
// 让解析器面对的是与线上抓包同构的输入，而不是被裁剪过的假数据。
func consoleStatusFixture(access any) string {
	payload := map[string]any{
		"subscriberUserId":  "user_synthetic",
		"product":           "go",
		"renewalProduct":    "go",
		"useBalance":        false,
		"cancelAtPeriodEnd": false,
		"renewalPending":    false,
		"access":            access,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func consoleMetersFixture(fiveHour, week, month any) map[string]any {
	meters := map[string]any{"fiveHour": fiveHour, "week": week}
	if month != nil {
		meters["month"] = month
	}
	return map[string]any{"startsAt": "2099-10-01T00:00:00.000Z", "meters": meters}
}

// consoleMeterFixture 组装单个 meter；resetsAt 为 nil 时按上游语义写成 JSON null。
func consoleMeterFixture(resetsAt any, limit, used any) map[string]any {
	return map[string]any{
		"startsAt":        "2099-10-01T00:00:00.000Z",
		"resetsAt":        resetsAt,
		"limitMicroCents": limit,
		"usedMicroCents":  used,
	}
}

func timePtr(t time.Time) string { return t.UTC().Format(consoleRFC3339Millis) }

// consoleDesignExampleBody 是变更设计文档中记录的原始响应样本（含毫秒级 ISO-8601
// 时间戳与全部合同字段），作为解析器对上游契约的定盘测试。
// 断言只取与当前时间无关的事实：固定时间戳下 ResetInSec 会随时间衰减到 0。
const consoleDesignExampleBody = `{
  "subscriberUserId": "user_synthetic",
  "product": "go",
  "renewalProduct": "go",
  "useBalance": false,
  "cancelAtPeriodEnd": false,
  "renewalPending": false,
  "access": {
    "startsAt": "2026-10-01T00:00:00.000Z",
    "endsAt": "2026-11-01T00:00:00.000Z",
    "cancelAtPeriodEnd": false,
    "meters": {
      "fiveHour": {
        "startsAt": "2026-10-09T01:00:00.000Z",
        "resetsAt": "2026-10-09T06:00:00.000Z",
        "limitMicroCents": "1200000000",
        "usedMicroCents": "240000000"
      },
      "week": {
        "startsAt": "2026-10-05T00:00:00.000Z",
        "resetsAt": "2026-10-12T00:00:00.000Z",
        "limitMicroCents": "3000000000",
        "usedMicroCents": "600000000"
      },
      "month": {
        "resetsAt": "2026-11-01T00:00:00.000Z",
        "limitMicroCents": "6000000000",
        "usedMicroCents": "1500000000"
      }
    }
  }
}`

func TestParseQuotaResponseConsoleDesignExamplePayload(t *testing.T) {
	got, err := ParseQuotaResponse(consoleDesignExampleBody)
	if err != nil {
		t.Fatalf("documented response sample must parse: %v", err)
	}
	if got.Lapsed {
		t.Fatal("documented sample has a live subscription")
	}
	if got.Rolling.UsagePercent != 20 || got.Weekly.UsagePercent != 20 {
		t.Fatalf("rolling/weekly percent = %v/%v, want 20/20", got.Rolling.UsagePercent, got.Weekly.UsagePercent)
	}
	if got.Monthly == nil || got.Monthly.UsagePercent != 25 {
		t.Fatalf("monthly = %+v, want 25%%", got.Monthly)
	}
	for name, usage := range map[string]QuotaUsage{"rolling": got.Rolling, "weekly": got.Weekly, "monthly": *got.Monthly} {
		if usage.Status != "active" {
			t.Fatalf("%s status = %q, want active", name, usage.Status)
		}
		if usage.ResetInSec < 0 || usage.ResetDisplay == "" {
			t.Fatalf("%s reset = %d/%q, want a non-negative countdown", name, usage.ResetInSec, usage.ResetDisplay)
		}
	}
}

func TestParseQuotaResponseConsoleActiveMeters(t *testing.T) {
	now := time.Now()
	body := consoleStatusFixture(consoleMetersFixture(
		consoleMeterFixture(timePtr(now.Add(5*time.Minute+30*time.Second)), "1200000000", "240000000"),
		consoleMeterFixture(timePtr(now.Add(7*24*time.Hour+90*time.Second)), "3000000000", "600000000"),
		consoleMeterFixture(timePtr(now.Add(30*24*time.Hour+90*time.Second)), "6000000000", "1500000000"),
	))

	got, err := ParseQuotaResponse(body)
	if err != nil {
		t.Fatalf("console active payload must parse: %v", err)
	}
	if got.Lapsed {
		t.Fatal("active subscription must not be marked lapsed")
	}
	if got.FetchedAt.IsZero() {
		t.Fatal("FetchedAt must be stamped")
	}
	if got.Rolling.Status != "active" || got.Rolling.UsagePercent != 20 {
		t.Fatalf("rolling = %+v, want active 20%%", got.Rolling)
	}
	if got.Rolling.ResetDisplay != "5m" {
		t.Fatalf("rolling reset display = %q, want 5m", got.Rolling.ResetDisplay)
	}
	assertResetWithin(t, "rolling", got.Rolling.ResetInSec, 330)
	if got.Weekly.Status != "active" || got.Weekly.UsagePercent != 20 {
		t.Fatalf("weekly = %+v, want active 20%%", got.Weekly)
	}
	if got.Weekly.ResetDisplay != "7d" {
		t.Fatalf("weekly reset display = %q, want 7d", got.Weekly.ResetDisplay)
	}
	assertResetWithin(t, "weekly", got.Weekly.ResetInSec, 7*24*3600+90)
	if got.Monthly == nil {
		t.Fatal("monthly must be present when meters.month is supplied")
	}
	if got.Monthly.Status != "active" || got.Monthly.UsagePercent != 25 {
		t.Fatalf("monthly = %+v, want active 25%%", *got.Monthly)
	}
	if got.Monthly.ResetDisplay != "30d" {
		t.Fatalf("monthly reset display = %q, want 30d", got.Monthly.ResetDisplay)
	}
}

// assertResetWithin 只允许秒级误差：解析器自己取 now，测试无法与它共享同一时刻。
func assertResetWithin(t *testing.T, window string, got, want int) {
	t.Helper()
	if got > want || got < want-5 {
		t.Fatalf("%s ResetInSec = %d, want within [%d,%d]", window, got, want-5, want)
	}
}

// TestParseQuotaResponseConsoleClampsFarFutureResetsAt 守住 32 位 int 平台的溢出边界：
// 100 年后的 resetsAt 距 now 约 3.15×10⁹ 秒，已超过 math.MaxInt32，
// 不钳位时会溢出为负值，使“倒计时非负”的契约反向。时间基点取 now，避免用例随时钟失效。
func TestParseQuotaResponseConsoleClampsFarFutureResetsAt(t *testing.T) {
	farFuture := timePtr(time.Now().AddDate(100, 0, 0))
	body := consoleStatusFixture(consoleMetersFixture(
		consoleMeterFixture(farFuture, "1200000000", "240000000"),
		consoleMeterFixture(farFuture, "3000000000", "600000000"),
		nil,
	))
	got, err := ParseQuotaResponse(body)
	if err != nil {
		t.Fatalf("far-future resetsAt must parse: %v", err)
	}
	for name, usage := range map[string]QuotaUsage{"rolling": got.Rolling, "weekly": got.Weekly} {
		if usage.ResetInSec != math.MaxInt32 {
			t.Fatalf("%s ResetInSec = %d, want clamp to math.MaxInt32 (%d)", name, usage.ResetInSec, math.MaxInt32)
		}
	}
}

func TestParseQuotaResponseConsoleUnstartedFiveHourWindow(t *testing.T) {
	now := time.Now()
	body := consoleStatusFixture(consoleMetersFixture(
		consoleMeterFixture(nil, "1200000000", "0"),
		consoleMeterFixture(timePtr(now.Add(7*24*time.Hour)), "3000000000", "0"),
		nil,
	))
	got, err := ParseQuotaResponse(body)
	if err != nil {
		t.Fatalf("unstarted 5h window must parse: %v", err)
	}
	if got.Rolling.ResetInSec != 0 || got.Rolling.ResetDisplay != "0s" {
		t.Fatalf("rolling = %+v, want reset 0s", got.Rolling)
	}
	if got.Rolling.UsagePercent != 0 || got.Rolling.Status != "active" {
		t.Fatalf("rolling = %+v, want active 0%%", got.Rolling)
	}
	if got.Monthly != nil {
		t.Fatalf("monthly must be omitted when meters.month is absent, got %+v", *got.Monthly)
	}
}

// TestParseQuotaResponseConsoleExpiredResetsAtClampsToZero 守住过期窗口的钳位分支：
// resetsAt 已过去时 remaining < 0，必须归零，否则下游会渲染出负倒计时。
// 时间基点取固定过去时刻（2020-01-01），断言不随真实时钟推移而腐化。
func TestParseQuotaResponseConsoleExpiredResetsAtClampsToZero(t *testing.T) {
	const expiredResetsAt = "2020-01-01T00:00:00Z"
	body := consoleStatusFixture(consoleMetersFixture(
		consoleMeterFixture(expiredResetsAt, "1200000000", "240000000"),
		consoleMeterFixture(expiredResetsAt, "3000000000", "600000000"),
		nil,
	))
	got, err := ParseQuotaResponse(body)
	if err != nil {
		t.Fatalf("expired resetsAt must still parse: %v", err)
	}
	for name, usage := range map[string]QuotaUsage{"rolling": got.Rolling, "weekly": got.Weekly} {
		if usage.ResetInSec != 0 {
			t.Fatalf("%s ResetInSec = %d, want 0 (clamped)", name, usage.ResetInSec)
		}
		if usage.ResetDisplay != "0s" {
			t.Fatalf("%s ResetDisplay = %q, want 0s", name, usage.ResetDisplay)
		}
	}
}

func TestParseQuotaResponseConsoleMissingFiveHourResetsAtKey(t *testing.T) {
	now := time.Now()
	fiveHour := map[string]any{"limitMicroCents": "1200000000", "usedMicroCents": "0"}
	body := consoleStatusFixture(consoleMetersFixture(
		fiveHour,
		consoleMeterFixture(timePtr(now.Add(7*24*time.Hour)), "3000000000", "0"),
		nil,
	))
	got, err := ParseQuotaResponse(body)
	if err != nil {
		t.Fatalf("absent fiveHour.resetsAt key must be treated as unstarted: %v", err)
	}
	if got.Rolling.ResetInSec != 0 || got.Rolling.ResetDisplay != "0s" {
		t.Fatalf("rolling = %+v, want reset 0s", got.Rolling)
	}
}

func TestParseQuotaResponseConsoleExhaustedMetersCapAt100(t *testing.T) {
	now := time.Now()
	body := consoleStatusFixture(consoleMetersFixture(
		consoleMeterFixture(nil, "1200000000", "1500000000"),
		consoleMeterFixture(timePtr(now.Add(7*24*time.Hour)), "3000000000", "0"),
		nil,
	))
	got, err := ParseQuotaResponse(body)
	if err != nil {
		t.Fatalf("used > limit must parse as exhausted, got: %v", err)
	}
	if got.Rolling.Status != "exhausted" || got.Rolling.UsagePercent != 100 {
		t.Fatalf("rolling = %+v, want exhausted 100%%", got.Rolling)
	}
}

func TestParseQuotaResponseConsolePercentPrecision(t *testing.T) {
	week := `{"resetsAt":"2099-10-12T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"600000000"}`
	tests := []struct {
		name     string
		fiveHour string
		want     float64
	}{
		{name: "exact two decimals", fiveHour: `{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"231000000"}`, want: 19.25},
		{name: "repeating decimal rounds to two decimals", fiveHour: `{"resetsAt":null,"limitMicroCents":"3","usedMicroCents":"1"}`, want: 33.33},
		{name: "zero usage", fiveHour: `{"resetsAt":null,"limitMicroCents":"3","usedMicroCents":"0"}`, want: 0},
		{name: "numeric micro-cents tokens", fiveHour: `{"resetsAt":null,"limitMicroCents":1200000000,"usedMicroCents":240000000}`, want: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"product":"go","access":{"meters":{"fiveHour":` + tt.fiveHour + `,"week":` + week + `}}}`
			got, err := ParseQuotaResponse(body)
			if err != nil {
				t.Fatalf("payload must parse: %v", err)
			}
			if got.Rolling.UsagePercent != tt.want {
				t.Fatalf("rolling percent = %v, want %v", got.Rolling.UsagePercent, tt.want)
			}
		})
	}
}

func TestParseQuotaResponseConsoleLapsedSubscription(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "literal null payload", body: `null`},
		{name: "null access with subscription envelope", body: consoleStatusFixture(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQuotaResponse(tt.body)
			if err != nil {
				t.Fatalf("lapsed subscription must not fail the account: %v", err)
			}
			if !got.Lapsed {
				t.Fatal("lapsed subscription must mark QuotaData.Lapsed")
			}
			if got.Rolling.Status != "unavailable" || got.Weekly.Status != "unavailable" {
				t.Fatalf("rolling/weekly = %+v / %+v, want unavailable", got.Rolling, got.Weekly)
			}
			if got.Rolling.ResetDisplay != "0s" || got.Weekly.ResetInSec != 0 {
				t.Fatalf("lapsed reset fields = %+v / %+v, want 0s", got.Rolling, got.Weekly)
			}
			if got.Monthly != nil {
				t.Fatalf("lapsed subscription must omit monthly, got %+v", *got.Monthly)
			}
		})
	}
}

func TestParseQuotaResponseConsoleRejectsMalformed(t *testing.T) {
	validWeek := `{"resetsAt":"2099-10-12T00:00:00.000Z","limitMicroCents":"3000000000","usedMicroCents":"600000000"}`
	validFiveHour := `{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"240000000"}`
	wrap := func(fiveHour, week, month string) string {
		monthField := ""
		if month != "" {
			monthField = `,"month":` + month
		}
		return `{"product":"go","access":{"meters":{"fiveHour":` + fiveHour + `,"week":` + week + monthField + `}}}`
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "access absent", body: `{"product":"go"}`},
		{name: "fiveHour absent", body: wrap("null", validWeek, "")},
		{name: "week absent", body: wrap(validFiveHour, "null", "")},
		{name: "meters absent", body: `{"product":"go","access":{"startsAt":"2099-10-01T00:00:00.000Z"}}`},
		{name: "access is a string", body: `{"product":"go","access":"go"}`},
		{name: "access is an array", body: `{"product":"go","access":[]}`},
		{name: "meters is a string", body: `{"product":"go","access":{"meters":"none"}}`},
		{name: "zero limit", body: wrap(`{"resetsAt":null,"limitMicroCents":"0","usedMicroCents":"0"}`, validWeek, "")},
		{name: "numeric zero limit", body: wrap(`{"resetsAt":null,"limitMicroCents":0,"usedMicroCents":0}`, validWeek, "")},
		{name: "negative limit", body: wrap(`{"resetsAt":null,"limitMicroCents":"-1","usedMicroCents":"0"}`, validWeek, "")},
		{name: "negative used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"-1"}`, validWeek, "")},
		{name: "non numeric used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"abc"}`, validWeek, "")},
		{name: "fractional used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"1.5"}`, validWeek, "")},
		{name: "exponent used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"1e3"}`, validWeek, "")},
		{name: "empty used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":""}`, validWeek, "")},
		{name: "null used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":null}`, validWeek, "")},
		{name: "used absent", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000"}`, validWeek, "")},
		{name: "limit absent", body: wrap(`{"resetsAt":null,"usedMicroCents":"240000000"}`, validWeek, "")},
		{name: "overflowing used", body: wrap(`{"resetsAt":null,"limitMicroCents":"1200000000","usedMicroCents":"18446744073709551616"}`, validWeek, "")},
		{name: "fiveHour resetsAt not RFC3339", body: wrap(`{"resetsAt":"soon","limitMicroCents":"1200000000","usedMicroCents":"0"}`, validWeek, "")},
		{name: "week resetsAt null", body: wrap(validFiveHour, `{"resetsAt":null,"limitMicroCents":"3000000000","usedMicroCents":"0"}`, "")},
		{name: "week resetsAt absent", body: wrap(validFiveHour, `{"limitMicroCents":"3000000000","usedMicroCents":"0"}`, "")},
		{name: "month resetsAt null", body: wrap(validFiveHour, validWeek, `{"resetsAt":null,"limitMicroCents":"6000000000","usedMicroCents":"0"}`)},
		{name: "month resetsAt not RFC3339", body: wrap(validFiveHour, validWeek, `{"resetsAt":"2026-13-45T99:99:99Z","limitMicroCents":"6000000000","usedMicroCents":"0"}`)},
		{name: "month zero limit", body: wrap(validFiveHour, validWeek, `{"resetsAt":"2099-11-01T00:00:00.000Z","limitMicroCents":"0","usedMicroCents":"0"}`)},
		{name: "truncated access object", body: `{"product":"go","access":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQuotaResponse(tt.body)
			if err == nil {
				t.Fatalf("expected error for %s, got quota %+v", tt.name, got)
			}
			if got != nil {
				t.Fatalf("parser must return nil quota on error for %s, got %+v", tt.name, got)
			}
		})
	}
}

// TestParseQuotaResponseConsoleJSONDoesNotFallBackToSeroval 守住判别边界：
// 形如 REST JSON 的输入即使字段不全也必须 fail-closed，不能退回 Seroval 解析而“碰巧成功”。
func TestParseQuotaResponseConsoleJSONDoesNotFallBackToSeroval(t *testing.T) {
	body := `{"rollingUsage":{"status":"ok","resetInSec":300,"usagePercent":42}}`
	got, err := ParseQuotaResponse(body)
	if err == nil {
		t.Fatalf("JSON without access/meters must fail closed, got %+v", got)
	}
	if !strings.Contains(err.Error(), "access") {
		t.Fatalf("error must name the missing console field, got %q", err)
	}
}
