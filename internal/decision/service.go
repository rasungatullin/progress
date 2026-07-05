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
		Number:    input.TaskNumber,
	})
	if err != nil {
		return StartResult{}, err
	}
	if response.Issue == nil {
		return StartResult{}, fmt.Errorf("integration did not return issue for task %d", input.TaskNumber)
	}

	mergeRequest, err := s.findTaskMergeRequest(ctx, response.Issue)
	if err != nil {
		s.logger.Printf("Не удалось восстановить связанный запрос на слияние: задача=%d ошибка=%v", input.TaskNumber, err)
	}

	decisionContext := DecisionContext{
		Signal:       signal,
		Task:         canonicalTaskFromIssue(response.Issue),
		Issue:        response.Issue,
		MergeRequest: mergeRequest,
	}
	consideration, err := s.Consider(ctx, ConsiderationInput{Context: decisionContext})
	if err != nil {
		return StartResult{
			Context:       decisionContext,
			Consideration: &consideration,
		}, err
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
	if issue == nil || issue.Number <= 0 || strings.TrimSpace(issue.Repository) == "" {
		return nil, nil
	}

	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeRepository,
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "search",
		Repository:      issue.Repository,
		RepoProvided:    true,
		State:           "open",
		Limit:           100,
	})
	if err != nil {
		return nil, err
	}

	head := strconv.Itoa(issue.Number)
	for _, mergeRequest := range response.MergeRequests {
		if strings.TrimSpace(mergeRequest.HeadRef) != head {
			continue
		}
		copyOfMergeRequest := mergeRequest
		return &copyOfMergeRequest, nil
	}

	return nil, nil
}

func (s *Service) Consider(ctx context.Context, input ConsiderationInput) (ConsiderationResult, error) {
	result := ConsiderationResult{Context: input.Context}
	if input.Context.Issue == nil {
		err := fmt.Errorf("decision context issue is required")
		result.Status = ConsiderationStatusFailed
		result.Failure = decisionFailure("missing_issue", err, false, true)
		return result, err
	}
	if result.Context.Task.Number == 0 {
		result.Context.Task = canonicalTaskFromIssue(input.Context.Issue)
	}

	route, err := s.selectWorkflowRoute(ctx, result.Context.Task)
	if err != nil {
		result.Status = ConsiderationStatusFailed
		result.Failure = decisionFailure("route_resolution_failed", err, false, true)
		return result, err
	}

	result.Route = route.Route
	result.Checks = route.Checks
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
		route.Action = "implement"
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
			TaskNumber:      issue.Number,
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
		Number:     issue.Number,
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
	if task.Number == 0 && strings.TrimSpace(task.Title) == "" && strings.TrimSpace(task.URL) == "" {
		return nil
	}

	return &execution.ObjectRef{
		Type:       "task",
		System:     strings.TrimSpace(task.System),
		Repository: strings.TrimSpace(task.Repository),
		Number:     task.Number,
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
		fmt.Sprintf("Task #%d: %s", issue.Number, strings.TrimSpace(issue.Title)),
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
