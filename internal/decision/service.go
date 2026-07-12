package decision

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

type integrationExecutor interface {
	Execute(context.Context, integration.Request) (integration.Response, error)
}

type executionStarter interface {
	ExecuteAction(context.Context, execution.ActionInvocation) (execution.ExecutionResult, error)
}

type Service struct {
	logger          *log.Logger
	integration     integrationExecutor
	execution       executionStarter
	resolveRepo     func(context.Context) (string, error)
	resolveRepoRoot func(context.Context) (string, error)
	readFile        func(string) ([]byte, error)
}

func NewService(logger *log.Logger) *Service {
	logger = ensureLogger(logger)

	return &Service{
		logger:          logger,
		integration:     integration.NewConfiguredService(logger),
		execution:       execution.NewService(logger),
		resolveRepo:     resolveCurrentGitHubRepository,
		resolveRepoRoot: resolveDecisionRepoRoot,
		readFile:        os.ReadFile,
	}
}

func (s *Service) Start(ctx context.Context, input StartInput) (StartResult, error) {
	if input.TaskNumber <= 0 {
		return StartResult{}, fmt.Errorf("task number must be greater than zero")
	}

	signal := Signal{
		Source:     SignalSourceTask,
		Kind:       SignalKindTask,
		TaskNumber: input.TaskNumber,
	}

	response, err := s.integration.Execute(ctx, integration.Request{
		System:    "github",
		Resource:  "issue",
		Operation: "get",
		ID:        strconv.Itoa(input.TaskNumber),
	})
	if err != nil {
		return StartResult{}, err
	}
	if response.Issue == nil {
		return StartResult{}, fmt.Errorf("integration did not return issue for task %d", input.TaskNumber)
	}

	mergeRequest, mergeRequestErr := s.findTaskMergeRequest(ctx, response.Issue)
	if mergeRequestErr != nil {
		s.logger.Printf("Не удалось восстановить связанный запрос на слияние: задача=%d ошибка=%v", input.TaskNumber, mergeRequestErr)
	}

	decisionContext := DecisionContext{
		Signal:       signal,
		Task:         canonicalTaskFromIssue(response.Issue),
		Issue:        response.Issue,
		MergeRequest: mergeRequest,
	}
	consideration, err := s.Consider(ctx, ConsiderationInput{Context: decisionContext, Route: input.Route})
	if err != nil {
		if mergeRequestErr != nil && consideration.Failure != nil && consideration.Failure.Code == "merge_request_missing" {
			err = fmt.Errorf("восстановить связанный запрос на слияние для задачи %d: %w", input.TaskNumber, mergeRequestErr)
		}
		return StartResult{
			Context:       decisionContext,
			Consideration: &consideration,
		}, err
	}
	if consideration.Status == ConsiderationStatusCompleted && consideration.ExecutionPlan == nil && mergeRequest != nil {
		externalState, err := s.loadMergeRequestExternalState(ctx, mergeRequest)
		if err != nil {
			return StartResult{Context: decisionContext, Consideration: &consideration}, fmt.Errorf("восстановить внешнее состояние запроса на слияние для задачи %d: %w", input.TaskNumber, err)
		}
		if externalState != nil {
			decisionContext.MergeRequestExternalState = externalState
			consideration, err = s.Consider(ctx, ConsiderationInput{Context: decisionContext})
			if err != nil {
				return StartResult{Context: decisionContext, Consideration: &consideration}, err
			}
		}
	}

	decision := decisionFromConsideration(consideration)
	result := StartResult{
		Context:       decisionContext,
		Ready:         consideration.Status != ConsiderationStatusFailed,
		Consideration: &consideration,
		Decision:      &decision,
	}
	if decision.ExecutionPlan == nil {
		s.logger.Printf("Рассмотрение задачи завершено без запуска: задача=%d исход=%q", input.TaskNumber, consideration.Status)
		return result, nil
	}

	executionResult, err := s.execution.ExecuteAction(ctx, executionActionInvocationFromDecisionPlan(decision.ExecutionPlan))
	result.ExecutionResult = &executionResult
	if err != nil {
		if executionResult.Launch != nil && (executionResult.Launch.Status != "" || strings.TrimSpace(executionResult.Launch.Summary) != "" || executionResult.Launch.StructuredOutput != nil) {
			result.Execution = executionResult.Launch
		}

		return result, err
	}
	result.Execution = executionResult.Launch

	s.logger.Printf("Контекст решения собран: задача=%d готовность=%t решение=%q", input.TaskNumber, result.Ready, decision.Type)
	return result, nil
}

