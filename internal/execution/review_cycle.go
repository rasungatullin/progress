package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/history"
)

const DefaultReviewCycleMaxExecutions = 5

type reviewCycleStarter interface {
	Start(context.Context, Invocation) (LaunchResult, error)
}

func RunReviewCycle(ctx context.Context, starter reviewCycleStarter, in Invocation, reviewProfile string, maxExecutions int) (LaunchResult, error) {
	historyRoot := executionHistoryRoot(in, Workplace{})
	historyHandle := beginReviewCycleAggregate(ctx, historyRoot, in)

	if starter == nil {
		err := fmt.Errorf("review cycle starter is required")
		result := failedStartResult(err)
		updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, err)
		return result, err
	}

	executionProfile := strings.TrimSpace(in.Profile)
	if executionProfile == "" {
		executionProfile = "default"
	}

	reviewProfile = strings.TrimSpace(reviewProfile)
	if reviewProfile == "" {
		err := fmt.Errorf("review profile is required")
		result := failedStartResult(err)
		updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, err)
		return result, err
	}

	if maxExecutions == 0 {
		maxExecutions = DefaultReviewCycleMaxExecutions
	}
	if maxExecutions < 0 {
		err := fmt.Errorf("max executions must be positive")
		result := failedStartResult(err)
		updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, err)
		return result, err
	}

	originalTask := ""
	if in.Launch.StructuredInput != nil {
		originalTask = in.Launch.StructuredInput.Task
	}
	attempts := make([]string, 0, maxExecutions)
	var lastExecution LaunchResult
	var lastReview LaunchResult

	for attempt := 1; attempt <= maxExecutions; attempt++ {
		executionInvocation := in
		executionInvocation.Profile = executionProfile
		if attempt > 1 {
			executionInvocation.Launch.StructuredInput = buildReviewCycleReworkInput(in.Launch.StructuredInput, originalTask, attempt, lastExecution, lastReview)
		}

		executionResult, err := starter.Start(ctx, executionInvocation)
		lastExecution = executionResult
		if err != nil {
			attempts = append(attempts, formatReviewCycleAttempt(attempt, executionResult, LaunchResult{}, ""))
			result := reviewCycleResult("failed", executionProfile, reviewProfile, maxExecutions, attempts, executionResult)
			updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, err)
			return result, err
		}

		reviewInvocation := in
		reviewInvocation.Profile = reviewProfile
		reviewInvocation.Launch.StructuredInput = buildReviewCycleReviewInput(in.Launch.StructuredInput, originalTask, attempt, executionResult)

		reviewResult, err := starter.Start(ctx, reviewInvocation)
		lastReview = reviewResult
		conclusion := reviewConclusionStatus(reviewResult)
		attempts = append(attempts, formatReviewCycleAttempt(attempt, executionResult, reviewResult, conclusion))
		if err != nil {
			result := reviewCycleResult("failed", executionProfile, reviewProfile, maxExecutions, attempts, reviewResult)
			updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, err)
			return result, err
		}
		if isApprovingReviewStatus(conclusion) {
			result := reviewCycleResult("completed", executionProfile, reviewProfile, maxExecutions, attempts, reviewResult)
			updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, nil)
			return result, nil
		}
	}

	result := reviewCycleResult("limit-reached", executionProfile, reviewProfile, maxExecutions, attempts, lastReview)
	updateReviewCycleAggregate(ctx, historyRoot, historyHandle, in, result, nil)
	return result, nil
}

func beginReviewCycleAggregate(ctx context.Context, root string, in Invocation) history.Handle {
	if root == "" {
		return history.Handle{}
	}
	handle, err := history.Begin(ctx, root, history.Run{
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		Status:             "running",
		Summary:            "",
		Name:               in.Workplace.Name,
		ProfileName:        fallbackExecutionHistoryValue(strings.TrimSpace(in.Profile)),
		Runner:             fallbackExecutionHistoryValue(in.Launch.Runner),
		Model:              fallbackExecutionHistoryValue(in.Launch.Model),
		LaunchDirectory:    fallbackLaunchDirectory(in, root),
		RawStructuredInput: history.StructuredInputJSON(in.Launch.StructuredInput),
	})
	if err != nil {
		return history.Handle{}
	}
	return handle
}

