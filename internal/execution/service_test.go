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
	}, profile{Mode: "manual"}, allocation{Runner: "codex", Model: "gpt-5.3-codex", ModelBinding: "coder", ReasoningEffort: "medium"}, workplace{})
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
	if launcher.invocation.Launch.ReasoningEffort != "medium" {
		t.Fatalf("expected reasoning effort from allocation, got %q", launcher.invocation.Launch.ReasoningEffort)
	}
}

func TestServiceLaunchPreservesExplicitReasoningEffortWhenAllocationOmitsIt(t *testing.T) {
	t.Parallel()

	launcher := &stubLauncher{}
	service := &Service{logger: log.Default(), launcher: launcher}

	_, err := service.launch(context.Background(), invocation{Launch: launchSpec{
		Directory: "/tmp/work", Runner: "codex", Model: "gpt-5.3-codex-spark", ReasoningEffort: "medium", Prompt: "ship it",
	}}, profile{}, allocation{Runner: "codex", Model: "gpt-5.3-codex-spark"}, workplace{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if launcher.invocation.Launch.ReasoningEffort != "medium" {
		t.Fatalf("expected explicit reasoning effort to be preserved, got %q", launcher.invocation.Launch.ReasoningEffort)
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
	if runs[0].RawStructuredOutput != `{"summary":"Done."}` {
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
	if result.Action.Name != ActionClassEngineeringSynthesis || result.Action.Profile != "default" {
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
		OperationKindBuildPrompt,
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
	launchOperation := findOperationResult(result.Operations, OperationKindLaunchSynthesis)
	if launchOperation == nil || launchOperation.Status != OperationStatusFailed {
		t.Fatalf("launch operation must be failed: %#v", result.Operations)
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
			Operations: []model.OperationSpec{
				builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
				builtinOperation(OperationKindResolveProfile, "Выбор исполнительного профиля", true),
				builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true),
				builtinOperation(OperationKindPrepareWorkplace, "Подготовка рабочего места", true),
				builtinOperation(OperationKindBuildDirective, "Сборка исполнительной директивы", true),
				{Name: OperationKindLaunchSynthesis, Kind: OperationKindLaunchSynthesis, Title: "Запуск синтеза", Required: true},
				{Name: "normalize-output", Kind: OperationKindParseResult, Title: "Разбор результата", Required: true},
				{Name: "finish-run", Kind: OperationKindFinalize, Title: "Завершающая фиксация", Required: true},
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
	if operation := findOperationResult(result.Operations, "normalize-output"); operation == nil || operation.Status != OperationStatusPending {
		t.Fatalf("custom parse operation must remain pending: %#v", result.Operations)
	}
	if operation := findOperationResult(result.Operations, "finish-run"); operation == nil || operation.Status != OperationStatusPending {
		t.Fatalf("custom finalize operation must remain pending: %#v", result.Operations)
	}
	if operation := findOperationResult(result.Operations, OperationKindParseResult); operation != nil {
		t.Fatalf("synthetic parse-result operation must not be added: %#v", operation)
	}
	if operation := findOperationResult(result.Operations, OperationKindFinalize); operation != nil {
		t.Fatalf("synthetic finalize operation must not be added: %#v", operation)
	}
}

func TestServiceExecuteRunsActionOperationWithIsolatedInputAndOutput(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)

	parseOperation := model.OperationSpec{
		Name:       OperationKindParseResult,
		Type:       model.OperationType(OperationTypeBuiltin),
		Kind:       OperationKindParseResult,
		Required:   true,
		RequiredIn: []string{"raw_output"},
		In: model.OperationMap{
			"raw_output": {Ref: "in.raw_output"},
		},
		Out: model.OperationMap{
			"result":            {Ref: "data.result"},
			"structured_output": {Ref: "data.structured_output"},
		},
	}
	child := model.Action{
		Name:         "engineering-synthesis",
		Operations:   []model.OperationSpec{parseOperation},
		OutputFields: []string{"result", "structured_output"},
		RequiredOut:  []string{"result"},
	}
	parent := model.Action{
		Name: "implement",
		Operations: []model.OperationSpec{{
			Name:       "execute-code",
			Type:       model.OperationType(OperationTypeAction),
			Kind:       "engineering-synthesis",
			Required:   true,
			RequiredIn: []string{"raw_output"},
			In: model.OperationMap{
				"raw_output": {Value: json.RawMessage(`"<progress-structured-output>\n{\"summary\":\"Готово.\"}\n</progress-structured-output>"`)},
			},
			Out: model.OperationMap{
				"result":            {Ref: "data.result"},
				"structured_output": {Ref: "data.structured_output"},
			},
		}},
	}
	service := &Service{
		logger: log.Default(),
		actions: &stubActionResolver{actions: map[string]model.Action{
			"implement":             parent,
			"engineering-synthesis": child,
		}},
	}

	result, err := service.ExecuteAction(context.Background(), ActionInvocation{Assignment: &ExecutionAssignment{Action: "implement"}})
	if err != nil {
		t.Fatalf("execute action operation: %v", err)
	}
	if result.Launch == nil || result.Launch.StructuredOutput == nil || result.Launch.StructuredOutput.Summary != "Готово." {
		t.Fatalf("action operation output must be published to parent: %#v", result)
	}
	operation := findOperationResult(result.Operations, "execute-code")
	if operation == nil || operation.Status != OperationStatusCompleted || len(operation.Operations) != 1 || operation.Operations[0].Name != OperationKindParseResult {
		t.Fatalf("action operation must keep nested operation results: %#v", operation)
	}
}

func TestServiceExecuteUsesMinimalIntegrationActionComposition(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	service := &Service{
		logger: log.Default(),
		actions: &stubActionResolver{action: model.Action{
			Name:       ActionClassIntegrationChange,
			Class:      ActionClassIntegrationChange,
			Operations: []model.OperationSpec{finalizeOperationSpec()},
		}},
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
	if result.Status != "completed" || result.Action.RequiresWorkplace {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	if result.Action.Class != ActionClassIntegrationChange {
		t.Fatalf("unexpected action class: %#v", result.Action)
	}
	if len(result.Operations) != 1 || result.Operations[0].Name != OperationKindFinalize {
		t.Fatalf("integration action must contain only finalize: %#v", result.Operations)
	}
	if result.Launch == nil || result.Launch.Status != "completed" || !strings.Contains(result.Launch.Summary, "operations=completed") {
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
			Name:    "profile-check",
			Class:   ActionClassEngineeringSynthesis,
			Profile: "coder",
			Operations: []model.OperationSpec{
				builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
				{
					Name:  OperationKindResolveProfile,
					Kind:  OperationKindResolveProfile,
					Title: "Выбор исполнительного профиля",

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
					Name:  OperationKindAllocateResources,
					Kind:  OperationKindAllocateResources,
					Title: "Ресурсное снабжение",

					In: model.OperationMap{
						"invocation": {Ref: "data.invocation"},
						"profile":    {Ref: "data.profile"},
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
		Name: OperationKindResolveProfile,
		Kind: OperationKindResolveProfile,

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
		Name: OperationKindResolveProfile,
		Kind: OperationKindResolveProfile,

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
		Name: OperationKindResolveProfile,
		Kind: OperationKindResolveProfile,

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
		assignment: &ExecutionAssignment{
			ExpectedResult:  "Старый результат.",
			CanonicalTask:   &ObjectRef{Type: "task", Repository: "legacy/name", Number: 999},
			StructuredInput: &StructuredInput{Task: "Legacy."},
		},
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
			Operations: []model.OperationSpec{operation},
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

func TestAllocateResourcesUsesResolvedProfileFields(t *testing.T) {
	t.Parallel()

	state := &operationExecution{data: map[string]any{
		"profile": model.Profile{Name: "coder", ModelBinding: "coder", AllowModelFallback: false},
	}}
	operation := model.OperationSpec{In: model.OperationMap{
		"model_binding":        {Ref: "data.profile.model_binding"},
		"allow_model_fallback": {Ref: "data.profile.allow_model_fallback"},
	}}

	input := allocateResourcesInputFromOperation(state, operation)
	resolved := input.resolvedProfile()
	if resolved.ModelBinding != "coder" || resolved.AllowModelFallback {
		t.Fatalf("allocate-resources must receive resolved profile fields: %#v", resolved)
	}
}

func TestAllocateResourcesPreservesExplicitReasoningEffort(t *testing.T) {
	t.Parallel()

	state := &operationExecution{data: map[string]any{
		"invocation": model.Invocation{Launch: model.LaunchSpec{ReasoningEffort: "medium"}},
	}}
	operation := model.OperationSpec{In: model.OperationMap{
		"reasoning_effort": {Ref: "data.invocation.launch.reasoning_effort"},
	}}

	input := allocateResourcesInputFromOperation(state, operation)
	if input.reasoningEffort != "medium" {
		t.Fatalf("allocate-resources must preserve explicit reasoning effort: %q", input.reasoningEffort)
	}
}

func TestLaunchSynthesisDoesNotReadImplicitAllocationReasoningEffort(t *testing.T) {
	t.Parallel()

	state := &operationExecution{data: map[string]any{
		"allocation": model.Allocation{ReasoningEffort: "medium"},
	}}
	operation := model.OperationSpec{In: model.OperationMap{
		"prompt":    {Value: []byte(`"ship it"`)},
		"directory": {Value: []byte(`"/tmp/work"`)},
		"runner":    {Value: []byte(`"codex"`)},
		"model":     {Value: []byte(`"gpt-5.3-codex-spark"`)},
	}}

	input := launchSynthesisInputFromOperation(state, operation)
	if input.reasoningEffort != "" {
		t.Fatalf("launch-synthesis must use only explicit input mappings: %q", input.reasoningEffort)
	}
}

func TestAllocateResourcesFailureDoesNotWriteStateResult(t *testing.T) {
	t.Parallel()

	operation := allocateResourcesOperationSpec()
	state := &operationExecution{
		action: model.Action{
			Operations: []model.OperationSpec{operation},
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
			StructuredOutputFields: []string{"remarks", "conclusion"},
			Operations:             []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42", Launch: model.LaunchSpec{StructuredInput: &StructuredInput{Task: "Ship it."}}},
			"profile":    model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
			"allocation": model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
		},
		profile:    model.Profile{Name: "legacy", Mode: "manual", ModelBinding: "legacy"},
		allocation: model.Allocation{Resource: "legacy", Runner: "legacy", Model: "legacy"},
		workplace:  model.Workplace{Name: "/tmp/legacy", Ready: true},
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
	if strings.Join(directive.StructuredOutputFields, ",") != "remarks,conclusion" {
		t.Fatalf("build-directive must transfer action structured output fields: %#v", directive.StructuredOutputFields)
	}
	if state.in.Launch.Runner != "" || state.in.Launch.Model != "" || state.in.Launch.ModelBinding != "" {
		t.Fatalf("build-directive must not write implicit state launch: %#v", state.in.Launch)
	}
	if state.allocation.Resource != "legacy" || state.allocation.Runner != "legacy" {
		t.Fatalf("build-directive must not read or write implicit state allocation: %#v", state.allocation)
	}
	if state.profile.Name != "legacy" || state.profile.ModelBinding != "legacy" {
		t.Fatalf("build-directive must not read or write implicit state profile: %#v", state.profile)
	}
	if state.workplace.Name != "/tmp/legacy" || !state.workplace.Ready {
		t.Fatalf("build-directive must not read or write implicit state workplace: %#v", state.workplace)
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
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Launch: model.LaunchSpec{ModelBinding: "explicit"}},
			"profile":    model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
			"allocation": model.Allocation{Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
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

func TestParseResultFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := parseResultOperationSpec()
	output := &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}
	state := &operationExecution{
		action: model.Action{
			Operations: []model.OperationSpec{operation},
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

func TestCommitPushFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := commitPushOperationSpec()
	structuredOutput := &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}
	state := &operationExecution{
		in: model.Invocation{
			Task: "legacy",
		},
		action: model.Action{
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation":        model.Invocation{Task: "task-42"},
			"profile":           model.Profile{Name: "coder", Mode: "manual", ModelBinding: "coder"},
			"allocation":        model.Allocation{Resource: "binding:coder", Runner: "opencode", Model: "openai/gpt-5.5", ModelBinding: "coder"},
			"workplace":         model.Workplace{Name: "/tmp/work", Ready: true},
			"result":            model.LaunchResult{Status: "completed", Summary: "launch complete"},
			"structured_output": structuredOutput,
		},
		profile:    model.Profile{Name: "legacy", Mode: "manual", ModelBinding: "legacy"},
		allocation: model.Allocation{Resource: "legacy", Runner: "legacy"},
		workplace:  model.Workplace{Name: "/tmp/legacy", Ready: true},
		result:     model.LaunchResult{Status: "legacy", Summary: "legacy", StructuredOutput: &model.StructuredOutput{Summary: "Legacy."}},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
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
	if dataResult.Summary != "launch complete" {
		t.Fatalf("commit-push must not rewrite launch result: %#v", dataResult)
	}
	if state.result.Status != "legacy" || state.result.Summary != "legacy" {
		t.Fatalf("commit-push must not write implicit state result: %#v", state.result)
	}
	if state.profile.Name != "legacy" || state.profile.ModelBinding != "legacy" {
		t.Fatalf("commit-push must not read or write implicit state profile: %#v", state.profile)
	}
	if state.allocation.Resource != "legacy" || state.allocation.Runner != "legacy" {
		t.Fatalf("commit-push must not read or write implicit state allocation: %#v", state.allocation)
	}
	if state.workplace.Name != "/tmp/legacy" || !state.workplace.Ready {
		t.Fatalf("commit-push must not read or write implicit state workplace: %#v", state.workplace)
	}
	if !launcher.commitCalled || launcher.commitInput.CommitMessage != "Apply change" {
		t.Fatalf("commit-push must pass narrow commit message: %#v", launcher.commitInput)
	}
	if launcher.commitInput.Directory != "/tmp/work" {
		t.Fatalf("commit-push must pass narrow repository directory: %#v", launcher.commitInput)
	}
}

func TestCommitPushDoesNotReadActionSynthesisFlag(t *testing.T) {
	t.Parallel()

	operation := commitPushOperationSpec()
	state := &operationExecution{
		in: model.Invocation{Task: "task-42"},
		action: model.Action{
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation":        model.Invocation{Task: "task-42"},
			"profile":           model.Profile{Name: "default", Mode: "manual"},
			"allocation":        model.Allocation{Resource: "not-required"},
			"workplace":         model.Workplace{Name: "/repo", Ready: true},
			"result":            model.LaunchResult{Status: "skipped", Summary: "synthesis=not-required"},
			"structured_output": (*model.StructuredOutput)(nil),
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{
		logger:   log.Default(),
		launcher: &stubLauncher{commitErr: errors.New("commit called")},
	}

	err := builtinOperationExecutor{service: service}.commitPush(context.Background(), state, operation, OperationKindCommitPush)
	if err == nil || err.Error() != "commit called" {
		t.Fatalf("commit-push must execute solely because it is present in the action: %v", err)
	}
}

func TestOperationFailsWhenRequiredInputIsNotResolved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   model.OperationMap
	}{
		{name: "mapping is absent", in: model.OperationMap{}},
		{name: "referenced data is absent", in: model.OperationMap{"directory": {Ref: "data.workplace.name"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := model.OperationSpec{
				Name: OperationKindCommitPush,
				Kind: OperationKindCommitPush,

				Required:   true,
				RequiredIn: []string{"directory"},
				In:         test.in,
			}
			state := &operationExecution{
				action:  model.Action{Operations: []model.OperationSpec{operation}},
				data:    map[string]any{},
				tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
			}
			service := &Service{logger: log.Default(), launcher: &stubLauncher{}}

			err := (builtinOperationExecutor{service: service}).Execute(context.Background(), state, operation)
			if err == nil || !strings.Contains(err.Error(), `required input "directory" is not resolved`) {
				t.Fatalf("expected required input error, got %v", err)
			}
			result := findOperationResult(state.tracker.snapshot(), OperationKindCommitPush)
			if result == nil || result.Status != OperationStatusFailed || result.Failure == nil || result.Failure.Code != "operation_required_input_missing" {
				t.Fatalf("unexpected operation result: %#v", result)
			}
			if service.launcher.(*stubLauncher).commitCalled {
				t.Fatal("operation implementation must not run after contract validation failure")
			}
		})
	}
}

func TestLoadPullRequestFillsOnlyActionData(t *testing.T) {
	t.Parallel()

	operation := loadPullRequestOperationSpec()
	state := &operationExecution{
		in: model.Invocation{
			Action: "legacy",
			Assignment: &ExecutionAssignment{
				RelatedObjects: []ObjectRef{{
					Type:       "merge-request",
					Repository: "legacy/name",
					Number:     999,
				}},
			},
		},
		action: model.Action{Operations: []model.OperationSpec{operation}},
		data: map[string]any{
			"invocation": model.Invocation{
				Task: "task-112",
				Assignment: &ExecutionAssignment{
					CanonicalTask: &ObjectRef{Type: "task", Number: 112},
					RelatedObjects: []ObjectRef{{
						Type:       "merge-request",
						Repository: "owner/name",
						Number:     17,
					}},
				},
			},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			if req.Operation != "get" || req.Repository != "owner/name" || req.MergeRequestNumber != 17 {
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
			if req.Operation != "list" || req.Repository != "owner/name" || req.MergeRequestNumber != 17 {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			return integration.Response{ReviewRemarks: []integration.ReviewRemark{{
				Repository:         req.Repository,
				MergeRequestNumber: req.MergeRequestNumber,
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
	if !ok || dataInvocation.Launch.StructuredInput != nil {
		t.Fatalf("load-review-remarks must not modify data.invocation: %#v", state.data)
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
	if _, ok := state.data["result"]; ok {
		t.Fatalf("load-review-remarks must not write data outside its output contract: %#v", state.data)
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
			Name:       ActionClassIntegrationChange,
			Class:      ActionClassIntegrationChange,
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
			"profile":    model.Profile{Name: "default", Mode: "manual"},
			"allocation": model.Allocation{Resource: "not-required"},
			"workplace":  model.Workplace{Name: "/repo", Ready: true},
			"result":     model.LaunchResult{Status: "legacy", Summary: "legacy"},
		},
		in:         model.Invocation{Task: "legacy"},
		profile:    model.Profile{Name: "legacy", Mode: "manual"},
		allocation: model.Allocation{Resource: "legacy"},
		workplace:  model.Workplace{Name: "/tmp/legacy", Ready: true},
		result:     model.LaunchResult{Status: "legacy-state", Summary: "legacy-state"},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	service := &Service{logger: log.Default()}

	err := builtinOperationExecutor{service: service}.finalize(context.Background(), state, operation, OperationKindFinalize)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "legacy" || dataResult.Summary != "legacy" {
		t.Fatalf("finalize must fill data.result: %#v", state.data)
	}
	if state.result.Status != "legacy-state" || state.result.Summary != "legacy-state" {
		t.Fatalf("finalize must not write implicit state result: %#v", state.result)
	}
	if state.in.Task != "legacy" || state.profile.Name != "legacy" || state.allocation.Resource != "legacy" || state.workplace.Name != "/tmp/legacy" {
		t.Fatalf("finalize must not read or write implicit state context: invocation=%#v profile=%#v allocation=%#v workplace=%#v", state.in, state.profile, state.allocation, state.workplace)
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
			Name:       ActionClassEngineeringSynthesis,
			Class:      ActionClassEngineeringSynthesis,
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
			"profile":    model.Profile{Name: "coder", Mode: "manual"},
			"allocation": model.Allocation{Resource: "binding:coder"},
			"workplace":  model.Workplace{Name: "/repo", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "data-result"},
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
			"profile":           model.Profile{Name: "coder"},
			"allocation":        model.Allocation{Model: "gpt-5"},
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
			if req.Operation == "search" {
				return integration.Response{}, nil
			}
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
			"profile":           model.Profile{Name: "coder"},
			"allocation":        model.Allocation{Model: "gpt-5"},
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
			"invocation": model.Invocation{
				Assignment: &ExecutionAssignment{
					CanonicalTask: &ObjectRef{Type: "task", Repository: "owner/name", Number: 132},
					RelatedObjects: []ObjectRef{{
						Type:       "merge-request",
						Repository: "owner/name",
						Number:     17,
					}},
				},
			},
			"profile":    model.Profile{Name: "coder"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "launch complete"},
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
			"profile":    model.Profile{Name: "review"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "review complete"},
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
			if req.Operation != "create" || req.Repository != "owner/name" || req.MergeRequestNumber != 17 || req.Path != "internal/execution/service.go" || req.Line != 42 {
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
			"profile":    model.Profile{Name: "review"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "review complete"},
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
			"profile":           model.Profile{Name: "review"},
			"allocation":        model.Allocation{Model: "gpt-5"},
			"workplace":         model.Workplace{Name: "/tmp/work", Ready: true},
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
			"profile":    model.Profile{Name: "coder"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "apply complete"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-1",
				ThreadID: "thread-1",
				Status:   "resolved",
				Summary:  "Проверка добавлена.",
				Body:     "Добавил покрытие отказа.",
			}}},
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
				if req.Operation != "reply" || req.Repository != "owner/name" || req.MergeRequestNumber != 17 || req.ThreadID != "thread-1" || !strings.Contains(req.Body, "Добавил покрытие отказа.") {
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

func TestPublishReviewResponsesSupportsCommentAndInlineRemarks(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{
				{RemarkID: "remark-3", Type: "comment", Status: "fixed", Summary: "Ответ на общий комментарий."},
				{RemarkID: "remark-1", Type: "inline", ThreadID: "thread-1", Status: "resolved", Summary: "Исправлено."},
				{RemarkID: "remark-2", Type: "inline", ThreadID: "thread-2", Status: "resolved", Summary: "Исправлено."},
				{RemarkID: "local-1", Type: "local", Status: "fixed", Summary: "Локальная заметка."},
			}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	err := builtinOperationExecutor{service: service}.publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if got := state.data["review_responses_summary"]; got != "review-responses-published=3 review-threads-resolved=2 review-responses-skipped=1" {
		t.Fatalf("unexpected summary: %#v", got)
	}
	if len(calls) != 5 {
		t.Fatalf("expected one comment, two replies and two resolutions, got %#v", calls)
	}
	if calls[0].Operation != "create" || calls[0].ThreadID != "" {
		t.Fatalf("ordinary comment must use explicit create policy: %#v", calls[0])
	}
	if calls[1].Operation != "reply" || calls[1].ThreadID != "thread-1" || calls[2].Operation != "reply" || calls[2].ThreadID != "thread-2" {
		t.Fatalf("inline responses must target their threads: %#v", calls)
	}
}

func TestPublishReviewResponsesRestoresKindFromCanonicalRemark(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{{
				ExternalID: "remark-3",
				Type:       "comment",
			}},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-3",
				Status:   "fixed",
				Summary:  "Ответ на общий комментарий.",
			}}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 1 || calls[0].Operation != "create" || calls[0].ThreadID != "" {
		t.Fatalf("canonical comment kind must select explicit create policy: %#v", calls)
	}
}

func TestPublishReviewResponsesRestoresKindByStructuredRemarkExternalID(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{
				{ExternalID: "PRRC_inline-1", ReplyToID: "PRRT_thread-1"},
				{ExternalID: "PRRC_comment-1", Type: "comment"},
			},
			"structured_output": &model.StructuredOutput{
				Remarks: []model.StructuredRemark{
					{ID: "remark-1", ExternalID: "PRRC_inline-1"},
					{ID: "remark-2", ExternalID: "PRRC_comment-1"},
				},
				ReviewResponses: []model.StructuredResponse{
					{RemarkID: "remark-1", Status: "resolved", Summary: "Строчный ответ."},
					{RemarkID: "remark-2", Status: "fixed", Summary: "Ответ на общий комментарий."},
				},
			},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 3 || calls[0].Operation != "reply" || calls[0].ThreadID != "PRRT_thread-1" || calls[1].Operation != "create" || calls[1].ThreadID != "" || calls[2].Operation != "resolve" {
		t.Fatalf("response kind and thread must follow canonical external identifiers: %#v", calls)
	}
}

func TestPublishReviewResponsesRejectsPRRCWithoutThreadBeforePublication(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{{
				ExternalID: "PRRC_kwDOSYi3G87Um5M0",
			}},
			"structured_output": &model.StructuredOutput{
				Remarks: []model.StructuredRemark{{ID: "remark-1", ExternalID: "PRRC_kwDOSYi3G87Um5M0"}},
				ReviewResponses: []model.StructuredResponse{{
					RemarkID: "remark-1",
					Status:   "resolved",
					Summary:  "Исправлено.",
				}},
			},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err == nil || !strings.Contains(err.Error(), "thread_id is required") {
		t.Fatalf("PRRC without thread_id must fail with a diagnostic: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("PRRC without thread_id must not be published externally: %#v", calls)
	}
}

func TestPublishReviewResponsesRejectsPRRCThreadIDBeforePublication(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-1", Type: "inline", ThreadID: "PRRC_comment-1", Status: "resolved", Summary: "Исправлено.",
			}}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err == nil || !strings.Contains(err.Error(), "review comment identifier") {
		t.Fatalf("PRRC thread_id must fail with a diagnostic: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("PRRC thread_id must not be published externally: %#v", calls)
	}
}

func TestPublishReviewResponsesCanonicalKindOverridesConflictingResponse(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{
				{ExternalID: "PRRC_inline-1", Type: "inline", ReplyToID: "PRRT_thread-1"},
				{ExternalID: "PRRC_comment-1", Type: "comment"},
			},
			"structured_output": &model.StructuredOutput{
				Remarks: []model.StructuredRemark{
					{ID: "remark-1", ExternalID: "PRRC_inline-1"},
					{ID: "remark-2", ExternalID: "PRRC_comment-1"},
				},
				ReviewResponses: []model.StructuredResponse{
					{RemarkID: "remark-1", Type: "comment", ThreadID: "stale-thread", Status: "resolved", Summary: "Строчный ответ."},
					{RemarkID: "remark-2", Type: "inline", ThreadID: "stale-thread", Status: "fixed", Summary: "Ответ на общий комментарий."},
				},
			},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 3 || calls[0].Operation != "reply" || calls[0].ThreadID != "PRRT_thread-1" || calls[1].Operation != "create" || calls[1].ThreadID != "" || calls[2].Operation != "resolve" {
		t.Fatalf("canonical response kind must override conflicting synthesized values: %#v", calls)
	}
}

func TestPublishReviewResponsesCommentIgnoresThreadID(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-3",
				Type:     "comment",
				ThreadID: "stale-thread-id",
				Status:   "fixed",
				Summary:  "Ответ на общий комментарий.",
			}}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 1 || calls[0].Operation != "create" || calls[0].ThreadID != "" || calls[0].ExternalID != "" {
		t.Fatalf("comment response must use create regardless of thread_id: %#v", calls)
	}
}

func TestPublishReviewResponsesRestoresKindFromRemarkShape(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{
				{ExternalID: "inline-remark", ReplyToID: "inline-thread"},
				{ExternalID: "comment-remark", URL: "https://example.test/comment"},
			},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{
				{RemarkID: "inline-remark", Status: "resolved", Summary: "Исправлено."},
				{RemarkID: "comment-remark", Status: "fixed", Summary: "Ответ дан."},
			}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 3 || calls[0].Operation != "reply" || calls[0].ThreadID != "inline-thread" || calls[1].Operation != "create" || calls[1].ThreadID != "" || calls[2].Operation != "resolve" {
		t.Fatalf("response kind must follow canonical remark shape: %#v", calls)
	}
}

func TestPublishReviewResponsesKeepsExplicitThreadKindWhenRemarkKindIsUnknown(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{{
				ExternalID: "legacy-remark",
			}},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "legacy-remark",
				ThreadID: "thread-1",
				Status:   "resolved",
				Summary:  "Исправлено.",
			}}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 2 || calls[0].Operation != "reply" || calls[0].ThreadID != "thread-1" || calls[1].Operation != "resolve" {
		t.Fatalf("explicit thread_id must preserve inline response policy: %#v", calls)
	}
}

func TestPublishReviewResponsesRejectsUnclassifiedResponseWithoutExternalPublication(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{
				{RemarkID: "local-1", Status: "fixed", Summary: "Локальная заметка."},
				{RemarkID: "remark-1", Type: "inline", ThreadID: "thread-1", Status: "resolved", Summary: "Исправлено."},
			}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err == nil {
		t.Fatalf("unclassified response must produce a partial failure")
	}
	if len(calls) != 0 {
		t.Fatalf("unclassified response must not be published externally: %#v", calls)
	}
}

func TestPublishReviewResponsesRejectsResponseWithoutRemarkKind(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{
				{RemarkID: "local-1", Status: "fixed", Summary: "Не публиковать без вида замечания."},
				{RemarkID: "remark-1", Type: "inline", ThreadID: "thread-1", Status: "resolved", Summary: "Исправлено."},
			}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err == nil {
		t.Fatalf("response without canonical remark kind must produce a partial failure")
	}
	if len(calls) != 0 {
		t.Fatalf("unclassified response must not be published externally: %#v", calls)
	}
}

func TestPublishReviewResponsesDoesNotUseRemarkCategoryAsResponseKind(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"review_remarks": []integration.ReviewRemark{{
				ExternalID: "remark-3",
				Type:       "comment",
			}},
			"structured_output": &model.StructuredOutput{Remarks: []model.StructuredRemark{{
				ID:         "remark-3",
				Type:       "correctness",
				Status:     "resolved",
				Answer:     "Ответ на общий комментарий.",
				Resolution: "Исправлено.",
			}}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err != nil {
		t.Fatalf("publish review responses: %v", err)
	}
	if len(calls) != 1 || calls[0].Operation != "create" || calls[0].ThreadID != "" {
		t.Fatalf("canonical comment kind must override remark category: %#v", calls)
	}
}

func TestPublishReviewResponsesKeepsPartialDiagnostics(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{
				{RemarkID: "remark-3", Type: "comment", Status: "fixed", Summary: "Ответ."},
				{RemarkID: "remark-1", Type: "inline", ThreadID: "thread-1", Status: "resolved", Summary: "Исправлено."},
			}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		if req.Operation == "create" {
			return integration.Response{}, errors.New("обычный комментарий недоступен")
		}
		return integration.Response{Status: "ok"}, nil
	}}}

	err := builtinOperationExecutor{service: service}.publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses)
	if err == nil || len(calls) != 3 {
		t.Fatalf("partial publication must continue after one failure: err=%v calls=%#v", err, calls)
	}
	dataResult, ok := state.data["result"].(model.LaunchResult)
	if !ok || dataResult.Status != "failed" || !strings.Contains(dataResult.Summary, "review-responses-published=1") || !strings.Contains(dataResult.Summary, "обычный комментарий недоступен") {
		t.Fatalf("partial publication must preserve diagnostics and successful count: %#v", state.data)
	}
}

func TestPublishReviewResponsesDoesNotResolveUnpublishedInlineResponse(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-1", Type: "inline", ThreadID: "thread-1", Status: "resolved", Summary: "Исправлено.",
			}}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{}, errors.New("ответ не опубликован")
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err == nil {
		t.Fatalf("publish review responses must fail")
	}
	if len(calls) != 1 || calls[0].Operation != "reply" {
		t.Fatalf("failed inline response must not trigger thread resolution: %#v", calls)
	}
}

func TestPublishReviewResponsesRejectsUnknownTypeWithoutExternalPublication(t *testing.T) {
	t.Parallel()

	operation := publishReviewResponsesOperationSpec()
	state := &operationExecution{
		data: map[string]any{
			"invocation": model.Invocation{Assignment: &ExecutionAssignment{RelatedObjects: []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 17}}}},
			"result":     model.LaunchResult{Status: "completed"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{
				{RemarkID: "unknown-1", Type: "unexpected", Status: "fixed", Summary: "Не публиковать."},
				{RemarkID: "remark-1", Type: "inline", ThreadID: "thread-1", Status: "resolved", Summary: "Исправлено."},
			}},
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
	var calls []integration.Request
	service := &Service{logger: log.Default(), integrations: &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		calls = append(calls, req)
		return integration.Response{Status: "ok"}, nil
	}}}

	if err := (builtinOperationExecutor{service: service}).publishReviewResponses(context.Background(), state, operation, OperationKindPublishReviewResponses); err == nil {
		t.Fatalf("unknown response type must fail")
	}
	if len(calls) != 0 {
		t.Fatalf("unknown response type must not be published externally: %#v", calls)
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
			"profile":    model.Profile{Name: "coder"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "apply complete"},
			"structured_output": &model.StructuredOutput{ReviewResponses: []model.StructuredResponse{{
				RemarkID: "remark-1",
				ThreadID: "thread-1",
				Status:   "resolved",
				Summary:  "Проверка добавлена.",
				Body:     "Добавил покрытие отказа.",
			}}},
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
	if result == nil || result.Status != OperationStatusFailed || result.Failure == nil || result.Failure.Code != "review_responses_partial_failure" {
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
			"profile":           model.Profile{Name: "coder"},
			"allocation":        model.Allocation{Model: "gpt-5"},
			"workplace":         model.Workplace{Name: "/tmp/work", Ready: true},
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
		Name: "unknown-operation",
		Kind: model.OperationKind("unknown-operation"),

		Required: true,
		In: model.OperationMap{
			"invocation": {Ref: "data.invocation"},
			"profile":    {Ref: "data.profile"},
			"allocation": {Ref: "data.allocation"},
			"workplace":  {Ref: "data.workplace"},
		},
		Out: model.OperationMap{"result": {Ref: "data.result"}},
	}
	state := &operationExecution{
		in: model.Invocation{Task: "legacy"},
		action: model.Action{
			Operations: []model.OperationSpec{operation},
		},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
			"profile":    model.Profile{Name: "coder"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
		},
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
	if state.in.Task != "legacy" {
		t.Fatalf("unsupported operation must not write implicit state invocation: %#v", state.in)
	}
}

func TestExecutionDataSnapshotUsesOnlyActionData(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		in:         model.Invocation{Task: "legacy"},
		profile:    model.Profile{Name: "legacy"},
		allocation: model.Allocation{Model: "legacy"},
		workplace:  model.Workplace{Name: "legacy"},
		result:     model.LaunchResult{Status: "legacy", Summary: "legacy"},
		data: map[string]any{
			"invocation": model.Invocation{Task: "task-42"},
			"profile":    model.Profile{Name: "coder"},
			"allocation": model.Allocation{Model: "gpt-5"},
			"workplace":  model.Workplace{Name: "/tmp/work", Ready: true},
			"result":     model.LaunchResult{Status: "completed", Summary: "data"},
		},
	}

	snapshot := executionDataFromState(state)
	if snapshot.invocation.Task != "task-42" || snapshot.profile.Name != "coder" || snapshot.allocation.Model != "gpt-5" || snapshot.workplace.Name != "/tmp/work" || snapshot.result.Summary != "data" {
		t.Fatalf("execution snapshot must use only action data: %#v", snapshot)
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
				Operations:        testExecutionOperations(OperationKindFinalize),
			},
			{
				Name:              "implement",
				Class:             ActionClassService,
				Profile:           "local",
				RequiresWorkplace: boolRef(false),
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
				Operations:        testExecutionOperations(OperationKindFinalize),
			},
			{
				Name:              "local-implementation",
				Class:             ActionClassService,
				Profile:           "local",
				Aliases:           []string{"implement"},
				RequiresWorkplace: boolRef(false),
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
						"invocation": mappingRef("data.invocation"),
						"profile":    mappingRef("data.profile"),
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
						"invocation": mappingRef("data.invocation"),
						"allocation": mappingRef("data.allocation"),
					},
					Out: map[string]methodology.ActionMapping{
						"directive": mappingRef("data.directive"),
					},
				},
				{
					Name: OperationKindLaunchSynthesis,
					In: map[string]methodology.ActionMapping{
						"invocation": mappingRef("data.invocation"),
						"directive":  mappingRef("data.directive"),
						"profile":    mappingRef("data.profile"),
						"allocation": mappingRef("data.allocation"),
						"workplace":  mappingRef("data.workplace"),
					},
					Out: map[string]methodology.ActionMapping{
						"result": mappingRef("data.result"),
					},
				},
				{
					Name: OperationKindParseResult,
					In: map[string]methodology.ActionMapping{
						"result": mappingRef("data.result"),
					},
					Out: map[string]methodology.ActionMapping{
						"structured_output": mappingRef("data.structured_output"),
					},
				},
				{
					Name: OperationKindCommitPush,
					In: map[string]methodology.ActionMapping{
						"directory":      mappingRef("data.workplace.name"),
						"commit_message": mappingRef("data.structured_output.commit_message"),
						"fallback_name":  mappingRef("data.invocation.workplace.name"),
						"git":            mappingRef("data.allocation.git"),
						"private_store":  mappingRef("data.allocation.private_store"),
						"config_home":    mappingRef("data.allocation.config_home"),
					},
					Out: map[string]methodology.ActionMapping{
						"commit_summary": mappingRef("data.commit_summary"),
					},
				},
				{Name: OperationKindPublishReviewResponses},
				{Name: OperationKindFinalize},
			},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindPrepareData, Kind: OperationKindPrepareData, Title: "Подготовка данных", Required: boolRef(true)},
			{Name: OperationKindLoadPullRequest, Kind: OperationKindLoadPullRequest, Title: "Получение запроса на слияние", Required: boolRef(true)},
			{Name: OperationKindLoadReviewRemarks, Kind: OperationKindLoadReviewRemarks, Title: "Получение замечаний ревизии"},
			{Name: OperationKindResolveProfile, Kind: OperationKindResolveProfile, Title: "Выбор исполнительного профиля", Required: boolRef(true)},
			{Name: OperationKindAllocateResources, Kind: OperationKindAllocateResources, Title: "Ресурсное снабжение", Required: boolRef(true)},
			{Name: OperationKindPrepareWorkplace, Kind: OperationKindPrepareWorkplace, Title: "Подготовка рабочего места", Required: boolRef(true)},
			{Name: OperationKindBuildDirective, Kind: OperationKindBuildDirective, Title: "Сборка исполнительной директивы", Required: boolRef(true)},
			{Name: OperationKindLaunchSynthesis, Kind: OperationKindLaunchSynthesis, Title: "Запуск синтеза", Required: boolRef(true)},
			{Name: OperationKindParseResult, Kind: OperationKindParseResult, Title: "Разбор результата", Required: boolRef(true)},
			{Name: OperationKindCommitPush, Kind: OperationKindCommitPush, Title: "Создание коммита и отправка ветки", Required: boolRef(true)},
			{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses, Title: "Запись ответов на замечания", Required: boolRef(true)},
			{Name: OperationKindFinalize, Kind: OperationKindFinalize, Title: "Завершающая фиксация", Required: boolRef(true)},
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
	if allocationOperation.In["profile"].Ref != "data.profile" || allocationOperation.Out["allocation"].Ref != "data.allocation" {
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
	if directiveOperation.In["allocation"].Ref != "data.allocation" || directiveOperation.Out["directive"].Ref != "data.directive" {
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
	if commitOperation.In["commit_message"].Ref != "data.structured_output.commit_message" || commitOperation.In["directory"].Ref != "data.workplace.name" || commitOperation.In["git"].Ref != "data.allocation.git" || commitOperation.Out["commit_summary"].Ref != "data.commit_summary" || len(commitOperation.Out) != 1 {
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
			{Name: OperationKindPublishMergeRequest, Kind: OperationKindPublishMergeRequest, Title: "Открытие запроса на слияние", Required: boolRef(true)},
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
			{Name: OperationKindLoadPullRequest, Kind: OperationKindLoadPullRequest, Title: "Получение запроса на слияние", Required: boolRef(true)},
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

func TestProjectReviewActionsLoadRelatedPullRequestFromPublicInput(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	for _, actionName := range []string{ActionReviewPullRequest, ActionApplyReviewComments} {
		t.Run(actionName, func(t *testing.T) {
			resolver := newMethodologyActionResolver()
			resolver.getwd = func() (string, error) { return repoRoot, nil }
			integrations := &stubIntegrationExecutor{execute: func(_ context.Context, request integration.Request) (integration.Response, error) {
				switch request.Operation {
				case "get":
					if request.Repository != "owner/name" || request.MergeRequestNumber != 184 {
						t.Fatalf("unexpected pull request request: %#v", request)
					}
					return integration.Response{MergeRequest: &integration.MergeRequest{
						Repository: "owner/name",
						Number:     184,
						BaseRef:    "main",
						HeadRef:    "167",
						URL:        "https://github.com/owner/name/pull/184",
					}}, nil
				case "list":
					return integration.Response{}, nil
				default:
					t.Fatalf("unexpected integration request: %#v", request)
					return integration.Response{}, nil
				}
			}}
			workplaceRoot := t.TempDir()
			service := &Service{
				logger:       log.Default(),
				actions:      resolver,
				profiles:     &stubProfileResolver{profile: model.Profile{Name: "review", Mode: "manual", ModelBinding: "review"}},
				resources:    &stubResourceProvider{allocation: model.Allocation{Resource: "binding:review", Reserved: true, Runner: "codex", Model: "openai/gpt-5.6-sol", ModelBinding: "review"}},
				workplaces:   &stubWorkplaceManager{workplace: model.Workplace{Name: workplaceRoot, Ready: true}},
				launcher:     &stubLauncher{result: model.LaunchResult{Status: "completed", StructuredOutput: &model.StructuredOutput{Summary: "Операция выполнена.", CommitMessage: "Проверить передачу запроса"}}},
				integrations: integrations,
			}

			_, err := service.ExecuteAction(context.Background(), ActionInvocation{Assignment: &ExecutionAssignment{
				Action:          actionName,
				CanonicalTask:   &ObjectRef{Type: "task", Repository: "owner/name", Number: 167},
				RelatedObjects:  []ObjectRef{{Type: "merge-request", Repository: "owner/name", Number: 184}},
				StructuredInput: &StructuredInput{Task: "Обработать запрос на слияние."},
			}})
			if err != nil {
				t.Fatalf("execute project action: %v", err)
			}
			if len(integrations.calls) < 1 || integrations.calls[0].Operation != "get" || integrations.calls[0].MergeRequestNumber != 184 {
				t.Fatalf("first integration call must load related pull request: %#v", integrations.calls)
			}
		})
	}
}

func TestActionResolutionKeepsLoadReviewRemarksMapping(t *testing.T) {
	t.Parallel()

	action, err := resolveActionFromCatalog(methodology.Catalog{
		Actions: []methodology.Action{{
			Name:              ActionApplyReviewComments,
			Class:             ActionClassEngineeringSynthesis,
			RequiresWorkplace: boolRef(true),
			Operations: []methodology.ActionOperation{{
				Name: OperationKindLoadReviewRemarks,
				In: map[string]methodology.ActionMapping{
					"invocation":   mappingRef("data.invocation"),
					"pull_request": mappingRef("data.pull_request"),
				},
				Out: map[string]methodology.ActionMapping{
					"review_remarks": mappingRef("data.review_remarks"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindLoadReviewRemarks, Kind: OperationKindLoadReviewRemarks, Title: "Получение замечаний ревизии", Required: boolRef(true)},
		},
	}, invocation{Action: ActionApplyReviewComments})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindLoadReviewRemarks)
	if operation == nil {
		t.Fatalf("load-review-remarks operation must be present: %#v", action.Operations)
	}
	if operation.In["invocation"].Ref != "data.invocation" || operation.In["pull_request"].Ref != "data.pull_request" || operation.Out["review_remarks"].Ref != "data.review_remarks" || len(operation.Out) != 1 {
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
			Operations: []methodology.ActionOperation{{
				Name: OperationKindFinalize,
				In: map[string]methodology.ActionMapping{
					"action_name":  mappingRef("action.name"),
					"action_class": mappingRef("action.class"),
					"result":       mappingRef("data.result"),
				},
				Out: map[string]methodology.ActionMapping{
					"result": mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindFinalize, Kind: OperationKindFinalize, Title: "Завершающая фиксация", Required: boolRef(true)},
		},
	}, invocation{Action: ActionClassIntegrationChange})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindFinalize)
	if operation == nil {
		t.Fatalf("finalize operation must be present: %#v", action.Operations)
	}
	if operation.In["action_name"].Ref != "action.name" || operation.In["action_class"].Ref != "action.class" || operation.In["result"].Ref != "data.result" || operation.Out["result"].Ref != "data.result" {
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
			{Name: OperationKindPublishReviewRemarks, Kind: OperationKindPublishReviewRemarks, Title: "Запись замечаний ревизии", Required: boolRef(true)},
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
			Operations: []methodology.ActionOperation{{
				Name: OperationKindPublishReviewResponses,
				In: map[string]methodology.ActionMapping{
					"invocation":        mappingRef("data.invocation"),
					"result":            mappingRef("data.result"),
					"structured_output": mappingRef("data.structured_output"),
				},
				Out: map[string]methodology.ActionMapping{
					"review_responses_summary": mappingRef("data.review_responses_summary"),
					"result":                   mappingRef("data.result"),
				},
			}},
		}},
		Operations: []methodology.Operation{
			{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses, Title: "Запись ответов на замечания", Required: boolRef(true)},
		},
	}, invocation{Action: ActionApplyReviewComments})
	if err != nil {
		t.Fatalf("resolve action: %v", err)
	}
	operation := findOperationSpec(action, OperationKindPublishReviewResponses)
	if operation == nil {
		t.Fatalf("publish-review-responses operation must be present: %#v", action.Operations)
	}
	if operation.In["structured_output"].Ref != "data.structured_output" || operation.In["review_remarks"].Ref != "" || operation.Out["review_responses_summary"].Ref != "data.review_responses_summary" || operation.Out["result"].Ref != "data.result" {
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
	if launcher.commitInput.Git != gitConfig {
		t.Fatalf("commit-push operation must pass allocated git config: %#v", launcher.commitInput.Git)
	}
	if launcher.invocation.Launch.CommitPush {
		t.Fatalf("launch synthesis must not receive hidden commit-push flag: %#v", launcher.invocation.Launch)
	}
	commitOperation := findOperationResult(result.Operations, OperationKindCommitPush)
	if commitOperation == nil || commitOperation.Status != OperationStatusCompleted || commitOperation.Summary != "git=committed+pushed branch=task-97" {
		t.Fatalf("unexpected commit operation: %#v", commitOperation)
	}
	if result.Summary != "launch complete" {
		t.Fatalf("commit-push must not rewrite launch result summary: %q", result.Summary)
	}
}

func TestServiceExecuteRecordsCommitPushCancellationInHistory(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	launcher := &stubLauncher{
		result: model.LaunchResult{Status: "completed", Summary: "launch complete", StructuredOutput: &model.StructuredOutput{Summary: "Done.", CommitMessage: "Apply change"}},
		commit: func(context.Context, model.CommitPushInput) (string, error) {
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
			if req.ObjectType != "merge-request" {
				t.Fatalf("unexpected integration request: %#v", req)
			}
			if req.Operation == "search" {
				return integration.Response{}, nil
			}
			if req.Operation != "create" {
				t.Fatalf("unexpected integration operation: %#v", req)
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
	if len(integrations.calls) != 2 {
		t.Fatalf("expected existence check and publication requests, got %#v", integrations.calls)
	}
	request := integrations.calls[0]
	if request.Operation != "search" || request.Repository != "owner/name" || request.Query != "head:132" || request.State != "open" {
		t.Fatalf("unexpected pull request existence check request: %#v", request)
	}
	request = integrations.calls[1]
	if request.Operation != "create" || request.Repository != "owner/name" || request.Base != "develop" || request.Head != "132" || request.Title == "" {
		t.Fatalf("unexpected pull request publication request: %#v", request)
	}
	operation := findOperationResult(result.Operations, OperationKindPublishMergeRequest)
	if operation == nil || operation.Status != OperationStatusCompleted {
		t.Fatalf("pull request operation must be completed: %#v", result.Operations)
	}
	synthesis := findOperationResult(result.Operations, OperationKindStructuredSynthesis)
	if synthesis == nil || synthesis.Status != OperationStatusCompleted || len(synthesis.Operations) != 3 {
		t.Fatalf("structured synthesis must keep three nested operations: %#v", synthesis)
	}
	for index, name := range []string{OperationKindBuildPrompt, OperationKindLaunchSynthesis, OperationKindParseResult} {
		if synthesis.Operations[index].Name != name || synthesis.Operations[index].Status != OperationStatusCompleted {
			t.Fatalf("unexpected structured synthesis operation at %d: %#v", index, synthesis.Operations)
		}
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
			if req.Operation == "search" {
				return integration.Response{}, nil
			}
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
	if len(integrations.calls) != 2 || integrations.calls[0].Operation != "search" || integrations.calls[1].Operation != "create" {
		t.Fatalf("expected existence check and publication requests, got %#v", integrations.calls)
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
				return integration.Response{MergeRequest: &integration.MergeRequest{Repository: req.Repository, Number: req.MergeRequestNumber, State: "OPEN", BaseRef: "main", HeadRef: "feature/review"}}, nil
			case "list":
				return integration.Response{ReviewRemarks: []integration.ReviewRemark{{
					Repository:         req.Repository,
					MergeRequestNumber: req.MergeRequestNumber,
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
	if !strings.Contains(launcher.invocation.Launch.Prompt, "Провести ревизию") {
		t.Fatalf("structured input must be included in synthesis prompt: %q", launcher.invocation.Launch.Prompt)
	}
	if workplaces.invocation.Workplace.Name != "feature-review" || workplaces.invocation.Workplace.HeadRef != "feature/review" || workplaces.invocation.Workplace.BaseRef != "main" {
		t.Fatalf("review action must use pull request head for workplace: %#v", workplaces.invocation.Workplace)
	}
	if len(integrations.calls) != 6 {
		t.Fatalf("expected get, comments and create integration calls, got %#v", integrations.calls)
	}
	if integrations.calls[2].MergeRequestNumber != 17 || integrations.calls[2].Repository != "owner/name" {
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

func TestPublishPullRequestCommentsFallsBackForUnresolvedGitHubLine(t *testing.T) {
	const body = "## Замечание ревизии\n\nСтрока больше не входит в diff."
	var genericBody string
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			switch req.Operation {
			case "create":
				if req.Path != "" {
					return integration.Response{System: "github"}, errors.New(`GitHub API returned status 422: {"errors":[{"field":"pull_request_review_thread.line","message":"could not be resolved"}]}`)
				}
				genericBody = req.Body
				return integration.Response{Status: "ok"}, nil
			case "list":
				return integration.Response{System: "github"}, nil
			default:
				t.Fatalf("unexpected integration request: %#v", req)
				return integration.Response{}, nil
			}
		},
	}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{Body: body, Path: "internal/service.go", Line: 42, Side: "RIGHT"}})
	if err != nil || count != 1 {
		t.Fatalf("unresolved line must be published as a pull request comment, count=%d err=%v", count, err)
	}
	if !strings.Contains(genericBody, "Исходная позиция встроенного замечания: `internal/service.go:42:RIGHT`") {
		t.Fatalf("fallback comment must preserve inline location: %q", genericBody)
	}

	integrations.execute = func(_ context.Context, req integration.Request) (integration.Response, error) {
		if req.Operation == "create" && req.Path != "" {
			return integration.Response{System: "github"}, errors.New(`GitHub API returned status 422: {"errors":[{"field":"pull_request_review_thread.line","message":"could not be resolved"}]}`)
		}
		if req.Operation == "list" {
			return integration.Response{ReviewRemarks: []integration.ReviewRemark{{Body: genericBody}}}, nil
		}
		t.Fatalf("duplicate fallback must not create another comment: %#v", req)
		return integration.Response{}, nil
	}
	count, err = executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{Body: body, Path: "internal/service.go", Line: 42, Side: "RIGHT"}})
	if err != nil || count != 1 {
		t.Fatalf("existing fallback comment must be reused, count=%d err=%v", count, err)
	}
}

func TestPublishPullRequestCommentsDoesNotFallbackForOtherGitHub422(t *testing.T) {
	integrations := &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		return integration.Response{System: "github"}, errors.New(`GitHub API returned status 422: {"message":"validation failed"}`)
	}}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{Body: "remark", Path: "file.go", Line: 12, Side: "RIGHT"}})
	if err == nil || count != 0 {
		t.Fatalf("other 422 must remain an error, count=%d err=%v", count, err)
	}
	if len(integrations.calls) != 1 {
		t.Fatalf("other 422 must not trigger fallback requests: %#v", integrations.calls)
	}
}

func TestPublishPullRequestCommentsRecreatesRemarkAfterStaleThread(t *testing.T) {
	integrations := &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		switch req.Operation {
		case "reply":
			return integration.Response{System: "github"}, errors.New("GitHub GraphQL returned errors: Could not resolve to a node with the global id of 'PRRT_old'")
		case "create":
			if req.ThreadID != "" || req.ExternalID != "" || req.Path != "internal/service.go" || req.Line != 42 {
				t.Fatalf("stale thread must be replaced with a current inline comment: %#v", req)
			}
			return integration.Response{Status: "ok"}, nil
		default:
			t.Fatalf("unexpected integration request: %#v", req)
			return integration.Response{}, nil
		}
	}}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{
		Body:       "## Замечание ревизии\n\nИсправление проверено.",
		Path:       "internal/service.go",
		Line:       42,
		Side:       "RIGHT",
		ThreadID:   "PRRT_old",
		ExternalID: "PRRC_old",
	}})
	if err != nil || count != 1 {
		t.Fatalf("stale thread must not stop publication, count=%d err=%v", count, err)
	}
}

func TestPublishPullRequestCommentsSkipsStaleThreadStateUpdate(t *testing.T) {
	integrations := &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		return integration.Response{System: "github"}, errors.New("GitHub GraphQL returned errors: Could not resolve to a node with the global id of 'PRRT_old'")
	}}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	updated, err := executor.updateReviewRemarkThreads(context.Background(), []reviewRemarkComment{{ThreadID: "PRRT_old", Status: "resolved"}})
	if err != nil || updated != 0 || len(integrations.calls) != 1 {
		t.Fatalf("stale thread state update must be skipped, updated=%d calls=%#v err=%v", updated, integrations.calls, err)
	}
}

func TestPublishPullRequestCommentsDoesNotDuplicateStaleThreadFallback(t *testing.T) {
	const body = "## Замечание ревизии\n\nПозиция больше недоступна."
	createCalls := 0
	integrations := &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		switch req.Operation {
		case "reply":
			return integration.Response{System: "github"}, errors.New("GitHub GraphQL returned errors: Could not resolve to a node with the global id of 'PRRT_old'")
		case "list":
			if createCalls == 0 {
				return integration.Response{}, nil
			}
			return integration.Response{ReviewRemarks: []integration.ReviewRemark{{Body: body}}}, nil
		case "create":
			createCalls++
			return integration.Response{Status: "ok"}, nil
		default:
			t.Fatalf("unexpected integration request: %#v", req)
			return integration.Response{}, nil
		}
	}}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}
	comment := []reviewRemarkComment{{Body: body, ThreadID: "PRRT_old", ExternalID: "PRRC_old"}}

	for i := 0; i < 2; i++ {
		count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, comment)
		if err != nil || count != 1 {
			t.Fatalf("stale thread fallback run %d failed, count=%d err=%v", i+1, count, err)
		}
	}
	if createCalls != 1 {
		t.Fatalf("stale thread fallback must be idempotent, create calls=%d", createCalls)
	}
}

func TestPublishPullRequestCommentsDoesNotFallbackForOtherGraphQLError(t *testing.T) {
	integrations := &stubIntegrationExecutor{execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
		if req.Operation != "reply" {
			t.Fatalf("other GraphQL errors must not trigger fallback: %#v", req)
		}
		return integration.Response{System: "github"}, errors.New("GitHub authentication failed: Could not resolve to a node")
	}}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{
		Body:     "remark",
		Path:     "file.go",
		Line:     12,
		ThreadID: "PRRT_old",
	}})
	if err == nil || count != 0 {
		t.Fatalf("other GraphQL errors must remain errors, count=%d err=%v", count, err)
	}
}

func TestPublishPullRequestCommentsContinuesExistingReviewThread(t *testing.T) {
	integrations := &stubIntegrationExecutor{}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{
		Body:       "## Замечание ревизии\n\nПроверка после исправления.",
		ExternalID: "PRRC_comment-1",
		ThreadID:   "PRRT_thread-1",
	}})
	if err != nil {
		t.Fatalf("publish review thread continuation: %v", err)
	}
	if count != 1 || len(integrations.calls) != 1 {
		t.Fatalf("expected one published continuation, count=%d calls=%#v", count, integrations.calls)
	}
	request := integrations.calls[0]
	if request.Operation != "reply" || request.ExternalID != "PRRC_comment-1" || request.ThreadID != "PRRT_thread-1" {
		t.Fatalf("review thread identifiers must be preserved: %#v", request)
	}
}

