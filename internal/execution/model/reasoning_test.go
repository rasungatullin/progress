package model

import "testing"

func TestValidateReasoningEffortUsesRunnerModelCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		runner string
		model  string
		effort string
		want   bool
	}{
		{name: "supported model", runner: "codex", model: "gpt-5.3-codex-spark", effort: "medium", want: true},
		{name: "supported model with provider prefix", runner: "codex", model: "openai/gpt-5.3-codex-spark", effort: "xhigh", want: true},
		{name: "unknown codex model", runner: "codex", model: "gpt-5-future", effort: "medium"},
		{name: "known model name without capability entry", runner: "codex", model: "gpt-5.4", effort: "xhigh"},
		{name: "unsupported runner", runner: "opencode", model: "gpt-5.5", effort: "medium"},
		{name: "unsupported value", runner: "codex", model: "gpt-5.3-codex", effort: "ultra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateReasoningEffort(tt.runner, tt.model, tt.effort)
			if (err == nil) != tt.want {
				t.Fatalf("ValidateReasoningEffort() error = %v, want success %t", err, tt.want)
			}
		})
	}
}

func TestValidateReasoningEffortNormalizesExplicitValue(t *testing.T) {
	for _, effort := range []string{"MEDIUM", " Medium "} {
		t.Run(effort, func(t *testing.T) {
			if err := ValidateReasoningEffort("codex", "gpt-5.3-codex-spark", effort); err != nil {
				t.Fatalf("ValidateReasoningEffort() error = %v", err)
			}
		})
	}
}
