package quota

import (
	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

// RefreshResult 是 SDK kimi.RefreshResult 的别名。
type RefreshResult = kimi.RefreshResult

// KimiQuerier 是 SDK kimi.KimiQuerier 的别名。
type KimiQuerier = kimi.KimiQuerier

var ErrKimiAuthExpired = kimi.ErrKimiAuthExpired
var ErrKimiTransport = kimi.ErrKimiTransport
var ErrKimiUnsupportedResponse = kimi.ErrKimiUnsupportedResponse
var ErrKimiTimeout = kimi.ErrKimiTimeout
var ErrKimiRefreshFailed = kimi.ErrKimiRefreshFailed
