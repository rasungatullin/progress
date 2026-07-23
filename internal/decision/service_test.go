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
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceStartBuildsExecuteDecisionAndLaunchesExecution(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "123",
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
	if result.Context.Task.ID != "123" {
		t.Fatalf("unexpected issue identifier: %s", result.Context.Task.ID)
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
	if result.Consideration.Route.Name != "task-processing" {
		t.Fatalf("unexpected consideration route: %#v", result.Consideration.Route)
	}
	if len(result.Consideration.Checks) != 1 || result.Consideration.Checks[0].Name != "task-processing-start" {
		t.Fatalf("unexpected consideration checks: %#v", result.Consideration.Checks)
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
	if result.Decision.ExecutionPlan.Action != execution.ActionStartImplementationPR {
		t.Fatalf("unexpected execution action: %q", result.Decision.ExecutionPlan.Action)
	}
	if result.Decision.ExecutionPlan.StructuredInput == nil || !strings.Contains(result.Decision.ExecutionPlan.StructuredInput.Task, "Task #123: Implement decision start") {
		t.Fatalf("unexpected execution structured input: %#v", result.Decision.ExecutionPlan.StructuredInput)
	}
	if result.Execution == nil {
		t.Fatal("expected execution result")
	}
	if result.Execution.Status != "completed" {
		t.Fatalf("unexpected execution status: %q", result.Execution.Status)
	}
	if got := len(integrationStub.requests); got != 2 {
		t.Fatalf("unexpected number of integration requests: %d", got)
	}
	if integrationStub.requests[0].System != "github" || integrationStub.requests[0].Resource != "issue" || integrationStub.requests[0].Operation != "get" {
		t.Fatalf("unexpected issue request: %#v", integrationStub.requests[0])
	}
	if integrationStub.requests[0].ID != "123" {
		t.Fatalf("unexpected issue request identifier: %s", integrationStub.requests[0].ID)
	}
	if integrationStub.requests[1].IntegrationType != integrationmodel.IntegrationTypeRepository || integrationStub.requests[1].Resource != "merge-request" || integrationStub.requests[1].Operation != "search" {
		t.Fatalf("unexpected merge-request request: %#v", integrationStub.requests[1])
	}
	if integrationStub.requests[1].Query != "head:123" {
		t.Fatalf("merge-request search must be constrained by head ref: %#v", integrationStub.requests[1])
	}
	if executionStub.request.Assignment == nil {
		t.Fatal("expected execution assignment")
	}
	if executionStub.request.Assignment.Action != execution.ActionStartImplementationPR {
		t.Fatalf("unexpected execution assignment: %#v", executionStub.request.Assignment)
	}
	if executionStub.request.Assignment.CanonicalTask == nil || executionStub.request.Assignment.CanonicalTask.Number != 123 || executionStub.request.Assignment.CanonicalTask.Repository != "owner/name" {
		t.Fatalf("assignment must include canonical task: %#v", executionStub.request.Assignment)
	}
	if len(executionStub.request.Assignment.Reasons) == 0 || executionStub.request.Assignment.Reasons[0].Code == "" {
		t.Fatalf("assignment must include decision reasons: %#v", executionStub.request.Assignment)
	}
	if executionStub.request.Assignment.StructuredInput == nil || executionStub.request.Assignment.StructuredInput.Task != result.Decision.ExecutionPlan.StructuredInput.Task {
		t.Fatalf("unexpected execution structured input: %#v", executionStub.request.Assignment.StructuredInput)
	}
}

func TestServiceStartPreservesCanonicalTaskState(t *testing.T) {
	t.Parallel()

	task := integration.CanonicalTask{
		System:     "github",
		Repository: "owner/name",
		ID:         "124",
		ExternalID: "node-124",
		Title:      "Preserve canonical task",
		Body:       "Canonical body",
		State:      "OPEN",
		Traits:     []string{"ready"},
		Attributes: map[string]string{"priority": "high"},
		Assignees:  []integration.User{{System: "github", Login: "engineer"}},
		Author:     integration.User{System: "github", Login: "author"},
		URL:        "https://github.com/owner/name/issues/124",
		CreatedAt:  "2026-07-22T10:00:00Z",
		UpdatedAt:  "2026-07-23T10:00:00Z",
	}
	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{Task: &task, Partial: true},
	}
	executionStub := &stubExecutionStarter{
		result: execution.LaunchResult{Status: "completed"},
	}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 124})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	contextTask := result.Context.Task
	if contextTask.ID != task.ID || contextTask.ExternalID != task.ExternalID || contextTask.Repository != task.Repository {
		t.Fatalf("canonical task identity was not preserved: %#v", contextTask)
	}
	if len(contextTask.Traits) != 1 || contextTask.Traits[0] != "ready" {
		t.Fatalf("canonical task traits were not preserved: %#v", contextTask.Traits)
	}
	if contextTask.Author.Login != "author" || len(contextTask.Assignees) != 1 || contextTask.Assignees[0].Login != "engineer" {
		t.Fatalf("canonical task users were not preserved: %#v", contextTask)
	}
	assignment := result.Decision.ExecutionPlan.Assignment
	if assignment == nil || assignment.CanonicalTask == nil {
		t.Fatalf("canonical task was not passed to execution: %#v", assignment)
	}
	if assignment.CanonicalTask.ID != task.ID || assignment.CanonicalTask.ExternalID != task.ExternalID {
		t.Fatalf("canonical task identifiers were not passed to execution: %#v", assignment.CanonicalTask)
	}
	if assignment.CanonicalTask.Attributes["priority"] != "high" || assignment.CanonicalTask.Attributes["body"] != task.Body || assignment.CanonicalTask.Attributes["state"] != task.State {
		t.Fatalf("canonical task attributes were not passed to execution: %#v", assignment.CanonicalTask.Attributes)
	}
}

