package decision

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
)

func TestServiceStartBuildsExecuteDecisionAndLaunchesExecution(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Title:      "Implement decision start",
				State:      "OPEN",
				URL:        "https://github.com/owner/name/issues/123",
			},
		},
	}
	executionStub := &stubExecutionStarter{
		result: execution.LaunchResult{Status: "completed", Summary: "execution launched"},
	}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 123})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !result.Ready {
		t.Fatal("expected ready result")
	}
	if result.Context.Signal.Source != SignalSourceTask {
		t.Fatalf("unexpected signal source: %q", result.Context.Signal.Source)
	}
	if result.Context.Signal.Kind != SignalKindTask {
		t.Fatalf("unexpected signal kind: %q", result.Context.Signal.Kind)
	}
	if result.Context.Signal.TaskNumber != 123 {
		t.Fatalf("unexpected task number: %d", result.Context.Signal.TaskNumber)
	}
	if result.Context.Issue == nil {
		t.Fatal("expected issue in context")
	}
	if result.Context.Issue.Number != 123 {
		t.Fatalf("unexpected issue number: %d", result.Context.Issue.Number)
	}
	if result.Decision == nil {
		t.Fatal("expected decision in result")
	}
	if result.Consideration == nil {
		t.Fatal("expected consideration result")
	}
	if result.Consideration.Status != ConsiderationStatusExecution {
		t.Fatalf("unexpected consideration status: %q", result.Consideration.Status)
	}
	if result.Consideration.Route.Name != "default" {
		t.Fatalf("unexpected consideration route: %#v", result.Consideration.Route)
	}
	if result.Decision.Type != DecisionType(DecisionTypeExecute) {
		t.Fatalf("unexpected decision type: %q", result.Decision.Type)
	}
	if len(result.Decision.Reasons) == 0 {
		t.Fatal("expected decision reasons")
	}
	if result.Decision.Reasons[0].Code == "" || result.Decision.Reasons[0].Message == "" {
		t.Fatalf("unexpected decision reason: %#v", result.Decision.Reasons[0])
	}
	if result.Decision.ExecutionPlan == nil {
		t.Fatal("expected execution plan")
	}
	if result.Decision.ExecutionPlan.Repository != "owner/name" {
		t.Fatalf("unexpected execution repository: %q", result.Decision.ExecutionPlan.Repository)
	}
	if result.Decision.ExecutionPlan.Action != "implement" {
		t.Fatalf("unexpected execution action: %q", result.Decision.ExecutionPlan.Action)
	}
	if result.Decision.ExecutionPlan.Step != "implement" {
		t.Fatalf("unexpected execution step: %q", result.Decision.ExecutionPlan.Step)
	}
	if result.Decision.ExecutionPlan.Profile != defaultExecutionProfile {
		t.Fatalf("unexpected execution profile: %q", result.Decision.ExecutionPlan.Profile)
	}
	if !strings.Contains(result.Decision.ExecutionPlan.Prompt, "Task #123: Implement decision start") {
		t.Fatalf("unexpected execution prompt: %q", result.Decision.ExecutionPlan.Prompt)
	}
	if result.Execution == nil {
		t.Fatal("expected execution result")
	}
	if result.Execution.Status != "completed" {
		t.Fatalf("unexpected execution status: %q", result.Execution.Status)
	}
	if integrationStub.request.System != "github" || integrationStub.request.Resource != "issue" || integrationStub.request.Operation != "get" {
		t.Fatalf("unexpected integration request: %#v", integrationStub.request)
	}
	if integrationStub.request.Number != 123 {
		t.Fatalf("unexpected integration request number: %d", integrationStub.request.Number)
	}
	if executionStub.invocation.Task != "task-123" {
		t.Fatalf("unexpected execution task: %q", executionStub.invocation.Task)
	}
	if executionStub.invocation.Action != "implement" {
		t.Fatalf("unexpected execution action: %q", executionStub.invocation.Action)
	}
	if executionStub.invocation.Profile != defaultExecutionProfile {
		t.Fatalf("unexpected execution invocation profile: %q", executionStub.invocation.Profile)
	}
	if executionStub.invocation.Repository.URL != "owner/name" {
		t.Fatalf("unexpected execution invocation repository: %q", executionStub.invocation.Repository.URL)
	}
	if executionStub.invocation.Workplace.Name != "task-123" {
		t.Fatalf("unexpected workplace name: %q", executionStub.invocation.Workplace.Name)
	}
	if executionStub.invocation.Launch.Runner != "" {
		t.Fatalf("expected execution runner to be inherited from profile, got %q", executionStub.invocation.Launch.Runner)
	}
	if executionStub.invocation.Launch.Model != "" {
		t.Fatalf("expected model to be inherited from profile, got %q", executionStub.invocation.Launch.Model)
	}
	if executionStub.invocation.Launch.Prompt != "" {
		t.Fatalf("execution prompt must not carry full-route task: %q", executionStub.invocation.Launch.Prompt)
	}
	if executionStub.invocation.Launch.StructuredInput == nil || executionStub.invocation.Launch.StructuredInput.Task != result.Decision.ExecutionPlan.Prompt {
		t.Fatalf("unexpected execution structured input: %#v", executionStub.invocation.Launch.StructuredInput)
	}
	if !executionStub.invocation.Launch.StructuredOutput {
		t.Fatal("expected decision-triggered execution to request structured output")
	}
	if executionStub.invocation.Launch.StructuredOutputRequired {
		t.Fatal("structured output must remain optional for decision-triggered execution")
	}
}