func (s *Service) findTaskMergeRequest(ctx context.Context, issue *integration.TrackerIssue) (*integration.MergeRequest, error) {
	if issue == nil || strings.TrimSpace(issue.ID) == "" || strings.TrimSpace(issue.Repository) == "" {
		return nil, nil
	}

	head := issue.ID
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeRepository,
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "search",
		Repository:      issue.Repository,
		RepoProvided:    true,
		Query:           "head:" + head,
		State:           "open",
		Limit:           100,
	})
	if err != nil {
		return nil, err
	}

	for _, mergeRequest := range response.MergeRequests {
		if strings.TrimSpace(mergeRequest.HeadRef) != head {
			continue
		}
		copyOfMergeRequest := mergeRequest
		return &copyOfMergeRequest, nil
	}

	return nil, nil
}

func taskLabelsRequireMergeRequest(labels []string) bool {
	return hasLabel(labels, "Ожидает экспертизы") || hasLabel(labels, "Требует доработки") || hasLabel(labels, "Экспертиза пройдена")
}

func (s *Service) loadMergeRequestExternalState(ctx context.Context, mergeRequest *integration.MergeRequest) (*MergeRequestExternalState, error) {
	if mergeRequest == nil {
		return nil, nil
	}

	state := &MergeRequestExternalState{HasMergeConflict: mergeRequestHasConflict(mergeRequest)}
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType:    integrationmodel.IntegrationTypeRepository,
		Resource:           "review-remark",
		ObjectType:         "review-remark",
		Operation:          "list",
		Repository:         mergeRequest.Repository,
		RepoProvided:       strings.TrimSpace(mergeRequest.Repository) != "",
		MergeRequestNumber: mergeRequest.Number,
	})
	if err != nil {
		if state.HasMergeConflict {
			return state, nil
		}
		return nil, err
	}
	state.ReviewRemarks = append([]integration.ReviewRemark(nil), response.ReviewRemarks...)
	state.HasUnresolvedReviewRemarks = hasUnresolvedExternalReviewRemarks(response.ReviewRemarks)
	if !state.HasMergeConflict && !state.HasUnresolvedReviewRemarks {
		return nil, nil
	}
	return state, nil
}

func hasUnresolvedExternalReviewRemarks(remarks []integration.ReviewRemark) bool {
	respondedRemarkIDs := map[string]struct{}{}
	for _, remark := range remarks {
		id := externalReviewRemarkReferenceID(remark.Body)
		if id != "" && (isResolvedExternalReviewResponse(remark.Body) || isResolvedExternalReviewRemark(remark.Body)) {
			respondedRemarkIDs[id] = struct{}{}
		}
	}

	for _, remark := range remarks {
		if isExternalReviewConclusion(remark.Body) {
			if isApprovedExternalReviewConclusion(remark.Body) {
				continue
			}
			// Неизвестное заключение обрабатывается как нерешённое.
			return true
		}
		if strings.TrimSpace(remark.ReplyToID) == "" {
			if strings.Contains(remark.Body, "## Ответ на замечание ревизии") {
				continue
			}
			if id := externalReviewRemarkReferenceID(remark.Body); id != "" {
				if _, ok := respondedRemarkIDs[id]; ok || isResolvedExternalReviewRemark(remark.Body) {
					continue
				}
			}
			return true
		}
		state := strings.ToLower(strings.TrimSpace(remark.State))
		switch state {
		case "", "resolved", "fixed", "done", "closed", "outdated":
			continue
		default:
			return true
		}
	}
	return false
}

func isExternalReviewConclusion(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line == "## Заключение ревизии"
	}
	return false
}

