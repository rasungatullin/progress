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
					In: model.OperationMap{
						"profile_name": {Ref: "action.profile"},
						"invocation":   {Ref: "data.invocation"},
					},
					Out: model.OperationMap{
						"profile": {Ref: "data.profile"},
						"result":  {Ref: "data.result"},
					},
				},
				{
					Name:   OperationKindAllocateResources,
					Kind:   OperationKindAllocateResources,
					Title:  "Ресурсное снабжение",
					Origin: OperationOriginBuiltin,
					In: model.OperationMap{
						"requires_synthesis": {Ref: "action.requires_synthesis"},
						"invocation":         {Ref: "data.invocation"},
						"profile":            {Ref: "data.profile"},
					},
					Out: model.OperationMap{"allocation": {Ref: "data.allocation"}},
				},
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
		t.Fatalf("resource allocation must receive mapped data profile: %#v", resources.profile)
	}
}

func TestResolveProfileFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{
		Name:   OperationKindResolveProfile,
		Kind:   OperationKindResolveProfile,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"profile_name": {Ref: "action.profile"},
			"invocation":   {Ref: "data.invocation"},
		},
		Out: model.OperationMap{
			"profile": {Ref: "data.profile"},
			"result":  {Ref: "data.result"},
		},
	}
	state := &operationExecution{
		in: model.Invocation{Profile: "coder"},
		action: model.Action{
			Profile:    "coder",
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "from-data", Action: "implement", Profile: "legacy"},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	profiles := &stubProfileResolver{profile: model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"}}
	service := &Service{logger: log.Default(), profiles: profiles}

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
	if state.profile.Name != "" || state.profile.ModelBinding != "" {
		t.Fatalf("resolve-profile must not write implicit state profile: %#v", state.profile)
	}
	if profiles.invocation.Task != "from-data" || profiles.invocation.Action != "implement" || profiles.invocation.Profile != "coder" {
		t.Fatalf("profile resolver must receive invocation from operation input: %#v", profiles.invocation)
	}
}

func TestResolveProfileUsesOperationInputValue(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{
		Name:   OperationKindResolveProfile,
		Kind:   OperationKindResolveProfile,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"profile_name": {Value: json.RawMessage(`"review"`)},
			"invocation":   {Ref: "data.invocation"},
		},
		Out: model.OperationMap{
			"profile": {Ref: "data.profile"},
			"result":  {Ref: "data.result"},
		},
	}
	state := &operationExecution{
		in:      model.Invocation{Profile: "coder"},
		action:  model.Action{Profile: "coder", Operations: []model.OperationSpec{operation}},
		data:    map[string]any{"invocation": model.Invocation{Task: "from-data", Profile: "coder"}},
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
	if profiles.invocation.Profile != "review" {
		t.Fatalf("profile resolver must receive operation input value, got %#v", profiles.invocation)
	}
	dataProfile, ok := state.data["profile"].(model.Profile)
	if !ok || dataProfile.Name != "review" {
		t.Fatalf("resolve-profile must write selected profile to data.profile: %#v", state.data)
	}
}

func TestResolveProfileFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{
		Name:   OperationKindResolveProfile,
		Kind:   OperationKindResolveProfile,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"profile_name": {Ref: "action.profile"},
			"invocation":   {Ref: "data.invocation"},
		},
		Out: model.OperationMap{
			"profile": {Ref: "data.profile"},
			"result":  {Ref: "data.result"},
		},
	}
	state := &operationExecution{
		action: model.Action{Profile: "coder", Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{err: errors.New("profile failed")},
	}

	err := builtinOperationExecutor{service: service}.resolveProfile(context.Background(), state, operation, OperationKindResolveProfile)
	if err == nil {
		t.Fatal("resolve profile must return profile error")
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("resolve-profile must not write implicit state result: %#v", state.result)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "profile failed") {
		t.Fatalf("resolve-profile must write failed result to data.result: %#v", state.data)
	}
}

func TestPrepareDataFillsActionData(t *testing.T) {
	t.Parallel()

	operation := prepareDataOperationSpec()
	assignment := &ExecutionAssignment{
		Action:          ActionStartImplementationPR,
		ExpectedResult:  "Выполнить реализацию.",
		Constraints:     []string{"Не менять публичный интерфейс."},
		CanonicalTask:   &ObjectRef{Type: "task", Repository: "owner/name", Number: 112},
		RelatedObjects:  []ObjectRef{{Type: "merge-request", Attributes: map[string]string{"base_ref": "main", "head_ref": "112"}}},
		Reasons:         []AssignmentReason{{Code: "route_selected", Message: "Маршрут выбрал реализацию."}},
		StructuredInput: &StructuredInput{Task: "Ship it."},
	}
	state := &operationExecution{
		in: model.Invocation{
			Assignment: assignment,
			Workplace:  model.WorkplaceSpec{Name: "112"},
		},
		assignment: assignment,
		action: model.Action{
			Name:       ActionStartImplementationPR,
			Operations: []model.OperationSpec{operation},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.prepareData(context.Background(), state, operation, OperationKindPrepareData)
	if err != nil {
		t.Fatalf("prepare data: %v", err)
	}
	dataInput, ok := state.data["structured_input"].(*StructuredInput)
	if !ok || dataInput.Task != "Ship it." {
		t.Fatalf("prepare-data must fill data.structured_input: %#v", state.data)
	}
	dataWorkplace, ok := state.data["workplace"].(model.WorkplaceSpec)
	if !ok || dataWorkplace.BaseRef != "main" || dataWorkplace.HeadRef != "112" {
		t.Fatalf("prepare-data must fill data.workplace with synchronized refs: %#v", state.data)
	}
	dataInvocation, ok := state.data["invocation"].(model.Invocation)
	if !ok {
		t.Fatalf("prepare-data must fill data.invocation: %#v", state.data)
	}
	if dataInvocation.Launch.StructuredInput == nil || dataInvocation.Launch.StructuredInput.Task != "Ship it." {
		t.Fatalf("data invocation must keep prepared structured input: %#v", dataInvocation)
	}
	if dataInvocation.Workplace.BaseRef != "main" || dataInvocation.Workplace.HeadRef != "112" {
		t.Fatalf("data invocation must keep prepared workplace refs: %#v", dataInvocation.Workplace)
	}
	if state.in.Launch.StructuredInput != nil || state.in.Workplace.BaseRef != "" || state.in.Workplace.HeadRef != "" {
		t.Fatalf("prepare-data must not write implicit state invocation: %#v", state.in)
	}
}

func TestPrepareDataUsesOperationInputValue(t *testing.T) {
	t.Parallel()

	operation := prepareDataOperationSpec()
	operation.In["structured_input"] = model.OperationMapping{Value: json.RawMessage(`{"task":"Из входа операции."}`)}
	state := &operationExecution{
		in: model.Invocation{
			Assignment: &ExecutionAssignment{StructuredInput: &StructuredInput{Task: "Из старого состояния."}},
		},
		assignment: &ExecutionAssignment{StructuredInput: &StructuredInput{Task: "Из старого состояния."}},
		action:     model.Action{Operations: []model.OperationSpec{operation}},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.prepareData(context.Background(), state, operation, OperationKindPrepareData)
	if err != nil {
		t.Fatalf("prepare data: %v", err)
	}
	dataInput, ok := state.data["structured_input"].(*StructuredInput)
	if !ok || dataInput.Task != "Из входа операции." {
		t.Fatalf("prepare-data must write selected structured input to data.structured_input: %#v", state.data)
	}
	dataInvocation, ok := state.data["invocation"].(model.Invocation)
	if !ok || dataInvocation.Assignment == nil || dataInvocation.Assignment.StructuredInput == nil || dataInvocation.Assignment.StructuredInput.Task != "Из входа операции." {
		t.Fatalf("prepare-data must write selected invocation to data.invocation: %#v", state.data)
	}
	if state.assignment.StructuredInput == nil || state.assignment.StructuredInput.Task != "Из старого состояния." {
		t.Fatalf("prepare-data must not write implicit state assignment: %#v", state.assignment)
	}
}

func TestAllocateResourcesFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := allocateResourcesOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Task:   "task-42",
			Action: "implement",
		},
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "from-data", Action: "implement"},
			"profile":    model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
		},
		profile: model.Profile{Name: "legacy", Mode: "manual", ModelBinding: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	resources := &stubResourceProvider{allocation: model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"}}
	service := &Service{
		logger:    log.Default(),
		resources: resources,
	}

	err := builtinOperationExecutor{service: service}.allocateResources(context.Background(), state, operation, OperationKindAllocateResources)
	if err != nil {
		t.Fatalf("allocate resources: %v", err)
	}
	dataAllocation, ok := state.data["allocation"].(model.Allocation)
	if !ok {
		t.Fatalf("allocate-resources must fill data.allocation: %#v", state.data)
	}
	if dataAllocation.Resource != "binding:coder" || dataAllocation.ModelBinding != "coder" {
		t.Fatalf("unexpected data allocation: %#v", dataAllocation)
	}
	if state.allocation.Resource != "" || state.allocation.ModelBinding != "" {
		t.Fatalf("allocate-resources must not write implicit state allocation: %#v", state.allocation)
	}
	if state.profile.Name != "legacy" || state.profile.ModelBinding != "legacy" {
		t.Fatalf("allocate-resources must not read or write implicit state profile: %#v", state.profile)
	}
	if resources.profile.Name != "coder" || resources.profile.ModelBinding != "coder" {
		t.Fatalf("resource allocation must use profile from operation input: %#v", resources.profile)
	}
	if resources.invocation.Task != "from-data" || resources.invocation.Action != "implement" {
		t.Fatalf("resource allocation must use invocation from operation input: %#v", resources.invocation)
	}
}

func TestAllocateResourcesWritesNotRequiredAllocationForActionWithoutSynthesis(t *testing.T) {
	t.Parallel()

	operation := allocateResourcesOperationSpec()
	state := &operationExecution{
		in: model.Invocation{Action: "service"},
		action: model.Action{
			RequiresSynthesis: false,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Action: "service"},
			"profile":    model.Profile{Name: "default", Mode: "manual"},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:    log.Default(),
		resources: &stubResourceProvider{err: errors.New("resources must not be called")},
	}

	err := builtinOperationExecutor{service: service}.allocateResources(context.Background(), state, operation, OperationKindAllocateResources)
	if err != nil {
		t.Fatalf("allocate resources: %v", err)
	}
	dataAllocation, ok := state.data["allocation"].(model.Allocation)
	if !ok || dataAllocation.Resource != "not-required" || dataAllocation.Source != "action-without-synthesis" {
		t.Fatalf("allocate-resources must write skipped allocation to data.allocation: %#v", state.data)
	}
	if state.allocation.Resource != "" || state.allocation.Source != "" {
		t.Fatalf("allocate-resources must not write skipped implicit state allocation: %#v", state.allocation)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindAllocateResources)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped allocation must keep contract output diagnostics: %#v", result)
	}
}

func TestAllocateResourcesFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := allocateResourcesOperationSpec()
	state := &operationExecution{
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
			"profile":    model.Profile{Name: "coder", Mode: "manual"},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:    log.Default(),
		resources: &stubResourceProvider{err: errors.New("resources failed")},
	}

	err := builtinOperationExecutor{service: service}.allocateResources(context.Background(), state, operation, OperationKindAllocateResources)
	if err == nil {
		t.Fatal("allocate resources must return resource error")
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("allocate-resources must not write implicit state result: %#v", state.result)
	}
}

func TestPrepareWorkplaceFillsActionData(t *testing.T) {
	t.Parallel()

	operation := prepareWorkplaceOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Task: "task-42",
			Workplace: model.WorkplaceSpec{
				Name: "task-42",
			},
		},
		action: model.Action{
			RequiresWorkplace: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{
				Task: "task-42",
				Workplace: model.WorkplaceSpec{
					Name: "task-42",
				},
			},
			"profile":    model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
			"allocation": model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
		},
		profile:    model.Profile{Name: "legacy", Mode: "manual", ModelBinding: "legacy"},
		allocation: model.Allocation{Resource: "legacy", Runner: "legacy"},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	workplaces := &stubWorkplaceManager{workplace: model.Workplace{Name: "/tmp/task-42", RepositoryRoot: "/tmp/repo", Ready: true}}
	service := &Service{
		logger:     log.Default(),
		workplaces: workplaces,
	}

	err := builtinOperationExecutor{service: service}.prepareWorkplace(context.Background(), state, operation, OperationKindPrepareWorkplace)
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}
	dataWorkplace, ok := state.data["workplace"].(model.Workplace)
	if !ok {
		t.Fatalf("prepare-workplace must fill data.workplace: %#v", state.data)
	}
	if dataWorkplace.Name != "/tmp/task-42" || !dataWorkplace.Ready {
		t.Fatalf("unexpected data workplace: %#v", dataWorkplace)
	}
	if state.workplace.Name != "" || state.workplace.Ready {
		t.Fatalf("prepare-workplace must not write implicit state workplace: %#v", state.workplace)
	}
	dataInvocation, ok := state.data["invocation"].(model.Invocation)
	if !ok || dataInvocation.Launch.Directory != "/tmp/task-42" {
		t.Fatalf("prepare-workplace must write launch directory to data.invocation: %#v", state.data)
	}
	if state.in.Launch.Directory != "" {
		t.Fatalf("prepare-workplace must not write implicit state invocation: %#v", state.in.Launch)
	}
	if state.profile.Name != "legacy" || state.profile.ModelBinding != "legacy" {
		t.Fatalf("prepare-workplace must not read or write implicit state profile: %#v", state.profile)
	}
	if state.allocation.Resource != "legacy" || state.allocation.Runner != "legacy" {
		t.Fatalf("prepare-workplace must not read or write implicit state allocation: %#v", state.allocation)
	}
	if workplaces.invocation.Workplace.Name != "task-42" {
		t.Fatalf("workplace manager must receive invocation from operation input: %#v", workplaces.invocation)
	}
	if workplaces.profile.Name != "coder" || workplaces.profile.ModelBinding != "coder" {
		t.Fatalf("workplace manager must receive profile from operation input: %#v", workplaces.profile)
	}
	if workplaces.allocation.Resource != "binding:coder" || workplaces.allocation.ModelBinding != "coder" {
		t.Fatalf("workplace manager must receive allocation from operation input: %#v", workplaces.allocation)
	}
}

func TestPrepareWorkplaceWritesLocalReadyWorkplaceForActionWithoutWorkplace(t *testing.T) {
	t.Parallel()

	operation := prepareWorkplaceOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Launch: model.LaunchSpec{Directory: "/repo"},
		},
		action: model.Action{
			RequiresWorkplace: false,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Launch: model.LaunchSpec{Directory: "/repo"}},
			"profile":    model.Profile{Name: "default", Mode: "manual"},
			"allocation": model.Allocation{Resource: "not-required", Source: "action-without-synthesis"},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:     log.Default(),
		workplaces: &stubWorkplaceManager{err: errors.New("workplace must not be called")},
	}

	err := builtinOperationExecutor{service: service}.prepareWorkplace(context.Background(), state, operation, OperationKindPrepareWorkplace)
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}
	dataWorkplace, ok := state.data["workplace"].(model.Workplace)
	if !ok || dataWorkplace.Name != "/repo" || !dataWorkplace.Ready {
		t.Fatalf("prepare-workplace must write skipped workplace to data.workplace: %#v", state.data)
	}
	if state.workplace.Name != "" || state.workplace.Ready {
		t.Fatalf("prepare-workplace must not write skipped implicit state workplace: %#v", state.workplace)
	}
	dataInvocation, ok := state.data["invocation"].(model.Invocation)
	if !ok || dataInvocation.Launch.Directory != "/repo" {
		t.Fatalf("prepare-workplace must keep skipped invocation output: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPrepareWorkplace)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped workplace must keep contract output diagnostics: %#v", result)
	}
}

func TestPrepareWorkplaceFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := prepareWorkplaceOperationSpec()
	state := &operationExecution{
		action: model.Action{
			RequiresWorkplace: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
			"profile":    model.Profile{Name: "coder", Mode: "manual"},
			"allocation": model.Allocation{Resource: "binding:coder"},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:     log.Default(),
		workplaces: &stubWorkplaceManager{err: errors.New("workplace failed")},
	}

	err := builtinOperationExecutor{service: service}.prepareWorkplace(context.Background(), state, operation, OperationKindPrepareWorkplace)
	if err == nil {
		t.Fatal("prepare workplace must return workplace error")
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("prepare-workplace must not write implicit state result: %#v", state.result)
	}
}

func TestBuildDirectiveFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := buildDirectiveOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Task: "legacy",
			Launch: model.LaunchSpec{
				StructuredInput: &StructuredInput{Task: "Legacy."},
			},
		},
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42", Launch: model.LaunchSpec{StructuredInput: &StructuredInput{Task: "Ship it."}}},
			"allocation": model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
		},
		allocation: model.Allocation{Resource: "legacy", Runner: "legacy", Model: "legacy"},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.buildDirective(context.Background(), state, operation, OperationKindBuildDirective)
	if err != nil {
		t.Fatalf("build directive: %v", err)
	}
	directive, ok := state.data["directive"].(model.LaunchSpec)
	if !ok {
		t.Fatalf("build-directive must fill data.directive: %#v", state.data)
	}
	if directive.Runner != "opencode" || directive.Model != "openai/gpt-5.5" || directive.ModelBinding != "coder" {
		t.Fatalf("unexpected directive: %#v", directive)
	}
	if state.in.Launch.Runner != "" || state.in.Launch.Model != "" || state.in.Launch.ModelBinding != "" {
		t.Fatalf("build-directive must not write implicit state launch: %#v", state.in.Launch)
	}
	if state.allocation.Resource != "legacy" || state.allocation.Runner != "legacy" {
		t.Fatalf("build-directive must not read or write implicit state allocation: %#v", state.allocation)
	}
}

func TestBuildDirectiveKeepsExplicitModelBinding(t *testing.T) {
	t.Parallel()

	operation := buildDirectiveOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Launch: model.LaunchSpec{ModelBinding: "legacy"},
		},
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Launch: model.LaunchSpec{ModelBinding: "explicit"}},
			"allocation": model.Allocation{Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.buildDirective(context.Background(), state, operation, OperationKindBuildDirective)
	if err != nil {
		t.Fatalf("build directive: %v", err)
	}
	directive, ok := state.data["directive"].(model.LaunchSpec)
	if !ok || directive.ModelBinding != "explicit" {
		t.Fatalf("build-directive must keep explicit model binding: %#v", state.data)
	}
}

