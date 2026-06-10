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

func TestBeginAndUpdateExecutionRunReusesRow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	handle, err := Begin(context.Background(), root, Run{
		CreatedAt:          "2026-06-10T10:00:00Z",
		Status:             "running",
		Summary:            "",
		Name:               "task-58",
		ProfileName:        "default",
		Runner:             "opencode",
		Model:              "openai/gpt-5.4",
		LaunchDirectory:    root,
		RawStructuredInput: `{"task":"Ship it"}`,
	})
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}

	runs, err := List(context.Background(), root, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list running run: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("expected one running run, got %#v", runs)
	}

	if err := Update(context.Background(), handle, Run{
		Status:              "completed",
		Summary:             "done",
		Name:                "task-58",
		ProfileName:         "coder",
		Runner:              "opencode",
		Model:               "openai/gpt-5.5",
		LaunchDirectory:     filepath.Join(root, "workplace"),
		RawStructuredInput:  `{"task":"Ship it","constraints":["minimal"]}`,
		RawOutputPath:       filepath.Join(root, "output.log"),
		RawStructuredOutput: `{"summary":"Done"}`,
		RunRecordPath:       filepath.Join(root, "record.json"),
	}); err != nil {
		t.Fatalf("update run: %v", err)
	}

	runs, err = List(context.Background(), root, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list updated run: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("update must reuse execution_runs row, got %d", len(runs))
	}
	if runs[0].ID != handle.RunID || runs[0].Status != "completed" || runs[0].ProfileName != "coder" || runs[0].Model != "openai/gpt-5.5" {
		t.Fatalf("unexpected updated run: %#v", runs[0])
	}
	if runs[0].RawOutputPath == "" || runs[0].RawStructuredOutput != `{"summary":"Done"}` || runs[0].RunRecordPath == "" {
		t.Fatalf("updated run must keep result metadata: %#v", runs[0])
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
