package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestLaunchCommitPushDisabled(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"summary":"Done.","commit_message":"Ignored when git is disabled"}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	workplace := validWorkplace(t)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RunRecordPath == "" {
		t.Fatalf("run record path must be present")
	}
	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Result.Status != "completed" {
		t.Fatalf("unexpected run record status: %#v", record.Result)
	}
	if record.Invocation.Launch.Prompt != "do work" {
		t.Fatalf("unexpected run record prompt: %#v", record.Invocation.Launch.Prompt)
	}
	if record.StructuredOutput == nil || record.StructuredOutput.Summary != "Done." {
		t.Fatalf("unexpected run record structured output: %#v", record.StructuredOutput)
	}
	if strings.TrimSpace(record.Result.RawOutputPath) == "" {
		t.Fatalf("run record must keep raw output path: %#v", record.Result)
	}
	if !strings.Contains(result.Summary, "git=disabled") {
		t.Fatalf("summary must include disabled git state: %q", result.Summary)
	}
	if result.StructuredOutput == nil || result.StructuredOutput.CommitMessage != "Ignored when git is disabled" {
		t.Fatalf("structured output must still be parsed when git stage is disabled: %#v", result.StructuredOutput)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one sqlite history run, got %d", len(runs))
	}
	if runs[0].Status != "completed" || runs[0].ProfileName != "default" || runs[0].Runner != RunnerOpenCode {
		t.Fatalf("unexpected sqlite history run: %#v", runs[0])
	}
	if runs[0].LaunchDirectory != invocation.Launch.Directory {
		t.Fatalf("sqlite history must keep launch directory: %#v", runs[0])
	}
	if !strings.Contains(runs[0].RawStructuredOutput, `"summary":"Done."`) {
		t.Fatalf("sqlite history must keep canonical structured output: %#v", runs[0])
	}
}

func TestLaunchUpdatesExistingHistoryHandle(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"summary":"Done."}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	workplace := validWorkplace(t)
	handle, err := history.Begin(context.Background(), workplace.Name, history.Run{
		CreatedAt:          "2026-06-10T10:00:00Z",
		Status:             "running",
		Summary:            "",
		Name:               "task-58",
		ProfileName:        "default",
		Runner:             invocation.Launch.Runner,
		Model:              invocation.Launch.Model,
		LaunchDirectory:    invocation.Launch.Directory,
		RawStructuredInput: history.StructuredInputJSON(invocation.Launch.StructuredInput),
	})
	if err != nil {
		t.Fatalf("begin history: %v", err)
	}

	result, err := service.Launch(WithHistoryHandle(context.Background(), handle), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("launch must update existing sqlite history run, got %d", len(runs))
	}
	if runs[0].ID != handle.RunID || runs[0].Status != "completed" || !strings.Contains(runs[0].RawStructuredOutput, `"summary":"Done."`) {
		t.Fatalf("unexpected updated sqlite history run: %#v", runs[0])
	}
}

func TestLaunchRecordsInvalidStructuredInputInHistory(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			t.Fatal("runner must not start for invalid structured input")
			return "", nil
		},
	}
	invocation := validInvocation(t, false)
	invocation.Launch.StructuredInput = &model.StructuredInput{}
	workplace := validWorkplace(t)

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err == nil {
		t.Fatal("expected invalid structured input error")
	}
	if result.Status != "failed" || strings.TrimSpace(result.RunRecordPath) == "" {
		t.Fatalf("failed pre-launch result must include run record path: %#v", result)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10, Status: "failed"})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one failed sqlite history run, got %d", len(runs))
	}
	if runs[0].Error == "" || runs[0].Summary == "" {
		t.Fatalf("failed pre-launch row must keep error details: %#v", runs[0])
	}
}

func TestLaunchUnavailableDirectoryDoesNotCreateHistoryArtifacts(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			t.Fatal("runner must not start for unavailable launch directory")
			return "", nil
		},
	}
	missingDir := filepath.Join(t.TempDir(), "missing")
	invocation := validInvocation(t, false)
	invocation.Launch.Directory = missingDir
	workplace := model.Workplace{Name: missingDir, Ready: true}

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err == nil {
		t.Fatal("expected unavailable launch directory error")
	}
	if !strings.Contains(err.Error(), "launch directory is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RunRecordPath != "" {
		t.Fatalf("run record must not be written under missing root: %#v", result)
	}
	if _, statErr := os.Stat(missingDir); !os.IsNotExist(statErr) {
		t.Fatalf("launch must not create missing directory, stat err: %v", statErr)
	}
}

func TestLaunchCommitPushWithChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	statusCalls := 0
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"summary":"Done.","commit_message":"  Ship release notes  "}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				statusCalls++
				if statusCalls == 1 {
					return " M file.txt\n", nil
				}
				return "M  file.txt\n", nil
			case "add -A -- . :(exclude).progress/runner-output :(exclude).progress/execution-runs":
				return "", nil
			case "commit -m Ship release notes":
				return "[feature/test abc123] Ship release notes\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "", nil
			case "push -u origin feature/test":
				return "branch 'feature/test' set up to track 'origin/feature/test'.\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=committed+pushed branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
		{"add", "-A", "--", ".", runnerOutputExcludePathspec, executionRunsExcludePathspec},
		{"status", "--porcelain"},
		{"commit", "-m", "Ship release notes"},
		{"for-each-ref", "--format=%(upstream:short)", "refs/heads/feature/test"},
		{"push", "-u", "origin", "feature/test"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
	if result.RunRecordPath == "" {
		t.Fatalf("run record path must be present")
	}
	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Result.Status != "completed" {
		t.Fatalf("unexpected run record status: %#v", record.Result)
	}
	if !strings.Contains(record.Result.Summary, "git=committed+pushed branch=feature/test") {
		t.Fatalf("run record summary must include git status: %#v", record.Result.Summary)
	}
}

func TestLaunchCommitPushExcludesRunnerOutputFromGitAdd(t *testing.T) {
	t.Parallel()

	worktree := tempDir(t)
	invocation := validInvocation(t, true)
	invocation.Launch.Directory = worktree
	workplace := model.Workplace{Name: worktree, Ready: true}

	var addArgs []string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				if addArgs == nil {
					return " M file.txt\n", nil
				}
				return "M  file.txt\n", nil
			case "add -A -- . :(exclude).progress/runner-output :(exclude).progress/execution-runs":
				addArgs = append([]string(nil), args...)
				return "", nil
			case "commit -m repo":
				return "[feature/test abc123] repo\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !reflect.DeepEqual(addArgs, []string{"add", "-A", "--", ".", runnerOutputExcludePathspec, executionRunsExcludePathspec}) {
		t.Fatalf("git add must exclude raw runner output path: %#v", addArgs)
	}
}

func TestLaunchCommitPushUsesWorkplaceNameWhenStructuredCommitMessageBlank(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, true)
	invocation.Workplace.Name = "review-fixes"

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"summary":"Done.","commit_message":"   "}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A -- . :(exclude).progress/runner-output :(exclude).progress/execution-runs":
				return "", nil
			case "commit -m review-fixes":
				return "[feature/test abc123] review-fixes\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t)); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

func TestLaunchCommitPushUsesWorktreeDirectoryNameWhenWorkplaceNameMissing(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, true)
	worktreeDir := filepath.Join(t.TempDir(), "structured-contract-v1-worktree")

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"summary":"Done.","commit_message":"\t"}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A -- . :(exclude).progress/runner-output :(exclude).progress/execution-runs":
				return "", nil
			case "commit -m structured-contract-v1-worktree":
				return "[feature/test abc123] structured-contract-v1-worktree\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	workplace := model.Workplace{Name: worktreeDir, Ready: true}
	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

func TestLaunchCommitPushSkipsCommitAndPushWhenNoChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushSkipsCommitAndPushWhenOnlyRunnerOutputChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "?? .progress/runner-output/raw-output.txt\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushSkipsCommitAndPushWhenOnlyExecutionRunRecordsChange(t *testing.T) {
	t.Parallel()

	var calls [][]string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "?? .progress/execution-runs/execution-123.json\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushKeepsProgressConfigVisibleAsUserChange(t *testing.T) {
	t.Parallel()

	if !statusLineHasUserChanges(" M .progress/execution/profiles.json") {
		t.Fatal("tracked progress execution config must remain visible to commit/push")
	}
	if statusLineHasUserChanges("?? .progress/execution-runs/execution-123.json") {
		t.Fatal("execution run records must be ignored as runtime artifacts")
	}
	if statusLineHasUserChanges(" M .progress/execution-runs/execution-123.json") {
		t.Fatal("unstaged execution run record changes must be ignored as runtime artifacts")
	}
	if statusLineHasUserChanges(" M .progress/execution-runs/execution.db") {
		t.Fatal("sqlite execution history must be ignored as a runtime artifact")
	}
	if statusLineHasUserChanges(" M .progress/runner-output/execution-123.log") {
		t.Fatal("unstaged runner output changes must be ignored as runtime artifacts")
	}
}

