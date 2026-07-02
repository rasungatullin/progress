package execution

import (
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	ActionClassEngineeringSynthesis = "engineering-synthesis"

	OperationKindResolveAction     = "resolve-action"
	OperationKindResolveProfile    = "resolve-profile"
	OperationKindAllocateResources = "allocate-resources"
	OperationKindPrepareWorkplace  = "prepare-workplace"
	OperationKindLaunchSynthesis   = "launch-synthesis"

	OperationStatusPending   = "pending"
	OperationStatusCompleted = "completed"
	OperationStatusFailed    = "failed"
)

func resolveAction(in model.Invocation) model.Action {
	name := strings.TrimSpace(in.Action)
	if name == "" {
		name = "engineering-synthesis"
	}

	profileName := strings.TrimSpace(in.Profile)
	if profileName == "" {
		profileName = "default"
	}

	return model.Action{
		Name:              name,
		Class:             model.ActionClass(ActionClassEngineeringSynthesis),
		Profile:           profileName,
		ExpectedResult:    "Получить результат выполнения действия в нормализованной форме.",
		RequiresWorkplace: true,
		RequiresSynthesis: true,
		Operations: []model.OperationSpec{
			{Name: OperationKindResolveAction, Kind: OperationKindResolveAction, Title: "Разрешение действия"},
			{Name: OperationKindResolveProfile, Kind: OperationKindResolveProfile, Title: "Выбор исполнительного профиля"},
			{Name: OperationKindAllocateResources, Kind: OperationKindAllocateResources, Title: "Ресурсное снабжение"},
			{Name: OperationKindPrepareWorkplace, Kind: OperationKindPrepareWorkplace, Title: "Подготовка рабочего места"},
			{Name: OperationKindLaunchSynthesis, Kind: OperationKindLaunchSynthesis, Title: "Запуск синтеза"},
		},
	}
}

type operationTracker struct {
	results []model.OperationResult
	index   map[string]int
}

func newOperationTracker(action model.Action) *operationTracker {
	results := make([]model.OperationResult, 0, len(action.Operations))
	index := make(map[string]int, len(action.Operations))
	for _, operation := range action.Operations {
		result := model.OperationResult{
			Name:   operation.Name,
			Kind:   operation.Kind,
			Title:  operation.Title,
			Status: model.OperationStatus(OperationStatusPending),
		}
		index[operation.Name] = len(results)
		results = append(results, result)
	}

	return &operationTracker{results: results, index: index}
}

func (t *operationTracker) complete(name string, summary string) {
	t.set(name, model.OperationStatus(OperationStatusCompleted), summary, nil)
}

func (t *operationTracker) fail(name string, summary string, err error, code string, retryable bool, manualIntervention bool) {
	t.set(name, model.OperationStatus(OperationStatusFailed), summary, executionFailure(code, err, retryable, manualIntervention))
}

func (t *operationTracker) set(name string, status model.OperationStatus, summary string, failure *model.Failure) {
	index, ok := t.index[name]
	if !ok {
		index = len(t.results)
		t.index[name] = index
		t.results = append(t.results, model.OperationResult{Name: name})
	}

	t.results[index].Status = status
	t.results[index].Summary = strings.TrimSpace(summary)
	t.results[index].Failure = failure
}

func (t *operationTracker) snapshot() []model.OperationResult {
	return append([]model.OperationResult(nil), t.results...)
}

func executionFailure(code string, err error, retryable bool, manualIntervention bool) *model.Failure {
	if err == nil {
		return nil
	}

	return &model.Failure{
		Code:               code,
		Message:            strings.TrimSpace(err.Error()),
		Retryable:          retryable,
		ManualIntervention: manualIntervention,
	}
}

func executionResultFromLaunch(action model.Action, operations []model.OperationResult, result model.LaunchResult, err error) model.ExecutionResult {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		if err != nil {
			status = "failed"
		} else {
			status = "completed"
		}
	}

	executionResult := model.ExecutionResult{
		Status:     status,
		Summary:    strings.TrimSpace(result.Summary),
		Action:     action,
		Operations: append([]model.OperationResult(nil), operations...),
		Launch:     &result,
	}
	if err != nil {
		executionResult.Failure = executionFailure("execution_failed", err, false, true)
	}

	return executionResult
}
