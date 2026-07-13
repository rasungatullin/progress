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
		IntegrationType:    integrationmodel.IntegrationTypeRepository,
		Resource:           "merge-request",
		ObjectType:         "merge-request",
		Operation:          "get",
		Repository:         ref.Repository,
		RepoProvided:       strings.TrimSpace(ref.Repository) != "",
		MergeRequestNumber: ref.Number,
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
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if _, ok := operation.In["invocation"]; !ok {
		input.invocation.Repository.URL, _ = operationMappingValue[string](state, operation.In["repository"])
		input.invocation.Workplace.BaseRef, _ = operationMappingValue[string](state, operation.In["base_ref"])
		input.invocation.Workplace.HeadRef, _ = operationMappingValue[string](state, operation.In["head_ref"])
		number, _ := operationMappingValue[int](state, operation.In["number"])
		input.invocation.Assignment = &ExecutionAssignment{CanonicalTask: &ObjectRef{Type: "pull-request", Repository: input.invocation.Repository.URL, Number: number}}
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
		IntegrationType:    integrationmodel.IntegrationTypeRepository,
		Resource:           "review-remark",
		ObjectType:         "review-remark",
		Operation:          "list",
		Repository:         ref.Repository,
		RepoProvided:       strings.TrimSpace(ref.Repository) != "",
		MergeRequestNumber: ref.Number,
	})
	if err != nil {
		return e.failOrSkipLoadReviewRemarksOperation(ctx, state, operation, input.invocation, name, "Замечания ревизии не получены.", err, "review_remarks_load_failed", required)
	}

	reviewRemarks := append([]integration.ReviewRemark(nil), response.ReviewRemarks...)
	writeLoadReviewRemarksData(state, operation, reviewRemarks)
	state.tracker.completeIO(name, loadReviewRemarksInputSummary(input, ref, operation), loadReviewRemarksOutputSummary(reviewRemarks, operation), "Замечания ревизии получены через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) failOrSkipLoadReviewRemarksOperation(ctx context.Context, state *operationExecution, operation OperationSpec, in invocation, name string, summary string, err error, code string, required bool) error {
	if required {
		result := failedStartResult(err)
		writeLoadReviewRemarksFailureData(state, operation, result)
		state.tracker.fail(name, summary, err, code, true, true)
		return err
	}

	writeOperationData(state, operation.Out, "review_remarks", []integration.ReviewRemark(nil))
	state.tracker.skip(name, joinExecutionSummaries(summary, strings.TrimSpace(err.Error())))
	return nil
}

func writeLoadReviewRemarksData(state *operationExecution, operation OperationSpec, remarks []integration.ReviewRemark) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"review_remarks": {Ref: "data.review_remarks"},
		}
	}
	writeOperationData(state, out, "review_remarks", remarks)
}

func writeLoadReviewRemarksFailureData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"result": {Ref: "data.result"}}
	}
	writeOperationData(state, out, "result", result)
}

type loadReviewRemarksInput struct {
	invocation  invocation
	pullRequest *integration.MergeRequest
}

