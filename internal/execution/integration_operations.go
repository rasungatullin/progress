package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/integration"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

type pullRequestRef struct {
	Repository string
	Number     int
	Base       string
	Head       string
	Title      string
	Body       string
	Draft      bool
}

func (e builtinOperationExecutor) loadPullRequest(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := loadPullRequestInputFromOperation(state, operation)
	ref := pullRequestRefFromAssignment(assignmentFromInvocation(input.invocation))
	if ref.Number <= 0 {
		return e.failLoadPullRequestOperation(ctx, state, operation, input.invocation, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failLoadPullRequestOperation(ctx, state, operation, input.invocation, name, "Контур интеграции недоступен.", err, "integration_unavailable")
	}

	response, err := executor.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeRepository,
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "get",
		Repository:      ref.Repository,
		RepoProvided:    strings.TrimSpace(ref.Repository) != "",
		Number:          ref.Number,
	})
	if err != nil {
		return e.failLoadPullRequestOperation(ctx, state, operation, input.invocation, name, "Запрос на слияние не получен.", err, "pull_request_load_failed")
	}

	mergeRequest, ok := mergeRequestFromIntegrationResponse(response)
	if !ok {
		return e.failLoadPullRequestOperation(ctx, state, operation, input.invocation, name, "Контур интеграции не вернул запрос на слияние.", fmt.Errorf("integration response does not include merge request"), "pull_request_missing")
	}

	invocation := invocationWithPullRequest(input.invocation, mergeRequest)
	writeLoadPullRequestData(state, operation, mergeRequest, invocation)
	state.tracker.completeIO(name, loadPullRequestInputSummary(input, operation), loadPullRequestOutputSummary(mergeRequest, invocation, operation), "Запрос на слияние получен через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) failLoadPullRequestOperation(ctx context.Context, state *operationExecution, operation OperationSpec, in invocation, name string, summary string, err error, code string) error {
	result := failedStartResult(err)
	writeLoadPullRequestFailureData(state, operation, result)
	state.tracker.fail(name, summary, err, code, true, true)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, err)
	return err
}

func writeLoadPullRequestData(state *operationExecution, operation OperationSpec, mergeRequest integration.MergeRequest, invocation invocation) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"pull_request": {Ref: "data.pull_request"},
			"invocation":   {Ref: "data.invocation"},
		}
	}
	writeOperationData(state, out, "pull_request", mergeRequest)
	writeOperationData(state, out, "invocation", invocation)
}

func writeLoadPullRequestFailureData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"result": {Ref: "data.result"}}
	}
	writeOperationData(state, out, "result", result)
}

type loadPullRequestInput struct {
	invocation invocation
}

func loadPullRequestInputFromOperation(state *operationExecution, operation OperationSpec) loadPullRequestInput {
	input := loadPullRequestInput{}
	if state != nil {
		input.invocation = invocationFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	return input
}

func loadPullRequestInputSummary(input loadPullRequestInput, operation OperationSpec) string {
	ref := pullRequestRefFromAssignment(input.invocation.Assignment)
	return operationIOSummary(operation.In, map[string]string{
		"invocation": fmt.Sprintf("repository=%s number=%d", ref.Repository, ref.Number),
	})
}

func loadPullRequestOutputSummary(pr integration.MergeRequest, in invocation, operation OperationSpec) string {
	return operationIOSummary(operation.Out, map[string]string{
		"pull_request": mergeRequestSummary(pr),
		"invocation":   invocationSummary(in),
	})
}

func (e builtinOperationExecutor) loadReviewRemarks(ctx context.Context, state *operationExecution, operation OperationSpec, name string, required bool) error {
	input := loadReviewRemarksInputFromOperation(state, operation)
	ref := pullRequestRefFromLoadReviewRemarksInput(input)
	if ref.Number <= 0 {
		return e.failOrSkipLoadReviewRemarksOperation(ctx, state, operation, input.invocation, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required", required)
	}

	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failOrSkipLoadReviewRemarksOperation(ctx, state, operation, input.invocation, name, "Контур интеграции недоступен.", err, "integration_unavailable", required)
	}

	response, err := executor.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeRepository,
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "comments",
		Repository:      ref.Repository,
		RepoProvided:    strings.TrimSpace(ref.Repository) != "",
		Number:          ref.Number,
	})
	if err != nil {
		return e.failOrSkipLoadReviewRemarksOperation(ctx, state, operation, input.invocation, name, "Замечания ревизии не получены.", err, "review_remarks_load_failed", required)
	}

	reviewRemarks := append([]integration.ReviewRemark(nil), response.ReviewRemarks...)
	invocation := invocationWithReviewRemarks(input.invocation, ref, reviewRemarks)
	writeLoadReviewRemarksData(state, operation, reviewRemarks, invocation)
	state.tracker.completeIO(name, loadReviewRemarksInputSummary(input, ref, operation), loadReviewRemarksOutputSummary(reviewRemarks, invocation, operation), "Замечания ревизии получены через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) failOrSkipLoadReviewRemarksOperation(ctx context.Context, state *operationExecution, operation OperationSpec, in invocation, name string, summary string, err error, code string, required bool) error {
	if required {
		result := failedStartResult(err)
		writeLoadReviewRemarksFailureData(state, operation, result)
		state.tracker.fail(name, summary, err, code, true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, err)
		return err
	}

	state.tracker.skip(name, joinExecutionSummaries(summary, strings.TrimSpace(err.Error())))
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), resultFromExecutionData(state), nil)
	return nil
}

func writeLoadReviewRemarksData(state *operationExecution, operation OperationSpec, remarks []integration.ReviewRemark, invocation invocation) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"review_remarks": {Ref: "data.review_remarks"},
			"invocation":     {Ref: "data.invocation"},
		}
	}
	writeOperationData(state, out, "review_remarks", remarks)
	writeOperationData(state, out, "invocation", invocation)
}

func writeLoadReviewRemarksFailureData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"result": {Ref: "data.result"}}
	}
	writeOperationData(state, out, "result", result)
}

func invocationWithReviewRemarks(in invocation, ref pullRequestRef, remarks []integration.ReviewRemark) invocation {
	result := in
	assignment := assignmentFromInvocation(result)
	if assignment == nil {
		assignment = &ExecutionAssignment{}
	}
	structuredInput := assignment.StructuredInput
	if structuredInput == nil {
		structuredInput = result.Launch.StructuredInput
	}
	if structuredInput == nil {
		structuredInput = &StructuredInput{}
	}
	structuredInput.ReviewRemarks = mergeStructuredRemarks(structuredInput.ReviewRemarks, structuredRemarksFromIntegration(remarks))
	structuredInput.OperationalContext = append(structuredInput.OperationalContext, StructuredContext{
		Title: "Сведения о замечаниях ревизии",
		Body:  fmt.Sprintf("repository=%s\npull_request=%d\nremarks=%d", ref.Repository, ref.Number, len(remarks)),
	})
	assignment.StructuredInput = structuredInput
	result.Assignment = assignment
	result.Launch.StructuredInput = structuredInput
	return result
}

