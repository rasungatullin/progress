package execution

import (
	"context"
	"fmt"
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

	ActionStartImplementationPR = "start-implementation-pr"
	ActionReviewPullRequest     = "review-pull-request"
	ActionApplyReviewComments   = "apply-review-comments"

	OperationKindResolveAction          = "resolve-action"
	OperationKindPrepareData            = "prepare-data"
	OperationKindLoadPullRequest        = "load-pull-request"
	OperationKindLoadReviewRemarks      = "load-review-remarks"
	OperationKindResolveProfile         = "resolve-profile"
	OperationKindAllocateResources      = "allocate-resources"
	OperationKindPrepareWorkplace       = "prepare-workplace"
	OperationKindBuildDirective         = "build-directive"
	OperationKindLaunchSynthesis        = "launch-synthesis"
	OperationKindParseResult            = "parse-result"
	OperationKindCommitPush             = "commit-push"
	OperationKindPublishMergeRequest    = "publish-merge-request"
	OperationKindPublishReviewRemarks   = "publish-review-remarks"
	OperationKindPublishReviewResponses = "publish-review-responses"
	OperationKindFinalize               = "finalize"

	OperationStatusPending   = "pending"
	OperationStatusCompleted = "completed"
	OperationStatusFailed    = "failed"
	OperationStatusSkipped   = "skipped"

	OperationOriginBuiltin = "builtin"
)

type actionResolver interface {
	ResolveAction(context.Context, invocation) (Action, error)
}

type actionCatalog struct {
	actions map[string]model.Action
	aliases map[string]string
}

func newActionCatalog() *actionCatalog {
	actions := map[string]model.Action{
		ActionClassEngineeringSynthesis: newActionTemplate(ActionClassEngineeringSynthesis, ActionClassEngineeringSynthesis, "default", true, true, "Получить результат инженерного синтеза в нормализованной форме."),
		"engineering-synthesis-commit":  newActionTemplateWithOperations("engineering-synthesis-commit", ActionClassEngineeringSynthesis, "default", true, true, "Получить результат инженерного синтеза, создать коммит и отправить ветку.", actionOperationsWithCommitPush()),
		ActionStartImplementationPR:     newActionTemplateWithOperations(ActionStartImplementationPR, ActionClassEngineeringSynthesis, "coder", true, true, "Выполнить реализацию задачи, отправить ветку и открыть запрос на слияние.", startImplementationPROperations()),
		ActionClassReview:               newActionTemplate(ActionClassReview, ActionClassReview, "review", true, true, "Провести ревизию результата и вернуть заключение ревизии."),
		ActionReviewPullRequest:         newActionTemplateWithOperations(ActionReviewPullRequest, ActionClassReview, "review", true, true, "Проверить открытый запрос на слияние и записать замечания ревизии.", reviewPullRequestOperations()),
		ActionApplyReviewComments:       newActionTemplateWithOperations(ActionApplyReviewComments, ActionClassEngineeringSynthesis, "coder", true, true, "Получить замечания ревизии, исправить их и записать ответы на замечания.", applyReviewCommentsOperations()),
		ActionClassTaskPreparation:      newActionTemplate(ActionClassTaskPreparation, ActionClassTaskPreparation, "task-description-assessor", true, true, "Подготовить или оценить постановку задачи."),
		ActionClassIntegrationChange:    newActionTemplate(ActionClassIntegrationChange, ActionClassIntegrationChange, "default", false, false, "Выполнить интеграционное изменение через опубликованную операцию."),
		ActionClassService:              newActionTemplate(ActionClassService, ActionClassService, "default", true, false, "Выполнить служебное действие без содержательного синтеза."),
	}
	aliases := map[string]string{
		"assess":                      ActionClassTaskPreparation,
		"assess-description":          ActionClassTaskPreparation,
		"description-assessment":      ActionClassTaskPreparation,
		"prepare-task":                ActionClassTaskPreparation,
		"task-description-assessment": ActionClassTaskPreparation,
		"code":                        ActionClassEngineeringSynthesis,
		"code-commit":                 "engineering-synthesis-commit",
		"coder":                       ActionClassEngineeringSynthesis,
		"coding":                      ActionClassEngineeringSynthesis,
		"implement":                   ActionClassEngineeringSynthesis,
		"implement-commit":            "engineering-synthesis-commit",
		"implementation":              ActionClassEngineeringSynthesis,
		"implement-pr":                ActionStartImplementationPR,
		"implementation-pr":           ActionStartImplementationPR,
		"open-pr":                     ActionStartImplementationPR,
		"start-implementation":        ActionStartImplementationPR,
		"start-implementation-pr":     ActionStartImplementationPR,
		"pr-review":                   ActionReviewPullRequest,
		"review-pr":                   ActionReviewPullRequest,
		"review-pull-request":         ActionReviewPullRequest,
		"address-review-comments":     ActionApplyReviewComments,
		"apply-review-comments":       ActionApplyReviewComments,
		"fix-review-comments":         ActionApplyReviewComments,
		"reply-review-comments":       ActionApplyReviewComments,
		"pull-request":                ActionClassIntegrationChange,
		"comment":                     ActionClassIntegrationChange,
		"integration":                 ActionClassIntegrationChange,
		"diagnostic":                  ActionClassService,
		"diagnostics":                 ActionClassService,
	}

	return &actionCatalog{actions: actions, aliases: aliases}
}