func TestBuildDirectiveWritesDirectiveForActionWithoutSynthesis(t *testing.T) {
	t.Parallel()

	operation := buildDirectiveOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Launch: model.LaunchSpec{StructuredInput: &StructuredInput{Task: "Legacy."}},
		},
		action: model.Action{
			RequiresSynthesis: false,
			Operations:        []model.OperationSpec{operation},
		},
		data:    map[string]any{"invocation": model.Invocation{Launch: model.LaunchSpec{StructuredInput: &StructuredInput{Task: "No synthesis."}}}, "allocation": model.Allocation{Resource: "not-required"}},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.buildDirective(context.Background(), state, operation, OperationKindBuildDirective)
	if err != nil {
		t.Fatalf("build directive: %v", err)
	}
	directive, ok := state.data["directive"].(model.LaunchSpec)
	if !ok || directive.StructuredInput == nil || directive.StructuredInput.Task != "No synthesis." {
		t.Fatalf("build-directive must write skipped directive to data.directive: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindBuildDirective)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped directive must keep contract output diagnostics: %#v", result)
	}
}

func TestLaunchSynthesisFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := launchSynthesisOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Task: "task-42",
		},
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"directive":  model.LaunchSpec{Directory: "/tmp/work", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder", CommitPush: true},
			"profile":    model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
			"allocation": model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
		},
		profile:    model.Profile{Name: "legacy", Mode: "manual", ModelBinding: "legacy"},
		allocation: model.Allocation{Resource: "legacy", Runner: "legacy"},
		workplace:  model.Workplace{Name: "/tmp/legacy", Ready: true},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	launcher := &stubLauncher{result: model.LaunchResult{Status: "completed", Summary: "launch complete", StructuredOutput: &model.StructuredOutput{Summary: "Done."}}}
	service := &Service{
		logger:   log.Default(),
		launcher: launcher,
	}

	err := builtinOperationExecutor{service: service}.launchSynthesis(context.Background(), state, operation, OperationKindLaunchSynthesis)
	if err != nil {
		t.Fatalf("launch synthesis: %v", err)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok {
		t.Fatalf("launch-synthesis must fill data.result: %#v", state.data)
	}
	if dataResult.Status != "completed" || dataResult.StructuredOutput == nil || dataResult.StructuredOutput.Summary != "Done." {
		t.Fatalf("unexpected data result: %#v", dataResult)
	}
	if state.result.Status != "" || state.result.StructuredOutput != nil {
		t.Fatalf("launch-synthesis must not write implicit state result: %#v", state.result)
	}
	if launcher.invocation.Launch.Runner != "opencode" || launcher.invocation.Launch.Model != "openai/gpt-5.5" || launcher.invocation.Launch.CommitPush {
		t.Fatalf("launcher must receive directive from operation input with commit push disabled: %#v", launcher.invocation.Launch)
	}
	if launcher.profile.Name != "coder" || launcher.allocation.Resource != "binding:coder" || launcher.workplace.Name != "/tmp/work" {
		t.Fatalf("launcher must receive operation input data: profile=%#v allocation=%#v workplace=%#v", launcher.profile, launcher.allocation, launcher.workplace)
	}
}

func TestLaunchSynthesisWritesSkippedResultForActionWithoutSynthesis(t *testing.T) {
	t.Parallel()

	operation := launchSynthesisOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Task: "task-42",
		},
		action: model.Action{
			RequiresSynthesis: false,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"directive":  model.LaunchSpec{StructuredInput: &StructuredInput{Task: "No synthesis."}},
			"profile":    model.Profile{Name: "default", Mode: "manual"},
			"allocation": model.Allocation{Resource: "not-required"},
			"workplace":  model.Workplace{Name: "/repo", Ready: true},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:   log.Default(),
		launcher: &stubLauncher{err: errors.New("launcher must not be called")},
	}

	err := builtinOperationExecutor{service: service}.launchSynthesis(context.Background(), state, operation, OperationKindLaunchSynthesis)
	if err != nil {
		t.Fatalf("launch synthesis: %v", err)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "skipped" || !strings.Contains(dataResult.Summary, "synthesis=not-required") {
		t.Fatalf("launch-synthesis must write skipped result to data.result: %#v", state.data)
	}
	if state.result.Status != "" {
		t.Fatalf("skipped launch must keep old empty state result for finalization compatibility: %#v", state.result)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindLaunchSynthesis)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped launch must keep contract output diagnostics: %#v", result)
	}
}

func TestParseResultFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := parseResultOperationSpec()
	output := &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}
	state := &operationExecution{
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"result": model.LaunchResult{Status: "completed", Summary: "launch complete", StructuredOutput: output},
		},
		result:  model.LaunchResult{Status: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}

	err := builtinOperationExecutor{}.parseResult(state, operation, OperationKindParseResult)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	dataOutput, ok := state.data["structured_output"].(*model.StructuredOutput)
	if !ok {
		t.Fatalf("parse-result must fill data.structured_output: %#v", state.data)
	}
	if dataOutput.Summary != "Done." || dataOutput.CommitMessage != "Apply change" {
		t.Fatalf("unexpected structured output: %#v", dataOutput)
	}
	if state.result.Status != "legacy" || state.result.StructuredOutput != nil {
		t.Fatalf("parse-result must not write implicit state result: %#v", state.result)
	}
}

func TestParseResultWritesEmptyOutputForActionWithoutSynthesis(t *testing.T) {
	t.Parallel()

	operation := parseResultOperationSpec()
	state := &operationExecution{
		action: model.Action{
			RequiresSynthesis: false,
			Operations:        []model.OperationSpec{operation},
		},
		data:    map[string]any{"result": model.LaunchResult{Status: "skipped", Summary: "synthesis=not-required"}},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}

	err := builtinOperationExecutor{}.parseResult(state, operation, OperationKindParseResult)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if _, ok := state.data["structured_output"].(*model.StructuredOutput); !ok {
		t.Fatalf("parse-result must write empty structured output to data.structured_output: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindParseResult)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped parse result must keep contract output diagnostics: %#v", result)
	}
}

func TestCommitPushFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := commitPushOperationSpec()
	structuredOutput := &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}
	state := &operationExecution{
		in: model.Invocation{
			Task: "task-42",
		},
		action: model.Action{
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"profile":           model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
			"allocation":        model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
			"workplace":         model.Workplace{Name: "/tmp/work", Ready: true},
			"result":            model.LaunchResult{Status: "completed", Summary: "launch complete"},
			"structured_output": structuredOutput,
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy", StructuredOutput: &model.StructuredOutput{Summary: "Legacy."}},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	launcher := &stubLauncher{commitSummary: "git=committed+pushed branch=task-42"}
	service := &Service{
		logger:   log.Default(),
		launcher: launcher,
	}

	err := builtinOperationExecutor{service: service}.commitPush(context.Background(), state, operation, OperationKindCommitPush)
	if err != nil {
		t.Fatalf("commit push: %v", err)
	}
	if state.data["commit_summary"] != "git=committed+pushed branch=task-42" {
		t.Fatalf("commit-push must fill data.commit_summary: %#v", state.data)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok {
		t.Fatalf("commit-push must update data.result: %#v", state.data)
	}
	if !strings.Contains(dataResult.Summary, "launch complete") || !strings.Contains(dataResult.Summary, "git=committed+pushed branch=task-42") {
		t.Fatalf("unexpected data result summary: %#v", dataResult)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("commit-push must not write implicit state result: %#v", state.result)
	}
	if !launcher.commitCalled || launcher.commitOutput == nil || launcher.commitOutput.Summary != "Done." {
		t.Fatalf("commit-push must pass structured output from operation input: %#v", launcher)
	}
	if launcher.commitAllocation.Resource != "binding:coder" || launcher.commitWorkplace.Name != "/tmp/work" {
		t.Fatalf("commit-push must pass operation input data: allocation=%#v workplace=%#v", launcher.commitAllocation, launcher.commitWorkplace)
	}
}

func TestCommitPushWritesResultForActionWithoutSynthesis(t *testing.T) {
	t.Parallel()

	operation := commitPushOperationSpec()
	state := &operationExecution{
		in: model.Invocation{Task: "task-42"},
		action: model.Action{
			RequiresSynthesis: false,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"result": model.LaunchResult{Status: "skipped", Summary: "synthesis=not-required"},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:   log.Default(),
		launcher: &stubLauncher{commitErr: errors.New("commit must not be called")},
	}

	err := builtinOperationExecutor{service: service}.commitPush(context.Background(), state, operation, OperationKindCommitPush)
	if err != nil {
		t.Fatalf("commit push: %v", err)
	}
	if state.data["commit_summary"] != "" {
		t.Fatalf("skipped commit-push must write empty commit summary: %#v", state.data)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "skipped" {
		t.Fatalf("skipped commit-push must keep data.result: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindCommitPush)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped commit-push must keep contract output diagnostics: %#v", result)
	}
}

func TestLoadPullRequestFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := loadPullRequestOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Number: 112},
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "owner/name",
					Number:     17,
				}},
			},
		},
		action:  model.Action{Operations: []model.OperationSpec{operation}},
		data:    map[string]any{},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "get" || req.Repository != "owner/name" || req.Number != 17 {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{MergeRequest: &integration.MergeRequest{
				Repository: "owner/name",
				Number:     17,
				State:      "OPEN",
				BaseRef:    "main",
				HeadRef:    "feature/review",
				Title:      "Исправить обработку",
				URL:        "https://github.com/owner/name/pull/17",
			}}, nil
		},
	}
	service := &Service{logger: log.Default(), integrations: integrations}

	err := builtinOperationExecutor{service: service}.loadPullRequest(context.Background(), state, operation, OperationKindLoadPullRequest)
	if err != nil {
		t.Fatalf("load pull request: %v", err)
	}
	dataPullRequest, ok := state.data["pull_request"].(integration.MergeRequest)
	if !ok || dataPullRequest.Number != 17 || dataPullRequest.HeadRef != "feature/review" {
		t.Fatalf("load-pull-request must fill data.pull_request: %#v", state.data)
	}
	dataInvocation, ok := state.data["invocation"].(model.Invocation)
	if !ok || dataInvocation.Workplace.HeadRef != "feature/review" || dataInvocation.Workplace.BaseRef != "main" {
		t.Fatalf("load-pull-request must fill synchronized data.invocation: %#v", state.data)
	}
	if state.pullRequest != nil {
		t.Fatalf("load-pull-request must not write implicit state pull request: %#v", state.pullRequest)
	}
	if state.in.Workplace.HeadRef != "" || state.assignment != nil {
		t.Fatalf("load-pull-request must not write implicit state invocation or assignment: state=%#v assignment=%#v", state.in, state.assignment)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindLoadPullRequest)
	if result == nil || result.Status != OperationStatusCompleted || result.Input == "" || result.Output == "" {
		t.Fatalf("load-pull-request must keep contract diagnostics: %#v", result)
	}
}

func TestLoadPullRequestFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := loadPullRequestOperationSpec()
	state := &operationExecution{
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{
				Assignment: &ExecutionAssignment{
					RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}},
				},
			},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("load failed")
		}},
	}

	err := builtinOperationExecutor{service: service}.loadPullRequest(context.Background(), state, operation, OperationKindLoadPullRequest)
	if err == nil {
		t.Fatal("load-pull-request must return integration error")
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "load failed") {
		t.Fatalf("load-pull-request must write failed result to data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("load-pull-request must not write implicit state result: %#v", state.result)
	}
}

func TestLoadReviewRemarksFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := loadReviewRemarksOperationSpec()
	input := model.Invocation{
		Assignment: &ExecutionAssignment{
			CanonicalTask:   &ObjectRef{Type: "task", Number: 112},
			StructuredInput: &StructuredInput{Task: "Исправить замечания ревизии."},
		},
	}
	pullRequest := integration.MergeRequest{
		Repository: "owner/name",
		Number:     17,
		State:      "OPEN",
		BaseRef:    "main",
		HeadRef:    "feature/review",
	}
	state := &operationExecution{
		in:     model.Invocation{Action: "legacy"},
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation":   input,
			"pull_request": pullRequest,
		},
		pullRequest: &integration.MergeRequest{
			Repository: "legacy/name",
			Number:     999,
			HeadRef:    "legacy/head",
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "comments" || req.Repository != "owner/name" || req.Number != 17 {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{ReviewRemarks: []integration.ReviewRemark{{
				Repository:         req.Repository,
				MergeRequestNumber: req.Number,
				ExternalID:         "comment-1",
				ReplyToID:          "thread-1",
				State:              "unresolved",
				Body:               "Добавьте проверку отказа.",
			}}}, nil
		},
	}
	service := &Service{logger: log.Default(), integrations: integrations}

	err := builtinOperationExecutor{service: service}.loadReviewRemarks(context.Background(), state, operation, OperationKindLoadReviewRemarks, true)
	if err != nil {
		t.Fatalf("load review remarks: %v", err)
	}
	dataRemarks, ok := state.data["review_remarks"].([]integration.ReviewRemark)
	if !ok || len(dataRemarks) != 1 || dataRemarks[0].ExternalID != "comment-1" {
		t.Fatalf("load-review-remarks must fill data.review_remarks: %#v", state.data)
	}
	dataInvocation, ok := state.data["invocation"].(model.Invocation)
	if !ok || dataInvocation.Launch.StructuredInput == nil || len(dataInvocation.Launch.StructuredInput.ReviewRemarks) != 1 || dataInvocation.Launch.StructuredInput.ReviewRemarks[0].ID != "comment-1" {
		t.Fatalf("load-review-remarks must fill enriched data.invocation: %#v", state.data)
	}
	if len(state.reviewRemarks) != 0 {
		t.Fatalf("load-review-remarks must not write implicit state review remarks: %#v", state.reviewRemarks)
	}
	if state.pullRequest == nil || state.pullRequest.Number != 999 || state.pullRequest.Repository != "legacy/name" {
		t.Fatalf("load-review-remarks must not read or write implicit state pull request: %#v", state.pullRequest)
	}
	if state.in.Action != "legacy" || state.in.Launch.StructuredInput != nil {
		t.Fatalf("load-review-remarks must not write implicit state invocation: %#v", state.in)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindLoadReviewRemarks)
	if result == nil || result.Status != OperationStatusCompleted || result.Input == "" || result.Output == "" {
		t.Fatalf("load-review-remarks must keep contract diagnostics: %#v", result)
	}
}

func TestLoadReviewRemarksFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := loadReviewRemarksOperationSpec()
	state := &operationExecution{
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation":   model.Invocation{},
			"pull_request": integration.MergeRequest{Repository: "owner/name", Number: 17},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("remarks failed")
		}},
	}

	err := builtinOperationExecutor{service: service}.loadReviewRemarks(context.Background(), state, operation, OperationKindLoadReviewRemarks, true)
	if err == nil {
		t.Fatal("load-review-remarks must return integration error")
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "remarks failed") {
		t.Fatalf("load-review-remarks must write failed result to data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("load-review-remarks must not write implicit state result: %#v", state.result)
	}
}

func TestLoadReviewRemarksOptionalFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := loadReviewRemarksOperationSpec()
	state := &operationExecution{
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation":   model.Invocation{},
			"pull_request": integration.MergeRequest{Repository: "owner/name", Number: 17},
			"result":       model.LaunchResult{Status: "running", Summary: "running"},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("optional remarks failed")
		}},
	}

	err := builtinOperationExecutor{service: service}.loadReviewRemarks(context.Background(), state, operation, OperationKindLoadReviewRemarks, false)
	if err != nil {
		t.Fatalf("optional load-review-remarks must skip integration error: %v", err)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "running" {
		t.Fatalf("optional load-review-remarks must keep data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("optional load-review-remarks must not write implicit state result: %#v", state.result)
	}
}

func TestFinalizeFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := finalizeOperationSpec()
	state := &operationExecution{
		action: model.Action{
			Name:              ActionClassIntegrationChange,
			Class:             ActionClassIntegrationChange,
			RequiresSynthesis: false,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"result": model.LaunchResult{Status: "legacy", Summary: "legacy"},
		},
		result:  model.LaunchResult{Status: "legacy-state", Summary: "legacy-state"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.finalize(context.Background(), state, operation, OperationKindFinalize)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "completed" || !strings.Contains(dataResult.Summary, "synthesis=not-required") {
		t.Fatalf("finalize must fill data.result: %#v", state.data)
	}
	if state.result.Status != "legacy-state" || state.result.Summary != "legacy-state" {
		t.Fatalf("finalize must not write implicit state result: %#v", state.result)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindFinalize)
	if result == nil || result.Status != OperationStatusCompleted || result.Input == "" || result.Output == "" {
		t.Fatalf("finalize must keep contract diagnostics: %#v", result)
	}
}

func TestFinalizeReadsResultOnlyFromActionData(t *testing.T) {
	t.Parallel()

	operation := finalizeOperationSpec()
	state := &operationExecution{
		action: model.Action{
			Name:              ActionClassEngineeringSynthesis,
			Class:             ActionClassEngineeringSynthesis,
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{operation},
		},
		data: map[string]any{
			"result": model.LaunchResult{Status: "completed", Summary: "data-result"},
		},
		result:  model.LaunchResult{Status: "completed", Summary: "legacy-state\ndata-result"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.finalize(context.Background(), state, operation, OperationKindFinalize)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "completed" || dataResult.Summary != "data-result" {
		t.Fatalf("finalize must use data.result as input: %#v", state.data)
	}
	if state.result.Summary != "legacy-state\ndata-result" {
		t.Fatalf("finalize must not read through implicit state result: %#v", state.result)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindFinalize)
	if result == nil || result.Status != OperationStatusCompleted || !strings.Contains(result.Output, "data-result") || strings.Contains(result.Output, "legacy-state") {
		t.Fatalf("finalize must keep contract output diagnostics from data.result: %#v", result)
	}
}

func TestPublishMergeRequestFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := publishMergeRequestOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Task: "legacy",
			Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999, Title: "Старое действие"},
			},
			Workplace: model.WorkplaceSpec{Name: "legacy"},
		},
		assignment: &ExecutionAssignment{
			CanonicalTask: &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999, Title: "Старое действие"},
		},
		action: model.Action{
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{
				Task: "task-132",
				Assignment: &ExecutionAssignment{
					CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 132, Title: "Поддержать действие"},
				},
				Workplace: model.WorkplaceSpec{Name: "132"},
			},
			"workplace":         model.Workplace{Name: "/tmp/work", RepositoryRoot: "/tmp/work", Ready: true},
			"result":            model.LaunchResult{Status: "completed", Summary: "launch complete"},
			"structured_output": &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"},
		},
		pullRequest: &integration.MergeRequest{Repository: "legacy/name", Number: 998, BaseRef: "legacy-base", HeadRef: "legacy-head"},
		result:      model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker:     newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "create" || req.Repository != "owner/name" || req.Base != "main" || req.Head != "132" {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{
				PullRequestStatus: &integration.PullRequestStatus{Repository: req.Repository, Number: 17, State: "OPEN", URL: "https://github.com/owner/name/pull/17", Base: req.Base, Head: req.Head, Title: req.Title},
				OperationResult:   &integration.OperationResult{Status: "ok", ExternalID: "17", URL: "https://github.com/owner/name/pull/17"},
			}, nil
		},
	}
	service := &Service{
		logger:       log.Default(),
		integrations: integrations,
		runGitOutput: func(_ context.Context, dir string, args ...string) (string, error) {
			if dir != "/tmp/work" || strings.Join(args, " ") != "symbolic-ref refs/remotes/origin/HEAD" {
				return "", errors.New("unexpected git command")
			}
			return "refs/remotes/origin/main\n", nil
		},
	}

	err := builtinOperationExecutor{service: service}.publishMergeRequest(context.Background(), state, operation, OperationKindPublishMergeRequest)
	if err != nil {
		t.Fatalf("publish merge request: %v", err)
	}
	mergeRequest, ok := state.data["merge_request"].(integration.MergeRequest)
	if !ok {
		t.Fatalf("publish-merge-request must fill data.merge_request: %#v", state.data)
	}
	if mergeRequest.Number != 17 || mergeRequest.URL != "https://github.com/owner/name/pull/17" || mergeRequest.HeadRef != "132" {
		t.Fatalf("unexpected merge request: %#v", mergeRequest)
	}
	if summary := state.data["publish_summary"]; summary == "" || !strings.Contains(summary.(string), "pull-request=17") {
		t.Fatalf("publish-merge-request must fill data.publish_summary: %#v", state.data)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || !strings.Contains(dataResult.Summary, "pull-request=17") {
		t.Fatalf("publish-merge-request must update data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("publish-merge-request must not write implicit state result: %#v", state.result)
	}
}

func TestPublishMergeRequestFailureFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := publishMergeRequestOperationSpec()
	state := &operationExecution{
		action: model.Action{
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{
				Task: "task-132",
				Assignment: &ExecutionAssignment{
					CanonicalTask: &ObjectRef{Type: "task", Number: 132, Title: "Поддержать действие"},
				},
				Workplace: model.WorkplaceSpec{Name: "132"},
			},
			"workplace":         model.Workplace{Name: "/tmp/work", RepositoryRoot: "/tmp/work", Ready: true},
			"result":            model.LaunchResult{Status: "completed", Summary: "launch complete"},
			"structured_output": &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.publishMergeRequest(context.Background(), state, operation, OperationKindPublishMergeRequest)
	if err == nil {
		t.Fatalf("publish merge request must fail without repository")
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "repository is required") {
		t.Fatalf("publish-merge-request failure must fill data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("publish-merge-request failure must not write implicit state result: %#v", state.result)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPublishMergeRequest)
	if result == nil || result.Status != OperationStatusFailed || result.Failure == nil || result.Failure.Code != "pull_request_repository_required" {
		t.Fatalf("publish-merge-request failure must keep diagnostics: %#v", result)
	}
}

func TestPublishMergeRequestWritesOutputsWhenPullRequestAlreadyExists(t *testing.T) {
	t.Parallel()

	operation := publishMergeRequestOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 132},
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "owner/name",
					Number:     17,
				}},
			},
		},
		assignment: &ExecutionAssignment{
			CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 132},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "owner/name",
				Number:     17,
			}},
		},
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"workplace": model.Workplace{Name: "/tmp/work", Ready: true},
			"result":    model.LaunchResult{Status: "completed", Summary: "launch complete"},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("integration must not be called")
		}},
	}

	err := builtinOperationExecutor{service: service}.publishMergeRequest(context.Background(), state, operation, OperationKindPublishMergeRequest)
	if err != nil {
		t.Fatalf("publish merge request: %v", err)
	}
	mergeRequest, ok := state.data["merge_request"].(integration.MergeRequest)
	if !ok || mergeRequest.Number != 17 {
		t.Fatalf("skipped publish must fill data.merge_request: %#v", state.data)
	}
	if state.data["publish_summary"] != "pull-request=17" {
		t.Fatalf("skipped publish must fill data.publish_summary: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPublishMergeRequest)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped publish must keep contract output diagnostics: %#v", result)
	}
}

func TestPublishReviewRemarksFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := publishReviewRemarksOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999},
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "legacy/name",
					Number:     999,
				}},
			},
		},
		assignment: &ExecutionAssignment{
			CanonicalTask: &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "legacy/name",
				Number:     999,
			}},
		},
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112},
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "owner/name",
					Number:     17,
				}},
			}},
			"result": model.LaunchResult{Status: "completed", Summary: "review complete"},
			"structured_output": &model.StructuredOutput{Remarks: []model.StructuredRemark{{
				ID:       "remark-1",
				Title:    "Не хватает проверки",
				Body:     "Добавьте проверку отказа.",
				Path:     "internal/execution/service.go",
				Line:     42,
				Side:     "RIGHT",
				Severity: "major",
			}}},
		},
		pullRequest: &integration.MergeRequest{Repository: "legacy/name", Number: 998, HeadRef: "legacy-head"},
		result:      model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker:     newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "create" || req.Repository != "owner/name" || req.Number != 17 || req.Path != "internal/execution/service.go" || req.Line != 42 {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{OperationResult: &integration.OperationResult{Status: "ok", URL: "https://github.com/owner/name/pull/17#discussion_r1"}}, nil
		},
	}
	service := &Service{logger: log.Default(), integrations: integrations}

	err := builtinOperationExecutor{service: service}.publishReviewRemarks(context.Background(), state, operation, OperationKindPublishReviewRemarks)
	if err != nil {
		t.Fatalf("publish review remarks: %v", err)
	}
	if state.data["review_remarks_summary"] != "review-remarks-published=1" {
		t.Fatalf("publish-review-remarks must fill data.review_remarks_summary: %#v", state.data)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || !strings.Contains(dataResult.Summary, "review-remarks-published=1") {
		t.Fatalf("publish-review-remarks must update data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("publish-review-remarks must not write implicit state result: %#v", state.result)
	}
	if state.pullRequest == nil || state.pullRequest.Number != 998 || state.pullRequest.Repository != "legacy/name" {
		t.Fatalf("publish-review-remarks must not read or write implicit state pull request: %#v", state.pullRequest)
	}
}

func TestPublishReviewRemarksFailureFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := publishReviewRemarksOperationSpec()
	state := &operationExecution{
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{
				Assignment: &ExecutionAssignment{
					RelatedObjects: []ObjectRef{{
						Type:       "merge-request",
						Repository: "owner/name",
						Number:     17,
					}},
				},
			},
			"result": model.LaunchResult{Status: "completed", Summary: "review complete"},
			"structured_output": &model.StructuredOutput{Remarks: []model.StructuredRemark{{
				ID:       "remark-1",
				Title:    "Не хватает проверки",
				Body:     "Добавьте проверку отказа.",
				Path:     "internal/execution/service.go",
				Line:     42,
				Side:     "RIGHT",
				Severity: "major",
			}}},
		},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("publish failed")
		}},
	}

	err := builtinOperationExecutor{service: service}.publishReviewRemarks(context.Background(), state, operation, OperationKindPublishReviewRemarks)
	if err == nil {
		t.Fatalf("publish review remarks must fail")
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "publish failed") {
		t.Fatalf("publish-review-remarks failure must fill data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("publish-review-remarks failure must not write implicit state result: %#v", state.result)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPublishReviewRemarks)
	if result == nil || result.Status != OperationStatusFailed || result.Failure == nil || result.Failure.Code != "review_remarks_publish_failed" {
		t.Fatalf("publish-review-remarks failure must keep diagnostics: %#v", result)
	}
}

func TestPublishReviewRemarksWritesOutputsWhenNoRemarks(t *testing.T) {
	t.Parallel()

	operation := publishReviewRemarksOperationSpec()
	state := &operationExecution{
		in:         model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "legacy/name", Number: 999}}}},
		assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "legacy/name", Number: 999}}},
		action:     model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation":        model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":            model.LaunchResult{Status: "completed", Summary: "review complete"},
			"structured_output": &model.StructuredOutput{Summary: "No remarks."},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("integration must not be called")
		}},
	}

	err := builtinOperationExecutor{service: service}.publishReviewRemarks(context.Background(), state, operation, OperationKindPublishReviewRemarks)
	if err != nil {
		t.Fatalf("publish review remarks: %v", err)
	}
	if state.data["review_remarks_summary"] != "" {
		t.Fatalf("skipped publish-review-remarks must write empty summary: %#v", state.data)
	}
	if _, ok := state.data["result"].(model.LaunchResult); !ok {
		t.Fatalf("skipped publish-review-remarks must keep data.result: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPublishReviewRemarks)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped publish-review-remarks must keep contract output diagnostics: %#v", result)
	}
}

func TestPublishReviewResponsesFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	remarks := []integration.ReviewRemark{{
		ExternalID: "remark-1",
		ReplyToID:  "thread-1",
		State:      "unresolved",
		Body:       "Исправьте обработку ошибки.",
	}}
	state := &operationExecution{
		in: model.Invocation{
			Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999},
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "legacy/name",
					Number:     999,
				}},
			},
		},
		assignment: &ExecutionAssignment{
			CanonicalTask: &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999},
			RelatedObjects: []ObjectRef{{
				Type:       "merge-request",
				Repository: "legacy/name",
				Number:     999,
			}},
		},
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{
				CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 112},
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "owner/name",
					Number:     17,
				}},
			}},
			"result": model.LaunchResult{Status: "completed", Summary: "apply complete"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-1",
				Status:   "resolved",
				Summary:  "Проверка добавлена.",
				Body:     "Добавил покрытие отказа.",
			}}},
			"review_remarks": remarks,
		},
		pullRequest:   &integration.MergeRequest{Repository: "legacy/name", Number: 998, HeadRef: "legacy-head"},
		result:        model.LaunchResult{Status: "legacy", Summary: "legacy"},
		reviewRemarks: []integration.ReviewRemark{{ExternalID: "legacy", ReplyToID: "legacy-thread"}},
		tracker:       newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	callIndex := 0
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			callIndex++
			switch callIndex {
			case 1:
				if req.Operation != "reply" || req.Repository != "owner/name" || req.Number != 17 || req.ThreadID != "thread-1" || !strings.Contains(req.Body, "Добавил покрытие отказа.") {
					t.Fatalf("unexpected reply request: %#v", req)
				}
			case 2:
				if req.Operation != "resolve" || req.ThreadID != "thread-1" || req.ExternalID != "thread-1" {
					t.Fatalf("unexpected resolve request: %#v", req)
				}
			default:
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{Status: "ok"}, nil
		},
	}
	service := &Service{logger: log.Default(), integrations: integrations}

	err := builtinOperationExecutor{service: service}.publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if state.data["review_responses_summary"] != "review-responses-published=1 review-threads-resolved=1" {
		t.Fatalf("publish-review-responses must fill data.review_responses_summary: %#v", state.data)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || !strings.Contains(dataResult.Summary, "review-responses-published=1 review-threads-resolved=1") {
		t.Fatalf("publish-review-responses must update data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("publish-review-responses must not write implicit state result: %#v", state.result)
	}
	if len(state.reviewRemarks) != 1 || state.reviewRemarks[0].ExternalID != "legacy" {
		t.Fatalf("publish-review-responses must not write implicit state review remarks: %#v", state.reviewRemarks)
	}
	if state.pullRequest == nil || state.pullRequest.Number != 998 || state.pullRequest.Repository != "legacy/name" {
		t.Fatalf("publish-review-responses must not read or write implicit state pull request: %#v", state.pullRequest)
	}
	if len(integrations.calls) != 2 {
		t.Fatalf("expected reply and resolve integration calls, got %#v", integrations.calls)
	}
}

func TestPublishReviewResponsesFailureFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{
				Assignment: &ExecutionAssignment{
					RelatedObjects: []ObjectRef{{
						Type:       "merge-request",
						Repository: "owner/name",
						Number:     17,
					}},
				},
			},
			"result": model.LaunchResult{Status: "completed", Summary: "apply complete"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-1",
				Status:   "resolved",
				Summary:  "Проверка добавлена.",
				Body:     "Добавил покрытие отказа.",
			}}},
			"review_remarks": []integration.ReviewRemark{{
				ExternalID: "remark-1",
				ReplyToID:  "thread-1",
				State:      "unresolved",
				Body:       "Исправьте обработку ошибки.",
			}},
		},
		result:        model.LaunchResult{Status: "legacy", Summary: "legacy"},
		reviewRemarks: []integration.ReviewRemark{{ExternalID: "legacy", ReplyToID: "legacy-thread"}},
		tracker:       newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("publish failed")
		}},
	}

	err := builtinOperationExecutor{service: service}.publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err == nil {
		t.Fatalf("publish review responses must fail")
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "publish failed") {
		t.Fatalf("publish-review-responses failure must fill data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("publish-review-responses failure must not write implicit state result: %#v", state.result)
	}
	if len(state.reviewRemarks) != 1 || state.reviewRemarks[0].ExternalID != "legacy" {
		t.Fatalf("publish-review-responses failure must not write implicit state review remarks: %#v", state.reviewRemarks)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPublishReviewResponses)
	if result == nil || result.Status != OperationStatusFailed || result.Failure == nil || result.Failure.Code != "review_responses_publish_failed" {
		t.Fatalf("publish-review-responses failure must keep diagnostics: %#v", result)
	}
}

func TestPublishReviewResponsesWritesOutputsWhenNoResponses(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		in:         model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "legacy/name", Number: 999}}}},
		assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "legacy/name", Number: 999}}},
		action:     model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation":        model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":            model.LaunchResult{Status: "completed", Summary: "apply complete"},
			"structured_output": &model.StructuredOutput{Summary: "No responses."},
			"review_remarks":    []integration.ReviewRemark{{ExternalID: "remark-1", ReplyToID: "thread-1"}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger: log.Default(),
		integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
			return integration.Response{}, errors.New("integration must not be called")
		}},
	}

	err := builtinOperationExecutor{service: service}.publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if state.data["review_responses_summary"] != "" {
		t.Fatalf("skipped publish-review-responses must write empty summary: %#v", state.data)
	}
	if _, ok := state.data["result"].(model.LaunchResult); !ok {
		t.Fatalf("skipped publish-review-responses must keep data.result: %#v", state.data)
	}
	result := findOperationResult(state.tracker.snapshot(), OperationKindPublishReviewResponses)
	if result == nil || result.Status != OperationStatusSkipped || result.Output == "" {
		t.Fatalf("skipped publish-review-responses must keep contract output diagnostics: %#v", result)
	}
}