type loadReviewRemarksInput struct {
	invocation  invocation
	pullRequest *integration.MergeRequest
}

func loadReviewRemarksInputFromOperation(state *operationExecution, operation OperationSpec) loadReviewRemarksInput {
	input := loadReviewRemarksInput{}
	if state != nil {
		input.invocation = invocationFromExecutionData(state)
		if value, ok := state.data["pull_request"].(integration.MergeRequest); ok {
			input.pullRequest = &value
		} else if state.pullRequest != nil {
			pullRequest := *state.pullRequest
			input.pullRequest = &pullRequest
		}
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["pull_request"]; ok {
		if value, ok := mergeRequestValueFromLoadReviewRemarksMapping(state, mapping); ok {
			input.pullRequest = &value
		}
	}
	return input
}

func pullRequestRefFromLoadReviewRemarksInput(input loadReviewRemarksInput) pullRequestRef {
	ref := pullRequestRefFromAssignment(assignmentFromInvocation(input.invocation))
	if input.pullRequest == nil {
		return ref
	}
	pullRequest := input.pullRequest
	if strings.TrimSpace(ref.Repository) == "" {
		ref.Repository = pullRequest.Repository
	}
	if ref.Number <= 0 {
		ref.Number = pullRequest.Number
	}
	if strings.TrimSpace(ref.Base) == "" {
		ref.Base = pullRequest.BaseRef
	}
	if strings.TrimSpace(ref.Head) == "" {
		ref.Head = pullRequest.HeadRef
	}
	if strings.TrimSpace(ref.Title) == "" {
		ref.Title = pullRequest.Title
	}
	return ref
}

func mergeRequestValueFromLoadReviewRemarksMapping(state *operationExecution, mapping model.OperationMapping) (integration.MergeRequest, bool) {
	if len(mapping.Value) != 0 {
		var value integration.MergeRequest
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return integration.MergeRequest{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.pull_request":
		if state == nil {
			return integration.MergeRequest{}, false
		}
		value, ok := state.data["pull_request"].(integration.MergeRequest)
		return value, ok
	case "state.pull_request":
		if state == nil || state.pullRequest == nil {
			return integration.MergeRequest{}, false
		}
		return *state.pullRequest, true
	default:
		return integration.MergeRequest{}, false
	}
}

func loadReviewRemarksInputSummary(input loadReviewRemarksInput, ref pullRequestRef, operation OperationSpec) string {
	pullRequest := ""
	if input.pullRequest != nil {
		pullRequest = mergeRequestSummary(*input.pullRequest)
	}
	return operationIOSummary(operation.In, map[string]string{
		"invocation":   fmt.Sprintf("repository=%s number=%d", ref.Repository, ref.Number),
		"pull_request": pullRequest,
	})
}

func loadReviewRemarksOutputSummary(remarks []integration.ReviewRemark, in invocation, operation OperationSpec) string {
	return operationIOSummary(operation.Out, map[string]string{
		"review_remarks": formatInt(len(remarks)),
		"invocation":     invocationSummary(in),
	})
}

func (e builtinOperationExecutor) publishMergeRequest(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := publishMergeRequestInputFromOperation(state, operation)
	ref := pullRequestRefFromPublishMergeRequestInput(state, input)
	if ref.Number > 0 {
		mergeRequest := mergeRequestFromRef(ref)
		summary := fmt.Sprintf("pull-request=%d", ref.Number)
		writePublishMergeRequestData(state, operation, mergeRequest, summary, input.result)
		state.tracker.skipIO(name, publishMergeRequestInputSummary(input, ref, operation), publishMergeRequestOutputSummary(mergeRequest, summary, input.result, operation), fmt.Sprintf("Запрос на слияние уже задан: number=%d.", ref.Number))
		return nil
	}
	if strings.TrimSpace(ref.Repository) == "" {
		return e.failIntegrationOperation(ctx, state, name, "Репозиторий запроса на слияние не задан.", fmt.Errorf("repository is required for pull request creation"), "pull_request_repository_required")
	}
	if strings.TrimSpace(ref.Head) == "" {
		return e.failIntegrationOperation(ctx, state, name, "Ветка запроса на слияние не задана.", fmt.Errorf("head branch is required for pull request creation"), "pull_request_head_required")
	}
	if strings.TrimSpace(ref.Base) == "" {
		base, err := e.defaultMergeRequestBase(ctx, state)
		if err != nil {
			return e.failIntegrationOperation(ctx, state, name, "Базовая ветка запроса на слияние не определена.", err, "pull_request_base_required")
		}
		ref.Base = base
	}
	if strings.TrimSpace(ref.Title) == "" {
		ref.Title = pullRequestTitleFromPublishMergeRequestInput(state, input, ref)
	}
	if strings.TrimSpace(ref.Body) == "" {
		ref.Body = pullRequestBodyFromPublishMergeRequestInput(state, input)
	}

	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Контур интеграции недоступен.", err, "integration_unavailable")
	}

	response, err := executor.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeRepository,
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "create",
		Repository:      ref.Repository,
		RepoProvided:    true,
		Base:            ref.Base,
		Head:            ref.Head,
		Title:           ref.Title,
		Body:            ref.Body,
		Draft:           ref.Draft,
	})
	if err != nil && !pullRequestAlreadyAvailable(response) {
		return e.failIntegrationOperation(ctx, state, name, "Запрос на слияние не открыт.", err, "pull_request_publish_failed")
	}

	summary := pullRequestPublishSummary(response)
	mergeRequest := mergeRequestFromPublishResponse(response, ref)
	result := input.result
	result.Summary = joinExecutionSummaries(result.Summary, summary)
	writePublishMergeRequestData(state, operation, mergeRequest, summary, result)
	state.tracker.completeIO(name, publishMergeRequestInputSummary(input, ref, operation), publishMergeRequestOutputSummary(mergeRequest, summary, result, operation), "Запрос на слияние зафиксирован через контур интеграции.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, profileFromExecutionData(state), allocationFromExecutionData(state), input.workplace, result, nil)
	return nil
}

func writePublishMergeRequestData(state *operationExecution, operation OperationSpec, mergeRequest integration.MergeRequest, summary string, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"merge_request":   {Ref: "data.merge_request"},
			"publish_summary": {Ref: "data.publish_summary"},
			"result":          {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "merge_request", mergeRequest)
	writeOperationData(state, out, "publish_summary", summary)
	writeOperationData(state, out, "result", result)
}

