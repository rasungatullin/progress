package execution

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/history"
	launchservice "github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestServiceLaunchUsesResolvedAllocationRunnerAndModel(t *testing.T) {
	t.Parallel()

	launcher := &stubLauncher{}
	service := &Service{logger: log.Default(), launcher: launcher}

	_, err := service.Launch(context.Background(), Invocation{
		Launch: LaunchSpec{
			Directory: "/tmp/work",
			Model:     "ignored",
			Prompt:    "ship it",
		},
	}, Profile{Mode: "manual"}, Allocation{Runner: "codex", Model: "gpt-5.3-codex", ModelBinding: "coder"}, Workplace{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	if launcher.invocation.Launch.Runner != "codex" {
		t.Fatalf("expected runner from allocation, got %q", launcher.invocation.Launch.Runner)
	}
	if launcher.invocation.Launch.Model != "gpt-5.3-codex" {
		t.Fatalf("expected model from allocation, got %q", launcher.invocation.Launch.Model)
	}
	if launcher.invocation.Launch.ModelBinding != "coder" {
		t.Fatalf("expected model-binding from allocation, got %q", launcher.invocation.Launch.ModelBinding)
	}
}

func TestServiceLaunchPreservesExistingPromptBehaviorWithoutProfileAdditions(t *testing.T) {
	t.Parallel()

	launcher := &stubLauncher{}
	service := &Service{logger: log.Default(), launcher: launcher}

	_, err := service.Launch(context.Background(), Invocation{
		Launch: LaunchSpec{
			Directory: "/tmp/work",
			Prompt:    "ship it",
		},
	}, Profile{Mode: "manual"}, Allocation{Runner: "opencode", Model: "gpt-5.4"}, Workplace{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(launcher.invocation.Launch.PromptAdditions) != 0 {
		t.Fatalf("prompt-additions must stay empty when profile does not define them: %#v", launcher.invocation.Launch.PromptAdditions)
	}
}

func TestServiceStartRecordsProfileFailureInHistory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	expectedErr := errors.New("profile unavailable")
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{err: expectedErr},
	}

	result, err := service.Start(context.Background(), Invocation{
		Profile:   "missing",
		Workplace: WorkplaceSpec{Name: "task-54"},
		Launch: LaunchSpec{
			Directory: root,
			Runner:    "opencode",
			Model:     "openai/gpt-5.4",
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected profile error, got %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed result, got %#v", result)
	}

	runs, err := history.List(context.Background(), root, history.ListFilter{Limit: 10, Status: "failed"})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one failed sqlite history run, got %d", len(runs))
	}
	if runs[0].Name != "task-54" || runs[0].Error != expectedErr.Error() || runs[0].LaunchDirectory != root {
		t.Fatalf("unexpected failed start row: %#v", runs[0])
	}
}

func TestServiceStartUpdatesRunningHistoryRowOnSuccess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	launcher := &stubLauncher{result: model.LaunchResult{
		Status:        "completed",
		Summary:       "launch complete",
		RawOutputPath: filepath.Join(root, "output.log"),
		StructuredOutput: &model.StructuredOutput{
			Summary: "Done.",
		},
		RunRecordPath: filepath.Join(root, "record.json"),
	}}
	service := &Service{
		logger:     log.Default(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher:   launcher,
	}

	result, err := service.Start(context.Background(), Invocation{
		Profile:   "coder",
		Workplace: WorkplaceSpec{Name: "task-58"},
		Launch: LaunchSpec{
			Directory:       root,
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}

	runs, err := history.List(context.Background(), root, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("start must update one sqlite history run, got %d", len(runs))
	}
	if runs[0].Status != "completed" || runs[0].Name != "task-58" || runs[0].ProfileName != "coder" || runs[0].Runner != "opencode" || runs[0].Model != "openai/gpt-5.5" {
		t.Fatalf("unexpected start row: %#v", runs[0])
	}
	if runs[0].RawOutputPath == "" || runs[0].RawStructuredOutput != `{"summary":"Done."}` || runs[0].RunRecordPath == "" {
		t.Fatalf("start row must keep result metadata: %#v", runs[0])
	}
}

func TestServiceExecuteReturnsActionAndOperationResults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := &Service{
		logger:     log.Default(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher: &stubLauncher{result: model.LaunchResult{
			Status:  "completed",
			Summary: "launch complete",
		}},
	}

	result, err := service.Execute(context.Background(), Invocation{
		Task:    "task-91",
		Action:  "implement",
		Profile: "coder",
		Workplace: WorkplaceSpec{
			Name: "task-91",
		},
		Assignment: &ExecutionAssignment{
			Action:         "implement",
			Profile:        "coder",
			ExpectedResult: "Выполнить реализацию.",
			CanonicalTask:  &ObjectRef{Type: "task", Repository: "owner/name", Number: 91},
			Reasons:        []AssignmentReason{{Code: "route_selected", Message: "Маршрут выбрал реализацию."}},
		},
		Launch: LaunchSpec{
			Directory:       root,
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != "completed" || result.Launch == nil || result.Launch.Status != "completed" {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Action.Name != "implement" || result.Action.Profile != "coder" || !result.Action.RequiresSynthesis {
		t.Fatalf("unexpected action: %#v", result.Action)
	}
	if result.Assignment == nil || result.Assignment.CanonicalTask == nil || result.Assignment.CanonicalTask.Number != 91 {
		t.Fatalf("execution result must keep assignment: %#v", result.Assignment)
	}
	expectedOperations := []string{
		OperationKindResolveAction,
		OperationKindPrepareData,
		OperationKindResolveProfile,
		OperationKindAllocateResources,
		OperationKindPrepareWorkplace,
		OperationKindBuildDirective,
		OperationKindLaunchSynthesis,
		OperationKindParseResult,
		OperationKindFinalize,
	}
	if len(result.Operations) != len(expectedOperations) {
		t.Fatalf("unexpected operation count: %#v", result.Operations)
	}
	for index, operationName := range expectedOperations {
		if result.Operations[index].Name != operationName {
			t.Fatalf("unexpected operation at %d: %#v", index, result.Operations[index])
		}
		if result.Operations[index].Status != OperationStatusCompleted {
			t.Fatalf("operation %s must be completed: %#v", operationName, result.Operations[index])
		}
	}
	if result.Operations[1].Input == "" || result.Operations[1].Output == "" {
		t.Fatalf("prepare data operation must keep input and output diagnostics: %#v", result.Operations[1])
	}
}

func TestServiceExecuteReturnsDiagnosedOperationFailure(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("profile unavailable")
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{err: expectedErr},
	}

	result, err := service.Execute(context.Background(), Invocation{
		Task:    "task-92",
		Action:  "implement",
		Profile: "missing",
		Workplace: WorkplaceSpec{
			Name: "task-92",
		},
		Launch: LaunchSpec{
			Directory: t.TempDir(),
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected profile error, got %v", err)
	}
	if result.Status != "failed" || result.Launch == nil || result.Launch.Status != "failed" {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Failure == nil || result.Failure.Code != "execution_failed" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
	if len(result.Operations) < 2 {
		t.Fatalf("expected operation diagnostics: %#v", result.Operations)
	}
	if result.Operations[0].Name != OperationKindResolveAction || result.Operations[0].Status != OperationStatusCompleted {
		t.Fatalf("action resolution must be completed: %#v", result.Operations[0])
	}
	if result.Operations[1].Name != OperationKindPrepareData || result.Operations[1].Status != OperationStatusCompleted {
		t.Fatalf("data preparation must be completed: %#v", result.Operations[1])
	}
	if result.Operations[2].Name != OperationKindResolveProfile || result.Operations[2].Status != OperationStatusFailed {
		t.Fatalf("profile operation must be failed: %#v", result.Operations[2])
	}
	if result.Operations[2].Failure == nil || result.Operations[2].Failure.Code != "profile_not_found" {
		t.Fatalf("unexpected operation failure: %#v", result.Operations[2])
	}
}

func TestServiceExecuteReturnsPartialResultWhenFinalOperationFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	expectedErr := errors.New("commit push failed")
	service := &Service{
		logger:     log.Default(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher: &stubLauncher{
			result: model.LaunchResult{
				Status:        "failed",
				Summary:       "commit push failed",
				RawOutputPath: filepath.Join(root, "runner.log"),
				StructuredOutput: &model.StructuredOutput{
					Summary: "Синтез выполнен.",
				},
				RunRecordPath: filepath.Join(root, "record.json"),
			},
			err: expectedErr,
		},
	}

	result, err := service.Execute(context.Background(), Invocation{
		Task:    "task-93",
		Action:  "implement",
		Profile: "coder",
		Workplace: WorkplaceSpec{
			Name: "task-93",
		},
		Launch: LaunchSpec{
			Directory:       root,
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected final operation error, got %v", err)
	}
	if result.Status != "partial" {
		t.Fatalf("expected partial result, got %#v", result)
	}
	if len(result.Artifacts) == 0 || result.Artifacts[0].Type != "runner-output" {
		t.Fatalf("partial result must keep artifacts: %#v", result.Artifacts)
	}
	if len(result.DiagnosticLinks) == 0 {
		t.Fatalf("partial result must keep diagnostic links: %#v", result.DiagnosticLinks)
	}
	finalOperation := result.Operations[len(result.Operations)-1]
	if finalOperation.Name != OperationKindFinalize || finalOperation.Status != OperationStatusFailed {
		t.Fatalf("finalize operation must be failed: %#v", finalOperation)
	}
	if finalOperation.Failure == nil || finalOperation.Failure.Code != "final_operation_failed" {
		t.Fatalf("unexpected finalize failure: %#v", finalOperation)
	}
}

func TestServiceStartEnrichesRunningHistoryRowBeforeLaunch(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	historyRoot := t.TempDir()
	if err := os.Chdir(historyRoot); err != nil {
		t.Fatalf("chdir history root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	workplaceDir := filepath.Join(historyRoot, "workplace")
	launcher := &stubLauncher{beforeReturn: func() {
		runs, err := history.List(context.Background(), historyRoot, history.ListFilter{Limit: 10})
		if err != nil {
			t.Fatalf("list running history: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("start must keep one running sqlite history run, got %d", len(runs))
		}
		if runs[0].Status != "running" || runs[0].LaunchDirectory != workplaceDir || runs[0].ProfileName != "coder" || runs[0].Runner != "opencode" || runs[0].Model != "openai/gpt-5.5" {
			t.Fatalf("running start row must be enriched before launch returns: %#v", runs[0])
		}
	}}
	service := &Service{
		logger:     log.Default(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: workplaceDir, Ready: true}},
		launcher:   launcher,
	}

	result, err := service.Start(context.Background(), Invocation{
		Profile:   "coder",
		Workplace: WorkplaceSpec{Name: "task-58"},
		Launch: LaunchSpec{
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestServiceResumeBuildsCompactStructuredInputAndHistoryLink(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	workplaceDir := filepath.Join(root, "workplace")
	if err := os.MkdirAll(workplaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workplace: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	parentInput := StructuredInput{Task: "Original task", ProjectContext: []StructuredContext{{Title: "Spec", Body: "Long context"}}}
	if err := history.Store(context.Background(), root, history.Run{
		CreatedAt:          "2026-06-22T10:00:00Z",
		Status:             "completed",
		Summary:            "Parent summary",
		Name:               "task-resume",
		ProfileName:        "coder",
		Runner:             "codex",
		RunnerSessionID:    "session-42",
		Model:              "openai/gpt-5.4",
		LaunchDirectory:    filepath.Join(root, "workplace"),
		RawStructuredInput: history.StructuredInputJSON(&parentInput),
		RunRecordPath:      filepath.Join(root, ".progress", "execution-runs", "parent.json"),
	}); err != nil {
		t.Fatalf("store parent run: %v", err)
	}

	launcher := &stubLauncher{}
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual"}},
		launcher: launcher,
	}

	result, err := service.Resume(context.Background(), ResumeRequest{Run: "1", Message: "Учти новый лимит", MessageSource: "message"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}

	input := launcher.invocation.Launch.StructuredInput
	if input == nil {
		t.Fatal("resume launch must receive structured input")
	}
	if input.Task != "" || len(input.ProjectContext) != 0 {
		t.Fatalf("resume input must stay compact: %#v", input)
	}
	if len(input.OperationalContext) != 1 || input.OperationalContext[0].Body != "Учти новый лимит" {
		t.Fatalf("unexpected operational context: %#v", input.OperationalContext)
	}
	if len(input.PreviousRunResults) != 1 || !strings.Contains(input.PreviousRunResults[0].Summary, "parent run #1 completed") {
		t.Fatalf("unexpected previous run results: %#v", input.PreviousRunResults)
	}
	if launcher.invocation.Launch.Resume == nil || launcher.invocation.Launch.Resume.ParentRunID != 1 || launcher.invocation.Launch.Resume.RunnerSessionID != "session-42" {
		t.Fatalf("unexpected resume metadata: %#v", launcher.invocation.Launch.Resume)
	}

	var resumeExtension struct {
		ParentRunID         int64  `json:"parent_run_id"`
		ParentRunner        string `json:"parent_runner"`
		ParentRunnerSession string `json:"parent_runner_session_id"`
		MessageSource       string `json:"message_source"`
	}
	if err := json.Unmarshal(input.Extensions["resume"], &resumeExtension); err != nil {
		t.Fatalf("decode resume extension: %v", err)
	}
	if resumeExtension.ParentRunID != 1 || resumeExtension.ParentRunner != "codex" || resumeExtension.ParentRunnerSession != "session-42" || resumeExtension.MessageSource != "message" {
		t.Fatalf("unexpected resume extension: %#v", resumeExtension)
	}

	runs, err := history.List(context.Background(), root, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected parent and child runs, got %d", len(runs))
	}
	child := runs[0]
	if child.ParentRunID != 1 || child.ResumeMessage != "Учти новый лимит" || child.ResumeMessageSource != "message" {
		t.Fatalf("unexpected child history row: %#v", child)
	}
}

func TestServiceResumeLatestUsesNewestMatchingRun(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	workplaceDir := filepath.Join(root, "workplace")
	if err := os.MkdirAll(workplaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workplace: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	for _, run := range []history.Run{
		{CreatedAt: "2026-06-22T10:00:00Z", Status: "completed", Summary: "first", Name: "task-resume", ProfileName: "coder", Runner: "codex", RunnerSessionID: "session-1", Model: "openai/gpt-5.4", LaunchDirectory: filepath.Join(root, "workplace")},
		{CreatedAt: "2026-06-22T10:01:00Z", Status: "completed", Summary: "second", Name: "task-resume", ProfileName: "coder", Runner: "codex", RunnerSessionID: "session-2", Model: "openai/gpt-5.4", LaunchDirectory: filepath.Join(root, "workplace")},
	} {
		if err := history.Store(context.Background(), root, run); err != nil {
			t.Fatalf("store run: %v", err)
		}
	}

	service := &Service{logger: log.Default(), profiles: &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual"}}}
	result, err := service.Resume(context.Background(), ResumeRequest{Run: "latest", Name: "task-resume", Message: "Continue", MessageSource: "message", DryRun: true})
	if err != nil {
		t.Fatalf("resume dry-run: %v", err)
	}
	if result.Status != "dry-run" {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if !strings.Contains(result.Summary, "parent-run=2") || !strings.Contains(result.Summary, "parent-session-id=session-2") {
		t.Fatalf("dry-run must use newest matching parent: %q", result.Summary)
	}
}

func TestServiceLaunchReturnsResumeUnsupportedForUnsupportedRunner(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default(), launcher: launchservice.NewService()}
	workplace := t.TempDir()
	result, err := service.Launch(context.Background(), Invocation{
		Launch: LaunchSpec{
			Directory: workplace,
			Runner:    "custom-runner",
			Model:     "openai/gpt-5.4",
			Prompt:    "Continue task",
			Resume:    &model.ResumeSpec{ParentRunID: 42, RunnerSessionID: "session-42", MessageSource: "message"},
		},
	}, Profile{Name: "coder", Mode: "manual"}, Allocation{Runner: "custom-runner", Model: "openai/gpt-5.4"}, Workplace{Name: workplace, Ready: true})
	if err == nil {
		t.Fatal("expected resume unsupported error")
	}
	if result.Status != "resume-unsupported" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(err.Error(), "resume is unsupported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceResumeReturnsResumeUnsupportedForUnsupportedRunner(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := t.TempDir()
	workplaceDir := filepath.Join(root, "workplace")
	if err := os.MkdirAll(workplaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workplace: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	if err := history.Store(context.Background(), root, history.Run{
		CreatedAt:       "2026-06-22T10:00:00Z",
		Status:          "completed",
		Summary:         "Parent summary",
		Name:            "task-resume",
		ProfileName:     "coder",
		Runner:          "custom-runner",
		RunnerSessionID: "session-42",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: workplaceDir,
	}); err != nil {
		t.Fatalf("store parent run: %v", err)
	}

	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual"}},
		launcher: launchservice.NewService(),
	}

	result, err := service.Resume(context.Background(), ResumeRequest{Run: "1", Message: "Continue", MessageSource: "message"})
	if err == nil {
		t.Fatal("expected resume unsupported error")
	}
	if result.Status != "resume-unsupported" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(err.Error(), "resume is unsupported") {
		t.Fatalf("unexpected error: %v", err)
	}

	runs, listErr := history.List(context.Background(), root, history.ListFilter{Limit: 10})
	if listErr != nil {
		t.Fatalf("list history: %v", listErr)
	}
	if len(runs) != 2 {
		t.Fatalf("expected parent and child runs, got %d", len(runs))
	}
	if runs[0].Status != "resume-unsupported" {
		t.Fatalf("unexpected child history row: %#v", runs[0])
	}
}

func TestServiceResumePreservesRawStructuredOutputInHistory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	if err := history.Store(context.Background(), root, history.Run{
		CreatedAt:       "2026-06-22T10:00:00Z",
		Status:          "completed",
		Summary:         "Parent summary",
		Name:            "task-resume",
		ProfileName:     "coder",
		Runner:          "codex",
		RunnerSessionID: "session-42",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: filepath.Join(root, "workplace"),
	}); err != nil {
		t.Fatalf("store parent run: %v", err)
	}

	expectedErr := errors.New("structured output is required: invalid trailing block")
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual"}},
		launcher: &stubLauncher{result: model.LaunchResult{
			Status:              "failed",
			Summary:             "runner returned invalid structured output",
			RawOutputPath:       filepath.Join(root, "runner.log"),
			RawStructuredOutput: `{"remarks":[{}]}`,
			RunRecordPath:       filepath.Join(root, ".progress", "execution-runs", "child.json"),
		}, err: expectedErr},
	}

	result, err := service.Resume(context.Background(), ResumeRequest{Run: "1", Message: "Continue", MessageSource: "message"})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected launcher error, got %v", err)
	}
	if result.RawStructuredOutput != `{"remarks":[{}]}` {
		t.Fatalf("unexpected result raw structured output: %#v", result)
	}

	runs, listErr := history.List(context.Background(), root, history.ListFilter{Limit: 10})
	if listErr != nil {
		t.Fatalf("list history: %v", listErr)
	}
	if len(runs) != 2 {
		t.Fatalf("expected parent and child runs, got %d", len(runs))
	}
	if runs[0].RawStructuredOutput != `{"remarks":[{}]}` {
		t.Fatalf("resume history must preserve raw structured payload: %#v", runs[0])
	}
}

type stubLauncher struct {
	invocation   model.Invocation
	result       model.LaunchResult
	err          error
	beforeReturn func()
}

func (s *stubLauncher) Launch(_ context.Context, in model.Invocation, _ model.Profile, _ model.Allocation, _ model.Workplace) (model.LaunchResult, error) {
	s.invocation = in
	if s.beforeReturn != nil {
		s.beforeReturn()
	}
	if s.result.Status == "" && s.err == nil {
		return model.LaunchResult{Status: "completed"}, nil
	}
	return s.result, s.err
}

type stubProfileResolver struct {
	profile model.Profile
	err     error
}

func (s *stubProfileResolver) Resolve(context.Context, model.Invocation) (model.Profile, error) {
	if s.err != nil {
		return model.Profile{}, s.err
	}
	return s.profile, nil
}

type stubResourceProvider struct {
	allocation model.Allocation
	err        error
}

func (s *stubResourceProvider) Allocate(context.Context, model.Invocation, model.Profile) (model.Allocation, error) {
	if s.err != nil {
		return model.Allocation{}, s.err
	}
	return s.allocation, nil
}

type stubWorkplaceManager struct {
	workplace model.Workplace
	err       error
}

func (s *stubWorkplaceManager) Prepare(context.Context, model.Invocation, model.Profile, model.Allocation) (model.Workplace, error) {
	if s.err != nil {
		return model.Workplace{}, s.err
	}
	return s.workplace, nil
}
