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
	if got := strings.Join(integrations.labels, "|"); got != "add:Ожидает экспертизы|add:Экспертиза пройдена|remove:Ожидает экспертизы" {
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

func TestServiceProcessTaskPassesExplicitRouteToDecision(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelAwaitingReview})
	integrations.searchErr = errors.New("search unavailable")
	decisions := &processingDecisionStub{results: []decision.ConsiderationResult{
		processingConsideration("task-description-assessment"),
	}}
	service := NewService(nil)
	service.integration = integrations
	service.decision = decisions
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}}}}

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Route: "task-description", Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if len(decisions.inputs) != 1 || decisions.inputs[0].Route != "task-description" {
		t.Fatalf("unexpected decision inputs: %#v", decisions.inputs)
	}
	if len(service.execution.(*processingExecutionStub).requests) != 1 {
		t.Fatalf("expected execution after deferred merge request search error")
	}
}

func TestServiceProcessTaskPassesFilledExecutionAssignmentToExecution(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelAwaitingReview})
	executions := &processingExecutionStub{results: []execution.ExecutionResult{
		{Status: "completed", Launch: &execution.LaunchResult{Status: "completed", StructuredOutput: &execution.StructuredOutput{Conclusion: &execution.StructuredConclusion{Status: "ok"}}}},
	}}
	service := NewService(nil)
	service.integration = integrations
	service.execution = executions

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if len(executions.requests) != 1 {
		t.Fatalf("expected one execution request, got %d", len(executions.requests))
	}
	assignment := executions.requests[0].Assignment
	if assignment == nil {
		t.Fatal("execution assignment must be passed to execution contour")
	}
	if assignment.Action != execution.ActionReviewPullRequest {
		t.Fatalf("unexpected action: %#v", assignment)
	}
	if strings.TrimSpace(assignment.ExpectedResult) == "" {
		t.Fatalf("expected_result must be filled: %#v", assignment)
	}
	if len(assignment.Constraints) == 0 {
		t.Fatalf("constraints must be filled from selected route: %#v", assignment)
	}
	if assignment.CanonicalTask == nil || assignment.CanonicalTask.Type != "task" || assignment.CanonicalTask.Number != 123 || assignment.CanonicalTask.Repository != "owner/name" {
		t.Fatalf("canonical_task must be filled from issue state: %#v", assignment.CanonicalTask)
	}
	if len(assignment.RelatedObjects) != 1 || assignment.RelatedObjects[0].Type != "merge-request" || assignment.RelatedObjects[0].Number != 17 || assignment.RelatedObjects[0].Attributes["head_ref"] != "123" {
		t.Fatalf("related_objects must include linked merge request: %#v", assignment.RelatedObjects)
	}
	if len(assignment.Reasons) == 0 || strings.TrimSpace(assignment.Reasons[0].Code) == "" || strings.TrimSpace(assignment.Reasons[0].Message) == "" {
		t.Fatalf("reasons must be filled from selected route: %#v", assignment.Reasons)
	}
	if assignment.StructuredInput == nil || !strings.Contains(assignment.StructuredInput.Task, "Task #123: Task") {
		t.Fatalf("structured_input.task must be filled from issue state: %#v", assignment.StructuredInput)
	}
	if len(assignment.StructuredInput.Constraints) == 0 {
		t.Fatalf("structured_input.constraints must mirror route constraints: %#v", assignment.StructuredInput)
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
	if got := strings.Join(integrations.labels, "|"); got != "add:Требует доработки|remove:Ожидает экспертизы" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceRunTaskActionReturnsMergeRequestSearchErrorForReviewAction(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{})
	integrations.searchErr = errors.New("search unavailable")
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{err: errors.New("decision must not be called")}
	service.execution = &processingExecutionStub{}

	_, err := service.RunTaskAction(context.Background(), TaskActionInput{TaskNumber: 123, Action: execution.ActionReviewPullRequest})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
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
	if got := strings.Join(integrations.labels, "|"); got != "add:Требует доработки|remove:Ожидает экспертизы" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceProcessTaskReworksReviewPassedTaskWithExternalRemarks(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelReviewPassed})
	integrations.reviewRemarks = []integration.ReviewRemark{{ExternalID: "comment-1", ReplyToID: "thread-1", State: "unresolved", Body: "Исправить обработку"}}
	service := NewService(nil)
	service.integration = integrations
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}}}}

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %#v", result.Cycles)
	}
	cycle := result.Cycles[0]
	if cycle.Consideration == nil || cycle.Consideration.ExecutionPlan == nil || cycle.Consideration.ExecutionPlan.Action != execution.ActionApplyReviewComments {
		t.Fatalf("expected apply-review-comments route, got %#v", cycle.Consideration)
	}
	if cycle.MergeRequestExternalState == nil || !cycle.MergeRequestExternalState.HasUnresolvedReviewRemarks {
		t.Fatalf("expected unresolved external remarks: %#v", cycle.MergeRequestExternalState)
	}
	if got := strings.Join(integrations.labels, "|"); got != "add:Ожидает экспертизы|remove:Экспертиза пройдена" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceProcessTaskIgnoresConversationCommentsForExternalRemarks(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelReviewPassed})
	integrations.reviewRemarks = []integration.ReviewRemark{{ExternalID: "comment-1", State: "conversation", Body: "Общий комментарий"}}
	service := NewService(nil)
	service.integration = integrations
	service.execution = &processingExecutionStub{}

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %#v", result.Cycles)
	}
	cycle := result.Cycles[0]
	if cycle.MergeRequestExternalState != nil {
		t.Fatalf("conversation comments must not block completion: %#v", cycle.MergeRequestExternalState)
	}
	if cycle.Consideration == nil || cycle.Consideration.Status != decision.ConsiderationStatusCompleted {
		t.Fatalf("expected completed consideration, got %#v", cycle.Consideration)
	}
	if len(integrations.labels) != 0 {
		t.Fatalf("unexpected label operations: %#v", integrations.labels)
	}
}