func externalReviewConclusionStatus(body string) string {
	seenHeader := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "## Заключение ревизии" {
			seenHeader = true
			continue
		}
		if !seenHeader || line == "" {
			continue
		}
		if strings.HasPrefix(line, "Статус:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "Статус:"))
		}
		status := strings.ToLower(strings.ReplaceAll(line, "_", "-"))
		switch status {
		case "ok", "ready", "passed", "approved", "approve", "success", "completed",
			"request-changes", "changes-requested", "needs-work", "needs-rework", "needs-follow-up", "rejected", "failed", "blocked":
			return status
		}
	}
	return ""
}

func isApprovedExternalReviewConclusion(body string) bool {
	switch externalReviewConclusionStatus(body) {
	case "ok", "ready", "passed", "approved", "approve", "success", "completed":
		return true
	default:
		return false
	}
}

func isReworkExternalReviewConclusion(body string) bool {
	switch externalReviewConclusionStatus(body) {
	case "request-changes", "changes-requested", "needs-work", "needs-rework", "needs-follow-up", "rejected", "failed", "blocked":
		return true
	default:
		return false
	}
}

func isResolvedExternalReviewRemark(body string) bool {
	if !strings.Contains(body, "## Замечание ревизии") {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(externalReviewRemarkID(body, "Состояние:")))
	switch state {
	case "resolved", "fixed", "done", "ok", "closed", "outdated":
		return true
	}
	return false
}

func isResolvedExternalReviewResponse(body string) bool {
	if !strings.Contains(body, "## Ответ на замечание ревизии") {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(externalReviewRemarkID(body, "Состояние:")))
	switch state {
	case "resolved", "fixed", "done", "ok", "closed", "outdated":
		return true
	default:
		return false
	}
}

func externalReviewRemarkID(body string, prefix string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func externalReviewRemarkReferenceID(body string) string {
	for _, prefix := range []string{"Идентификатор:", "Замечание:"} {
		if id := externalReviewRemarkID(body, prefix); id != "" {
			return id
		}
	}
	return ""
}

func mergeRequestHasConflict(mergeRequest *integration.MergeRequest) bool {
	if mergeRequest == nil {
		return false
	}
	for key, value := range mergeRequest.Attributes {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "has_merge_conflict", "merge_conflict", "conflict", "conflicted":
			if isTruthyExternalState(value) {
				return true
			}
		case "mergeable", "can_merge":
			if isFalsyExternalState(value) || isConflictMergeState(value) {
				return true
			}
		case "mergeable_state", "merge_state", "merge_state_status":
			if isConflictMergeState(value) {
				return true
			}
		}
	}
	return false
}

func isConflictMergeState(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "conflict", "conflicted", "conflicting", "dirty", "has-conflicts", "has_conflicts", "merge-conflict", "merge_conflict":
		return true
	default:
		return false
	}
}

func isTruthyExternalState(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func isFalsyExternalState(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "f", "false", "no", "n", "off":
		return true
	default:
		return false
	}
}

func hasLabel(labels []string, candidate string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	for _, label := range labels {
		if strings.ToLower(strings.TrimSpace(label)) == candidate {
			return true
		}
	}
	return false
}