type publishMergeRequestInput struct {
	invocation       invocation
	workplace        workplace
	result           LaunchResult
	structuredOutput *StructuredOutput
}

func publishMergeRequestInputFromOperation(state *operationExecution, operation OperationSpec) publishMergeRequestInput {
	input := publishMergeRequestInput{}
	if state != nil {
		input.invocation = invocationFromExecutionData(state)
		input.workplace = workplaceFromExecutionData(state)
		input.result = resultFromExecutionData(state)
		input.structuredOutput = structuredOutputFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["workplace"]; ok {
		if value, ok := workplaceValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.workplace = value
		}
	}
	if mapping, ok := operation.In["result"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.result = value
			input.structuredOutput = value.StructuredOutput
		}
	}
	if mapping, ok := operation.In["structured_output"]; ok {
		if value, ok := structuredOutputValueFromCommitPushMapping(state, mapping); ok {
			input.structuredOutput = value
		}
	}
	return input
}

func applyPublishMergeRequestInputToState(state *operationExecution, input publishMergeRequestInput) {
	if state == nil {
		return
	}
	state.in = input.invocation
	state.assignment = assignmentFromInvocation(input.invocation)
	state.result = input.result
	state.result.StructuredOutput = input.structuredOutput
}

func publishMergeRequestInputSummary(input publishMergeRequestInput, ref pullRequestRef, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"invocation":        invocationSummary(input.invocation),
		"result":            resultSummary(input.result),
		"structured_output": structuredOutputSummary(input.structuredOutput),
		"workplace":         workplaceSummary(input.workplace),
		"pull_request_ref":  fmt.Sprintf("repository=%s base=%s head=%s", ref.Repository, ref.Base, ref.Head),
	})
}

func publishMergeRequestOutputSummary(mergeRequest integration.MergeRequest, summary string, result LaunchResult, operation OperationSpec) string {
	mergeRequestJSON, err := json.Marshal(mergeRequest)
	if err != nil {
		mergeRequestJSON = []byte(fmt.Sprintf(`{"repository":%q,"number":%d}`, mergeRequest.Repository, mergeRequest.Number))
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"status":%q}`, result.Status))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"merge_request":   string(mergeRequestJSON),
		"publish_summary": summary,
		"result":          string(resultJSON),
	})
}

func (e builtinOperationExecutor) defaultMergeRequestBase(ctx context.Context, state *operationExecution) (string, error) {
	repoRoot := ""
	if state != nil {
		workplace := workplaceFromExecutionData(state)
		repoRoot = strings.TrimSpace(workplace.RepositoryRoot)
		if repoRoot == "" {
			repoRoot = strings.TrimSpace(workplace.Name)
		}
	}
	if repoRoot == "" {
		return "", fmt.Errorf("base branch is required because prepared repository is not available")
	}
	gitOutput := runGitOutput
	if e.service != nil && e.service.runGitOutput != nil {
		gitOutput = e.service.runGitOutput
	}
	output, err := gitOutput(ctx, repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve default branch for pull request base: %w", err)
	}
	base := strings.TrimPrefix(strings.TrimSpace(output), "refs/remotes/origin/")
	if strings.TrimSpace(base) == "" || base == strings.TrimSpace(output) {
		return "", fmt.Errorf("resolve default branch for pull request base: unexpected origin HEAD %q", strings.TrimSpace(output))
	}
	return base, nil
}

func (e builtinOperationExecutor) publishReviewRemarks(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := publishMergeRequestInputFromOperation(state, operation)
	ref := pullRequestRefFromPublishMergeRequestInput(state, input)
	if ref.Number <= 0 {
		return e.failIntegrationOperation(ctx, state, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	comments := reviewRemarkComments(input.structuredOutput)
	if len(comments) == 0 {
		writePublishReviewRemarksData(state, operation, "", input.result)
		state.tracker.skipIO(name, publishReviewRemarksInputSummary(input, operation), publishReviewRemarksOutputSummary("", input.result, operation), "Структурированный вывод не содержит замечаний или заключения для записи.")
		return nil
	}

	count, err := e.publishPullRequestComments(ctx, state, ref, comments)
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Замечания ревизии не записаны.", err, "review_remarks_publish_failed")
	}

	summary := fmt.Sprintf("review-remarks-published=%d", count)
	result := input.result
	result.Summary = joinExecutionSummaries(result.Summary, summary)
	writePublishReviewRemarksData(state, operation, summary, result)
	state.tracker.completeIO(name, publishReviewRemarksInputSummary(input, operation), publishReviewRemarksOutputSummary(summary, result, operation), "Замечания ревизии записаны через контур интеграции.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, nil)
	return nil
}

func writePublishReviewRemarksData(state *operationExecution, operation OperationSpec, summary string, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"review_remarks_summary": {Ref: "data.review_remarks_summary"},
			"result":                 {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "review_remarks_summary", summary)
	writeOperationData(state, out, "result", result)
}

func publishReviewRemarksInputSummary(input publishMergeRequestInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"invocation":        invocationSummary(input.invocation),
		"result":            resultSummary(input.result),
		"structured_output": structuredOutputSummary(input.structuredOutput),
	})
}

func publishReviewRemarksOutputSummary(summary string, result LaunchResult, operation OperationSpec) string {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"status":%q}`, result.Status))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"review_remarks_summary": summary,
		"result":                 string(resultJSON),
	})
}

