package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/model"
)

const DefaultCycleMaxExecutions = 5

type cycleStarter interface {
	Start(context.Context, Invocation) (LaunchResult, error)
}

func RunExecutionCycle(ctx context.Context, starter cycleStarter, config model.CycleConfig, cycleName string, in Invocation) (LaunchResult, error) {
	cycleName = strings.TrimSpace(cycleName)
	if cycleName == "" {
		return LaunchResult{Status: "failed", Summary: "cycle name is required"}, fmt.Errorf("cycle name is required")
	}

	if starter == nil {
		err := fmt.Errorf("cycle starter is required")
		return failedStartResult(err), err
	}

	definition, ok := config.Cycles[cycleName]
	if !ok {
		err := fmt.Errorf("unknown cycle: %s", cycleName)
		result := failedStartResult(err)
		return result, err
	}

	steps := make(map[string]model.CycleStep, len(definition.Steps))
	for _, step := range definition.Steps {
		steps[strings.TrimSpace(step.Name)] = step
	}

	currentStepName := strings.TrimSpace(definition.StartStep)
	maxExecutions := definition.Limits.MaxExecutions
	if maxExecutions == 0 {
		maxExecutions = DefaultCycleMaxExecutions
	}
	if maxExecutions < 0 {
		err := fmt.Errorf("limits.max_executions must be non-negative")
		result := failedStartResult(err)
		return result, err
	}

	root := executionHistoryRoot(in, Workplace{})
	historyHandle := beginExecutionCycleAggregate(ctx, root, in, cycleName)

	originalInput := cloneStructuredInput(in.Launch.StructuredInput)
	originalTask := ""
	if originalInput != nil {
		originalTask = originalInput.Task
	}

	attempts := make([]string, 0, maxExecutions)
	var lastResult LaunchResult
	var lastStep model.CycleStep
	var taskOnRepeat string
	var err error

	for attempt := 1; attempt <= maxExecutions; attempt++ {
		step, ok := steps[currentStepName]
		if !ok {
			err = fmt.Errorf("execution step %q not found in cycle", currentStepName)
			updateExecutionCycleAggregate(ctx, root, historyHandle, in, LaunchResult{Status: "failed", Summary: cycleAttemptSummary(attempt, currentStepName, lastResult, "")}, err)
			return failedExecutionCycleResult("failed", cycleName, maxExecutions, attempts, lastResult, err), err
		}

		input := buildCycleInvocationInput(originalInput, originalTask, taskOnRepeat, lastResult, lastStep)
		execution := in
		execution.Profile = step.Profile
		execution.Launch.StructuredInput = input

		result, invokeErr := starter.Start(ctx, execution)
		lastResult = result
		lastStep = step
		conclusion := reviewConclusionStatus(result)
		conclusion = strings.TrimSpace(strings.ToLower(conclusion))
		nextStep := strings.TrimSpace(nextCycleStep(step, conclusion))
		attempts = append(attempts, cycleAttemptSummary(attempt, currentStepName, result, conclusion))

		if invokeErr != nil {
			updateExecutionCycleAggregate(ctx, root, historyHandle, in, failedExecutionCycleResult("failed", cycleName, maxExecutions, attempts, result, invokeErr), invokeErr)
			return failedExecutionCycleResult("failed", cycleName, maxExecutions, attempts, result, invokeErr), invokeErr
		}

		if nextStep == "" {
			result = completedExecutionCycleResult(cycleName, maxExecutions, attempts, result)
			updateExecutionCycleAggregate(ctx, root, historyHandle, in, result, nil)
			return result, nil
		}
		if !stepTransitionExists(step, conclusion) {
			err = fmt.Errorf("step %q has no valid transition for conclusion status %q", currentStepName, conclusionOrMissing(conclusion))
			updateExecutionCycleAggregate(ctx, root, historyHandle, in, failedExecutionCycleResult("failed", cycleName, maxExecutions, attempts, result, err), err)
			return failedExecutionCycleResult("failed", cycleName, maxExecutions, attempts, result, err), err
		}

		currentStepName = nextStep
		taskOnRepeat = strings.TrimSpace(step.InputTransform.TaskOnRepeat)
	}

	result := failedExecutionCycleResult("limit-reached", cycleName, maxExecutions, attempts, lastResult, nil)
	updateExecutionCycleAggregate(ctx, root, historyHandle, in, result, nil)
	return result, nil
}