func (s *Service) Consider(ctx context.Context, input ConsiderationInput) (ConsiderationResult, error) {
	result := ConsiderationResult{Context: input.Context}
	if input.Context.Issue == nil {
		err := fmt.Errorf("decision context issue is required")
		result.Status = ConsiderationStatusFailed
		result.Failure = decisionFailure("missing_issue", err, false, true)
		return result, err
	}
	if strings.TrimSpace(result.Context.Task.ID) == "" {
		result.Context.Task = canonicalTaskFromIssue(input.Context.Issue)
	}

	route, err := s.selectWorkflowRoute(ctx, result.Context.Task, input.Route)
	if err != nil {
		result.Status = ConsiderationStatusFailed
		failureCode := "route_resolution_failed"
		if coded, ok := err.(interface{ Code() string }); ok && strings.TrimSpace(coded.Code()) != "" {
			failureCode = coded.Code()
		}
		result.Failure = decisionFailure(failureCode, err, false, true)
		return result, err
	}

	result.Route = route.Route
	result.RouteSource = route.RouteSource
	result.CheckSources = route.CheckSources
	result.Checks = route.Checks
	if strings.TrimSpace(route.Action) == execution.ActionStartImplementationPR && isOpenMergeRequest(input.Context.MergeRequest) {
		route = routeForExistingMergeRequest(route)
		if externalMergeRequestBlocksCompletion(input.Context.MergeRequestExternalState) {
			route = reworkRouteForExternalMergeRequestState(route, input.Context.MergeRequestExternalState)
		}
		result.Route = route.Route
		result.Checks = route.Checks
	}
	if externalMergeRequestBlocksCompletion(input.Context.MergeRequestExternalState) && strings.TrimSpace(route.Outcome) != "" {
		route = reworkRouteForExternalMergeRequestState(route, input.Context.MergeRequestExternalState)
		result.Route = route.Route
		result.Checks = route.Checks
	}
	if strings.TrimSpace(route.Outcome) != "" && strings.TrimSpace(route.Action) == "" {
		result.Status = ConsiderationStatusCompleted
		result.Reasons = []DecisionReason{decisionReasonFromRoute(route, "route_completed", "Маршрут обработки завершён; следующая операция не требуется.")}
		return result, nil
	}
	if requiresMergeRequest(route.Action) && input.Context.MergeRequest == nil {
		err := fmt.Errorf("merge request is required for action %q", route.Action)
		result.Status = ConsiderationStatusManualIntervention
		result.Failure = decisionFailure("merge_request_missing", err, false, true)
		result.Reasons = []DecisionReason{decisionReasonFromRoute(route, "merge_request_required", "Для выбранного действия требуется связанный запрос на слияние.")}
		return result, err
	}

	decision := buildExecuteDecision(result.Context, route)
	result.Status = ConsiderationStatusExecution
	result.Reasons = append([]DecisionReason(nil), decision.Reasons...)
	result.ExecutionPlan = decision.ExecutionPlan
	return result, nil
}

func isOpenMergeRequest(mergeRequest *integration.MergeRequest) bool {
	if mergeRequest == nil {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(mergeRequest.State)) {
	case "", "open":
		return true
	default:
		return false
	}
}

func routeForExistingMergeRequest(previous selectedWorkflowRoute) selectedWorkflowRoute {
	previous.Action = execution.ActionReviewPullRequest
	previous.ExpectedResult = "Проверить существующий открытый запрос на слияние и продолжить обработку его состояния."
	previous.ReasonCode = "open_merge_request_found"
	previous.ReasonMessage = "Для рабочей ветки уже найден открытый запрос на слияние; повторное создание запрещено."
	previous.Checks = append(append([]RouteCheckResult(nil), previous.Checks...), RouteCheckResult{
		Name:   "open-merge-request-invariant",
		Status: RouteCheckStatusPassed,
		Reasons: []DecisionReason{{
			Code:    previous.ReasonCode,
			Message: previous.ReasonMessage,
		}},
	})
	return previous
}

func externalMergeRequestBlocksCompletion(state *MergeRequestExternalState) bool {
	return state != nil && (state.HasUnresolvedReviewRemarks || state.HasMergeConflict)
}

func reworkRouteForExternalMergeRequestState(previous selectedWorkflowRoute, state *MergeRequestExternalState) selectedWorkflowRoute {
	reasonCode := "external_pr_requires_rework"
	reasonMessage := "Актуальное внешнее состояние запроса на слияние требует доработки."
	if state != nil && state.HasUnresolvedReviewRemarks && !state.HasMergeConflict {
		reasonCode = "external_review_remarks_unresolved"
		reasonMessage = "В запросе на слияние есть нерешённые внешние замечания ревизии; задача возвращена в доработку."
	} else if state != nil && state.HasMergeConflict && !state.HasUnresolvedReviewRemarks {
		reasonCode = "merge_request_conflict"
		reasonMessage = "Запрос на слияние находится в конфликтном состоянии; задача возвращена в доработку."
	}

	return selectedWorkflowRoute{
		Action:         execution.ActionApplyReviewComments,
		ExpectedResult: "Получить актуальные внешние замечания или состояние конфликта запроса на слияние, доработать ветку и отправить результат на повторную экспертизу.",
		Constraints: []string{
			"Работать в отдельном исполнительном рабочем месте.",
			"Сохранять имя ветки по номеру задачи.",
			"После доработки задача должна быть повторно направлена на экспертизу.",
		},
		ReasonCode:    reasonCode,
		ReasonMessage: reasonMessage,
		Route: ProcessingRoute{
			Name:        "task-processing-external-pr-rework",
			Title:       "Доработка внешнего состояния запроса на слияние",
			Description: "Направляет задачу на доработку, если завершённая задача имеет открытые внешние препятствия в запросе на слияние.",
		},
		Checks: append(append([]RouteCheckResult(nil), previous.Checks...), externalMergeRequestStateCheck(previous, state, reasonCode, reasonMessage)),
	}
}

