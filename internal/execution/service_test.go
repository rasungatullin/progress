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
	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/methodology"
)

func TestServiceLaunchUsesResolvedAllocationRunnerAndModel(t *testing.T) {
	t.Parallel()

	launcher := &stubLauncher{}
	service := &Service{logger: log.Default(), launcher: launcher}

	_, err := service.launch(context.Background(), invocation{
		Launch: launchSpec{
			Directory: "/tmp/work",
			Model:     "ignored",
			Prompt:    "ship it",
		},
	}, profile{Mode: "manual"}, allocation{Runner: "codex", Model: "gpt-5.3-codex", ModelBinding: "coder"}, workplace{})
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

	_, err := service.launch(context.Background(), invocation{
		Launch: launchSpec{
			Directory: "/tmp/work",
			Prompt:    "ship it",
		},
	}, profile{Mode: "manual"}, allocation{Runner: "opencode", Model: "gpt-5.4"}, workplace{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(launcher.invocation.Launch.PromptAdditions) != 0 {
		t.Fatalf("prompt-additions must stay empty when profile does not define them: %#v", launcher.invocation.Launch.PromptAdditions)
	}
}

func TestServiceStartRecordsProfileFailureInHistory(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	expectedErr := errors.New("profile unavailable")
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{err: expectedErr},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 54},
			StructuredInput: &StructuredInput{Task: "Ship it."},
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
	if runs[0].Name != "54" || runs[0].Error != expectedErr.Error() || strings.TrimSpace(runs[0].LaunchDirectory) == "" {
		t.Fatalf("unexpected failed start row: %#v", runs[0])
	}
}

func TestServiceStartUpdatesRunningHistoryRowOnSuccess(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
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

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 58},
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
	if runs[0].Status != "completed" || runs[0].Name != "58" || runs[0].ProfileName != "coder" || runs[0].Runner != "opencode" || runs[0].Model != "openai/gpt-5.5" {
		t.Fatalf("unexpected start row: %#v", runs[0])
	}
	if runs[0].RawOutputPath == "" || runs[0].RawStructuredOutput != `{"summary":"Done."}` || runs[0].RunRecordPath == "" {
		t.Fatalf("start row must keep result metadata: %#v", runs[0])
	}
}

func TestServiceExecuteOperationClosesHistoryForEarlyOperation(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	service := &Service{logger: log.Default()}

	result, err := service.ExecuteOperation(context.Background(), OperationInvocation{
		Operation: OperationKindPrepareData,
		Assignment: &ExecutionAssignment{
			Action:        "review",
			CanonicalTask: &ObjectRef{Type: "task", Number: 61},
		},
	})
	if err != nil {
		t.Fatalf("execute operation: %v", err)
	}
	if result.Name != OperationKindPrepareData || result.Status != OperationStatusCompleted {
		t.Fatalf("unexpected operation result: %#v", result)
	}

	runs, err := history.List(context.Background(), root, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("operation must keep one sqlite history run, got %d", len(runs))
	}
	if runs[0].Status != "completed" || runs[0].Name != "61" || runs[0].ProfileName != "review" {
		t.Fatalf("operation must close history row with resolved diagnostics: %#v", runs[0])
	}
	if runs[0].Summary != result.Summary {
		t.Fatalf("history summary must follow operation result: %#v", runs[0])
	}
}

func TestServiceExecuteReturnsActionAndOperationResults(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
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

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement",
			ExpectedResult:  "Выполнить реализацию.",
			CanonicalTask:   &ObjectRef{Type: "task", Repository: "owner/name", Number: 91},
			Reasons:         []AssignmentReason{{Code: "route_selected", Message: "Маршрут выбрал реализацию."}},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != "completed" || result.Launch == nil || result.Launch.Status != "completed" {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Action.Name != ActionClassEngineeringSynthesis || result.Action.Profile != "default" || !result.Action.RequiresSynthesis {
		t.Fatalf("unexpected action: %#v", result.Action)
	}
	if result.Assignment == nil || result.Assignment.CanonicalTask == nil || result.Assignment.CanonicalTask.Number != 91 {
		t.Fatalf("execution result must keep assignment: %#v", result.Assignment)
	}
	expectedOperations := []string{
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
	if result.Operations[0].Input == "" || result.Operations[0].Output == "" {
		t.Fatalf("prepare data operation must keep input and output diagnostics: %#v", result.Operations[0])
	}
}

func TestServiceExecuteReturnsDiagnosedOperationFailure(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	expectedErr := errors.New("profile unavailable")
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{err: expectedErr},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 92},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected profile error, got %v", err)
	}
	if result.Status != "failed" || result.Launch == nil || result.Launch.Status != "failed" {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Failure == nil || result.Failure.Code != "profile_not_found" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
	if len(result.Operations) < 2 {
		t.Fatalf("expected operation diagnostics: %#v", result.Operations)
	}
	if result.Operations[0].Name != OperationKindPrepareData || result.Operations[0].Status != OperationStatusCompleted {
		t.Fatalf("data preparation must be completed: %#v", result.Operations[1])
	}
	if result.Operations[1].Name != OperationKindResolveProfile || result.Operations[1].Status != OperationStatusFailed {
		t.Fatalf("profile operation must be failed: %#v", result.Operations[1])
	}
	if result.Operations[1].Failure == nil || result.Operations[1].Failure.Code != "profile_not_found" {
		t.Fatalf("unexpected operation failure: %#v", result.Operations[1])
	}
}

func TestServiceExecuteReturnsActionFailureAtZeroStage(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	service := &Service{
		logger: log.Default(),
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "unknown-action",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 94},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if err == nil {
		t.Fatal("expected unknown action error")
	}
	if result.Status != "failed" || result.Launch == nil || result.Launch.Status != "failed" {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Failure == nil || result.Failure.Code != "action_not_found" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
	if len(result.Operations) != 0 {
		t.Fatalf("action resolution failure must not create operation diagnostics: %#v", result.Operations)
	}

	runs, historyErr := history.List(context.Background(), root, history.ListFilter{Limit: 10, Status: "failed"})
	if historyErr != nil {
		t.Fatalf("list history: %v", historyErr)
	}
	if len(runs) != 1 || runs[0].Error == "" {
		t.Fatalf("action failure must be recorded in history: %#v", runs)
	}
}

func TestServiceExecuteReturnsFailedResultWhenFinalOperationFails(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
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

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 93},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected final operation error, got %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed result, got %#v", result)
	}
	if len(result.Artifacts) == 0 || result.Artifacts[0].Type != "runner-output" {
		t.Fatalf("failed result must keep artifacts: %#v", result.Artifacts)
	}
	if len(result.DiagnosticLinks) == 0 {
		t.Fatalf("failed result must keep diagnostic links: %#v", result.DiagnosticLinks)
	}
	if result.Launch == nil || result.Launch.StructuredOutput == nil || result.Launch.StructuredOutput.Summary != "Синтез выполнен." {
		t.Fatalf("failed result must keep structured output for diagnostics: %#v", result.Launch)
	}
	finalOperation := result.Operations[len(result.Operations)-1]
	if finalOperation.Name != OperationKindFinalize || finalOperation.Status != OperationStatusFailed {
		t.Fatalf("finalize operation must be failed: %#v", finalOperation)
	}
	if finalOperation.Failure == nil || finalOperation.Failure.Code != "final_operation_failed" {
		t.Fatalf("unexpected finalize failure: %#v", finalOperation)
	}
}

