package quota

import (
	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
)

// DeepSeekQuerier 是 SDK deepseek.DeepSeekQuerier 的别名。
type DeepSeekQuerier = deepseek.DeepSeekQuerier

// DeepSeekWebQuerier 是 SDK deepseek.DeepSeekWebQuerier 的别名。
type DeepSeekWebQuerier = deepseek.DeepSeekWebQuerier

// NewDeepSeekQuerier 创建 DeepSeekQuerier。
func NewDeepSeekQuerier() *DeepSeekQuerier {
	return deepseek.NewDeepSeekQuerier()
}