func TestLaunchPushErrorReturned(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A -- . :(exclude).progress/runner-output :(exclude).progress/execution-runs":
				return "", nil
			case "commit -m repo":
				return "[feature/test abc123] repo\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "", errors.New("exit status 1\nremote rejected")
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected push error")
	}
	if !strings.Contains(err.Error(), "git push failed") || !strings.Contains(err.Error(), "remote rejected") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("result status must signal failed launch: %#v", result)
	}
	if strings.TrimSpace(result.RawOutputPath) == "" {
		t.Fatalf("raw output path must be preserved when commit/push fails: %#v", result)
	}
	if result.RunRecordPath == "" {
		t.Fatalf("run record path must be present on push error: %#v", result)
	}
	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Result.Status != "failed" {
		t.Fatalf("unexpected run record status: %#v", record.Result)
	}
	if !strings.Contains(record.Error, "git push failed") {
		t.Fatalf("unexpected run record error: %#v", record.Error)
	}
}

func TestLaunchRunnerErrorReturned(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("launch runner failed")
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "", runnerErr
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when runner fails")
			return "", nil
		},
	}

	workplace := validWorkplace(t)
	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), workplace)
	if err == nil {
		t.Fatal("expected runner error")
	}
	if result.Status != "failed" {
		t.Fatalf("result status must signal failed launch: %#v", result)
	}
	if !strings.Contains(result.Summary, "launch runner failed") {
		t.Fatalf("result summary should include runner error: %#v", result)
	}
	if result.RunRecordPath == "" {
		t.Fatalf("run record path must be present on runner error: %#v", result)
	}
	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Result.Status != "failed" {
		t.Fatalf("unexpected run record status: %#v", record.Result)
	}
	if !strings.Contains(record.Error, "launch runner failed") {
		t.Fatalf("unexpected run record error: %#v", record.Error)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runner failure must update one sqlite history run, got %d", len(runs))
	}
	if runs[0].Status != "failed" || runs[0].Error != runnerErr.Error() {
		t.Fatalf("unexpected runner failure sqlite row: %#v", runs[0])
	}
}

func TestLaunchStructuredOutputPresent(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"summary":"Main result.","commit_message":"Document deploy checklist","remarks":[{"id":"remark-1","severity":"critical","title":"Rollback plan","body":"Document rollback steps."}],"questions":[{"id":"question-1","title":"Integration coverage","body":"Should we add an integration test?"}],"follow_up_actions":[{"id":"action-1","status":"pending","type":"docs","title":"Update release checklist"}],"changes":[{"summary":"Touched deploy docs."}],"commands":[{"name":"open-pr","args":["--draft"]}],"conclusion":{"status":"needs-follow-up","summary":"Ship after docs update"},"extensions":{"custom":{"owner":"release"}}}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "result=Main result.") {
		t.Fatalf("summary must include compact structured result: %q", result.Summary)
	}
	if strings.Contains(result.Summary, "Applied the requested changes.") {
		t.Fatalf("summary must not include full plain runner output for valid structured runs: %q", result.Summary)
	}
	if strings.TrimSpace(result.RawOutputPath) == "" {
		t.Fatalf("raw output path must be present: %#v", result)
	}
	rawBytes, readErr := os.ReadFile(result.RawOutputPath)
	if readErr != nil {
		t.Fatalf("read raw output: %v", readErr)
	}
	if !strings.Contains(string(rawBytes), "Applied the requested changes.") || !strings.Contains(string(rawBytes), structuredOutputStart) {
		t.Fatalf("raw output must preserve full runner output: %q", string(rawBytes))
	}
	if result.StructuredOutput == nil {
		t.Fatal("structured output must be parsed")
	}
	if result.StructuredOutput.Summary != "Main result." {
		t.Fatalf("unexpected structured summary: %#v", result.StructuredOutput)
	}
	if result.StructuredOutput.CommitMessage != "Document deploy checklist" {
		t.Fatalf("unexpected structured commit message: %#v", result.StructuredOutput)
	}
	if len(result.StructuredOutput.Remarks) != 1 || result.StructuredOutput.Remarks[0].Body != "Document rollback steps." {
		t.Fatalf("unexpected remarks: %#v", result.StructuredOutput.Remarks)
	}
	if len(result.StructuredOutput.Commands) != 1 || result.StructuredOutput.Commands[0].Name != "open-pr" {
		t.Fatalf("unexpected commands: %#v", result.StructuredOutput.Commands)
	}
	if result.StructuredOutput.Conclusion == nil || result.StructuredOutput.Conclusion.Status != "needs-follow-up" {
		t.Fatalf("unexpected conclusion: %#v", result.StructuredOutput.Conclusion)
	}
	if string(result.StructuredOutput.Extensions["custom"]) != `{"owner":"release"}` {
		t.Fatalf("unexpected extensions: %#v", result.StructuredOutput.Extensions)
	}
}

