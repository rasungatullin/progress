package model

import (
	"fmt"
	"strings"
)

type reasoningEffortBinding struct {
	runner string
	model  string
}

var reasoningEffortCapabilities = map[reasoningEffortBinding][]string{
	{runner: "codex", model: "gpt-5.3-codex"}:       {"none", "minimal", "low", "medium", "high", "xhigh"},
	{runner: "codex", model: "gpt-5.3-codex-spark"}: {"none", "minimal", "low", "medium", "high", "xhigh"},
	{runner: "codex", model: "gpt-5.5"}:             {"none", "minimal", "low", "medium", "high", "xhigh"},
}

// ReasoningEffortSupported определяет, поддерживает ли связка исполнительного
// модуля и модели параметр reasoning-effort.
func ReasoningEffortSupported(runner, modelName string) bool {
	return len(ReasoningEffortValues(runner, modelName)) != 0
}

// ReasoningEffortValues возвращает допустимые значения reasoning-effort для
// конкретной связки исполнительного модуля и модели.
func ReasoningEffortValues(runner, modelName string) []string {
	values := reasoningEffortCapabilities[reasoningEffortBinding{
		runner: strings.TrimSpace(runner),
		model:  strings.TrimPrefix(strings.TrimSpace(modelName), "openai/"),
	}]
	if len(values) == 0 {
		return nil
	}

	return append([]string(nil), values...)
}

// ValidateReasoningEffort проверяет значение reasoning-effort по возможностям
// конкретной связки исполнительного модуля и модели.
func ValidateReasoningEffort(runner, modelName, effort string) error {
	effort = NormalizeReasoningEffort(effort)
	if effort == "" {
		return nil
	}

	values := ReasoningEffortValues(runner, modelName)
	if len(values) == 0 {
		if strings.TrimSpace(runner) != "codex" {
			return fmt.Errorf("runner %q does not support reasoning-effort", runner)
		}
		return fmt.Errorf("model %q does not support reasoning-effort", modelName)
	}

	for _, supported := range values {
		if effort == supported {
			return nil
		}
	}

	return fmt.Errorf("unsupported reasoning-effort value %q", effort)
}

// NormalizeReasoningEffort приводит явно заданное усилие рассуждения к
// канонической форме, используемой в конфигурации и командной строке.
func NormalizeReasoningEffort(effort string) string {
	return strings.ToLower(strings.TrimSpace(effort))
}
