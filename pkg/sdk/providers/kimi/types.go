package kimi

import (
	"errors"
	"time"
)

// ErrKimiAuthExpired 表示 Kimi 鉴权已失效（401 或 Connect unauthenticated），需触发刷新或重登录。
var ErrKimiAuthExpired = errors.New("Kimi 鉴权已过期，请重新登录")

// ErrKimiTransport 表示非鉴权引起的网络传输层故障。
var ErrKimiTransport = errors.New("Kimi 网络请求失败")

// ErrKimiUnsupportedResponse 表示返回内容不符合预期的配额结构。
var ErrKimiUnsupportedResponse = errors.New("Kimi 响应不受支持")

// ErrKimiTimeout 表示请求超时。
var ErrKimiTimeout = errors.New("Kimi 请求超时")

// KimiQuotaData 是 Kimi Code 专属的配额聚合模型。
// 涵盖总量分组（包含 Kimi 与 Code 双段占比）、5 小时窗口及 7 天窗口。
type KimiQuotaData struct {
	Total     KimiTotalUsage `json:"total"`
	FiveHour  KimiQuotaUsage `json:"five_hour"`
	SevenDay  KimiQuotaUsage `json:"seven_day"`
	FetchedAt time.Time      `json:"fetched_at"`
}

// KimiTotalUsage 对应总量使用分组（TotalPercent = KimiPercent + CodePercent）。
type KimiTotalUsage struct {
	Status       string    `json:"status"`
	TotalPercent float64   `json:"total_percent"`
	KimiPercent  float64   `json:"kimi_percent"`
	CodePercent  float64   `json:"code_percent"`
	ResetAt      time.Time `json:"reset_at"`
	ResetInSec   int       `json:"reset_in_sec"`
	ResetDisplay string    `json:"reset_display"`
}

// KimiQuotaUsage 对应单项 Code 时间窗口的配额指标。
type KimiQuotaUsage struct {
	Status       string    `json:"status"`
	UsagePercent float64   `json:"usage_percent"`
	ResetAt      time.Time `json:"reset_at"`
	ResetInSec   int       `json:"reset_in_sec"`
	ResetDisplay string    `json:"reset_display"`
}