func TestServiceExecuteLaunchFailureUsesCatalogOperationNames(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	expectedErr := errors.New("final stage failed")
	service := &Service{
		logger: log.Default(),
		actions: &stubActionResolver{action: model.Action{
			Name:              "custom-synthesis",
			Class:             ActionClassEngineeringSynthesis,
			Profile:           "coder",
			RequiresWorkplace: true,
			RequiresSynthesis: true,
			Operations: []model.OperationSpec{
				builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
				builtinOperation(OperationKindResolveProfile, "Выбор исполнительного профиля", true),
				builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true),
				builtinOperation(OperationKindPrepareWorkplace, "Подготовка рабочего места", true),
				builtinOperation(OperationKindBuildDirective, "Сборка исполнительной директивы", true),
				builtinOperation(OperationKindLaunchSynthesis, "Запуск синтеза", true),
				{Name: "normalize-output", Kind: OperationKindParseResult, Title: "Разбор результата", Origin: OperationOriginBuiltin, Required: true},
				{Name: "finish-run", Kind: OperationKindFinalize, Title: "Завершающая фиксация", Origin: OperationOriginBuiltin, Required: true},
			},
		}},
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher: &stubLauncher{
			result: model.LaunchResult{
				Status: "failed",
				StructuredOutput: &model.StructuredOutput{
					Summary: "Синтез выполнен.",
				},
			},
			err: expectedErr,
		},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "custom-synthesis",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 94},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected launch error, got %v", err)
	}
	if operation := findOperationResult(result.Operations, "normalize-output"); operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("custom parse operation must be completed: %#v", result.Operations)
	}
	if operation := findOperationResult(result.Operations, "finish-run"); operation == nil || operation.Status != OperationStatusFailed {
		t.Fatalf("custom finalize operation must be failed: %#v", result.Operations)
	}
	if operation := findOperationResult(result.Operations, OperationKindParseResult); operation != nil {
		t.Fatalf("synthetic parse-result operation must not be added: %#v", operation)
	}
	if operation := findOperationResult(result.Operations, OperationKindFinalize); operation != nil {
		t.Fatalf("synthetic finalize operation must not be added: %#v", operation)
	}
}

func TestServiceExecuteSkipsResourcesWorkplaceAndLaunchWhenActionDoesNotNeedSynthesis(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	service := &Service{
		logger:     log.Default(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "default", Mode: "manual"}},
		resources:  &stubResourceProvider{err: errors.New("resources must not be called")},
		workplaces: &stubWorkplaceManager{err: errors.New("workplace must not be called")},
		launcher:   &stubLauncher{err: errors.New("launcher must not be called")},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "integration-change",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 95},
			StructuredInput: &StructuredInput{IntegrationActions: []StructuredAction{{Type: "github", Title: "Опубликовать комментарий"}}},
		},
	})
	if err != nil {
		t.Fatalf("execute integration action: %v", err)
	}
	if result.Status != "completed" || result.Action.RequiresWorkplace || result.Action.RequiresSynthesis {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Action.Class != ActionClassIntegrationChange {
		t.Fatalf("unexpected action class: %#v", result.Action)
	}
	for _, operationName := range []string{OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult} {
		operation := findOperationResult(result.Operations, operationName)
		if operation == nil || operation.Status != OperationStatusSkipped {
			t.Fatalf("operation %s must be skipped: %#v", operationName, result.Operations)
		}
	}
	if result.Launch == nil || result.Launch.Status != "completed" || !strings.Contains(result.Launch.Summary, "synthesis=not-required") {
		t.Fatalf("unexpected launch summary: %#v", result.Launch)
	}
}

func TestServiceExecuteUsesResolvedActionOperationList(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	expectedErr := errors.New("profile resolver must not be called")
	service := &Service{
		logger: log.Default(),
		actions: &stubActionResolver{action: model.Action{
			Name:              "diagnostic",
			Class:             ActionClassService,
			RequiresWorkplace: false,
			RequiresSynthesis: false,
			Operations: []model.OperationSpec{
				builtinOperation(OperationKindFinalize, "Завершающая фиксация", true),
			},
		}},
		profiles: &stubProfileResolver{err: expectedErr},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "diagnostic",
			StructuredInput: &StructuredInput{Task: "Проверить маршрут."},
		},
	})
	if err != nil {
		t.Fatalf("execute custom action: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.Operations) != 1 {
		t.Fatalf("operation list must come from resolved action: %#v", result.Operations)
	}
	if result.Operations[0].Name != OperationKindFinalize {
		t.Fatalf("unexpected operation order: %#v", result.Operations)
	}
}

func TestServiceExecuteResolveProfileUsesOperationIOAndKeepsStateProfile(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	resources := &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5"}}
	service := &Service{
		logger: log.Default(),
		actions: &stubActionResolver{action: model.Action{
			Name:              "profile-check",
			Class:             ActionClassEngineeringSynthesis,
			Profile:           "coder",
			RequiresSynthesis: true,
			Operations: []model.OperationSpec{
				builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
				{
					Name:   OperationKindResolveProfile,
					Kind:   OperationKindResolveProfile,
					Title:  "Выбор исполнительного профиля",
					Origin: OperationOriginBuiltin,
					In:     model.OperationMap{"profile_name": {Ref: "action.profile"}},
					Out:    model.OperationMap{"profile": {Ref: "data.profile"}},
				},
				builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true),
				builtinOperation(OperationKindFinalize, "Завершающая фиксация", true),
			},
		}},
		profiles:  &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder", StructuredOutput: true}},
		resources: resources,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "profile-check",
			StructuredInput: &StructuredInput{Task: "Проверить профиль."},
		},
	})
	if err != nil {
		t.Fatalf("execute action: %v", err)
	}
	operation := findOperationResult(result.Operations, OperationKindResolveProfile)
	if operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("resolve-profile must complete: %#v", result.Operations)
	}
	if !strings.Contains(operation.Input, "profile_name[action.profile]=coder") {
		t.Fatalf("resolve-profile input must use action mapping: %#v", operation)
	}
	if !strings.Contains(operation.Output, "profile[data.profile]=") || !strings.Contains(operation.Output, `"name":"coder"`) {
		t.Fatalf("resolve-profile output must include mapped profile: %#v", operation)
	}
	if resources.profile.Name != "coder" || resources.profile.ModelBinding != "coder" {
		t.Fatalf("resource allocation must receive state profile copy: %#v", resources.profile)
	}
}