func (c *actionCatalog) ResolveAction(ctx context.Context, in invocation) (Action, error) {
	_ = ctx
	if c == nil {
		c = newActionCatalog()
	}

	name := actionNameFromInvocation(in)
	if name == "" {
		name = defaultActionName()
	}
	canonicalName := strings.ToLower(strings.TrimSpace(name))
	if alias := strings.TrimSpace(c.aliases[canonicalName]); alias != "" {
		canonicalName = alias
	}
	template, ok := c.actions[canonicalName]
	if !ok {
		return model.Action{}, fmt.Errorf("действие %q не найдено", name)
	}

	action := cloneAction(template)
	action.Name = name
	if strings.TrimSpace(action.Profile) == "" {
		action.Profile = "default"
	}
	if in.Assignment != nil && strings.TrimSpace(in.Assignment.ExpectedResult) != "" {
		action.ExpectedResult = strings.TrimSpace(in.Assignment.ExpectedResult)
	}
	if strings.TrimSpace(action.ExpectedResult) == "" {
		action.ExpectedResult = "Получить результат выполнения действия в нормализованной форме."
	}

	return action, nil
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
		if strings.TrimSpace(assignment.Action) == "" {
			assignment.Action = defaultActionName()
		}
		if len(assignment.Constraints) == 0 && assignment.StructuredInput != nil {
			assignment.Constraints = append([]string(nil), assignment.StructuredInput.Constraints...)
		}
		return assignment
	}

	assignment := &model.ExecutionAssignment{
		Action:          strings.TrimSpace(in.Action),
		ExpectedResult:  "Получить результат выполнения действия в нормализованной форме.",
		StructuredInput: in.Launch.StructuredInput,
	}
	if assignment.Action == "" {
		assignment.Action = defaultActionName()
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
			Name:     operation.Name,
			Kind:     operation.Kind,
			Title:    operation.Title,
			Origin:   operation.Origin,
			Required: operation.Required,
			Status:   model.OperationStatus(OperationStatusPending),
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

func (t *operationTracker) skip(name string, summary string) {
	t.set(name, model.OperationStatus(OperationStatusSkipped), "", "", summary, nil)
}

func (t *operationTracker) fail(name string, summary string, err error, code string, retryable bool, manualIntervention bool) {
	t.set(name, model.OperationStatus(OperationStatusFailed), "", "", summary, executionFailure(code, err, retryable, manualIntervention))
}

func (t *operationTracker) set(name string, status model.OperationStatus, input string, output string, summary string, failure *model.Failure) {
	index, ok := t.index[name]
	if !ok {
		index = len(t.results)
		t.index[name] = index
		t.results = append(t.results, model.OperationResult{Name: name, Origin: OperationOriginBuiltin, Required: true})
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
		executionResult.Failure = executionFailure(executionFailureCode(operations), err, false, true)
	}

	return executionResult
}

func newActionTemplate(name, class, profile string, requiresWorkplace, requiresSynthesis bool, expectedResult string) model.Action {
	return newActionTemplateWithOperations(name, class, profile, requiresWorkplace, requiresSynthesis, expectedResult, defaultActionOperations())
}

func newActionTemplateWithOperations(name, class, profile string, requiresWorkplace, requiresSynthesis bool, expectedResult string, operations []model.OperationSpec) model.Action {
	return model.Action{
		Name:              name,
		Class:             model.ActionClass(class),
		Profile:           profile,
		ExpectedResult:    expectedResult,
		RequiresWorkplace: requiresWorkplace,
		RequiresSynthesis: requiresSynthesis,
		Operations:        append([]model.OperationSpec(nil), operations...),
	}
}

func defaultActionOperations() []model.OperationSpec {
	return []model.OperationSpec{
		builtinOperation(OperationKindResolveAction, "Разрешение действия", true),
		builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
		builtinOperation(OperationKindResolveProfile, "Выбор исполнительного профиля", true),
		builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true),
		builtinOperation(OperationKindPrepareWorkplace, "Подготовка рабочего места", true),
		builtinOperation(OperationKindBuildDirective, "Сборка исполнительной директивы", true),
		builtinOperation(OperationKindLaunchSynthesis, "Запуск синтеза", true),
		builtinOperation(OperationKindParseResult, "Разбор результата", true),
		builtinOperation(OperationKindFinalize, "Завершающая фиксация", true),
	}
}