func TestServiceStartRecoversMergeRequestForReviewRoute(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "201",
				Title:      "Run review route",
				Traits:     []string{"Ожидает экспертизы"},
			},
			MergeRequests: []integration.MergeRequest{{
				System:     "github",
				Repository: "owner/name",
				Number:     45,
				Title:      "Review route task",
				State:      "open",
				BaseRef:    "main",
				HeadRef:    "201",
			}},
		},
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 201})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Decision == nil || result.Decision.ExecutionPlan == nil {
		t.Fatalf("expected execution plan")
	}
	if result.Decision.ExecutionPlan.Action != execution.ActionReviewPullRequest {
		t.Fatalf("unexpected execution action: %q", result.Decision.ExecutionPlan.Action)
	}
	if result.Context.MergeRequest == nil || result.Context.MergeRequest.Number != 45 {
		t.Fatalf("expected recovered merge request in decision context: %#v", result.Context.MergeRequest)
	}
	if result.Consideration.Failure != nil {
		t.Fatalf("did not expect consideration failure: %#v", result.Consideration.Failure)
	}
	if len(result.Consideration.Route.Name) == 0 {
		t.Fatalf("unexpected empty route")
	}
	if executionStub.request.Assignment == nil || len(executionStub.request.Assignment.RelatedObjects) != 1 {
		t.Fatalf("expected merge request related object in execution assignment: %#v", executionStub.request.Assignment)
	}
	object := executionStub.request.Assignment.RelatedObjects[0]
	if object.Number != 45 {
		t.Fatalf("unexpected execution related object: %#v", object)
	}
	if object.Attributes["head_ref"] != "201" {
		t.Fatalf("expected execution related object with head_ref=201: %#v", object)
	}
	if object.Type != "merge-request" {
		t.Fatalf("expected merge-request related object type: %#v", object)
	}
	if len(integrationStub.requests) != 3 {
		t.Fatalf("unexpected number of integration requests: %d", len(integrationStub.requests))
	}
	if integrationStub.requests[1].Query != "head:201" {
		t.Fatalf("merge-request search must be constrained by head ref: %#v", integrationStub.requests[1])
	}
}

func TestServiceStartReturnsMergeRequestSearchErrorForReviewRoute(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "201",
				Title:      "Run review route",
				Traits:     []string{"Ожидает экспертизы"},
			},
		},
		errOnSearch: errors.New("search unavailable"),
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	_, err := service.Start(context.Background(), StartInput{TaskNumber: 201})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
	}
	if executionStub.request.Assignment != nil {
		t.Fatalf("execution must not start after merge request restoration failure: %#v", executionStub.request)
	}
}

