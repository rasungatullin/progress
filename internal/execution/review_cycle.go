package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/launch"
)

const DefaultReviewCycleMaxExecutions = 5

type reviewCycleStarter interface {
	Start(context.Context, Invocation) (LaunchResult, error)
}

func RunReviewCycle(ctx context.Context, starter reviewCycleStarter, in Invocation, reviewProfile string, maxExecutions int) (LaunchResult, error) {
	if starter == nil {
		err := fmt.Errorf("review cycle starter is required")
		result := failedStartResult(err)
		storeReviewCycleAggregate(ctx, in, result, err)
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
		storeReviewCycleAggregate(ctx, in, result, err)
		return result, err
	}

	if maxExecutions == 0 {
		maxExecutions = DefaultReviewCycleMaxExecutions
	}
	if maxExecutions < 0 {
		err := fmt.Errorf("max executions must be positive")
		result := failedStartResult(err)
		storeReviewCycleAggregate(ctx, in, result, err)
		return result, err
	}

	normalizedPrompt, structuredInput, err := launch.NormalizeStructuredInput(in.Launch.Prompt, in.Launch.StructuredInput)
	if err != nil {
		result := failedStartResult(err)
		storeReviewCycleAggregate(ctx, in, result, err)
		return result, err
	}
	in.Launch.Prompt = normalizedPrompt
	in.Launch.StructuredInput = structuredInput

	originalPrompt := in.Launch.Prompt
	attempts := make([]string, 0, maxExecutions)
	var lastExecution LaunchResult
	var lastReview LaunchResult

	for attempt := 1; attempt <= maxExecutions; attempt++ {
		executionInvocation := in
		executionInvocation.Profile = executionProfile
		if attempt > 1 {
			executionInvocation.Launch.Prompt = buildReviewCycleReworkPrompt(originalPrompt, attempt)
			executionInvocation.Launch.StructuredInput = buildReviewCycleReworkInput(in.Launch.StructuredInput, originalPrompt, attempt, lastExecution, lastReview)
		}

		executionResult, err := starter.Start(ctx, executionInvocation)
		lastExecution = executionResult
		if err != nil {
			attempts = append(attempts, formatReviewCycleAttempt(attempt, executionResult, LaunchResult{}, ""))
			result := reviewCycleResult("failed", executionProfile, reviewProfile, maxExecutions, attempts, executionResult)
			storeReviewCycleAggregate(ctx, in, result, err)
			return result, err
		}

		reviewInvocation := in
		reviewInvocation.Profile = reviewProfile
		reviewInvocation.Launch.Prompt = buildReviewCycleReviewPrompt(originalPrompt, attempt)
		reviewInvocation.Launch.StructuredInput = buildReviewCycleReviewInput(in.Launch.StructuredInput, originalPrompt, attempt, executionResult)

		reviewResult, err := starter.Start(ctx, reviewInvocation)
		lastReview = reviewResult
		conclusion := reviewConclusionStatus(reviewResult)
		attempts = append(attempts, formatReviewCycleAttempt(attempt, executionResult, reviewResult, conclusion))
		if err != nil {
			result := reviewCycleResult("failed", executionProfile, reviewProfile, maxExecutions, attempts, reviewResult)
			storeReviewCycleAggregate(ctx, in, result, err)
			return result, err
		}
		if isApprovingReviewStatus(conclusion) {
			result := reviewCycleResult("completed", executionProfile, reviewProfile, maxExecutions, attempts, reviewResult)
			storeReviewCycleAggregate(ctx, in, result, nil)
			return result, nil
		}
	}

	result := reviewCycleResult("limit-reached", executionProfile, reviewProfile, maxExecutions, attempts, lastReview)
	storeReviewCycleAggregate(ctx, in, result, nil)
	return result, nil
}

func storeReviewCycleAggregate(ctx context.Context, in Invocation, result LaunchResult, runErr error) {
	root := executionHistoryRoot(in, Workplace{})
	if root == "" {
		return
	}

	errorText := ""
	if runErr != nil {
		errorText = strings.TrimSpace(runErr.Error())
	}

	_ = history.Store(ctx, root, history.Run{
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

func buildReviewCycleReviewPrompt(originalPrompt string, attempt int) string {
	return joinReviewCycleParts(
		fmt.Sprintf("Проведи code review результата исполнения попытки %d в текущем рабочем каталоге.", attempt),
		"Проверь соответствие исходной задаче, bugs, behavioral regressions, missing tests, strict contract violations, security/privacy risks и risky side effects.",
		"Не изменяй файлы, не коммить изменения и не создавай PR.",
		"Исходная задача:",
		strings.TrimSpace(originalPrompt),
		"Если blocking issues не найдены, верни conclusion.status=ok или approve. Если нужны доработки, верни actionable remarks.",
	)
}

func buildReviewCycleReworkPrompt(originalPrompt string, attempt int) string {
	return joinReviewCycleParts(
		fmt.Sprintf("Исправь замечания ревью перед попыткой исполнения %d.", attempt),
		"Сохрани уже сделанные корректные изменения и исправляй только замечания, влияющие на корректность задачи.",
		"Исходная задача:",
		strings.TrimSpace(originalPrompt),
		"Используй structured input ниже как источник предыдущего результата и замечаний ревью.",
	)
}

func buildReviewCycleReviewInput(base *StructuredInput, originalPrompt string, attempt int, executionResult LaunchResult) *StructuredInput {
	input := cloneStructuredInput(base)
	if input == nil {
		input = &StructuredInput{}
	}
	input.ProtocolVersion = StructuredIOVersion
	input.ProjectContext = appendStructuredTaskContext(input.ProjectContext, input.Task)
	input.Task = fmt.Sprintf("Провести ревью результата исполнения попытки %d.", attempt)
	input.Constraints = append(input.Constraints,
		"Не изменять файлы во время ревью.",
		"Вернуть conclusion.status=ok или approve, если blocking issues не найдены.",
	)
	input.ProjectContext = appendOriginalPromptContext(input.ProjectContext, originalPrompt)
	input.PreviousRunResults = append(input.PreviousRunResults, StructuredResult{
		Summary: fmt.Sprintf("execution attempt %d", attempt),
		Body:    executionResult.Summary,
	})

	return input
}

func buildReviewCycleReworkInput(base *StructuredInput, originalPrompt string, attempt int, executionResult, reviewResult LaunchResult) *StructuredInput {
	input := cloneStructuredInput(base)
	if input == nil {
		input = &StructuredInput{}
	}
	input.ProtocolVersion = StructuredIOVersion
	input.ProjectContext = appendStructuredTaskContext(input.ProjectContext, input.Task)
	input.Task = fmt.Sprintf("Доработать результат исполнения по замечаниям ревью перед попыткой %d.", attempt)
	input.Constraints = append(input.Constraints,
		"Сохранить корректные изменения предыдущих попыток.",
		"Исправить actionable замечания ревью.",
	)
	input.ProjectContext = appendOriginalPromptContext(input.ProjectContext, originalPrompt)
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

func appendOriginalPromptContext(contexts []StructuredContext, originalPrompt string) []StructuredContext {
	originalPrompt = strings.TrimSpace(originalPrompt)
	if originalPrompt == "" {
		return contexts
	}

	return append(contexts, StructuredContext{Title: "Исходная задача", Body: originalPrompt})
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