func externalMergeRequestStateCheck(previous selectedWorkflowRoute, state *MergeRequestExternalState, reasonCode string, reasonMessage string) RouteCheckResult {
	name := "external-merge-request-state"
	if strings.TrimSpace(previous.Route.Name) != "" {
		name = previous.Route.Name + ":external-merge-request-state"
	}
	return RouteCheckResult{
		Name:   name,
		Status: RouteCheckStatusPassed,
		Reasons: []DecisionReason{{
			Code:    reasonCode,
			Message: reasonMessage,
		}},
	}
}

func (s *Service) validateIssueRepository(ctx context.Context, issue *integration.TrackerIssue) error {
	if issue == nil {
		return nil
	}

	issueRepository, err := normalizeGitHubRepository(issue.Repository)
	if err != nil {
		return fmt.Errorf("issue repository must use owner/name format: %w", err)
	}

	currentRepository, err := s.resolveCurrentRepository(ctx)
	if err != nil {
		return err
	}
	if currentRepository != issueRepository {
		return fmt.Errorf("issue repository %q does not match current repository %q", issueRepository, currentRepository)
	}

	return nil
}

func (s *Service) resolveCurrentRepository(ctx context.Context) (string, error) {
	if s.resolveRepo == nil {
		s.resolveRepo = resolveCurrentGitHubRepository
	}

	repository, err := s.resolveRepo(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve current repository for execution handoff: %w", err)
	}

	normalized, err := normalizeGitHubRepository(repository)
	if err != nil {
		return "", fmt.Errorf("resolve current repository for execution handoff: %w", err)
	}

	return normalized, nil
}

func buildExecuteDecision(context DecisionContext, route selectedWorkflowRoute) Decision {
	task := context.Task
	issue := context.Issue
	prompt := buildExecutionTask(issue)
	if strings.TrimSpace(route.Action) == "" {
		route.Action = execution.ActionStartImplementationPR
	}
	if strings.TrimSpace(route.ExpectedResult) == "" {
		route.ExpectedResult = "Выполнить выбранное действие и вернуть диагностируемый результат."
	}
	if strings.TrimSpace(route.ReasonCode) == "" {
		route.ReasonCode = "issue_context_ready"
	}
	if strings.TrimSpace(route.ReasonMessage) == "" {
		route.ReasonMessage = "Контекст задачи готов к передаче в контур исполнения."
	}
	reasons := []DecisionReason{{
		Code:    route.ReasonCode,
		Message: route.ReasonMessage,
	}}
	routeRef := route.Route
	if strings.TrimSpace(routeRef.Name) == "" {
		routeRef.Name = "default"
	}
	if strings.TrimSpace(routeRef.Title) == "" {
		routeRef.Title = route.Action
	}

	structuredInput := &execution.StructuredInput{
		Task:        prompt,
		Constraints: append([]string(nil), route.Constraints...),
	}
	assignment := &execution.ExecutionAssignment{
		Action:          route.Action,
		ExpectedResult:  route.ExpectedResult,
		Constraints:     append([]string(nil), route.Constraints...),
		CanonicalTask:   executionObjectRefFromCanonicalTask(task),
		RelatedObjects:  executionRelatedObjectsFromDecisionContext(context),
		Reasons:         executionReasonsFromDecisionReasons(reasons),
		StructuredInput: structuredInput,
	}

	return Decision{
		Type:    DecisionType(DecisionTypeExecute),
		Status:  ConsiderationStatusExecution,
		Route:   routeRef,
		Checks:  append([]RouteCheckResult(nil), route.Checks...),
		Reasons: reasons,
		ExecutionPlan: &ExecutionPlan{
			TaskNumber:      numericIssueID(issue.ID),
			TaskTitle:       issue.Title,
			Action:          route.Action,
			ExpectedResult:  route.ExpectedResult,
			Constraints:     append([]string(nil), route.Constraints...),
			Route:           routeRef,
			Reasons:         append([]DecisionReason(nil), reasons...),
			Assignment:      assignment,
			StructuredInput: structuredInput,
		},
	}
}