func (e builtinOperationExecutor) publishReviewResponses(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := publishReviewResponsesInputFromOperation(state, operation)
	ref := pullRequestRefFromPublishReviewResponsesInput(state, input)
	if ref.Number <= 0 {
		return e.failIntegrationOperation(ctx, state, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	responses := reviewResponsesFromOutput(input.structuredOutput)
	if len(responses) == 0 {
		writePublishReviewResponsesData(state, operation, "", input.result)
		state.tracker.skipIO(name, publishReviewResponsesInputSummary(input, operation), publishReviewResponsesOutputSummary("", input.result, operation), "Структурированный вывод не содержит ответов на замечания.")
		return nil
	}

	count, err := e.publishReviewResponseComments(ctx, ref, responses, input.reviewRemarks)
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Ответы на замечания не записаны.", err, "review_responses_publish_failed")
	}

	resolved, err := e.resolveReviewThreads(ctx, responses, input.reviewRemarks)
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Ответы записаны, но часть цепочек обсуждения не закрыта.", err, "review_thread_resolve_failed")
	}

	summary := fmt.Sprintf("review-responses-published=%d review-threads-resolved=%d", count, resolved)
	result := input.result
	result.Summary = joinExecutionSummaries(result.Summary, summary)
	writePublishReviewResponsesData(state, operation, summary, result)
	state.tracker.completeIO(name, publishReviewResponsesInputSummary(input, operation), publishReviewResponsesOutputSummary(summary, result, operation), "Ответы на замечания записаны через контур интеграции.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, nil)
	return nil
}

func writePublishReviewResponsesData(state *operationExecution, operation OperationSpec, summary string, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"review_responses_summary": {Ref: "data.review_responses_summary"},
			"result":                   {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "review_responses_summary", summary)
	writeOperationData(state, out, "result", result)
}

type publishReviewResponsesInput struct {
	invocation       invocation
	result           LaunchResult
	structuredOutput *StructuredOutput
	reviewRemarks    []integration.ReviewRemark
}

func publishReviewResponsesInputFromOperation(state *operationExecution, operation OperationSpec) publishReviewResponsesInput {
	input := publishReviewResponsesInput{}
	if state != nil {
		input.invocation = invocationFromExecutionData(state)
		input.result = resultFromExecutionData(state)
		input.structuredOutput = structuredOutputFromExecutionData(state)
		input.reviewRemarks = reviewRemarksFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["result"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.result = value
			input.structuredOutput = value.StructuredOutput
		}
	}
	if mapping, ok := operation.In["structured_output"]; ok {
		if value, ok := structuredOutputValueFromCommitPushMapping(state, mapping); ok {
			input.structuredOutput = value
		}
	}
	if mapping, ok := operation.In["review_remarks"]; ok {
		if value, ok := reviewRemarksValueFromPublishReviewResponsesMapping(state, mapping); ok {
			input.reviewRemarks = value
		}
	}
	return input
}

func reviewRemarksFromExecutionData(state *operationExecution) []integration.ReviewRemark {
	if state == nil {
		return nil
	}
	if value, ok := state.data["review_remarks"].([]integration.ReviewRemark); ok {
		return append([]integration.ReviewRemark(nil), value...)
	}
	return append([]integration.ReviewRemark(nil), state.reviewRemarks...)
}

func reviewRemarksValueFromPublishReviewResponsesMapping(state *operationExecution, mapping model.OperationMapping) ([]integration.ReviewRemark, bool) {
	if len(mapping.Value) != 0 {
		var value []integration.ReviewRemark
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return nil, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.review_remarks":
		if state == nil {
			return nil, false
		}
		value, ok := state.data["review_remarks"].([]integration.ReviewRemark)
		return value, ok
	case "state.review_remarks":
		if state == nil {
			return nil, false
		}
		return append([]integration.ReviewRemark(nil), state.reviewRemarks...), true
	default:
		return nil, false
	}
}

func publishReviewResponsesInputSummary(input publishReviewResponsesInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"invocation":        invocationSummary(input.invocation),
		"result":            resultSummary(input.result),
		"structured_output": structuredOutputSummary(input.structuredOutput),
		"review_remarks":    formatInt(len(input.reviewRemarks)),
	})
}

