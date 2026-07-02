package execution

import (
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	ActionClassEngineeringSynthesis = "engineering-synthesis"
	ActionClassReview               = "review"
	ActionClassTaskPreparation      = "task-preparation"
	ActionClassIntegrationChange    = "integration-change"
	ActionClassService              = "service"

	OperationKindResolveAction     = "resolve-action"
	OperationKindPrepareData       = "prepare-data"
	OperationKindResolveProfile    = "resolve-profile"
	OperationKindAllocateResources = "allocate-resources"
	OperationKindPrepareWorkplace  = "prepare-workplace"
	OperationKindBuildDirective    = "build-directive"
	OperationKindLaunchSynthesis   = "launch-synthesis"
	OperationKindParseResult       = "parse-result"
	OperationKindFinalize          = "finalize"

	OperationStatusPending   = "pending"
	OperationStatusCompleted = "completed"
	OperationStatusFailed    = "failed"
)

func resolveAction(in model.Invocation) model.Action {
	name := strings.TrimSpace(in.Action)
	if in.Assignment != nil && strings.TrimSpace(in.Assignment.Action) != "" {
		name = strings.TrimSpace(in.Assignment.Action)
	}
	if name == "" {
		name = "engineering-synthesis"
	}

	profileName := strings.TrimSpace(in.Profile)
	if in.Assignment != nil && strings.TrimSpace(in.Assignment.Profile) != "" {
		profileName = strings.TrimSpace(in.Assignment.Profile)
	}
	if profileName == "" {
		profileName = "default"
	}

	expectedResult := "Получить результат выполнения действия в нормализованной форме."
	if in.Assignment != nil && strings.TrimSpace(in.Assignment.ExpectedResult) != "" {
		expectedResult = strings.TrimSpace(in.Assignment.ExpectedResult)
	}

	return model.Action{
		Name:              name,
		Class:             classifyAction(name, profileName),
		Profile:           profileName,
		ExpectedResult:    expectedResult,
		RequiresWorkplace: actionRequiresWorkplace(name, profileName),
		RequiresSynthesis: actionRequiresSynthesis(name, profileName),
		Operations: []model.OperationSpec{
			{Name: OperationKindResolveAction, Kind: OperationKindResolveAction, Title: "Разрешение действия"},
			{Name: OperationKindPrepareData, Kind: OperationKindPrepareData, Title: "Подготовка данных"},
			{Name: OperationKindResolveProfile, Kind: OperationKindResolveProfile, Title: "Выбор исполнительного профиля"},
			{Name: OperationKindAllocateResources, Kind: OperationKindAllocateResources, Title: "Ресурсное снабжение"},
			{Name: OperationKindPrepareWorkplace, Kind: OperationKindPrepareWorkplace, Title: "Подготовка рабочего места"},
			{Name: OperationKindBuildDirective, Kind: OperationKindBuildDirective, Title: "Сборка исполнительной директивы"},
			{Name: OperationKindLaunchSynthesis, Kind: OperationKindLaunchSynthesis, Title: "Запуск синтеза"},
			{Name: OperationKindParseResult, Kind: OperationKindParseResult, Title: "Разбор результата"},
			{Name: OperationKindFinalize, Kind: OperationKindFinalize, Title: "Завершающая фиксация"},
		},
	}
}

func classifyAction(actionName, profileName string) model.ActionClass {
	value := strings.ToLower(strings.TrimSpace(actionName + " " + profileName))
	switch {
	case strings.Contains(value, "review"):
		return model.ActionClass(ActionClassReview)
	case strings.Contains(value, "assess") || strings.Contains(value, "description") || strings.Contains(value, "describe"):
		return model.ActionClass(ActionClassTaskPreparation)
	case strings.Contains(value, "integration") || strings.Contains(value, "comment") || strings.Contains(value, "merge") || strings.Contains(value, "pull-request"):
		return model.ActionClass(ActionClassIntegrationChange)
	case strings.Contains(value, "service") || strings.Contains(value, "diagnostic"):
		return model.ActionClass(ActionClassService)
	default:
		return model.ActionClass(ActionClassEngineeringSynthesis)
	}
}

func actionRequiresWorkplace(actionName, profileName string) bool {
	class := classifyAction(actionName, profileName)
	return class != model.ActionClass(ActionClassIntegrationChange)
}

func actionRequiresSynthesis(actionName, profileName string) bool {
	class := classifyAction(actionName, profileName)
	return class != model.ActionClass(ActionClassIntegrationChange) && class != model.ActionClass(ActionClassService)
}