func beginExecutionCycleAggregate(ctx context.Context, root string, in Invocation, cycleName string) history.Handle {
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

func updateExecutionCycleAggregate(ctx context.Context, root string, handle history.Handle, in Invocation, result LaunchResult, runErr error) {
	if root == "" {
		return
	}
	if result.Status == "" {
		result.Status = "failed"
	}

	errorText := ""
	if runErr != nil {
		errorText = strings.TrimSpace(runErr.Error())
	}

	_ = history.Update(ctx, handle, history.Run{
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		Status:              result.Status,
		Summary:             result.Summary,
		Name:                in.Workplace.Name,
		ProfileName:         fallbackExecutionHistoryValue(strings.TrimSpace(in.Profile)),
		Runner:              fallbackExecutionHistoryValue(in.Launch.Runner),
		Model:               fallbackExecutionHistoryValue(in.Launch.Model),
		LaunchDirectory:     fallbackLaunchDirectory(in, root),
		RawStructuredInput:  history.StructuredInputJSON(in.Launch.StructuredInput),
		RawOutputPath:       result.RawOutputPath,
		RawStructuredOutput: history.StructuredOutputJSON(result.StructuredOutput, result.RawStructuredOutput),
		RunRecordPath:       result.RunRecordPath,
		Error:               errorText,
	})
}

func buildCycleInvocationInput(baseInput *StructuredInput, originalTask, taskOnRepeat string, lastResult LaunchResult, lastStep model.CycleStep) *StructuredInput {
	input := cloneStructuredInput(baseInput)
	if input == nil {
		input = &StructuredInput{}
	}

	input.ProjectContext = appendOriginalTaskContext(input.ProjectContext, originalTask)
	if taskOnRepeat != "" {
		input.Task = taskOnRepeat
	}

	if strings.TrimSpace(lastStep.Name) != "" {
		input.PreviousRunResults = append(input.PreviousRunResults, StructuredResult{
			Summary: fmt.Sprintf("шаг %s завершён", lastStep.Name),
			Body:    strings.TrimSpace(lastResult.Summary),
		})
		input.ReviewRemarks = append(input.ReviewRemarks, reviewRemarksForRework(lastResult.StructuredOutput)...)
	}

	return input
}

func nextCycleStep(step model.CycleStep, conclusion string) string {
	for _, transition := range step.Transitions {
		if matchesCycleTransition(transition, conclusion) {
			return transition.Next
		}
	}
	return ""
}

func stepTransitionExists(step model.CycleStep, conclusion string) bool {
	for _, transition := range step.Transitions {
		if matchesCycleTransition(transition, conclusion) {
			return true
		}
	}
	return false
}

func matchesCycleTransition(transition model.CycleTransition, conclusion string) bool {
	conclusion = conclusionOrMissing(conclusion)

	if conclusion == "missing" {
		return transition.Missing
	}

	inSet := make(map[string]struct{}, len(transition.In))
	for _, value := range transition.In {
		inSet[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	notInSet := make(map[string]struct{}, len(transition.NotIn))
	for _, value := range transition.NotIn {
		notInSet[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}

	if len(inSet) > 0 {
		if _, ok := inSet[conclusion]; !ok {
			return false
		}
	}
	if len(notInSet) > 0 {
		if _, ok := notInSet[conclusion]; ok {
			return false
		}
	}

	return len(inSet) > 0 || len(notInSet) > 0
}

func conclusionOrMissing(conclusion string) string {
	conclusion = strings.ToLower(strings.TrimSpace(conclusion))
	if conclusion == "" {
		return "missing"
	}
	return conclusion
}

func cycleAttemptSummary(attempt int, stepName string, result LaunchResult, conclusion string) string {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "unknown"
	}
	if conclusion == "" {
		conclusion = "missing"
	}
	return fmt.Sprintf("attempt=%d step=%s status=%s conclusion=%s", attempt, stepName, status, conclusion)
}

func failedExecutionCycleResult(status, cycleName string, maxExecutions int, attempts []string, final LaunchResult, err error) LaunchResult {
	summary := joinReviewCycleParts(
		fmt.Sprintf("execution-cycle cycle=%s max-executions=%d attempts=%d", cycleName, maxExecutions, len(attempts)),
		strings.Join(attempts, "\n"),
	)
	if err != nil {
		summary = joinReviewCycleParts(summary, "error: "+strings.TrimSpace(err.Error()))
	}

	return LaunchResult{
		Status:           status,
		Summary:          summary,
		RawOutputPath:    final.RawOutputPath,
		StructuredOutput: final.StructuredOutput,
	}
}

func completedExecutionCycleResult(cycleName string, maxExecutions int, attempts []string, final LaunchResult) LaunchResult {
	return LaunchResult{
		Status: "completed",
		Summary: joinReviewCycleParts(
			fmt.Sprintf("execution-cycle cycle=%s max-executions=%d attempts=%d", cycleName, maxExecutions, len(attempts)),
			strings.Join(attempts, "\n"),
		),
		RawOutputPath:    final.RawOutputPath,
		StructuredOutput: final.StructuredOutput,
	}
}