func TestValidateLaunchAcceptsCodexRunner(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, false)
	invocation.Launch.Runner = RunnerCodex

	if err := validateLaunch(invocation, validWorkplace(t)); err != nil {
		t.Fatalf("validate launch: %v", err)
	}
}

func TestBuildRunnerCommandOpenCode(t *testing.T) {
	t.Parallel()

	cmd, err := buildRunnerCommand(context.Background(), model.LaunchSpec{
		Directory: "/tmp/work",
		Runner:    RunnerOpenCode,
		Model:     "openai/gpt-5.4",
	}, "ship it")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	assertRunnerCommand(t, cmd, RunnerOpenCode, []string{"run", "--dir", "/tmp/work", "--model", "openai/gpt-5.4", "ship it"})
}

func TestBuildRunnerCommandCodex(t *testing.T) {
	t.Parallel()

	cmd, err := buildRunnerCommand(context.Background(), model.LaunchSpec{
		Directory: "/tmp/work",
		Runner:    RunnerCodex,
		Model:     "gpt-5.3-codex",
	}, "ship it")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	assertRunnerCommand(t, cmd, RunnerCodex, []string{"exec", "-C", "/tmp/work", "-m", "gpt-5.3-codex", "ship it"})
}

func TestLaunchStructuredOutputInvalidPreservesFreeText(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"remarks":[{}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput != nil {
		t.Fatalf("invalid structured output must not populate fields: %#v", result.StructuredOutput)
	}
	if !strings.Contains(result.Summary, `{"remarks":[{}]}`) {
		t.Fatalf("summary must preserve invalid block for diagnostics: %q", result.Summary)
	}
}

func TestLaunchStructuredOutputRequiredMissingFails(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "Applied the requested changes.", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.StructuredOutputRequired = true

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected required structured output error")
	}
	if !strings.Contains(err.Error(), "structured output is required") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("unexpected result status: %#v", result)
	}
	if result.RunRecordPath == "" {
		t.Fatalf("run record path must be present on required output error")
	}
	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Result.Status != "failed" || !strings.Contains(record.StructuredOutputErr, "missing") {
		t.Fatalf("run record must include required output validation error: %#v", record)
	}
	if !strings.Contains(result.Summary, "Applied the requested changes.") {
		t.Fatalf("summary must preserve plain runner output: %q", result.Summary)
	}
	if strings.TrimSpace(result.RawOutputPath) == "" {
		t.Fatalf("raw output path must be present on failure: %#v", result)
	}
}

func TestLaunchStructuredOutputRequiredFromProfileUsesORSemantics(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "Applied the requested changes.", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), model.Profile{
		Name:                     "review",
		StructuredOutputRequired: true,
	}, validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected required structured output error")
	}
	if !strings.Contains(err.Error(), "structured output is required") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("unexpected result status: %#v", result)
	}
}