func assignmentFromInvocation(in model.Invocation) *model.ExecutionAssignment {
	if in.Assignment != nil {
		assignment := cloneAssignment(in.Assignment)
		if assignment.StructuredInput == nil {
			assignment.StructuredInput = in.Launch.StructuredInput
		}
		if strings.TrimSpace(assignment.Action) == "" {
			assignment.Action = strings.TrimSpace(in.Action)
		}
		if strings.TrimSpace(assignment.Profile) == "" {
			assignment.Profile = strings.TrimSpace(in.Profile)
		}
		if len(assignment.Constraints) == 0 && assignment.StructuredInput != nil {
			assignment.Constraints = append([]string(nil), assignment.StructuredInput.Constraints...)
		}
		return assignment
	}

	assignment := &model.ExecutionAssignment{
		Action:          strings.TrimSpace(in.Action),
		Profile:         strings.TrimSpace(in.Profile),
		ExpectedResult:  "Получить результат выполнения действия в нормализованной форме.",
		StructuredInput: in.Launch.StructuredInput,
	}
	if assignment.Action == "" {
		assignment.Action = "engineering-synthesis"
	}
	if assignment.Profile == "" {
		assignment.Profile = "default"
	}
	if assignment.StructuredInput != nil {
		assignment.Constraints = append([]string(nil), assignment.StructuredInput.Constraints...)
	}

	return assignment
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
	t.set(name, model.OperationStatus(OperationStatusCompleted), "", "", summary, nil)
}

func (t *operationTracker) completeIO(name string, input string, output string, summary string) {
	t.set(name, model.OperationStatus(OperationStatusCompleted), input, output, summary, nil)
}

func (t *operationTracker) fail(name string, summary string, err error, code string, retryable bool, manualIntervention bool) {
	t.set(name, model.OperationStatus(OperationStatusFailed), "", "", summary, executionFailure(code, err, retryable, manualIntervention))
}

func (t *operationTracker) set(name string, status model.OperationStatus, input string, output string, summary string, failure *model.Failure) {
	index, ok := t.index[name]
	if !ok {
		index = len(t.results)
		t.index[name] = index
		t.results = append(t.results, model.OperationResult{Name: name})
	}

	t.results[index].Status = status
	t.results[index].Input = strings.TrimSpace(input)
	t.results[index].Output = strings.TrimSpace(output)
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

func executionResultFromLaunch(assignment *model.ExecutionAssignment, action model.Action, operations []model.OperationResult, result model.LaunchResult, err error) model.ExecutionResult {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		if err != nil {
			status = "failed"
		} else {
			status = "completed"
		}
	}
	if err != nil && result.StructuredOutput != nil {
		status = "partial"
	}

	executionResult := model.ExecutionResult{
		Status:          status,
		Summary:         strings.TrimSpace(result.Summary),
		Assignment:      cloneAssignment(assignment),
		Action:          action,
		Operations:      append([]model.OperationResult(nil), operations...),
		Artifacts:       executionArtifacts(result),
		DiagnosticLinks: executionDiagnosticLinks(result),
		Launch:          &result,
	}
	if err != nil {
		executionResult.Failure = executionFailure("execution_failed", err, false, true)
	}

	return executionResult
}

func cloneAssignment(assignment *model.ExecutionAssignment) *model.ExecutionAssignment {
	if assignment == nil {
		return nil
	}

	cloned := *assignment
	cloned.Constraints = append([]string(nil), assignment.Constraints...)
	cloned.RelatedObjects = append([]model.ObjectRef(nil), assignment.RelatedObjects...)
	cloned.Reasons = append([]model.AssignmentReason(nil), assignment.Reasons...)
	return &cloned
}

func executionArtifacts(result model.LaunchResult) []model.Artifact {
	artifacts := make([]model.Artifact, 0, 1)
	if path := strings.TrimSpace(result.RawOutputPath); path != "" {
		artifacts = append(artifacts, model.Artifact{Type: "runner-output", Path: path})
	}
	return artifacts
}

func executionDiagnosticLinks(result model.LaunchResult) []model.DiagnosticLink {
	links := make([]model.DiagnosticLink, 0, 2)
	if path := strings.TrimSpace(result.RunRecordPath); path != "" {
		links = append(links, model.DiagnosticLink{Type: "run-record", Path: path})
	}
	if path := strings.TrimSpace(result.RawOutputPath); path != "" {
		links = append(links, model.DiagnosticLink{Type: "runner-output", Path: path})
	}
	return links
}