func TestResolveProfileFillsActionDataAndStateProfile(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{
		Name:   OperationKindResolveProfile,
		Kind:   OperationKindResolveProfile,
		Origin: OperationOriginBuiltin,
		In:     model.OperationMap{"profile_name": {Ref: "action.profile"}},
		Out:    model.OperationMap{"profile": {Ref: "data.profile"}},
	}
	state := &operationExecution{
		in: model.Invocation{Profile: "coder"},
		action: model.Action{
			Operations: []model.OperationSpec{operation},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
	}

	err := builtinOperationExecutor{service: service}.resolveProfile(context.Background(), state, operation, OperationKindResolveProfile)
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	dataProfile, ok := state.data["profile"].(model.Profile)
	if !ok {
		t.Fatalf("resolve-profile must fill data.profile: %#v", state.data)
	}
	if dataProfile.Name != "coder" || dataProfile.ModelBinding != "coder" {
		t.Fatalf("unexpected data profile: %#v", dataProfile)
	}
	if state.profile.Name != "coder" || state.profile.ModelBinding != "coder" {
		t.Fatalf("state profile must keep compatibility copy: %#v", state.profile)
	}
}

func TestResolveProfileUsesOperationInputValue(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{
		Name:   OperationKindResolveProfile,
		Kind:   OperationKindResolveProfile,
		Origin: OperationOriginBuiltin,
		In:     model.OperationMap{"profile_name": {Value: json.RawMessage(`"review"`)}},
		Out:    model.OperationMap{"profile": {Ref: "data.profile"}},
	}
	state := &operationExecution{
		in:      model.Invocation{Profile: "coder"},
		action:  model.Action{Profile: "coder", Operations: []model.OperationSpec{operation}},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	profiles := &stubProfileResolver{profile: model.Profile{Name: "review", Mode: "manual", ModelBinding: "review"}}
	service := &Service{
		logger:   log.Default(),
		profiles: profiles,
	}

	err := builtinOperationExecutor{service: service}.resolveProfile(context.Background(), state, operation, OperationKindResolveProfile)
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if state.profile.Name != "review" {
		t.Fatalf("resolve-profile must use operation input value: %#v", state.profile)
	}
	if profiles.invocation.Profile != "review" {
		t.Fatalf("profile resolver must receive operation input value, got %#v", profiles.invocation)
	}
	dataProfile, ok := state.data["profile"].(model.Profile)
	if !ok || dataProfile.Name != "review" {
		t.Fatalf("resolve-profile must write selected profile to data.profile: %#v", state.data)
	}
}

func TestActionResolutionKeepsProfileFromActionTemplate(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(testExecutionMethodologyCatalog(), invocation{Action: "review"})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	if action.Class != ActionClassReview {
		t.Fatalf("action must select review class: %#v", action)
	}
	if action.Profile != "review" {
		t.Fatalf("action template must select technical profile: %#v", action)
	}
}

func TestActionResolutionPrefersExactNameBeforeAlias(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{
			{
				Name:              ActionClassEngineeringSynthesis,
				Class:             ActionClassEngineeringSynthesis,
				Profile:           "global",
				Aliases:           []string{"implement"},
				RequiresWorkplace: boolRef(true),
				RequiresSynthesis: boolRef(true),
				Operations:        testExecutionOperations(OperationKindFinalize),
			},
			{
				Name:              "implement",
				Class:             ActionClassService,
				Profile:           "local",
				RequiresWorkplace: boolRef(false),
				RequiresSynthesis: boolRef(false),
				Operations:        testExecutionOperations(OperationKindFinalize),
			},
		},
	}, invocation{Action: "implement"})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	if action.Name != "implement" || action.Profile != "local" || action.Class != ActionClassService {
		t.Fatalf("exact action name must win over earlier alias: %#v", action)
	}
}

func TestActionResolutionPrefersLaterAlias(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{
			{
				Name:              ActionClassEngineeringSynthesis,
				Class:             ActionClassEngineeringSynthesis,
				Profile:           "global",
				Aliases:           []string{"implement"},
				RequiresWorkplace: boolRef(true),
				RequiresSynthesis: boolRef(true),
				Operations:        testExecutionOperations(OperationKindFinalize),
			},
			{
				Name:              "local-implementation",
				Class:             ActionClassService,
				Profile:           "local",
				Aliases:           []string{"implement"},
				RequiresWorkplace: boolRef(false),
				RequiresSynthesis: boolRef(false),
				Operations:        testExecutionOperations(OperationKindFinalize),
			},
		},
	}, invocation{Action: "implement"})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	if action.Name != "local-implementation" || action.Profile != "local" || action.Class != ActionClassService {
		t.Fatalf("later alias must win over earlier alias: %#v", action)
	}
}

func TestActionResolutionRejectsDuplicateOperations(t *testing.T) {
	t.Parallel()

	_, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              "diagnostic",
			Class:             ActionClassService,
			RequiresWorkplace: boolRef(false),
			RequiresSynthesis: boolRef(false),
			Operations:        testExecutionOperations(OperationKindPrepareData, OperationKindPrepareData),
		}},
	}, invocation{Action: "diagnostic"})
	if err == nil {
		t.Fatal("expected duplicate operation error")
	}
	if !strings.Contains(err.Error(), `операция "prepare-data" задана повторно`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestActionResolutionMakesReviewRemarksRequiredForApplyReviewComments(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionApplyReviewComments,
			Class:             ActionClassEngineeringSynthesis,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations:        testExecutionOperations(OperationKindLoadReviewRemarks, OperationKindFinalize),
		}},
	}, invocation{Action: ActionApplyReviewComments})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindLoadReviewRemarks)
	if operation == nil || !operation.Required {
		t.Fatalf("load-review-remarks must be required for apply-review-comments legacy operation: %#v", action.Operations)
	}
}