func TestLaunchStructuredOutputRequiredInvalidFails(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		payload    string
		expectPart string
	}{
		{
			name:       "empty summary",
			payload:    `{"summary":"ok","summary":""}`,
			expectPart: "structured output must include a non-empty summary",
		},
		{
			name:       "unknown field",
			payload:    `{"summary":"Done.","unknown":true}`,
			expectPart: `unknown field "unknown"`,
		},
		{
			name:       "summary type mismatch",
			payload:    `{"summary":42}`,
			expectPart: "type mismatch at summary: expected string but got number",
		},
		{
			name:       "remarks string type mismatch",
			payload:    `{"summary":"Done.","remarks":"not-an-array"}`,
			expectPart: "type mismatch at remarks: expected array of objects with id/status/severity/type/title/body/answer/resolution but got string",
		},
		{
			name:       "remarks array of strings mismatch",
			payload:    `{"summary":"Done.","remarks":["bad-item"]}`,
			expectPart: "type mismatch at remarks: expected array of objects with id/status/severity/type/title/body/answer/resolution but got string",
		},
		{
			name:       "commands string type mismatch",
			payload:    `{"summary":"Done.","commands":"not-an-array"}`,
			expectPart: "type mismatch at commands: expected array of objects with name/args/title/body but got string",
		},
		{
			name:       "commands array of strings mismatch",
			payload:    `{"summary":"Done.","commands":["bad-item"]}`,
			expectPart: "type mismatch at commands: expected array of objects with name/args/title/body but got string",
		},
		{
			name:       "conclusion string type mismatch",
			payload:    `{"summary":"Done.","conclusion":"not-an-object"}`,
			expectPart: "type mismatch at conclusion: expected object with status/summary/body but got string",
		},
		{
			name:       "meaningless remark object",
			payload:    `{"summary":"Done.","remarks":[{}]}`,
			expectPart: "structured output remarks[0] must include at least one non-empty field",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service := &Service{
				runRunner: func(context.Context, model.Invocation) (string, error) {
					return strings.Join([]string{
						"Applied the requested changes.",
						structuredOutputStart,
						tc.payload,
						structuredOutputEnd,
					}, "\n"), nil
				},
				runGitOutput: func(context.Context, string, ...string) (string, error) {
					t.Fatal("git must not be called when commit-push is disabled")
					return "", nil
				},
			}

			invocation := validInvocation(t, false)
			invocation.Launch.StructuredOutputRequired = true

			result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
			if err == nil {
				t.Fatal("expected required structured output error")
			}
			if !strings.Contains(err.Error(), "structured output is required") || !strings.Contains(err.Error(), tc.expectPart) {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "failed" {
				t.Fatalf("unexpected result status: %#v", result)
			}
			if !strings.Contains(result.Summary, tc.payload) {
				t.Fatalf("summary must preserve invalid structured payload: %q", result.Summary)
			}
		})
	}
}

func TestStructuredInputCanonicalValidatorRejectsEmptyProgrammaticInput(t *testing.T) {
	t.Parallel()

	_, programmaticErr := buildRunnerPrompt(model.LaunchSpec{
		Prompt:          "Apply the fixes.",
		StructuredInput: &model.StructuredInput{},
	})
	if programmaticErr == nil {
		t.Fatal("expected programmatic structured input validation error")
	}

	expectPart := "structured input must include at least one non-empty field"
	if !strings.Contains(programmaticErr.Error(), expectPart) {
		t.Fatalf("unexpected programmatic validation error: %v", programmaticErr)
	}
}

func TestLaunchProgrammaticStructuredInputRejectsEmptyPayload(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			t.Fatal("runner must not be called when structured input is invalid")
			return "", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = ""
	invocation.Launch.StructuredInput = &model.StructuredInput{}

	workplace := validWorkplace(t)

	_, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err == nil {
		t.Fatal("expected invalid structured input error")
	}
	if !strings.Contains(err.Error(), "structured input must include at least one non-empty field") {
		t.Fatalf("unexpected error: %v", err)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10, Status: "failed"})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one failed sqlite history run, got %d", len(runs))
	}
	if runs[0].Model != invocation.Launch.Model || runs[0].LaunchDirectory != invocation.Launch.Directory {
		t.Fatalf("failed invalid structured input row must keep request metadata: %#v", runs[0])
	}
	if runs[0].RawStructuredInput != `{}` {
		t.Fatalf("failed invalid structured input row must keep available structured input: %#v", runs[0])
	}
}

func TestLaunchKeepsProgrammaticStructuredInput(t *testing.T) {
	t.Parallel()

	structuredInput := model.StructuredInput{
		Task:        "Answer review remarks.",
		Constraints: []string{"Do not change public API."},
		ReviewRemarks: []model.StructuredRemark{{
			ID:       "remark-1",
			Severity: "critical",
			Title:    "Rollback plan",
			Body:     "Please add rollback steps.",
		}},
		ReviewResponses: []model.StructuredResponse{{
			RemarkID: "remark-1",
			Summary:  "Will update docs.",
		}},
	}

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			if in.Launch.Prompt != "Reply to the latest review." {
				t.Fatalf("runner must keep direct prompt: %q", in.Launch.Prompt)
			}
			if !reflect.DeepEqual(in.Launch.StructuredInput, &structuredInput) {
				t.Fatalf("unexpected structured input: %#v", in.Launch.StructuredInput)
			}
			return "done", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = "Reply to the latest review."
	invocation.Launch.StructuredInput = &structuredInput

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Invocation.Launch.Prompt != "Reply to the latest review." {
		t.Fatalf("run record must keep direct prompt: %#v", record.Invocation.Launch.Prompt)
	}
	if !reflect.DeepEqual(record.StructuredInput, &structuredInput) {
		t.Fatalf("run record must keep normalized structured input: %#v", record.StructuredInput)
	}
}