func TestUnsupportedRequiredOperationWritesResultData(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{
		Name:     "unknown-operation",
		Kind:     model.OperationKind("unknown-operation"),
		Origin:   OperationOriginBuiltin,
		Required: true,
		Out:      model.OperationMap{"result": {Ref: "data.result"}},
	}
	state := &operationExecution{
		in:      model.Invocation{Task: "task-42"},
		action:  model.Action{Operations: []model.OperationSpec{operation}},
		result:  model.LaunchResult{Status: "legacy", Summary: "legacy"},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.unsupported(context.Background(), state, operation, operation.Name)
	if err == nil {
		t.Fatal("unsupported required operation must return error")
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "unknown-operation") {
		t.Fatalf("unsupported operation must write failed result to data.result: %#v", state.data)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("unsupported operation must not write implicit state result: %#v", state.result)
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
				{
					Name: OperationKindPrepareData,
					In: map[string]methodology.ActionMapping{
						"expected_result":  mappingRef("in.expected_result"),
						"constraints":      mappingRef("in.constraints"),
						"canonical_task":   mappingRef("in.canonical_task"),
						"related_objects":  mappingRef("in.related_objects"),
						"reasons":          mappingRef("in.reasons"),
						"structured_input": mappingRef("in.structured_input"),
					},
					Out: map[string]methodology.ActionMapping{
						"structured_input": mappingRef("data.structured_input"),
						"workplace":        mappingRef("data.workplace"),
						"invocation":       mappingRef("data.invocation"),
					},
				},
				{Name: OperationKindLoadPullRequest},
				{Name: OperationKindLoadReviewRemarks},
				{
					Name: OperationKindResolveProfile,
					In: map[string]methodology.ActionMapping{
						"profile_name": mappingRef("action.profile"),
						"invocation":   mappingRef("data.invocation"),
					},
					Out: map[string]methodology.ActionMapping{
						"profile": mappingRef("data.profile"),
						"result":  mappingRef("data.result"),
					},
				},
				{
					Name: OperationKindAllocateResources,
					In: map[string]methodology.ActionMapping{
						"requires_synthesis": mappingRef("action.requires_synthesis"),
						"invocation":         mappingRef("data.invocation"),
						"profile":            mappingRef("data.profile"),
					},
					Out: map[string]methodology.ActionMapping{
						"allocation": mappingRef("data.allocation"),
					},
				},
				{
					Name: OperationKindPrepareWorkplace,
					In: map[string]methodology.ActionMapping{
						"requires_workplace": mappingRef("action.requires_workplace"),
						"invocation":         mappingRef("data.invocation"),
						"profile":            mappingRef("data.profile"),
						"allocation":         mappingRef("data.allocation"),
					},
					Out: map[string]methodology.ActionMapping{
						"workplace":  mappingRef("data.workplace"),
						"invocation": mappingRef("data.invocation"),
					},
				},
				{
					Name: OperationKindBuildDirective,
					In: map[string]methodology.ActionMapping{
						"requires_synthesis": mappingRef("action.requires_synthesis"),
						"invocation":         mappingRef("data.invocation"),
						"allocation":         mappingRef("data.allocation"),
					},
					Out: map[string]methodology.ActionMapping{
						"directive": mappingRef("data.directive"),
					},
				},
				{
					Name: OperationKindLaunchSynthesis,
					In: map[string]methodology.ActionMapping{
						"requires_synthesis": mappingRef("action.requires_synthesis"),
						"invocation":         mappingRef("data.invocation"),
						"directive":          mappingRef("data.directive"),
						"profile":            mappingRef("data.profile"),
						"allocation":         mappingRef("data.allocation"),
						"workplace":          mappingRef("data.workplace"),
					},
					Out: map[string]methodology.ActionMapping{
						"result": mappingRef("data.result"),
					},
				},
				{
					Name: OperationKindParseResult,
					In: map[string]methodology.ActionMapping{
						"requires_synthesis": mappingRef("action.requires_synthesis"),
						"result":             mappingRef("data.result"),
					},
					Out: map[string]methodology.ActionMapping{
						"structured_output": mappingRef("data.structured_output"),
					},
				},
				{
					Name: OperationKindCommitPush,
					In: map[string]methodology.ActionMapping{
						"requires_synthesis": mappingRef("action.requires_synthesis"),
						"invocation":         mappingRef("data.invocation"),
						"profile":            mappingRef("data.profile"),
						"allocation":         mappingRef("data.allocation"),
						"workplace":          mappingRef("data.workplace"),
						"result":             mappingRef("data.result"),
						"structured_output":  mappingRef("data.structured_output"),
					},
					Out: map[string]methodology.ActionMapping{
						"commit_summary": mappingRef("data.commit_summary"),
						"result":         mappingRef("data.result"),
					},
				},
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
	prepareOperation := findOperationSpec(action, OperationKindPrepareData)
	if prepareOperation == nil {
		t.Fatalf("prepare-data operation must be present: %#v", action.Operations)
	}
	if prepareOperation.In["structured_input"].Ref != "in.structured_input" || prepareOperation.Out["structured_input"].Ref != "data.structured_input" || prepareOperation.Out["workplace"].Ref != "data.workplace" || prepareOperation.Out["invocation"].Ref != "data.invocation" {
		t.Fatalf("prepare-data must keep action data mapping: %#v", prepareOperation)
	}
	profileOperation := findOperationSpec(action, OperationKindResolveProfile)
	if profileOperation == nil {
		t.Fatalf("resolve-profile operation must be present: %#v", action.Operations)
	}
	if profileOperation.In["profile_name"].Ref != "action.profile" || profileOperation.In["invocation"].Ref != "data.invocation" || profileOperation.Out["profile"].Ref != "data.profile" || profileOperation.Out["result"].Ref != "data.result" {
		t.Fatalf("resolve-profile must keep action data mapping: %#v", profileOperation)
	}
	allocationOperation := findOperationSpec(action, OperationKindAllocateResources)
	if allocationOperation == nil {
		t.Fatalf("allocate-resources operation must be present: %#v", action.Operations)
	}
	if allocationOperation.In["requires_synthesis"].Ref != "action.requires_synthesis" || allocationOperation.In["profile"].Ref != "data.profile" || allocationOperation.Out["allocation"].Ref != "data.allocation" {
		t.Fatalf("allocate-resources must keep action data mapping: %#v", allocationOperation)
	}
	workplaceOperation := findOperationSpec(action, OperationKindPrepareWorkplace)
	if workplaceOperation == nil {
		t.Fatalf("prepare-workplace operation must be present: %#v", action.Operations)
	}
	if workplaceOperation.In["requires_workplace"].Ref != "action.requires_workplace" || workplaceOperation.In["allocation"].Ref != "data.allocation" || workplaceOperation.Out["workplace"].Ref != "data.workplace" || workplaceOperation.Out["invocation"].Ref != "data.invocation" {
		t.Fatalf("prepare-workplace must keep action data mapping: %#v", workplaceOperation)
	}
	directiveOperation := findOperationSpec(action, OperationKindBuildDirective)
	if directiveOperation == nil {
		t.Fatalf("build-directive operation must be present: %#v", action.Operations)
	}
	if directiveOperation.In["requires_synthesis"].Ref != "action.requires_synthesis" || directiveOperation.In["allocation"].Ref != "data.allocation" || directiveOperation.Out["directive"].Ref != "data.directive" {
		t.Fatalf("build-directive must keep action data mapping: %#v", directiveOperation)
	}
	launchOperation := findOperationSpec(action, OperationKindLaunchSynthesis)
	if launchOperation == nil {
		t.Fatalf("launch-synthesis operation must be present: %#v", action.Operations)
	}
	if launchOperation.In["directive"].Ref != "data.directive" || launchOperation.In["workplace"].Ref != "data.workplace" || launchOperation.Out["result"].Ref != "data.result" {
		t.Fatalf("launch-synthesis must keep action data mapping: %#v", launchOperation)
	}
	parseOperation := findOperationSpec(action, OperationKindParseResult)
	if parseOperation == nil {
		t.Fatalf("parse-result operation must be present: %#v", action.Operations)
	}
	if parseOperation.In["result"].Ref != "data.result" || parseOperation.Out["structured_output"].Ref != "data.structured_output" {
		t.Fatalf("parse-result must keep action data mapping: %#v", parseOperation)
	}
	commitOperation := findOperationSpec(action, OperationKindCommitPush)
	if commitOperation == nil {
		t.Fatalf("commit-push operation must be present: %#v", action.Operations)
	}
	if commitOperation.In["structured_output"].Ref != "data.structured_output" || commitOperation.In["workplace"].Ref != "data.workplace" || commitOperation.Out["commit_summary"].Ref != "data.commit_summary" || commitOperation.Out["result"].Ref != "data.result" {
		t.Fatalf("commit-push must keep action data mapping: %#v", commitOperation)
	}
}

func TestActionResolutionKeepsPublishMergeRequestMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionStartImplementationPR,
			Class:             ActionClassEngineeringSynthesis,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindPublishMergeRequest,
				In: map[string]methodology.ActionMapping{
					"invocation":        mappingRef("data.invocation"),
					"workplace":         mappingRef("data.workplace"),
					"result":            mappingRef("data.result"),
					"structured_output": mappingRef("data.structured_output"),
				},
				Out: map[string]methodology.ActionMapping{
					"merge_request":   mappingRef("data.merge_request"),
					"publish_summary": mappingRef("data.publish_summary"),
					"result":          mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindPublishMergeRequest, Kind: OperationKindPublishMergeRequest, Title: "Открытие запроса на слияние", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionStartImplementationPR})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindPublishMergeRequest)
	if operation == nil {
		t.Fatalf("publish-merge-request operation must be present: %#v", action.Operations)
	}
	if operation.In["structured_output"].Ref != "data.structured_output" || operation.Out["merge_request"].Ref != "data.merge_request" || operation.Out["result"].Ref != "data.result" {
		t.Fatalf("publish-merge-request must keep action data mapping: %#v", operation)
	}
}

func TestActionResolutionKeepsLoadPullRequestMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionReviewPullRequest,
			Class:             ActionClassReview,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindLoadPullRequest,
				In: map[string]methodology.ActionMapping{
					"invocation": mappingRef("data.invocation"),
				},
				Out: map[string]methodology.ActionMapping{
					"pull_request": mappingRef("data.pull_request"),
					"invocation":   mappingRef("data.invocation"),
					"result":       mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindLoadPullRequest, Kind: OperationKindLoadPullRequest, Title: "Получение запроса на слияние", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionReviewPullRequest})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindLoadPullRequest)
	if operation == nil {
		t.Fatalf("load-pull-request operation must be present: %#v", action.Operations)
	}
	if operation.In["invocation"].Ref != "data.invocation" || operation.Out["pull_request"].Ref != "data.pull_request" || operation.Out["invocation"].Ref != "data.invocation" || operation.Out["result"].Ref != "data.result" {
		t.Fatalf("load-pull-request must keep action data mapping: %#v", operation)
	}
}

func TestActionResolutionKeepsLoadReviewRemarksMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionApplyReviewComments,
			Class:             ActionClassEngineeringSynthesis,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindLoadReviewRemarks,
				In: map[string]methodology.ActionMapping{
					"invocation":   mappingRef("data.invocation"),
					"pull_request": mappingRef("data.pull_request"),
				},
				Out: map[string]methodology.ActionMapping{
					"review_remarks": mappingRef("data.review_remarks"),
					"invocation":     mappingRef("data.invocation"),
					"result":         mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindLoadReviewRemarks, Kind: OperationKindLoadReviewRemarks, Title: "Получение замечаний ревизии", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionApplyReviewComments})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindLoadReviewRemarks)
	if operation == nil {
		t.Fatalf("load-review-remarks operation must be present: %#v", action.Operations)
	}
	if operation.In["invocation"].Ref != "data.invocation" || operation.In["pull_request"].Ref != "data.pull_request" || operation.Out["review_remarks"].Ref != "data.review_remarks" || operation.Out["invocation"].Ref != "data.invocation" || operation.Out["result"].Ref != "data.result" {
		t.Fatalf("load-review-remarks must keep action data mapping: %#v", operation)
	}
}

func TestActionResolutionKeepsFinalizeMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionClassIntegrationChange,
			Class:             ActionClassIntegrationChange,
			RequiresWorkplace: boolRef(false),
			RequiresSynthesis: boolRef(false),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindFinalize,
				In: map[string]methodology.ActionMapping{
					"requires_synthesis": mappingRef("action.requires_synthesis"),
					"action_name":        mappingRef("action.name"),
					"action_class":       mappingRef("action.class"),
					"result":             mappingRef("data.result"),
				},
				Out: map[string]methodology.ActionMapping{
					"result": mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindFinalize, Kind: OperationKindFinalize, Title: "Завершающая фиксация", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionClassIntegrationChange})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindFinalize)
	if operation == nil {
		t.Fatalf("finalize operation must be present: %#v", action.Operations)
	}
	if operation.In["requires_synthesis"].Ref != "action.requires_synthesis" || operation.In["action_name"].Ref != "action.name" || operation.In["action_class"].Ref != "action.class" || operation.In["result"].Ref != "data.result" || operation.Out["result"].Ref != "data.result" {
		t.Fatalf("finalize must keep action data mapping: %#v", operation)
	}
}

