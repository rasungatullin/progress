package model

import "strings"

// ReasoningEffortSupported определяет, поддерживает ли связка исполнительного
// модуля и модели параметр reasoning-effort.
func ReasoningEffortSupported(runner, modelName string) bool {
	if strings.TrimSpace(runner) != "codex" {
		return false
	}

	switch strings.TrimPrefix(strings.TrimSpace(modelName), "openai/") {
	case "gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-5.5":
		return true
	default:
		return false
	}
}
