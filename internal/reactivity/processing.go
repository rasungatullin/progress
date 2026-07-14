package reactivity

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/decision"
	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
)

const (
	StopReasonNoNextOperation = "no-next-operation"
	StopReasonSingleCycle     = "single-cycle"
)

type TaskProcessingInput struct {
	TaskNumber int
	Route      string
	Once       bool
	MaxCycles  int
}

type TaskActionInput struct {
	TaskNumber int
	Action     string
}

type LabelChange struct {
	Operation string
	Labels    []string
}

type TaskProcessingCycle struct {
	Index                     int
	Issue                     *integration.TrackerIssue
	MergeRequest              *integration.MergeRequest
	MergeRequestExternalState *decision.MergeRequestExternalState
	Consideration             *decision.ConsiderationResult
	Action                    string
	ExecutionResult           *execution.ExecutionResult
	Execution                 *execution.LaunchResult
	LabelChanges              []LabelChange
	ReviewPassed              *bool
}

type TaskProcessingResult struct {
	TaskNumber int
	Cycles     []TaskProcessingCycle
	Completed  bool
	StopReason string
	FinalIssue *integration.TrackerIssue
}

func (s *Service) ProcessTask(ctx context.Context, input TaskProcessingInput) (TaskProcessingResult, error) {
	if s == nil {
		s = NewService(nil)
	}
	s.ensureProcessingDependencies()
	if input.TaskNumber <= 0 {
		return TaskProcessingResult{}, fmt.Errorf("номер задачи должен быть больше нуля")
	}

	maxCycles := input.MaxCycles
	if maxCycles <= 0 {
		maxCycles = defaultMaxProcessingCycles
	}

	result := TaskProcessingResult{TaskNumber: input.TaskNumber}
	var knownMergeRequest *integration.MergeRequest
	for index := 1; index <= maxCycles; index++ {
		cycle, err := s.runDecisionCycle(ctx, input.TaskNumber, input.Route, index, knownMergeRequest)
		result.Cycles = append(result.Cycles, cycle)
		result.FinalIssue = cycle.Issue
		if err != nil {
			return result, err
		}
		if cycle.ExecutionResult != nil && cycle.ExecutionResult.MergeRequest != nil {
			knownMergeRequest = integrationMergeRequestFromExecutionResult(cycle.ExecutionResult.MergeRequest)
		}
		if cycle.Consideration == nil || cycle.Consideration.ExecutionPlan == nil {
			result.Completed = true
			result.StopReason = StopReasonNoNextOperation
			return result, nil
		}
		if input.Once {
			result.StopReason = StopReasonSingleCycle
			return result, nil
		}
	}

	return result, fmt.Errorf("обработка задачи %d превысила лимит циклов: %d", input.TaskNumber, maxCycles)
}