func TestServiceStartReturnsMergeRequestSearchErrorForExplicitReviewRoute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"default_route": "task-processing",
		"routes": [
			{
				"name": "task-processing",
				"checks": [{
					"name": "task-processing-start",
					"action": "start-implementation-pr",
					"reason_code": "implementation_start",
					"reason_message": "Запущена реализация."
				}]
			},
			{
				"name": "pull-request-review",
				"checks": [{
					"name": "review-run",
					"action": "review-pull-request",
					"reason_code": "review_run",
					"reason_message": "Запущена ревизия."
				}]
			}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "202",
				Title:      "Run explicit review route",
			},
		},
		errOnSearch: errors.New("search unavailable"),
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{
		logger:          log.Default(),
		integration:     integrationStub,
		execution:       executionStub,
		resolveRepo:     func(context.Context) (string, error) { return "owner/name", nil },
		resolveRepoRoot: func(context.Context) (string, error) { return root, nil },
		readFile:        os.ReadFile,
	}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 202, Route: "pull-request-review"})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
	}
	if result.Consideration == nil || result.Consideration.Failure == nil || result.Consideration.Failure.Code != "merge_request_missing" {
		t.Fatalf("expected diagnosed missing merge request: %#v", result.Consideration)
	}
	if executionStub.request.Assignment != nil {
		t.Fatalf("execution must not start after merge request restoration failure: %#v", executionStub.request)
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
		"default_route": "description-assessment",
		"defaults": {
			"name": "default",
			"title": "Маршрут по умолчанию",
			"action": "implement",
			"reason_code": "issue_context_ready",
			"reason_message": "Контекст задачи готов."
		},
		"routes": [
			{
				"name": "description-assessment",
				"title": "Оценка описания",
				"description": "Проверяет достаточность постановки.",
				"action": "task-description-assessment",
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
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "211",
			Title:      "Assess task description",
			Traits:     []string{"description-assessment"},
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
	if result.Context.Task.ID != "211" || result.Context.Task.Traits[0] != "description-assessment" {
		t.Fatalf("expected canonical task in consideration context: %#v", result.Context.Task)
	}
	if result.ExecutionPlan.Action != "task-description-assessment" {
		t.Fatalf("unexpected action: %q", result.ExecutionPlan.Action)
	}
	if result.ExecutionPlan.Assignment == nil {
		t.Fatal("expected execution assignment")
	}
	if result.ExecutionPlan.Assignment.CanonicalTask == nil || result.ExecutionPlan.Assignment.CanonicalTask.Repository != "owner/name" {
		t.Fatalf("unexpected canonical task assignment: %#v", result.ExecutionPlan.Assignment)
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

func TestServiceConsiderUsesExplicitNamedRoute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"default_route": "implementation",
		"routes": [
			{
				"name": "implementation",
				"title": "Реализация задачи",
				"checks": [{
					"name": "implementation-start",
					"action": "start-implementation-pr",
					"missing_labels": ["Ожидает экспертизы"],
					"reason_code": "implementation_start",
					"reason_message": "Запущена реализация."
				}]
			},
			{
				"name": "review-only",
				"title": "Ревизия чужой реализации",
				"checks": [{
					"name": "review-run",
					"action": "review-pull-request",
					"reason_code": "review_run",
					"reason_message": "Запущена ревизия."
				}]
			}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Route: "review-only", Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 212},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "212", Title: "Review external change"},
		MergeRequest: &integration.MergeRequest{
			System: "github", Repository: "owner/name", Number: 12, HeadRef: "212",
		},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "review-only" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "review-run" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionReviewPullRequest {
		t.Fatalf("expected review action, got %#v", result.ExecutionPlan)
	}
}

func TestServiceConsiderUsesCompatibleDefaultRouteForLegacyWorkflowConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"routes": [{
			"name": "task-processing-start",
			"title": "Начало выполнения",
			"action": "start-implementation-pr",
			"missing_labels": ["Ожидает экспертизы"],
			"reason_code": "implementation_start",
			"reason_message": "Запущена реализация."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 215},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "215", Title: "Legacy route"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "task-processing" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "task-processing-start" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
}

func TestServiceConsiderUsesLegacyWorkflowDefaultsWhenNoRouteMatches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"defaults": {
			"name": "default",
			"action": "start-implementation-pr",
			"reason_code": "implementation_start",
			"reason_message": "Запущена реализация."
		},
		"routes": [{
			"name": "task-processing-review",
			"title": "Ревизия результата",
			"action": "review-pull-request",
			"has_labels": ["Ожидает экспертизы"],
			"reason_code": "review_required",
			"reason_message": "Запущена ревизия."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 218},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "218", Title: "Legacy default fallback"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "task-processing" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "default" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionStartImplementationPR {
		t.Fatalf("unexpected execution plan: %#v", result.ExecutionPlan)
	}
}

func TestServiceConsiderUsesLegacyWorkflowDefaultsWithoutRoutes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"defaults": {
			"name": "default",
			"action": "start-implementation-pr",
			"reason_code": "implementation_start",
			"reason_message": "Запущена реализация."
		}
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 219},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "219", Title: "Legacy defaults only"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "task-processing" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "default" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
}

