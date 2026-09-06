package quota

import (
	"foundry-quota-sentinel/pkg/sdk/providers/ollama"
)

// OllamaQuerier 是 SDK ollama.OllamaQuerier 的别名。
type OllamaQuerier = ollama.OllamaQuerier

// NewOllamaQuerier 创建 OllamaQuerier。
func NewOllamaQuerier() *OllamaQuerier {
	return ollama.NewOllamaQuerier()
}