func TestActionResolutionResolvesOperationsFromRegistry(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionApplyReviewComments,
			Class:             ActionClassEngineeringSynthesis,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations: []methodology.ActionOperation{
				{Name: OperationKindPrepareData},
				{Name: OperationKindLoadPullRequest},
				{Name: OperationKindLoadReviewRemarks},
				{Name: OperationKindResolveProfile, In: map[string]methodology.ActionMapping{"profile_name": mappingRef("action.profile")}, Out: map[string]methodology.ActionMapping{"profile": mappingRef("data.profile")}},
				{Name: OperationKindAllocateResources},
				{Name: OperationKindPrepareWorkplace},
				{Name: OperationKindBuildDirective},
				{Name: OperationKindLaunchSynthesis},
				{Name: OperationKindParseResult},
				{Name: OperationKindCommitPush},
				{Name: OperationKindPublishReviewResponses},
				{Name: OperationKindFinalize},
			},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindPrepareData, Kind: OperationKindPrepareData, Title: "Подготовка данных", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindLoadPullRequest, Kind: OperationKindLoadPullRequest, Title: "Получение запроса на слияние", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindLoadReviewRemarks, Kind: OperationKindLoadReviewRemarks, Title: "Получение замечаний ревизии", Origin: OperationOriginBuiltin},
			{Name: OperationKindResolveProfile, Kind: OperationKindResolveProfile, Title: "Выбор исполнительного профиля", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindAllocateResources, Kind: OperationKindAllocateResources, Title: "Ресурсное снабжение", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindPrepareWorkplace, Kind: OperationKindPrepareWorkplace, Title: "Подготовка рабочего места", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindBuildDirective, Kind: OperationKindBuildDirective, Title: "Сборка исполнительной директивы", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindLaunchSynthesis, Kind: OperationKindLaunchSynthesis, Title: "Запуск синтеза", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindParseResult, Kind: OperationKindParseResult, Title: "Разбор результата", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindCommitPush, Kind: OperationKindCommitPush, Title: "Создание коммита и отправка ветки", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses, Title: "Запись ответов на замечания", Origin: OperationOriginBuiltin, Required: boolRef(true)},
			{Name: OperationKindFinalize, Kind: OperationKindFinalize, Title: "Завершающая фиксация", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionApplyReviewComments})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}

	expected := []struct {
		name     string
		required bool
	}{
		{OperationKindPrepareData, true},
		{OperationKindLoadPullRequest, true},
		{OperationKindLoadReviewRemarks, true},
		{OperationKindResolveProfile, true},
		{OperationKindAllocateResources, true},
		{OperationKindPrepareWorkplace, true},
		{OperationKindBuildDirective, true},
		{OperationKindLaunchSynthesis, true},
		{OperationKindParseResult, true},
		{OperationKindCommitPush, true},
		{OperationKindPublishReviewResponses, true},
		{OperationKindFinalize, true},
	}
	if len(action.Operations) != len(expected) {
		t.Fatalf("unexpected operation count: %#v", action.Operations)
	}
	for index, expectation := range expected {
		operation := action.Operations[index]
		if operation.Name != expectation.name {
			t.Fatalf("unexpected operation at %d: %#v", index, action.Operations)
		}
		if operation.Required != expectation.required {
			t.Fatalf("unexpected required flag for %q: %#v", expectation.name, operation)
		}
	}
	profileOperation := findOperationSpec(action, OperationKindResolveProfile)
	if profileOperation == nil {
		t.Fatalf("resolve-profile operation must be present: %#v", action.Operations)
	}
	if profileOperation.In["profile_name"].Ref != "action.profile" || profileOperation.Out["profile"].Ref != "data.profile" {
		t.Fatalf("resolve-profile must keep action data mapping: %#v", profileOperation)
	}
}

func TestServiceExecuteRunsCommitPushOnlyAsActionOperation(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	gitConfig := &model.GitConfig{Identity: &model.GitIdentityConfig{AuthorName: "Progress", AuthorEmail: "progress@example.com", CommitterName: "Progress", CommitterEmail: "progress@example.com"}}
	allocation := model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder", Git: gitConfig}
	launcher := &stubLauncher{
		result:        model.LaunchResult{Status: "completed", Summary: "launch complete", StructuredOutput: &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}},
		commitSummary: "git=committed+pushed branch=task-97",
	}
	service := &Service{
		logger:     log.Default(),
		actions:    newMethodologyActionResolver(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: allocation},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher:   launcher,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement-commit",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 97},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if err != nil {
		t.Fatalf("execute commit action: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !launcher.commitCalled {
		t.Fatal("commit-push operation must call launcher commit stage")
	}
	if launcher.commitAllocation.Git != gitConfig {
		t.Fatalf("commit-push operation must pass allocated git config: %#v", launcher.commitAllocation.Git)
	}
	if launcher.invocation.Launch.CommitPush {
		t.Fatalf("launch synthesis must not receive hidden commit-push flag: %#v", launcher.invocation.Launch)
	}
	commitOperation := findOperationResult(result.Operations, OperationKindCommitPush)
	if commitOperation == nil || commitOperation.Status != OperationStatusCompleted || commitOperation.Summary != "git=committed+pushed branch=task-97" {
		t.Fatalf("unexpected commit operation: %#v", commitOperation)
	}
	if !strings.Contains(result.Summary, "git=committed+pushed branch=task-97") {
		t.Fatalf("result summary must include commit operation summary: %q", result.Summary)
	}
}

func TestServiceExecuteRecordsCommitPushCancellationInHistory(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	launcher := &stubLauncher{
		result: model.LaunchResult{Status: "completed", Summary: "launch complete", StructuredOutput: &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}},
		commit: func(context.Context, model.Invocation, model.Allocation, model.Workplace, *model.StructuredOutput) (string, error) {
			cancel()
			return "", context.Canceled
		},
	}
	service := &Service{
		logger:     log.Default(),
		actions:    newMethodologyActionResolver(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher:   launcher,
	}

	result, err := service.ExecuteAction(ctx, ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement-commit",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 98},
			StructuredInput: &StructuredInput{Task: "Ship it."},
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected commit cancellation, got %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("canceled commit-push must fail action result: %#v", result)
	}

	runs, err := history.List(context.Background(), root, history.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one sqlite history run, got %d", len(runs))
	}
	if runs[0].Status != "failed" || runs[0].Error != context.Canceled.Error() || strings.TrimSpace(runs[0].RawStructuredOutput) == "" {
		t.Fatalf("canceled commit-push must overwrite completed launch history: %#v", runs[0])
	}
}

func TestServiceExecuteStartImplementationPublishesPullRequest(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	launcher := &stubLauncher{
		result: model.LaunchResult{
			Status: "completed",
			StructuredOutput: &model.StructuredOutput{
				Summary:       "Реализация выполнена.",
				CommitMessage: "Добавить действие исполнения",
				Changes:       []model.StructuredChange{{Summary: "Добавлена операция публикации запроса на слияние."}},
			},
		},
		commitSummary: "git=committed+pushed branch=132",
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "create" || req.ObjectType != "merge-request" {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{
				PullRequestStatus: &integration.PullRequestStatus{Repository: req.Repository, Number: 17, State: "OPEN", URL: "https://github.com/owner/name/pull/17"},
				OperationResult:   &integration.OperationResult{Status: "ok", ExternalID: "17", URL: "https://github.com/owner/name/pull/17"},
			}, nil
		},
	}
	workplaces := &stubWorkplaceManager{workplace: model.Workplace{Name: root, RepositoryRoot: root, Ready: true}}
	service := &Service{
		logger:       log.Default(),
		actions:      newMethodologyActionResolver(),
		profiles:     &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:    &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces:   workplaces,
		launcher:     launcher,
		integrations: integrations,
		runGitOutput: func(_ context.Context, dir string, args ...string) (string, error) {
			if dir != root || strings.Join(args, " ") != "symbolic-ref refs/remotes/origin/HEAD" {
				return "", errors.New("unexpected git command")
			}
			return "refs/remotes/origin/develop\n", nil
		},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        ActionStartImplementationPR,
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 132, Title: "Поддержать действие"},
			StructuredInput: &StructuredInput{
				Task: "Выполнить реализацию.",
			},
		},
	})
	if err != nil {
		t.Fatalf("execute implementation action: %v", err)
	}
	if !launcher.commitCalled {
		t.Fatal("start implementation action must push the task branch before pull request publication")
	}
	if workplaces.invocation.Workplace.Name != "132" || workplaces.invocation.Workplace.HeadRef != "" {
		t.Fatalf("start implementation must prepare a new task branch without forced head_ref: %#v", workplaces.invocation.Workplace)
	}
	if len(integrations.calls) != 1 {
		t.Fatalf("expected one integration request, got %#v", integrations.calls)
	}
	request := integrations.calls[0]
	if request.Repository != "owner/name" || request.Base != "develop" || request.Head != "132" || request.Title == "" {
		t.Fatalf("unexpected pull request publication request: %#v", request)
	}
	operation := findOperationResult(result.Operations, OperationKindPublishMergeRequest)
	if operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("pull request operation must be completed: %#v", result.Operations)
	}
	if !strings.Contains(result.Summary, "pull-request=17") {
		t.Fatalf("result summary must include pull request diagnostics: %q", result.Summary)
	}
}

