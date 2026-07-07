package execution

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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

func (e builtinOperationExecutor) loadPullRequest(ctx context.Context, state *operationExecution, name string) error {
	ref := pullRequestRefFromState(state)
	if ref.Number <= 0 {
		return e.failIntegrationOperation(ctx, state, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Контур интеграции недоступен.", err, "integration_unavailable")
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
		return e.failIntegrationOperation(ctx, state, name, "Запрос на слияние не получен.", err, "pull_request_load_failed")
	}

	mergeRequest, ok := mergeRequestFromIntegrationResponse(response)
	if !ok {
		return e.failIntegrationOperation(ctx, state, name, "Контур интеграции не вернул запрос на слияние.", fmt.Errorf("integration response does not include merge request"), "pull_request_missing")
	}

	state.pullRequest = &mergeRequest
	applyPullRequestToState(state, mergeRequest)
	state.tracker.completeIO(name, fmt.Sprintf("repository=%s number=%d", mergeRequest.Repository, mergeRequest.Number), mergeRequestSummary(mergeRequest), "Запрос на слияние получен через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) loadReviewRemarks(ctx context.Context, state *operationExecution, name string, required bool) error {
	ref := pullRequestRefFromState(state)
	if ref.Number <= 0 {
		return e.failOrSkipIntegrationOperation(ctx, state, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required", required)
	}

	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failOrSkipIntegrationOperation(ctx, state, name, "Контур интеграции недоступен.", err, "integration_unavailable", required)
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
		return e.failOrSkipIntegrationOperation(ctx, state, name, "Замечания ревизии не получены.", err, "review_remarks_load_failed", required)
	}

	state.reviewRemarks = append([]integration.ReviewRemark(nil), response.ReviewRemarks...)
	input := ensureExecutionStructuredInput(state)
	input.ReviewRemarks = mergeStructuredRemarks(input.ReviewRemarks, structuredRemarksFromIntegration(response.ReviewRemarks))
	input.OperationalContext = append(input.OperationalContext, StructuredContext{
		Title: "Сведения о замечаниях ревизии",
		Body:  fmt.Sprintf("repository=%s\npull_request=%d\nremarks=%d", ref.Repository, ref.Number, len(response.ReviewRemarks)),
	})
	state.tracker.completeIO(name, fmt.Sprintf("repository=%s number=%d", ref.Repository, ref.Number), fmt.Sprintf("review-remarks=%d", len(response.ReviewRemarks)), "Замечания ревизии получены через контур интеграции.")
	return nil
}

func (e builtinOperationExecutor) publishMergeRequest(ctx context.Context, state *operationExecution, name string) error {
	ref := pullRequestRefFromState(state)
	if ref.Number > 0 {
		state.tracker.skip(name, fmt.Sprintf("Запрос на слияние уже задан: number=%d.", ref.Number))
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
		ref.Title = pullRequestTitle(state)
	}
	ref.Body = pullRequestBodyForRef(state, name, ref)

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
	state.result.Summary = joinExecutionSummaries(state.result.Summary, summary)
	state.tracker.completeIO(name, fmt.Sprintf("repository=%s base=%s head=%s", ref.Repository, ref.Base, ref.Head), summary, "Запрос на слияние зафиксирован через контур интеграции.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
	return nil
}

func (e builtinOperationExecutor) defaultMergeRequestBase(ctx context.Context, state *operationExecution) (string, error) {
	repoRoot := ""
	if state != nil {
		repoRoot = strings.TrimSpace(state.workplace.RepositoryRoot)
		if repoRoot == "" {
			repoRoot = strings.TrimSpace(state.workplace.Name)
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

func (e builtinOperationExecutor) publishReviewRemarks(ctx context.Context, state *operationExecution, name string) error {
	ref := pullRequestRefFromState(state)
	if ref.Number <= 0 {
		return e.failIntegrationOperation(ctx, state, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	comments := reviewRemarkComments(state, name)
	if len(comments) == 0 {
		state.tracker.skip(name, "Структурированный вывод не содержит замечаний или заключения для записи.")
		return nil
	}

	count, err := e.publishPullRequestComments(ctx, state, ref, comments)
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Замечания ревизии не записаны.", err, "review_remarks_publish_failed")
	}

	summary := fmt.Sprintf("review-remarks-published=%d", count)
	state.result.Summary = joinExecutionSummaries(state.result.Summary, summary)
	state.tracker.completeIO(name, fmt.Sprintf("repository=%s number=%d", ref.Repository, ref.Number), summary, "Замечания ревизии записаны через контур интеграции.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
	return nil
}

func (e builtinOperationExecutor) publishReviewResponses(ctx context.Context, state *operationExecution, name string) error {
	ref := pullRequestRefFromState(state)
	if ref.Number <= 0 {
		return e.failIntegrationOperation(ctx, state, name, "Номер запроса на слияние не задан.", fmt.Errorf("pull request number is required"), "pull_request_number_required")
	}

	responses := reviewResponsesFromOutput(state.result.StructuredOutput)
	if len(responses) == 0 {
		state.tracker.skip(name, "Структурированный вывод не содержит ответов на замечания.")
		return nil
	}

	count, err := e.publishReviewResponseComments(ctx, state, name, ref, responses)
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Ответы на замечания не записаны.", err, "review_responses_publish_failed")
	}

	resolved, err := e.resolveReviewThreads(ctx, state, responses)
	if err != nil {
		return e.failIntegrationOperation(ctx, state, name, "Ответы записаны, но часть цепочек обсуждения не закрыта.", err, "review_thread_resolve_failed")
	}

	summary := fmt.Sprintf("review-responses-published=%d review-threads-resolved=%d", count, resolved)
	state.result.Summary = joinExecutionSummaries(state.result.Summary, summary)
	state.tracker.completeIO(name, fmt.Sprintf("repository=%s number=%d", ref.Repository, ref.Number), summary, "Ответы на замечания записаны через контур интеграции.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
	return nil
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

func (e builtinOperationExecutor) publishReviewResponseComments(ctx context.Context, state *operationExecution, operationName string, ref pullRequestRef, responses []StructuredResponse) (int, error) {
	executor, err := e.integrationExecutor()
	if err != nil {
		return 0, err
	}

	count := 0
	policy, _ := policyForStatePublication(state, publicationTargetReviewResponse, operationName)
	for _, response := range responses {
		body := reviewResponseCommentBodyWithPolicy(response, policy)
		if body == "" {
			continue
		}
		threadID := reviewThreadIDForResponse(state.reviewRemarks, response)
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

func (e builtinOperationExecutor) resolveReviewThreads(ctx context.Context, state *operationExecution, responses []StructuredResponse) (int, error) {
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
		threadID := reviewThreadIDForResponse(state.reviewRemarks, response)
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
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, err)
	return err
}

func (e builtinOperationExecutor) failOrSkipIntegrationOperation(ctx context.Context, state *operationExecution, name string, summary string, err error, code string, required bool) error {
	if required {
		return e.failIntegrationOperation(ctx, state, name, summary, err, code)
	}

	state.tracker.skip(name, joinExecutionSummaries(summary, strings.TrimSpace(err.Error())))
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
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

func pullRequestBody(state *operationExecution, operationName string) string {
	if policy, ok := policyForStatePublication(state, publicationTargetMergeRequestDescription, operationName); ok && policy.TaskLinkOnly {
		if body := compactPullRequestBody(state); body != "" {
			return body
		}
	}
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

func pullRequestBodyForRef(state *operationExecution, operationName string, ref pullRequestRef) string {
	if policy, ok := policyForStatePublication(state, publicationTargetMergeRequestDescription, operationName); ok && policy.TaskLinkOnly {
		if body := compactPullRequestBody(state); body != "" {
			return body
		}
	}
	if body := strings.TrimSpace(ref.Body); body != "" {
		return body
	}
	return pullRequestBody(state, operationName)
}

func compactPullRequestBody(state *operationExecution) string {
	if state == nil || state.assignment == nil || state.assignment.CanonicalTask == nil {
		return ""
	}
	task := state.assignment.CanonicalTask
	parts := []string{}
	if task.Number > 0 {
		parts = append(parts, fmt.Sprintf("Задача: #%d", task.Number))
	}
	if strings.TrimSpace(task.URL) != "" {
		parts = append(parts, "Ссылка на задачу: "+strings.TrimSpace(task.URL))
	}
	return strings.Join(parts, "\n")
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

func reviewRemarkComments(state *operationExecution, operationName string) []reviewRemarkComment {
	var output *StructuredOutput
	if state != nil {
		output = state.result.StructuredOutput
	}
	if output == nil {
		return nil
	}
	policy, hasPolicy := policyForStatePublication(state, publicationTargetReviewRemark, operationName)
	if len(output.Remarks) == 0 {
		if output.Conclusion == nil {
			return nil
		}
		heading := "## Заключение ревизии"
		if hasPolicy && policy.NoHeading {
			heading = ""
		}
		status := output.Conclusion.Status
		if hasPolicy && policy.HideStatus {
			status = ""
		}
		body := strings.TrimSpace(strings.Join(nonEmptyParts([]string{
			heading,
			status,
			output.Conclusion.Summary,
			output.Conclusion.Body,
		}), "\n\n"))
		if body == strings.TrimSpace(heading) {
			return nil
		}
		return []reviewRemarkComment{{Body: body}}
	}

	comments := make([]reviewRemarkComment, 0, len(output.Remarks))
	for _, remark := range output.Remarks {
		heading := "## Замечание ревизии"
		if hasPolicy && (policy.NoHeading || policy.OptionalHeading && strings.TrimSpace(remark.Title) == "") {
			heading = ""
		}
		title := formatNamedLine("Заголовок", remark.Title)
		if hasPolicy && policy.OptionalHeading {
			title = strings.TrimSpace(remark.Title)
		}
		body := strings.TrimSpace(strings.Join(nonEmptyParts([]string{
			heading,
			formatNamedLine("Идентификатор", remark.ID),
			formatNamedLine("Критичность", remark.Severity),
			formatNamedLine("Тип", remark.Type),
			title,
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
	return reviewResponseCommentBodyWithPolicy(response, textPublicationPolicy{})
}

func reviewResponseCommentBodyWithPolicy(response StructuredResponse, policy textPublicationPolicy) string {
	heading := "## Ответ на замечание ревизии"
	if policy.NoHeading {
		heading = ""
	}
	status := formatNamedLine("Состояние", response.Status)
	if policy.HideStatus {
		status = ""
	}
	if len(nonEmptyParts([]string{status, response.Summary, response.Body})) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(nonEmptyParts([]string{
		heading,
		formatNamedLine("Замечание", response.RemarkID),
		status,
		response.Summary,
		response.Body,
	}), "\n\n"))
}

func policyForStatePublication(state *operationExecution, target string, steps ...string) (textPublicationPolicy, bool) {
	if state == nil {
		return textPublicationPolicy{}, false
	}
	allSteps := normalizePolicyList(append([]string{state.action.Name}, steps...))
	requestedStepSet := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		step = strings.TrimSpace(strings.ToLower(step))
		if step == "" {
			continue
		}
		requestedStepSet[step] = struct{}{}
	}
	requestedSteps := make(map[string]struct{}, len(allSteps))
	for _, step := range allSteps {
		requestedSteps[step] = struct{}{}
	}

	operationNames := make(map[string]struct{}, len(state.action.Operations))
	operationKindsByAnyMatch := make(map[string]struct{}, len(state.action.Operations))
	operationKindsByNameMatch := make(map[string]struct{}, len(state.action.Operations))
	actionName := strings.TrimSpace(strings.ToLower(state.action.Name))
	if actionName != "" {
		operationNames[actionName] = struct{}{}
	}
	for _, operation := range state.action.Operations {
		name := strings.TrimSpace(strings.ToLower(operationResultName(operation)))
		if name == "" {
			continue
		}
		kind := strings.TrimSpace(strings.ToLower(string(operationKind(operation))))
		_, requestedByName := requestedStepSet[name]
		_, requestedByKind := requestedStepSet[kind]
		if !requestedByName && !requestedByKind {
			continue
		}
		if requestedByKind {
			operationKindsByAnyMatch[kind] = struct{}{}
		}
		if requestedByName {
			operationNames[name] = struct{}{}
			operationKindsByNameMatch[kind] = struct{}{}
		}
		operationKindsByAnyMatch[kind] = struct{}{}
	}

	specificSteps := make([]string, 0, len(allSteps))
	for _, step := range allSteps {
		if _, ok := operationNames[step]; ok {
			specificSteps = append(specificSteps, step)
		}
	}
	if len(specificSteps) > 0 {
		if policy, ok := policyForPublication(state.policies, target, specificSteps...); ok {
			return policy, true
		}
		kindsForFallback := operationKindsByAnyMatch
		if len(operationKindsByNameMatch) > 0 {
			kindsForFallback = operationKindsByNameMatch
		}
		specificOperationKinds := make([]string, 0, len(kindsForFallback))
		for step := range kindsForFallback {
			specificOperationKinds = append(specificOperationKinds, step)
		}
		if len(specificOperationKinds) > 0 {
			if policy, ok := policyForPublication(state.policies, target, specificOperationKinds...); ok {
				return policy, true
			}
		}
		return textPublicationPolicy{}, false
	}

	return policyForPublication(state.policies, target, allSteps...)
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