func decisionFromConsideration(result ConsiderationResult) Decision {
	decision := Decision{
		Status:        result.Status,
		Route:         result.Route,
		Checks:        append([]RouteCheckResult(nil), result.Checks...),
		Reasons:       append([]DecisionReason(nil), result.Reasons...),
		ExecutionPlan: result.ExecutionPlan,
		Failure:       result.Failure,
	}
	if result.ExecutionPlan != nil {
		decision.Type = DecisionType(DecisionTypeExecute)
	} else if result.Status == ConsiderationStatusCompleted {
		decision.Type = DecisionType(DecisionTypeNone)
	}
	return decision
}

func executionActionInvocationFromDecisionPlan(plan *ExecutionPlan) execution.ActionInvocation {
	if plan == nil {
		return execution.ActionInvocation{}
	}

	return execution.ActionInvocation{Assignment: plan.Assignment}
}

func canonicalTaskFromIssue(issue *integration.TrackerIssue) integration.CanonicalTask {
	if issue == nil {
		return integration.CanonicalTask{}
	}

	attributes := make(map[string]string)
	if state := strings.TrimSpace(issue.State); state != "" {
		attributes["state"] = state
	}
	if repository := strings.TrimSpace(issue.Repository); repository != "" {
		attributes["repository"] = repository
	}
	if body := strings.TrimSpace(issue.Body); body != "" {
		attributes["body"] = body
	}

	return integration.CanonicalTask{
		System:     strings.TrimSpace(issue.System),
		Repository: strings.TrimSpace(issue.Repository),
		ID:         issue.ID,
		ExternalID: issue.ExternalID,
		Title:      strings.TrimSpace(issue.Title),
		Body:       strings.TrimSpace(issue.Body),
		State:      strings.TrimSpace(issue.State),
		URL:        strings.TrimSpace(issue.URL),
		Traits:     normalizeFeatures(issue.Labels),
		Attributes: attributes,
	}
}

func decisionReasonFromRoute(route selectedWorkflowRoute, defaultCode string, defaultMessage string) DecisionReason {
	return DecisionReason{
		Code:    firstNonEmpty(strings.TrimSpace(route.ReasonCode), defaultCode),
		Message: firstNonEmpty(strings.TrimSpace(route.ReasonMessage), defaultMessage),
	}
}

func requiresMergeRequest(action string) bool {
	switch strings.TrimSpace(action) {
	case execution.ActionReviewPullRequest, execution.ActionApplyReviewComments:
		return true
	default:
		return false
	}
}

func executionRelatedObjectsFromDecisionContext(context DecisionContext) []execution.ObjectRef {
	if context.MergeRequest == nil {
		return nil
	}

	mergeRequest := context.MergeRequest
	attributes := map[string]string{}
	if value := strings.TrimSpace(mergeRequest.BaseRef); value != "" {
		attributes["base_ref"] = value
	}
	if value := strings.TrimSpace(mergeRequest.HeadRef); value != "" {
		attributes["head_ref"] = value
	}
	if value := strings.TrimSpace(mergeRequest.Body); value != "" {
		attributes["body"] = value
	}
	if len(attributes) == 0 {
		attributes = nil
	}

	return []execution.ObjectRef{{
		Type:       "merge-request",
		System:     strings.TrimSpace(mergeRequest.System),
		Repository: strings.TrimSpace(mergeRequest.Repository),
		Number:     mergeRequest.Number,
		Title:      strings.TrimSpace(mergeRequest.Title),
		URL:        strings.TrimSpace(mergeRequest.URL),
		Attributes: attributes,
	}}
}