func updateReviewCycleAggregate(ctx context.Context, root string, handle history.Handle, in Invocation, result LaunchResult, runErr error) {
	if root == "" {
		return
	}

	errorText := ""
	if runErr != nil {
		errorText = strings.TrimSpace(runErr.Error())
	}

	_ = history.Update(ctx, handle, history.Run{
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		Status:              fallbackExecutionHistoryValue(result.Status),
		Summary:             result.Summary,
		Name:                in.Workplace.Name,
		ProfileName:         fallbackExecutionHistoryValue(strings.TrimSpace(in.Profile)),
		Runner:              fallbackExecutionHistoryValue(in.Launch.Runner),
		Model:               fallbackExecutionHistoryValue(in.Launch.Model),
		LaunchDirectory:     fallbackLaunchDirectory(in, root),
		RawStructuredInput:  history.StructuredInputJSON(in.Launch.StructuredInput),
		RawOutputPath:       result.RawOutputPath,
		RawStructuredOutput: history.StructuredOutputJSON(result.StructuredOutput, ""),
		RunRecordPath:       result.RunRecordPath,
		Error:               errorText,
	})
}

func buildReviewCycleReviewInput(base *StructuredInput, originalTask string, attempt int, executionResult LaunchResult) *StructuredInput {
	input := cloneStructuredInput(base)
	if input == nil {
		input = &StructuredInput{}
	}
	input.ProjectContext = appendStructuredTaskContext(input.ProjectContext, input.Task)
	input.Task = fmt.Sprintf("Провести ревью результата исполнения попытки %d.", attempt)
	input.Constraints = append(input.Constraints,
		"Не изменять файлы во время ревью.",
		"Вернуть conclusion.status=ok или approve, если blocking issues не найдены.",
	)
	input.ProjectContext = appendOriginalTaskContext(input.ProjectContext, originalTask)
	input.PreviousRunResults = append(input.PreviousRunResults, StructuredResult{
		Summary: fmt.Sprintf("execution attempt %d", attempt),
		Body:    executionResult.Summary,
	})

	return input
}

func buildReviewCycleReworkInput(base *StructuredInput, originalTask string, attempt int, executionResult, reviewResult LaunchResult) *StructuredInput {
	input := cloneStructuredInput(base)
	if input == nil {
		input = &StructuredInput{}
	}
	input.ProjectContext = appendStructuredTaskContext(input.ProjectContext, input.Task)
	input.Task = fmt.Sprintf("Доработать результат исполнения по замечаниям ревью перед попыткой %d.", attempt)
	input.Constraints = append(input.Constraints,
		"Сохранить корректные изменения предыдущих попыток.",
		"Исправить actionable замечания ревью.",
	)
	input.ProjectContext = appendOriginalTaskContext(input.ProjectContext, originalTask)
	input.PreviousRunResults = append(input.PreviousRunResults,
		StructuredResult{Summary: fmt.Sprintf("previous execution before attempt %d", attempt), Body: executionResult.Summary},
		StructuredResult{Summary: fmt.Sprintf("review before attempt %d", attempt), Body: reviewResult.Summary},
	)
	input.ReviewRemarks = append(input.ReviewRemarks, reviewRemarksForRework(reviewResult.StructuredOutput)...)

	return input
}