func loadReviewRemarksInputFromOperation(state *operationExecution, operation OperationSpec) loadReviewRemarksInput {
	input := loadReviewRemarksInput{}
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
	if _, ok := operation.In["invocation"]; !ok {
		repository, _ := operationMappingValue[string](state, operation.In["repository"])
		number, _ := operationMappingValue[int](state, operation.In["number"])
		input.invocation.Repository.URL = repository
		input.invocation.Assignment = &ExecutionAssignment{CanonicalTask: &ObjectRef{Type: "pull-request", Repository: repository, Number: number}}
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

func loadReviewRemarksOutputSummary(remarks []integration.ReviewRemark, operation OperationSpec) string {
	return operationIOSummary(operation.Out, map[string]string{
		"review_remarks": formatInt(len(remarks)),
	})
}

func (e builtinOperationExecutor) publishMergeRequest(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := publishMergeRequestInputFromOperation(state, operation)
	ref := pullRequestRefFromPublishMergeRequestInput(input)
	if ref.Number > 0 {
		mergeRequest := mergeRequestFromRef(ref)
		summary := fmt.Sprintf("pull-request=%d", ref.Number)
		writePublishMergeRequestData(state, operation, mergeRequest, summary, input.result)
		state.tracker.skipIO(name, publishMergeRequestInputSummary(input, ref, operation), publishMergeRequestOutputSummary(mergeRequest, summary, input.result, operation), fmt.Sprintf("Запрос на слияние уже задан: number=%d.", ref.Number))
		return nil
	}
	if strings.TrimSpace(ref.Repository) == "" {
		return e.failPublishMergeRequestOperation(ctx, state, operation, input, name, "Репозиторий запроса на слияние не задан.", fmt.Errorf("repository is required for pull request creation"), "pull_request_repository_required")
	}
	if strings.TrimSpace(ref.Head) == "" {
		return e.failPublishMergeRequestOperation(ctx, state, operation, input, name, "Ветка запроса на слияние не задана.", fmt.Errorf("head branch is required for pull request creation"), "pull_request_head_required")
	}
	if strings.TrimSpace(ref.Base) == "" {
		base, err := e.defaultMergeRequestBase(ctx, input.workplace)
		if err != nil {
			return e.failPublishMergeRequestOperation(ctx, state, operation, input, name, "Базовая ветка запроса на слияние не определена.", err, "pull_request_base_required")
		}
		ref.Base = base
	}
	if strings.TrimSpace(ref.Title) == "" {
		ref.Title = pullRequestTitleFromPublishMergeRequestInput(input, ref)
	}
	if strings.TrimSpace(ref.Body) == "" {
		ref.Body = pullRequestBodyFromPublishMergeRequestInput(input)
	}

	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failPublishMergeRequestOperation(ctx, state, operation, input, name, "Контур интеграции недоступен.", err, "integration_unavailable")
	}

	response, err := executor.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeRepository,
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "search",
		Repository:      ref.Repository,
		RepoProvided:    true,
		Query:           "head:" + ref.Head,
		State:           "open",
		Limit:           100,
	})
	if err != nil {
		return e.failPublishMergeRequestOperation(ctx, state, operation, input, name, "Наличие открытого запроса на слияние не проверено.", err, "pull_request_existence_check_failed")
	}
	for _, existing := range response.MergeRequests {
		if strings.TrimSpace(existing.HeadRef) != ref.Head || (strings.TrimSpace(existing.BaseRef) != "" && strings.TrimSpace(existing.BaseRef) != ref.Base) {
			continue
		}
		summary := fmt.Sprintf("pull-request=%d", existing.Number)
		result := input.result
		result.Summary = joinExecutionSummaries(result.Summary, summary)
		writePublishMergeRequestData(state, operation, existing, summary, result)
		state.tracker.skipIO(name, publishMergeRequestInputSummary(input, ref, operation), publishMergeRequestOutputSummary(existing, summary, result, operation), "Открытый запрос на слияние уже существует; создание пропущено.")
		return nil
	}

	response, err = executor.Execute(ctx, integration.Request{
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
		return e.failPublishMergeRequestOperation(ctx, state, operation, input, name, "Запрос на слияние не открыт.", err, "pull_request_publish_failed")
	}

	summary := pullRequestPublishSummary(response)
	mergeRequest := mergeRequestFromPublishResponse(response, ref)
	result := input.result
	result.Summary = joinExecutionSummaries(result.Summary, summary)
	writePublishMergeRequestData(state, operation, mergeRequest, summary, result)
	state.tracker.completeIO(name, publishMergeRequestInputSummary(input, ref, operation), publishMergeRequestOutputSummary(mergeRequest, summary, result, operation), "Запрос на слияние зафиксирован через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) failPublishMergeRequestOperation(ctx context.Context, state *operationExecution, operation OperationSpec, input publishMergeRequestInput, name string, summary string, err error, code string) error {
	result := failedPublishMergeRequestResult(input, err)
	writePublishMergeRequestFailureData(state, operation, result)
	state.tracker.fail(name, summary, err, code, true, true)
	return err
}

func failedPublishMergeRequestResult(input publishMergeRequestInput, err error) LaunchResult {
	result := input.result
	if strings.TrimSpace(result.Status) == "" {
		result = failedStartResult(err)
	} else {
		result.Status = "failed"
		result.Summary = joinExecutionSummaries(result.Summary, strings.TrimSpace(err.Error()))
	}
	if result.StructuredOutput == nil {
		result.StructuredOutput = input.structuredOutput
	}
	return result
}

func writePublishMergeRequestFailureData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"result": {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "result", result)
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
	ref              pullRequestRef
	invocation       invocation
	profile          profile
	allocation       allocation
	workplace        workplace
	result           LaunchResult
	structuredOutput *StructuredOutput
}