func TestServiceConsiderBuildsExecutionAssignmentFromWorkflowRoute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"defaults": {
			"name": "default",
			"title": "Маршрут по умолчанию",
			"step": "implement",
			"profile": "default",
			"reason_code": "issue_context_ready",
			"reason_message": "Контекст задачи готов."
		},
		"routes": [
			{
				"name": "description-assessment",
				"title": "Оценка описания",
				"description": "Проверяет достаточность постановки.",
				"step": "assess-description",
				"action": "task-description-assessment",
				"profile": "task-description-assessor",
				"has_labels": ["description-assessment"],
				"missing_labels": ["description-assessed"],
				"expected_result": "Сформировать заключение о достаточности описания.",
				"constraints": ["Не менять код."],
				"reason_code": "task_description_not_assessed",
				"reason_message": "Описание задачи ещё не оценено."
			}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{
		logger:          log.Default(),
		resolveRepoRoot: func(context.Context) (string, error) { return root, nil },
		readFile:        os.ReadFile,
	}

	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 211},
		Issue: &integration.TrackerIssue{
			Repository: "owner/name",
			Number:     211,
			Title:      "Assess task description",
			Labels:     []string{"description-assessment"},
		},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Status != ConsiderationStatusExecution {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Route.Name != "description-assessment" || result.Route.Title != "Оценка описания" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Status != RouteCheckStatusPassed {
		t.Fatalf("unexpected route checks: %#v", result.Checks)
	}
	if len(result.Reasons) != 1 || result.Reasons[0].Code != "task_description_not_assessed" {
		t.Fatalf("unexpected reasons: %#v", result.Reasons)
	}
	if result.ExecutionPlan == nil {
		t.Fatal("expected execution plan")
	}
	if result.ExecutionPlan.Action != "task-description-assessment" {
		t.Fatalf("unexpected action: %q", result.ExecutionPlan.Action)
	}
	if result.ExecutionPlan.Step != "assess-description" || result.ExecutionPlan.Profile != "task-description-assessor" {
		t.Fatalf("unexpected execution plan: %#v", result.ExecutionPlan)
	}
	if result.ExecutionPlan.ExpectedResult != "Сформировать заключение о достаточности описания." {
		t.Fatalf("unexpected expected result: %q", result.ExecutionPlan.ExpectedResult)
	}
	if len(result.ExecutionPlan.Constraints) != 1 || result.ExecutionPlan.Constraints[0] != "Не менять код." {
		t.Fatalf("unexpected constraints: %#v", result.ExecutionPlan.Constraints)
	}
	if result.ExecutionPlan.StructuredInput == nil || len(result.ExecutionPlan.StructuredInput.Constraints) != 1 {
		t.Fatalf("expected constraints in structured input: %#v", result.ExecutionPlan.StructuredInput)
	}
}

func TestServiceConsiderReturnsDiagnosedFailureWithoutIssue(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{})
	if err == nil {
		t.Fatal("expected missing issue error")
	}
	if result.Status != ConsiderationStatusFailed {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Failure == nil || result.Failure.Code != "missing_issue" || !result.Failure.ManualIntervention {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
}

func TestServiceStartRoutesTaskDescriptionAssessment(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     211,
				Title:      "Assess task description",
				State:      "OPEN",
				Labels:     []string{"description-assessment"},
			},
		},
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 211})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Decision == nil || result.Decision.ExecutionPlan == nil {
		t.Fatalf("expected execution plan, got %#v", result.Decision)
	}
	if result.Decision.ExecutionPlan.Step != "assess-description" {
		t.Fatalf("unexpected execution step: %q", result.Decision.ExecutionPlan.Step)
	}
	if result.Decision.ExecutionPlan.Profile != "task-description-assessor" {
		t.Fatalf("unexpected execution profile: %q", result.Decision.ExecutionPlan.Profile)
	}
	if len(result.Decision.Reasons) != 1 || result.Decision.Reasons[0].Code != "task_description_not_assessed" {
		t.Fatalf("unexpected decision reasons: %#v", result.Decision.Reasons)
	}
	if executionStub.invocation.Profile != "task-description-assessor" {
		t.Fatalf("unexpected execution invocation profile: %q", executionStub.invocation.Profile)
	}
}