func reviewRemarksForRework(output *StructuredOutput) []StructuredRemark {
	if output == nil {
		return nil
	}

	remarks := make([]StructuredRemark, 0, len(output.Remarks))
	for _, remark := range output.Remarks {
		if isResolvedReviewStatus(remark.Status) {
			continue
		}
		remarks = append(remarks, remark)
	}
	if len(remarks) != 0 || output.Conclusion == nil || isApprovingReviewStatus(output.Conclusion.Status) {
		return remarks
	}

	return append(remarks, StructuredRemark{
		ID:       "review-conclusion",
		Status:   output.Conclusion.Status,
		Type:     "review-conclusion",
		Title:    firstNonEmptyReviewCycle(output.Conclusion.Summary, "Review did not approve the result"),
		Body:     output.Conclusion.Body,
		Severity: "blocking",
	})
}

func reviewConclusionStatus(result LaunchResult) string {
	if result.StructuredOutput == nil || result.StructuredOutput.Conclusion == nil {
		return ""
	}

	return strings.ToLower(strings.TrimSpace(result.StructuredOutput.Conclusion.Status))
}

func isApprovingReviewStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "approve", "approved":
		return true
	default:
		return false
	}
}

func isResolvedReviewStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "approve", "approved", "resolved", "fixed", "done":
		return true
	default:
		return false
	}
}

func reviewCycleResult(status, executionProfile, reviewProfile string, maxExecutions int, attempts []string, final LaunchResult) LaunchResult {
	summary := joinReviewCycleParts(
		fmt.Sprintf(
			"review-cycle execution-profile=%s review-profile=%s max-executions=%d attempts=%d",
			executionProfile,
			reviewProfile,
			maxExecutions,
			len(attempts),
		),
		strings.Join(attempts, "\n"),
	)

	return LaunchResult{
		Status:           status,
		Summary:          summary,
		RawOutputPath:    final.RawOutputPath,
		StructuredOutput: final.StructuredOutput,
	}
}

func formatReviewCycleAttempt(attempt int, executionResult, reviewResult LaunchResult, conclusion string) string {
	executionStatus := strings.TrimSpace(executionResult.Status)
	if executionStatus == "" {
		executionStatus = "unknown"
	}
	reviewStatus := strings.TrimSpace(reviewResult.Status)
	if reviewStatus == "" {
		reviewStatus = "skipped"
	}
	if strings.TrimSpace(conclusion) == "" {
		conclusion = "missing"
	}

	return fmt.Sprintf("attempt=%d execution=%s review=%s conclusion=%s", attempt, executionStatus, reviewStatus, conclusion)
}

func cloneStructuredInput(input *StructuredInput) *StructuredInput {
	if input == nil {
		return nil
	}

	cloned := *input
	cloned.Constraints = append([]string(nil), input.Constraints...)
	cloned.ProjectContext = append([]StructuredContext(nil), input.ProjectContext...)
	cloned.OperationalContext = append([]StructuredContext(nil), input.OperationalContext...)
	cloned.PreviousRunResults = append([]StructuredResult(nil), input.PreviousRunResults...)
	cloned.ReviewRemarks = append([]StructuredRemark(nil), input.ReviewRemarks...)
	cloned.ReviewResponses = append([]StructuredResponse(nil), input.ReviewResponses...)
	cloned.IntegrationActions = append([]StructuredAction(nil), input.IntegrationActions...)
	if input.Extensions != nil {
		cloned.Extensions = make(StructuredExtensions, len(input.Extensions))
		for key, value := range input.Extensions {
			cloned.Extensions[key] = append([]byte(nil), value...)
		}
	}

	return &cloned
}

func appendOriginalTaskContext(contexts []StructuredContext, originalTask string) []StructuredContext {
	originalTask = strings.TrimSpace(originalTask)
	if originalTask == "" {
		return contexts
	}

	return append(contexts, StructuredContext{Title: "Исходная задача", Body: originalTask})
}

func appendStructuredTaskContext(contexts []StructuredContext, task string) []StructuredContext {
	task = strings.TrimSpace(task)
	if task == "" {
		return contexts
	}

	return append(contexts, StructuredContext{Title: "Исходная структурированная задача", Body: task})
}

func joinReviewCycleParts(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}

	return strings.Join(filtered, "\n")
}

func firstNonEmptyReviewCycle(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}

	return ""
}