func publishMergeRequestInputFromOperation(state *operationExecution, operation OperationSpec) publishMergeRequestInput {
	input := publishMergeRequestInput{ref: pullRequestRefFromOperation(state, operation)}
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
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.allocation = value
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

func publishReviewRemarksInputFromOperation(state *operationExecution, operation OperationSpec) publishMergeRequestInput {
	input := publishMergeRequestInput{ref: pullRequestRefFromOperation(state, operation)}
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
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.allocation = value
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

func publishMergeRequestInputSummary(input publishMergeRequestInput, ref pullRequestRef, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"invocation":        invocationSummary(input.invocation),
		"profile":           profileSummary(input.profile),
		"allocation":        allocationSummary(input.allocation),
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

func (e builtinOperationExecutor) defaultMergeRequestBase(ctx context.Context, workplace workplace) (string, error) {
	repoRoot := strings.TrimSpace(workplace.RepositoryRoot)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(workplace.Name)
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
	input := publishReviewRemarksInputFromOperation(state, operation)
	ref := pullRequestRefFromPublishReviewRemarksInput(input)
	if ref.Number <= 0 {
		return e.failPublishReviewRemarksOperation(ctx, state, operation, input, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	comments := reviewRemarkComments(input.structuredOutput)
	if len(comments) == 0 {
		writePublishReviewRemarksData(state, operation, "", input.result)
		state.tracker.skipIO(name, publishReviewRemarksInputSummary(input, operation), publishReviewRemarksOutputSummary("", input.result, operation), "Структурированный вывод не содержит замечаний или заключения для записи.")
		return nil
	}

	count, err := e.publishPullRequestComments(ctx, state, ref, comments)
	if err != nil {
		return e.failPublishReviewRemarksOperation(ctx, state, operation, input, name, "Замечания ревизии не записаны.", err, "review_remarks_publish_failed")
	}
	if _, err := e.updateReviewRemarkThreads(ctx, comments); err != nil {
		return e.failPublishReviewRemarksOperation(ctx, state, operation, input, name, "Замечания записаны, но состояние цепочки обсуждения не обновлено.", err, "review_thread_state_update_failed")
	}

	summary := fmt.Sprintf("review-remarks-published=%d", count)
	result := input.result
	result.Summary = joinExecutionSummaries(result.Summary, summary)
	writePublishReviewRemarksData(state, operation, summary, result)
	state.tracker.completeIO(name, publishReviewRemarksInputSummary(input, operation), publishReviewRemarksOutputSummary(summary, result, operation), "Замечания ревизии записаны через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) failPublishReviewRemarksOperation(ctx context.Context, state *operationExecution, operation OperationSpec, input publishMergeRequestInput, name string, summary string, err error, code string) error {
	result := failedPublishReviewRemarksResult(input, err)
	writePublishReviewRemarksFailureData(state, operation, result)
	state.tracker.fail(name, summary, err, code, true, true)
	return err
}

func failedPublishReviewRemarksResult(input publishMergeRequestInput, err error) LaunchResult {
	result := input.result
	if strings.TrimSpace(result.Status) == "" {
		result = failedStartResult(err)
	} else {
		result.Status = "failed"
		result.Summary = joinExecutionSummaries(result.Summary, strings.TrimSpace(err.Error()))
	}
	if result.StructuredOutput == nil {
		result.StructuredOutput = input.structuredOutput
	}
	return result
}

func writePublishReviewRemarksFailureData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"result": {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "result", result)
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
		"profile":           profileSummary(input.profile),
		"allocation":        allocationSummary(input.allocation),
		"workplace":         workplaceSummary(input.workplace),
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
	ref := pullRequestRefFromPublishReviewResponsesInput(input)
	if ref.Number <= 0 {
		return e.failPublishReviewResponsesOperation(ctx, state, operation, input, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	responses := reviewResponsesFromOutput(input.structuredOutput, input.reviewRemarks)
	if len(responses) == 0 {
		writePublishReviewResponsesData(state, operation, "", input.result)
		state.tracker.skipIO(name, publishReviewResponsesInputSummary(input, operation), publishReviewResponsesOutputSummary("", input.result, operation), "Структурированный вывод не содержит ответов на замечания.")
		return nil
	}
	if err := validateReviewResponses(responses); err != nil {
		return e.failPublishReviewResponsesOperation(ctx, state, operation, input, name, "Ответы на замечания не прошли предварительную проверку.", err, "review_responses_invalid")
	}
	if err := validateReviewResponseTargets(responses, input.reviewRemarks, input.structuredOutput); err != nil {
		return e.failPublishReviewResponsesOperation(ctx, state, operation, input, name, "Ответы на замечания не прошли предварительную проверку.", err, "review_responses_invalid")
	}

	count, publishedResponses, publishFailures := e.publishReviewResponseComments(ctx, ref, responses)
	resolved, resolveFailures := e.resolveReviewThreads(ctx, publishedResponses)
	summary := fmt.Sprintf("review-responses-published=%d review-threads-resolved=%d", count, resolved)
	if skipped := reviewResponseCountByType(responses, "local"); skipped > 0 {
		summary += fmt.Sprintf(" review-responses-skipped=%d", skipped)
	}
	result := input.result
	result.Summary = joinExecutionSummaries(result.Summary, summary)
	if err := errors.Join(publishFailures, resolveFailures); err != nil {
		result.Status = "failed"
		result.Summary = joinExecutionSummaries(result.Summary, err.Error())
		writePublishReviewResponsesFailureData(state, operation, result)
		state.tracker.fail(name, "Ответы обработаны частично.", err, "review_responses_partial_failure", true, true)
		return err
	}
	writePublishReviewResponsesData(state, operation, summary, result)
	state.tracker.completeIO(name, publishReviewResponsesInputSummary(input, operation), publishReviewResponsesOutputSummary(summary, result, operation), "Ответы на замечания записаны через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) failPublishReviewResponsesOperation(ctx context.Context, state *operationExecution, operation OperationSpec, input publishReviewResponsesInput, name string, summary string, err error, code string) error {
	result := failedPublishReviewResponsesResult(input, err)
	writePublishReviewResponsesFailureData(state, operation, result)
	state.tracker.fail(name, summary, err, code, true, true)
	return err
}

func failedPublishReviewResponsesResult(input publishReviewResponsesInput, err error) LaunchResult {
	result := input.result
	if strings.TrimSpace(result.Status) == "" {
		result = failedStartResult(err)
	} else {
		result.Status = "failed"
		result.Summary = joinExecutionSummaries(result.Summary, strings.TrimSpace(err.Error()))
	}
	if result.StructuredOutput == nil {
		result.StructuredOutput = input.structuredOutput
	}
	return result
}

func writePublishReviewResponsesFailureData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"result": {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "result", result)
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
	ref              pullRequestRef
	invocation       invocation
	profile          profile
	allocation       allocation
	workplace        workplace
	result           LaunchResult
	structuredOutput *StructuredOutput
	reviewRemarks    []integration.ReviewRemark
}

func publishReviewResponsesInputFromOperation(state *operationExecution, operation OperationSpec) publishReviewResponsesInput {
	input := publishReviewResponsesInput{ref: pullRequestRefFromOperation(state, operation)}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.allocation = value
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
	if mapping, ok := operation.In["review_remarks"]; ok {
		input.reviewRemarks, _ = operationMappingValue[[]integration.ReviewRemark](state, mapping)
	}
	return input
}

func publishReviewResponsesInputSummary(input publishReviewResponsesInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"invocation":        invocationSummary(input.invocation),
		"profile":           profileSummary(input.profile),
		"allocation":        allocationSummary(input.allocation),
		"workplace":         workplaceSummary(input.workplace),
		"result":            resultSummary(input.result),
		"structured_output": structuredOutputSummary(input.structuredOutput),
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
	Body       string
	Path       string
	Line       int
	Side       string
	ExternalID string
	ThreadID   string
	Status     string
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
		comment = normalizeReviewRemarkCommentIdentifiers(comment)
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
		request := integration.Request{
			IntegrationType:    integrationmodel.IntegrationTypeRepository,
			Resource:           "comment",
			ObjectType:         "comment",
			Operation:          reviewRemarkCommentOperation(comment),
			Repository:         ref.Repository,
			RepoProvided:       strings.TrimSpace(ref.Repository) != "",
			MergeRequestNumber: ref.Number,
			ExternalID:         strings.TrimSpace(comment.ExternalID),
			ThreadID:           strings.TrimSpace(comment.ThreadID),
			Body:               body,
			Text:               body,
			Path:               path,
			Line:               line,
			Side:               side,
		}
		response, err := executor.Execute(ctx, request)
		if err != nil {
			if request.Operation == "reply" && staleGitHubReviewThread(response, err) {
				if line > 0 {
					request.Operation = "create"
					request.ExternalID = ""
					request.ThreadID = ""
					response, err = executor.Execute(ctx, request)
				} else {
					fallbackPublished, fallbackErr := e.publishReviewRemarkFallback(ctx, executor, ref, body)
					if fallbackErr == nil {
						if fallbackPublished {
							count++
						}
						continue
					}
					err = errors.Join(err, fmt.Errorf("fallback to pull request comment failed: %w", fallbackErr))
				}
				if err == nil {
					count++
					continue
				}
			}
			if line > 0 && request.Operation == "create" && unresolvedGitHubReviewLine(response, err) {
				fallbackBody := reviewRemarkFallbackBody(body, path, line, side)
				published, fallbackErr := e.publishReviewRemarkFallback(ctx, executor, ref, fallbackBody)
				if fallbackErr == nil {
					if published {
						count++
					}
					if e.service != nil && e.service.logger != nil {
						e.service.logger.Printf("Публикация встроенного замечания %s переведена в общий комментарий запроса на слияние: позиция не разрешается GitHub", reviewRemarkCommentTarget(comment))
					}
					continue
				}
				err = errors.Join(err, fmt.Errorf("fallback to pull request comment failed: %w", fallbackErr))
			}
			failures = append(failures, fmt.Errorf("publish review remark comment %s: %w", reviewRemarkCommentTarget(comment), err))
			continue
		}
		count++
	}

	return count, errors.Join(failures...)
}

func normalizeReviewRemarkCommentIdentifiers(comment reviewRemarkComment) reviewRemarkComment {
	threadID := strings.TrimSpace(comment.ThreadID)
	if !strings.HasPrefix(threadID, "PRRC_") {
		return comment
	}

	// PRRC обозначает комментарий, а не PullRequestReviewThread. При такой
	// подмене публикуем новое замечание, не передавая идентификатор комментария
	// в GitHub как идентификатор цепочки.
	if externalID := strings.TrimSpace(comment.ExternalID); strings.HasPrefix(externalID, "PRRT_") {
		comment.ExternalID, comment.ThreadID = threadID, externalID
		return comment
	}
	comment.ThreadID = ""
	comment.ExternalID = ""
	return comment
}

func unresolvedGitHubReviewLine(response integration.Response, err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if response.System != "" && !strings.EqualFold(strings.TrimSpace(response.System), "github") {
		return false
	}
	return strings.Contains(message, "status 422") &&
		strings.Contains(message, "pull_request_review_thread.line") &&
		strings.Contains(message, "could not be resolved")
}

func staleGitHubReviewThread(response integration.Response, err error) bool {
	if err == nil {
		return false
	}
	if response.System != "" && !strings.EqualFold(strings.TrimSpace(response.System), "github") {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "github") && strings.Contains(message, "could not resolve to a node") && strings.Contains(message, "global id")
}

func reviewRemarkFallbackBody(body, path string, line int, side string) string {
	return strings.TrimSpace(strings.Join([]string{
		body,
		fmt.Sprintf("Исходная позиция встроенного замечания: `%s:%d:%s`", path, line, side),
	}, "\n\n"))
}

func (e builtinOperationExecutor) publishReviewRemarkFallback(ctx context.Context, executor integrationExecutor, ref pullRequestRef, body string) (bool, error) {
	commentsResponse, err := executor.Execute(ctx, integration.Request{
		IntegrationType:    integrationmodel.IntegrationTypeRepository,
		Resource:           "review-remark",
		ObjectType:         "review-remark",
		Operation:          "list",
		Repository:         ref.Repository,
		RepoProvided:       strings.TrimSpace(ref.Repository) != "",
		MergeRequestNumber: ref.Number,
	})
	if err != nil {
		return false, fmt.Errorf("check existing pull request comments: %w", err)
	}
	for _, existing := range commentsResponse.ReviewRemarks {
		if strings.TrimSpace(existing.Body) == body && strings.TrimSpace(existing.Path) == "" && existing.Line == 0 {
			return true, nil
		}
	}

	_, err = executor.Execute(ctx, integration.Request{
		IntegrationType:    integrationmodel.IntegrationTypeRepository,
		Resource:           "comment",
		ObjectType:         "comment",
		Operation:          "create",
		Repository:         ref.Repository,
		RepoProvided:       strings.TrimSpace(ref.Repository) != "",
		MergeRequestNumber: ref.Number,
		Body:               body,
		Text:               body,
	})
	if err != nil {
		return false, fmt.Errorf("create pull request fallback comment: %w", err)
	}
	return true, nil
}

func (e builtinOperationExecutor) publishReviewResponseComments(ctx context.Context, ref pullRequestRef, responses []StructuredResponse) (int, []StructuredResponse, error) {
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, nil, err
	}

	count := 0
	var published []StructuredResponse
	var failures []error
	for _, response := range responses {
		typ := reviewResponseType(response)
		if typ == "local" {
			continue
		}
		if typ != "inline" && typ != "comment" {
			failures = append(failures, fmt.Errorf("publish review response %s: unsupported type %q", reviewResponseTarget(response), response.Type))
			continue
		}
		body := reviewResponseCommentBody(response)
		if body == "" {
			continue
		}
		threadID := strings.TrimSpace(response.ThreadID)
		if typ == "inline" && threadID == "" {
			failures = append(failures, fmt.Errorf("publish review response %s: thread_id is required for inline response", reviewResponseTarget(response)))
			continue
		}
		request := integration.Request{
			IntegrationType:    integrationmodel.IntegrationTypeRepository,
			Resource:           "comment",
			ObjectType:         "comment",
			Operation:          "create",
			Repository:         ref.Repository,
			RepoProvided:       strings.TrimSpace(ref.Repository) != "",
			MergeRequestNumber: ref.Number,
			Body:               body,
			Text:               body,
		}
		if typ == "inline" {
			request.Operation = "reply"
			request.ThreadID = threadID
			request.ExternalID = threadID
		}
		integrationResponse, err := executor.Execute(ctx, request)
		if err != nil {
			if typ == "inline" && staleGitHubReviewThread(integrationResponse, err) {
				fallbackPublished, fallbackErr := e.publishReviewRemarkFallback(ctx, executor, ref, body)
				if fallbackErr == nil {
					if fallbackPublished {
						count++
						published = append(published, response)
					}
					continue
				}
				err = errors.Join(err, fmt.Errorf("fallback to pull request comment failed: %w", fallbackErr))
			}
			failures = append(failures, fmt.Errorf("publish review response %s: %w", reviewResponseTarget(response), err))
			continue
		}
		count++
		published = append(published, response)
	}

	return count, published, errors.Join(failures...)
}

func (e builtinOperationExecutor) resolveReviewThreads(ctx context.Context, responses []StructuredResponse) (int, error) {
	if len(responses) == 0 {
		return 0, nil
	}
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, err
	}

	resolved := 0
	seen := map[string]struct{}{}
	var failures []error
	for _, response := range responses {
		if reviewResponseType(response) != "inline" {
			continue
		}
		operation := reviewResponseThreadOperation(response)
		if operation == "" {
			continue
		}
		threadID := strings.TrimSpace(response.ThreadID)
		if threadID == "" {
			continue
		}
		if _, ok := seen[threadID]; ok {
			continue
		}
		seen[threadID] = struct{}{}
		integrationResponse, err := executor.Execute(ctx, integration.Request{
			IntegrationType: integrationmodel.IntegrationTypeRepository,
			Resource:        "comment",
			ObjectType:      "comment",
			Operation:       operation,
			ExternalID:      threadID,
			ThreadID:        threadID,
		})
		if err != nil {
			if staleGitHubReviewThread(integrationResponse, err) {
				continue
			}
			failures = append(failures, fmt.Errorf("resolve review response %s: %w", reviewResponseTarget(response), err))
			continue
		}
		resolved++
	}

	return resolved, errors.Join(failures...)
}

func (e builtinOperationExecutor) updateReviewRemarkThreads(ctx context.Context, comments []reviewRemarkComment) (int, error) {
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, err
	}

	updated := 0
	seen := map[string]struct{}{}
	for _, comment := range comments {
		threadID := strings.TrimSpace(comment.ThreadID)
		operation := reviewRemarkThreadOperation(comment.Status)
		if threadID == "" || operation == "" {
			continue
		}
		if _, ok := seen[threadID]; ok {
			continue
		}
		seen[threadID] = struct{}{}
		integrationResponse, err := executor.Execute(ctx, integration.Request{
			IntegrationType: integrationmodel.IntegrationTypeRepository,
			Resource:        "comment",
			ObjectType:      "comment",
			Operation:       operation,
			ExternalID:      threadID,
			ThreadID:        threadID,
		})
		if err != nil {
			if staleGitHubReviewThread(integrationResponse, err) {
				continue
			}
			return updated, err
		}
		updated++
	}

	return updated, nil
}

func (e builtinOperationExecutor) integrationExecutor() (integrationExecutor, error) {
	if e.service == nil || e.service.integrations == nil {
		return nil, fmt.Errorf("integration executor is not configured")
	}
	return e.service.integrations, nil
}

func pullRequestRefFromPublishMergeRequestInput(input publishMergeRequestInput) pullRequestRef {
	ref := pullRequestRefFromAssignment(publishMergeRequestAssignment(input))
	ref = mergePullRequestRefs(ref, input.ref)
	if strings.TrimSpace(ref.Head) == "" {
		assignment := publishMergeRequestAssignment(input)
		if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
			ref.Head = strconv.Itoa(assignment.CanonicalTask.Number)
		}
	}
	return ref
}

func publishMergeRequestAssignment(input publishMergeRequestInput) *ExecutionAssignment {
	if input.invocation.Assignment != nil {
		return assignmentFromInvocation(input.invocation)
	}
	return nil
}

func pullRequestRefFromPublishReviewRemarksInput(input publishMergeRequestInput) pullRequestRef {
	ref := pullRequestRefFromAssignment(publishReviewRemarksAssignment(input))
	ref = mergePullRequestRefs(ref, input.ref)
	if strings.TrimSpace(ref.Head) == "" {
		assignment := publishReviewRemarksAssignment(input)
		if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
			ref.Head = strconv.Itoa(assignment.CanonicalTask.Number)
		}
	}
	return ref
}

func publishReviewRemarksAssignment(input publishMergeRequestInput) *ExecutionAssignment {
	if input.invocation.Assignment != nil {
		return assignmentFromInvocation(input.invocation)
	}
	return nil
}

func pullRequestRefFromPublishReviewResponsesInput(input publishReviewResponsesInput) pullRequestRef {
	ref := pullRequestRefFromAssignment(publishReviewResponsesAssignment(input))
	ref = mergePullRequestRefs(ref, input.ref)
	if strings.TrimSpace(ref.Head) == "" {
		assignment := publishReviewResponsesAssignment(input)
		if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
			ref.Head = strconv.Itoa(assignment.CanonicalTask.Number)
		}
	}
	return ref
}

