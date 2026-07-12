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
	"time"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	codexRunnerStartupTimeout = "5s"
	codexRunnerTimeout        = "10s"
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

func TestRunRunnerCommandStopsOnOverallTimeoutAndKeepsOutput(t *testing.T) {
	t.Parallel()

	output, err := runRunnerCommand(context.Background(), exec.Command("sh", "-c", "printf partial; sleep 1"), model.LaunchSpec{
		Timeout:         "50ms",
		NoOutputTimeout: "1s",
	})
	if !errors.Is(err, errRunnerTimeout) {
		t.Fatalf("expected overall timeout, got %v", err)
	}
	if output != "" {
		t.Fatalf("timeout output must be returned through runner error, got %q", output)
	}
	var runnerErr *runnerExecutionError
	if !errors.As(err, &runnerErr) || runnerErr.output != "partial" {
		t.Fatalf("partial output was not preserved: %#v", runnerErr)
	}
}

func TestRunRunnerCommandStopsOnNoOutputTimeout(t *testing.T) {
	t.Parallel()

	_, err := runRunnerCommand(context.Background(), exec.Command("sh", "-c", "printf initial; sleep 1"), model.LaunchSpec{
		Timeout:         "1s",
		NoOutputTimeout: "50ms",
	})
	if !errors.Is(err, errRunnerNoOutputTimeout) {
		t.Fatalf("expected no-output timeout, got %v", err)
	}
	var runnerErr *runnerExecutionError
	if !errors.As(err, &runnerErr) || runnerErr.output != "initial" {
		t.Fatalf("partial output was not preserved: %#v", runnerErr)
	}
}