func TestServiceConsiderUsesCompatibleDefaultRouteForLegacyMethodologyCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "catalog.json"), []byte(`{
		"routes": [{
			"name": "task-processing-start",
			"title": "Начало выполнения",
			"action": "start-implementation-pr",
			"missing_labels": ["Ожидает экспертизы"],
			"reason_code": "implementation_start",
			"reason_message": "Запущена реализация."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 216},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "216", Title: "Legacy methodology route"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "task-processing" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "task-processing-start" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
}

func TestServiceConsiderUsesLegacyMethodologyDefaultWhenNoRouteMatches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "catalog.json"), []byte(`{
		"routes": [
			{
				"name": "default",
				"title": "Маршрут по умолчанию",
				"action": "start-implementation-pr",
				"reason_code": "implementation_start",
				"reason_message": "Запущена реализация."
			},
			{
				"name": "task-processing-review",
				"title": "Ревизия результата",
				"action": "review-pull-request",
				"has_labels": ["Ожидает экспертизы"],
				"reason_code": "review_required",
				"reason_message": "Запущена ревизия."
			}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 220},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "220", Title: "Legacy methodology default fallback"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "task-processing" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "default" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
}

func TestServiceConsiderUsesDefaultRouteForLegacyMethodologyDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "catalog.json"), []byte(`{
		"routes": [{
			"name": "default",
			"title": "Маршрут по умолчанию",
			"action": "start-implementation-pr",
			"reason_code": "implementation_start",
			"reason_message": "Запущена реализация."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 217},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "217", Title: "Legacy default route"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "default" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
}

func TestServiceConsiderResolvesMethodologyRouteCheckReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "catalog.json"), []byte(`{
		"default_route": "task-processing",
		"routes": [
			{
				"name": "task-processing",
				"title": "Обработка задачи",
				"checks": ["task-processing-start"]
			},
			{
				"name": "task-processing-start",
				"title": "Начало выполнения",
				"action": "start-implementation-pr",
				"missing_labels": ["Ожидает экспертизы"],
				"reason_code": "implementation_start",
				"reason_message": "Запущена реализация."
			}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 217},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "217", Title: "Referenced check"},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Route.Name != "task-processing" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Checks) != 1 || result.Checks[0].Name != "task-processing-start" {
		t.Fatalf("unexpected checks: %#v", result.Checks)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionStartImplementationPR {
		t.Fatalf("unexpected execution plan: %#v", result.ExecutionPlan)
	}
}

func TestServiceConsiderDiagnosesMissingDefaultRoute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{"routes": []}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 213},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "213", Title: "No route"},
	}})
	if err == nil {
		t.Fatal("expected missing default route error")
	}
	if result.Failure == nil || result.Failure.Code != "default_route_not_configured" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
}

func TestServiceConsiderDiagnosesDefaultRouteNotFound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "decision")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workflows.json"), []byte(`{
		"default_route": "missing-route",
		"routes": [{
			"name": "implementation",
			"checks": [{
				"name": "implementation-start",
				"action": "start-implementation-pr",
				"missing_labels": ["Ожидает экспертизы"],
				"reason_code": "implementation_start",
				"reason_message": "Запущена реализация."
			}]
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write workflow config: %v", err)
	}

	service := &Service{logger: log.Default(), resolveRepoRoot: func(context.Context) (string, error) { return root, nil }, readFile: os.ReadFile}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 214},
		Task:   integration.CanonicalTask{Repository: "owner/name", ID: "214", Title: "Missing default route"},
	}})
	if err == nil {
		t.Fatal("expected default route not found error")
	}
	if result.Failure == nil || result.Failure.Code != "default_route_not_found" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
}

func TestServiceConsiderCompletesWhenReviewPassed(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 123},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "123",
			Title:      "Completed task",
			Traits:     []string{"Экспертиза пройдена"},
		},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Status != ConsiderationStatusCompleted {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.ExecutionPlan != nil {
		t.Fatalf("completed route must not produce execution plan: %#v", result.ExecutionPlan)
	}
	decision := decisionFromConsideration(result)
	if decision.Type != DecisionType(DecisionTypeNone) {
		t.Fatalf("unexpected decision type: %q", decision.Type)
	}
}