func pullRequestRefFromOperation(state *operationExecution, operation OperationSpec) pullRequestRef {
	ref := pullRequestRef{}
	ref.Repository, _ = operationMappingValue[string](state, operation.In["repository"])
	ref.Number, _ = operationMappingValue[int](state, operation.In["number"])
	ref.Base, _ = operationMappingValue[string](state, operation.In["base_ref"])
	ref.Head, _ = operationMappingValue[string](state, operation.In["head_ref"])
	ref.Title, _ = operationMappingValue[string](state, operation.In["title"])
	ref.Body, _ = operationMappingValue[string](state, operation.In["body"])
	ref.Draft, _ = operationMappingValue[bool](state, operation.In["draft"])
	return ref
}

func mergePullRequestRefs(base pullRequestRef, override pullRequestRef) pullRequestRef {
	if strings.TrimSpace(override.Repository) != "" {
		base.Repository = override.Repository
	}
	if override.Number > 0 {
		base.Number = override.Number
	}
	if strings.TrimSpace(override.Base) != "" {
		base.Base = override.Base
	}
	if strings.TrimSpace(override.Head) != "" {
		base.Head = override.Head
	}
	if strings.TrimSpace(override.Title) != "" {
		base.Title = override.Title
	}
	if strings.TrimSpace(override.Body) != "" {
		base.Body = override.Body
	}
	base.Draft = base.Draft || override.Draft
	return base
}