func TestActionResolutionKeepsPublishReviewRemarksMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionReviewPullRequest,
			Class:             ActionClassReview,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindPublishReviewRemarks,
				In: map[string]methodology.ActionMapping{
					"invocation":        mappingRef("data.invocation"),
					"result":            mappingRef("data.result"),
					"structured_output": mappingRef("data.structured_output"),
				},
				Out: map[string]methodology.ActionMapping{
					"review_remarks_summary": mappingRef("data.review_remarks_summary"),
					"result":                 mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindPublishReviewRemarks, Kind: OperationKindPublishReviewRemarks, Title: "Запись замечаний ревизии", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionReviewPullRequest})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindPublishReviewRemarks)
	if operation == nil {
		t.Fatalf("publish-review-remarks operation must be present: %#v", action.Operations)
	}
	if operation.In["structured_output"].Ref != "data.structured_output" || operation.Out["review_remarks_summary"].Ref != "data.review_remarks_summary" || operation.Out["result"].Ref != "data.result" {
		t.Fatalf("publish-review-remarks must keep action data mapping: %#v", operation)
	}
}

func TestActionResolutionKeepsPublishReviewResponsesMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionApplyReviewComments,
			Class:             ActionClassEngineeringSynthesis,
			RequiresWorkplace: boolRef(true),
			RequiresSynthesis: boolRef(true),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindPublishReviewResponses,
				In: map[string]methodology.ActionMapping{
					"invocation":        mappingRef("data.invocation"),
					"result":            mappingRef("data.result"),
					"structured_output": mappingRef("data.structured_output"),
					"review_remarks":    mappingRef("data.review_remarks"),
				},
				Out: map[string]methodology.ActionMapping{
					"review_responses_summary": mappingRef("data.review_responses_summary"),
					"result":                   mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses, Title: "Запись ответов на замечания", Origin: OperationOriginBuiltin, Required: boolRef(true)},
		},
	}, invocation{Action: ActionApplyReviewComments})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindPublishReviewResponses)
	if operation == nil {
		t.Fatalf("publish-review-responses operation must be present: %#v", action.Operations)
	}
	if operation.In["structured_output"].Ref != "data.structured_output" || operation.In["review_remarks"].Ref != "data.review_remarks" || operation.Out["review_responses_summary"].Ref != "data.review_responses_summary" || operation.Out["result"].Ref != "data.result" {
		t.Fatalf("publish-review-responses must keep action data mapping: %#v", operation)
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
	allocation       model.Allocation
	workplace        model.Workplace
	result           model.LaunchResult
	err              error
	beforeReturn     func()
	commitCalled     bool
	commitInvocation model.Invocation
	commitAllocation model.Allocation
	commitWorkplace  model.Workplace
	commitOutput     *model.StructuredOutput
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

func (s *stubLauncher) Launch(_ context.Context, in model.Invocation, profile model.Profile, allocation model.Allocation, workplace model.Workplace) (model.LaunchResult, error) {
	s.invocation = in
	s.profile = profile
	s.allocation = allocation
	s.workplace = workplace
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
	s.commitInvocation = in
	s.commitAllocation = allocation
	s.commitWorkplace = workplace
	s.commitOutput = output
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
	invocation model.Invocation
	profile    model.Profile
	err        error
}

func (s *stubResourceProvider) Allocate(_ context.Context, in model.Invocation, profile model.Profile) (model.Allocation, error) {
	if s.err != nil {
		return model.Allocation{}, s.err
	}
	s.invocation = in
	s.profile = profile
	return s.allocation, nil
}

type stubWorkplaceManager struct {
	workplace  model.Workplace
	invocation model.Invocation
	profile    model.Profile
	allocation model.Allocation
	err        error
}

func (s *stubWorkplaceManager) Prepare(_ context.Context, in model.Invocation, profile model.Profile, allocation model.Allocation) (model.Workplace, error) {
	s.invocation = in
	s.profile = profile
	s.allocation = allocation
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

func prepareDataOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindPrepareData,
		Kind:   OperationKindPrepareData,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"expected_result":  {Ref: "in.expected_result"},
			"constraints":      {Ref: "in.constraints"},
			"canonical_task":   {Ref: "in.canonical_task"},
			"related_objects":  {Ref: "in.related_objects"},
			"reasons":          {Ref: "in.reasons"},
			"structured_input": {Ref: "in.structured_input"},
		},
		Out: model.OperationMap{
			"structured_input": {Ref: "data.structured_input"},
			"workplace":        {Ref: "data.workplace"},
			"invocation":       {Ref: "data.invocation"},
		},
	}
}

func allocateResourcesOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindAllocateResources,
		Kind:   OperationKindAllocateResources,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_synthesis": {Ref: "action.requires_synthesis"},
			"invocation":         {Ref: "data.invocation"},
			"profile":            {Ref: "data.profile"},
		},
		Out: model.OperationMap{
			"allocation": {Ref: "data.allocation"},
		},
	}
}

func prepareWorkplaceOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindPrepareWorkplace,
		Kind:   OperationKindPrepareWorkplace,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_workplace": {Ref: "action.requires_workplace"},
			"invocation":         {Ref: "data.invocation"},
			"profile":            {Ref: "data.profile"},
			"allocation":         {Ref: "data.allocation"},
		},
		Out: model.OperationMap{
			"workplace":  {Ref: "data.workplace"},
			"invocation": {Ref: "data.invocation"},
		},
	}
}

func buildDirectiveOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindBuildDirective,
		Kind:   OperationKindBuildDirective,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_synthesis": {Ref: "action.requires_synthesis"},
			"invocation":         {Ref: "data.invocation"},
			"allocation":         {Ref: "data.allocation"},
		},
		Out: model.OperationMap{
			"directive": {Ref: "data.directive"},
		},
	}
}

func launchSynthesisOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindLaunchSynthesis,
		Kind:   OperationKindLaunchSynthesis,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_synthesis": {Ref: "action.requires_synthesis"},
			"invocation":         {Ref: "data.invocation"},
			"directive":          {Ref: "data.directive"},
			"profile":            {Ref: "data.profile"},
			"allocation":         {Ref: "data.allocation"},
			"workplace":          {Ref: "data.workplace"},
		},
		Out: model.OperationMap{
			"result": {Ref: "data.result"},
		},
	}
}

func parseResultOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindParseResult,
		Kind:   OperationKindParseResult,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_synthesis": {Ref: "action.requires_synthesis"},
			"result":             {Ref: "data.result"},
		},
		Out: model.OperationMap{
			"structured_output": {Ref: "data.structured_output"},
		},
	}
}

func commitPushOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindCommitPush,
		Kind:   OperationKindCommitPush,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_synthesis": {Ref: "action.requires_synthesis"},
			"invocation":         {Ref: "data.invocation"},
			"profile":            {Ref: "data.profile"},
			"allocation":         {Ref: "data.allocation"},
			"workplace":          {Ref: "data.workplace"},
			"result":             {Ref: "data.result"},
			"structured_output":  {Ref: "data.structured_output"},
		},
		Out: model.OperationMap{
			"commit_summary": {Ref: "data.commit_summary"},
			"result":         {Ref: "data.result"},
		},
	}
}

func loadPullRequestOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindLoadPullRequest,
		Kind:   OperationKindLoadPullRequest,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"invocation": {Ref: "data.invocation"},
		},
		Out: model.OperationMap{
			"pull_request": {Ref: "data.pull_request"},
			"invocation":   {Ref: "data.invocation"},
			"result":       {Ref: "data.result"},
		},
	}
}

func loadReviewRemarksOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindLoadReviewRemarks,
		Kind:   OperationKindLoadReviewRemarks,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"invocation":   {Ref: "data.invocation"},
			"pull_request": {Ref: "data.pull_request"},
		},
		Out: model.OperationMap{
			"review_remarks": {Ref: "data.review_remarks"},
			"invocation":     {Ref: "data.invocation"},
			"result":         {Ref: "data.result"},
		},
	}
}

func finalizeOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindFinalize,
		Kind:   OperationKindFinalize,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"requires_synthesis": {Ref: "action.requires_synthesis"},
			"action_name":        {Ref: "action.name"},
			"action_class":       {Ref: "action.class"},
			"result":             {Ref: "data.result"},
		},
		Out: model.OperationMap{
			"result": {Ref: "data.result"},
		},
	}
}

func publishMergeRequestOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindPublishMergeRequest,
		Kind:   OperationKindPublishMergeRequest,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"invocation":        {Ref: "data.invocation"},
			"workplace":         {Ref: "data.workplace"},
			"result":            {Ref: "data.result"},
			"structured_output": {Ref: "data.structured_output"},
		},
		Out: model.OperationMap{
			"merge_request":   {Ref: "data.merge_request"},
			"publish_summary": {Ref: "data.publish_summary"},
			"result":          {Ref: "data.result"},
		},
	}
}

func publishReviewRemarksOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindPublishReviewRemarks,
		Kind:   OperationKindPublishReviewRemarks,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"invocation":        {Ref: "data.invocation"},
			"result":            {Ref: "data.result"},
			"structured_output": {Ref: "data.structured_output"},
		},
		Out: model.OperationMap{
			"review_remarks_summary": {Ref: "data.review_remarks_summary"},
			"result":                 {Ref: "data.result"},
		},
	}
}

func publishReviewResponsesOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name:   OperationKindPublishReviewResponses,
		Kind:   OperationKindPublishReviewResponses,
		Origin: OperationOriginBuiltin,
		In: model.OperationMap{
			"invocation":        {Ref: "data.invocation"},
			"result":            {Ref: "data.result"},
			"structured_output": {Ref: "data.structured_output"},
			"review_remarks":    {Ref: "data.review_remarks"},
		},
		Out: model.OperationMap{
			"review_responses_summary": {Ref: "data.review_responses_summary"},
			"result":                   {Ref: "data.result"},
		},
	}
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