func assignmentSummary(assignment *model.ExecutionAssignment) string {
	if assignment == nil {
		return ""
	}

	parts := []string{
		"action=" + strings.TrimSpace(assignment.Action),
		"profile=" + strings.TrimSpace(assignment.Profile),
	}
	if assignment.CanonicalTask != nil {
		if assignment.CanonicalTask.Number != 0 {
			parts = append(parts, "task-number="+formatInt(assignment.CanonicalTask.Number))
		}
		if repository := strings.TrimSpace(assignment.CanonicalTask.Repository); repository != "" {
			parts = append(parts, "repository="+repository)
		}
	}
	if len(assignment.Constraints) != 0 {
		parts = append(parts, "constraints="+formatInt(len(assignment.Constraints)))
	}

	return strings.Join(nonEmptyParts(parts), " ")
}

func structuredInputSummary(input *model.StructuredInput) string {
	if input == nil {
		return "structured-input=absent"
	}

	parts := []string{"structured-input=present"}
	if strings.TrimSpace(input.Task) != "" {
		parts = append(parts, "task=present")
	}
	if len(input.Constraints) != 0 {
		parts = append(parts, "constraints="+formatInt(len(input.Constraints)))
	}
	if len(input.ProjectContext) != 0 {
		parts = append(parts, "project-context="+formatInt(len(input.ProjectContext)))
	}
	if len(input.OperationalContext) != 0 {
		parts = append(parts, "operational-context="+formatInt(len(input.OperationalContext)))
	}
	if len(input.PreviousRunResults) != 0 {
		parts = append(parts, "previous-results="+formatInt(len(input.PreviousRunResults)))
	}
	if len(input.ReviewRemarks) != 0 {
		parts = append(parts, "review-remarks="+formatInt(len(input.ReviewRemarks)))
	}
	if len(input.IntegrationActions) != 0 {
		parts = append(parts, "integration-actions="+formatInt(len(input.IntegrationActions)))
	}

	return strings.Join(parts, " ")
}

func resultSummary(result model.LaunchResult) string {
	parts := []string{"status=" + strings.TrimSpace(result.Status)}
	if strings.TrimSpace(result.Summary) != "" {
		parts = append(parts, "summary=present")
	}
	if strings.TrimSpace(result.RawStructuredOutput) != "" {
		parts = append(parts, "raw-structured-output=present")
	}
	return strings.Join(nonEmptyParts(parts), " ")
}

func structuredOutputSummary(output *model.StructuredOutput) string {
	if output == nil {
		return "structured-output=absent"
	}

	parts := []string{"structured-output=present"}
	if strings.TrimSpace(output.Summary) != "" {
		parts = append(parts, "summary=present")
	}
	if len(output.Remarks) != 0 {
		parts = append(parts, "remarks="+formatInt(len(output.Remarks)))
	}
	if len(output.Questions) != 0 {
		parts = append(parts, "questions="+formatInt(len(output.Questions)))
	}
	if len(output.FollowUpActions) != 0 {
		parts = append(parts, "follow-up-actions="+formatInt(len(output.FollowUpActions)))
	}
	if len(output.Changes) != 0 {
		parts = append(parts, "changes="+formatInt(len(output.Changes)))
	}
	if len(output.Commands) != 0 {
		parts = append(parts, "commands="+formatInt(len(output.Commands)))
	}
	if output.Conclusion != nil && strings.TrimSpace(output.Conclusion.Status) != "" {
		parts = append(parts, "conclusion="+strings.TrimSpace(output.Conclusion.Status))
	}
	return strings.Join(parts, " ")
}

func finalizeSummary(result model.LaunchResult) string {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(result.RunRecordPath) != "" {
		parts = append(parts, "run-record=present")
	}
	if strings.TrimSpace(result.RawOutputPath) != "" {
		parts = append(parts, "runner-output=present")
	}
	if result.RunnerSessionID != "" {
		parts = append(parts, "runner-session=present")
	}
	if len(parts) == 0 {
		return "diagnostics=none"
	}
	return strings.Join(parts, " ")
}

func nonEmptyParts(parts []string) []string {
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasSuffix(part, "=") {
			continue
		}
		result = append(result, part)
	}
	return result
}

func formatInt(value int) string {
	return strconv.Itoa(value)
}