func publishReviewResponsesAssignment(input publishReviewResponsesInput) *ExecutionAssignment {
	if input.invocation.Assignment != nil {
		return assignmentFromInvocation(input.invocation)
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

func workplaceNameFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	return stableIdentifier(ref)
}

func mergeRequestSummary(pr integration.MergeRequest) string {
	return fmt.Sprintf("repository=%s number=%d state=%s base=%s head=%s url=%s", pr.Repository, pr.Number, pr.State, pr.BaseRef, pr.HeadRef, pr.URL)
}

func pullRequestTitleFromPublishMergeRequestInput(input publishMergeRequestInput, ref pullRequestRef) string {
	if strings.TrimSpace(ref.Title) != "" {
		return strings.TrimSpace(ref.Title)
	}
	assignment := publishMergeRequestAssignment(input)
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

func pullRequestBodyFromPublishMergeRequestInput(input publishMergeRequestInput) string {
	parts := []string{}
	assignment := publishMergeRequestAssignment(input)
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
			formatNamedLine("Состояние", remark.Status),
			formatNamedLine("Заголовок", remark.Title),
			remark.Body,
		}), "\n\n"))
		if body != "" {
			comments = append(comments, normalizeReviewRemarkCommentIdentifiers(reviewRemarkComment{
				Body:       body,
				Path:       strings.TrimSpace(remark.Path),
				Line:       remark.Line,
				Side:       strings.TrimSpace(remark.Side),
				ExternalID: strings.TrimSpace(remark.ExternalID),
				ThreadID:   strings.TrimSpace(remark.ThreadID),
				Status:     strings.TrimSpace(remark.Status),
			}))
		}
	}
	return comments
}