func TestRunRunnerCommandStopsOnNoOutputTimeoutWithoutOutput(t *testing.T) {
	t.Parallel()

	startedAt := time.Now()
	_, err := runRunnerCommand(context.Background(), exec.Command("sh", "-c", "sleep 1"), model.LaunchSpec{
		Timeout:         "1s",
		NoOutputTimeout: "50ms",
	})
	if !errors.Is(err, errRunnerNoOutputTimeout) {
		t.Fatalf("expected no-output timeout, got %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Fatalf("silent runner was allowed to run until overall timeout: %s", elapsed)
	}
	var runnerErr *runnerExecutionError
	if !errors.As(err, &runnerErr) || runnerErr.output != "" || !runnerErr.lastOutputAt.IsZero() {
		t.Fatalf("unexpected silent-runner diagnostics: %#v", runnerErr)
	}
}

func TestRunRunnerCommandUsesStartupTimeoutBeforeFirstOutput(t *testing.T) {
	t.Parallel()

	output, err := runRunnerCommand(context.Background(), exec.Command("sh", "-c", "sleep .1; printf ready"), model.LaunchSpec{
		Timeout:         "1s",
		StartupTimeout:  "200ms",
		NoOutputTimeout: "20ms",
	})
	if err != nil {
		t.Fatalf("startup delay within startup timeout must be allowed: %v", err)
	}
	if output != "ready" {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunRunnerCommandKeepsStreamingOutputActive(t *testing.T) {
	t.Parallel()

	output, err := runRunnerCommand(context.Background(), exec.Command("sh", "-c", "for value in 1 2 3 4 5 6; do printf '%s\\n' \"$value\"; sleep .05; done"), model.LaunchSpec{
		Timeout:         "1s",
		NoOutputTimeout: "150ms",
	})
	if err != nil {
		t.Fatalf("streaming output must keep runner active: %v", err)
	}
	if output != "1\n2\n3\n4\n5\n6\n" {
		t.Fatalf("unexpected streaming output: %q", output)
	}
}

func TestRunRunnerCommandNoOutputTimeoutUsesLastFragment(t *testing.T) {
	t.Parallel()

	_, err := runRunnerCommand(context.Background(), exec.Command("sh", "-c", "printf first; sleep .5"), model.LaunchSpec{
		Timeout:         "1s",
		NoOutputTimeout: "200ms",
	})
	if !errors.Is(err, errRunnerNoOutputTimeout) {
		t.Fatalf("expected no-output timeout, got %v", err)
	}
	var runnerErr *runnerExecutionError
	if !errors.As(err, &runnerErr) || runnerErr.output != "first" || runnerErr.lastOutputAt.IsZero() {
		t.Fatalf("missing last-output diagnostics: %#v", runnerErr)
	}
}

func TestResetRunnerWatchdogUsesLastOutputTime(t *testing.T) {
	t.Parallel()

	watchdog := time.NewTimer(time.Hour)
	defer watchdog.Stop()
	lastOutputAt := time.Now().Add(-80 * time.Millisecond)

	resetRunnerWatchdog(watchdog, 100*time.Millisecond, lastOutputAt, time.Now())

	select {
	case <-watchdog.C:
	case <-time.After(70 * time.Millisecond):
		t.Fatal("watchdog was reset from the stale activity signal instead of last output time")
	}
}

func TestRunCodexRunnerStreamsJSONEventsBeforeProcessExit(t *testing.T) {
	codexPath, err := filepath.Abs(filepath.Join("testdata", RunnerCodex))
	if err != nil {
		t.Fatalf("resolve codex stand-in: %v", err)
	}
	startedAt := time.Now()
	spec := model.LaunchSpec{
		Directory:       t.TempDir(),
		Runner:          RunnerCodex,
		Model:           "openai/gpt-5.4",
		Timeout:         codexRunnerTimeout,
		StartupTimeout:  codexRunnerStartupTimeout,
		NoOutputTimeout: "500ms",
	}
	cmd := exec.Command(codexPath, "exec", "-C", spec.Directory, "-m", "gpt-5.4", "stream")
	output, err := runCodexRunnerCommand(context.Background(), spec, cmd)
	if err != nil {
		t.Fatalf("codex JSON stream must keep runner active: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed < 500*time.Millisecond {
		t.Fatalf("codex runner finished before the streaming process: %s", elapsed)
	}
	if output != "1\n2\n3\n4\n5\n6" {
		t.Fatalf("unexpected normalized codex output: %q", output)
	}
}

func TestRunnerOutputWriterSnapshotsBeforeProcessExit(t *testing.T) {
	writer := &runnerOutputWriter{activity: make(chan struct{}, 1)}
	processExit := make(chan struct{})
	go func() {
		_, _ = writer.Write([]byte(`{"type":"item.completed"}\n`))
		<-processExit
	}()

	select {
	case <-writer.activity:
		output, lastOutputAt := writer.snapshot()
		if output != `{"type":"item.completed"}\n` || lastOutputAt.IsZero() {
			t.Fatalf("output was not accumulated before process exit: %q, %v", output, lastOutputAt)
		}
	case <-time.After(time.Second):
		t.Fatal("output was not reported before process exit")
	}
	close(processExit)
}

func TestRunCodexRunnerPreservesEventsOnNoOutputTimeout(t *testing.T) {
	codexPath, err := filepath.Abs(filepath.Join("testdata", RunnerCodex))
	if err != nil {
		t.Fatalf("resolve codex stand-in: %v", err)
	}
	spec := model.LaunchSpec{
		Directory:               t.TempDir(),
		Runner:                  RunnerCodex,
		Model:                   "openai/gpt-5.4",
		Timeout:                 codexRunnerTimeout,
		StartupTimeout:          codexRunnerStartupTimeout,
		NoOutputTimeout:         "300ms",
		StructuredOutputTimeout: "300ms",
	}
	cmd := exec.Command(codexPath, "exec", "-C", spec.Directory, "-m", "gpt-5.4", "timeout")
	_, err = runCodexRunnerCommand(context.Background(), spec, cmd)
	if !errors.Is(err, errRunnerNoOutputTimeout) {
		t.Fatalf("expected no-output timeout, got %v", err)
	}
	var runnerErr *runnerExecutionError
	if !errors.As(err, &runnerErr) || !strings.Contains(runnerErr.output, `"thread_id":"thread-test"`) || runnerErr.lastOutputAt.IsZero() {
		t.Fatalf("codex events were not preserved before Wait: %#v", runnerErr)
	}
	for _, expected := range []string{"last structured event=item.completed/agent_message", "idle=", "rule=300ms после структурированного события"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("timeout diagnostics must include %q: %v", expected, err)
		}
	}
}

func TestRunCodexRunnerAllowsPauseAfterStructuredFileChange(t *testing.T) {
	codexPath, err := filepath.Abs(filepath.Join("testdata", RunnerCodex))
	if err != nil {
		t.Fatalf("resolve codex stand-in: %v", err)
	}
	spec := model.LaunchSpec{
		Directory:               t.TempDir(),
		Runner:                  RunnerCodex,
		Model:                   "openai/gpt-5.4",
		Timeout:                 codexRunnerTimeout,
		StartupTimeout:          codexRunnerStartupTimeout,
		NoOutputTimeout:         "50ms",
		StructuredOutputTimeout: "300ms",
	}
	cmd := exec.Command(codexPath, "exec", "-C", spec.Directory, "-m", "gpt-5.4", "pause")
	output, err := runCodexRunnerCommand(context.Background(), spec, cmd)
	if err != nil {
		t.Fatalf("structured event pause must be allowed: %v", err)
	}
	if !strings.Contains(output, "continued") {
		t.Fatalf("output after structured event was lost: %q", output)
	}
}

func TestRunCodexRunnerDoesNotKeepStructuredTimeoutAfterItsDeadline(t *testing.T) {
	codexPath, err := filepath.Abs(filepath.Join("testdata", RunnerCodex))
	if err != nil {
		t.Fatalf("resolve codex stand-in: %v", err)
	}
	spec := model.LaunchSpec{
		Directory:               t.TempDir(),
		Runner:                  RunnerCodex,
		Model:                   "openai/gpt-5.4",
		Timeout:                 codexRunnerTimeout,
		StartupTimeout:          codexRunnerStartupTimeout,
		NoOutputTimeout:         "50ms",
		StructuredOutputTimeout: "200ms",
	}
	startedAt := time.Now()
	cmd := exec.Command(codexPath, "exec", "-C", spec.Directory, "-m", "gpt-5.4", "stale-event")
	_, err = runCodexRunnerCommand(context.Background(), spec, cmd)
	if !errors.Is(err, errRunnerNoOutputTimeout) {
		t.Fatalf("expected no-output timeout, got %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 320*time.Millisecond {
		t.Fatalf("expired structured event kept extending the watchdog: %s", elapsed)
	}
	for _, expected := range []string{"last structured event=item.completed/file_change", "rule=50ms после последнего фрагмента"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("timeout diagnostics must include %q: %v", expected, err)
		}
	}
}

func TestLaunchPersistsTimedOutRunnerOutputAndHistory(t *testing.T) {
	t.Parallel()

	workplace := validWorkplace(t)
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "", &runnerExecutionError{err: errRunnerTimeout, output: "partial output"}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), workplace)
	if !errors.Is(err, errRunnerTimeout) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if result.Status != "timeout" || result.RawOutput != "partial output" {
		t.Fatalf("unexpected timeout result: %#v", result)
	}
	if result.RawOutputPath == "" || result.RunRecordPath == "" {
		t.Fatalf("timeout diagnostics must be persisted: %#v", result)
	}
	rawOutput, readErr := os.ReadFile(result.RawOutputPath)
	if readErr != nil || string(rawOutput) != "partial output" {
		t.Fatalf("unexpected persisted runner output: %q, %v", rawOutput, readErr)
	}
	runs, listErr := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 1})
	if listErr != nil || len(runs) != 1 || runs[0].Status != "timeout" || runs[0].RunRecordPath != result.RunRecordPath {
		t.Fatalf("timeout history was not finalized: %#v, %v", runs, listErr)
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

func TestLaunchRecordsInterruptedRunOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		runRunner: func(ctx context.Context, invocation model.Invocation) (string, error) {
			if err := os.WriteFile(filepath.Join(invocation.Launch.Directory, "partial.txt"), []byte("partial result"), 0o644); err != nil {
				return "", err
			}
			cancel()
			<-ctx.Done()
			return "", ctx.Err()
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "status --porcelain -z -uall":
				return " M partial.txt\x00?? notes.txt\x00", nil
			case "branch --show-current":
				return "136\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	invocation := validInvocation(t, false)
	workplace := validWorkplace(t)
	result, err := service.Launch(ctx, invocation, validProfile(), validAllocation(), workplace)
	if err == nil {
		t.Fatal("expected interrupted launch error")
	}
	if result.Status != "interrupted" || strings.TrimSpace(result.RunRecordPath) == "" {
		t.Fatalf("interrupted result must include status and run record path: %#v", result)
	}

	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.Result.Status != "interrupted" || strings.TrimSpace(record.Error) == "" {
		t.Fatalf("interrupted run record must keep diagnostic details: %#v", record)
	}
	if record.Result.WorktreeDiagnostic == nil || !record.Result.WorktreeDiagnostic.DirtyWorktree || !reflect.DeepEqual(record.Result.WorktreeDiagnostic.ChangedPaths, []string{"partial.txt", "notes.txt"}) {
		t.Fatalf("interrupted run record must keep dirty-worktree diagnostic: %#v", record.Result)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10, Status: "interrupted"})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one interrupted sqlite history run, got %d", len(runs))
	}
	if runs[0].Error == "" || runs[0].RunRecordPath == "" {
		t.Fatalf("interrupted sqlite row must keep error and run record path: %#v", runs[0])
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
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				statusCalls++
				if statusCalls == 1 {
					return " M file.txt\x00", nil
				}
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
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
		{"rev-parse", "--show-toplevel"},
		{"branch", "--show-current"},
		{"status", "--porcelain", "-z", "-uall"},
		{"add", "-A", "--", "file.txt"},
		{"status", "--porcelain", "-z", "-uall"},
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

func TestLaunchCommitPushFailureKeepsRunnerSessionID(t *testing.T) {
	t.Parallel()

	worktree := tempDir(t)
	invocation := validInvocation(t, true)
	invocation.Launch.Directory = worktree
	workplace := model.Workplace{Name: worktree, Ready: true}

	service := NewService()
	service.runRunner = func(context.Context, model.Invocation) (string, error) {
		return appendTrailingRunnerMetadata(strings.Join([]string{
			"runner output",
			structuredOutputStart,
			`{"summary":"Done.","commit_message":"Ship result"}`,
			structuredOutputEnd,
		}, "\n"), runnerMetadata{RunnerSessionID: "session-commit-failure"}), nil
	}
	service.runGitOutput = func(_ context.Context, _ string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "rev-parse --is-inside-work-tree":
			return "true\n", nil
		case "rev-parse --show-toplevel":
			return worktree + "\n", nil
		case "branch --show-current":
			return "feature/test\n", nil
		case "status --porcelain -z -uall":
			return " M file.txt\x00", nil
		case "add -A -- file.txt":
			return "", fmt.Errorf("git add failed")
		default:
			return "", fmt.Errorf("unexpected git command: %v", args)
		}
	}

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err == nil {
		t.Fatal("expected commit-push failure")
	}
	if result.RunnerSessionID != "session-commit-failure" {
		t.Fatalf("runner session id must survive commit-push failure: %#v", result)
	}

	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.RunnerSessionID != "session-commit-failure" {
		t.Fatalf("run record must keep runner session id: %#v", record)
	}
	if record.Result.WorktreeDiagnostic == nil || !record.Result.WorktreeDiagnostic.DirtyWorktree || record.Result.WorktreeDiagnostic.Path != workplace.Name {
		t.Fatalf("commit-push failure must keep dirty-worktree diagnostic: %#v", record.Result)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 || runs[0].RunnerSessionID != "session-commit-failure" {
		t.Fatalf("sqlite history must keep runner session id: %#v", runs)
	}
}

func TestEnrichFailedLaunchWithWorktreeUsesWorkplacePath(t *testing.T) {
	t.Parallel()

	workplacePath := filepath.Join(t.TempDir(), "workplace")
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	var inspectedPath string
	service := &Service{
		runGitOutput: func(_ context.Context, path string, args ...string) (string, error) {
			inspectedPath = path
			switch strings.Join(args, " ") {
			case "status --porcelain -z -uall":
				return " M partial.go\x00", nil
			case "branch --show-current":
				return "136\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}
	result := model.LaunchResult{Status: "failed", Summary: "runner failed"}
	enrichFailedLaunchWithWorktree(context.Background(), service, &result, model.Workplace{Name: workplacePath, RepositoryRoot: repositoryRoot})
	if inspectedPath != workplacePath || result.WorktreeDiagnostic == nil || result.WorktreeDiagnostic.Path != workplacePath {
		t.Fatalf("diagnostic must inspect workplace path: path=%q diagnostic=%#v", inspectedPath, result.WorktreeDiagnostic)
	}
}

func TestEnrichFailedLaunchWithWorktreeUsesIndependentDeadline(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	var deadline time.Time
	service := &Service{
		runGitOutput: func(ctx context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "status --porcelain -z -uall", "branch --show-current":
				var ok bool
				deadline, ok = ctx.Deadline()
				if !ok {
					t.Fatal("worktree diagnostic must have a deadline")
				}
				if ctx.Err() != nil {
					t.Fatalf("worktree diagnostic must not inherit parent cancellation: %v", ctx.Err())
				}
				return "", nil
			default:
				t.Fatalf("unexpected git command: %v", args)
				return "", nil
			}
		},
	}

	result := model.LaunchResult{Status: "failed"}
	enrichFailedLaunchWithWorktree(parent, service, &result, model.Workplace{Name: t.TempDir()})
	if deadline.IsZero() {
		t.Fatal("worktree diagnostic deadline was not captured")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > worktreeDiagnosticTimeout {
		t.Fatalf("unexpected worktree diagnostic deadline: %v", remaining)
	}
}

func TestLaunchCommitPushRebindsMismatchedUpstream(t *testing.T) {
	t.Parallel()

	worktree := tempDir(t)
	invocation := validInvocation(t, true)
	invocation.Launch.Directory = worktree
	workplace := model.Workplace{Name: worktree, Ready: true}

	var pushArgs []string
	service := NewService()
	service.runRunner = func(context.Context, model.Invocation) (string, error) {
		return strings.Join([]string{
			"runner output",
			structuredOutputStart,
			`{"summary":"Done.","commit_message":"Ship result"}`,
			structuredOutputEnd,
		}, "\n"), nil
	}
	statusCalls := 0
	service.runGitOutput = func(_ context.Context, _ string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "rev-parse --is-inside-work-tree":
			return "true\n", nil
		case "rev-parse --show-toplevel":
			return worktree + "\n", nil
		case "branch --show-current":
			return "feature/test\n", nil
		case "status --porcelain -z -uall":
			statusCalls++
			if statusCalls == 1 {
				return " M file.txt\x00", nil
			}
			return "M  file.txt\x00", nil
		case "add -A -- file.txt":
			return "", nil
		case "commit -m Ship result":
			return "[feature/test abc123] Ship result\n", nil
		case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
			return "origin/main\n", nil
		case "push -u origin feature/test":
			pushArgs = append([]string(nil), args...)
			return "branch 'feature/test' set up to track 'origin/feature/test'.\n", nil
		default:
			return "", fmt.Errorf("unexpected git command: %v", args)
		}
	}

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=committed+pushed branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if !reflect.DeepEqual(pushArgs, []string{"push", "-u", "origin", "feature/test"}) {
		t.Fatalf("push must rebind mismatched upstream: %#v", pushArgs)
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
			case "rev-parse --show-toplevel":
				return worktree + "\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				if addArgs == nil {
					return " M file.txt\x00!! .progress/runner-output/\x00!! .progress/execution-runs/\x00", nil
				}
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
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
	if !reflect.DeepEqual(addArgs, []string{"add", "-A", "--", "file.txt"}) {
		t.Fatalf("git add must exclude raw runner output path: %#v", addArgs)
	}
}

func TestLaunchCommitPushStagesPathsFromRepositoryRootForSubdirectoryLaunch(t *testing.T) {
	t.Parallel()

	repoRoot := tempDir(t)
	launchDir := filepath.Join(repoRoot, "sub")
	if err := os.MkdirAll(launchDir, 0o755); err != nil {
		t.Fatalf("mkdir launch subdir: %v", err)
	}

	invocation := validInvocation(t, true)
	invocation.Launch.Directory = launchDir

	var statusDirs []string
	var addDir string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, dir string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "rev-parse --show-toplevel":
				return repoRoot + "\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				statusDirs = append(statusDirs, dir)
				if addDir == "" {
					return " M sub/file.txt\x00", nil
				}
				return "M  sub/file.txt\x00", nil
			case "add -A -- sub/file.txt":
				addDir = dir
				return "", nil
			case "commit -m repo":
				return "[feature/test abc123] repo\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: dir=%q args=%v", dir, args)
			}
		},
	}

	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), model.Workplace{Name: repoRoot, Ready: true}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !reflect.DeepEqual(statusDirs, []string{repoRoot, repoRoot}) {
		t.Fatalf("status must run from repository root: %#v", statusDirs)
	}
	if addDir != repoRoot {
		t.Fatalf("git add must run from repository root: %q", addDir)
	}
}