func actionOperationsWithCommitPush() []model.OperationSpec {
	operations := defaultActionOperations()
	return insertBeforeFinalize(operations, builtinOperation(OperationKindCommitPush, "Создание коммита и отправка ветки", true))
}

func startImplementationPROperations() []model.OperationSpec {
	operations := defaultActionOperations()
	return insertBeforeFinalize(operations,
		builtinOperation(OperationKindCommitPush, "Создание коммита и отправка ветки", true),
		builtinOperation(OperationKindPublishMergeRequest, "Открытие запроса на слияние", true),
	)
}

func reviewPullRequestOperations() []model.OperationSpec {
	return []model.OperationSpec{
		builtinOperation(OperationKindResolveAction, "Разрешение действия", true),
		builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
		builtinOperation(OperationKindLoadPullRequest, "Получение запроса на слияние", true),
		builtinOperation(OperationKindLoadReviewRemarks, "Получение замечаний ревизии", false),
		builtinOperation(OperationKindResolveProfile, "Выбор исполнительного профиля", true),
		builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true),
		builtinOperation(OperationKindPrepareWorkplace, "Подготовка рабочего места", true),
		builtinOperation(OperationKindBuildDirective, "Сборка исполнительной директивы", true),
		builtinOperation(OperationKindLaunchSynthesis, "Запуск ревизии", true),
		builtinOperation(OperationKindParseResult, "Разбор результата", true),
		builtinOperation(OperationKindPublishReviewRemarks, "Запись замечаний ревизии", true),
		builtinOperation(OperationKindFinalize, "Завершающая фиксация", true),
	}
}

func applyReviewCommentsOperations() []model.OperationSpec {
	operations := []model.OperationSpec{
		builtinOperation(OperationKindResolveAction, "Разрешение действия", true),
		builtinOperation(OperationKindPrepareData, "Подготовка данных", true),
		builtinOperation(OperationKindLoadPullRequest, "Получение запроса на слияние", true),
		builtinOperation(OperationKindLoadReviewRemarks, "Получение замечаний ревизии", true),
		builtinOperation(OperationKindResolveProfile, "Выбор исполнительного профиля", true),
		builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true),
		builtinOperation(OperationKindPrepareWorkplace, "Подготовка рабочего места", true),
		builtinOperation(OperationKindBuildDirective, "Сборка исполнительной директивы", true),
		builtinOperation(OperationKindLaunchSynthesis, "Запуск доработки", true),
		builtinOperation(OperationKindParseResult, "Разбор результата", true),
		builtinOperation(OperationKindCommitPush, "Создание коммита и отправка ветки", true),
		builtinOperation(OperationKindPublishReviewResponses, "Запись ответов на замечания", true),
		builtinOperation(OperationKindFinalize, "Завершающая фиксация", true),
	}
	return operations
}

func insertBeforeFinalize(operations []model.OperationSpec, additions ...model.OperationSpec) []model.OperationSpec {
	for index, operation := range operations {
		if operation.Name != OperationKindFinalize {
			continue
		}

		withAdditions := make([]model.OperationSpec, 0, len(operations)+len(additions))
		withAdditions = append(withAdditions, operations[:index]...)
		withAdditions = append(withAdditions, additions...)
		withAdditions = append(withAdditions, operations[index:]...)
		return withAdditions
	}

	return append(operations, additions...)
}

func builtinOperation(kind string, title string, required bool) model.OperationSpec {
	return model.OperationSpec{
		Name:     kind,
		Kind:     model.OperationKind(kind),
		Title:    title,
		Origin:   OperationOriginBuiltin,
		Required: required,
	}
}

func actionNameFromInvocation(in model.Invocation) string {
	if in.Assignment != nil && strings.TrimSpace(in.Assignment.Action) != "" {
		return strings.TrimSpace(in.Assignment.Action)
	}
	return strings.TrimSpace(in.Action)
}

func defaultActionName() string {
	return ActionClassEngineeringSynthesis
}

func cloneAction(action model.Action) model.Action {
	cloned := action
	cloned.Operations = append([]model.OperationSpec(nil), action.Operations...)
	return cloned
}

func actionResolutionFailureOperations(err error) []model.OperationResult {
	tracker := newOperationTracker(model.Action{
		Operations: []model.OperationSpec{
			builtinOperation(OperationKindResolveAction, "Разрешение действия", true),
		},
	})
	tracker.fail(OperationKindResolveAction, "Действие не найдено на нулевом этапе исполнения.", err, "action_not_found", false, true)
	return tracker.snapshot()
}

func executionFailureCode(operations []model.OperationResult) string {
	for _, operation := range operations {
		if operation.Failure != nil && strings.TrimSpace(operation.Failure.Code) != "" {
			return operation.Failure.Code
		}
	}
	return "execution_failed"
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