func TestBuildRunnerPromptAppendsProgrammaticStructuredInputAndOutputInstruction(t *testing.T) {
	t.Parallel()

	structuredInput := &model.StructuredInput{
		Task:        "Apply the accepted fixes.",
		Constraints: []string{"Do not change the public API."},
		ProjectContext: []model.StructuredContext{{
			Title: "Service",
			Body:  "Execution contour migration.",
		}},
		OperationalContext: []model.StructuredContext{{
			Title: "Branch",
			Body:  "feature/structured-io",
		}},
		PreviousRunResults: []model.StructuredResult{{
			Summary: "Earlier attempt failed validation.",
		}},
		ReviewRemarks: []model.StructuredRemark{{
			ID:       "remark-1",
			Severity: "critical",
			Title:    "Rollback plan",
			Body:     "Please add rollback steps.",
		}},
		ReviewResponses: []model.StructuredResponse{{
			RemarkID: "remark-1",
			Summary:  "Will update docs.",
		}},
		IntegrationActions: []model.StructuredAction{{
			Type:  "github",
			Title: "Open PR after changes",
		}},
	}

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:                 "Apply the latest review fixes.",
		StructuredInput:        structuredInput,
		StructuredOutput:       true,
		StructuredOutputFields: []string{"remarks", "commands"},
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Use every field from the structured input JSON below as execution context.") {
		t.Fatalf("prompt must mention structured input usage: %q", prompt)
	}
	if !strings.Contains(prompt, "Include remarks, commands when they are applicable.") {
		t.Fatalf("prompt must mention selected structured output fields: %q", prompt)
	}
	if !strings.Contains(prompt, "Object forms: remarks[{id,status,severity,type,title,body,answer,resolution}], commands[{name,args,title,body}].") {
		t.Fatalf("prompt must describe selected object forms: %q", prompt)
	}
	if !strings.Contains(prompt, "Canonical compact JSON example:") {
		t.Fatalf("prompt must include canonical compact JSON example: %q", prompt)
	}
	if strings.Contains(prompt, "commit_message") {
		t.Fatalf("prompt must not request unselected structured output fields: %q", prompt)
	}
	if strings.Contains(prompt, "questions[{") || strings.Contains(prompt, "conclusion{") {
		t.Fatalf("prompt must not describe unselected object forms: %q", prompt)
	}

	payload, err := buildStructuredJSON(*structuredInput)
	if err != nil {
		t.Fatalf("marshal structured input: %v", err)
	}
	if !strings.Contains(prompt, payload) {
		t.Fatalf("programmatic structured input must be encoded into runner prompt: %q", prompt)
	}
}

func TestBuildRunnerPromptPlacesPromptAdditionsBeforeStructuredSections(t *testing.T) {
	t.Parallel()

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:                 "Review PR #38.",
		PromptAdditions:        []string{"Collect PR context first.", "Do not modify code."},
		StructuredInput:        &model.StructuredInput{Task: "Check review remarks."},
		StructuredOutput:       true,
		StructuredOutputFields: []string{"remarks", "conclusion"},
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}

	userIndex := strings.Index(prompt, "Review PR #38.")
	additionIndex := strings.Index(prompt, "Collect PR context first.")
	secondAdditionIndex := strings.Index(prompt, "Do not modify code.")
	structuredOutputIndex := strings.Index(prompt, "Return your normal answer")
	structuredInputIndex := strings.Index(prompt, `{"task":"Check review remarks."}`)
	if userIndex == -1 || additionIndex == -1 || secondAdditionIndex == -1 || structuredOutputIndex == -1 || structuredInputIndex == -1 {
		t.Fatalf("prompt missing expected sections: %q", prompt)
	}
	if !(userIndex < additionIndex && additionIndex < secondAdditionIndex && secondAdditionIndex < structuredOutputIndex && structuredOutputIndex < structuredInputIndex) {
		t.Fatalf("prompt sections must keep expected order, got %q", prompt)
	}
}