func TestServiceProcessTaskReworksReviewPassedTaskWithMergeConflict(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelReviewPassed})
	integrations.mergeRequest.Attributes = map[string]string{"mergeable_state": "dirty"}
	service := NewService(nil)
	service.integration = integrations
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}}}}

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %#v", result.Cycles)
	}
	cycle := result.Cycles[0]
	if cycle.Consideration == nil || cycle.Consideration.ExecutionPlan == nil || cycle.Consideration.ExecutionPlan.Action != execution.ActionApplyReviewComments {
		t.Fatalf("expected apply-review-comments route, got %#v", cycle.Consideration)
	}
	if cycle.MergeRequestExternalState == nil || !cycle.MergeRequestExternalState.HasMergeConflict {
		t.Fatalf("expected merge conflict state: %#v", cycle.MergeRequestExternalState)
	}
	if got := strings.Join(integrations.labels, "|"); got != "add:Ожидает экспертизы|remove:Экспертиза пройдена" {
		t.Fatalf("unexpected label operations: %s", got)
	}
}

func TestServiceProcessTaskKeepsMergeConflictWhenReviewRemarksFail(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelReviewPassed})
	integrations.mergeRequest.Attributes = map[string]string{"mergeable_state": "dirty"}
	integrations.commentsErr = errors.New("comments unavailable")
	service := NewService(nil)
	service.integration = integrations
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}}}}

	result, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	cycle := result.Cycles[0]
	if cycle.MergeRequestExternalState == nil || !cycle.MergeRequestExternalState.HasMergeConflict {
		t.Fatalf("expected merge conflict state: %#v", cycle.MergeRequestExternalState)
	}
	if cycle.Consideration == nil || cycle.Consideration.ExecutionPlan == nil || cycle.Consideration.ExecutionPlan.Action != execution.ActionApplyReviewComments {
		t.Fatalf("expected apply-review-comments route, got %#v", cycle.Consideration)
	}
}

func TestServiceProcessTaskDoesNotLoadExternalRemarksBeforeNonCompletedRoute(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{})
	integrations.commentsErr = errors.New("comments must not be loaded")
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{results: []decision.ConsiderationResult{
		processingConsideration(execution.ActionStartImplementationPR),
	}}
	service.execution = &processingExecutionStub{results: []execution.ExecutionResult{{Status: "completed", Launch: &execution.LaunchResult{Status: "completed"}}}}

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err != nil {
		t.Fatalf("process task: %v", err)
	}
	for _, request := range integrations.requests {
		if request.Operation == "comments" {
			t.Fatalf("comments must not be loaded for non-completed route: %#v", integrations.requests)
		}
	}
}