func TestPublishPullRequestCommentsRestoresSwappedReviewIdentifiers(t *testing.T) {
	integrations := &stubIntegrationExecutor{}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{
		Body:       "## Замечание ревизии\n\nОтвет на замечание.",
		ExternalID: "PRRT_thread-1",
		ThreadID:   "PRRC_comment-1",
	}})
	if err != nil {
		t.Fatalf("publish review thread continuation: %v", err)
	}
	if count != 1 || len(integrations.calls) != 1 {
		t.Fatalf("expected one published continuation, count=%d calls=%#v", count, integrations.calls)
	}
	request := integrations.calls[0]
	if request.Operation != "reply" || request.ExternalID != "PRRC_comment-1" || request.ThreadID != "PRRT_thread-1" {
		t.Fatalf("swapped review identifiers must be restored before publication: %#v", request)
	}
}

func TestReviewRemarkCommentsPreservesExternalIdentifiers(t *testing.T) {
	comments := reviewRemarkComments(&model.StructuredOutput{Remarks: []model.StructuredRemark{{
		ID:         "remark-2-follow-up",
		ExternalID: "PRRC_comment-1",
		ThreadID:   "PRRT_thread-1",
		Status:     "open",
		Body:       "Проверка после исправления.",
	}}})
	if len(comments) != 1 || comments[0].ExternalID != "PRRC_comment-1" || comments[0].ThreadID != "PRRT_thread-1" {
		t.Fatalf("review remark external identifiers were lost: %#v", comments)
	}
	if got := reviewRemarkCommentOperation(comments[0]); got != "reply" {
		t.Fatalf("existing review thread must be published as reply, got %q", got)
	}
	if !strings.Contains(comments[0].Body, "Состояние: open") {
		t.Fatalf("review remark state must be preserved in published body: %q", comments[0].Body)
	}
}

