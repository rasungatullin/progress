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
