package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/methodology"
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
	ActionResolveMergeConflict  = "resolve-merge-conflict"

	OperationKindPrepareData            = "prepare-data"
	OperationKindLoadPullRequest        = "load-pull-request"
	OperationKindLoadReviewRemarks      = "load-review-remarks"
	OperationKindResolveProfile         = "resolve-profile"
	OperationKindAllocateResources      = "allocate-resources"
	OperationKindPrepareWorkplace       = "prepare-workplace"
	OperationKindBuildDirective         = "build-directive"
	OperationKindBuildPrompt            = "build-prompt"
	OperationKindStructuredSynthesis    = "structured-synthesis"
	OperationKindLaunchSynthesis        = "launch-synthesis"
	OperationKindParseResult            = "parse-result"
	OperationKindCommitPush             = "commit-push"
	OperationKindRebase                 = "rebase"
	OperationKindResolveMergeConflict   = "resolve-merge-conflict"
	OperationKindPublishMergeRequest    = "publish-merge-request"
	OperationKindPublishReviewRemarks   = "publish-review-remarks"
	OperationKindPublishReviewResponses = "publish-review-responses"
	OperationKindFinalize               = "finalize"

	OperationStatusPending   = "pending"
	OperationStatusCompleted = "completed"
	OperationStatusFailed    = "failed"
	OperationStatusSkipped   = "skipped"

	OperationTypeBuiltin     = "builtin"
	OperationTypeAction      = "action"
	OperationTypeIntegration = "integration"
)

type actionResolver interface {
	ResolveAction(context.Context, invocation) (Action, error)
}

type methodologyActionResolver struct {
	loadCatalog func(context.Context, methodology.CatalogRequest) (methodology.CatalogSnapshot, error)
	getwd       func() (string, error)
	stat        func(string) (os.FileInfo, error)
}

func newMethodologyActionResolver() *methodologyActionResolver {
	service := methodology.NewService(nil)
	return &methodologyActionResolver{
		loadCatalog: service.Load,
		getwd:       os.Getwd,
		stat:        os.Stat,
	}
}

func (r *methodologyActionResolver) ResolveAction(ctx context.Context, in invocation) (Action, error) {
	if r == nil {
		r = newMethodologyActionResolver()
	}
	if r.loadCatalog == nil {
		service := methodology.NewService(nil)
		r.loadCatalog = service.Load
	}

	repoRoot := r.resolveRepoRoot()
	snapshot, err := r.loadCatalog(ctx, methodology.CatalogRequest{RepoRoot: repoRoot})
	if err != nil {
		return model.Action{}, actionResolutionError{code: "action_catalog_unavailable", err: err}
	}
	return resolveActionFromCatalog(snapshot.Catalog, in)
}

type actionResolutionError struct {
	code string
	err  error
}

func (e actionResolutionError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e actionResolutionError) Unwrap() error {
	return e.err
}