func TestLaunchAppliesProfilePromptAdditionsToRunnerPrompt(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			prompt, err := buildRunnerPrompt(in.Launch)
			if err != nil {
				t.Fatalf("buildRunnerPrompt inside runner stub: %v", err)
			}
			if !strings.Contains(prompt, "Collect PR, issue, diff, and previous review comments first.") {
				t.Fatalf("prompt must include profile prompt additions: %q", prompt)
			}
			if !strings.Contains(prompt, "Do not modify code.") {
				t.Fatalf("prompt must include all profile prompt additions: %q", prompt)
			}
			return "review complete", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = "Review PR #38."
	if _, err := service.Launch(context.Background(), invocation, model.Profile{
		Name:            "review",
		PromptAdditions: []string{"Collect PR, issue, diff, and previous review comments first.", "Do not modify code."},
	}, validAllocation(), validWorkplace(t)); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

func TestBuildRunnerPromptKeepsFullFieldListWhenSelectionNotConfigured(t *testing.T) {
	t.Parallel()

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:           "Apply the latest review fixes.",
		StructuredOutput: true,
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Include commit_message, remarks, questions, follow_up_actions, changes, commands, conclusion, extensions when they are applicable.") {
		t.Fatalf("prompt must keep full optional field list by default: %q", prompt)
	}
	if !strings.Contains(prompt, "Object forms: remarks[{id,status,severity,type,title,body,answer,resolution}], questions[{id,status,title,body,answer}], follow_up_actions[{id,status,type,title,body}], changes[{summary}], commands[{name,args,title,body}], conclusion{status,summary,body}.") {
		t.Fatalf("prompt must keep full object forms when selection is not configured: %q", prompt)
	}
}

func TestBuildRunnerPromptAllowsNoOptionalStructuredOutputFields(t *testing.T) {
	t.Parallel()

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:                 "Apply the latest review fixes.",
		StructuredOutput:       true,
		StructuredOutputFields: []string{},
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}
	if strings.Contains(prompt, "Include ") {
		t.Fatalf("prompt must omit optional field instruction when the selection is explicitly empty: %q", prompt)
	}
	if strings.Contains(prompt, "Object forms:") {
		t.Fatalf("prompt must omit object forms when no optional fields are selected: %q", prompt)
	}
	if !strings.Contains(prompt, "Canonical compact JSON example: {\"summary\":\"Implemented changes.\"}.") {
		t.Fatalf("prompt must keep compact mandatory-only example when selection is empty: %q", prompt)
	}
}

func TestBuildRunnerPromptTreatsSummaryAsMandatoryEvenWhenSelected(t *testing.T) {
	t.Parallel()

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:                 "Apply the latest review fixes.",
		StructuredOutput:       true,
		StructuredOutputFields: []string{"summary"},
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}
	if !strings.Contains(prompt, `a non-empty summary field`) {
		t.Fatalf("prompt must keep summary as mandatory: %q", prompt)
	}
	if strings.Contains(prompt, "Include ") {
		t.Fatalf("prompt must not treat summary as an optional field: %q", prompt)
	}
}

func TestLaunchAppliesProfileStructuredOutputFieldSelectionToPromptOnly(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			prompt, err := buildRunnerPrompt(in.Launch)
			if err != nil {
				t.Fatalf("buildRunnerPrompt inside runner stub: %v", err)
			}
			if !strings.Contains(prompt, "Include remarks, commands when they are applicable.") {
				t.Fatalf("prompt must request only selected fields: %q", prompt)
			}
			if strings.Contains(prompt, "commit_message") {
				t.Fatalf("prompt must not request unselected fields: %q", prompt)
			}

			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"summary":"Done.","commit_message":"Extra fields are still accepted.","remarks":[{"title":"Rollback plan"}],"commands":[{"name":"open-pr"}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), model.Profile{
		Name:                   "review",
		StructuredOutput:       true,
		StructuredOutputFields: []string{"remarks", "commands"},
	}, validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput == nil {
		t.Fatal("structured output must still parse")
	}
	if result.StructuredOutput.CommitMessage != "Extra fields are still accepted." {
		t.Fatalf("parser must still accept extra canonical sections: %#v", result.StructuredOutput)
	}
}