func TestPublishPullRequestCommentsDoesNotUseReviewCommentIDAsThread(t *testing.T) {
	integrations := &stubIntegrationExecutor{}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	count, err := executor.publishPullRequestComments(context.Background(), &operationExecution{}, pullRequestRef{Repository: "owner/name", Number: 17}, []reviewRemarkComment{{
		Body:       "## Замечание ревизии\n\nНовое замечание.",
		Path:       "internal/service.go",
		Line:       42,
		ExternalID: "PRRC_comment-1",
		ThreadID:   "PRRC_comment-1",
	}})
	if err != nil {
		t.Fatalf("publish review remark: %v", err)
	}
	if count != 1 || len(integrations.calls) != 1 {
		t.Fatalf("expected one new remark, count=%d calls=%#v", count, integrations.calls)
	}
	request := integrations.calls[0]
	if request.Operation != "create" || request.ThreadID != "" || request.ExternalID != "" {
		t.Fatalf("review comment identifier must not be sent as thread: %#v", request)
	}
}

func TestUpdateReviewRemarkThreadsReopensExistingChain(t *testing.T) {
	integrations := &stubIntegrationExecutor{}
	executor := builtinOperationExecutor{service: &Service{integrations: integrations}}

	updated, err := executor.updateReviewRemarkThreads(context.Background(), []reviewRemarkComment{{
		ThreadID: "PRRT_thread-1",
		Status:   "open",
	}})
	if err != nil {
		t.Fatalf("update review thread state: %v", err)
	}
	if updated != 1 || len(integrations.calls) != 1 {
		t.Fatalf("expected one thread state update, updated=%d calls=%#v", updated, integrations.calls)
	}
	request := integrations.calls[0]
	if request.Operation != "unresolve" || request.ThreadID != "PRRT_thread-1" || request.ExternalID != "PRRT_thread-1" {
		t.Fatalf("open remark must reopen its existing thread: %#v", request)
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
				return integration.Response{MergeRequest: &integration.MergeRequest{Repository: req.Repository, Number: req.MergeRequestNumber, State: "OPEN", BaseRef: "main", HeadRef: "112"}}, nil
			case "list":
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
	if !strings.Contains(launcher.invocation.Launch.Prompt, "Провести ревизию") {
		t.Fatalf("review synthesis must proceed with prepared prompt: %q", launcher.invocation.Launch.Prompt)
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
					ThreadID: "thread-1",
					Status:   "resolved",
					Summary:  "Исправлено.",
				}, {
					RemarkID: "thread-2",
					ThreadID: "thread-2",
					Status:   "open",
					Summary:  "Требуется повторная проверка.",
				}},
			},
		},
		commitSummary: "git=committed+pushed branch=feature/fixes",
	}
	integrations := &stubIntegrationExecutor{
		execute: func(_ context.Context, req integration.Request) (integration.Response, error) {
			switch req.Operation {
			case "get":
				return integration.Response{MergeRequest: &integration.MergeRequest{Repository: req.Repository, Number: req.MergeRequestNumber, State: "OPEN", BaseRef: "main", HeadRef: "feature/fixes"}}, nil
			case "list":
				return integration.Response{ReviewRemarks: []integration.ReviewRemark{{
					Repository:         req.Repository,
					MergeRequestNumber: req.MergeRequestNumber,
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
			case "unresolve":
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
	if !strings.Contains(launcher.invocation.Launch.Prompt, "Исправить замечания") {
		t.Fatalf("structured input must be passed into synthesis prompt: %q", launcher.invocation.Launch.Prompt)
	}
	if !strings.Contains(launcher.invocation.Launch.Prompt, `"external_id":"thread-1"`) || !strings.Contains(launcher.invocation.Launch.Prompt, `"thread_id":"thread-1"`) {
		t.Fatalf("canonical review remark identifiers must be passed into synthesis prompt: %q", launcher.invocation.Launch.Prompt)
	}
	if strings.Contains(launcher.invocation.Launch.Prompt, `"ReplyToID"`) || strings.Contains(launcher.invocation.Launch.Prompt, `"ExternalID"`) {
		t.Fatalf("integration-specific review remark fields must not be passed into synthesis prompt: %q", launcher.invocation.Launch.Prompt)
	}
	if !launcher.commitCalled {
		t.Fatal("review rework action must push fixes before publishing responses")
	}
	if workplaces.invocation.Workplace.Name != "feature-fixes" || workplaces.invocation.Workplace.HeadRef != "feature/fixes" || workplaces.invocation.Workplace.BaseRef != "main" {
		t.Fatalf("review rework action must use pull request head for workplace: %#v", workplaces.invocation.Workplace)
	}
	if len(integrations.calls) != 6 {
		t.Fatalf("expected get, comments, two replies and two thread state changes, got %#v", integrations.calls)
	}
	if integrations.calls[2].Operation != "reply" || integrations.calls[2].ThreadID != "thread-1" || integrations.calls[3].Operation != "reply" || integrations.calls[3].ThreadID != "thread-2" || integrations.calls[4].Operation != "resolve" || integrations.calls[4].ThreadID != "thread-1" || integrations.calls[5].Operation != "unresolve" || integrations.calls[5].ThreadID != "thread-2" {
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
	invocation    model.Invocation
	profile       model.Profile
	allocation    model.Allocation
	workplace     model.Workplace
	result        model.LaunchResult
	err           error
	beforeReturn  func()
	commitCalled  bool
	commitInput   model.CommitPushInput
	commitSummary string
	commitErr     error
	commit        func(context.Context, model.CommitPushInput) (string, error)
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

func (s *stubLauncher) CommitAndPush(ctx context.Context, input model.CommitPushInput) (string, error) {
	s.commitCalled = true
	s.commitInput = input
	if s.commit != nil {
		return s.commit(ctx, input)
	}
	if s.commitErr != nil {
		return "", s.commitErr
	}
	return s.commitSummary, nil
}

type stubActionResolver struct {
	action  model.Action
	actions map[string]model.Action
	err     error
}

func (s *stubActionResolver) ResolveAction(_ context.Context, in model.Invocation) (model.Action, error) {
	if s.err != nil {
		return model.Action{}, s.err
	}
	if action, ok := s.actions[actionNameFromInvocation(in)]; ok {
		return action, nil
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
	payload, err := json.Marshal(testExecutionMethodologyCatalog())
	if err != nil {
		t.Fatalf("marshal methodology catalog: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "catalog.json"), payload, 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}
}

func testExecutionMethodologyCatalog() methodology.Catalog {
	return methodology.Catalog{
		Actions: []methodology.Action{
			{Name: ActionClassEngineeringSynthesis, Class: ActionClassEngineeringSynthesis, Profile: "default", StructuredOutputFields: testStructuredOutputFields(), Aliases: []string{"implement"}, RequiresWorkplace: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildPrompt, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindFinalize)},
			{Name: "engineering-synthesis-commit", Class: ActionClassEngineeringSynthesis, Profile: "default", StructuredOutputFields: testStructuredOutputFields(), Aliases: []string{"implement-commit"}, RequiresWorkplace: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildPrompt, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindCommitPush, OperationKindFinalize)},
			{Name: ActionStartImplementationPR, Class: ActionClassEngineeringSynthesis, Profile: "coder", StructuredOutputFields: testStructuredOutputFields(), RequiresWorkplace: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindStructuredSynthesis, OperationKindCommitPush, OperationKindPublishMergeRequest, OperationKindFinalize)},
			structuredSynthesisMethodologyAction(),
			{Name: ActionClassReview, Class: ActionClassReview, Profile: "review", StructuredOutputFields: testStructuredOutputFields(), RequiresWorkplace: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildPrompt, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindFinalize)},
			{Name: ActionReviewPullRequest, Class: ActionClassReview, Profile: "review", StructuredOutputFields: testStructuredOutputFields(), RequiresWorkplace: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindLoadPullRequest, optionalExecutionOperation(OperationKindLoadReviewRemarks), OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildPrompt, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindPublishReviewRemarks, OperationKindFinalize)},
			{Name: ActionApplyReviewComments, Class: ActionClassEngineeringSynthesis, Profile: "coder", StructuredOutputFields: testStructuredOutputFields(), RequiresWorkplace: boolRef(true), Operations: testExecutionOperations(OperationKindPrepareData, OperationKindLoadPullRequest, OperationKindLoadReviewRemarks, OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace, OperationKindBuildPrompt, OperationKindLaunchSynthesis, OperationKindParseResult, OperationKindCommitPush, OperationKindPublishReviewResponses, OperationKindFinalize)},
			{Name: ActionClassIntegrationChange, Class: ActionClassIntegrationChange, Profile: "default", RequiresWorkplace: boolRef(false), Operations: testExecutionOperations(OperationKindFinalize)},
		},
		Operations: testExecutionOperationRegistry(),
	}
}

func testStructuredOutputFields() []string {
	return []string{"summary", "commit_message", "remarks", "review_responses", "questions", "follow_up_actions", "changes", "commands", "conclusion", "extensions"}
}

func structuredSynthesisMethodologyAction() methodology.Action {
	required := boolRef(true)
	return methodology.Action{
		Name:              OperationKindStructuredSynthesis,
		Class:             ActionClassEngineeringSynthesis,
		RequiresWorkplace: boolRef(false),
		Contract: methodology.ActionContract{
			In: map[string]methodology.ActionContractField{
				"prompt":                   {Type: "string"},
				"prompt_additions":         {Type: "string_array"},
				"structured_input":         {Type: "object", Required: required},
				"structured_output_fields": {Type: "string_array", Required: required},
				"review_remarks":           {Type: "object_array"},
				"directory":                {Type: "string", Required: required},
				"runner":                   {Type: "string", Required: required},
				"model":                    {Type: "string", Required: required},
				"reasoning_effort":         {Type: "string"},
				"resume_session_id":        {Type: "string"},
			},
			Data: map[string]methodology.ActionContractField{
				"prompt":            {Type: "string"},
				"raw_output":        {Type: "string"},
				"session_id":        {Type: "string"},
				"result":            {Type: "object"},
				"structured_output": {Type: "object"},
			},
			Out: map[string]methodology.ActionContractField{
				"result":            {Type: "object", Required: required},
				"structured_output": {Type: "object", Required: required},
			},
		},
		Operations: []methodology.ActionOperation{
			structuredSynthesisBuildPromptOperation(),
			structuredSynthesisLaunchOperation(),
			structuredSynthesisParseOperation(),
		},
	}
}

func testExecutionOperationRegistry() []methodology.Operation {
	names := []string{
		OperationKindPrepareData, OperationKindLoadPullRequest, OperationKindLoadReviewRemarks,
		OperationKindResolveProfile, OperationKindAllocateResources, OperationKindPrepareWorkplace,
		OperationKindBuildDirective, OperationKindBuildPrompt, OperationKindLaunchSynthesis,
		OperationKindParseResult, OperationKindCommitPush, OperationKindPublishMergeRequest,
		OperationKindPublishReviewRemarks, OperationKindPublishReviewResponses, OperationKindFinalize,
	}
	result := make([]methodology.Operation, 0, len(names)+1)
	for _, name := range names {
		result = append(result, methodology.Operation{Name: name, Type: OperationTypeBuiltin, Kind: name, Required: boolRef(true)})
	}
	required := boolRef(true)
	result = append(result, methodology.Operation{
		Name: OperationKindStructuredSynthesis, Type: OperationTypeAction, Kind: OperationKindStructuredSynthesis, Required: required,
		Contract: methodology.OperationContract{
			In: map[string]methodology.OperationContractField{
				"prompt":                   {Type: "string"},
				"prompt_additions":         {Type: "string_array"},
				"structured_input":         {Type: "object", Required: required},
				"structured_output_fields": {Type: "string_array", Required: required},
				"review_remarks":           {Type: "object_array"},
				"directory":                {Type: "string", Required: required},
				"runner":                   {Type: "string", Required: required},
				"model":                    {Type: "string", Required: required},
				"reasoning_effort":         {Type: "string"},
				"resume_session_id":        {Type: "string"},
			},
			Out: map[string]methodology.OperationContractField{
				"result":            {Type: "object", Required: required},
				"structured_output": {Type: "object", Required: required},
			},
		},
	})
	return result
}

func testExecutionOperations(operations ...any) []methodology.ActionOperation {
	result := make([]methodology.ActionOperation, 0, len(operations))
	for _, value := range operations {
		switch operation := value.(type) {
		case string:
			if operation == OperationKindPrepareData {
				result = append(result, prepareDataActionOperation())
				continue
			}
			if operation == OperationKindResolveProfile {
				result = append(result, resolveProfileActionOperation())
				continue
			}
			if operation == OperationKindAllocateResources {
				result = append(result, allocateResourcesActionOperation())
				continue
			}
			if operation == OperationKindPrepareWorkplace {
				result = append(result, prepareWorkplaceActionOperation())
				continue
			}
			if operation == OperationKindLoadPullRequest {
				result = append(result, loadPullRequestActionOperation())
				continue
			}
			if operation == OperationKindLoadReviewRemarks {
				result = append(result, loadReviewRemarksActionOperation())
				continue
			}
			if operation == OperationKindBuildDirective {
				result = append(result, buildDirectiveActionOperation())
				continue
			}
			if operation == OperationKindBuildPrompt {
				result = append(result, buildPromptActionOperation())
				continue
			}
			if operation == OperationKindStructuredSynthesis {
				result = append(result, structuredSynthesisActionOperation())
				continue
			}
			if operation == OperationKindLaunchSynthesis {
				result = append(result, launchSynthesisActionOperation())
				continue
			}
			if operation == OperationKindParseResult {
				result = append(result, parseResultActionOperation())
				continue
			}
			if operation == OperationKindCommitPush {
				result = append(result, commitPushActionOperation())
				continue
			}
			if operation == OperationKindPublishMergeRequest {
				result = append(result, publishMergeRequestActionOperation())
				continue
			}
			if operation == OperationKindPublishReviewRemarks {
				result = append(result, publishReviewRemarksActionOperation())
				continue
			}
			if operation == OperationKindPublishReviewResponses {
				result = append(result, publishReviewResponsesActionOperation())
				continue
			}
			if operation == OperationKindFinalize {
				result = append(result, finalizeActionOperation())
				continue
			}
			result = append(result, methodology.ActionOperation{Name: operation, Required: boolRef(true)})
		case methodology.ActionOperation:
			result = append(result, operation)
		}
	}
	return result
}

func prepareDataActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindPrepareData,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation":       mappingRef("in.invocation"),
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
	}
}

func resolveProfileActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindResolveProfile,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"profile_name": mappingRef("action.profile"),
			"invocation":   mappingRef("data.invocation"),
		},
		Out: map[string]methodology.ActionMapping{
			"profile": mappingRef("data.profile"),
			"result":  mappingRef("data.result"),
		},
	}
}

func allocateResourcesActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindAllocateResources,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation": mappingRef("data.invocation"),
			"profile":    mappingRef("data.profile"),
		},
		Out: map[string]methodology.ActionMapping{
			"allocation": mappingRef("data.allocation"),
		},
	}
}

func prepareWorkplaceActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindPrepareWorkplace,

		Required: boolRef(true),
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
	}
}

func loadPullRequestActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindLoadPullRequest,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation": mappingRef("data.invocation"),
		},
		Out: map[string]methodology.ActionMapping{
			"pull_request": mappingRef("data.pull_request"),
			"invocation":   mappingRef("data.invocation"),
			"result":       mappingRef("data.result"),
		},
	}
}

func loadReviewRemarksActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindLoadReviewRemarks,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation":   mappingRef("data.invocation"),
			"pull_request": mappingRef("data.pull_request"),
		},
		Out: map[string]methodology.ActionMapping{
			"review_remarks": mappingRef("data.review_remarks"),
		},
	}
}

func buildDirectiveActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindBuildDirective,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation":               mappingRef("data.invocation"),
			"profile":                  mappingRef("data.profile"),
			"allocation":               mappingRef("data.allocation"),
			"workplace":                mappingRef("data.workplace"),
			"structured_output_fields": mappingRef("action.structured_output_fields"),
		},
		Out: map[string]methodology.ActionMapping{
			"directive": mappingRef("data.directive"),
		},
	}
}

func buildPromptActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindBuildPrompt, Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"prompt_additions":           mappingRef("data.profile.prompt_additions"),
			"structured_output":          mappingRef("data.profile.structured_output"),
			"structured_output_required": mappingRef("data.profile.structured_output_required"),
			"structured_output_fields":   mappingRef("action.structured_output_fields"),
			"structured_input":           mappingRef("data.invocation.launch.structured_input"),
			"review_remarks":             mappingRef("data.review_remarks"),
		},
		Out: map[string]methodology.ActionMapping{"prompt": mappingRef("data.prompt")},
	}
}

func structuredSynthesisActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name:     OperationKindStructuredSynthesis,
		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"prompt":                   mappingRef("in.launch.prompt"),
			"prompt_additions":         mappingRef("data.profile.prompt_additions"),
			"structured_input":         mappingRef("in.structured_input"),
			"structured_output_fields": mappingRef("action.structured_output_fields"),
			"directory":                mappingRef("data.workplace.name"),
			"runner":                   mappingRef("data.allocation.runner"),
			"model":                    mappingRef("data.allocation.model"),
			"reasoning_effort":         mappingRef("data.allocation.reasoning_effort"),
			"resume_session_id":        mappingRef("in.launch.resume.runner_session_id"),
		},
		Out: map[string]methodology.ActionMapping{
			"result":            mappingRef("data.result"),
			"structured_output": mappingRef("data.structured_output"),
		},
	}
}

func structuredSynthesisBuildPromptOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name:     OperationKindBuildPrompt,
		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"prompt":                     mappingRef("in.prompt"),
			"prompt_additions":           mappingRef("in.prompt_additions"),
			"structured_output":          mappingValue(true),
			"structured_output_required": mappingValue(true),
			"structured_output_fields":   mappingRef("in.structured_output_fields"),
			"structured_input":           mappingRef("in.structured_input"),
			"review_remarks":             mappingRef("in.review_remarks"),
		},
		Out: map[string]methodology.ActionMapping{"prompt": mappingRef("data.prompt")},
	}
}

func structuredSynthesisLaunchOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name:     OperationKindLaunchSynthesis,
		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"prompt":            mappingRef("data.prompt"),
			"directory":         mappingRef("in.directory"),
			"runner":            mappingRef("in.runner"),
			"model":             mappingRef("in.model"),
			"reasoning_effort":  mappingRef("in.reasoning_effort"),
			"resume_session_id": mappingRef("in.resume_session_id"),
		},
		Out: map[string]methodology.ActionMapping{
			"raw_output": mappingRef("data.raw_output"),
			"session_id": mappingRef("data.session_id"),
		},
	}
}

func structuredSynthesisParseOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name:     OperationKindParseResult,
		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"raw_output":               mappingRef("data.raw_output"),
			"session_id":               mappingRef("data.session_id"),
			"structured_output_fields": mappingRef("in.structured_output_fields"),
		},
		Out: map[string]methodology.ActionMapping{
			"result":            mappingRef("data.result"),
			"structured_output": mappingRef("data.structured_output"),
		},
	}
}

func launchSynthesisActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindLaunchSynthesis,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"prompt":           mappingRef("data.prompt"),
			"directory":        mappingRef("data.workplace.name"),
			"runner":           mappingRef("data.allocation.runner"),
			"model":            mappingRef("data.allocation.model"),
			"reasoning_effort": mappingRef("data.allocation.reasoning_effort"),
		},
		Out: map[string]methodology.ActionMapping{
			"raw_output": mappingRef("data.raw_output"),
			"session_id": mappingRef("data.session_id"),
		},
	}
}

func parseResultActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindParseResult,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"raw_output":               mappingRef("data.raw_output"),
			"session_id":               mappingRef("data.session_id"),
			"structured_output_fields": mappingRef("action.structured_output_fields"),
		},
		Out: map[string]methodology.ActionMapping{
			"structured_output": mappingRef("data.structured_output"),
			"result":            mappingRef("data.result"),
		},
	}
}

func commitPushActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindCommitPush,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"directory":      mappingRef("data.workplace.name"),
			"commit_message": mappingRef("data.structured_output.commit_message"),
			"fallback_name":  mappingRef("data.invocation.workplace.name"),
			"git":            mappingRef("data.allocation.git"),
			"private_store":  mappingRef("data.allocation.private_store"),
			"config_home":    mappingRef("data.allocation.config_home"),
		},
		Out: map[string]methodology.ActionMapping{
			"commit_summary": mappingRef("data.commit_summary"),
		},
	}
}

func publishMergeRequestActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindPublishMergeRequest,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation":        mappingRef("data.invocation"),
			"profile":           mappingRef("data.profile"),
			"allocation":        mappingRef("data.allocation"),
			"workplace":         mappingRef("data.workplace"),
			"result":            mappingRef("data.result"),
			"structured_output": mappingRef("data.structured_output"),
		},
		Out: map[string]methodology.ActionMapping{
			"merge_request":   mappingRef("data.merge_request"),
			"publish_summary": mappingRef("data.publish_summary"),
			"result":          mappingRef("data.result"),
		},
	}
}

func publishReviewRemarksActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindPublishReviewRemarks,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation":        mappingRef("data.invocation"),
			"profile":           mappingRef("data.profile"),
			"allocation":        mappingRef("data.allocation"),
			"workplace":         mappingRef("data.workplace"),
			"result":            mappingRef("data.result"),
			"structured_output": mappingRef("data.structured_output"),
		},
		Out: map[string]methodology.ActionMapping{
			"review_remarks_summary": mappingRef("data.review_remarks_summary"),
			"result":                 mappingRef("data.result"),
		},
	}
}

func publishReviewResponsesActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindPublishReviewResponses,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"invocation":        mappingRef("data.invocation"),
			"profile":           mappingRef("data.profile"),
			"allocation":        mappingRef("data.allocation"),
			"workplace":         mappingRef("data.workplace"),
			"result":            mappingRef("data.result"),
			"structured_output": mappingRef("data.structured_output"),
			"review_remarks":    mappingRef("data.review_remarks"),
		},
		Out: map[string]methodology.ActionMapping{
			"review_responses_summary": mappingRef("data.review_responses_summary"),
			"result":                   mappingRef("data.result"),
		},
	}
}

func finalizeActionOperation() methodology.ActionOperation {
	return methodology.ActionOperation{
		Name: OperationKindFinalize,

		Required: boolRef(true),
		In: map[string]methodology.ActionMapping{
			"action_name":  mappingRef("action.name"),
			"action_class": mappingRef("action.class"),
			"invocation":   mappingRef("data.invocation"),
			"profile":      mappingRef("data.profile"),
			"allocation":   mappingRef("data.allocation"),
			"workplace":    mappingRef("data.workplace"),
			"result":       mappingRef("data.result"),
		},
		Out: map[string]methodology.ActionMapping{
			"result": mappingRef("data.result"),
		},
	}
}

