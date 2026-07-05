package reactivity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rasungatullin/progress/internal/decision"
	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceNormalizeBuildsSignal(t *testing.T) {
	t.Parallel()

	service := NewService(nil)
	service.now = func() time.Time { return time.Unix(10, 0) }

	result, err := service.Normalize(context.Background(), Event{
		Source:     "github",
		Kind:       "issue-comment",
		ObjectType: "issue",
		ObjectID:   "123",
		Metadata:   map[string]string{"repository": "owner/name"},
	}, Process{
		Name:            "github-issue-events",
		EventSource:     "github",
		EventKind:       "issue-comment",
		SignalKind:      "task-updated",
		IntegrationType: "tracker",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Status != StatusAccepted {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Signal == nil {
		t.Fatal("expected signal")
	}
	if result.Signal.Process != "github-issue-events" {
		t.Fatalf("unexpected process: %q", result.Signal.Process)
	}
	if result.Signal.Kind != "task-updated" {
		t.Fatalf("unexpected signal kind: %q", result.Signal.Kind)
	}
	if result.Signal.IntegrationType != "tracker" {
		t.Fatalf("unexpected integration type: %q", result.Signal.IntegrationType)
	}
	if result.Signal.Metadata["repository"] != "owner/name" {
		t.Fatalf("metadata was not copied: %#v", result.Signal.Metadata)
	}
	if !result.Signal.OccurredAt.Equal(time.Unix(10, 0)) {
		t.Fatalf("unexpected occurrence time: %s", result.Signal.OccurredAt)
	}
}

func TestServiceNormalizeIgnoresForeignEvent(t *testing.T) {
	t.Parallel()

	result, err := NewService(nil).Normalize(context.Background(), Event{
		Source:   "mattermost",
		Kind:     "message",
		ObjectID: "abc",
	}, Process{
		Name:        "github-events",
		EventSource: "github",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Status != StatusIgnored {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Signal != nil {
		t.Fatalf("ignored event must not return signal: %#v", result.Signal)
	}
}

func TestServiceNormalizeIgnoresForeignEventBeforeObjectValidation(t *testing.T) {
	t.Parallel()

	result, err := NewService(nil).Normalize(context.Background(), Event{
		Source: "mattermost",
		Kind:   "message",
	}, Process{
		Name:        "github-events",
		EventSource: "github",
		EventKind:   "issue-comment",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Status != StatusIgnored {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if len(result.Reasons) == 0 || result.Reasons[0].Code != "source_mismatch" {
		t.Fatalf("unexpected reasons: %#v", result.Reasons)
	}
}

func TestServiceProcessTaskRepeatsUntilDecisionHasNoNextOperation(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{})
	decisions := &processingDecisionStub{results: []decision.ConsiderationResult{
		processingConsideration(execution.ActionStartImplementationPR),
		processingConsideration(execution.ActionReviewPullRequest),
		{Status: decision.ConsiderationStatusCompleted, Route: decision.ProcessingRoute{Name: "task-processing-completed"}},
	}}
	executions := &processingExecutionStub{results: []execution.ExecutionResult{
		{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}},
		{Status: "completed", Launch: &execution.LaunchResult{Status: "completed", StructuredOutput: &execution.StructuredOutput{Conclusion: &execution.StructuredConclusion{Status: "ok"}}}},
	}}
	service := NewService(nil)
	service.integration = integrations
	service.decision = decisions
	service.execution = executions

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if !result.Completed || result.StopReason != StopReasonNoNextOperation {
		t.Fatalf("unexpected completion: %#v", result)
	}
	if len(result.Cycles) != 3 {
		t.Fatalf("expected three cycles, got %#v", result.Cycles)
	}
	if len(executions.requests) != 2 {
		t.Fatalf("expected two executions, got %d", len(executions.requests))
	}
	if got := strings.Join(integrations.labels, "|"); got != "add:Ожидает экспертизы|remove:Ожидает экспертизы|add:Экспертиза пройдена" {
		t.Fatalf("unexpected label operations: %s", got)
	}
	mergeRequestSearches := 0
	for _, request := range integrations.requests {
		if request.IntegrationType != integrationmodel.IntegrationTypeRepository || request.Operation != "search" {
			continue
		}
		mergeRequestSearches++
		if request.Query != "head:123" {
			t.Fatalf("merge-request search must be constrained by head ref: %#v", request)
		}
	}
	if mergeRequestSearches == 0 {
		t.Fatal("expected merge-request search request")
	}
	if result.FinalIssue == nil || !containsLabel(result.FinalIssue.Labels, LabelReviewPassed) {
		t.Fatalf("final issue must include passed label: %#v", result.FinalIssue)
	}
}

func TestServiceProcessTaskOnceStopsAfterFirstCycle(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{})
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{results: []decision.ConsiderationResult{
		processingConsideration(execution.ActionStartImplementationPR),
	}}
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{
		{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}},
	}}

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task once: %v", err)
	}
	if result.Completed || result.StopReason != StopReasonSingleCycle {
		t.Fatalf("unexpected single-cycle result: %#v", result)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %#v", result.Cycles)
	}
	if got := strings.Join(integrations.labels, "|"); got != "add:Ожидает экспертизы" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceRunTaskActionSkipsDecisionAndMarksRework(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelAwaitingReview})
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{err: errors.New("decision must not be called")}
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{
		{
			Status: "completed",
			Launch: &execution.LaunchResult{Status: "completed", StructuredOutput: &execution.StructuredOutput{
				Remarks: []execution.StructuredRemark{{Title: "Замечание"}},
			}},
		},
	}}

	result, err := service.RunTaskAction(context.Background(), TaskActionInput{TaskNumber: 123, Action: execution.ActionReviewPullRequest})
	if err != nil {
		t.Fatalf("run task action: %v", err)
	}
	if result.StopReason != StopReasonSingleCycle {
		t.Fatalf("unexpected stop reason: %#v", result)
	}
	if len(result.Cycles) != 1 || result.Cycles[0].ReviewPassed == nil || *result.Cycles[0].ReviewPassed {
		t.Fatalf("review must be marked as failed: %#v", result.Cycles)
	}
	if got := strings.Join(integrations.labels, "|"); got != "remove:Ожидает экспертизы|add:Требует доработки" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceProcessTaskReviewWithoutConclusionMarksRework(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelAwaitingReview})
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{results: []decision.ConsiderationResult{
		processingConsideration(execution.ActionReviewPullRequest),
	}}
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{
		{
			Status: "completed",
			Launch: &execution.LaunchResult{
				Status:           "completed",
				StructuredOutput: &execution.StructuredOutput{Summary: "Reviewed"},
			},
		},
	}}

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %#v", result.Cycles)
	}
	if result.Cycles[0].ReviewPassed == nil || *result.Cycles[0].ReviewPassed {
		t.Fatalf("review must be marked as failed: %#v", result.Cycles[0].ReviewPassed)
	}
	if got := strings.Join(integrations.labels, "|"); got != "remove:Ожидает экспертизы|add:Требует доработки" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceProcessTaskReturnsMergeRequestSearchErrorForReviewLabel(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelAwaitingReview})
	integrations.searchErr = errors.New("search unavailable")
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{results: []decision.ConsiderationResult{
		processingConsideration(execution.ActionReviewPullRequest),
	}}
	service.execution = &processingExecutionStub{}

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
	}
	if service.decision.(*processingDecisionStub).calls != 0 {
		t.Fatal("decision must not run after merge request restoration failure")
	}
}