func TestServiceConsiderRoutesReviewPassedTaskToReworkForExternalRemarks(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 123},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "123",
			Title:      "Completed task",
			Traits:     []string{"Экспертиза пройдена"},
		},
		MergeRequest: &integration.MergeRequest{
			System:     "github",
			Repository: "owner/name",
			Number:     17,
			Title:      "Completed task",
			BaseRef:    "main",
			HeadRef:    "123",
		},
		MergeRequestExternalState: &MergeRequestExternalState{HasUnresolvedReviewRemarks: true},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.Status != ConsiderationStatusExecution {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionApplyReviewComments {
		t.Fatalf("expected apply-review-comments execution plan, got %#v", result.ExecutionPlan)
	}
	if result.Route.Name != "task-processing-external-pr-rework" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if len(result.Reasons) != 1 || result.Reasons[0].Code != "external_review_remarks_unresolved" {
		t.Fatalf("unexpected reasons: %#v", result.Reasons)
	}
}

func TestServiceConsiderRoutesReviewPassedTaskToReworkForMergeConflict(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 123},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "123",
			Title:      "Completed task",
			Traits:     []string{"Экспертиза пройдена"},
		},
		MergeRequest: &integration.MergeRequest{
			System:     "github",
			Repository: "owner/name",
			Number:     17,
			Title:      "Completed task",
			BaseRef:    "main",
			HeadRef:    "123",
		},
		MergeRequestExternalState: &MergeRequestExternalState{HasMergeConflict: true},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionResolveMergeConflict {
		t.Fatalf("expected conflict resolution execution plan, got %#v", result.ExecutionPlan)
	}
	if len(result.Reasons) != 1 || result.Reasons[0].Code != "merge_request_conflict" {
		t.Fatalf("unexpected reasons: %#v", result.Reasons)
	}
}

func TestMergeRequestHasConflictIgnoresNonConflictGitHubStatesAndLabels(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"BEHIND", "BLOCKED"} {
		mergeRequest := &integration.MergeRequest{Attributes: map[string]string{"merge_state_status": state}}
		if mergeRequestHasConflict(mergeRequest) {
			t.Fatalf("merge_state_status=%s must not be treated as conflict", state)
		}
	}

	mergeRequest := &integration.MergeRequest{Traits: []string{"blocked", "behind", "conflict"}}
	if mergeRequestHasConflict(mergeRequest) {
		t.Fatal("PR labels must not be treated as merge conflicts")
	}
}

func TestServiceConsiderRequiresMergeRequestForReview(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 123},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "123",
			Title:      "Review task",
			Traits:     []string{"Ожидает экспертизы"},
		},
	}})
	if err == nil {
		t.Fatal("expected missing merge request error")
	}
	if result.Status != ConsiderationStatusManualIntervention {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Failure == nil || result.Failure.Code != "merge_request_missing" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
}

func TestServiceConsiderDoesNotStartImplementationWhenMergeRequestExists(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 143},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "143",
			Title:      "Продолжить выполнение",
		},
		MergeRequest: &integration.MergeRequest{
			Repository: "owner/name",
			Number:     144,
			BaseRef:    "main",
			HeadRef:    "143",
			State:      "OPEN",
		},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionReviewPullRequest {
		t.Fatalf("expected review execution plan, got %#v", result.ExecutionPlan)
	}
	if len(result.Checks) == 0 || result.Checks[len(result.Checks)-1].Name != "open-merge-request-invariant" {
		t.Fatalf("expected open merge request invariant check, got %#v", result.Checks)
	}
}

func TestServiceConsiderAllowsImplementationForClosedMergeRequest(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 143},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "143",
			Title:      "Начать новый цикл",
		},
		MergeRequest: &integration.MergeRequest{
			Repository: "owner/name",
			Number:     144,
			BaseRef:    "main",
			HeadRef:    "143",
			State:      "CLOSED",
		},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionStartImplementationPR {
		t.Fatalf("expected implementation execution plan, got %#v", result.ExecutionPlan)
	}
}

