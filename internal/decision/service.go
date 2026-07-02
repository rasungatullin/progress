package decision

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
)

const defaultExecutionProfile = "default"

type integrationExecutor interface {
	Execute(context.Context, integration.Request) (integration.Response, error)
}

type executionStarter interface {
	Start(context.Context, execution.Invocation) (execution.LaunchResult, error)
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

	decisionContext := DecisionContext{
		Signal: signal,
		Issue:  response.Issue,
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

	launchResult, err := s.execution.Start(ctx, executionInvocationFromDecisionPlan(decision.ExecutionPlan))
	if err != nil {
		if launchResult.Status != "" || strings.TrimSpace(launchResult.Summary) != "" || launchResult.StructuredOutput != nil {
			result.Execution = &launchResult
		}

		return result, err
	}
	result.Execution = &launchResult

	s.logger.Printf("Контекст решения собран: задача=%d готовность=%t решение=%q", input.TaskNumber, result.Ready, decision.Type)
	return result, nil
}

func (s *Service) Consider(ctx context.Context, input ConsiderationInput) (ConsiderationResult, error) {
	result := ConsiderationResult{Context: input.Context}
	if input.Context.Issue == nil {
		err := fmt.Errorf("decision context issue is required")
		result.Status = ConsiderationStatusFailed
		result.Failure = decisionFailure("missing_issue", err, false, true)
		return result, err
	}

	route, err := s.selectWorkflowRoute(ctx, input.Context.Issue)
	if err != nil {
		result.Status = ConsiderationStatusFailed
		result.Failure = decisionFailure("route_resolution_failed", err, false, true)
		return result, err
	}

	decision := buildExecuteDecision(input.Context.Issue, route)
	result.Status = ConsiderationStatusExecution
	result.Route = route.Route
	result.Checks = route.Checks
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

func buildExecuteDecision(issue *integration.TrackerIssue, route selectedWorkflowRoute) Decision {
	prompt := buildExecutionTask(issue)
	if strings.TrimSpace(route.Step) == "" {
		route.Step = "implement"
	}
	if strings.TrimSpace(route.Action) == "" {
		route.Action = route.Step
	}
	if strings.TrimSpace(route.Profile) == "" {
		route.Profile = defaultExecutionProfile
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
		routeRef.Title = route.Step
	}

	return Decision{
		Type:    DecisionType(DecisionTypeExecute),
		Status:  ConsiderationStatusExecution,
		Route:   routeRef,
		Checks:  append([]RouteCheckResult(nil), route.Checks...),
		Reasons: reasons,
		ExecutionPlan: &ExecutionPlan{
			TaskNumber:     issue.Number,
			TaskTitle:      issue.Title,
			Repository:     strings.TrimSpace(issue.Repository),
			Action:         route.Action,
			Step:           route.Step,
			Profile:        route.Profile,
			Prompt:         prompt,
			ExpectedResult: route.ExpectedResult,
			Constraints:    append([]string(nil), route.Constraints...),
			Route:          routeRef,
			Reasons:        append([]DecisionReason(nil), reasons...),
			StructuredInput: &execution.StructuredInput{
				Task:        prompt,
				Constraints: append([]string(nil), route.Constraints...),
			},
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
	}
	return decision
}

func executionInvocationFromDecisionPlan(plan *ExecutionPlan) execution.Invocation {
	if plan == nil {
		return execution.Invocation{}
	}

	return execution.Invocation{
		Task:       fmt.Sprintf("task-%d", plan.TaskNumber),
		Action:     plan.Action,
		Profile:    plan.Profile,
		Repository: execution.RepositorySpec{URL: plan.Repository},
		Workplace: execution.WorkplaceSpec{
			Name: fmt.Sprintf("task-%d", plan.TaskNumber),
		},
		Launch: execution.LaunchSpec{
			StructuredInput:  plan.StructuredInput,
			StructuredOutput: true,
		},
	}
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