func TestLaunchCommitPushUsesUAllForVisibleProgressFiles(t *testing.T) {
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
			case "rev-parse --show-toplevel":
				return worktree + "\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				if addArgs == nil {
					return "?? .progress/execution/profiles.json\x00?? .progress/runner-output/raw-output.txt\x00", nil
				}
				return "A  .progress/execution/profiles.json\x00", nil
			case "add -A -- .progress/execution/profiles.json":
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
	if !reflect.DeepEqual(addArgs, []string{"add", "-A", "--", ".progress/execution/profiles.json"}) {
		t.Fatalf("git add must keep visible progress config only: %#v", addArgs)
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
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
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
			case "rev-parse --show-toplevel":
				return worktreeDir + "\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
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
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "", nil
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
		{"rev-parse", "--show-toplevel"},
		{"branch", "--show-current"},
		{"status", "--porcelain", "-z", "-uall"},
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
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "?? .progress/runner-output/raw-output.txt\x00", nil
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
		{"rev-parse", "--show-toplevel"},
		{"branch", "--show-current"},
		{"status", "--porcelain", "-z", "-uall"},
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
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "?? .progress/execution-runs/execution-123.json\x00", nil
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
		{"rev-parse", "--show-toplevel"},
		{"branch", "--show-current"},
		{"status", "--porcelain", "-z", "-uall"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushKeepsProgressConfigVisibleAsUserChange(t *testing.T) {
	t.Parallel()

	paths := userChangedPathsFromPorcelain(strings.Join([]string{
		" M .progress/execution/profiles.json",
		" M .progress/decision/workflows.json",
		"?? .progress/execution-runs/execution-123.json",
		" M .progress/execution-runs/execution.db",
		" M .progress/runner-output/execution-123.log",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(paths, []string{".progress/execution/profiles.json", ".progress/decision/workflows.json"}) {
		t.Fatalf("unexpected visible progress paths: %#v", paths)
	}
}

func TestLaunchCommitPushKeepsTrackedRuntimeDeletionSeparateFromNewRuntimeFile(t *testing.T) {
	t.Parallel()

	paths, deletionPaths := userChangedPathsForCommitFromPorcelain(strings.Join([]string{
		" D .progress/execution-runs/execution.db",
		"?? .progress/execution-runs/execution-123.json",
		" M file.txt",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(paths, []string{"file.txt"}) {
		t.Fatalf("unexpected visible paths: %#v", paths)
	}
	if !reflect.DeepEqual(deletionPaths, []string{".progress/execution-runs/execution.db"}) {
		t.Fatalf("unexpected tracked deletion paths: %#v", deletionPaths)
	}
}

func TestLaunchCommitPushSeparatesPreparedAndUnpreparedRuntimeDeletions(t *testing.T) {
	t.Parallel()

	paths, deletionPaths := userChangedPathsForCommitFromPorcelain(strings.Join([]string{
		"D  .progress/execution-runs/prepared.db",
		" D .progress/execution-runs/unprepared.db",
		" M file.txt",
		"?? .progress/execution-runs/execution-123.json",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(paths, []string{"file.txt"}) {
		t.Fatalf("unexpected visible paths: %#v", paths)
	}
	if !reflect.DeepEqual(deletionPaths, []string{".progress/execution-runs/unprepared.db"}) {
		t.Fatalf("unexpected unprepared deletion paths: %#v", deletionPaths)
	}
	changes := commitChangesForPorcelain(strings.Join([]string{
		"D  .progress/execution-runs/prepared.db",
		" D .progress/execution-runs/unprepared.db",
		" M file.txt",
		"?? .progress/execution-runs/execution-123.json",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(changes.stagedRuntimeDeletions, []string{".progress/execution-runs/prepared.db"}) {
		t.Fatalf("unexpected prepared deletion paths: %#v", changes.stagedRuntimeDeletions)
	}
}

func TestCommitAndPushStagesTrackedRuntimeDeletionWithoutAddingNewRuntimeFile(t *testing.T) {
	t.Parallel()

	var calls [][]string
	statusCalls := 0
	service := &Service{
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				statusCalls++
				if statusCalls == 1 {
					return "D  .progress/execution-runs/execution.db\x00?? .progress/execution-runs/execution-123.json\x00 M file.txt\x00", nil
				}
				return "D  .progress/execution-runs/execution.db\x00M  file.txt\x00", nil
			case "add -A -- file.txt":
				return "", nil
			case "commit -m remove execution database":
				return "[feature/test abc123] remove execution database\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.CommitAndPush(context.Background(), model.CommitPushInput{
		Directory:     "/repo",
		CommitMessage: "remove execution database",
	})
	if err != nil {
		t.Fatalf("commit and push: %v", err)
	}
	if !strings.Contains(result, "committed+pushed") {
		t.Fatalf("unexpected result: %q", result)
	}
	for _, call := range calls {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "add -u") {
			t.Fatalf("prepared runtime deletion must not be added again: %#v", call)
		}
		if strings.Contains(joined, "execution-123.json") {
			t.Fatalf("new runtime file must not be added: %#v", call)
		}
	}
}

func TestChangedPathsForCommitRecognizesNestedRuntimeDeletion(t *testing.T) {
	t.Parallel()

	repo := tempDir(t)
	runGitTestCommand(t, repo, "init")
	runGitTestCommand(t, repo, "config", "user.email", "test@example.com")
	runGitTestCommand(t, repo, "config", "user.name", "Test User")

	trackedPath := filepath.Join(repo, "internal", "execution", ".progress", "execution-runs", "execution.db")
	if err := os.MkdirAll(filepath.Dir(trackedPath), 0o755); err != nil {
		t.Fatalf("mkdir tracked path: %v", err)
	}
	if err := os.WriteFile(trackedPath, []byte("database"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	runGitTestCommand(t, repo, "add", "--", "internal/execution/.progress/execution-runs/execution.db")
	runGitTestCommand(t, repo, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("internal/execution/.progress/execution-runs/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runGitTestCommand(t, repo, "add", ".gitignore")
	runGitTestCommand(t, repo, "commit", "-m", "ignore runtime files")
	if err := os.Remove(trackedPath); err != nil {
		t.Fatalf("remove tracked file: %v", err)
	}
	ignoredPath := filepath.Join(repo, "internal", "execution", ".progress", "execution-runs", "new.db")
	if err := os.WriteFile(ignoredPath, []byte("new database"), 0o644); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	paths, deletionPaths, err := NewService().changedPathsForCommit(context.Background(), repo)
	if err != nil {
		t.Fatalf("inspect changes: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("new ignored file must not be visible: %#v", paths)
	}
	if !reflect.DeepEqual(deletionPaths, []string{"internal/execution/.progress/execution-runs/execution.db"}) {
		t.Fatalf("unexpected nested deletion paths: %#v", deletionPaths)
	}
}

func TestLaunchCommitPushDropsCollapsedProgressDirectoryPath(t *testing.T) {
	t.Parallel()

	paths := userChangedPathsFromPorcelain(strings.Join([]string{
		"?? .progress/",
		"?? .progress/execution/profiles.json",
		"?? .progress/runner-output/raw-output.txt",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(paths, []string{".progress/execution/profiles.json"}) {
		t.Fatalf("unexpected visible progress paths: %#v", paths)
	}
}

func TestLaunchCommitPushKeepsBothRenamePathsVisibleAsUserChange(t *testing.T) {
	t.Parallel()

	paths := userChangedPathsFromPorcelain(strings.Join([]string{
		"R  docs/old.md",
		"docs/new.md",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(paths, []string{"docs/old.md", "docs/new.md"}) {
		t.Fatalf("unexpected rename paths: %#v", paths)
	}
}

func TestLaunchCommitPushDropsRuntimePathFromRenamePair(t *testing.T) {
	t.Parallel()

	paths := userChangedPathsFromPorcelain(strings.Join([]string{
		"R  .progress/runner-output/execution.log",
		"docs/execution.log",
		"R  docs/trace.log",
		".progress/execution-runs/trace.log",
		"",
	}, "\x00"))
	if !reflect.DeepEqual(paths, []string{"docs/execution.log", "docs/trace.log"}) {
		t.Fatalf("unexpected visible rename paths: %#v", paths)
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
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
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

func TestLaunchCommitPushAppliesGitOverrideToCommitAndPush(t *testing.T) {
	t.Parallel()

	var commitEnv []string
	var commitArgs []string
	var pushEnv []string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
				return "", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
		runGitOutputEnv: func(_ context.Context, _ string, env []string, args ...string) (string, error) {
			switch args[len(args)-1] {
			case "repo":
				commitEnv = append([]string(nil), env...)
				commitArgs = append([]string(nil), args...)
				return "[feature/test abc123] repo\n", nil
			default:
				if len(args) == 1 && args[0] == "push" {
					pushEnv = append([]string(nil), env...)
					return "", nil
				}
				return "", fmt.Errorf("unexpected git command with env: %v", args)
			}
		},
	}
	allocation := validAllocation()
	allocation.Git = &model.GitConfig{
		Identity: &model.GitIdentityConfig{
			AuthorName:     "Progress Execution",
			AuthorEmail:    "progress@example.com",
			CommitterName:  "Progress Committer",
			CommitterEmail: "committer@example.com",
		},
		Signing: &model.GitSigningConfig{Enabled: true, Format: "ssh", SigningKey: "/keys/signing.pub", Program: "/usr/bin/ssh-keygen"},
		Push:    &model.GitPushConfig{SSHIdentityFile: "/keys/push", KnownHostsFile: "/keys/known_hosts", IdentitiesOnly: true},
	}

	_, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), allocation, validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	expectedEnv := []string{"GIT_AUTHOR_NAME=Progress Execution", "GIT_AUTHOR_EMAIL=progress@example.com", "GIT_COMMITTER_NAME=Progress Committer", "GIT_COMMITTER_EMAIL=committer@example.com"}
	if !reflect.DeepEqual(commitEnv, expectedEnv) {
		t.Fatalf("unexpected commit env: %#v", commitEnv)
	}
	expectedArgs := []string{"-c", "commit.gpgsign=true", "-c", "gpg.format=ssh", "-c", "user.signingkey=/keys/signing.pub", "-c", "gpg.ssh.program=/usr/bin/ssh-keygen", "commit", "-m", "repo"}
	if !reflect.DeepEqual(commitArgs, expectedArgs) {
		t.Fatalf("unexpected commit args: %#v", commitArgs)
	}
	if len(pushEnv) != 1 || !strings.Contains(pushEnv[0], "GIT_SSH_COMMAND=ssh -i '/keys/push'") || !strings.Contains(pushEnv[0], "IdentitiesOnly=yes") || !strings.Contains(pushEnv[0], "UserKnownHostsFile='/keys/known_hosts'") {
		t.Fatalf("unexpected push env: %#v", pushEnv)
	}
}

func TestGitPushEnvWritesPrivateIdentityToTemporaryFile(t *testing.T) {
	t.Parallel()

	env, cleanup, err := gitPushEnv(context.Background(), &model.GitConfig{Push: &model.GitPushConfig{SSHIdentityPrivateValue: "PRIVATE KEY", IdentitiesOnly: true}}, model.ResourcePrivateStoreConfig{}, "")
	if err != nil {
		t.Fatalf("git push env: %v", err)
	}
	if len(env) != 1 || !strings.Contains(env[0], "GIT_SSH_COMMAND=ssh -i '") {
		t.Fatalf("unexpected env: %#v", env)
	}
	pathStart := strings.Index(env[0], "'")
	pathEnd := strings.Index(env[0][pathStart+1:], "'")
	if pathStart == -1 || pathEnd == -1 {
		t.Fatalf("temporary path was not quoted: %#v", env)
	}
	path := env[0][pathStart+1 : pathStart+1+pathEnd]
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temporary private key: %v", err)
	}
	if string(content) != "PRIVATE KEY" {
		t.Fatalf("unexpected temporary private key content")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary private key must have 0600 permissions: info=%v err=%v", info, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary private key must be removed, err=%v", err)
	}
}

func TestGitPushEnvForcesIdentitiesOnlyWithIdentityFile(t *testing.T) {
	t.Parallel()

	env, cleanup, err := gitPushEnv(context.Background(), &model.GitConfig{Push: &model.GitPushConfig{SSHIdentityFile: "/keys/push"}}, model.ResourcePrivateStoreConfig{}, "")
	defer cleanup()
	if err != nil {
		t.Fatalf("git push env: %v", err)
	}
	if len(env) != 1 || !strings.Contains(env[0], "GIT_SSH_COMMAND=ssh -i '/keys/push'") || !strings.Contains(env[0], "IdentitiesOnly=yes") {
		t.Fatalf("unexpected env: %#v", env)
	}
}

func TestGitPushEnvResolvesPrivateIdentityFromStore(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "private-values.json")
	if err := os.WriteFile(storePath, []byte(`{"progress_push_key":"PRIVATE KEY"}`), 0o600); err != nil {
		t.Fatalf("write private store: %v", err)
	}
	config := &model.GitConfig{Push: &model.GitPushConfig{SSHIdentityPrivate: "progress_push_key", IdentitiesOnly: true}}

	env, cleanup, err := gitPushEnv(context.Background(), config, model.ResourcePrivateStoreConfig{Type: "file", Path: storePath}, "")
	if err != nil {
		t.Fatalf("git push env: %v", err)
	}
	defer cleanup()
	if len(env) != 1 || !strings.Contains(env[0], "GIT_SSH_COMMAND=ssh -i '") {
		t.Fatalf("unexpected env: %#v", env)
	}
	if config.Push.SSHIdentityPrivateValue != "PRIVATE KEY" {
		t.Fatalf("private identity was not resolved")
	}
}

func TestGitPushEnvRejectsIncompletePushOverride(t *testing.T) {
	t.Parallel()

	_, _, err := gitPushEnv(context.Background(), &model.GitConfig{Push: &model.GitPushConfig{KnownHostsFile: "/keys/known_hosts", IdentitiesOnly: true}}, model.ResourcePrivateStoreConfig{}, "")
	if err == nil {
		t.Fatal("expected incomplete git push override error")
	}
	if !strings.Contains(err.Error(), "git.push must define ssh-identity-file or ssh-identity-private") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitPushEnvRejectsUnresolvedPrivateIdentity(t *testing.T) {
	t.Parallel()

	_, _, err := gitPushEnv(context.Background(), &model.GitConfig{Push: &model.GitPushConfig{SSHIdentityPrivate: "progress-push-key"}}, model.ResourcePrivateStoreConfig{}, "")
	if err == nil {
		t.Fatal("expected unresolved private identity error")
	}
	if !strings.Contains(err.Error(), "git.push requires private store") && !strings.Contains(err.Error(), "git.push references missing private value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommitAndPushRejectsIncompletePushOverrideBeforeGitAdd(t *testing.T) {
	t.Parallel()

	var addCalled bool
	service := &Service{
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "rev-parse --show-toplevel":
				return "/repo\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain -z -uall":
				return "M  file.txt\x00", nil
			case "add -A -- file.txt":
				addCalled = true
				return "", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
		runGitOutputEnv: func(_ context.Context, _ string, _ []string, args ...string) (string, error) {
			return "", fmt.Errorf("git command with env must not be called: %v", args)
		},
	}
	allocation := validAllocation()
	allocation.Git = &model.GitConfig{Push: &model.GitPushConfig{KnownHostsFile: "/keys/known_hosts", IdentitiesOnly: true}}

	_, err := service.commitAndPush(context.Background(), commitPushInputFromLaunch(validInvocation(t, true), allocation, validWorkplace(t), nil))
	if err == nil {
		t.Fatal("expected incomplete git push override error")
	}
	if !strings.Contains(err.Error(), "git.push must define ssh-identity-file or ssh-identity-private") {
		t.Fatalf("unexpected error: %v", err)
	}
	if addCalled {
		t.Fatal("git add must not run after incomplete git push override")
	}
}

func TestLaunchRunnerErrorReturned(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("launch runner failed")
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "", runnerErr
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "status --porcelain -z -uall":
				return " M partial.go\x00", nil
			case "branch --show-current":
				return "136\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
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
	if record.Result.WorktreeDiagnostic == nil || !record.Result.WorktreeDiagnostic.DirtyWorktree || record.Result.WorktreeDiagnostic.Branch != "136" {
		t.Fatalf("runner failure must keep dirty-worktree diagnostic: %#v", record.Result)
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
				`{"summary":"Main result.","commit_message":"Document deploy checklist","remarks":[{"id":"remark-1","external_id":"PRRC_comment-1","thread_id":"PRRT_thread-1","severity":"critical","title":"Rollback plan","body":"Document rollback steps.","path":"docs/deploy.md","line":7,"side":"RIGHT"}],"review_responses":[{"remark_id":"remark-1","status":"resolved","summary":"Rollback steps documented."}],"questions":[{"id":"question-1","title":"Integration coverage","body":"Should we add an integration test?"}],"follow_up_actions":[{"id":"action-1","status":"pending","type":"docs","title":"Update release checklist"}],"changes":[{"summary":"Touched deploy docs."}],"commands":[{"name":"open-pr","args":["--draft"]}],"conclusion":{"status":"needs-follow-up","summary":"Ship after docs update"},"extensions":{"custom":{"owner":"release"}}}`,
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
	if len(result.StructuredOutput.Remarks) != 1 || result.StructuredOutput.Remarks[0].Body != "Document rollback steps." || result.StructuredOutput.Remarks[0].ExternalID != "PRRC_comment-1" || result.StructuredOutput.Remarks[0].ThreadID != "PRRT_thread-1" || result.StructuredOutput.Remarks[0].Path != "docs/deploy.md" || result.StructuredOutput.Remarks[0].Line != 7 || result.StructuredOutput.Remarks[0].Side != "RIGHT" {
		t.Fatalf("unexpected remarks: %#v", result.StructuredOutput.Remarks)
	}
	if len(result.StructuredOutput.ReviewResponses) != 1 || result.StructuredOutput.ReviewResponses[0].RemarkID != "remark-1" {
		t.Fatalf("unexpected review responses: %#v", result.StructuredOutput.ReviewResponses)
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
		Model:     "openai/gpt-5.4",
	}, "ship it")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	assertRunnerCommand(t, cmd, RunnerCodex, []string{"exec", "-C", "/tmp/work", "-m", "gpt-5.4", "ship it"})
}

func TestBuildRunnerCommandCodexResume(t *testing.T) {
	t.Parallel()

	cmd, err := buildRunnerCommand(context.Background(), model.LaunchSpec{
		Directory: "/tmp/work",
		Runner:    RunnerCodex,
		Model:     "openai/gpt-5.4",
		Resume:    &model.ResumeSpec{ParentRunID: 42, RunnerSessionID: "session-42", MessageSource: "message"},
	}, "ship it")
	if err != nil {
		t.Fatalf("build command: %v", err)
	}

	assertRunnerCommand(t, cmd, RunnerCodex, []string{"exec", "resume", "session-42", "ship it"})
}

func TestBuildRunnerCommandResumeUnsupportedRunner(t *testing.T) {
	t.Parallel()

	_, err := buildRunnerCommand(context.Background(), model.LaunchSpec{
		Directory: "/tmp/work",
		Runner:    "custom-runner",
		Model:     "openai/gpt-5.4",
		Resume:    &model.ResumeSpec{ParentRunID: 42, RunnerSessionID: "session-42", MessageSource: "message"},
	}, "ship it")
	if !errors.Is(err, errResumeUnsupported) {
		t.Fatalf("expected resume unsupported error, got %v", err)
	}
}

func TestLaunchPersistsRunnerSessionID(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.runRunner = func(context.Context, model.Invocation) (string, error) {
		return appendTrailingRunnerMetadata(strings.Join([]string{"runner output", structuredOutputStart, `{"summary":"Done."}`, structuredOutputEnd}, "\n"), runnerMetadata{RunnerSessionID: "session-42"}), nil
	}

	invocation := validInvocation(t, false)
	workplace := validWorkplace(t)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RunnerSessionID != "session-42" {
		t.Fatalf("unexpected runner session id: %#v", result)
	}
	if strings.Contains(result.Summary, runnerMetadataStart) {
		t.Fatalf("summary must not expose runner metadata block: %q", result.Summary)
	}

	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.RunnerSessionID != "session-42" {
		t.Fatalf("run record must keep runner session id: %#v", record)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 || runs[0].RunnerSessionID != "session-42" {
		t.Fatalf("sqlite history must keep runner session id: %#v", runs)
	}
	rawOutput, readErr := os.ReadFile(result.RawOutputPath)
	if readErr != nil {
		t.Fatalf("read raw output: %v", readErr)
	}
	if strings.Contains(string(rawOutput), runnerMetadataStart) {
		t.Fatalf("raw output must not expose runner metadata block: %q", string(rawOutput))
	}
}

func TestNormalizeCodexJSONOutputExtractsThreadStartedSessionID(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-42"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}`,
		`{"type":"assistant_message","item":{"type":"agent_message","text":"Ignored."}}`,
		`summary line`,
	}, "\n")

	plain, sessionID := normalizeCodexJSONOutput(output)
	if strings.TrimSpace(sessionID) != "thread-42" {
		t.Fatalf("unexpected session id: %q", sessionID)
	}
	if strings.TrimSpace(plain) != "summary line\nDone." {
		t.Fatalf("unexpected plain output: %q", plain)
	}
}

func TestNormalizeCodexJSONOutputReturnsPlainForMissingThreadSession(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		`{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}`,
		`summary line`,
	}, "\n")

	plain, sessionID := normalizeCodexJSONOutput(output)
	if strings.TrimSpace(sessionID) != "" {
		t.Fatalf("unexpected session id: %q", sessionID)
	}
	if strings.TrimSpace(plain) != "summary line\nDone." {
		t.Fatalf("unexpected plain output: %q", plain)
	}
}

func TestLaunchPersistsCodexRunnerSessionIDFromRunnerMetadata(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return appendTrailingRunnerMetadata(strings.Join([]string{
				`{"type":"thread.started","thread_id":"thread-codex-42"}`,
				`{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}`,
			}, "\n"), runnerMetadata{RunnerSessionID: "thread-codex-42"}), nil
		},
		extractSessionID: extractRunnerSessionID,
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Runner = RunnerCodex
	workplace := validWorkplace(t)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RunnerSessionID != "thread-codex-42" {
		t.Fatalf("unexpected runner session id: %#v", result)
	}

	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.RunnerSessionID != "thread-codex-42" {
		t.Fatalf("run record must keep runner session id: %#v", record)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 || runs[0].RunnerSessionID != "thread-codex-42" {
		t.Fatalf("sqlite history must keep runner session id: %#v", runs)
	}
}

func TestLaunchPrefersRunnerMetadataOverCodexTextJSON(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return appendTrailingRunnerMetadata(strings.Join([]string{
				`{"type":"thread.started","thread_id":"payload-thread"}`,
				structuredOutputStart,
				`{"summary":"Done."}`,
				structuredOutputEnd,
			}, "\n"), runnerMetadata{RunnerSessionID: "adapter-thread"}), nil
		},
		extractSessionID: extractRunnerSessionID,
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Runner = RunnerCodex
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RunnerSessionID != "adapter-thread" {
		t.Fatalf("runner metadata must win over text JSON: %#v", result)
	}
}

func TestLaunchOmitsCodexRunnerSessionIDWhenNotProvidedByAdapter(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				`{"type":"thread.started","thread_id":"fake-session-42"}`,
				`{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}`,
				`{"type":"assistant_message","item":{"type":"agent_message","text":"Ignored."}}`,
			}, "\n"), nil
		},
		extractSessionID: extractRunnerSessionID,
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Runner = RunnerCodex
	workplace := validWorkplace(t)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RunnerSessionID != "" {
		t.Fatalf("missing adapter session id must stay empty: %#v", result)
	}

	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.RunnerSessionID != "" {
		t.Fatalf("run record must not store fake runner session id: %#v", record)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 || runs[0].RunnerSessionID != "" {
		t.Fatalf("sqlite history must not keep fake runner session id: %#v", runs)
	}
}

func TestLaunchIgnoresRunnerSessionIDFromArbitraryOutput(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Task completed successfully.",
				`session_id: fake-session-42`,
				structuredOutputStart,
				`{"summary":"Done."}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		extractSessionID: extractRunnerSessionID,
	}

	invocation := validInvocation(t, false)
	workplace := validWorkplace(t)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.RunnerSessionID != "" {
		t.Fatalf("arbitrary output must not set runner session id: %#v", result)
	}

	record := readLaunchRunRecord(t, result.RunRecordPath)
	if record.RunnerSessionID != "" {
		t.Fatalf("run record must not keep fake runner session id: %#v", record)
	}

	runs, err := history.List(context.Background(), workplace.Name, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 || runs[0].RunnerSessionID != "" {
		t.Fatalf("sqlite history must not keep fake runner session id: %#v", runs)
	}
}

func TestLaunchReturnsResumeUnsupportedState(t *testing.T) {
	t.Parallel()

	service := NewService()
	invocation := validInvocation(t, false)
	invocation.Launch.Resume = &model.ResumeSpec{ParentRunID: 42, MessageSource: "message"}
	workplace := validWorkplace(t)

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace)
	if !errors.Is(err, errResumeUnsupported) {
		t.Fatalf("expected resume unsupported error, got %v", err)
	}
	if result.Status != "resume-unsupported" {
		t.Fatalf("unexpected result state: %#v", result)
	}
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
		runGitOutput: failedLaunchDiagnosticGitOutput,
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
		runGitOutput: failedLaunchDiagnosticGitOutput,
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
			name:       "legacy response identifiers in remark",
			payload:    `{"summary":"Done.","remarks":[{"id":"remark-2-follow-up","remark_id":"PRRC_comment-1","thread_id":"PRRT_thread-1","status":"open"}]}`,
			expectPart: `unknown field "remark_id"`,
		},
		{
			name:       "summary type mismatch",
			payload:    `{"summary":42}`,
			expectPart: "type mismatch at summary: expected string but got number",
		},
		{
			name:       "remarks string type mismatch",
			payload:    `{"summary":"Done.","remarks":"not-an-array"}`,
			expectPart: "type mismatch at remarks: expected array of objects with id/external_id/thread_id/status/severity/type/title/body/path/line/side/answer/resolution but got string",
		},
		{
			name:       "remarks array of strings mismatch",
			payload:    `{"summary":"Done.","remarks":["bad-item"]}`,
			expectPart: "type mismatch at remarks: expected array of objects with id/external_id/thread_id/status/severity/type/title/body/path/line/side/answer/resolution but got string",
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
		{
			name:       "remark with only inline location metadata",
			payload:    `{"summary":"Done.","remarks":[{"path":"internal/service.go","line":42,"side":"RIGHT"}]}`,
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
				runGitOutput: failedLaunchDiagnosticGitOutput,
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

func TestLaunchStructuredOutputAcceptsCanonicalFollowUpRemarkPayload(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Повторная ревизия завершена.",
				structuredOutputStart,
				`{"summary":"Проверка завершена.","remarks":[{"id":"remark-2-follow-up","external_id":"PRRC_comment-1","thread_id":"PRRT_thread-1","status":"open","title":"Предыдущее замечание ревизии","body":"Проверка после исправления."}]}`,
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
	if err != nil {
		t.Fatalf("canonical follow-up remark payload must be accepted: %v", err)
	}
	if result.StructuredOutput == nil || len(result.StructuredOutput.Remarks) != 1 {
		t.Fatalf("expected one parsed follow-up remark: %#v", result.StructuredOutput)
	}
	remark := result.StructuredOutput.Remarks[0]
	if remark.ExternalID != "PRRC_comment-1" || remark.ThreadID != "PRRT_thread-1" || remark.Status != "open" {
		t.Fatalf("follow-up remark identifiers must survive parsing: %#v", remark)
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
	if !strings.Contains(prompt, "Object forms: remarks[{id,external_id,thread_id,status,severity,type,title,body,path,line,side,answer,resolution}], commands[{name,args,title,body}].") {
		t.Fatalf("prompt must describe selected object forms: %q", prompt)
	}
	if !strings.Contains(prompt, "line must be a diff line on the selected side") {
		t.Fatalf("prompt must explain remark inline line semantics: %q", prompt)
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
	if !strings.Contains(prompt, "Object forms: remarks[{id,external_id,thread_id,status,severity,type,title,body,path,line,side,answer,resolution}], questions[{id,status,title,body,answer}], follow_up_actions[{id,status,type,title,body}], changes[{summary}], commands[{name,args,title,body}], conclusion{status,summary,body}.") {
		t.Fatalf("prompt must keep full object forms when selection is not configured: %q", prompt)
	}
	if !strings.Contains(prompt, "line must be a diff line on the selected side") {
		t.Fatalf("prompt must explain remark inline line semantics: %q", prompt)
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

func TestBuildRunnerPromptDoesNotMentionUnselectedReviewResponses(t *testing.T) {
	t.Parallel()

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:                 "Повторно провести ревизию.",
		StructuredOutput:       true,
		StructuredOutputFields: []string{"summary", "remarks", "conclusion"},
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}
	if strings.Contains(prompt, "review_responses") {
		t.Fatalf("prompt must not mention an unselected structured output field: %q", prompt)
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
	RunnerSessionID     string                  `json:"runner_session_id,omitempty"`
	RawStructuredOutput string                  `json:"raw_structured_output"`
	StructuredOutput    *model.StructuredOutput `json:"structured_output,omitempty"`
	StructuredOutputErr string                  `json:"structured_output_error,omitempty"`
	Error               string                  `json:"error,omitempty"`
	Result              struct {
		Status             string                    `json:"status"`
		Summary            string                    `json:"summary"`
		RawOutputPath      string                    `json:"raw_output_path"`
		WorktreeDiagnostic *model.WorktreeDiagnostic `json:"worktree_diagnostic,omitempty"`
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

func failedLaunchDiagnosticGitOutput(_ context.Context, _ string, args ...string) (string, error) {
	switch strings.Join(args, " ") {
	case "status --porcelain -z -uall":
		return " M partial.go\x00", nil
	case "branch --show-current":
		return "136\n", nil
	default:
		return "", fmt.Errorf("unexpected git command: %v", args)
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir temp repo: %v", err)
	}
	return dir
}

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
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