func (s *Service) RunTaskAction(ctx context.Context, input TaskActionInput) (TaskProcessingResult, error) {
	if s == nil {
		s = NewService(nil)
	}
	s.ensureProcessingDependencies()
	if input.TaskNumber <= 0 {
		return TaskProcessingResult{}, fmt.Errorf("номер задачи должен быть больше нуля")
	}
	action := strings.TrimSpace(input.Action)
	if action == "" {
		return TaskProcessingResult{}, fmt.Errorf("действие должно быть задано")
	}

	cycle := TaskProcessingCycle{
		Index:  1,
		Action: canonicalProcessingAction(action),
	}
	if cycle.Action == "" {
		cycle.Action = action
	}

	issue, mergeRequest, externalState, err := s.loadTaskState(ctx, input.TaskNumber, requiresMergeRequest(cycle.Action))
	if err != nil {
		return TaskProcessingResult{TaskNumber: input.TaskNumber}, err
	}
	cycle.Issue = issue
	cycle.MergeRequest = mergeRequest
	cycle.MergeRequestExternalState = externalState
	if requiresMergeRequest(cycle.Action) && mergeRequest == nil {
		result := TaskProcessingResult{TaskNumber: input.TaskNumber, Cycles: []TaskProcessingCycle{cycle}, FinalIssue: issue, StopReason: StopReasonSingleCycle}
		return result, fmt.Errorf("для действия %q требуется связанный запрос на слияние", cycle.Action)
	}

	executionResult, err := s.execution.ExecuteAction(ctx, actionInvocationFromTaskState(cycle.Action, issue, mergeRequest))
	cycle.ExecutionResult = &executionResult
	cycle.Execution = executionResult.Launch
	if err != nil {
		result := TaskProcessingResult{TaskNumber: input.TaskNumber, Cycles: []TaskProcessingCycle{cycle}, FinalIssue: issue, StopReason: StopReasonSingleCycle}
		return result, err
	}

	labelChanges, reviewPassed, err := s.applyTaskLabelsAfterAction(ctx, issue, cycle.Action, &executionResult)
	cycle.LabelChanges = labelChanges
	cycle.ReviewPassed = reviewPassed
	result := TaskProcessingResult{TaskNumber: input.TaskNumber, Cycles: []TaskProcessingCycle{cycle}, FinalIssue: issue, StopReason: StopReasonSingleCycle}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) runDecisionCycle(ctx context.Context, taskNumber int, route string, index int, knownMergeRequest *integration.MergeRequest) (TaskProcessingCycle, error) {
	issue, mergeRequest, externalState, mergeRequestErr, err := s.loadTaskStateWithMergeRequestError(ctx, taskNumber, false, knownMergeRequest)
	if err != nil {
		return TaskProcessingCycle{Index: index}, err
	}

	cycle := TaskProcessingCycle{
		Index:                     index,
		Issue:                     issue,
		MergeRequest:              mergeRequest,
		MergeRequestExternalState: externalState,
	}
	consideration, err := s.decision.Consider(ctx, decision.ConsiderationInput{Route: route, Context: decision.DecisionContext{
		Signal:                    decision.Signal{Source: decision.SignalSourceTask, Kind: decision.SignalKindTask, TaskNumber: taskNumber},
		Issue:                     issue,
		MergeRequest:              mergeRequest,
		MergeRequestExternalState: externalState,
	}})
	cycle.Consideration = &consideration
	if err != nil {
		if mergeRequestErr != nil && consideration.Failure != nil && consideration.Failure.Code == "merge_request_missing" {
			return cycle, fmt.Errorf("восстановить связанный запрос на слияние для задачи %d: %w", taskNumber, mergeRequestErr)
		}
		return cycle, err
	}
	if consideration.Status == decision.ConsiderationStatusCompleted && consideration.ExecutionPlan == nil && mergeRequest != nil && externalState == nil {
		externalState, err = s.loadMergeRequestExternalState(ctx, mergeRequest)
		if err != nil {
			return cycle, fmt.Errorf("восстановить внешнее состояние запроса на слияние для задачи %d: %w", taskNumber, err)
		}
		if externalState != nil {
			cycle.MergeRequestExternalState = externalState
			consideration, err = s.decision.Consider(ctx, decision.ConsiderationInput{Route: route, Context: decision.DecisionContext{
				Signal:                    decision.Signal{Source: decision.SignalSourceTask, Kind: decision.SignalKindTask, TaskNumber: taskNumber},
				Issue:                     issue,
				MergeRequest:              mergeRequest,
				MergeRequestExternalState: externalState,
			}})
			cycle.Consideration = &consideration
			if err != nil {
				return cycle, err
			}
		}
	}
	if consideration.ExecutionPlan == nil {
		return cycle, nil
	}

	cycle.Action = strings.TrimSpace(consideration.ExecutionPlan.Action)
	assignment := assignmentWithMergeRequest(consideration.ExecutionPlan.Assignment, mergeRequest)
	executionResult, err := s.execution.ExecuteAction(ctx, execution.ActionInvocation{Assignment: assignment})
	cycle.ExecutionResult = &executionResult
	cycle.Execution = executionResult.Launch
	if err != nil {
		return cycle, err
	}

	labelChanges, reviewPassed, err := s.applyTaskLabelsAfterAction(ctx, issue, cycle.Action, &executionResult)
	cycle.LabelChanges = labelChanges
	cycle.ReviewPassed = reviewPassed
	if err != nil {
		return cycle, err
	}
	return cycle, nil
}