func TestServiceConsiderPassesMergeRequestToReviewAssignment(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default()}
	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 123},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "123",
			Title:      "Review task",
			Traits:     []string{"Ожидает экспертизы"},
		},
		MergeRequest: &integration.MergeRequest{
			System:     "github",
			Repository: "owner/name",
			Number:     17,
			Title:      "Review task",
			BaseRef:    "main",
			HeadRef:    "123",
		},
	}})
	if err != nil {
		t.Fatalf("consider: %v", err)
	}
	if result.ExecutionPlan == nil || result.ExecutionPlan.Action != execution.ActionReviewPullRequest {
		t.Fatalf("expected review execution plan, got %#v", result.ExecutionPlan)
	}
	if result.ExecutionPlan.Assignment == nil || len(result.ExecutionPlan.Assignment.RelatedObjects) != 1 {
		t.Fatalf("expected merge request related object: %#v", result.ExecutionPlan.Assignment)
	}
	object := result.ExecutionPlan.Assignment.RelatedObjects[0]
	if object.Number != 17 || object.Attributes["head_ref"] != "123" || object.Attributes["base_ref"] != "main" {
		t.Fatalf("unexpected merge request related object: %#v", object)
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
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "211",
				Title:      "Assess task description",
				State:      "OPEN",
				Traits:     []string{"description-assessment", "Ожидает экспертизы"},
			},
		},
		errOnSearch: errors.New("search unavailable"),
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 211, Route: "task-description"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Decision == nil || result.Decision.ExecutionPlan == nil {
		t.Fatalf("expected execution plan, got %#v", result.Decision)
	}
	if result.Decision.ExecutionPlan.Action != "task-description-assessment" {
		t.Fatalf("unexpected execution action: %q", result.Decision.ExecutionPlan.Action)
	}
	if len(result.Decision.Reasons) != 1 || result.Decision.Reasons[0].Code != "task_description_not_assessed" {
		t.Fatalf("unexpected decision reasons: %#v", result.Decision.Reasons)
	}
	if executionStub.request.Assignment == nil || executionStub.request.Assignment.Action != "task-description-assessment" {
		t.Fatalf("unexpected execution request: %#v", executionStub.request)
	}
}

func TestServiceConsiderDefaultRouteDoesNotStartImplementationForDescriptionAssessment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir methodology dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "catalog.json"), []byte(`{
		"default_route": "task-processing",
		"routes": [
			{
				"name": "task-processing",
				"checks": [{
					"name": "task-processing-start",
					"action": "start-implementation-pr",
					"missing_labels": ["Ожидает экспертизы", "Требует доработки", "Экспертиза пройдена", "description-assessment"],
					"reason_code": "task_processing_not_started",
					"reason_message": "Требуется начать выполнение."
				}]
			},
			{
				"name": "task-description",
				"checks": [{
					"name": "description-assessment",
					"action": "task-description-assessment",
					"has_labels": ["description-assessment"],
					"reason_code": "task_description_not_assessed",
					"reason_message": "Описание задачи ещё не оценено."
				}]
			}
		]
	}`), 0o600); err != nil {
		t.Fatalf("write methodology catalog: %v", err)
	}

	service := &Service{
		logger:          log.Default(),
		resolveRepoRoot: func(context.Context) (string, error) { return root, nil },
		readFile:        os.ReadFile,
	}

	result, err := service.Consider(context.Background(), ConsiderationInput{Context: DecisionContext{
		Signal: Signal{Source: SignalSourceTask, Kind: SignalKindTask, TaskNumber: 211},
		Task: integration.CanonicalTask{
			Repository: "owner/name",
			ID:         "211",
			Title:      "Assess task description",
			Traits:     []string{"description-assessment"},
		},
	}})
	if err == nil {
		t.Fatal("expected default route to skip implementation start")
	}
	if result.Failure == nil || result.Failure.Code != "route_check_not_found" {
		t.Fatalf("unexpected failure: %#v", result.Failure)
	}
	if result.ExecutionPlan != nil {
		t.Fatalf("description assessment task must not start implementation by default: %#v", result.ExecutionPlan)
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

func TestServiceStartRejectsEmptyCanonicalTaskResponse(t *testing.T) {
	t.Parallel()

	service := &Service{
		logger:      log.Default(),
		integration: &stubIntegrationExecutor{response: integration.Response{Partial: true}},
		execution:   &stubExecutionStarter{},
	}

	_, err := service.Start(context.Background(), StartInput{TaskNumber: 42})
	if err == nil || !strings.Contains(err.Error(), "integration did not return issue for task 42") {
		t.Fatalf("unexpected empty canonical task response error: %v", err)
	}
}

func TestServiceStartPropagatesExecutionErrorWithDecisionContext(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "77",
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
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "55",
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
	if executionStub.request.Assignment == nil || executionStub.request.Assignment.CanonicalTask == nil || executionStub.request.Assignment.CanonicalTask.Repository != "owner/name" {
		t.Fatalf("execution must receive issue repository, got %#v", executionStub.request)
	}
}

func TestServiceStartRoutesReviewPassedTaskToReworkForExternalRemarks(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Task: &integration.CanonicalTask{
				System:     "github",
				Repository: "owner/name",
				ID:         "123",
				Title:      "Completed task",
				State:      "OPEN",
				Traits:     []string{"Экспертиза пройдена"},
			},
			MergeRequests: []integration.MergeRequest{{
				System:     "github",
				Repository: "owner/name",
				Number:     17,
				Title:      "Completed task",
				State:      "OPEN",
				BaseRef:    "main",
				HeadRef:    "123",
			}},
			ReviewRemarks: []integration.ReviewRemark{{ExternalID: "comment-1", ReplyToID: "thread-1", State: "unresolved", Body: "Исправить обработку"}},
		},
	}
	executionStub := &stubExecutionStarter{result: execution.LaunchResult{Status: "completed", Summary: "execution launched"}}
	service := &Service{logger: log.Default(), integration: integrationStub, execution: executionStub, resolveRepo: func(context.Context) (string, error) { return "owner/name", nil }}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 123})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Context.MergeRequestExternalState == nil || !result.Context.MergeRequestExternalState.HasUnresolvedReviewRemarks {
		t.Fatalf("expected external state in decision context: %#v", result.Context.MergeRequestExternalState)
	}
	if result.Decision == nil || result.Decision.ExecutionPlan == nil || result.Decision.ExecutionPlan.Action != execution.ActionApplyReviewComments {
		t.Fatalf("expected apply-review-comments decision, got %#v", result.Decision)
	}
	foundCommentsRequest := false
	for _, request := range integrationStub.requests {
		if request.Operation == "list" {
			foundCommentsRequest = true
		}
	}
	if !foundCommentsRequest {
		t.Fatalf("expected review remarks list request, got %#v", integrationStub.requests)
	}
}