func TestMergeRequestHasConflictUsesGitHubMergeStateAttributes(t *testing.T) {
	t.Parallel()

	mergeRequest := &integration.MergeRequest{Attributes: map[string]string{"merge_state_status": "DIRTY"}}
	if !mergeRequestHasConflict(mergeRequest) {
		t.Fatal("expected merge_state_status=DIRTY to be treated as conflict")
	}

	mergeRequest = &integration.MergeRequest{Attributes: map[string]string{"mergeable": "CONFLICTING"}}
	if !mergeRequestHasConflict(mergeRequest) {
		t.Fatal("expected mergeable=CONFLICTING to be treated as conflict")
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

func TestServiceProcessTaskReturnsMergeRequestSearchErrorForReviewLabel(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelAwaitingReview})
	integrations.searchErr = errors.New("search unavailable")
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{results: []decision.ConsiderationResult{
		{
			Status:  decision.ConsiderationStatusManualIntervention,
			Failure: &decision.DecisionFailure{Code: "merge_request_missing"},
		},
	}, err: errors.New("merge request is required for action \"review-pull-request\"")}
	service.execution = &processingExecutionStub{}

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
	}
	if service.decision.(*processingDecisionStub).calls != 1 {
		t.Fatal("decision must run before merge request restoration failure is returned")
	}
}

func TestServiceProcessTaskReturnsMergeRequestSearchErrorForExplicitReviewRoute(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{})
	integrations.searchErr = errors.New("search unavailable")
	service := NewService(nil)
	service.integration = integrations
	service.decision = &processingDecisionStub{
		results: []decision.ConsiderationResult{{
			Status:  decision.ConsiderationStatusManualIntervention,
			Failure: &decision.DecisionFailure{Code: "merge_request_missing"},
		}},
		err: errors.New("merge request is required for action \"review-pull-request\""),
	}
	service.execution = &processingExecutionStub{}

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Route: "pull-request-review", Once: true})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
	}
}

func TestServiceProcessTaskReturnsMergeRequestSearchErrorForReviewPassedLabel(t *testing.T) {
	t.Parallel()

	integrations := newProcessingIntegrationStub([]string{LabelReviewPassed})
	integrations.searchErr = errors.New("search unavailable")
	service := NewService(nil)
	service.integration = integrations
	service.execution = &processingExecutionStub{}

	_, err := service.ProcessTask(context.Background(), TaskProcessingInput{TaskNumber: 123, Once: true})
	if err == nil {
		t.Fatal("expected merge request search error")
	}
	if !strings.Contains(err.Error(), "search unavailable") {
		t.Fatalf("expected original search error, got: %v", err)
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
	issue         integration.TrackerIssue
	mergeRequest  integration.MergeRequest
	reviewRemarks []integration.ReviewRemark
	labels        []string
	requests      []integration.Request
	searchErr     error
	commentsErr   error
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
		mergeRequest: integration.MergeRequest{
			System:     "github",
			Repository: "owner/name",
			Number:     17,
			Title:      "Task",
			State:      "OPEN",
			BaseRef:    "main",
			HeadRef:    "123",
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
		mergeRequest := s.mergeRequest
		mergeRequest.Traits = append([]string(nil), s.mergeRequest.Traits...)
		if s.mergeRequest.Attributes != nil {
			mergeRequest.Attributes = make(map[string]string, len(s.mergeRequest.Attributes))
			for key, value := range s.mergeRequest.Attributes {
				mergeRequest.Attributes[key] = value
			}
		}
		return integration.Response{MergeRequests: []integration.MergeRequest{mergeRequest}}, nil
	case request.IntegrationType == integrationmodel.IntegrationTypeRepository && request.Operation == "comments":
		if s.commentsErr != nil {
			return integration.Response{}, s.commentsErr
		}
		return integration.Response{ReviewRemarks: append([]integration.ReviewRemark(nil), s.reviewRemarks...)}, nil
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
	inputs  []decision.ConsiderationInput
}

func (s *processingDecisionStub) Consider(_ context.Context, input decision.ConsiderationInput) (decision.ConsiderationResult, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		if s.calls >= len(s.results) {
			return decision.ConsiderationResult{}, s.err
		}
		result := s.results[s.calls]
		s.calls++
		return result, s.err
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
