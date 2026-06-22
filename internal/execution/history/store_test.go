package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestStoreAndListExecutionRuns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := &model.StructuredInput{Task: "Ship it"}
	output := &model.StructuredOutput{Summary: "Done"}
	if err := Store(context.Background(), root, Run{
		CreatedAt:       "2026-06-10T09:59:00Z",
		Status:          "completed",
		Summary:         "parent",
		Name:            "task-parent",
		ProfileName:     "default",
		Runner:          "opencode",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: filepath.Join(root, "repo"),
	}); err != nil {
		t.Fatalf("store parent run: %v", err)
	}

	err := Store(context.Background(), root, Run{
		CreatedAt:           "2026-06-10T10:00:00Z",
		Status:              "completed",
		Summary:             "done",
		Name:                "task-54",
		ProfileName:         "default",
		Runner:              "opencode",
		RunnerSessionID:     "session-54",
		ParentRunID:         1,
		ResumeMessage:       "Continue",
		ResumeMessageSource: "message",
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
	if runs[0].RunnerSessionID != "session-54" || runs[0].ParentRunID != 1 || runs[0].ResumeMessage != "Continue" || runs[0].ResumeMessageSource != "message" {
		t.Fatalf("unexpected resume metadata: %#v", runs[0])
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

func TestBeginDoesNotCreateMissingRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "missing")
	_, err := Begin(context.Background(), root, Run{
		CreatedAt:       "2026-06-10T10:00:00Z",
		Status:          "running",
		ProfileName:     "default",
		Runner:          "opencode",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: root,
	})
	if err == nil {
		t.Fatal("expected missing root error")
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("begin must not create missing root, stat err: %v", statErr)
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

func TestGetExecutionRunByID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ctx := context.Background()
	if err := Store(ctx, root, Run{
		CreatedAt:       "2026-06-10T10:00:00Z",
		Status:          "completed",
		Summary:         "first",
		Name:            "task-1",
		ProfileName:     "default",
		Runner:          "opencode",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: root,
	}); err != nil {
		t.Fatalf("store first run: %v", err)
	}
	if err := Store(ctx, root, Run{
		CreatedAt:       "2026-06-10T10:01:00Z",
		Status:          "failed",
		Summary:         "second",
		Name:            "task-2",
		ProfileName:     "review",
		Runner:          "codex",
		Model:           "openai/gpt-5.5",
		LaunchDirectory: root,
		Error:           "boom",
	}); err != nil {
		t.Fatalf("store second run: %v", err)
	}

	run, err := Get(ctx, root, 2)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.ID != 2 || run.Name != "task-2" || run.Status != "failed" || run.Summary != "second" || run.ProfileName != "review" || run.Runner != "codex" || run.Model != "openai/gpt-5.5" || run.Error != "boom" {
		t.Fatalf("unexpected run: %#v", run)
	}

	_, err = Get(ctx, root, 99)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for missing run, got %v", err)
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