func TestServiceExecuteStartImplementationUsesPullRequestBaseForWorkplace(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	launcher := &stubLauncher{
		result: model.LaunchResult{
			Status:           "completed",
			StructuredOutput: &model.StructuredOutput{Summary: "Реализация выполнена.", CommitMessage: "Исправить маршрут."},
		},
		commitSummary: "git=committed+pushed branch=112",
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "create" || req.Base != "release" || req.Head != "112" {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{
				PullRequestStatus: &integration.PullRequestStatus{Repository: req.Repository, Number: 18, State: "OPEN", URL: "https://github.com/owner/name/pull/18"},
				OperationResult:   &integration.OperationResult{Status: "ok", ExternalID: "18", URL: "https://github.com/owner/name/pull/18"},
			}, nil
		},
	}
	workplaces := &stubWorkplaceManager{workplace: model.Workplace{Name: root, RepositoryRoot: root, Ready: true}}
	service := &Service{
		logger:       log.Default(),
		actions:      newMethodologyActionResolver(),
		profiles:     &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:    &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces:   workplaces,
		launcher:     launcher,
		integrations: integrations,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        ActionStartImplementationPR,
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112, Title: "Поддержать действие"},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "owner/name",
				Attributes: map[string]string{
					"base_ref": "release",
					"head_ref": "112",
				},
			}},
			StructuredInput: &StructuredInput{Task: "Выполнить реализацию."},
		},
	})
	if err != nil {
		t.Fatalf("execute implementation action: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if workplaces.invocation.Workplace.Name != "112" || workplaces.invocation.Workplace.BaseRef != "release" || workplaces.invocation.Workplace.HeadRef != "112" {
		t.Fatalf("pull request refs must be synchronized with workplace: %#v", workplaces.invocation.Workplace)
	}
	if len(integrations.calls) != 1 {
		t.Fatalf("expected one integration request, got %#v", integrations.calls)
	}
}