func TestLaunchStructuredOutputWithLiteralTagInsidePayload(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"summary":"Done.","remarks":[{"id":"remark-1","title":"Literal tag","body":"Keep literal <progress-structured-output> inside payload."}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput == nil || len(result.StructuredOutput.Remarks) != 1 || result.StructuredOutput.Remarks[0].Body != "Keep literal <progress-structured-output> inside payload." {
		t.Fatalf("structured output with literal tag inside payload must still parse: %#v", result.StructuredOutput)
	}
}

func TestLaunchBrokenBlockBeforeValidTrailingBlock(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Broken example in prose:",
				structuredOutputStart,
				`{`,
				"Applied the requested changes.",
				structuredOutputStart,
				`{"summary":"Done.","remarks":[{"title":"Rollback plan","body":"missing rollback plan"}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput == nil || len(result.StructuredOutput.Remarks) != 1 {
		t.Fatalf("unexpected structured output: %#v", result.StructuredOutput)
	}
	if strings.Contains(result.Summary, `{`) {
		t.Fatalf("successful structured run summary must stay compact: %q", result.Summary)
	}
}

func TestLaunchInvalidTrailingBlockWinsOverEarlierValidBlock(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"summary":"Earlier valid block."}`,
				structuredOutputEnd,
				structuredOutputStart,
				`{"remarks":"broken trailing block"}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput != nil {
		t.Fatalf("invalid trailing block must suppress structured extraction: %#v", result.StructuredOutput)
	}
	if !strings.Contains(result.Summary, `{"summary":"Earlier valid block."}`) {
		t.Fatalf("summary must preserve earlier valid block when trailing block is invalid: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, `{"remarks":"broken trailing block"}`) {
		t.Fatalf("summary must preserve invalid trailing block for diagnostics: %q", result.Summary)
	}
}

type persistedLaunchRunRecord struct {
	CreatedAt           string                  `json:"created_at"`
	Invocation          model.Invocation        `json:"invocation"`
	Profile             model.Profile           `json:"profile"`
	Allocation          model.Allocation        `json:"allocation"`
	Workplace           model.Workplace         `json:"workplace"`
	StructuredInput     *model.StructuredInput  `json:"structured_input,omitempty"`
	RawStructuredOutput string                  `json:"raw_structured_output"`
	StructuredOutput    *model.StructuredOutput `json:"structured_output,omitempty"`
	StructuredOutputErr string                  `json:"structured_output_error,omitempty"`
	Error               string                  `json:"error,omitempty"`
	Result              struct {
		Status        string `json:"status"`
		Summary       string `json:"summary"`
		RawOutputPath string `json:"raw_output_path"`
	} `json:"result"`
}

func readLaunchRunRecord(t *testing.T, path string) persistedLaunchRunRecord {
	t.Helper()

	if strings.TrimSpace(path) == "" {
		t.Fatalf("expected run record path")
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run record: %v", err)
	}

	var record persistedLaunchRunRecord
	if err := json.Unmarshal(bytes, &record); err != nil {
		t.Fatalf("parse run record: %v", err)
	}

	if strings.TrimSpace(record.CreatedAt) == "" {
		t.Fatalf("run record missing created_at")
	}

	return record
}

func validInvocation(t *testing.T, commitPush bool) model.Invocation {
	t.Helper()

	return model.Invocation{
		Launch: model.LaunchSpec{
			Directory:  tempDir(t),
			Runner:     RunnerOpenCode,
			Model:      "openai/gpt-5.4",
			Prompt:     "do work",
			CommitPush: commitPush,
		},
	}
}

func validProfile() model.Profile {
	return model.Profile{Name: "default"}
}

func validAllocation() model.Allocation {
	return model.Allocation{Resource: "external-launch", Reserved: true, Runner: RunnerOpenCode, Model: "openai/gpt-5.4"}
}

func validWorkplace(t *testing.T) model.Workplace {
	t.Helper()
	return model.Workplace{Name: tempDir(t), Ready: true}
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir temp repo: %v", err)
	}
	return dir
}

func buildStructuredJSON(value any) (string, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func assertRunnerCommand(t *testing.T, cmd *exec.Cmd, expectedPath string, expectedArgs []string) {
	t.Helper()
	if filepath.Base(cmd.Path) != expectedPath {
		t.Fatalf("unexpected command path: %q", cmd.Path)
	}
	if !reflect.DeepEqual(cmd.Args, append([]string{expectedPath}, expectedArgs...)) {
		t.Fatalf("unexpected command args: %#v", cmd.Args)
	}
}