func reviewRemarkThreadOperation(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "open", "unresolved", "reopened":
		return "unresolve"
	case "resolved", "fixed", "done", "ok", "closed":
		return "resolve"
	default:
		return ""
	}
}

func reviewRemarkCommentOperation(comment reviewRemarkComment) string {
	if strings.TrimSpace(comment.ThreadID) != "" {
		return "reply"
	}
	return "create"
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

func reviewResponsesFromOutput(output *StructuredOutput, remarks []integration.ReviewRemark) []StructuredResponse {
	if output == nil {
		return nil
	}
	index := newReviewRemarkIndex(remarks)
	aliases, _ := reviewRemarkAliases(output, index)
	responses := append([]StructuredResponse(nil), output.ReviewResponses...)
	for responseIndex := range responses {
		enrichReviewResponse(&responses[responseIndex], aliases, index)
	}
	for _, remark := range output.Remarks {
		if strings.TrimSpace(remark.Answer) == "" && strings.TrimSpace(remark.Resolution) == "" {
			continue
		}
		response := StructuredResponse{
			RemarkID: strings.TrimSpace(remark.ID),
			Status:   firstNonEmptyTrimmed(remark.Status, "resolved"),
			Summary:  strings.TrimSpace(remark.Resolution),
			Body:     strings.TrimSpace(remark.Answer),
		}
		enrichReviewResponse(&response, aliases, index)
		responses = append(responses, response)
	}
	return responses
}

func reviewRemarkProjectID(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Идентификатор:") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(line, "Идентификатор:"))
		if !strings.HasPrefix(id, "remark-") || len(id) == len("remark-") {
			continue
		}
		valid := true
		for _, char := range id[len("remark-"):] {
			if char < '0' || char > '9' {
				valid = false
				break
			}
		}
		if valid {
			return id
		}
	}
	return ""
}

type reviewRemarkIndex struct {
	external       map[string][]integration.ReviewRemark
	project        map[string][]integration.ReviewRemark
	stable         map[string][]integration.ReviewRemark
	stableReserved map[string][]integration.ReviewRemark
	responseIDs    []reviewRemarkResponseIDEntry
}

type reviewRemarkResponseIDEntry struct {
	remark integration.ReviewRemark
	id     string
}

