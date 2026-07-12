package model

import "strings"

var reasoningEffortCapabilities = map[string]map[string][]string{
	"codex": {
		"gpt-5.3-codex":       {"none", "minimal", "low", "medium", "high", "xhigh"},
		"gpt-5.3-codex-spark": {"none", "minimal", "low", "medium", "high", "xhigh"},
		"gpt-5.5":             {"none", "minimal", "low", "medium", "high", "xhigh"},
	},
}

// ReasoningEffortSupported определяет, поддерживает ли связка исполнительного
// модуля и модели параметр reasoning-effort.
func ReasoningEffortSupported(runner, modelName string) bool {
	return len(ReasoningEffortValues(runner, modelName)) != 0
}

// ReasoningEffortValues возвращает допустимые значения reasoning-effort для
// конкретной связки исполнительного модуля и модели.
func ReasoningEffortValues(runner, modelName string) []string {
	runnerCapabilities := reasoningEffortCapabilities[strings.TrimSpace(runner)]
	values := runnerCapabilities[strings.TrimPrefix(strings.TrimSpace(modelName), "openai/")]
	if len(values) == 0 {
		return nil
	}

	return append([]string(nil), values...)
}