func assignmentWithMergeRequest(assignment *execution.ExecutionAssignment, mergeRequest *integration.MergeRequest) *execution.ExecutionAssignment {
	if assignment == nil || mergeRequest == nil || mergeRequest.Number <= 0 {
		return assignment
	}
	copyOfAssignment := *assignment
	copyOfAssignment.RelatedObjects = append([]execution.ObjectRef(nil), assignment.RelatedObjects...)
	for _, object := range copyOfAssignment.RelatedObjects {
		if object.Number == mergeRequest.Number && (object.Type == "merge-request" || object.Type == "pull-request" || object.Type == "pr") {
			return &copyOfAssignment
		}
	}
	copyOfAssignment.RelatedObjects = append(copyOfAssignment.RelatedObjects, executionObjectRefFromMergeRequest(mergeRequest))
	return &copyOfAssignment
}

func (s *Service) ensureProcessingDependencies() {
	if s.logger == nil {
		s.logger = NewService(nil).logger
	}
	if s.integration == nil {
		s.integration = integration.NewConfiguredService(s.logger)
	}
	if s.decision == nil {
		s.decision = decision.NewService(s.logger)
	}
	if s.execution == nil {
		s.execution = execution.NewService(s.logger)
	}
}

func integrationMergeRequestFromExecutionResult(value *execution.MergeRequest) *integration.MergeRequest {
	if value == nil || value.Number <= 0 {
		return nil
	}
	return &integration.MergeRequest{
		System: value.System, Repository: value.Repository, Number: value.Number,
		Title: value.Title, Body: value.Body, State: value.State,
		BaseRef: value.BaseRef, HeadRef: value.HeadRef, URL: value.URL,
	}
}

