package history

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestStoreAndListExecutionRuns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := &model.StructuredInput{Task: "Ship it"}
	output := &model.StructuredOutput{Summary: "Done"}

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
	if runs[0].RawStructuredInput != `{"task":"Ship it"}` {
		t.Fatalf("unexpected structured input json: %q", runs[0].RawStructuredInput)
	}
	if runs[0].RawStructuredOutput != `{"summary":"Done"}` {
		t.Fatalf("unexpected structured output json: %q", runs[0].RawStructuredOutput)
	}
}

func TestListInitializesEmptyExecutionHistory(t *testing.T) {
	t.Parallel()

	runs, err := List(context.Background(), t.TempDir(), ListFilter{})
	if err != nil {
		t.Fatalf("list empty history: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected empty history, got %#v", runs)
	}
}

func TestStoreCreatesExecutionRunIndexes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := Store(context.Background(), root, Run{
		CreatedAt:       "2026-06-10T10:00:00Z",
		Status:          "failed",
		Summary:         "boom",
		Name:            "task-54",
		ProfileName:     "default",
		Runner:          "opencode",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: root,
		Error:           "boom",
	}); err != nil {
		t.Fatalf("store run: %v", err)
	}

	db, err := sql.Open("sqlite3", DatabasePath(root))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for _, indexName := range []string{"execution_runs_created_at_idx", "execution_runs_name_idx", "execution_runs_status_idx"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&count); err != nil {
			t.Fatalf("query index %s: %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("expected index %s to exist", indexName)
		}
	}
}