func newReviewRemarkIndex(remarks []integration.ReviewRemark) reviewRemarkIndex {
	index := reviewRemarkIndex{
		external:       make(map[string][]integration.ReviewRemark),
		project:        make(map[string][]integration.ReviewRemark),
		stable:         make(map[string][]integration.ReviewRemark),
		stableReserved: make(map[string][]integration.ReviewRemark),
	}
	for _, remark := range remarks {
		if id := strings.TrimSpace(remark.ExternalID); id != "" {
			index.external[id] = append(index.external[id], remark)
		}
		if id := reviewRemarkProjectID(remark.Body); id != "" {
			index.project[id] = append(index.project[id], remark)
		}
		if id := strings.TrimSpace(remark.ExternalID); id != "" {
			index.stableReserved["remark-ref:"+id] = append(index.stableReserved["remark-ref:"+id], remark)
		}
	}
	// Сначала используем базовые ссылки как рабочие резервирования для
	// выбора идентификаторов, затем оставляем только те устойчивые ссылки,
	// которые действительно выдаёт canonicalReviewRemarks.
	index.stable = index.stableReserved
	issuedStable := make(map[string][]integration.ReviewRemark)
	for _, remark := range remarks {
		id := reviewRemarkResponseID(remark, index)
		index.responseIDs = append(index.responseIDs, reviewRemarkResponseIDEntry{remark: remark, id: id})
		if id != "" && strings.HasPrefix(id, "remark-ref:") {
			issuedStable[id] = append(issuedStable[id], remark)
		}
	}
	index.stable = issuedStable
	return index
}

func (index reviewRemarkIndex) resolve(id string) (integration.ReviewRemark, bool) {
	id = strings.TrimSpace(id)
	externalMatches := index.external[id]
	projectMatches := index.project[id]
	stableMatches := index.stableMatches(id)
	if len(stableMatches) == 1 && len(projectMatches) == 0 {
		if len(externalMatches) == 0 || (len(externalMatches) == 1 && sameReviewRemark(stableMatches[0], externalMatches[0])) {
			return stableMatches[0], true
		}
		return integration.ReviewRemark{}, false
	}
	if len(stableMatches) > 1 {
		return integration.ReviewRemark{}, false
	}
	if len(stableMatches) == 1 && len(projectMatches) > 0 {
		return integration.ReviewRemark{}, false
	}
	if len(externalMatches) == 1 {
		if len(projectMatches) == 0 || sameReviewRemark(externalMatches[0], projectMatches[0]) {
			return externalMatches[0], true
		}
		return integration.ReviewRemark{}, false
	}
	if len(externalMatches) == 0 && len(projectMatches) == 1 {
		return projectMatches[0], true
	}
	return integration.ReviewRemark{}, false
}

func (index reviewRemarkIndex) hasAmbiguous(id string) bool {
	id = strings.TrimSpace(id)
	externalMatches := index.external[id]
	projectMatches := index.project[id]
	stableMatches := index.stableMatches(id)
	if len(stableMatches) == 0 {
		stableMatches = index.stableReserved[id]
	}
	if len(stableMatches) > 1 {
		return true
	}
	if len(externalMatches) > 1 || len(projectMatches) > 1 {
		return true
	}
	if len(stableMatches) == 1 && len(externalMatches) == 1 && !sameReviewRemark(stableMatches[0], externalMatches[0]) {
		return true
	}
	return len(externalMatches) == 1 && len(projectMatches) == 1 && !sameReviewRemark(externalMatches[0], projectMatches[0])
}

func (index reviewRemarkIndex) stableMatches(id string) []integration.ReviewRemark {
	return index.stable[id]
}

func sameReviewRemark(left, right integration.ReviewRemark) bool {
	return strings.TrimSpace(left.ExternalID) == strings.TrimSpace(right.ExternalID) &&
		strings.TrimSpace(left.ReplyToID) == strings.TrimSpace(right.ReplyToID) &&
		strings.TrimSpace(left.Body) == strings.TrimSpace(right.Body) &&
		strings.TrimSpace(left.Path) == strings.TrimSpace(right.Path) &&
		left.Line == right.Line &&
		strings.TrimSpace(left.Side) == strings.TrimSpace(right.Side)
}

func reviewRemarkAliases(output *StructuredOutput, index reviewRemarkIndex) (map[string][]integration.ReviewRemark, map[string]bool) {
	aliases := make(map[string][]integration.ReviewRemark)
	conflicts := make(map[string]bool)
	if output == nil {
		return aliases, conflicts
	}
	for _, remark := range output.Remarks {
		id := strings.TrimSpace(remark.ID)
		externalID := strings.TrimSpace(remark.ExternalID)
		if id == "" || externalID == "" {
			continue
		}
		if index.hasAmbiguous(id) {
			conflicts[id] = true
			continue
		}
		if index.hasAmbiguous(externalID) {
			conflicts[id] = true
			continue
		}
		canonical, ok := index.resolve(externalID)
		if !ok {
			continue
		}
		aliases[id] = append(aliases[id], canonical)
	}
	for id, matches := range aliases {
		if len(matches) > 1 {
			conflicts[id] = true
			continue
		}
		if canonical, ok := index.resolve(id); ok && strings.TrimSpace(canonical.ExternalID) != strings.TrimSpace(matches[0].ExternalID) {
			conflicts[id] = true
		}
	}
	for id := range conflicts {
		delete(aliases, id)
	}
	return aliases, conflicts
}

func enrichReviewResponse(response *StructuredResponse, aliases map[string][]integration.ReviewRemark, index reviewRemarkIndex) {
	if response == nil {
		return
	}
	remark, ok := index.resolve(response.RemarkID)
	if !ok {
		matches := aliases[strings.TrimSpace(response.RemarkID)]
		if len(matches) == 1 {
			remark, ok = matches[0], true
		}
	}
	if !ok {
		return
	}
	if canonicalType := reviewResponseTypeFromRemark(remark); canonicalType != "" {
		response.Type = canonicalType
		if canonicalType == "inline" {
			response.ThreadID = strings.TrimSpace(remark.ReplyToID)
		} else {
			response.ThreadID = ""
		}
		return
	}
	if strings.TrimSpace(response.Type) == "" && strings.TrimSpace(response.ThreadID) != "" {
		return
	}
	if strings.TrimSpace(response.Type) == "" {
		response.Type = reviewResponseTypeFromRemark(remark)
	}
	if strings.TrimSpace(response.ThreadID) == "" {
		response.ThreadID = strings.TrimSpace(remark.ReplyToID)
	}
}