func (s *Service) loadTaskState(ctx context.Context, taskNumber int, requireMergeRequest bool) (*integration.TrackerIssue, *integration.MergeRequest, *decision.MergeRequestExternalState, error) {
	issue, mergeRequest, externalState, mergeRequestErr, err := s.loadTaskStateWithMergeRequestError(ctx, taskNumber, requireMergeRequest, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	if mergeRequestErr != nil {
		return nil, nil, nil, mergeRequestErr
	}
	return issue, mergeRequest, externalState, nil
}

func (s *Service) loadTaskStateWithMergeRequestError(ctx context.Context, taskNumber int, requireMergeRequest bool, knownMergeRequest *integration.MergeRequest) (*integration.TrackerIssue, *integration.MergeRequest, *decision.MergeRequestExternalState, error, error) {
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationTypeTracker,
		Resource:        "issue",
		ObjectType:      "issue",
		Operation:       "get",
		ID:              strconv.Itoa(taskNumber),
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if response.Issue == nil {
		return nil, nil, nil, nil, fmt.Errorf("контур интеграции не вернул задачу %d", taskNumber)
	}

	mergeRequest := knownMergeRequest
	if mergeRequest != nil {
		copyOfMergeRequest := *mergeRequest
		fresh, refreshErr := s.integration.Execute(ctx, integration.Request{IntegrationType: integrationTypeRepository, Resource: "merge-request", ObjectType: "merge-request", Operation: "get", Repository: copyOfMergeRequest.Repository, RepoProvided: strings.TrimSpace(copyOfMergeRequest.Repository) != "", MergeRequestNumber: copyOfMergeRequest.Number})
		if refreshErr == nil {
			if refreshed, ok := integrationMergeRequestFromResponse(fresh); ok {
				copyOfMergeRequest = refreshed
			}
		}
		mergeRequest = &copyOfMergeRequest
	} else {
		mergeRequest, err = s.findTaskMergeRequest(ctx, response.Issue)
		if err != nil {
			if requireMergeRequest || taskLabelsRequireCompletionMergeRequest(response.Issue.Labels) {
				return nil, nil, nil, err, fmt.Errorf("восстановить связанный запрос на слияние для задачи %d: %w", taskNumber, err)
			}
			s.logger.Printf("Связанный запрос на слияние не восстановлен: задача=%d ошибка=%v", taskNumber, err)
		}
	}
	if mergeRequest != nil {
		externalState, stateErr := s.loadMergeRequestExternalState(ctx, mergeRequest)
		if stateErr != nil {
			return response.Issue, mergeRequest, nil, nil, stateErr
		}
		return response.Issue, mergeRequest, externalState, nil, nil
	}
	return response.Issue, mergeRequest, nil, err, nil
}

func integrationMergeRequestFromResponse(response integration.Response) (integration.MergeRequest, bool) {
	if response.MergeRequest == nil {
		return integration.MergeRequest{}, false
	}
	return *response.MergeRequest, true
}

func (s *Service) loadMergeRequestExternalState(ctx context.Context, mergeRequest *integration.MergeRequest) (*decision.MergeRequestExternalState, error) {
	if mergeRequest == nil {
		return nil, nil
	}

	state := &decision.MergeRequestExternalState{
		HasMergeConflict: mergeRequestHasConflict(mergeRequest),
	}
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType:    integrationTypeRepository,
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

func (s *Service) findTaskMergeRequest(ctx context.Context, issue *integration.TrackerIssue) (*integration.MergeRequest, error) {
	if issue == nil || strings.TrimSpace(issue.ID) == "" || strings.TrimSpace(issue.Repository) == "" {
		return nil, nil
	}

	head := issue.ID
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationTypeRepository,
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

func (s *Service) applyTaskLabelsAfterAction(ctx context.Context, issue *integration.TrackerIssue, action string, result *execution.ExecutionResult) ([]LabelChange, *bool, error) {
	add, remove, reviewPassed := labelTransitionForAction(action, result)
	changes := make([]LabelChange, 0, 2)

	add = labelsMissing(issue.Labels, add)
	if len(add) != 0 {
		if err := s.changeTaskLabels(ctx, issue, "add", add); err != nil {
			return changes, reviewPassed, err
		}
		issue.Labels = append(issue.Labels, add...)
		changes = append(changes, LabelChange{Operation: "add", Labels: add})
	}

	remove = labelsPresent(issue.Labels, remove)
	if len(remove) != 0 {
		if err := s.changeTaskLabels(ctx, issue, "remove", remove); err != nil {
			return changes, reviewPassed, err
		}
		issue.Labels = removeLabels(issue.Labels, remove)
		changes = append(changes, LabelChange{Operation: "remove", Labels: remove})
	}

	return changes, reviewPassed, nil
}

func (s *Service) changeTaskLabels(ctx context.Context, issue *integration.TrackerIssue, operation string, labels []string) error {
	_, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationTypeTracker,
		Resource:        "label",
		ObjectType:      "label",
		Operation:       operation,
		Repository:      issue.Repository,
		RepoProvided:    strings.TrimSpace(issue.Repository) != "",
		ID:              issue.ID,
		Labels:          append([]string(nil), labels...),
	})
	return err
}

func labelTransitionForAction(action string, result *execution.ExecutionResult) ([]string, []string, *bool) {
	switch canonicalProcessingAction(action) {
	case execution.ActionStartImplementationPR:
		return []string{LabelAwaitingReview}, []string{LabelNeedsRework, LabelReviewPassed}, nil
	case execution.ActionApplyReviewComments:
		return []string{LabelAwaitingReview}, []string{LabelNeedsRework, LabelReviewPassed}, nil
	case execution.ActionReviewPullRequest:
		passed := reviewExecutionPassed(result)
		if passed {
			return []string{LabelReviewPassed}, []string{LabelAwaitingReview, LabelNeedsRework}, &passed
		}
		return []string{LabelNeedsRework}, []string{LabelAwaitingReview, LabelReviewPassed}, &passed
	default:
		return nil, nil, nil
	}
}

func reviewExecutionPassed(result *execution.ExecutionResult) bool {
	if result == nil || !executionStatusCompleted(result.Status) {
		return false
	}
	var output *execution.StructuredOutput
	if result.Launch != nil {
		output = result.Launch.StructuredOutput
	}
	if output == nil {
		return false
	}
	if hasReviewRemarks(output.Remarks) {
		return false
	}
	if output.Conclusion == nil {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(output.Conclusion.Status)) {
	case "ok", "ready", "passed", "approved", "approve", "success", "completed":
		return true
	case "failed", "blocked", "needs-work", "needs-rework", "changes-requested", "needs-follow-up", "rejected":
		return false
	default:
		return false
	}
}

func executionStatusCompleted(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "ok", "success", "completed", "succeeded":
		return true
	default:
		return false
	}
}