func executionObjectRefFromCanonicalTask(task integration.CanonicalTask) *execution.ObjectRef {
	if strings.TrimSpace(task.ID) == "" && strings.TrimSpace(task.Title) == "" && strings.TrimSpace(task.URL) == "" {
		return nil
	}

	return &execution.ObjectRef{
		Type:       "task",
		System:     strings.TrimSpace(task.System),
		Repository: strings.TrimSpace(task.Repository),
		Number:     numericIssueID(task.ID),
		Title:      strings.TrimSpace(task.Title),
		URL:        strings.TrimSpace(task.URL),
		Attributes: cloneStringMap(task.Attributes),
	}
}

func executionReasonsFromDecisionReasons(reasons []DecisionReason) []execution.AssignmentReason {
	if len(reasons) == 0 {
		return nil
	}

	result := make([]execution.AssignmentReason, 0, len(reasons))
	for _, reason := range reasons {
		if strings.TrimSpace(reason.Code) == "" && strings.TrimSpace(reason.Message) == "" {
			continue
		}
		result = append(result, execution.AssignmentReason{
			Code:    strings.TrimSpace(reason.Code),
			Message: strings.TrimSpace(reason.Message),
		})
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" && value == "" {
			continue
		}
		result[key] = value
	}
	return result
}

func decisionFailure(code string, err error, retryable bool, manualIntervention bool) *DecisionFailure {
	if err == nil {
		return nil
	}

	return &DecisionFailure{
		Code:               code,
		Message:            strings.TrimSpace(err.Error()),
		Retryable:          retryable,
		ManualIntervention: manualIntervention,
	}
}

func buildExecutionTask(issue *integration.TrackerIssue) string {
	lines := []string{
		fmt.Sprintf("Task #%s: %s", issue.ID, strings.TrimSpace(issue.Title)),
	}

	if repository := strings.TrimSpace(issue.Repository); repository != "" {
		lines = append(lines, fmt.Sprintf("Repository: %s", repository))
	}
	if issueURL := strings.TrimSpace(issue.URL); issueURL != "" {
		lines = append(lines, fmt.Sprintf("Issue: %s", issueURL))
	}
	if state := strings.TrimSpace(issue.State); state != "" {
		lines = append(lines, fmt.Sprintf("State: %s", state))
	}

	body := strings.TrimSpace(issue.Body)
	if body == "" {
		body = "No additional issue description was provided."
	}

	lines = append(lines,
		"",
		"Implement the task with the smallest correct change.",
		"Start by inspecting the relevant code and preserve existing behavior unless the task requires a change.",
		"",
		"Issue details:",
		body,
	)

	return strings.Join(lines, "\n")
}

func numericIssueID(id string) int {
	number, _ := strconv.Atoi(strings.TrimSpace(id))
	return number
}

func ensureLogger(logger *log.Logger) *log.Logger {
	if logger != nil {
		return logger
	}

	return log.New(io.Discard, "", 0)
}

func resolveCurrentGitHubRepository(ctx context.Context) (string, error) {
	output, err := runGitOutput(ctx, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("read git remote.origin.url: %w", err)
	}

	repository, err := parseGitHubRepositoryFromRemoteURL(strings.TrimSpace(output))
	if err != nil {
		return "", err
	}

	return repository, nil
}

func resolveDecisionRepoRoot(ctx context.Context) (string, error) {
	output, err := runGitOutput(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

func parseGitHubRepositoryFromRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("git remote.origin.url is empty")
	}

	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/", "git://github.com/"} {
		if strings.HasPrefix(raw, prefix) {
			return normalizeGitHubRepository(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), ".git"))
		}
	}

	return "", fmt.Errorf("git remote.origin.url %q is not a supported GitHub remote", raw)
}

func normalizeGitHubRepository(repository string) (string, error) {
	repository = strings.TrimSpace(repository)
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !isGitHubRepositoryPart(parts[0]) || !isGitHubRepositoryPart(parts[1]) {
		return "", fmt.Errorf("GitHub repository must use owner/name format")
	}

	return repository, nil
}

func isGitHubRepositoryPart(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, " \t\r\n")
}

func runGitOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}
