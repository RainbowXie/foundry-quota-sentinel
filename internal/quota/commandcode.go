package quota

import (
	"foundry-quota-sentinel/pkg/sdk/providers/commandcode"
)

// CommandCodeQuerier 是 SDK commandcode.CommandCodeQuerier 的别名。
type CommandCodeQuerier = commandcode.CommandCodeQuerier

// NewCommandCodeQuerier 创建 CommandCodeQuerier。
func NewCommandCodeQuerier() *CommandCodeQuerier {
	return commandcode.NewCommandCodeQuerier()
}
