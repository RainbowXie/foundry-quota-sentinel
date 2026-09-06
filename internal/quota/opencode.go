package quota

import (
	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

// OpenCodeGoQuerier 是 SDK opencode.OpenCodeQuerier 的别名。
type OpenCodeGoQuerier = opencode.OpenCodeQuerier

// NewOpenCodeGoQuerier 创建 OpenCodeGoQuerier。
func NewOpenCodeGoQuerier() *OpenCodeGoQuerier {
	return opencode.NewOpenCodeQuerier()
}