func TestReviewExecutionPassedRequiresConclusionStatus(t *testing.T) {
	t.Parallel()

	if !reviewExecutionPassed(&execution.ExecutionResult{
		Status: "completed",
		Launch: &execution.LaunchResult{
			Status:           "completed",
			StructuredOutput: &execution.StructuredOutput{Conclusion: &execution.StructuredConclusion{Status: "ok"}},
		},
	}) {
		t.Fatal("expected review to pass with ok conclusion")
	}

	if reviewExecutionPassed(&execution.ExecutionResult{
		Status: "completed",
		Launch: &execution.LaunchResult{
			Status:           "completed",
			StructuredOutput: &execution.StructuredOutput{Summary: "Reviewed"},
		},
	}) {
		t.Fatal("expected review to fail without conclusion")
	}

	if reviewExecutionPassed(&execution.ExecutionResult{
		Status: "completed",
		Launch: &execution.LaunchResult{
			Status:           "completed",
			StructuredOutput: &execution.StructuredOutput{Conclusion: &execution.StructuredConclusion{Status: "needs-work"}},
		},
	}) {
		t.Fatal("expected review to fail on negative conclusion status")
	}

	if !reviewExecutionPassed(&execution.ExecutionResult{
		Status: "completed",
		Launch: &execution.LaunchResult{
			Status: "completed",
			StructuredOutput: &execution.StructuredOutput{
				Conclusion: &execution.StructuredConclusion{Status: "approve"},
				Remarks:    []execution.StructuredRemark{{ID: "remark-1", Status: "resolved", Title: "Замечание"}},
			},
		},
	}) {
		t.Fatal("expected review to pass with resolved remark")
	}

	if reviewExecutionPassed(&execution.ExecutionResult{
		Status: "completed",
		Launch: &execution.LaunchResult{
			Status: "completed",
			StructuredOutput: &execution.StructuredOutput{
				Conclusion: &execution.StructuredConclusion{Status: "approve"},
				Remarks:    []execution.StructuredRemark{{ID: "remark-2", Status: "unresolved", Title: "Замечание"}},
			},
		},
	}) {
		t.Fatal("expected review to fail on unresolved remark")
	}
}