func (r *methodologyActionResolver) resolveRepoRoot() string {
	if r == nil || r.getwd == nil {
		return ""
	}
	wd, err := r.getwd()
	if err != nil {
		return ""
	}
	wd = strings.TrimSpace(wd)
	if wd == "" {
		return ""
	}
	for current := wd; current != ""; current = filepath.Dir(current) {
		if r.catalogExists(filepath.Join(current, ".progress", "methodology", "catalog.json")) || r.catalogExists(filepath.Join(current, ".git")) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return wd
}

func (r *methodologyActionResolver) catalogExists(path string) bool {
	if r == nil || r.stat == nil {
		return false
	}
	_, err := r.stat(path)
	return err == nil
}

func resolveActionFromCatalog(catalog methodology.Catalog, in invocation) (Action, error) {
	name := actionNameFromInvocation(in)
	if name == "" {
		name = defaultActionName()
	}
	canonicalName := strings.ToLower(strings.TrimSpace(name))
	entry, ok := findMethodologyAction(catalog.Actions, canonicalName)
	if !ok {
		return model.Action{}, actionResolutionError{code: "action_not_found", err: fmt.Errorf("действие %q не найдено в каталоге методик", name)}
	}

	operationsByName := map[string]methodology.Operation{}
	for _, operation := range catalog.Operations {
		operation = normalizeMethodologyOperation(operation)
		if operation.Name == "" {
			continue
		}
		operationsByName[operation.Name] = operation
	}

	action, err := executionActionFromMethodology(*entry, operationsByName)
	if err != nil {
		return model.Action{}, actionResolutionError{code: "action_invalid", err: fmt.Errorf("действие %q в каталоге методик невалидно: %w", entry.Name, err)}
	}
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

func findMethodologyAction(actions []methodology.Action, name string) (*methodology.Action, bool) {
	for index := range actions {
		action := &actions[index]
		if strings.EqualFold(strings.TrimSpace(action.Name), name) {
			return action, true
		}
	}
	for index := len(actions) - 1; index >= 0; index-- {
		action := &actions[index]
		for _, alias := range action.Aliases {
			if strings.EqualFold(strings.TrimSpace(alias), name) {
				return action, true
			}
		}
	}
	return nil, false
}

func executionActionFromMethodology(action methodology.Action, operationsByName map[string]methodology.Operation) (model.Action, error) {
	structuredOutputFields, err := normalizeStructuredOutputFields(action.StructuredOutputFields)
	if err != nil {
		return model.Action{}, fmt.Errorf("некорректные structured_output_fields: %w", err)
	}
	operations := make([]model.OperationSpec, 0, len(action.Operations))
	seenOperations := map[string]struct{}{}
	for _, operation := range action.Operations {
		spec, err := operationSpecFromMethodology(action, operation, operationsByName)
		if err != nil {
			return model.Action{}, err
		}
		name := operationResultName(spec)
		if _, ok := seenOperations[name]; ok {
			return model.Action{}, fmt.Errorf("операция %q задана повторно", name)
		}
		seenOperations[name] = struct{}{}
		operations = append(operations, spec)
	}
	if len(operations) == 0 {
		return model.Action{}, fmt.Errorf("не задан список операций")
	}

	return model.Action{
		Name:                   strings.TrimSpace(action.Name),
		Class:                  model.ActionClass(strings.TrimSpace(action.Class)),
		Profile:                strings.TrimSpace(action.Profile),
		StructuredOutputFields: structuredOutputFields,
		ExpectedResult:         strings.TrimSpace(action.ExpectedResult),
		RequiresWorkplace:      actionRequiresWorkplace(action, operations),
		Operations:             operations,
		OutputFields:           actionContractFieldNames(action.Contract.Out),
		RequiredOut:            requiredActionContractFields(action.Contract.Out),
	}, nil
}

func normalizeStructuredOutputFields(fields []string) ([]string, error) {
	if fields == nil {
		return nil, nil
	}

	allowed := map[string]struct{}{
		"summary": {}, "commit_message": {}, "remarks": {}, "review_responses": {},
		"questions": {}, "follow_up_actions": {}, "changes": {}, "commands": {},
		"conclusion": {}, "extensions": {},
	}
	normalized := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for index, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("поле с индексом %d должно быть непустым", index)
		}
		if _, ok := allowed[field]; !ok {
			return nil, fmt.Errorf("поле %q не поддерживается", field)
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		normalized = append(normalized, field)
	}
	return normalized, nil
}

func operationSpecFromMethodology(action methodology.Action, operation methodology.ActionOperation, operationsByName map[string]methodology.Operation) (model.OperationSpec, error) {
	name := strings.TrimSpace(operation.Name)
	if name == "" {
		return model.OperationSpec{}, fmt.Errorf("операция должна задавать name")
	}
	resolvedOperation, ok := operationsByName[name]
	var defaultSpec model.OperationSpec
	if ok {
		resolvedOperation = normalizeMethodologyOperation(resolvedOperation)
		defaultSpec = model.OperationSpec{
			Name:       resolvedOperation.Name,
			Type:       model.OperationType(resolvedOperation.Type),
			Kind:       model.OperationKind(resolvedOperation.Kind),
			Title:      resolvedOperation.Title,
			Required:   resolvedOperation.Required != nil && *resolvedOperation.Required,
			RequiredIn: requiredOperationInputFields(resolvedOperation.Contract),
		}
	} else {
		defaultSpec = defaultOperationSpec(name)
	}
	if strings.TrimSpace(defaultSpec.Name) == "" {
		defaultSpec.Name = name
		defaultSpec.Type = model.OperationType(OperationTypeBuiltin)
		defaultSpec.Kind = model.OperationKind(name)
		defaultSpec.Required = true
	}
	defaultSpec.Name = name
	if title := strings.TrimSpace(operation.Title); title != "" {
		defaultSpec.Title = title
	}
	defaultSpec.In = operationMappingFromMethodology(operation.In)
	defaultSpec.Out = operationMappingFromMethodology(operation.Out)
	if operation.Required == nil && strings.TrimSpace(action.Name) == ActionApplyReviewComments && string(defaultSpec.Kind) == OperationKindLoadReviewRemarks {
		defaultSpec.Required = true
	}
	if operation.Required != nil {
		defaultSpec.Required = *operation.Required
	}
	return defaultSpec, nil
}

func requiredOperationInputFields(contract methodology.OperationContract) []string {
	result := make([]string, 0, len(contract.In))
	for name, field := range contract.In {
		if field.Required != nil && *field.Required {
			result = append(result, strings.TrimSpace(name))
		}
	}
	sort.Strings(result)
	return result
}

func operationMappingFromMethodology(mappings map[string]methodology.ActionMapping) model.OperationMap {
	if len(mappings) == 0 {
		return nil
	}
	result := make(model.OperationMap, len(mappings))
	for name, mapping := range mappings {
		field := model.OperationMapping{}
		if mapping.Ref != nil {
			field.Ref = strings.TrimSpace(*mapping.Ref)
		}
		if mapping.Value != nil {
			field.Value = append(json.RawMessage(nil), (*mapping.Value)...)
		}
		result[strings.TrimSpace(name)] = field
	}
	return result
}

func normalizeMethodologyOperation(operation methodology.Operation) methodology.Operation {
	operation.Name = strings.TrimSpace(operation.Name)
	operation.Name = strings.TrimSpace(strings.ToLower(operation.Name))
	operation.Type = strings.TrimSpace(strings.ToLower(operation.Type))
	operation.Kind = strings.TrimSpace(operation.Kind)
	operation.Kind = strings.TrimSpace(strings.ToLower(operation.Kind))
	if operation.Name == "" {
		operation.Name = operation.Kind
	}
	if operation.Kind == "" {
		operation.Kind = operation.Name
	}
	operation.Title = strings.TrimSpace(operation.Title)
	return operation
}

func actionContractFieldNames(fields map[string]methodology.ActionContractField) []string {
	result := make([]string, 0, len(fields))
	for name := range fields {
		result = append(result, strings.TrimSpace(name))
	}
	sort.Strings(result)
	return result
}

func requiredActionContractFields(fields map[string]methodology.ActionContractField) []string {
	result := make([]string, 0, len(fields))
	for name, field := range fields {
		if field.Required != nil && *field.Required {
			result = append(result, strings.TrimSpace(name))
		}
	}
	sort.Strings(result)
	return result
}

func defaultOperationSpec(kind string) model.OperationSpec {
	switch strings.TrimSpace(kind) {
	case OperationKindPrepareData:
		return builtinOperation(OperationKindPrepareData, "Подготовка данных", true)
	case OperationKindLoadReviewRemarks:
		return builtinOperation(OperationKindLoadReviewRemarks, "Получение замечаний ревизии", false)
	case OperationKindResolveProfile:
		return builtinOperation(OperationKindResolveProfile, "Выбор исполнительного профиля", true)
	case OperationKindAllocateResources:
		return builtinOperation(OperationKindAllocateResources, "Ресурсное снабжение", true)
	case OperationKindPrepareWorkplace:
		return builtinOperation(OperationKindPrepareWorkplace, "Подготовка рабочего места", true)
	case OperationKindBuildDirective:
		return builtinOperation(OperationKindBuildDirective, "Сборка исполнительной директивы", true)
	case OperationKindBuildPrompt:
		return builtinOperation(OperationKindBuildPrompt, "Сборка исполнительной директивы", true)
	case OperationKindLaunchSynthesis:
		return builtinOperation(OperationKindLaunchSynthesis, "Запуск синтеза", true)
	case OperationKindParseResult:
		return builtinOperation(OperationKindParseResult, "Разбор результата", true)
	case OperationKindCommitPush:
		return builtinOperation(OperationKindCommitPush, "Создание коммита и отправка ветки", true)
	case OperationKindRebase:
		return builtinOperation(OperationKindRebase, "Безопасное перебазирование ветки", true)
	case OperationKindResolveMergeConflict:
		return builtinOperation(OperationKindResolveMergeConflict, "Завершение разрешения конфликта запроса на слияние", true)
	case OperationKindPublishMergeRequest:
		return builtinOperation(OperationKindPublishMergeRequest, "Открытие запроса на слияние", true)
	case OperationKindPublishReviewRemarks:
		return builtinOperation(OperationKindPublishReviewRemarks, "Запись замечаний ревизии", true)
	case OperationKindPublishReviewResponses:
		return builtinOperation(OperationKindPublishReviewResponses, "Запись ответов на замечания", true)
	case OperationKindFinalize:
		return builtinOperation(OperationKindFinalize, "Завершающая фиксация", true)
	default:
		return model.OperationSpec{}
	}
}

func actionRequiresWorkplace(action methodology.Action, operations []model.OperationSpec) bool {
	if action.RequiresWorkplace != nil {
		return *action.RequiresWorkplace
	}
	for _, operation := range operations {
		switch operationKind(operation) {
		case OperationKindPrepareWorkplace, OperationKindCommitPush, OperationKindRebase, OperationKindResolveMergeConflict, OperationKindPublishMergeRequest, OperationKindPublishReviewRemarks, OperationKindPublishReviewResponses:
			return true
		}
	}
	return false
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
			Type:     operation.Type,
			Kind:     operation.Kind,
			Title:    operation.Title,
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

func (t *operationTracker) skipIO(name string, input string, output string, summary string) {
	t.set(name, model.OperationStatus(OperationStatusSkipped), input, output, summary, nil)
}

func (t *operationTracker) fail(name string, summary string, err error, code string, retryable bool, manualIntervention bool) {
	t.set(name, model.OperationStatus(OperationStatusFailed), "", "", summary, executionFailure(code, err, retryable, manualIntervention))
}

func (t *operationTracker) set(name string, status model.OperationStatus, input string, output string, summary string, failure *model.Failure) {
	index, ok := t.index[name]
	if !ok {
		index = len(t.results)
		t.index[name] = index
		t.results = append(t.results, model.OperationResult{Name: name, Type: model.OperationType(OperationTypeBuiltin), Required: true})
	}

	t.results[index].Status = status
	t.results[index].Input = strings.TrimSpace(input)
	t.results[index].Output = strings.TrimSpace(output)
	t.results[index].Summary = strings.TrimSpace(summary)
	t.results[index].Failure = failure
}

func (t *operationTracker) setOperations(name string, operations []model.OperationResult) {
	if index, ok := t.index[name]; ok {
		t.results[index].Operations = append([]model.OperationResult(nil), operations...)
	}
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

func executionResultFromLaunch(assignment *model.ExecutionAssignment, action model.Action, operations []model.OperationResult, mergeRequest *model.MergeRequest, result model.LaunchResult, err error) model.ExecutionResult {
	status := strings.TrimSpace(result.Status)
	if err != nil {
		status = "failed"
	} else if status == "" {
		status = "completed"
	}
	executionResult := model.ExecutionResult{
		Status:          status,
		Summary:         strings.TrimSpace(result.Summary),
		Assignment:      cloneAssignment(assignment),
		Action:          action,
		MergeRequest:    mergeRequest,
		Operations:      append([]model.OperationResult(nil), operations...),
		Artifacts:       executionArtifacts(result),
		DiagnosticLinks: executionDiagnosticLinks(result),
		Launch:          &result,
	}
	if err != nil {
		executionResult.Failure = executionFailure(executionFailureCode(err, operations), err, false, true)
	}

	return executionResult
}

func builtinOperation(kind string, title string, required bool) model.OperationSpec {
	return model.OperationSpec{
		Name:     kind,
		Type:     model.OperationType(OperationTypeBuiltin),
		Kind:     model.OperationKind(kind),
		Title:    title,
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
	cloned.OutputFields = append([]string(nil), action.OutputFields...)
	cloned.RequiredOut = append([]string(nil), action.RequiredOut...)
	return cloned
}

func actionResolutionFailureCode(err error) string {
	var resolutionErr actionResolutionError
	if !errors.As(err, &resolutionErr) {
		return ""
	}
	if code := strings.TrimSpace(resolutionErr.code); code != "" {
		return code
	}
	return "action_not_found"
}

func executionFailureCode(err error, operations []model.OperationResult) string {
	for _, operation := range operations {
		if operation.Failure != nil && strings.TrimSpace(operation.Failure.Code) != "" {
			return operation.Failure.Code
		}
	}
	if code := actionResolutionFailureCode(err); code != "" {
		return code
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