func validateReviewResponseTargets(responses []StructuredResponse, remarks []integration.ReviewRemark, output *StructuredOutput) error {
	index := newReviewRemarkIndex(remarks)
	aliases, aliasConflicts := reviewRemarkAliases(output, index)
	var failures []error
	for responseIndex, response := range responses {
		if reviewResponseType(response) == "local" {
			continue
		}
		id := strings.TrimSpace(response.RemarkID)
		if aliasConflicts[id] {
			failures = append(failures, fmt.Errorf("review response %d (remark_id %q): canonical alias is conflicting or ambiguous; external publication is not allowed", responseIndex, id))
			continue
		}
		if index.hasAmbiguous(id) {
			failures = append(failures, fmt.Errorf("review response %d (remark_id %q): canonical remark is ambiguous; external publication is not allowed", responseIndex, id))
			continue
		}
		remark, ok := index.resolve(id)
		if !ok {
			if matches := aliases[id]; len(matches) == 1 {
				remark, ok = matches[0], true
			}
		}
		if ok {
			if reviewResponseType(response) == "local" {
				continue
			}
			if reviewRemarkResponseType := reviewResponseTypeFromRemark(remark); reviewRemarkResponseType == "" {
				failures = append(failures, fmt.Errorf("review response %d (remark_id %q): canonical remark has unknown response type; external publication is not allowed", responseIndex, id))
			}
			continue
		}
		reason := "unknown"
		if index.hasAmbiguous(id) {
			reason = "ambiguous"
		}
		failures = append(failures, fmt.Errorf("review response %d (remark_id %q): canonical remark is %s; external publication is not allowed", responseIndex, id, reason))
	}
	return errors.Join(failures...)
}

func reviewResponseTypeFromRemark(remark integration.ReviewRemark) string {
	typ := strings.ToLower(strings.TrimSpace(remark.Type))
	switch typ {
	case "inline", "comment", "local":
		return typ
	}
	if strings.TrimSpace(remark.ReplyToID) != "" {
		return "inline"
	}
	if strings.HasPrefix(strings.TrimSpace(remark.ExternalID), "PRRC_") {
		return "inline"
	}
	if strings.TrimSpace(remark.URL) != "" {
		return "comment"
	}
	return ""
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

func reviewResponseThreadOperation(response StructuredResponse) string {
	status := strings.ToLower(strings.TrimSpace(response.Status))
	switch {
	case isResolvedReviewResponse(response):
		return "resolve"
	case status == "open" || status == "unresolved" || status == "reopened":
		return "unresolve"
	default:
		return ""
	}
}

func validateReviewResponseThreadIDs(responses []StructuredResponse) error {
	for index, response := range responses {
		if reviewResponseType(response) != "inline" || (strings.TrimSpace(reviewResponseCommentBody(response)) == "" && !isResolvedReviewResponse(response)) {
			continue
		}
		if strings.TrimSpace(response.ThreadID) == "" {
			return fmt.Errorf("review response %d thread_id is required", index)
		}
		if strings.HasPrefix(strings.TrimSpace(response.ThreadID), "PRRC_") {
			return fmt.Errorf("review response %d thread_id %q is a review comment identifier; PullRequestReviewThread identifier is required", index, strings.TrimSpace(response.ThreadID))
		}
	}
	return nil
}

func validateReviewResponses(responses []StructuredResponse) error {
	var failures []error
	for index, response := range responses {
		typ := reviewResponseType(response)
		if typ == "local" {
			continue
		}
		switch typ {
		case "inline":
			if strings.TrimSpace(response.ThreadID) == "" && (strings.TrimSpace(reviewResponseCommentBody(response)) != "" || isResolvedReviewResponse(response)) {
				failures = append(failures, fmt.Errorf("review response %d (remark_id %q): thread_id is required; canonical external_id is missing, unknown, or ambiguous", index, strings.TrimSpace(response.RemarkID)))
			}
			if strings.HasPrefix(strings.TrimSpace(response.ThreadID), "PRRC_") {
				failures = append(failures, fmt.Errorf("review response %d (remark_id %q): thread_id %q is a review comment identifier; PullRequestReviewThread identifier is required", index, strings.TrimSpace(response.RemarkID), strings.TrimSpace(response.ThreadID)))
			}
		case "comment":
			// Общий комментарий формируется как новая связанная запись и thread_id не требует.
		default:
			failures = append(failures, fmt.Errorf("review response %d (remark_id %q): type is unsupported or cannot be restored from canonical external_id/thread_id", index, strings.TrimSpace(response.RemarkID)))
		}
	}
	return errors.Join(failures...)
}

func reviewResponseType(response StructuredResponse) string {
	typ := strings.ToLower(strings.TrimSpace(response.Type))
	if typ == "" {
		if strings.TrimSpace(response.ThreadID) != "" {
			return "inline"
		}
		return "unknown"
	}
	return typ
}

func reviewResponseCountByType(responses []StructuredResponse, typ string) int {
	count := 0
	for _, response := range responses {
		if reviewResponseType(response) == typ {
			count++
		}
	}
	return count
}

func reviewResponseTarget(response StructuredResponse) string {
	return firstNonEmptyTrimmed(response.RemarkID, response.ID, "без-идентификатора")
}

func formatNamedLine(name string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return name + ": " + value
}