func processingConsideration(action string) decision.ConsiderationResult {
	return decision.ConsiderationResult{
		Status: decision.ConsiderationStatusExecution,
		Route:  decision.ProcessingRoute{Name: "test-route"},
		ExecutionPlan: &decision.ExecutionPlan{
			Action: action,
			Assignment: &execution.ExecutionAssignment{
				Action: action,
				CanonicalTask: &execution.ObjectRef{
					Type:       "task",
					Repository: "owner/name",
					Number:     123,
					Title:      "Task",
				},
			},
		},
	}
}

type processingIntegrationStub struct {
	issue     integration.TrackerIssue
	labels    []string
	requests  []integration.Request
	searchErr error
}

func newProcessingIntegrationStub(labels []string) *processingIntegrationStub {
	return &processingIntegrationStub{
		issue: integration.TrackerIssue{
			System:     "github",
			Repository: "owner/name",
			Number:     123,
			Title:      "Task",
			State:      "OPEN",
			Labels:     append([]string(nil), labels...),
		},
	}
}

func (s *processingIntegrationStub) Execute(_ context.Context, request integration.Request) (integration.Response, error) {
	s.requests = append(s.requests, request)
	switch {
	case request.IntegrationType == integrationmodel.IntegrationTypeTracker && request.Operation == "get":
		issue := s.issue
		issue.Labels = append([]string(nil), s.issue.Labels...)
		return integration.Response{Issue: &issue}, nil
	case request.IntegrationType == integrationmodel.IntegrationTypeRepository && request.Operation == "search":
		if s.searchErr != nil {
			return integration.Response{}, s.searchErr
		}
		return integration.Response{MergeRequests: []integration.MergeRequest{{
			System:     "github",
			Repository: "owner/name",
			Number:     17,
			Title:      "Task",
			State:      "OPEN",
			BaseRef:    "main",
			HeadRef:    "123",
		}}}, nil
	case request.IntegrationType == integrationmodel.IntegrationTypeTracker && request.Operation == "add":
		s.labels = append(s.labels, "add:"+strings.Join(request.Labels, ","))
		s.issue.Labels = append(s.issue.Labels, request.Labels...)
		return integration.Response{OperationResult: &integration.OperationResult{Status: "ok"}}, nil
	case request.IntegrationType == integrationmodel.IntegrationTypeTracker && request.Operation == "remove":
		s.labels = append(s.labels, "remove:"+strings.Join(request.Labels, ","))
		s.issue.Labels = removeLabels(s.issue.Labels, request.Labels)
		return integration.Response{OperationResult: &integration.OperationResult{Status: "ok"}}, nil
	default:
		return integration.Response{}, nil
	}
}

type processingDecisionStub struct {
	results []decision.ConsiderationResult
	err     error
	calls   int
}

func (s *processingDecisionStub) Consider(_ context.Context, _ decision.ConsiderationInput) (decision.ConsiderationResult, error) {
	if s.err != nil {
		return decision.ConsiderationResult{}, s.err
	}
	if s.calls >= len(s.results) {
		return decision.ConsiderationResult{Status: decision.ConsiderationStatusCompleted}, nil
	}
	result := s.results[s.calls]
	s.calls++
	return result, nil
}

type processingExecutionStub struct {
	results  []execution.ExecutionResult
	requests []execution.ActionInvocation
}

func (s *processingExecutionStub) ExecuteAction(_ context.Context, request execution.ActionInvocation) (execution.ExecutionResult, error) {
	s.requests = append(s.requests, request)
	if len(s.results) == 0 {
		return execution.ExecutionResult{Status: "completed", Assignment: request.Assignment, Launch: &execution.LaunchResult{Status: "completed"}}, nil
	}
	result := s.results[0]
	s.results = s.results[1:]
	result.Assignment = request.Assignment
	if result.Launch == nil {
		result.Launch = &execution.LaunchResult{Status: result.Status}
	}
	return result, nil
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