func publishReviewResponsesOutputSummary(summary string, result LaunchResult, operation OperationSpec) string {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"status":%q}`, result.Status))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"review_responses_summary": summary,
		"result":                   string(resultJSON),
	})
}

type reviewRemarkComment struct {
	Body string
	Path string
	Line int
	Side string
}

func (e builtinOperationExecutor) publishPullRequestComments(ctx context.Context, state *operationExecution, ref pullRequestRef, comments []reviewRemarkComment) (int, error) {
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, err
	}

	count := 0
	var failures []error
	for _, comment := range comments {
		body := strings.TrimSpace(comment.Body)
		if body == "" {
			continue
		}
		path := strings.TrimSpace(comment.Path)
		side := strings.TrimSpace(comment.Side)
		line := 0
		if comment.Line < 0 {
			failures = append(failures, fmt.Errorf("publish review remark comment %s: inline line must be positive or omitted, got %d", reviewRemarkCommentTarget(comment), comment.Line))
			continue
		}
		if path != "" && comment.Line > 0 {
			line = comment.Line
			if side == "" {
				side = "RIGHT"
			}
		} else {
			path = ""
			side = ""
		}
		_, err := executor.Execute(ctx, integration.Request{
			IntegrationType: integrationmodel.IntegrationTypeRepository,
			Resource:        "comment",
			ObjectType:      "comment",
			Operation:       "create",
			Repository:      ref.Repository,
			RepoProvided:    strings.TrimSpace(ref.Repository) != "",
			Number:          ref.Number,
			Body:            body,
			Text:            body,
			Path:            path,
			Line:            line,
			Side:            side,
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("publish review remark comment %s: %w", reviewRemarkCommentTarget(comment), err))
			continue
		}
		count++
	}

	return count, errors.Join(failures...)
}

func (e builtinOperationExecutor) publishReviewResponseComments(ctx context.Context, ref pullRequestRef, responses []StructuredResponse, reviewRemarks []integration.ReviewRemark) (int, error) {
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, response := range responses {
		body := reviewResponseCommentBody(response)
		if body == "" {
			continue
		}
		threadID := reviewThreadIDForResponse(reviewRemarks, response)
		request := integration.Request{
			IntegrationType: integrationmodel.IntegrationTypeRepository,
			Resource:        "comment",
			ObjectType:      "comment",
			Operation:       "create",
			Repository:      ref.Repository,
			RepoProvided:    strings.TrimSpace(ref.Repository) != "",
			Number:          ref.Number,
			Body:            body,
			Text:            body,
		}
		if threadID != "" {
			request.Operation = "reply"
			request.ThreadID = threadID
			request.ExternalID = threadID
		}
		_, err := executor.Execute(ctx, request)
		if err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

func (e builtinOperationExecutor) resolveReviewThreads(ctx context.Context, responses []StructuredResponse, reviewRemarks []integration.ReviewRemark) (int, error) {
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, err
	}

	resolved := 0
	seen := map[string]struct{}{}
	for _, response := range responses {
		if !isResolvedReviewResponse(response) {
			continue
		}
		threadID := reviewThreadIDForResponse(reviewRemarks, response)
		if threadID == "" {
			continue
		}
		if _, ok := seen[threadID]; ok {
			continue
		}
		seen[threadID] = struct{}{}
		_, err := executor.Execute(ctx, integration.Request{
			IntegrationType: integrationmodel.IntegrationTypeRepository,
			Resource:        "comment",
			ObjectType:      "comment",
			Operation:       "resolve",
			ExternalID:      threadID,
			ThreadID:        threadID,
		})
		if err != nil {
			return resolved, err
		}
		resolved++
	}

	return resolved, nil
}

func (e builtinOperationExecutor) integrationExecutor() (integrationExecutor, error) {
	if e.service == nil || e.service.integrations == nil {
		return nil, fmt.Errorf("integration executor is not configured")
	}
	return e.service.integrations, nil
}

func (e builtinOperationExecutor) failIntegrationOperation(ctx context.Context, state *operationExecution, name string, summary string, err error, code string) error {
	if strings.TrimSpace(state.result.Status) == "" {
		state.result = failedStartResult(err)
	} else {
		state.result.Status = "failed"
		state.result.Summary = joinExecutionSummaries(state.result.Summary, strings.TrimSpace(err.Error()))
	}
	state.tracker.fail(name, summary, err, code, true, true)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), state.result, err)
	return err
}

func (e builtinOperationExecutor) failOrSkipIntegrationOperation(ctx context.Context, state *operationExecution, name string, summary string, err error, code string, required bool) error {
	if required {
		return e.failIntegrationOperation(ctx, state, name, summary, err, code)
	}

	state.tracker.skip(name, joinExecutionSummaries(summary, strings.TrimSpace(err.Error())))
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), state.result, nil)
	return nil
}

func pullRequestRefFromState(state *operationExecution) pullRequestRef {
	ref := pullRequestRefFromAssignment(state.assignment)
	if state != nil && state.pullRequest != nil {
		if strings.TrimSpace(ref.Repository) == "" {
			ref.Repository = state.pullRequest.Repository
		}
		if ref.Number <= 0 {
			ref.Number = state.pullRequest.Number
		}
		if strings.TrimSpace(ref.Base) == "" {
			ref.Base = state.pullRequest.BaseRef
		}
		if strings.TrimSpace(ref.Head) == "" {
			ref.Head = state.pullRequest.HeadRef
		}
		if strings.TrimSpace(ref.Title) == "" {
			ref.Title = state.pullRequest.Title
		}
	}
	if strings.TrimSpace(ref.Head) == "" && state != nil && state.assignment != nil && state.assignment.CanonicalTask != nil && state.assignment.CanonicalTask.Number > 0 {
		ref.Head = strconv.Itoa(state.assignment.CanonicalTask.Number)
	}
	return ref
}

func pullRequestRefFromPublishMergeRequestInput(state *operationExecution, input publishMergeRequestInput) pullRequestRef {
	ref := pullRequestRefFromAssignment(publishMergeRequestAssignment(state, input))
	if strings.TrimSpace(ref.Repository) == "" && state != nil && state.assignment != nil {
		stateRef := pullRequestRefFromAssignment(state.assignment)
		ref.Repository = stateRef.Repository
	}
	if ref.Number <= 0 && state != nil && state.pullRequest != nil {
		ref.Number = state.pullRequest.Number
	}
	if strings.TrimSpace(ref.Repository) == "" && state != nil && state.pullRequest != nil {
		ref.Repository = state.pullRequest.Repository
	}
	if strings.TrimSpace(ref.Base) == "" && state != nil && state.pullRequest != nil {
		ref.Base = state.pullRequest.BaseRef
	}
	if strings.TrimSpace(ref.Head) == "" && state != nil && state.pullRequest != nil {
		ref.Head = state.pullRequest.HeadRef
	}
	if strings.TrimSpace(ref.Title) == "" && state != nil && state.pullRequest != nil {
		ref.Title = state.pullRequest.Title
	}
	if strings.TrimSpace(ref.Head) == "" {
		assignment := publishMergeRequestAssignment(state, input)
		if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
			ref.Head = strconv.Itoa(assignment.CanonicalTask.Number)
		}
	}
	return ref
}

func publishMergeRequestAssignment(state *operationExecution, input publishMergeRequestInput) *ExecutionAssignment {
	if input.invocation.Assignment != nil {
		return assignmentFromInvocation(input.invocation)
	}
	if state != nil {
		return state.assignment
	}
	return nil
}

func pullRequestRefFromPublishReviewResponsesInput(state *operationExecution, input publishReviewResponsesInput) pullRequestRef {
	ref := pullRequestRefFromAssignment(publishReviewResponsesAssignment(state, input))
	if strings.TrimSpace(ref.Repository) == "" && state != nil && state.assignment != nil {
		stateRef := pullRequestRefFromAssignment(state.assignment)
		ref.Repository = stateRef.Repository
	}
	if ref.Number <= 0 && state != nil && state.pullRequest != nil {
		ref.Number = state.pullRequest.Number
	}
	if strings.TrimSpace(ref.Repository) == "" && state != nil && state.pullRequest != nil {
		ref.Repository = state.pullRequest.Repository
	}
	if strings.TrimSpace(ref.Base) == "" && state != nil && state.pullRequest != nil {
		ref.Base = state.pullRequest.BaseRef
	}
	if strings.TrimSpace(ref.Head) == "" && state != nil && state.pullRequest != nil {
		ref.Head = state.pullRequest.HeadRef
	}
	if strings.TrimSpace(ref.Title) == "" && state != nil && state.pullRequest != nil {
		ref.Title = state.pullRequest.Title
	}
	if strings.TrimSpace(ref.Head) == "" {
		assignment := publishReviewResponsesAssignment(state, input)
		if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
			ref.Head = strconv.Itoa(assignment.CanonicalTask.Number)
		}
	}
	return ref
}

func publishReviewResponsesAssignment(state *operationExecution, input publishReviewResponsesInput) *ExecutionAssignment {
	if input.invocation.Assignment != nil {
		return assignmentFromInvocation(input.invocation)
	}
	if state != nil {
		return state.assignment
	}
	return nil
}

func pullRequestRefFromAssignment(assignment *ExecutionAssignment) pullRequestRef {
	ref := pullRequestRef{}
	if assignment == nil {
		return ref
	}
	if assignment.CanonicalTask != nil {
		ref.Repository = strings.TrimSpace(assignment.CanonicalTask.Repository)
		if assignment.CanonicalTask.Number > 0 {
			ref.Head = strconv.Itoa(assignment.CanonicalTask.Number)
		}
		ref.Title = strings.TrimSpace(assignment.CanonicalTask.Title)
		if assignment.CanonicalTask.Attributes != nil {
			ref.Body = strings.TrimSpace(assignment.CanonicalTask.Attributes["body"])
		}
	}
	for _, object := range assignment.RelatedObjects {
		if !isPullRequestObject(object.Type) {
			continue
		}
		if strings.TrimSpace(object.Repository) != "" {
			ref.Repository = strings.TrimSpace(object.Repository)
		}
		if object.Number > 0 {
			ref.Number = object.Number
		}
		if strings.TrimSpace(object.Title) != "" {
			ref.Title = strings.TrimSpace(object.Title)
		}
		if object.Attributes != nil {
			ref.Base = firstNonEmptyTrimmed(ref.Base, object.Attributes["base_ref"], object.Attributes["base"])
			ref.Head = firstNonEmptyTrimmed(object.Attributes["head_ref"], object.Attributes["head"], ref.Head)
			ref.Body = firstNonEmptyTrimmed(ref.Body, object.Attributes["body"])
			ref.Draft = ref.Draft || strings.EqualFold(strings.TrimSpace(object.Attributes["draft"]), "true")
		}
	}
	return ref
}

func isPullRequestObject(value string) bool {
	switch strings.TrimSpace(value) {
	case "merge-request", "pull-request", "pr":
		return true
	default:
		return false
	}
}

func mergeRequestFromIntegrationResponse(response integration.Response) (integration.MergeRequest, bool) {
	if response.MergeRequest != nil {
		return *response.MergeRequest, true
	}
	if response.PullRequest != nil {
		return integration.MergeRequest{
			System:         response.PullRequest.System,
			Repository:     response.PullRequest.Repository,
			Number:         response.PullRequest.Number,
			Title:          response.PullRequest.Title,
			Body:           response.PullRequest.Body,
			State:          response.PullRequest.State,
			BaseRef:        response.PullRequest.BaseRef,
			HeadRef:        response.PullRequest.HeadRef,
			ReviewDecision: response.PullRequest.ReviewDecision,
			URL:            response.PullRequest.URL,
			CreatedAt:      response.PullRequest.CreatedAt,
			UpdatedAt:      response.PullRequest.UpdatedAt,
		}, true
	}
	return integration.MergeRequest{}, false
}

func mergeRequestFromPublishResponse(response integration.Response, ref pullRequestRef) integration.MergeRequest {
	if mergeRequest, ok := mergeRequestFromIntegrationResponse(response); ok {
		return mergeRequest
	}
	mergeRequest := mergeRequestFromRef(ref)
	if response.PullRequestStatus != nil {
		status := response.PullRequestStatus
		mergeRequest.System = strings.TrimSpace(status.System)
		mergeRequest.Repository = firstNonEmptyTrimmed(status.Repository, mergeRequest.Repository)
		if status.Number > 0 {
			mergeRequest.Number = status.Number
		}
		mergeRequest.State = strings.TrimSpace(status.State)
		mergeRequest.BaseRef = firstNonEmptyTrimmed(status.Base, mergeRequest.BaseRef)
		mergeRequest.HeadRef = firstNonEmptyTrimmed(status.Head, mergeRequest.HeadRef)
		mergeRequest.Title = firstNonEmptyTrimmed(status.Title, mergeRequest.Title)
		mergeRequest.URL = strings.TrimSpace(status.URL)
		return mergeRequest
	}
	if response.OperationResult != nil {
		result := response.OperationResult
		mergeRequest.System = strings.TrimSpace(result.System)
		mergeRequest.ExternalID = strings.TrimSpace(result.ExternalID)
		if number, err := strconv.Atoi(strings.TrimSpace(result.ExternalID)); err == nil && number > 0 {
			mergeRequest.Number = number
		}
		mergeRequest.State = strings.TrimSpace(result.Status)
		mergeRequest.URL = strings.TrimSpace(result.URL)
		return mergeRequest
	}
	return mergeRequest
}

func mergeRequestFromRef(ref pullRequestRef) integration.MergeRequest {
	return integration.MergeRequest{
		Repository: strings.TrimSpace(ref.Repository),
		Number:     ref.Number,
		Title:      strings.TrimSpace(ref.Title),
		Body:       strings.TrimSpace(ref.Body),
		BaseRef:    strings.TrimSpace(ref.Base),
		HeadRef:    strings.TrimSpace(ref.Head),
	}
}

func applyPullRequestToState(state *operationExecution, pr integration.MergeRequest) {
	if state == nil {
		return
	}
	if state.assignment == nil {
		state.assignment = &ExecutionAssignment{}
	}
	if state.assignment.CanonicalTask == nil {
		state.assignment.CanonicalTask = &ObjectRef{Type: "task"}
	}
	if strings.TrimSpace(state.assignment.CanonicalTask.Repository) == "" {
		state.assignment.CanonicalTask.Repository = strings.TrimSpace(pr.Repository)
	}
	if number, ok := numericBranch(pr.HeadRef); ok && state.assignment.CanonicalTask.Number == 0 {
		state.assignment.CanonicalTask.Number = number
	}
	upsertPullRequestObject(state.assignment, pr)
	if strings.TrimSpace(state.in.Repository.URL) == "" {
		state.in.Repository.URL = strings.TrimSpace(pr.Repository)
	}
	if strings.TrimSpace(state.in.Workplace.BaseRef) == "" {
		state.in.Workplace.BaseRef = strings.TrimSpace(pr.BaseRef)
	}
	if strings.TrimSpace(pr.HeadRef) != "" {
		state.in.Workplace.HeadRef = strings.TrimSpace(pr.HeadRef)
	}
	workplaceName := workplaceNameFromState(state)
	if workplaceName != "" {
		state.in.Workplace.Name = workplaceName
	}
}

func invocationWithPullRequest(in invocation, pr integration.MergeRequest) invocation {
	result := in
	assignment := assignmentFromInvocation(result)
	if assignment == nil {
		assignment = &ExecutionAssignment{}
	}
	if assignment.CanonicalTask == nil {
		assignment.CanonicalTask = &ObjectRef{Type: "task"}
	}
	if strings.TrimSpace(assignment.CanonicalTask.Repository) == "" {
		assignment.CanonicalTask.Repository = strings.TrimSpace(pr.Repository)
	}
	if number, ok := numericBranch(pr.HeadRef); ok && assignment.CanonicalTask.Number == 0 {
		assignment.CanonicalTask.Number = number
	}
	upsertPullRequestObject(assignment, pr)
	result.Assignment = assignment
	if strings.TrimSpace(result.Repository.URL) == "" {
		result.Repository.URL = strings.TrimSpace(pr.Repository)
	}
	if strings.TrimSpace(result.Workplace.BaseRef) == "" {
		result.Workplace.BaseRef = strings.TrimSpace(pr.BaseRef)
	}
	if strings.TrimSpace(pr.HeadRef) != "" {
		result.Workplace.HeadRef = strings.TrimSpace(pr.HeadRef)
	}
	if workplaceName := workplaceNameFromPullRequestAssignment(assignment); workplaceName != "" {
		result.Workplace.Name = workplaceName
	}
	return result
}

func upsertPullRequestObject(assignment *ExecutionAssignment, pr integration.MergeRequest) {
	if assignment == nil {
		return
	}
	object := ObjectRef{
		Type:       "merge-request",
		Repository: strings.TrimSpace(pr.Repository),
		Number:     pr.Number,
		Title:      strings.TrimSpace(pr.Title),
		URL:        strings.TrimSpace(pr.URL),
		Attributes: map[string]string{
			"base_ref": strings.TrimSpace(pr.BaseRef),
			"head_ref": strings.TrimSpace(pr.HeadRef),
		},
	}
	for index, existing := range assignment.RelatedObjects {
		if !isPullRequestObject(existing.Type) {
			continue
		}
		assignment.RelatedObjects[index] = object
		return
	}
	assignment.RelatedObjects = append(assignment.RelatedObjects, object)
}

func numericBranch(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	return number, err == nil && number > 0
}

func workplaceNameFromState(state *operationExecution) string {
	return workplaceNameFromRef(workplaceHeadRefFromState(state))
}

func workplaceNameFromPullRequestAssignment(assignment *ExecutionAssignment) string {
	ref := pullRequestRefFromAssignment(assignment)
	if strings.TrimSpace(ref.Head) != "" {
		return workplaceNameFromRef(ref.Head)
	}
	if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
		return workplaceNameFromRef(strconv.Itoa(assignment.CanonicalTask.Number))
	}
	return ""
}

func workplaceHeadRefFromState(state *operationExecution) string {
	if state == nil || state.assignment == nil {
		return ""
	}
	ref := pullRequestRefFromAssignment(state.assignment)
	if strings.TrimSpace(ref.Head) != "" {
		return strings.TrimSpace(ref.Head)
	}
	if state.assignment.CanonicalTask != nil && state.assignment.CanonicalTask.Number > 0 {
		return strconv.Itoa(state.assignment.CanonicalTask.Number)
	}
	return ""
}

func workplaceNameFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	return stableIdentifier(ref)
}

func ensureExecutionStructuredInput(state *operationExecution) *StructuredInput {
	if state.assignment == nil {
		state.assignment = &ExecutionAssignment{}
	}
	if state.assignment.StructuredInput == nil {
		state.assignment.StructuredInput = &StructuredInput{}
	}
	state.in.Launch.StructuredInput = state.assignment.StructuredInput
	return state.assignment.StructuredInput
}

func structuredRemarksFromIntegration(remarks []integration.ReviewRemark) []StructuredRemark {
	result := make([]StructuredRemark, 0, len(remarks))
	for index, remark := range remarks {
		id := firstNonEmptyTrimmed(remark.ExternalID, remark.URL, fmt.Sprintf("remark-%d", index+1))
		title := "Замечание ревизии"
		if strings.TrimSpace(remark.Path) != "" {
			title = fmt.Sprintf("%s:%d", strings.TrimSpace(remark.Path), remark.Line)
		}
		body := strings.TrimSpace(remark.Body)
		if body == "" && strings.TrimSpace(remark.URL) != "" {
			body = strings.TrimSpace(remark.URL)
		}
		result = append(result, StructuredRemark{
			ID:       id,
			Status:   firstNonEmptyTrimmed(remark.State, "open"),
			Type:     "review-remark",
			Title:    title,
			Body:     body,
			Path:     strings.TrimSpace(remark.Path),
			Line:     remark.Line,
			Side:     strings.TrimSpace(remark.Side),
			Severity: "",
		})
	}
	return result
}

func mergeStructuredRemarks(base []StructuredRemark, additions []StructuredRemark) []StructuredRemark {
	seen := map[string]struct{}{}
	result := make([]StructuredRemark, 0, len(base)+len(additions))
	for _, remark := range base {
		if strings.TrimSpace(remark.ID) != "" {
			seen[strings.TrimSpace(remark.ID)] = struct{}{}
		}
		result = append(result, remark)
	}
	for _, remark := range additions {
		id := strings.TrimSpace(remark.ID)
		if id != "" {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
		}
		result = append(result, remark)
	}
	return result
}

func mergeRequestSummary(pr integration.MergeRequest) string {
	return fmt.Sprintf("repository=%s number=%d state=%s base=%s head=%s url=%s", pr.Repository, pr.Number, pr.State, pr.BaseRef, pr.HeadRef, pr.URL)
}

func pullRequestTitle(state *operationExecution) string {
	ref := pullRequestRefFromState(state)
	if strings.TrimSpace(ref.Title) != "" {
		return strings.TrimSpace(ref.Title)
	}
	if state != nil && state.assignment != nil && state.assignment.CanonicalTask != nil {
		task := state.assignment.CanonicalTask
		if task.Number > 0 && strings.TrimSpace(task.Title) != "" {
			return fmt.Sprintf("Задача #%d: %s", task.Number, strings.TrimSpace(task.Title))
		}
		if task.Number > 0 {
			return fmt.Sprintf("Задача #%d", task.Number)
		}
	}
	if state != nil && state.result.StructuredOutput != nil && strings.TrimSpace(state.result.StructuredOutput.CommitMessage) != "" {
		return strings.TrimSpace(state.result.StructuredOutput.CommitMessage)
	}
	return "Инженерное изменение"
}

func pullRequestTitleFromPublishMergeRequestInput(state *operationExecution, input publishMergeRequestInput, ref pullRequestRef) string {
	if strings.TrimSpace(ref.Title) != "" {
		return strings.TrimSpace(ref.Title)
	}
	assignment := publishMergeRequestAssignment(state, input)
	if assignment != nil && assignment.CanonicalTask != nil {
		task := assignment.CanonicalTask
		if task.Number > 0 && strings.TrimSpace(task.Title) != "" {
			return fmt.Sprintf("Задача #%d: %s", task.Number, strings.TrimSpace(task.Title))
		}
		if task.Number > 0 {
			return fmt.Sprintf("Задача #%d", task.Number)
		}
	}
	if input.structuredOutput != nil && strings.TrimSpace(input.structuredOutput.CommitMessage) != "" {
		return strings.TrimSpace(input.structuredOutput.CommitMessage)
	}
	return "Инженерное изменение"
}

func pullRequestBody(state *operationExecution) string {
	parts := []string{}
	if state != nil && state.assignment != nil && state.assignment.CanonicalTask != nil {
		task := state.assignment.CanonicalTask
		if task.Number > 0 {
			parts = append(parts, fmt.Sprintf("Задача: #%d", task.Number))
		}
		if strings.TrimSpace(task.URL) != "" {
			parts = append(parts, "Ссылка на задачу: "+strings.TrimSpace(task.URL))
		}
		if task.Attributes != nil && strings.TrimSpace(task.Attributes["body"]) != "" {
			parts = append(parts, strings.TrimSpace(task.Attributes["body"]))
		}
	}
	if output := state.result.StructuredOutput; output != nil {
		if strings.TrimSpace(output.Summary) != "" {
			parts = append(parts, "Результат:\n"+strings.TrimSpace(output.Summary))
		}
		if len(output.Changes) != 0 {
			lines := []string{"Изменения:"}
			for _, change := range output.Changes {
				if strings.TrimSpace(change.Summary) != "" {
					lines = append(lines, "- "+strings.TrimSpace(change.Summary))
				}
			}
			if len(lines) > 1 {
				parts = append(parts, strings.Join(lines, "\n"))
			}
		}
	}
	if len(parts) == 0 {
		return "Запрос на слияние открыт контуром исполнения."
	}
	return strings.Join(parts, "\n\n")
}

func pullRequestBodyFromPublishMergeRequestInput(state *operationExecution, input publishMergeRequestInput) string {
	parts := []string{}
	assignment := publishMergeRequestAssignment(state, input)
	if assignment != nil && assignment.CanonicalTask != nil {
		task := assignment.CanonicalTask
		if task.Number > 0 {
			parts = append(parts, fmt.Sprintf("Задача: #%d", task.Number))
		}
		if strings.TrimSpace(task.URL) != "" {
			parts = append(parts, "Ссылка на задачу: "+strings.TrimSpace(task.URL))
		}
		if task.Attributes != nil && strings.TrimSpace(task.Attributes["body"]) != "" {
			parts = append(parts, strings.TrimSpace(task.Attributes["body"]))
		}
	}
	if output := input.structuredOutput; output != nil {
		if strings.TrimSpace(output.Summary) != "" {
			parts = append(parts, "Результат:\n"+strings.TrimSpace(output.Summary))
		}
		if len(output.Changes) != 0 {
			lines := []string{"Изменения:"}
			for _, change := range output.Changes {
				if strings.TrimSpace(change.Summary) != "" {
					lines = append(lines, "- "+strings.TrimSpace(change.Summary))
				}
			}
			if len(lines) > 1 {
				parts = append(parts, strings.Join(lines, "\n"))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func pullRequestAlreadyAvailable(response integration.Response) bool {
	if response.PullRequestStatus != nil && response.PullRequestStatus.Number > 0 && strings.TrimSpace(response.PullRequestStatus.URL) != "" {
		return true
	}
	if response.OperationResult != nil && strings.TrimSpace(response.OperationResult.URL) != "" {
		return true
	}
	return false
}

func pullRequestPublishSummary(response integration.Response) string {
	if response.PullRequestStatus != nil {
		return fmt.Sprintf("pull-request=%d url=%s state=%s", response.PullRequestStatus.Number, response.PullRequestStatus.URL, response.PullRequestStatus.State)
	}
	if response.OperationResult != nil {
		return fmt.Sprintf("pull-request=%s url=%s status=%s", response.OperationResult.ExternalID, response.OperationResult.URL, response.OperationResult.Status)
	}
	return "pull-request=published"
}

func reviewRemarkComments(output *StructuredOutput) []reviewRemarkComment {
	if output == nil {
		return nil
	}
	if len(output.Remarks) == 0 {
		if output.Conclusion == nil {
			return nil
		}
		body := strings.TrimSpace(strings.Join(nonEmptyParts([]string{
			"## Заключение ревизии",
			output.Conclusion.Status,
			output.Conclusion.Summary,
			output.Conclusion.Body,
		}), "\n\n"))
		if body == "## Заключение ревизии" {
			return nil
		}
		return []reviewRemarkComment{{Body: body}}
	}

	comments := make([]reviewRemarkComment, 0, len(output.Remarks))
	for _, remark := range output.Remarks {
		body := strings.TrimSpace(strings.Join(nonEmptyParts([]string{
			"## Замечание ревизии",
			formatNamedLine("Идентификатор", remark.ID),
			formatNamedLine("Критичность", remark.Severity),
			formatNamedLine("Тип", remark.Type),
			formatNamedLine("Заголовок", remark.Title),
			remark.Body,
		}), "\n\n"))
		if body != "" {
			comments = append(comments, reviewRemarkComment{
				Body: body,
				Path: strings.TrimSpace(remark.Path),
				Line: remark.Line,
				Side: strings.TrimSpace(remark.Side),
			})
		}
	}
	return comments
}

func reviewRemarkCommentTarget(comment reviewRemarkComment) string {
	path := strings.TrimSpace(comment.Path)
	side := strings.TrimSpace(comment.Side)
	if side == "" {
		side = "RIGHT"
	}
	if path != "" && comment.Line != 0 {
		return fmt.Sprintf("%s:%d:%s", path, comment.Line, side)
	}
	if path == "" || comment.Line <= 0 {
		return "pull-request"
	}
	return fmt.Sprintf("%s:%d:%s", path, comment.Line, side)
}

func reviewResponsesFromOutput(output *StructuredOutput) []StructuredResponse {
	if output == nil {
		return nil
	}
	responses := append([]StructuredResponse(nil), output.ReviewResponses...)
	for _, remark := range output.Remarks {
		if strings.TrimSpace(remark.Answer) == "" && strings.TrimSpace(remark.Resolution) == "" {
			continue
		}
		responses = append(responses, StructuredResponse{
			RemarkID: strings.TrimSpace(remark.ID),
			Status:   firstNonEmptyTrimmed(remark.Status, "resolved"),
			Summary:  strings.TrimSpace(remark.Resolution),
			Body:     strings.TrimSpace(remark.Answer),
		})
	}
	return responses
}

func reviewResponseCommentBodies(responses []StructuredResponse) []string {
	bodies := make([]string, 0, len(responses))
	for _, response := range responses {
		if body := reviewResponseCommentBody(response); body != "" {
			bodies = append(bodies, body)
		}
	}
	return bodies
}

func reviewResponseCommentBody(response StructuredResponse) string {
	return strings.TrimSpace(strings.Join(nonEmptyParts([]string{
		"## Ответ на замечание ревизии",
		formatNamedLine("Замечание", response.RemarkID),
		formatNamedLine("Состояние", response.Status),
		response.Summary,
		response.Body,
	}), "\n\n"))
}

func isResolvedReviewResponse(response StructuredResponse) bool {
	status := strings.ToLower(strings.TrimSpace(response.Status))
	return status == "resolved" || status == "fixed" || status == "done" || status == "ok"
}

func reviewThreadIDForResponse(remarks []integration.ReviewRemark, response StructuredResponse) string {
	remarkID := strings.TrimSpace(response.RemarkID)
	if remarkID == "" {
		remarkID = strings.TrimSpace(response.ID)
	}
	if remarkID == "" {
		return ""
	}
	for _, remark := range remarks {
		if remarkID != strings.TrimSpace(remark.ExternalID) && remarkID != strings.TrimSpace(remark.URL) && remarkID != strings.TrimSpace(remark.ReplyToID) {
			continue
		}
		return strings.TrimSpace(remark.ReplyToID)
	}
	return ""
}

func formatNamedLine(name string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return name + ": " + value
}
