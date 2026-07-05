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
	Index           int
	Issue           *integration.TrackerIssue
	MergeRequest    *integration.MergeRequest
	Consideration   *decision.ConsiderationResult
	Action          string
	ExecutionResult *execution.ExecutionResult
	Execution       *execution.LaunchResult
	LabelChanges    []LabelChange
	ReviewPassed    *bool
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
	for index := 1; index <= maxCycles; index++ {
		cycle, err := s.runDecisionCycle(ctx, input.TaskNumber, index)
		result.Cycles = append(result.Cycles, cycle)
		result.FinalIssue = cycle.Issue
		if err != nil {
			return result, err
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

	issue, mergeRequest, err := s.loadTaskState(ctx, input.TaskNumber)
	if err != nil {
		return TaskProcessingResult{TaskNumber: input.TaskNumber}, err
	}

	cycle := TaskProcessingCycle{
		Index:        1,
		Issue:        issue,
		MergeRequest: mergeRequest,
		Action:       canonicalProcessingAction(action),
	}
	if cycle.Action == "" {
		cycle.Action = action
	}
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

func (s *Service) runDecisionCycle(ctx context.Context, taskNumber int, index int) (TaskProcessingCycle, error) {
	issue, mergeRequest, err := s.loadTaskState(ctx, taskNumber)
	if err != nil {
		return TaskProcessingCycle{Index: index}, err
	}

	cycle := TaskProcessingCycle{
		Index:        index,
		Issue:        issue,
		MergeRequest: mergeRequest,
	}
	consideration, err := s.decision.Consider(ctx, decision.ConsiderationInput{Context: decision.DecisionContext{
		Signal:       decision.Signal{Source: decision.SignalSourceTask, Kind: decision.SignalKindTask, TaskNumber: taskNumber},
		Issue:        issue,
		MergeRequest: mergeRequest,
	}})
	cycle.Consideration = &consideration
	if err != nil {
		return cycle, err
	}
	if consideration.ExecutionPlan == nil {
		return cycle, nil
	}

	cycle.Action = strings.TrimSpace(consideration.ExecutionPlan.Action)
	executionResult, err := s.execution.ExecuteAction(ctx, execution.ActionInvocation{Assignment: consideration.ExecutionPlan.Assignment})
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

func (s *Service) loadTaskState(ctx context.Context, taskNumber int) (*integration.TrackerIssue, *integration.MergeRequest, error) {
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationTypeTracker,
		Resource:        "issue",
		ObjectType:      "issue",
		Operation:       "get",
		Number:          taskNumber,
	})
	if err != nil {
		return nil, nil, err
	}
	if response.Issue == nil {
		return nil, nil, fmt.Errorf("контур интеграции не вернул задачу %d", taskNumber)
	}

	mergeRequest, err := s.findTaskMergeRequest(ctx, response.Issue)
	if err != nil {
		if taskLabelsRequireMergeRequest(response.Issue.Labels) {
			return nil, nil, fmt.Errorf("восстановить связанный запрос на слияние для задачи %d: %w", taskNumber, err)
		}
		s.logger.Printf("Связанный запрос на слияние не восстановлен: задача=%d ошибка=%v", taskNumber, err)
	}
	return response.Issue, mergeRequest, nil
}

func (s *Service) findTaskMergeRequest(ctx context.Context, issue *integration.TrackerIssue) (*integration.MergeRequest, error) {
	if issue == nil || issue.Number <= 0 || strings.TrimSpace(issue.Repository) == "" {
		return nil, nil
	}

	head := strconv.Itoa(issue.Number)
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

	remove = labelsPresent(issue.Labels, remove)
	if len(remove) != 0 {
		if err := s.changeTaskLabels(ctx, issue, "remove", remove); err != nil {
			return changes, reviewPassed, err
		}
		issue.Labels = removeLabels(issue.Labels, remove)
		changes = append(changes, LabelChange{Operation: "remove", Labels: remove})
	}

	add = labelsMissing(issue.Labels, add)
	if len(add) != 0 {
		if err := s.changeTaskLabels(ctx, issue, "add", add); err != nil {
			return changes, reviewPassed, err
		}
		issue.Labels = append(issue.Labels, add...)
		changes = append(changes, LabelChange{Operation: "add", Labels: add})
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
		Number:          issue.Number,
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
		if isResolvedReviewRemark(remark) {
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

func isResolvedReviewRemark(remark execution.StructuredRemark) bool {
	status := strings.ToLower(strings.TrimSpace(remark.Status))
	switch status {
	case "resolved", "fixed", "done", "ok":
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
	default:
		return strings.TrimSpace(action)
	}
}

func requiresMergeRequest(action string) bool {
	switch canonicalProcessingAction(action) {
	case execution.ActionReviewPullRequest, execution.ActionApplyReviewComments:
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
		Number:     issue.Number,
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
	parts := []string{fmt.Sprintf("Task #%d: %s", issue.Number, title)}
	if body := strings.TrimSpace(issue.Body); body != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n\n")
}

func expectedResultForAction(action string) string {
	switch canonicalProcessingAction(action) {
	case execution.ActionStartImplementationPR:
		return "Выполнить реализацию задачи, отправить ветку и открыть запрос на слияние."
	case execution.ActionReviewPullRequest:
		return "Проверить открытый запрос на слияние и записать заключение ревизии."
	case execution.ActionApplyReviewComments:
		return "Исправить замечания ревизии, отправить ветку и записать ответы на замечания."
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
	return awaitingReview || needsRework
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