func hasReviewRemarks(remarks []execution.StructuredRemark) bool {
	for _, remark := range remarks {
		if isResolvedReviewRemark(remark) || !isBlockingReviewRemark(remark) {
			continue
		}
		if strings.TrimSpace(remark.ID) != "" ||
			strings.TrimSpace(remark.Status) != "" ||
			strings.TrimSpace(remark.Severity) != "" ||
			strings.TrimSpace(remark.Type) != "" ||
			strings.TrimSpace(remark.Title) != "" ||
			strings.TrimSpace(remark.Body) != "" ||
			strings.TrimSpace(remark.Answer) != "" ||
			strings.TrimSpace(remark.Resolution) != "" {
			return true
		}
	}
	return false
}

func isBlockingReviewRemark(remark execution.StructuredRemark) bool {
	severity := strings.ToLower(strings.TrimSpace(remark.Severity))
	switch severity {
	case "minor", "info", "informational", "warning", "non-blocking", "nonblocking":
		return false
	default:
		return true
	}
}

func isResolvedReviewRemark(remark execution.StructuredRemark) bool {
	status := strings.ToLower(strings.TrimSpace(remark.Status))
	switch status {
	case "resolved", "fixed", "done", "ok", "closed", "outdated":
		return true
	default:
		return false
	}
}

func canonicalProcessingAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "implement-pr", "implementation-pr", "open-pr", "start-implementation", execution.ActionStartImplementationPR:
		return execution.ActionStartImplementationPR
	case "pr-review", "review-pr", execution.ActionReviewPullRequest:
		return execution.ActionReviewPullRequest
	case "address-review-comments", "fix-review-comments", "reply-review-comments", execution.ActionApplyReviewComments:
		return execution.ActionApplyReviewComments
	case "resolve-conflict", execution.ActionResolveMergeConflict:
		return execution.ActionResolveMergeConflict
	default:
		return strings.TrimSpace(action)
	}
}

func requiresMergeRequest(action string) bool {
	switch canonicalProcessingAction(action) {
	case execution.ActionReviewPullRequest, execution.ActionApplyReviewComments, execution.ActionResolveMergeConflict:
		return true
	default:
		return false
	}
}

func actionInvocationFromTaskState(action string, issue *integration.TrackerIssue, mergeRequest *integration.MergeRequest) execution.ActionInvocation {
	assignment := &execution.ExecutionAssignment{
		Action:          action,
		ExpectedResult:  expectedResultForAction(action),
		CanonicalTask:   executionObjectRefFromIssue(issue),
		StructuredInput: structuredInputFromIssue(issue),
	}
	if mergeRequest != nil {
		assignment.RelatedObjects = []execution.ObjectRef{executionObjectRefFromMergeRequest(mergeRequest)}
	}
	return execution.ActionInvocation{Assignment: assignment}
}