func TestServiceStartRejectsNonPositiveTaskNumber(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default(), integration: &stubIntegrationExecutor{}, execution: &stubExecutionStarter{}, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	_, err := service.Start(context.Background(), StartInput{TaskNumber: 0})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if err.Error() != "task number must be greater than zero" {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestServiceStartPropagatesIntegrationError(t *testing.T) {
	t.Parallel()

	service := &Service{
		logger:      log.Default(),
		integration: &stubIntegrationExecutor{err: errors.New("integration failed")},
		execution:   &stubExecutionStarter{},
		resolveRepo: func(context.Context) (string, error) { return "owner/name", nil },
	}

	_, err := service.Start(context.Background(), StartInput{TaskNumber: 42})
	if err == nil {
		t.Fatal("expected integration error")
	}
	if err.Error() != "integration failed" {
		t.Fatalf("unexpected integration error: %v", err)
	}
}

func TestServiceStartPropagatesExecutionErrorWithDecisionContext(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     77,
				Title:      "Implement execution handoff",
				State:      "OPEN",
			},
		},
	}
	executionStub := &stubExecutionStarter{
		result: execution.LaunchResult{Status: "failed", Summary: "launch failed"},
		err:    errors.New("execution failed"),
	}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 77})
	if err == nil {
		t.Fatal("expected execution error")
	}
	if err.Error() != "execution failed" {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if result.Decision == nil || result.Decision.Type != DecisionType(DecisionTypeExecute) {
		t.Fatalf("expected execute decision on failure, got %#v", result.Decision)
	}
	if result.Execution == nil {
		t.Fatal("expected execution result on failure")
	}
	if result.Execution.Status != "failed" {
		t.Fatalf("unexpected failed execution status: %q", result.Execution.Status)
	}
}

func TestServiceStartPassesExternalRepositoryToExecution(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     55,
				Title:      "Keep execution in the correct repository",
				State:      "OPEN",
			},
		},
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{
		logger:      log.Default(),
		integration: integrationStub,
		execution:   executionStub,
		resolveRepo: func(context.Context) (string, error) { return "other/repo", nil },
	}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 55})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !result.Ready {
		t.Fatal("expected ready decision context")
	}
	if result.Decision == nil || result.Decision.Type != DecisionType(DecisionTypeExecute) {
		t.Fatalf("expected execute decision, got %#v", result.Decision)
	}
	if result.Execution == nil || result.Execution.Status != "completed" {
		t.Fatalf("expected execution result, got %#v", result.Execution)
	}
	if executionStub.invocation.Repository.URL != "owner/name" {
		t.Fatalf("execution must receive issue repository, got %#v", executionStub.invocation)
	}
}

func TestBuildExecutionTaskPreservesIssueBodyLiteralStructuredInputBlock(t *testing.T) {
	t.Parallel()

	task := buildExecutionTask(&integration.TrackerIssue{
		Number: 88,
		Title:  "Fix prompt handoff",
		Body: strings.Join([]string{
			"Issue body ends with a literal block:",
			"<progress-structured-input>",
			`{"task":"literal example"}`,
			"</progress-structured-input>",
		}, "\n"),
	})

	if !strings.Contains(task, "<progress-structured-input>") || !strings.Contains(task, "</progress-structured-input>") {
		t.Fatalf("execution task must preserve literal issue body text: %q", task)
	}
	if !strings.Contains(task, "Task #88: Fix prompt handoff") {
		t.Fatalf("execution task must include issue title: %q", task)
	}
}

type stubIntegrationExecutor struct {
	response integration.Response
	err      error
	request  integration.Request
}

func (s *stubIntegrationExecutor) Execute(_ context.Context, request integration.Request) (integration.Response, error) {
	s.request = request
	return s.response, s.err
}

type stubExecutionStarter struct {
	result     execution.LaunchResult
	err        error
	invocation execution.Invocation
}

func (s *stubExecutionStarter) Start(_ context.Context, invocation execution.Invocation) (execution.LaunchResult, error) {
	s.invocation = invocation
	return s.result, s.err
}
