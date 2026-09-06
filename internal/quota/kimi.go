package quota

import (
	"time"

	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

type KimiQuotaData = kimi.KimiQuotaData
type KimiTotalUsage = kimi.KimiTotalUsage
type KimiQuotaUsage = kimi.KimiQuotaUsage

func ParseKimiQuota(body string, now time.Time) (*KimiQuotaData, error) {
	return kimi.ParseKimiQuota(body, now)
}

func FormatKimiPercent(p float64) string {
	return kimi.FormatKimiPercent(p)
}

func KimiRatioToPercent(r float64, name string) (float64, error) {
	return kimi.KimiRatioToPercent(r, name)
}

func KimiParseResetAt(resetTime, name string, now time.Time) (time.Time, error) {
	return kimi.KimiParseResetAt(resetTime, name, now)
}