func executionObjectRefFromIssue(issue *integration.TrackerIssue) *execution.ObjectRef {
	if issue == nil {
		return nil
	}
	attributes := map[string]string{}
	if body := strings.TrimSpace(issue.Body); body != "" {
		attributes["body"] = body
	}
	if len(attributes) == 0 {
		attributes = nil
	}
	return &execution.ObjectRef{
		Type:       "task",
		System:     issue.System,
		Repository: issue.Repository,
		Number:     numericIssueID(issue.ID),
		Title:      issue.Title,
		URL:        issue.URL,
		Attributes: attributes,
	}
}

func executionObjectRefFromMergeRequest(mergeRequest *integration.MergeRequest) execution.ObjectRef {
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
	return execution.ObjectRef{
		Type:       "merge-request",
		System:     mergeRequest.System,
		Repository: mergeRequest.Repository,
		Number:     mergeRequest.Number,
		Title:      mergeRequest.Title,
		URL:        mergeRequest.URL,
		Attributes: attributes,
	}
}

func structuredInputFromIssue(issue *integration.TrackerIssue) *execution.StructuredInput {
	if issue == nil {
		return nil
	}
	return &execution.StructuredInput{Task: taskTextFromIssue(issue)}
}

func taskTextFromIssue(issue *integration.TrackerIssue) string {
	if issue == nil {
		return ""
	}
	title := strings.TrimSpace(issue.Title)
	if title == "" {
		title = "Без названия"
	}
	parts := []string{fmt.Sprintf("Task #%s: %s", issue.ID, title)}
	if body := strings.TrimSpace(issue.Body); body != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n")
}

func numericIssueID(id string) int {
	number, _ := strconv.Atoi(strings.TrimSpace(id))
	return number
}

func expectedResultForAction(action string) string {
	switch canonicalProcessingAction(action) {
	case execution.ActionStartImplementationPR:
		return "Выполнить реализацию задачи, отправить ветку и открыть запрос на слияние."
	case execution.ActionReviewPullRequest:
		return "Проверить открытый запрос на слияние и записать заключение ревизии."
	case execution.ActionApplyReviewComments:
		return "Исправить замечания ревизии, отправить ветку и записать ответы на замечания."
	case execution.ActionResolveMergeConflict:
		return "Разрешить конфликт запроса на слияние, завершить перебазирование и отправить ветку через --force-with-lease."
	default:
		return "Выполнить выбранное действие и вернуть диагностируемый результат."
	}
}

func labelsPresent(existing []string, candidates []string) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if label, ok := findLabel(existing, candidate); ok {
			result = append(result, label)
		}
	}
	return dedupeLabels(result)
}

func labelsMissing(existing []string, candidates []string) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := findLabel(existing, candidate); !ok {
			result = append(result, candidate)
		}
	}
	return dedupeLabels(result)
}

func taskLabelsRequireMergeRequest(labels []string) bool {
	_, awaitingReview := findLabel(labels, LabelAwaitingReview)
	_, needsRework := findLabel(labels, LabelNeedsRework)
	_, reviewPassed := findLabel(labels, LabelReviewPassed)
	return awaitingReview || needsRework || reviewPassed
}

func taskLabelsRequireCompletionMergeRequest(labels []string) bool {
	_, reviewPassed := findLabel(labels, LabelReviewPassed)
	return reviewPassed
}

func removeLabels(existing []string, removed []string) []string {
	if len(existing) == 0 || len(removed) == 0 {
		return append([]string(nil), existing...)
	}
	removedSet := map[string]struct{}{}
	for _, label := range removed {
		removedSet[strings.ToLower(strings.TrimSpace(label))] = struct{}{}
	}
	result := make([]string, 0, len(existing))
	for _, label := range existing {
		if _, ok := removedSet[strings.ToLower(strings.TrimSpace(label))]; ok {
			continue
		}
		result = append(result, label)
	}
	return result
}

func findLabel(existing []string, candidate string) (string, bool) {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "" {
		return "", false
	}
	for _, label := range existing {
		if strings.ToLower(strings.TrimSpace(label)) == candidate {
			return label, true
		}
	}
	return "", false
}

func dedupeLabels(labels []string) []string {
	result := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, label)
	}
	return result
}