func TestServiceExecuteEngineeringSynthesisCommitDoesNotForceHeadRefWithoutMergeRequest(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	launcher := &stubLauncher{
		result: model.LaunchResult{
			Status:           "completed",
			StructuredOutput: &model.StructuredOutput{Summary: "Реализация выполнена.", CommitMessage: "Выполнить первичную реализацию."},
		},
		commitSummary: "git=committed+pushed branch=101",
	}
	service := &Service{
		logger:     log.Default(),
		actions:    newMethodologyActionResolver(),
		profiles:   &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:  &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces: &stubWorkplaceManager{workplace: model.Workplace{Name: root, RepositoryRoot: root, Ready: true}},
		launcher:   launcher,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        "engineering-synthesis-commit",
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 101, Title: "Проверить первичную реализацию"},
			StructuredInput: &StructuredInput{
				Task: "Выполнить первичную реализацию.",
			},
		},
	})
	if err != nil {
		t.Fatalf("execute engineering-synthesis-commit action: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	// Для первичного действия без MR контуру не нужно принудительно использовать HeadRef из номера задачи.
	// Имя рабочего места должно оставаться совпадающим с номером задачи, а ветка создаётся по его имени.
	// Это позволит создать новую рабочую ветку от базовой при отсутствии такой ветки в origin.
	// Проверяем только синхронизацию рабочего места: отсутствие HeadRef означает fallback на имя рабочеого места.
	if workplaces, ok := service.workplaces.(*stubWorkplaceManager); !ok || workplaces.invocation.Workplace.HeadRef != "" {
		t.Fatalf("engineering-synthesis-commit must not prefill workplace head_ref: %#v", workplaces)
	}
}

func TestServiceExecuteStartImplementationRejectsMismatchedHeadBranch(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	service := &Service{
		logger:     log.Default(),
		actions:    newMethodologyActionResolver(),
		profiles:   &stubProfileResolver{err: errors.New("profile must not be resolved")},
		workplaces: &stubWorkplaceManager{err: errors.New("workplace must not be prepared")},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        ActionStartImplementationPR,
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112, Title: "Поддержать действие"},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "owner/name",
				Attributes: map[string]string{
					"base_ref": "release",
					"head_ref": "feature-x",
				},
			}},
			StructuredInput: &StructuredInput{Task: "Выполнить реализацию."},
		},
	})
	if err == nil {
		t.Fatal("expected mismatched head branch error")
	}
	if !strings.Contains(err.Error(), `head branch "feature-x" does not match workplace branch "112"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("unexpected result: %#v", result)
	}
	operation := findOperationResult(result.Operations, OperationKindPrepareData)
	if operation == nil || operation.Status != OperationStatusFailed || operation.Failure == nil || operation.Failure.Code != "pull_request_branch_mismatch" {
		t.Fatalf("prepare data must keep branch mismatch diagnostics: %#v", result.Operations)
	}
}

func TestServiceExecuteReviewPullRequestPublishesRemarks(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	launcher := &stubLauncher{
		result: model.LaunchResult{
			Status: "completed",
			StructuredOutput: &model.StructuredOutput{
				Summary: "Ревизия выполнена.",
				Remarks: []model.StructuredRemark{{
					ID:       "remark-1",
					Type:     "defect",
					Severity: "major",
					Title:    "Не хватает проверки",
					Body:     "Добавьте проверку отказа интеграционной операции.",
					Path:     "internal/execution/integration_operations.go",
					Line:     42,
				}, {
					ID:    "remark-2",
					Title: "Общее замечание",
					Body:  "Проверьте описание результата.",
				}, {
					ID:    "remark-3",
					Title: "Неполная привязка без строки",
					Body:  "Публикуется как общий комментарий.",
					Path:  "internal/execution/integration_operations.go",
				}, {
					ID:    "remark-4",
					Title: "Неполная привязка без пути",
					Body:  "Публикуется как общий комментарий.",
					Line:  42,
				}},
			},
		},
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			switch req.Operation {
			case "get":
				return integration.Response{MergeRequest: &integration.MergeRequest{Repository: req.Repository, Number: req.Number, State: "OPEN", BaseRef: "main", HeadRef: "feature/review"}}, nil
			case "comments":
				return integration.Response{ReviewRemarks: []integration.ReviewRemark{{
					Repository:         req.Repository,
					MergeRequestNumber: req.Number,
					ExternalID:         "previous-comment",
					State:              "open",
					Body:               "Ранее записанное замечание.",
				}}}, nil
			case "create":
				if req.Resource != "comment" || req.ObjectType != "comment" || !strings.Contains(req.Body, "## Замечание ревизии") {
					t.Fatalf("unexpected review remark publication request: %#v", req)
				}
				return integration.Response{OperationResult: &integration.OperationResult{Status: "ok", URL: "https://github.com/owner/name/pull/17#issuecomment-2"}}, nil
			default:
				t.Fatalf("unexpected integration request: %#v", req)
				return integration.Response{}, nil
			}
		},
	}
	workplaces := &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}}
	service := &Service{
		logger:       log.Default(),
		actions:      newMethodologyActionResolver(),
		profiles:     &stubProfileResolver{profile: model.Profile{Name: "review", Mode: "manual", ModelBinding: "review"}},
		resources:    &stubResourceProvider{allocation: model.Allocation{Resource: "binding:review", Reserved: true, Runner: "codex", Model: "openai/gpt-5.5", ModelBinding: "review"}},
		workplaces:   workplaces,
		launcher:     launcher,
		integrations: integrations,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        ActionReviewPullRequest,
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "owner/name",
				Number:     17,
			}},
			StructuredInput: &StructuredInput{Task: "Провести ревизию запроса на слияние."},
		},
	})
	if err != nil {
		t.Fatalf("execute review action: %v", err)
	}
	if len(launcher.invocation.Launch.StructuredInput.ReviewRemarks) != 1 || launcher.invocation.Launch.StructuredInput.ReviewRemarks[0].ID != "previous-comment" {
		t.Fatalf("existing review remarks must be passed into review synthesis: %#v", launcher.invocation.Launch.StructuredInput)
	}
	if workplaces.invocation.Workplace.Name != "feature-review" || workplaces.invocation.Workplace.HeadRef != "feature/review" || workplaces.invocation.Workplace.BaseRef != "main" {
		t.Fatalf("review action must use pull request head for workplace: %#v", workplaces.invocation.Workplace)
	}
	if len(integrations.calls) != 6 {
		t.Fatalf("expected get, comments and create integration calls, got %#v", integrations.calls)
	}
	if integrations.calls[2].Number != 17 || integrations.calls[2].Repository != "owner/name" {
		t.Fatalf("unexpected review remark target: %#v", integrations.calls[2])
	}
	if integrations.calls[2].Path != "internal/execution/integration_operations.go" || integrations.calls[2].Line != 42 || integrations.calls[2].Side != "RIGHT" {
		t.Fatalf("inline review remark must keep diff location with default side: %#v", integrations.calls[2])
	}
	if integrations.calls[3].Path != "" || integrations.calls[3].Line != 0 || integrations.calls[3].Side != "" {
		t.Fatalf("review remark without location must stay pull request comment: %#v", integrations.calls[3])
	}
	if integrations.calls[4].Path != "" || integrations.calls[4].Line != 0 || integrations.calls[4].Side != "" {
		t.Fatalf("review remark without line must stay pull request comment: %#v", integrations.calls[4])
	}
	if integrations.calls[5].Path != "" || integrations.calls[5].Line != 0 || integrations.calls[5].Side != "" {
		t.Fatalf("review remark without path must stay pull request comment: %#v", integrations.calls[5])
	}
	operation := findOperationResult(result.Operations, OperationKindPublishReviewRemarks)
	if operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("review remark operation must be completed: %#v", result.Operations)
	}
}

func TestPublishPullRequestCommentsRejectsNegativeInlineLine(t *testing.T) {
	integrations := &stubIntegrationExecutor{}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{
		Body: "## Замечание ревизии\n\nНекорректная строка.",
		Path: "internal/execution/integration_operations.go",
		Line: -1,
		Side: "RIGHT",
	}, {
		Body: "## Замечание ревизии\n\nКорректная строка.",
		Path: "internal/execution/integration_operations.go",
		Line: 42,
		Side: "RIGHT",
	}})
	if err == nil {
		t.Fatal("expected negative inline line error")
	}
	if !strings.Contains(err.Error(), "inline line must be positive or omitted, got -1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("valid review remark must still be published, got count=%d", count)
	}
	if len(integrations.calls) != 1 {
		t.Fatalf("negative line must not be sent to integration executor: %#v", integrations.calls)
	}
	if integrations.calls[0].Path != "internal/execution/integration_operations.go" || integrations.calls[0].Line != 42 || integrations.calls[0].Side != "RIGHT" {
		t.Fatalf("valid inline remark must keep location: %#v", integrations.calls[0])
	}
}

func TestServiceExecuteReviewPullRequestContinuesWhenOptionalRemarksFail(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	launcher := &stubLauncher{
		result: model.LaunchResult{
			Status: "completed",
			StructuredOutput: &model.StructuredOutput{
				Summary: "Ревизия выполнена.",
				Conclusion: &model.StructuredConclusion{
					Status:  "approve",
					Summary: "Замечаний не найдено.",
				},
			},
		},
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			switch req.Operation {
			case "get":
				return integration.Response{MergeRequest: &integration.MergeRequest{Repository: req.Repository, Number: req.Number, State: "OPEN", BaseRef: "main", HeadRef: "112"}}, nil
			case "comments":
				return integration.Response{}, errors.New("temporary comments outage")
			case "create":
				return integration.Response{OperationResult: &integration.OperationResult{Status: "ok", URL: "https://github.com/owner/name/pull/17#issuecomment-2"}}, nil
			default:
				t.Fatalf("unexpected integration request: %#v", req)
				return integration.Response{}, nil
			}
		},
	}
	service := &Service{
		logger:       log.Default(),
		actions:      newMethodologyActionResolver(),
		profiles:     &stubProfileResolver{profile: model.Profile{Name: "review", Mode: "manual", ModelBinding: "review"}},
		resources:    &stubResourceProvider{allocation: model.Allocation{Resource: "binding:review", Reserved: true, Runner: "codex", Model: "openai/gpt-5.5", ModelBinding: "review"}},
		workplaces:   &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}},
		launcher:     launcher,
		integrations: integrations,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        ActionReviewPullRequest,
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "owner/name",
				Number:     17,
			}},
			StructuredInput: &StructuredInput{Task: "Провести ревизию запроса на слияние."},
		},
	})
	if err != nil {
		t.Fatalf("review action must continue after optional remarks failure: %v", err)
	}
	remarksOperation := findOperationResult(result.Operations, OperationKindLoadReviewRemarks)
	if remarksOperation == nil || remarksOperation.Status != OperationStatusSkipped {
		t.Fatalf("optional remarks operation must be skipped: %#v", result.Operations)
	}
	if !strings.Contains(remarksOperation.Summary, "temporary comments outage") {
		t.Fatalf("skipped operation must keep diagnostics: %#v", remarksOperation)
	}
	if launcher.invocation.Launch.StructuredInput == nil || len(launcher.invocation.Launch.StructuredInput.ReviewRemarks) != 0 {
		t.Fatalf("review synthesis must proceed without loaded remarks: %#v", launcher.invocation.Launch.StructuredInput)
	}
	operation := findOperationResult(result.Operations, OperationKindPublishReviewRemarks)
	if operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("review publication must still complete: %#v", result.Operations)
	}
}

func TestServiceExecuteApplyReviewCommentsLoadsRemarksAndPublishesResponses(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	launcher := &stubLauncher{
		result: model.LaunchResult{
			Status: "completed",
			StructuredOutput: &model.StructuredOutput{
				Summary:       "Замечание исправлено.",
				CommitMessage: "Исправить замечание ревизии",
				ReviewResponses: []model.StructuredResponse{{
					RemarkID: "thread-1",
					Status:   "resolved",
					Summary:  "Исправлено.",
				}},
			},
		},
		commitSummary: "git=committed+pushed branch=feature/fixes",
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			switch req.Operation {
			case "get":
				return integration.Response{MergeRequest: &integration.MergeRequest{Repository: req.Repository, Number: req.Number, State: "OPEN", BaseRef: "main", HeadRef: "feature/fixes"}}, nil
			case "comments":
				return integration.Response{ReviewRemarks: []integration.ReviewRemark{{
					Repository:         req.Repository,
					MergeRequestNumber: req.Number,
					ExternalID:         "thread-1",
					ReplyToID:          "thread-1",
					State:              "unresolved",
					Body:               "Нужно добавить проверку.",
					Path:               "internal/execution/service.go",
					Line:               12,
				}}}, nil
			case "create":
				return integration.Response{OperationResult: &integration.OperationResult{Status: "ok", URL: "https://github.com/owner/name/pull/17#issuecomment-1"}}, nil
			case "reply":
				return integration.Response{OperationResult: &integration.OperationResult{Status: "ok", ExternalID: "comment-1"}}, nil
			case "resolve":
				return integration.Response{OperationResult: &integration.OperationResult{Status: "ok", ExternalID: req.ThreadID}}, nil
			default:
				t.Fatalf("unexpected integration request: %#v", req)
				return integration.Response{}, nil
			}
		},
	}
	workplaces := &stubWorkplaceManager{workplace: model.Workplace{Name: root, Ready: true}}
	service := &Service{
		logger:       log.Default(),
		actions:      newMethodologyActionResolver(),
		profiles:     &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}},
		resources:    &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Reserved: true, Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}},
		workplaces:   workplaces,
		launcher:     launcher,
		integrations: integrations,
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:        ActionApplyReviewComments,
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "owner/name",
				Number:     17,
			}},
			StructuredInput: &StructuredInput{Task: "Исправить замечания ревизии."},
		},
	})
	if err != nil {
		t.Fatalf("execute review rework action: %v", err)
	}
	if len(launcher.invocation.Launch.StructuredInput.ReviewRemarks) != 1 || launcher.invocation.Launch.StructuredInput.ReviewRemarks[0].ID != "thread-1" {
		t.Fatalf("review remarks must be passed into synthesis: %#v", launcher.invocation.Launch.StructuredInput)
	}
	if !launcher.commitCalled {
		t.Fatal("review rework action must push fixes before publishing responses")
	}
	if workplaces.invocation.Workplace.Name != "feature-fixes" || workplaces.invocation.Workplace.HeadRef != "feature/fixes" || workplaces.invocation.Workplace.BaseRef != "main" {
		t.Fatalf("review rework action must use pull request head for workplace: %#v", workplaces.invocation.Workplace)
	}
	if len(integrations.calls) != 4 {
		t.Fatalf("expected get, comments, reply and resolve integration calls, got %#v", integrations.calls)
	}
	if integrations.calls[2].Operation != "reply" || integrations.calls[2].ThreadID != "thread-1" || integrations.calls[3].Operation != "resolve" || integrations.calls[3].ThreadID != "thread-1" {
		t.Fatalf("unexpected response publication calls: %#v", integrations.calls)
	}
	operation := findOperationResult(result.Operations, OperationKindPublishReviewResponses)
	if operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("review response operation must be completed: %#v", result.Operations)
	}
}

func TestServiceStartEnrichesRunningHistoryRowBeforeLaunch(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	historyRoot := t.TempDir()
	writeTestMethodologyCatalog(t, historyRoot)
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

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{
		Assignment: &ExecutionAssignment{
			Action:          "implement",
			CanonicalTask:   &ObjectRef{Type: "task", Number: 58},
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

func TestServiceLaunchReturnsResumeUnsupportedForUnsupportedRunner(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default(), launcher: launchservice.NewService()}
	workplace := t.TempDir()
	result, err := service.launch(context.Background(), invocation{
		Launch: launchSpec{
			Directory: workplace,
			Runner:    "custom-runner",
			Model:     "openai/gpt-5.4",
			Prompt:    "Continue task",
			Resume:    &model.ResumeSpec{ParentRunID: 42, RunnerSessionID: "session-42", MessageSource: "message"},
		},
	}, profile{Name: "coder", Mode: "manual"}, allocation{Runner: "custom-runner", Model: "openai/gpt-5.4"}, model.Workplace{Name: workplace, Ready: true})
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

type stubLauncher struct {
	invocation       model.Invocation
	profile          model.Profile
	result           model.LaunchResult
	err              error
	beforeReturn     func()
	commitCalled     bool
	commitAllocation model.Allocation
	commitSummary    string
	commitErr        error
	commit           func(context.Context, model.Invocation, model.Allocation, model.Workplace, *model.StructuredOutput) (string, error)
}

type stubIntegrationExecutor struct {
	calls   []integration.Request
	execute func(context.Context, integration.Request) (integration.Response, error)
}

func (s *stubIntegrationExecutor) Execute(ctx context.Context, req integration.Request) (integration.Response, error) {
	s.calls = append(s.calls, req)
	if s.execute != nil {
		return s.execute(ctx, req)
	}
	return integration.Response{Status: "ok"}, nil
}

func (s *stubLauncher) Launch(_ context.Context, in model.Invocation, profile model.Profile, _ model.Allocation, _ model.Workplace) (model.LaunchResult, error) {
	s.invocation = in
	s.profile = profile
	if s.beforeReturn != nil {
		s.beforeReturn()
	}
	if s.result.Status == "" && s.err == nil {
		return model.LaunchResult{Status: "completed"}, nil
	}
	return s.result, s.err
}

func (s *stubLauncher) CommitAndPush(ctx context.Context, in model.Invocation, allocation model.Allocation, workplace model.Workplace, output *model.StructuredOutput) (string, error) {
	s.commitCalled = true
	s.commitAllocation = allocation
	if s.commit != nil {
		return s.commit(ctx, in, allocation, workplace, output)
	}
	if s.commitErr != nil {
		return "", s.commitErr
	}
	return s.commitSummary, nil
}

type stubActionResolver struct {
	action model.Action
	err    error
}

func (s *stubActionResolver) ResolveAction(context.Context, model.Invocation) (model.Action, error) {
	if s.err != nil {
		return model.Action{}, s.err
	}
	return s.action, nil
}

type stubProfileResolver struct {
	profile    model.Profile
	invocation model.Invocation
	err        error
}

func (s *stubProfileResolver) Resolve(_ context.Context, in model.Invocation) (model.Profile, error) {
	if s.err != nil {
		return model.Profile{}, s.err
	}
	s.invocation = in
	return s.profile, nil
}

type stubResourceProvider struct {
	allocation model.Allocation
	profile    model.Profile
	err        error
}

func (s *stubResourceProvider) Allocate(_ context.Context, _ model.Invocation, profile model.Profile) (model.Allocation, error) {
	if s.err != nil {
		return model.Allocation{}, s.err
	}
	s.profile = profile
	return s.allocation, nil
}

type stubWorkplaceManager struct {
	workplace  model.Workplace
	invocation model.Invocation
	err        error
}

func (s *stubWorkplaceManager) Prepare(_ context.Context, in model.Invocation, _ model.Profile, _ model.Allocation) (model.Workplace, error) {
	s.invocation = in
	if s.err != nil {
		return model.Workplace{}, s.err
	}
	return s.workplace, nil
}

func withWorkingDirectory(t *testing.T, dir string) {
	t.Helper()
	writeTestMethodologyCatalog(t, dir)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

func writeTestMethodologyCatalog(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir methodology dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "catalog.json"), []byte(testExecutionMethodologyCatalogJSON), 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}
}

func testExecutionMethodologyCatalog() methodology.Catalog {
	return methodology.Catalog{
		Actions: []methodology.Action{
			{Name: ActionClassEngineeringSynthesis, Class: ActionClassEngineeringSynthesis, Profile: "default", Aliases: []string{"implement"}, RequiresWorkplace: boolRef(true), RequiresSynthesis: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindFinalize)},
			{Name: "engineering-synthesis-commit", Class: ActionClassEngineeringSynthesis, Profile: "default", Aliases: []string{"implement-commit"}, RequiresWorkplace: boolRef(true), RequiresSynthesis: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindCommitPush, OperationKindFinalize)},
			{Name: ActionStartImplementationPR, Class: ActionClassEngineeringSynthesis, Profile: "coder", RequiresWorkplace: boolRef(true), RequiresSynthesis: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindCommitPush, OperationKindPublishMergeRequest, OperationKindFinalize)},
			{Name: ActionClassReview, Class: ActionClassReview, Profile: "review", RequiresWorkplace: boolRef(true), RequiresSynthesis: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindFinalize)},
			{Name: ActionReviewPullRequest, Class: ActionClassReview, Profile: "review", RequiresWorkplace: boolRef(true), RequiresSynthesis: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindLoadPullRequest, optionalExecutionOperation(OperationKindLoadReviewRemarks), OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindPublishReviewRemarks, OperationKindFinalize)},
			{Name: ActionApplyReviewComments, Class: ActionClassEngineeringSynthesis, Profile: "coder", RequiresWorkplace: boolRef(true), RequiresSynthesis: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindLoadPullRequest, OperationKindLoadReviewRemarks, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindCommitPush, OperationKindPublishReviewResponses, OperationKindFinalize)},
			{Name: ActionClassIntegrationChange, Class: ActionClassIntegrationChange, Profile: "default", RequiresWorkplace: boolRef(false), RequiresSynthesis: boolRef(false), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildDirective, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindFinalize)},
		},
	}
}

func testExecutionOperations(operations ...any) []methodology.ActionOperation {
	result := make([]methodology.ActionOperation, 0, len(operations))
	for _, value := range operations {
		switch operation := value.(type) {
		case string:
			result = append(result, methodology.ActionOperation{Name: operation, Kind: operation, Origin: OperationOriginBuiltin, Required: boolRef(true)})
		case methodology.ActionOperation:
			result = append(result, operation)
		}
	}
	return result
}

func optionalExecutionOperation(name string) methodology.ActionOperation {
	return methodology.ActionOperation{Name: name, Kind: name, Origin: OperationOriginBuiltin, Required: boolRef(false)}
}

func boolRef(value bool) *bool {
	return &value
}

func mappingRef(value string) methodology.ActionMapping {
	return methodology.ActionMapping{Ref: &value}
}

const testExecutionMethodologyCatalogJSON = `{
  "actions": [
    {
      "name": "engineering-synthesis",
      "class": "engineering-synthesis",
      "profile": "default",
      "aliases": ["implement"],
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    },
    {
      "name": "engineering-synthesis-commit",
      "class": "engineering-synthesis",
      "profile": "default",
      "aliases": ["implement-commit"],
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "commit-push", "kind": "commit-push", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    },
    {
      "name": "start-implementation-pr",
      "class": "engineering-synthesis",
      "profile": "coder",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "commit-push", "kind": "commit-push", "origin": "builtin", "required": true},
        {"name": "publish-merge-request", "kind": "publish-merge-request", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    },
    {
      "name": "review",
      "class": "review",
      "profile": "review",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    },
    {
      "name": "review-pull-request",
      "class": "review",
      "profile": "review",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "load-pull-request", "kind": "load-pull-request", "origin": "builtin", "required": true},
        {"name": "load-review-remarks", "kind": "load-review-remarks", "origin": "builtin", "required": false},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "publish-review-remarks", "kind": "publish-review-remarks", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    },
    {
      "name": "apply-review-comments",
      "class": "engineering-synthesis",
      "profile": "coder",
      "requires_workplace": true,
      "requires_synthesis": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "load-pull-request", "kind": "load-pull-request", "origin": "builtin", "required": true},
        {"name": "load-review-remarks", "kind": "load-review-remarks", "origin": "builtin", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "commit-push", "kind": "commit-push", "origin": "builtin", "required": true},
        {"name": "publish-review-responses", "kind": "publish-review-responses", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    },
    {
      "name": "integration-change",
      "class": "integration-change",
      "profile": "default",
      "requires_workplace": false,
      "requires_synthesis": false,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "origin": "builtin", "required": true},
        {"name": "resolve-profile", "kind": "resolve-profile", "origin": "builtin", "required": true},
        {"name": "allocate-resources", "kind": "allocate-resources", "origin": "builtin", "required": true},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "origin": "builtin", "required": true},
        {"name": "build-directive", "kind": "build-directive", "origin": "builtin", "required": true},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "origin": "builtin", "required": true},
        {"name": "parse-result", "kind": "parse-result", "origin": "builtin", "required": true},
        {"name": "finalize", "kind": "finalize", "origin": "builtin", "required": true}
      ]
    }
  ]
}`
