package history

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestStoreAndListExecutionRuns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := &model.StructuredInput{ProtocolVersion: model.StructuredIOVersion, Task: "Ship it"}
	output := &model.StructuredOutput{ProtocolVersion: model.StructuredIOVersion, Summary: "Done"}

	err := Store(context.Background(), root, Run{
		CreatedAt:           "2026-06-10T10:00:00Z",
		Status:              "completed",
		Summary:             "done",
		Name:                "task-54",
		ProfileName:         "default",
		Runner:              "opencode",
		Model:               "openai/gpt-5.4",
		LaunchDirectory:     filepath.Join(root, "repo"),
		RawStructuredInput:  StructuredInputJSON(input),
		RawOutputPath:       filepath.Join(root, ".progress", "runner-output", "execution.log"),
		RawStructuredOutput: StructuredOutputJSON(output, ""),
		RunRecordPath:       filepath.Join(root, ".progress", "execution-runs", "execution.json"),
	})
	if err != nil {
		t.Fatalf("store run: %v", err)
	}

	runs, err := List(context.Background(), root, ListFilter{Limit: 10, Name: "task-54", Status: "completed"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %d", len(runs))
	}
	if runs[0].Name != "task-54" || runs[0].Status != "completed" || runs[0].Model != "openai/gpt-5.4" {
		t.Fatalf("unexpected run: %#v", runs[0])
	}
	if runs[0].RawStructuredInput != `{"protocol_version":"review-cycle/v1","task":"Ship it"}` {
		t.Fatalf("unexpected structured input json: %q", runs[0].RawStructuredInput)
	}
	if runs[0].RawStructuredOutput != `{"protocol_version":"review-cycle/v1","summary":"Done"}` {
		t.Fatalf("unexpected structured output json: %q", runs[0].RawStructuredOutput)
	}
}