func optionalExecutionOperation(name string) methodology.ActionOperation {
	if name == OperationKindLoadReviewRemarks {
		operation := loadReviewRemarksActionOperation()
		operation.Required = boolRef(false)
		return operation
	}
	return methodology.ActionOperation{Name: name, Required: boolRef(false)}
}

func prepareDataOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindPrepareData,
		Kind: OperationKindPrepareData,

		In: model.OperationMap{
			"invocation":       {Ref: "in.invocation"},
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
		Name: OperationKindAllocateResources,
		Kind: OperationKindAllocateResources,

		In: model.OperationMap{
			"invocation": {Ref: "data.invocation"},
			"profile":    {Ref: "data.profile"},
		},
		Out: model.OperationMap{
			"allocation": {Ref: "data.allocation"},
		},
	}
}

func prepareWorkplaceOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindPrepareWorkplace,
		Kind: OperationKindPrepareWorkplace,

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
		Name: OperationKindBuildDirective,
		Kind: OperationKindBuildDirective,

		In: model.OperationMap{
			"invocation":               {Ref: "data.invocation"},
			"profile":                  {Ref: "data.profile"},
			"allocation":               {Ref: "data.allocation"},
			"workplace":                {Ref: "data.workplace"},
			"structured_output_fields": {Ref: "action.structured_output_fields"},
		},
		Out: model.OperationMap{
			"directive": {Ref: "data.directive"},
		},
	}
}

func launchSynthesisOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindLaunchSynthesis,
		Kind: OperationKindLaunchSynthesis,

		In: model.OperationMap{
			"invocation": {Ref: "data.invocation"},
			"directive":  {Ref: "data.directive"},
			"profile":    {Ref: "data.profile"},
			"allocation": {Ref: "data.allocation"},
			"workplace":  {Ref: "data.workplace"},
		},
		Out: model.OperationMap{
			"result": {Ref: "data.result"},
		},
	}
}

func parseResultOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindParseResult,
		Kind: OperationKindParseResult,

		In: model.OperationMap{
			"result":                   {Ref: "data.result"},
			"structured_output_fields": {Ref: "action.structured_output_fields"},
		},
		Out: model.OperationMap{
			"structured_output": {Ref: "data.structured_output"},
		},
	}
}

func commitPushOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindCommitPush,
		Kind: OperationKindCommitPush,

		In: model.OperationMap{
			"directory":      {Ref: "data.workplace.name"},
			"commit_message": {Ref: "data.structured_output.commit_message"},
			"fallback_name":  {Ref: "data.invocation.workplace.name"},
			"git":            {Ref: "data.allocation.git"},
			"private_store":  {Ref: "data.allocation.private_store"},
			"config_home":    {Ref: "data.allocation.config_home"},
		},
		Out: model.OperationMap{
			"commit_summary": {Ref: "data.commit_summary"},
		},
	}
}

func loadPullRequestOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindLoadPullRequest,
		Kind: OperationKindLoadPullRequest,

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
		Name: OperationKindLoadReviewRemarks,
		Kind: OperationKindLoadReviewRemarks,

		In: model.OperationMap{
			"invocation":   {Ref: "data.invocation"},
			"pull_request": {Ref: "data.pull_request"},
		},
		Out: model.OperationMap{
			"review_remarks": {Ref: "data.review_remarks"},
		},
	}
}

func finalizeOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindFinalize,
		Kind: OperationKindFinalize,

		In: model.OperationMap{
			"action_name":  {Ref: "action.name"},
			"action_class": {Ref: "action.class"},
			"invocation":   {Ref: "data.invocation"},
			"profile":      {Ref: "data.profile"},
			"allocation":   {Ref: "data.allocation"},
			"workplace":    {Ref: "data.workplace"},
			"result":       {Ref: "data.result"},
		},
		Out: model.OperationMap{
			"result": {Ref: "data.result"},
		},
	}
}

func publishMergeRequestOperationSpec() model.OperationSpec {
	return model.OperationSpec{
		Name: OperationKindPublishMergeRequest,
		Kind: OperationKindPublishMergeRequest,

		In: model.OperationMap{
			"invocation":        {Ref: "data.invocation"},
			"profile":           {Ref: "data.profile"},
			"allocation":        {Ref: "data.allocation"},
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
		Name: OperationKindPublishReviewRemarks,
		Kind: OperationKindPublishReviewRemarks,

		In: model.OperationMap{
			"invocation":        {Ref: "data.invocation"},
			"profile":           {Ref: "data.profile"},
			"allocation":        {Ref: "data.allocation"},
			"workplace":         {Ref: "data.workplace"},
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
		Name: OperationKindPublishReviewResponses,
		Kind: OperationKindPublishReviewResponses,

		In: model.OperationMap{
			"invocation":        {Ref: "data.invocation"},
			"profile":           {Ref: "data.profile"},
			"allocation":        {Ref: "data.allocation"},
			"workplace":         {Ref: "data.workplace"},
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

func mappingValue(value any) methodology.ActionMapping {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	raw := json.RawMessage(payload)
	return methodology.ActionMapping{Value: &raw}
}

const testExecutionMethodologyCatalogJSON = `{
  "actions": [
    {
      "name": "engineering-synthesis",
      "class": "engineering-synthesis",
      "profile": "default",
      "aliases": ["implement"],
      "requires_workplace": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    },
    {
      "name": "engineering-synthesis-commit",
      "class": "engineering-synthesis",
      "profile": "default",
      "aliases": ["implement-commit"],
      "requires_workplace": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "commit-push", "kind": "commit-push", "type":"builtin", "required": true, "in": {"directory": {"ref": "data.workplace.name"}, "commit_message": {"ref": "data.structured_output.commit_message"}, "fallback_name": {"ref": "data.invocation.workplace.name"}, "git": {"ref": "data.allocation.git"}, "private_store": {"ref": "data.allocation.private_store"}, "config_home": {"ref": "data.allocation.config_home"}}, "out": {"commit_summary": {"ref": "data.commit_summary"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    },
    {
      "name": "start-implementation-pr",
      "class": "engineering-synthesis",
      "profile": "coder",
      "requires_workplace": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "commit-push", "kind": "commit-push", "type":"builtin", "required": true, "in": {"directory": {"ref": "data.workplace.name"}, "commit_message": {"ref": "data.structured_output.commit_message"}, "fallback_name": {"ref": "data.invocation.workplace.name"}, "git": {"ref": "data.allocation.git"}, "private_store": {"ref": "data.allocation.private_store"}, "config_home": {"ref": "data.allocation.config_home"}}, "out": {"commit_summary": {"ref": "data.commit_summary"}}},
        {"name": "publish-merge-request", "kind": "publish-merge-request", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}, "structured_output": {"ref": "data.structured_output"}}, "out": {"merge_request": {"ref": "data.merge_request"}, "publish_summary": {"ref": "data.publish_summary"}, "result": {"ref": "data.result"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    },
    {
      "name": "review",
      "class": "review",
      "profile": "review",
      "requires_workplace": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    },
    {
      "name": "review-pull-request",
      "class": "review",
      "profile": "review",
      "requires_workplace": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "load-pull-request", "kind": "load-pull-request", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}}, "out": {"pull_request": {"ref": "data.pull_request"}, "invocation": {"ref": "data.invocation"}, "result": {"ref": "data.result"}}},
        {"name": "load-review-remarks", "kind": "load-review-remarks", "type":"builtin", "required": false, "in": {"invocation": {"ref": "data.invocation"}, "pull_request": {"ref": "data.pull_request"}}, "out": {"review_remarks": {"ref": "data.review_remarks"}, "invocation": {"ref": "data.invocation"}, "result": {"ref": "data.result"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "publish-review-remarks", "kind": "publish-review-remarks", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}, "structured_output": {"ref": "data.structured_output"}}, "out": {"review_remarks_summary": {"ref": "data.review_remarks_summary"}, "result": {"ref": "data.result"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    },
    {
      "name": "apply-review-comments",
      "class": "engineering-synthesis",
      "profile": "coder",
      "requires_workplace": true,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "load-pull-request", "kind": "load-pull-request", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}}, "out": {"pull_request": {"ref": "data.pull_request"}, "invocation": {"ref": "data.invocation"}, "result": {"ref": "data.result"}}},
        {"name": "load-review-remarks", "kind": "load-review-remarks", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "pull_request": {"ref": "data.pull_request"}}, "out": {"review_remarks": {"ref": "data.review_remarks"}, "invocation": {"ref": "data.invocation"}, "result": {"ref": "data.result"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "commit-push", "kind": "commit-push", "type":"builtin", "required": true, "in": {"directory": {"ref": "data.workplace.name"}, "commit_message": {"ref": "data.structured_output.commit_message"}, "fallback_name": {"ref": "data.invocation.workplace.name"}, "git": {"ref": "data.allocation.git"}, "private_store": {"ref": "data.allocation.private_store"}, "config_home": {"ref": "data.allocation.config_home"}}, "out": {"commit_summary": {"ref": "data.commit_summary"}}},
        {"name": "publish-review-responses", "kind": "publish-review-responses", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}, "structured_output": {"ref": "data.structured_output"}, "review_remarks": {"ref": "data.review_remarks"}}, "out": {"review_responses_summary": {"ref": "data.review_responses_summary"}, "result": {"ref": "data.result"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    },
    {
      "name": "integration-change",
      "class": "integration-change",
      "profile": "default",
      "requires_workplace": false,
      "operations": [
        {"name": "prepare-data", "kind": "prepare-data", "type":"builtin", "required": true, "in": {"invocation": {"ref": "in.invocation"}, "expected_result": {"ref": "in.expected_result"}, "constraints": {"ref": "in.constraints"}, "canonical_task": {"ref": "in.canonical_task"}, "related_objects": {"ref": "in.related_objects"}, "reasons": {"ref": "in.reasons"}, "structured_input": {"ref": "in.structured_input"}}, "out": {"structured_input": {"ref": "data.structured_input"}, "workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "resolve-profile", "kind": "resolve-profile", "type":"builtin", "required": true, "in": {"profile_name": {"ref": "action.profile"}, "invocation": {"ref": "data.invocation"}}, "out": {"profile": {"ref": "data.profile"}, "result": {"ref": "data.result"}}},
        {"name": "allocate-resources", "kind": "allocate-resources", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}}, "out": {"allocation": {"ref": "data.allocation"}}},
        {"name": "prepare-workplace", "kind": "prepare-workplace", "type":"builtin", "required": true, "in": {"requires_workplace": {"ref": "action.requires_workplace"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}}, "out": {"workplace": {"ref": "data.workplace"}, "invocation": {"ref": "data.invocation"}}},
        {"name": "build-directive", "kind": "build-directive", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"directive": {"ref": "data.directive"}}},
        {"name": "launch-synthesis", "kind": "launch-synthesis", "type":"builtin", "required": true, "in": {"invocation": {"ref": "data.invocation"}, "directive": {"ref": "data.directive"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}}, "out": {"result": {"ref": "data.result"}}},
        {"name": "parse-result", "kind": "parse-result", "type":"builtin", "required": true, "in": {"result": {"ref": "data.result"}}, "out": {"structured_output": {"ref": "data.structured_output"}}},
        {"name": "finalize", "kind": "finalize", "type":"builtin", "required": true, "in": {"action_name": {"ref": "action.name"}, "action_class": {"ref": "action.class"}, "invocation": {"ref": "data.invocation"}, "profile": {"ref": "data.profile"}, "allocation": {"ref": "data.allocation"}, "workplace": {"ref": "data.workplace"}, "result": {"ref": "data.result"}}, "out": {"result": {"ref": "data.result"}}}
      ]
    }
  ]
}`