func TestHasUnresolvedExternalReviewRemarksRequiresResolvedResponseState(t *testing.T) {
	t.Parallel()

	const original = "Идентификатор: remark-2\n\nНабор проверок завершается ошибкой"

	tests := []struct {
		name     string
		response string
		want     bool
	}{
		{
			name:     "resolved response suppresses processed comment",
			response: "## Ответ на замечание ревизии\n\nЗамечание: remark-2\n\nСостояние: resolved",
			want:     false,
		},
		{
			name:     "open response keeps comment unresolved",
			response: "## Ответ на замечание ревизии\n\nЗамечание: remark-2\n\nСостояние: open",
			want:     true,
		},
		{
			name:     "new ordinary comment remains unresolved",
			response: "## Ответ на замечание ревизии\n\nЗамечание: remark-1\n\nСостояние: resolved",
			want:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			remarks := []integration.ReviewRemark{
				{State: "conversation", Body: original},
				{State: "conversation", Body: test.response},
			}
			if got := hasUnresolvedExternalReviewRemarks(remarks); got != test.want {
				t.Fatalf("hasUnresolvedExternalReviewRemarks() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestHasUnresolvedExternalReviewRemarksIgnoresResolvedGeneralRemarkByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state string
		body  string
		want  bool
	}{
		{name: "resolved", state: "resolved", body: "Замечание закрыто", want: false},
		{name: "closed", state: "closed", body: "Замечание закрыто", want: false},
		{name: "fixed", state: "fixed", body: "Замечание закрыто", want: false},
		{name: "explicit closure with normalized state", state: "resolved", body: "Замечание закрыто: исправление подтверждено", want: false},
		{name: "closure-like text without normalized state", body: "Замечание закрыто: исправление подтверждено", want: true},
		{name: "closure mention in open remark", body: "Замечание закрыто не было; требуется исправление", want: true},
		{name: "negative closure statement", body: "Замечание закрыто: не было подтверждено", want: true},
		{name: "open", state: "open", body: "Ожидается исправление", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			remarks := []integration.ReviewRemark{{
				State: "conversation",
				Body:  "## Замечание ревизии\n\nИдентификатор: remark-1\n\nСостояние: " + test.state + "\n\n" + test.body,
			}}
			if got := hasUnresolvedExternalReviewRemarks(remarks); got != test.want {
				t.Fatalf("hasUnresolvedExternalReviewRemarks() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestHasUnresolvedExternalReviewRemarksIgnoresConfirmationByID(t *testing.T) {
	t.Parallel()

	remarks := []integration.ReviewRemark{
		{State: "conversation", Body: "## Замечание ревизии\n\nИдентификатор: remark-1\n\nИсправить обработку"},
		{State: "conversation", Body: "## Ответ на замечание ревизии\n\nЗамечание: remark-1\n\nСостояние: resolved\n\nЗамечание закрыто: исправление подтверждено"},
	}
	if got := hasUnresolvedExternalReviewRemarks(remarks); got {
		t.Fatal("resolved confirmation must suppress the original general remark")
	}
}

func TestHasUnresolvedExternalReviewRemarksClassifiesReviewConclusions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "approve", body: "## Заключение ревизии\n\napprove\n\nПроверка завершена", want: false},
		{name: "approve in thread", body: "## Заключение ревизии\n\nСтатус: approve", want: false},
		{name: "request changes", body: "## Заключение ревизии\n\nrequest-changes\n\nТребуется доработка", want: true},
		{name: "unknown status", body: "## Заключение ревизии\n\nunknown", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasUnresolvedExternalReviewRemarks([]integration.ReviewRemark{{ReplyToID: "thread-1", Body: test.body}}); got != test.want {
				t.Fatalf("hasUnresolvedExternalReviewRemarks() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestHasUnresolvedExternalReviewRemarksDoesNotTreatUnstructuredResolvedCommentAsConfirmation(t *testing.T) {
	t.Parallel()

	remarks := []integration.ReviewRemark{{
		State: "conversation",
		Body:  "Замечание: remark-2\n\nСостояние: resolved\n\nЗамечание закрыто: исправление подтверждено",
	}}
	if got := hasUnresolvedExternalReviewRemarks(remarks); !got {
		t.Fatal("unstructured resolved comment must remain unresolved")
	}
}

func TestHasUnresolvedExternalReviewRemarksKeepsQuotedConclusionHeaderAsRemark(t *testing.T) {
	t.Parallel()

	remarks := []integration.ReviewRemark{{
		State: "conversation",
		Body:  "## Замечание ревизии\n\nВ тексте цитируется заголовок: ## Заключение ревизии\n\nИсправить обработку",
	}}
	if got := hasUnresolvedExternalReviewRemarks(remarks); !got {
		t.Fatal("remark quoting the conclusion header must remain unresolved")
	}
}

func TestHasUnresolvedExternalReviewRemarksKeepsRemarkWithQuotedConclusionOnSeparateLine(t *testing.T) {
	t.Parallel()

	remarks := []integration.ReviewRemark{{
		State: "conversation",
		Body:  "## Замечание ревизии\n\nВ цитате:\n## Заключение ревизии\n\napprove\n\nИсправить обработку",
	}}
	if got := hasUnresolvedExternalReviewRemarks(remarks); !got {
		t.Fatal("remark quoting the conclusion on a separate line must remain unresolved")
	}
}

func TestBuildExecutionTaskPreservesIssueBodyLiteralStructuredInputBlock(t *testing.T) {
	t.Parallel()

	task := buildExecutionTask(&integration.CanonicalTask{
		ID:    "88",
		Title: "Fix prompt handoff",
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
	response    integration.Response
	err         error
	errOnSearch error
	request     integration.Request
	requests    []integration.Request
}

func (s *stubIntegrationExecutor) Execute(_ context.Context, request integration.Request) (integration.Response, error) {
	s.requests = append(s.requests, request)
	s.request = request
	if request.Operation == "search" && s.errOnSearch != nil {
		return integration.Response{}, s.errOnSearch
	}
	return s.response, s.err
}

type stubExecutionStarter struct {
	result  execution.LaunchResult
	err     error
	request execution.ActionInvocation
}

func (s *stubExecutionStarter) ExecuteAction(_ context.Context, request execution.ActionInvocation) (execution.ExecutionResult, error) {
	s.request = request
	return execution.ExecutionResult{
		Status:     s.result.Status,
		Summary:    s.result.Summary,
		Assignment: request.Assignment,
		Launch:     &s.result,
	}, s.err
}
