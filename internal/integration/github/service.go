package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rasungatullin/progress/internal/integration/model"
)

type ghRunner interface {
	RunAuthStatus(context.Context) (CommandResult, resolvedConfig, error)
	RunRepoView(context.Context, string) (CommandResult, resolvedConfig, error)
	RunIssueView(context.Context, string, int) (CommandResult, resolvedConfig, error)
	RunIssueList(context.Context, string, IssueListRequest) (CommandResult, resolvedConfig, error)
	RunIssueComments(context.Context, string, int) (CommandResult, resolvedConfig, error)
	RunIssueCommentCreate(context.Context, string, int, string) (CommandResult, resolvedConfig, error)
	RunIssueLabelsAdd(context.Context, string, int, []string) (CommandResult, resolvedConfig, error)
	RunIssueLabelsRemove(context.Context, string, int, []string) (CommandResult, resolvedConfig, error)
	RunPRView(context.Context, string, int) (CommandResult, resolvedConfig, error)
	RunPRList(context.Context, string, PRListRequest) (CommandResult, resolvedConfig, error)
	RunPRCreate(context.Context, string, PRCreateRequest) (CommandResult, resolvedConfig, error)
	RunPRReviewThreads(context.Context, string, int) (CommandResult, resolvedConfig, error)
	RunPRCommentCreate(context.Context, string, int, PRCommentCreateRequest) (CommandResult, resolvedConfig, error)
	RunPRReviewThreadReply(context.Context, PRReviewThreadReplyRequest) (CommandResult, resolvedConfig, error)
	RunPRReviewThreadResolve(context.Context, string) (CommandResult, resolvedConfig, error)
}

type Service struct {
	runner ghRunner
}

type ghRepoView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
	DefaultBranchRef *struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

type ghIssueView struct {
	Number    int            `json:"number"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	State     string         `json:"state"`
	URL       string         `json:"url"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
	Labels    []ghIssueLabel `json:"labels"`
	Assignees []ghIssueUser  `json:"assignees"`
	Author    *ghIssueUser   `json:"author"`
}

type ghIssueUser struct {
	Login    string `json:"login"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	URL      string `json:"url"`
	HTMLURL  string `json:"html_url"`
	IsBot    bool   `json:"isBot"`
	IsActive bool   `json:"isActive"`
}

type ghIssueComment struct {
	ID        int          `json:"id"`
	Body      string       `json:"body"`
	URL       string       `json:"url"`
	HTMLURL   string       `json:"html_url"`
	CreatedAt string       `json:"created_at"`
	UpdatedAt string       `json:"updated_at"`
	User      *ghIssueUser `json:"user"`
}

type ghPRView struct {
	Number         int            `json:"number"`
	Title          string         `json:"title"`
	Body           string         `json:"body"`
	State          string         `json:"state"`
	Mergeable      any            `json:"mergeable"`
	MergeState     string         `json:"mergeStateStatus"`
	Author         *ghIssueUser   `json:"author"`
	Labels         []ghIssueLabel `json:"labels"`
	ReviewDecision string         `json:"reviewDecision"`
	BaseRefName    string         `json:"baseRefName"`
	HeadRefName    string         `json:"headRefName"`
	URL            string         `json:"url"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
}

type ghIssueLabel struct {
	Name string `json:"name"`
}

type ghPRReviewThreadsResponse struct {
	Data struct {
		Repository struct {
			PullRequest *struct {
				ReviewThreads struct {
					Nodes []ghPRReviewThread `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

type ghPRReviewThread struct {
	ID         string             `json:"id"`
	IsResolved bool               `json:"isResolved"`
	IsOutdated bool               `json:"isOutdated"`
	Path       string             `json:"path"`
	Line       int                `json:"line"`
	Comments   ghPRReviewComments `json:"comments"`
}

type ghPRReviewComments struct {
	Nodes []ghPRReviewComment `json:"nodes"`
}

type ghPRReviewComment struct {
	ID        string       `json:"id"`
	Body      string       `json:"body"`
	URL       string       `json:"url"`
	Path      string       `json:"path"`
	Line      int          `json:"line"`
	Author    *ghIssueUser `json:"author"`
	CreatedAt string       `json:"createdAt"`
	UpdatedAt string       `json:"updatedAt"`
}

type ghPRReviewThreadCreateResponse struct {
	Data struct {
		AddPullRequestReviewThread struct {
			Thread ghPRReviewThread `json:"thread"`
		} `json:"addPullRequestReviewThread"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type ghPRReviewThreadResolveResponse struct {
	Data struct {
		ResolveReviewThread struct {
			Thread struct {
				ID         string `json:"id"`
				IsResolved bool   `json:"isResolved"`
			} `json:"thread"`
		} `json:"resolveReviewThread"`
	} `json:"data"`
}

type ghPRReviewThreadReplyResponse struct {
	Data struct {
		AddPullRequestReviewThreadReply struct {
			Comment ghPRReviewComment `json:"comment"`
		} `json:"addPullRequestReviewThreadReply"`
	} `json:"data"`
}

func NewService() *Service {
	return &Service{runner: NewRunner()}
}

func NewServiceWithConfig(config model.IntegrationSystemConfig) *Service {
	if strings.EqualFold(strings.TrimSpace(config.Transport), "api") {
		return &Service{runner: NewAPIRunnerWithSystemConfig(config)}
	}
	return &Service{runner: NewRunnerWithSystemConfig(config)}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: firstNonEmpty(req.IntegrationType, integrationTypeForRequest(req)),
		System:          "github",
		Resource:        req.Resource,
		ObjectType:      firstNonEmpty(req.ObjectType, req.Resource),
		Operation:       req.Operation,
	}

	switch {
	case req.Resource == "auth" && req.Operation == "status":
		return s.executeAuthStatus(ctx, response)
	case isRepositoryRequest(req) && req.Operation == "get":
		return s.executeRepoGet(ctx, response, req)
	case isIssueRequest(req) && req.Operation == "get":
		return s.executeIssueGet(ctx, response, req)
	case isIssueRequest(req) && (req.Operation == "list" || req.Operation == "search"):
		return s.executeIssueList(ctx, response, req)
	case isIssueRequest(req) && req.Operation == "comments":
		return s.executeIssueComments(ctx, response, req)
	case isIssueCommentRequest(req) && req.Operation == "create":
		return s.executeIssueCommentCreate(ctx, response, req)
	case isIssueLabelRequest(req) && req.Operation == "add":
		return s.executeIssueLabelsChange(ctx, response, req, true)
	case isIssueLabelRequest(req) && req.Operation == "remove":
		return s.executeIssueLabelsChange(ctx, response, req, false)
	case isPullRequestRequest(req) && req.Operation == "get":
		return s.executePRGet(ctx, response, req)
	case isPullRequestRequest(req) && (req.Operation == "list" || req.Operation == "search"):
		return s.executePRList(ctx, response, req)
	case isPullRequestRequest(req) && req.Operation == "create":
		return s.executePRCreate(ctx, response, req)
	case isPullRequestRequest(req) && req.Operation == "comments":
		return s.executePRComments(ctx, response, req)
	case isPullRequestCommentRequest(req) && req.Operation == "list":
		return s.executePRComments(ctx, response, req)
	case isPullRequestCommentRequest(req) && req.Operation == "create":
		return s.executePRCommentCreate(ctx, response, req)
	case isPullRequestCommentRequest(req) && req.Operation == "reply":
		return s.executePRCommentReply(ctx, response, req)
	case isPullRequestCommentRequest(req) && req.Operation == "resolve":
		return s.executePRCommentResolve(ctx, response, req)
	default:
		err := &Error{
			Code:    ErrorCodeUnsupportedOperation,
			Message: fmt.Sprintf("GitHub integration does not support %s %s at this stage", req.Resource, req.Operation),
		}
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Message}
		return response, err
	}
}

func (s *Service) executePRCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := normalizeRepository(req.Repository)
	if err != nil {
		status := prErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Base, req.Head, req.Title, req.Body, req.Draft)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "pull request create request rejected before invoking gh")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	prRequest, err := normalizePRCreateRequest(PRCreateRequest{Base: req.Base, Head: req.Head, Title: req.Title, Body: req.Body, Draft: req.Draft})
	if err != nil {
		status := prErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, req.Base, req.Head, req.Title, req.Body, req.Draft)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "pull request create request rejected before invoking gh")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	result, config, err := s.runner.RunPRCreate(ctx, repository, prRequest)
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := prErrorStatus(config, result, repository, prRequest.Base, prRequest.Head, prRequest.Title, prRequest.Body, prRequest.Draft)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = prStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh pr create failed before returning a pull request payload")
			response.PullRequestStatus = &status
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh pr create failed before returning a pull request payload")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			status.Diagnostics = append(status.Diagnostics, "gh pr create reported that no GitHub login is configured")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeAuthRequired, Message: status.Message, Result: result}
		case isPRCreateNoCommits(result):
			status.State = ErrorCodeInvalidRequest
			status.Message = fmt.Sprintf("GitHub pull request cannot be created because %s has no commits ahead of %s", prRequest.Head, prRequest.Base)
			status.Diagnostics = append(status.Diagnostics, "gh pr create reported no commits between the requested branches")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: result}
		case isRepoNotFound(result), isPRCreateBranchNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub repository or branch not found for pull request creation: %s %s -> %s", repository, prRequest.Head, prRequest.Base)
			status.Diagnostics = append(status.Diagnostics, "gh pr create could not resolve the requested repository or branch")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeNotFound, Message: status.Message, Result: result}
		case isPRAlreadyExists(result):
			status.State = ErrorCodeAlreadyExists
			status.Message = fmt.Sprintf("GitHub pull request already exists for %s %s -> %s", repository, prRequest.Head, prRequest.Base)
			status.Diagnostics = append(status.Diagnostics, "gh pr create reported an existing pull request between the requested branches")
			status.URL = extractFirstURL(result.Stdout + "\n" + result.Stderr)
			status.Number = pullRequestNumberFromURL(status.URL)
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeAlreadyExists, Message: status.Message, Result: result}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			status.Diagnostics = append(status.Diagnostics, "gh pr create exited with a non-zero code")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
		}
	}

	status.URL = extractFirstURL(result.Stdout)
	status.Number = pullRequestNumberFromURL(status.URL)
	if status.URL == "" || status.Number <= 0 {
		status.State = StateExternalFailure
		status.Message = "unexpected GitHub CLI response: missing pull request URL or number"
		status.Diagnostics = append(status.Diagnostics, "gh pr create returned a success exit code without a parseable pull request URL")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
	}

	status.State = "OPEN"
	status.Message = fmt.Sprintf("GitHub pull request created for %s %s -> %s", repository, prRequest.Head, prRequest.Base)
	status.Diagnostics = append(status.Diagnostics, "gh pr create completed successfully")
	response.PullRequestStatus = &status
	response.OperationResult = &model.OperationResult{
		System:     "github",
		ObjectType: "merge-request",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: strconv.Itoa(status.Number),
		URL:        status.URL,
		Method:     methodFromConfig(config),
		Endpoint:   "pr create",
		Message:    status.Message,
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePRList(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request list request rejected before invoking gh")
		}
	}

	listRequest, err := normalizePRListRequest(PRListRequest{State: req.State, Scope: req.Scope, Query: req.Query, Limit: req.Limit})
	if err != nil {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request list request rejected before invoking gh")
	}

	result, config, err := s.runner.RunPRList(ctx, repository, listRequest)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil {
		return responseWithGitHubFailure(response, result, err, "gh pr list failed before returning a pull request payload")
	}
	if result.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, result, repository, 0, "gh pr list exited with a non-zero code")
	}

	var raw []ghPRView
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err), Result: result, Err: err}, "gh pr list returned malformed JSON")
	}

	response.MergeRequests = make([]model.MergeRequest, 0, len(raw))
	response.SearchResults = make([]model.TrackerSearchResult, 0, len(raw))
	for _, item := range raw {
		pr := mergeRequestFromGHPR(repository, item)
		response.MergeRequests = append(response.MergeRequests, pr)
		response.SearchResults = append(response.SearchResults, model.TrackerSearchResult{
			System:     "github",
			Repository: repository,
			Kind:       "merge-request",
			Number:     pr.Number,
			Title:      pr.Title,
			State:      pr.State,
			URL:        pr.URL,
			UpdatedAt:  pr.UpdatedAt,
		})
	}
	response.Metadata = map[string]string{
		"repository": repository,
		"state":      listRequest.State,
		"scope":      listRequest.Scope,
		"limit":      strconv.Itoa(listRequest.Limit),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePRComments(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request comments request rejected before invoking gh")
		}
	}
	number, err := normalizePullRequestNumber(req.Number)
	if err != nil {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request comments request rejected before invoking gh")
	}

	result, config, err := s.runner.RunIssueComments(ctx, repository, number)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil {
		return responseWithGitHubFailure(response, result, err, "gh issue comments for pull request failed before returning a comments payload")
	}
	if result.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, result, repository, number, "gh issue comments for pull request exited with a non-zero code")
	}
	rawComments, err := decodeIssueComments(result.Stdout)
	if err != nil {
		return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err), Result: result, Err: err}, "gh issue comments for pull request returned malformed JSON")
	}

	threadResult, _, err := s.runner.RunPRReviewThreads(ctx, repository, number)
	if err != nil {
		return responseWithGitHubFailure(response, threadResult, err, "gh pull request review threads failed before returning a comments payload")
	}
	if threadResult.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, threadResult, repository, number, "gh pull request review threads exited with a non-zero code")
	}
	var rawThreads ghPRReviewThreadsResponse
	if err := json.Unmarshal([]byte(threadResult.Stdout), &rawThreads); err != nil {
		return responseWithGitHubFailure(response, threadResult, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub GraphQL JSON response: %v", err), Result: threadResult, Err: err}, "gh pull request review threads returned malformed JSON")
	}

	var threadNodes []ghPRReviewThread
	if rawThreads.Data.Repository.PullRequest != nil {
		threadNodes = rawThreads.Data.Repository.PullRequest.ReviewThreads.Nodes
	}
	response.ReviewRemarks = make([]model.ReviewRemark, 0, len(rawComments)+len(threadNodes))
	for _, item := range rawComments {
		response.ReviewRemarks = append(response.ReviewRemarks, reviewRemarkFromIssueComment(repository, number, item))
	}
	response.ReviewRemarks = append(response.ReviewRemarks, reviewRemarksFromThreads(repository, number, threadNodes)...)
	response.Metadata = map[string]string{
		"repository": repository,
		"number":     strconv.Itoa(number),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePRCommentCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request comment create request rejected before invoking gh")
		}
	}
	number, err := normalizePullRequestNumber(req.Number)
	if err != nil {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request comment create request rejected before invoking gh")
	}
	commentRequest, err := normalizePRCommentCreateRequest(PRCommentCreateRequest{Body: firstNonEmpty(req.Text, req.Body), Path: req.Path, Line: req.Line, Side: req.Side})
	if err != nil {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request comment create request rejected before invoking gh")
	}
	if commentRequest.Path != "" {
		if existing, config, found := s.findExistingPRRemark(ctx, repository, number, commentRequest); found {
			return successfulPRCommentCreate(response, existing, config, repository, number)
		}
	}

	result, config, err := s.runner.RunPRCommentCreate(ctx, repository, number, commentRequest)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil {
		return responseWithGitHubFailure(response, result, err, "gh pull request comment create failed before returning a comment payload")
	}
	if result.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, result, repository, number, "gh pull request comment create exited with a non-zero code")
	}

	var remark model.ReviewRemark
	if commentRequest.Path == "" {
		var raw ghIssueComment
		if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
			return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err), Result: result, Err: err}, "gh pull request comment create returned malformed JSON")
		}
		remark = reviewRemarkFromIssueComment(repository, number, raw)
	} else {
		var raw ghPRReviewThreadCreateResponse
		if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
			return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub GraphQL JSON response: %v", err), Result: result, Err: err}, "gh pull request inline comment create returned malformed JSON")
		}
		payload := raw.Data.AddPullRequestReviewThread
		if len(raw.Errors) > 0 {
			reference := githubPullRequestReference(repository, number)
			return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("GitHub addPullRequestReviewThread returned GraphQL errors (operation=addPullRequestReviewThread reference=%s response=%s)", reference, safeGitHubResponseDiagnostic(result.Stdout)), Result: result}, "code=external-failure operation=addPullRequestReviewThread reference="+reference+"; GitHub returned GraphQL errors")
		}
		remarks := reviewRemarksFromThreads(repository, number, []ghPRReviewThread{payload.Thread})
		if len(remarks) > 0 {
			remark = remarks[0]
		}
	}
	if remark.ExternalID == "" && remark.URL == "" {
		if existing, _, found := s.findExistingPRRemark(ctx, repository, number, commentRequest); found {
			remark = existing
		} else {
			return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodePartialPayload, Message: fmt.Sprintf("GitHub addPullRequestReviewThread returned no stable identifier for %s#%d (operation=addPullRequestReviewThread reference=%s response=%s)", repository, number, githubPullRequestReference(repository, number), safeGitHubResponseDiagnostic(result.Stdout)), Result: result}, "code=partial-payload operation=addPullRequestReviewThread reference="+githubPullRequestReference(repository, number)+"; existing remarks were checked before retry")
		}
	}

	return successfulPRCommentCreate(response, remark, config, repository, number)
}

func successfulPRCommentCreate(response model.Response, remark model.ReviewRemark, config resolvedConfig, repository string, number int) (model.Response, error) {
	response.ReviewRemarks = []model.ReviewRemark{remark}
	response.OperationResult = &model.OperationResult{
		System:     "github",
		ObjectType: "review-remark",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: firstNonEmpty(remark.ExternalID, remark.URL),
		URL:        remark.URL,
		Method:     methodFromConfig(config),
		Endpoint:   "pr comment create",
		Message:    fmt.Sprintf("GitHub pull request comment created for %s#%d", repository, number),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) findExistingPRRemark(ctx context.Context, repository string, number int, request PRCommentCreateRequest) (model.ReviewRemark, resolvedConfig, bool) {
	result, config, err := s.runner.RunPRReviewThreads(ctx, repository, number)
	if err != nil || result.ExitCode != 0 {
		return model.ReviewRemark{}, config, false
	}
	var raw ghPRReviewThreadsResponse
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil || raw.Data.Repository.PullRequest == nil {
		return model.ReviewRemark{}, config, false
	}
	for _, remark := range reviewRemarksFromThreads(repository, number, raw.Data.Repository.PullRequest.ReviewThreads.Nodes) {
		if remark.Path == request.Path && remark.Line == request.Line && remark.Body == request.Body {
			return remark, config, true
		}
	}
	return model.ReviewRemark{}, config, false
}

func safeGitHubResponseDiagnostic(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "empty-payload"
	}
	return fmt.Sprintf("payload-bytes=%d", len(raw))
}

func githubPullRequestReference(repository string, number int) string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", strings.Trim(strings.TrimSpace(repository), "/"), number)
}

func (s *Service) executePRCommentReply(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	replyRequest, err := normalizePRReviewThreadReplyRequest(PRReviewThreadReplyRequest{ThreadID: firstNonEmpty(req.ThreadID, req.ExternalID), Body: firstNonEmpty(req.Text, req.Body)})
	if err != nil {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "pull request comment reply request rejected before invoking gh")
	}

	result, config, err := s.runner.RunPRReviewThreadReply(ctx, replyRequest)
	if err != nil {
		return responseWithGitHubFailure(response, result, err, "gh pull request review thread reply failed before returning a payload")
	}
	if result.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, result, "", 0, "gh pull request review thread reply exited with a non-zero code")
	}

	var raw ghPRReviewThreadReplyResponse
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub GraphQL JSON response: %v", err), Result: result, Err: err}, "gh pull request review thread reply returned malformed JSON")
	}
	remark := reviewRemarkFromThreadReply(replyRequest.ThreadID, raw.Data.AddPullRequestReviewThreadReply.Comment)
	if remark.ExternalID == "" && remark.URL == "" {
		return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: "unexpected GitHub response: missing pull request review thread reply identifier", Result: result}, "gh pull request review thread reply returned an incomplete payload")
	}

	response.ReviewRemarks = []model.ReviewRemark{remark}
	response.OperationResult = &model.OperationResult{
		System:     "github",
		ObjectType: "review-remark",
		Operation:  "reply",
		Status:     model.ResponseStatusOK,
		ExternalID: firstNonEmpty(remark.ExternalID, remark.URL),
		URL:        remark.URL,
		Method:     methodFromConfig(config),
		Endpoint:   "addPullRequestReviewThreadReply",
		Message:    fmt.Sprintf("GitHub pull request review thread reply created: %s", replyRequest.ThreadID),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePRCommentResolve(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	threadID := strings.TrimSpace(firstNonEmpty(req.ThreadID, req.ExternalID))
	if threadID == "" {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub pull request review thread id is required"}, "pull request comment resolve request rejected before invoking gh")
	}

	result, config, err := s.runner.RunPRReviewThreadResolve(ctx, threadID)
	if err != nil {
		return responseWithGitHubFailure(response, result, err, "gh pull request review thread resolve failed before returning a payload")
	}
	if result.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, result, "", 0, "gh pull request review thread resolve exited with a non-zero code")
	}

	var raw ghPRReviewThreadResolveResponse
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub GraphQL JSON response: %v", err), Result: result, Err: err}, "gh pull request review thread resolve returned malformed JSON")
	}
	resolvedThreadID := firstNonEmpty(raw.Data.ResolveReviewThread.Thread.ID, threadID)
	state := "unresolved"
	if raw.Data.ResolveReviewThread.Thread.IsResolved {
		state = "resolved"
	}
	response.ReviewRemarks = []model.ReviewRemark{{
		System:     "github",
		ExternalID: resolvedThreadID,
		ReplyToID:  resolvedThreadID,
		State:      state,
	}}
	response.OperationResult = &model.OperationResult{
		System:     "github",
		ObjectType: "review-remark",
		Operation:  "resolve",
		Status:     model.ResponseStatusOK,
		ExternalID: resolvedThreadID,
		Method:     methodFromConfig(config),
		Endpoint:   "resolveReviewThread",
		Message:    fmt.Sprintf("GitHub pull request review thread resolved: %s", resolvedThreadID),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeIssueList(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "issue list request rejected before invoking gh")
		}
	}

	listRequest, err := normalizeIssueListRequest(IssueListRequest{State: req.State, Query: req.Query, Labels: req.Labels, ExcludeLabels: req.ExcludeLabels, Limit: req.Limit})
	if err != nil {
		return responseWithGitHubFailure(response, CommandResult{Command: defaultCommand, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}, "issue list request rejected before invoking gh")
	}

	result, config, err := s.runner.RunIssueList(ctx, repository, listRequest)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil {
		return responseWithGitHubFailure(response, result, err, "gh issue list failed before returning an issue payload")
	}
	if result.ExitCode != 0 {
		return responseWithGitHubExitFailure(response, result, repository, 0, "gh issue list exited with a non-zero code")
	}

	var raw []ghIssueView
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return responseWithGitHubFailure(response, result, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err), Result: result, Err: err}, "gh issue list returned malformed JSON")
	}

	response.SearchResults = make([]model.TrackerSearchResult, 0, len(raw))
	for _, item := range raw {
		assignees := make([]model.TrackerUser, 0, len(item.Assignees))
		for _, assignee := range item.Assignees {
			assignees = append(assignees, normalizeTrackerUser(assignee))
		}

		author := model.TrackerUser{System: "github"}
		if item.Author != nil {
			author = normalizeTrackerUser(*item.Author)
		}

		response.SearchResults = append(response.SearchResults, model.TrackerSearchResult{
			System:     "github",
			Repository: repository,
			Kind:       "issue",
			Number:     item.Number,
			Title:      strings.TrimSpace(item.Title),
			State:      strings.TrimSpace(item.State),
			Labels:     normalizeTrackerLabels(item.Labels),
			Author:     author,
			Assignees:  assignees,
			URL:        strings.TrimSpace(item.URL),
			CreatedAt:  strings.TrimSpace(item.CreatedAt),
			UpdatedAt:  strings.TrimSpace(item.UpdatedAt),
		})
	}
	response.Metadata = map[string]string{
		"repository": repository,
		"state":      listRequest.State,
		"limit":      strconv.Itoa(listRequest.Limit),
	}
	if listRequest.Query != "" {
		response.Metadata["query"] = listRequest.Query
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeIssueGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			status := issueErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Number)
			status.State = ErrorCodeInvalidRequest
			status.Message = err.Error()
			status.Diagnostics = append(status.Diagnostics, "issue request rejected before invoking gh")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
		}
	}

	number, err := normalizeIssueNumber(req.Number)
	if err != nil {
		status := issueErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, req.Number)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "issue request rejected before invoking gh")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	result, config, err := s.runner.RunIssueView(ctx, repository, number)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := issueErrorStatus(config, result, repository, number)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = repositoryStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh issue view failed before returning an issue payload")
			response.IssueStatus = &status
			applyGitHubFailure(&response, ghErr.Code, status.Message, status.Diagnostics)
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh issue view failed before returning an issue payload")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			status.Diagnostics = append(status.Diagnostics, "gh issue view reported that no GitHub login is configured")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeAuthRequired, Message: status.Message, Result: result}
		case isIssueNotFound(result), isRepoNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub issue not found: %s#%d", repository, number)
			status.Diagnostics = append(status.Diagnostics, "gh issue view could not resolve the requested issue")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeNotFound, Message: status.Message, Result: result}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			status.Diagnostics = append(status.Diagnostics, "gh issue view exited with a non-zero code")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
		}
	}

	var raw ghIssueView
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		status.State = StateExternalFailure
		status.Message = fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err)
		status.Diagnostics = append(status.Diagnostics, "gh issue view returned malformed JSON")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if raw.Number <= 0 || strings.TrimSpace(raw.Title) == "" {
		status.State = StateExternalFailure
		status.Message = "unexpected GitHub CLI JSON response: missing issue number or title"
		status.Diagnostics = append(status.Diagnostics, "gh issue view returned an incomplete issue payload")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
	}

	labels := normalizeTrackerLabels(raw.Labels)

	assignees := make([]model.TrackerUser, 0, len(raw.Assignees))
	for _, assignee := range raw.Assignees {
		assignees = append(assignees, normalizeTrackerUser(assignee))
	}

	author := model.TrackerUser{System: "github"}
	if raw.Author != nil {
		author = normalizeTrackerUser(*raw.Author)
	}

	response.Issue = &model.TrackerIssue{
		System:     "github",
		Repository: repository,
		Number:     raw.Number,
		Title:      strings.TrimSpace(raw.Title),
		Body:       raw.Body,
		State:      strings.TrimSpace(raw.State),
		Labels:     labels,
		Assignees:  assignees,
		Author:     author,
		URL:        strings.TrimSpace(raw.URL),
		CreatedAt:  strings.TrimSpace(raw.CreatedAt),
		UpdatedAt:  strings.TrimSpace(raw.UpdatedAt),
	}
	response.Task = &model.CanonicalTask{
		System:     "github",
		Repository: response.Issue.Repository,
		Number:     response.Issue.Number,
		Title:      response.Issue.Title,
		Body:       response.Issue.Body,
		State:      response.Issue.State,
		Traits:     append([]string(nil), response.Issue.Labels...),
		Author:     userFromTrackerUser(response.Issue.Author),
		Assignees:  usersFromTrackerUsers(response.Issue.Assignees),
		URL:        response.Issue.URL,
		CreatedAt:  response.Issue.CreatedAt,
		UpdatedAt:  response.Issue.UpdatedAt,
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeIssueComments(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			status := issueCommentsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Number)
			status.State = ErrorCodeInvalidRequest
			status.Message = err.Error()
			status.Diagnostics = append(status.Diagnostics, "issue comments request rejected before invoking gh")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
		}
	}

	number, err := normalizeIssueNumber(req.Number)
	if err != nil {
		status := issueCommentsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, req.Number)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "issue comments request rejected before invoking gh")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	result, config, err := s.runner.RunIssueComments(ctx, repository, number)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := issueCommentsErrorStatus(config, result, repository, number)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = repositoryStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh issue comments failed before returning a comments payload")
			response.IssueStatus = &status
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh issue comments failed before returning a comments payload")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			status.Diagnostics = append(status.Diagnostics, "gh issue comments reported that no GitHub login is configured")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeAuthRequired, Message: status.Message, Result: result}
		case isIssueNotFound(result), isRepoNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub issue not found: %s#%d", repository, number)
			status.Diagnostics = append(status.Diagnostics, "gh issue comments could not resolve the requested issue")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeNotFound, Message: status.Message, Result: result}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			status.Diagnostics = append(status.Diagnostics, "gh issue comments exited with a non-zero code")
			response.IssueStatus = &status
			return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
		}
	}

	raw, err := decodeIssueComments(result.Stdout)
	if err != nil {
		status.State = StateExternalFailure
		status.Message = fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err)
		status.Diagnostics = append(status.Diagnostics, "gh issue comments returned malformed JSON")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	comments := make([]model.TrackerComment, 0, len(raw))
	for _, item := range raw {
		author := model.TrackerUser{System: "github"}
		if item.User != nil {
			author = normalizeTrackerUser(*item.User)
		}
		comments = append(comments, model.TrackerComment{
			System:     "github",
			Repository: repository,
			Number:     number,
			Author:     author,
			Body:       item.Body,
			URL:        strings.TrimSpace(firstNonEmpty(item.HTMLURL, item.URL)),
			CreatedAt:  strings.TrimSpace(item.CreatedAt),
			UpdatedAt:  strings.TrimSpace(item.UpdatedAt),
		})
	}

	response.Comments = comments
	response.TaskComments = make([]model.TaskComment, 0, len(comments))
	for _, comment := range comments {
		response.TaskComments = append(response.TaskComments, model.TaskComment{
			System:     "github",
			Repository: comment.Repository,
			TaskNumber: comment.Number,
			Author: model.User{
				System:   "github",
				Login:    comment.Author.Login,
				Name:     comment.Author.Name,
				Email:    comment.Author.Email,
				URL:      comment.Author.URL,
				IsBot:    comment.Author.IsBot,
				IsActive: comment.Author.IsActive,
			},
			Body:      comment.Body,
			URL:       comment.URL,
			CreatedAt: comment.CreatedAt,
			UpdatedAt: comment.UpdatedAt,
		})
	}
	response.Metadata = map[string]string{
		"repository": repository,
		"number":     strconv.Itoa(number),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeIssueCommentCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			status := issueCommentsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Number)
			status.State = ErrorCodeInvalidRequest
			status.Message = err.Error()
			status.Diagnostics = append(status.Diagnostics, "issue comment create request rejected before invoking gh")
			response.IssueStatus = &status
			response.Status = model.ResponseStatusFailed
			response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: status.Message}
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
		}
	}

	number, err := normalizeIssueNumber(req.Number)
	if err != nil {
		status := issueCommentsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, req.Number)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "issue comment create request rejected before invoking gh")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: status.Message}
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	body := strings.TrimSpace(firstNonEmpty(req.Text, req.Body))
	if body == "" {
		status := issueCommentsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, number)
		status.State = ErrorCodeInvalidRequest
		status.Message = "GitHub issue comment body is required"
		status.Diagnostics = append(status.Diagnostics, "issue comment create request rejected before invoking gh")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: status.Message}
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	result, config, err := s.runner.RunIssueCommentCreate(ctx, repository, number, body)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := issueCommentsErrorStatus(config, result, repository, number)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = repositoryStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh issue comment create failed before returning a comment payload")
			response.IssueStatus = &status
			response.Status = model.ResponseStatusFailed
			response.Failure = &model.Failure{Kind: failureKindForGitHubError(ghErr.Code), Message: status.Message}
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh issue comment create failed before returning a comment payload")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: status.Message}
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			response.Failure = &model.Failure{Kind: model.FailureKindAuthRequired, Message: status.Message}
		case isIssueNotFound(result), isRepoNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub issue not found: %s#%d", repository, number)
			response.Failure = &model.Failure{Kind: model.FailureKindNotFound, Message: status.Message}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: status.Message}
		}
		status.Diagnostics = append(status.Diagnostics, "gh issue comment create exited with a non-zero code")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		return response, &Error{Code: status.State, Message: status.Message, Result: result}
	}

	var raw ghIssueComment
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		status.State = StateExternalFailure
		status.Message = fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err)
		status.Diagnostics = append(status.Diagnostics, "gh issue comment create returned malformed JSON")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: status.Message}
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	author := model.TrackerUser{System: "github"}
	if raw.User != nil {
		author = normalizeTrackerUser(*raw.User)
	}
	comment := model.TrackerComment{
		System:     "github",
		Repository: repository,
		Number:     number,
		Author:     author,
		Body:       raw.Body,
		URL:        strings.TrimSpace(firstNonEmpty(raw.HTMLURL, raw.URL)),
		CreatedAt:  strings.TrimSpace(raw.CreatedAt),
		UpdatedAt:  strings.TrimSpace(raw.UpdatedAt),
	}
	response.Comments = []model.TrackerComment{comment}
	response.TaskComments = []model.TaskComment{{
		System:     "github",
		Repository: repository,
		TaskNumber: number,
		Author: model.User{
			System:   "github",
			Login:    author.Login,
			Name:     author.Name,
			Email:    author.Email,
			URL:      author.URL,
			IsBot:    author.IsBot,
			IsActive: author.IsActive,
		},
		Body:      comment.Body,
		URL:       comment.URL,
		CreatedAt: comment.CreatedAt,
		UpdatedAt: comment.UpdatedAt,
	}}
	response.OperationResult = &model.OperationResult{
		System:     "github",
		ObjectType: "comment",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: comment.URL,
		URL:        comment.URL,
		Method:     methodFromConfig(config),
		Endpoint:   fmt.Sprintf("repos/%s/issues/%d/comments", repository, number),
		Message:    fmt.Sprintf("GitHub issue comment created for %s#%d", repository, number),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeIssueLabelsChange(ctx context.Context, response model.Response, req model.ProviderRequest, add bool) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	operation := "remove"
	action := "remove"
	if add {
		operation = "add"
		action = "add"
	}
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			status := issueLabelsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Number, operation, req.Labels)
			status.State = ErrorCodeInvalidRequest
			status.Message = err.Error()
			status.Diagnostics = append(status.Diagnostics, "issue label "+action+" request rejected before invoking gh")
			response.IssueStatus = &status
			response.Status = model.ResponseStatusFailed
			response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: status.Message}
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
		}
	}

	number, err := normalizeIssueNumber(req.Number)
	if err != nil {
		status := issueLabelsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, req.Number, operation, req.Labels)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "issue label "+action+" request rejected before invoking gh")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: status.Message}
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	labels := normalizeLabelNames(req.Labels)
	if len(labels) == 0 {
		status := issueLabelsErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, number, operation, labels)
		status.State = ErrorCodeInvalidRequest
		status.Message = "GitHub issue label is required"
		status.Diagnostics = append(status.Diagnostics, "issue label "+action+" request rejected before invoking gh")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: status.Message}
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	var result CommandResult
	var config resolvedConfig
	if add {
		result, config, err = s.runner.RunIssueLabelsAdd(ctx, repository, number, labels)
	} else {
		result, config, err = s.runner.RunIssueLabelsRemove(ctx, repository, number, labels)
	}
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := issueLabelsErrorStatus(config, result, repository, number, operation, labels)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = repositoryStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh issue label "+action+" failed before applying labels")
			response.IssueStatus = &status
			response.Status = model.ResponseStatusFailed
			response.Failure = &model.Failure{Kind: failureKindForGitHubError(ghErr.Code), Message: status.Message}
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh issue label "+action+" failed before applying labels")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: status.Message}
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			response.Failure = &model.Failure{Kind: model.FailureKindAuthRequired, Message: status.Message}
		case isIssueNotFound(result), isRepoNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub issue not found: %s#%d", repository, number)
			response.Failure = &model.Failure{Kind: model.FailureKindNotFound, Message: status.Message}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: status.Message}
		}
		status.Diagnostics = append(status.Diagnostics, "gh issue label "+action+" exited with a non-zero code")
		response.IssueStatus = &status
		response.Status = model.ResponseStatusFailed
		return response, &Error{Code: status.State, Message: status.Message, Result: result}
	}

	response.OperationResult = &model.OperationResult{
		System:     "github",
		ObjectType: "label",
		Operation:  operation,
		Status:     model.ResponseStatusOK,
		ExternalID: strconv.Itoa(number),
		Method:     methodFromConfig(config),
		Endpoint:   "issue edit",
		Message:    fmt.Sprintf("GitHub issue labels updated for %s#%d", repository, number),
		Diagnostics: []string{
			"repository=" + repository,
			"number=" + strconv.Itoa(number),
			"labels=" + strings.Join(labels, ","),
		},
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func decodeIssueComments(payload string) ([]ghIssueComment, error) {
	var pages [][]ghIssueComment
	if err := json.Unmarshal([]byte(payload), &pages); err == nil {
		comments := make([]ghIssueComment, 0)
		for _, page := range pages {
			comments = append(comments, page...)
		}
		return comments, nil
	}

	var comments []ghIssueComment
	if err := json.Unmarshal([]byte(payload), &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

func (s *Service) executePRGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			status := prGetErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Number)
			status.State = ErrorCodeInvalidRequest
			status.Message = err.Error()
			status.Diagnostics = append(status.Diagnostics, "pull request get request rejected before invoking gh")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
		}
	}

	number, err := normalizePullRequestNumber(req.Number)
	if err != nil {
		status := prGetErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, repository, req.Number)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "pull request get request rejected before invoking gh")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	result, config, err := s.runner.RunPRView(ctx, repository, number)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := prGetErrorStatus(config, result, repository, number)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = repositoryStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh pr view failed before returning a pull request payload")
			response.PullRequestStatus = &status
			applyGitHubFailure(&response, ghErr.Code, status.Message, status.Diagnostics)
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh pr view failed before returning a pull request payload")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			status.Diagnostics = append(status.Diagnostics, "gh pr view reported that no GitHub login is configured")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeAuthRequired, Message: status.Message, Result: result}
		case isPRNotFound(result), isRepoNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub pull request not found: %s#%d", repository, number)
			status.Diagnostics = append(status.Diagnostics, "gh pr view could not resolve the requested pull request")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeNotFound, Message: status.Message, Result: result}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			status.Diagnostics = append(status.Diagnostics, "gh pr view exited with a non-zero code")
			response.PullRequestStatus = &status
			return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
		}
	}

	var raw ghPRView
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		status.State = StateExternalFailure
		status.Message = fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err)
		status.Diagnostics = append(status.Diagnostics, "gh pr view returned malformed JSON")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if raw.Number <= 0 || strings.TrimSpace(raw.Title) == "" {
		status.State = StateExternalFailure
		status.Message = "unexpected GitHub CLI JSON response: missing pull request number or title"
		status.Diagnostics = append(status.Diagnostics, "gh pr view returned an incomplete pull request payload")
		response.PullRequestStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
	}

	author := model.TrackerUser{System: "github"}
	if raw.Author != nil {
		author = normalizeTrackerUser(*raw.Author)
	}

	response.PullRequest = &model.TrackerPullRequest{
		System:         "github",
		Repository:     repository,
		Number:         raw.Number,
		Title:          strings.TrimSpace(raw.Title),
		Body:           raw.Body,
		State:          strings.TrimSpace(raw.State),
		Author:         author,
		ReviewDecision: strings.TrimSpace(raw.ReviewDecision),
		BaseRef:        strings.TrimSpace(raw.BaseRefName),
		HeadRef:        strings.TrimSpace(raw.HeadRefName),
		Labels:         normalizeTrackerLabels(raw.Labels),
		URL:            strings.TrimSpace(raw.URL),
		CreatedAt:      strings.TrimSpace(raw.CreatedAt),
		UpdatedAt:      strings.TrimSpace(raw.UpdatedAt),
	}
	mergeRequest := mergeRequestFromGHPR(repository, raw)
	response.MergeRequest = &mergeRequest
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeAuthStatus(ctx context.Context, response model.Response) (model.Response, error) {
	result, config, err := s.runner.RunAuthStatus(ctx)
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}

	status := model.AuthStatus{
		System:    "github",
		Command:   config.Command,
		Path:      result.Path,
		ExitCode:  result.ExitCode,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		Available: result.Path != "",
		Diagnostics: []string{
			fmt.Sprintf("command=%s auth status", config.Command),
		},
	}

	if err == nil && result.ExitCode == 0 {
		status.State = StateReady
		status.Available = true
		status.Authenticated = true
		if config.Command == "http" {
			status.Message = "GitHub API token is accepted and API is available"
			status.Diagnostics = append(status.Diagnostics, "GitHub API auth status completed successfully")
		} else {
			status.Message = "GitHub CLI is installed and authentication is available"
			status.Diagnostics = append(status.Diagnostics, "gh auth status completed successfully")
		}
		response.AuthStatus = &status
		response.Status = model.ResponseStatusOK
		return response, nil
	}

	var ghErr *Error
	if errors.As(err, &ghErr) {
		switch ghErr.Code {
		case ErrorCodeNotInstalled:
			status.State = StateNotInstalled
			status.Message = "GitHub CLI is not installed or not available in PATH"
			status.Diagnostics = append(status.Diagnostics, "gh binary could not be resolved")
			response.AuthStatus = &status
			return response, &Error{Code: ghErr.Code, Message: status.Message, Result: result, Err: ghErr}
		case ErrorCodeTimeout:
			status.State = StateTimeout
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh auth status timed out")
			response.AuthStatus = &status
			return response, &Error{Code: ghErr.Code, Message: status.Message, Result: result, Err: ghErr}
		case ErrorCodeAuthRequired:
			status.State = StateAuthRequired
			status.Available = config.Command == "http" && result.Path != ""
			status.Authenticated = false
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "GitHub API token is required")
			response.AuthStatus = &status
			return response, &Error{Code: ghErr.Code, Message: status.Message, Result: result, Err: ghErr}
		case ErrorCodePermissionDenied:
			status.State = ErrorCodePermissionDenied
			status.Available = true
			status.Authenticated = false
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "GitHub API rejected the configured token")
			response.AuthStatus = &status
			return response, &Error{Code: ghErr.Code, Message: status.Message, Result: result, Err: ghErr}
		default:
			status.State = StateExternalFailure
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh auth status failed before completing")
			response.AuthStatus = &status
			return response, &Error{Code: ghErr.Code, Message: status.Message, Result: result, Err: ghErr}
		}
	}

	if err != nil {
		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh auth status failed before completing")
		response.AuthStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if isAuthRequired(result) {
		status.State = StateAuthRequired
		status.Available = true
		status.Authenticated = false
		status.Message = "GitHub authentication is required"
		status.Diagnostics = append(status.Diagnostics, "gh auth status reported that no GitHub login is configured")
		response.AuthStatus = &status
		return response, &Error{Code: ErrorCodeAuthRequired, Message: status.Message, Result: result}
	}

	status.State = StateExternalFailure
	status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
	status.Diagnostics = append(status.Diagnostics, "gh auth status exited with a non-zero code")
	response.AuthStatus = &status
	return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
}

func mergeRequestFromGHPR(repository string, raw ghPRView) model.MergeRequest {
	author := model.User{System: "github"}
	if raw.Author != nil {
		author = userFromTrackerUser(normalizeTrackerUser(*raw.Author))
	}
	attributes := mergeRequestAttributesFromGHPR(raw)
	return model.MergeRequest{
		System:         "github",
		Repository:     repository,
		Number:         raw.Number,
		ExternalID:     strconv.Itoa(raw.Number),
		Title:          strings.TrimSpace(raw.Title),
		Body:           raw.Body,
		State:          strings.TrimSpace(raw.State),
		Traits:         normalizeTrackerLabels(raw.Labels),
		Attributes:     attributes,
		Author:         author,
		ReviewDecision: strings.TrimSpace(raw.ReviewDecision),
		BaseRef:        strings.TrimSpace(raw.BaseRefName),
		HeadRef:        strings.TrimSpace(raw.HeadRefName),
		URL:            strings.TrimSpace(raw.URL),
		CreatedAt:      strings.TrimSpace(raw.CreatedAt),
		UpdatedAt:      strings.TrimSpace(raw.UpdatedAt),
	}
}

func mergeRequestAttributesFromGHPR(raw ghPRView) map[string]string {
	attributes := make(map[string]string, 2)
	if value := normalizedExternalValue(raw.Mergeable); value != "" {
		attributes["mergeable"] = value
	}
	if value := strings.TrimSpace(raw.MergeState); value != "" {
		attributes["merge_state_status"] = value
	}
	if len(attributes) == 0 {
		return nil
	}
	return attributes
}

func normalizedExternalValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func reviewRemarkFromIssueComment(repository string, number int, raw ghIssueComment) model.ReviewRemark {
	author := model.User{System: "github"}
	if raw.User != nil {
		author = userFromTrackerUser(normalizeTrackerUser(*raw.User))
	}
	externalID := ""
	if raw.ID > 0 {
		externalID = strconv.Itoa(raw.ID)
	}
	url := strings.TrimSpace(firstNonEmpty(raw.HTMLURL, raw.URL))
	return model.ReviewRemark{
		System:             "github",
		Repository:         repository,
		MergeRequestNumber: number,
		ExternalID:         firstNonEmpty(externalID, url),
		Author:             author,
		State:              "conversation",
		Body:               raw.Body,
		URL:                url,
		CreatedAt:          strings.TrimSpace(raw.CreatedAt),
		UpdatedAt:          strings.TrimSpace(raw.UpdatedAt),
	}
}

func reviewRemarksFromThreads(repository string, number int, threads []ghPRReviewThread) []model.ReviewRemark {
	remarks := make([]model.ReviewRemark, 0, len(threads))
	for _, thread := range threads {
		state := "unresolved"
		if thread.IsResolved {
			state = "resolved"
		}
		if len(thread.Comments.Nodes) == 0 {
			remarks = append(remarks, model.ReviewRemark{
				System:             "github",
				Repository:         repository,
				MergeRequestNumber: number,
				ExternalID:         strings.TrimSpace(thread.ID),
				State:              state,
				Path:               strings.TrimSpace(thread.Path),
				Line:               thread.Line,
				ReplyToID:          strings.TrimSpace(thread.ID),
			})
			continue
		}
		for _, comment := range thread.Comments.Nodes {
			author := model.User{System: "github"}
			if comment.Author != nil {
				author = userFromTrackerUser(normalizeTrackerUser(*comment.Author))
			}
			path := strings.TrimSpace(firstNonEmpty(comment.Path, thread.Path))
			line := comment.Line
			if line == 0 {
				line = thread.Line
			}
			remarks = append(remarks, model.ReviewRemark{
				System:             "github",
				Repository:         repository,
				MergeRequestNumber: number,
				ExternalID:         strings.TrimSpace(firstNonEmpty(comment.ID, thread.ID)),
				Author:             author,
				State:              state,
				Body:               comment.Body,
				Path:               path,
				Line:               line,
				ReplyToID:          strings.TrimSpace(thread.ID),
				URL:                strings.TrimSpace(comment.URL),
				CreatedAt:          strings.TrimSpace(comment.CreatedAt),
				UpdatedAt:          strings.TrimSpace(comment.UpdatedAt),
			})
		}
	}
	return remarks
}

func reviewRemarkFromThreadReply(threadID string, comment ghPRReviewComment) model.ReviewRemark {
	author := model.User{System: "github"}
	if comment.Author != nil {
		author = userFromTrackerUser(normalizeTrackerUser(*comment.Author))
	}
	return model.ReviewRemark{
		System:     "github",
		ExternalID: strings.TrimSpace(comment.ID),
		Author:     author,
		State:      "reply",
		Body:       strings.TrimSpace(comment.Body),
		Path:       strings.TrimSpace(comment.Path),
		Line:       comment.Line,
		ReplyToID:  strings.TrimSpace(threadID),
		URL:        strings.TrimSpace(comment.URL),
		CreatedAt:  strings.TrimSpace(comment.CreatedAt),
		UpdatedAt:  strings.TrimSpace(comment.UpdatedAt),
	}
}

func responseWithGitHubFailure(response model.Response, result CommandResult, err error, diagnostic string) (model.Response, error) {
	response.Status = model.ResponseStatusFailed
	var ghErr *Error
	if errors.As(err, &ghErr) {
		response.Failure = &model.Failure{
			Kind:        failureKindForGitHubError(ghErr.Code),
			Message:     ghErr.Message,
			Diagnostics: []string{diagnostic},
		}
		return response, ghErr
	}
	message := "GitHub integration failed"
	if err != nil {
		message = err.Error()
	}
	response.Failure = &model.Failure{
		Kind:        model.FailureKindExternalFailure,
		Message:     message,
		Diagnostics: []string{diagnostic},
	}
	return response, &Error{Code: ErrorCodeExternalFailure, Message: message, Result: result, Err: err}
}

func applyGitHubFailure(response *model.Response, code string, message string, diagnostics []string) {
	response.Status = model.ResponseStatusFailed
	response.Failure = &model.Failure{
		Kind:        failureKindForGitHubError(code),
		Message:     message,
		Diagnostics: append([]string(nil), diagnostics...),
	}
}

func responseWithGitHubExitFailure(response model.Response, result CommandResult, repository string, number int, diagnostic string) (model.Response, error) {
	code := ErrorCodeExternalFailure
	message := fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
	switch {
	case isAuthRequired(result):
		code = ErrorCodeAuthRequired
		message = "GitHub authentication is required"
	case isPRNotFound(result), isIssueNotFound(result):
		code = ErrorCodeNotFound
		if repository != "" && number > 0 {
			message = fmt.Sprintf("GitHub pull request not found: %s#%d", repository, number)
		} else {
			message = "GitHub pull request not found"
		}
	case isRepoNotFound(result):
		code = ErrorCodeNotFound
		if repository != "" {
			message = fmt.Sprintf("GitHub repository not found: %s", repository)
		} else {
			message = "GitHub repository not found"
		}
	}
	return responseWithGitHubFailure(response, result, &Error{Code: code, Message: message, Result: result}, diagnostic)
}

func (s *Service) executeRepoGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if req.RepoProvided {
		var err error
		repository, err = normalizeRepository(repository)
		if err != nil {
			status := repositoryErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository))
			status.State = ErrorCodeInvalidRequest
			status.Message = err.Error()
			status.Diagnostics = append(status.Diagnostics, "repository request rejected before invoking gh")
			response.RepositoryStatus = &status
			return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
		}
	}

	result, config, err := s.runner.RunRepoView(ctx, repository)
	repository = firstNonEmpty(repository, strings.TrimSpace(config.DefaultRepo))
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = -1
	}
	if config.Command == "" {
		config.Command = defaultCommand
	}

	status := repositoryErrorStatus(config, result, repository)
	if err != nil {
		var ghErr *Error
		if errors.As(err, &ghErr) {
			status.State = repositoryStateForErrorCode(ghErr.Code)
			status.Message = ghErr.Message
			status.Diagnostics = append(status.Diagnostics, "gh repo view failed before returning a repository payload")
			response.RepositoryStatus = &status
			applyGitHubFailure(&response, ghErr.Code, status.Message, status.Diagnostics)
			return response, ghErr
		}

		status.State = StateExternalFailure
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "gh repo view failed before returning a repository payload")
		response.RepositoryStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	if result.ExitCode != 0 {
		switch {
		case isAuthRequired(result):
			status.State = ErrorCodeAuthRequired
			status.Message = "GitHub authentication is required"
			status.Diagnostics = append(status.Diagnostics, "gh repo view reported that no GitHub login is configured")
			response.RepositoryStatus = &status
			return response, &Error{Code: ErrorCodeAuthRequired, Message: status.Message, Result: result}
		case isRepoNotFound(result):
			status.State = ErrorCodeNotFound
			status.Message = fmt.Sprintf("GitHub repository not found: %s", repository)
			status.Diagnostics = append(status.Diagnostics, "gh repo view could not resolve the requested repository")
			response.RepositoryStatus = &status
			return response, &Error{Code: ErrorCodeNotFound, Message: status.Message, Result: result}
		default:
			status.State = StateExternalFailure
			status.Message = fmt.Sprintf("GitHub CLI returned exit code %d", result.ExitCode)
			status.Diagnostics = append(status.Diagnostics, "gh repo view exited with a non-zero code")
			response.RepositoryStatus = &status
			return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
		}
	}

	var raw ghRepoView
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		status.State = StateExternalFailure
		status.Message = fmt.Sprintf("unexpected GitHub CLI JSON response: %v", err)
		status.Diagnostics = append(status.Diagnostics, "gh repo view returned malformed JSON")
		response.RepositoryStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result, Err: err}
	}

	owner := strings.TrimSpace(raw.Owner.Login)
	name := strings.TrimSpace(raw.Name)
	if owner == "" || name == "" {
		status.State = StateExternalFailure
		status.Message = "unexpected GitHub CLI JSON response: missing repository owner or name"
		status.Diagnostics = append(status.Diagnostics, "gh repo view returned an incomplete repository payload")
		response.RepositoryStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
	}

	defaultBranch := ""
	if raw.DefaultBranchRef != nil {
		defaultBranch = strings.TrimSpace(raw.DefaultBranchRef.Name)
	}

	response.RepositoryRef = &model.TrackerRepository{
		System:        "github",
		FullName:      owner + "/" + name,
		Owner:         owner,
		Name:          name,
		Description:   strings.TrimSpace(raw.Description),
		DefaultBranch: defaultBranch,
		URL:           strings.TrimSpace(raw.URL),
	}
	response.Repository = &model.Repository{
		System:        "github",
		FullName:      response.RepositoryRef.FullName,
		Owner:         response.RepositoryRef.Owner,
		Name:          response.RepositoryRef.Name,
		Description:   response.RepositoryRef.Description,
		DefaultBranch: response.RepositoryRef.DefaultBranch,
		URL:           response.RepositoryRef.URL,
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func repositoryErrorStatus(config resolvedConfig, result CommandResult, repository string) model.RepositoryStatus {
	status := model.RepositoryStatus{
		System:     "github",
		Repository: repository,
		State:      StateExternalFailure,
		Command:    config.Command,
		Path:       result.Path,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}

	if status.Command == "" {
		status.Command = defaultCommand
	}
	if status.ExitCode == 0 {
		status.ExitCode = -1
	}

	if repository != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("repository=%s", repository))
	}
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("command=%s repo view %s --json name,owner,description,defaultBranchRef,url", status.Command, repository))
	return status
}

func repositoryStateForErrorCode(code string) string {
	switch code {
	case ErrorCodeInvalidRequest:
		return ErrorCodeInvalidRequest
	case ErrorCodeNotInstalled:
		return StateNotInstalled
	case ErrorCodeAuthRequired:
		return ErrorCodeAuthRequired
	case ErrorCodeNotFound:
		return ErrorCodeNotFound
	case ErrorCodeTimeout:
		return StateTimeout
	case ErrorCodePermissionDenied:
		return ErrorCodePermissionDenied
	case ErrorCodeTemporaryUnavailable:
		return model.FailureKindTemporaryUnavailable
	case ErrorCodeUnsupportedOperation:
		return model.FailureKindUnsupportedOperation
	case ErrorCodeInternalIntegration:
		return ErrorCodeInternalIntegration
	default:
		return StateExternalFailure
	}
}

func prStateForErrorCode(code string) string {
	switch code {
	case ErrorCodeInvalidRequest:
		return ErrorCodeInvalidRequest
	case ErrorCodeNotInstalled:
		return StateNotInstalled
	case ErrorCodeAuthRequired:
		return ErrorCodeAuthRequired
	case ErrorCodeNotFound:
		return ErrorCodeNotFound
	case ErrorCodeAlreadyExists:
		return ErrorCodeAlreadyExists
	case ErrorCodeTimeout:
		return StateTimeout
	case ErrorCodePermissionDenied:
		return ErrorCodePermissionDenied
	case ErrorCodeTemporaryUnavailable:
		return model.FailureKindTemporaryUnavailable
	case ErrorCodeUnsupportedOperation:
		return model.FailureKindUnsupportedOperation
	case ErrorCodeInternalIntegration:
		return ErrorCodeInternalIntegration
	default:
		return StateExternalFailure
	}
}

func issueErrorStatus(config resolvedConfig, result CommandResult, repository string, number int) model.IssueStatus {
	status := model.IssueStatus{
		System:     "github",
		Repository: repository,
		Number:     number,
		State:      StateExternalFailure,
		Command:    config.Command,
		Path:       result.Path,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}

	if status.Command == "" {
		status.Command = defaultCommand
	}
	if status.ExitCode == 0 {
		status.ExitCode = -1
	}

	if repository != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("repository=%s", repository))
	}
	if number > 0 {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("number=%d", number))
	}
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("command=%s issue view %d --repo %s --json number,title,body,state,labels,assignees,author,url,createdAt,updatedAt", status.Command, number, repository))
	return status
}

func issueLabelsErrorStatus(config resolvedConfig, result CommandResult, repository string, number int, operation string, labels []string) model.IssueStatus {
	status := model.IssueStatus{
		System:     "github",
		Repository: repository,
		Number:     number,
		State:      StateExternalFailure,
		Command:    config.Command,
		Path:       result.Path,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}

	if status.Command == "" {
		status.Command = defaultCommand
	}
	if status.ExitCode == 0 {
		status.ExitCode = -1
	}

	labels = normalizeLabelNames(labels)
	if repository != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("repository=%s", repository))
	}
	if number > 0 {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("number=%d", number))
	}
	if len(labels) > 0 {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("labels=%s", strings.Join(labels, ",")))
	}
	status.Diagnostics = append(status.Diagnostics, issueLabelsCommandDiagnostic(status.Command, repository, number, operation, labels))
	return status
}

func issueLabelsCommandDiagnostic(command string, repository string, number int, operation string, labels []string) string {
	flag := "--remove-label"
	if operation == "add" {
		flag = "--add-label"
	}

	parts := []string{command, "issue", "edit", strconv.Itoa(number), "--repo", repository}
	for _, label := range labels {
		parts = append(parts, flag, label)
	}
	return "command=" + strings.Join(parts, " ")
}

func issueCommentsErrorStatus(config resolvedConfig, result CommandResult, repository string, number int) model.IssueStatus {
	status := model.IssueStatus{
		System:     "github",
		Repository: repository,
		Number:     number,
		State:      StateExternalFailure,
		Command:    config.Command,
		Path:       result.Path,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}

	if status.Command == "" {
		status.Command = defaultCommand
	}
	if status.ExitCode == 0 {
		status.ExitCode = -1
	}

	if repository != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("repository=%s", repository))
	}
	if number > 0 {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("number=%d", number))
	}
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("command=%s api --paginate --slurp repos/%s/issues/%d/comments", status.Command, repository, number))
	return status
}

func prGetErrorStatus(config resolvedConfig, result CommandResult, repository string, number int) model.PullRequestStatus {
	status := model.PullRequestStatus{
		System:     "github",
		Repository: repository,
		Number:     number,
		State:      StateExternalFailure,
		Command:    config.Command,
		Path:       result.Path,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}

	if status.Command == "" {
		status.Command = defaultCommand
	}
	if status.ExitCode == 0 {
		status.ExitCode = -1
	}

	if repository != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("repository=%s", repository))
	}
	if number > 0 {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("number=%d", number))
	}
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("command=%s pr view %d --repo %s --json number,title,body,state,mergeable,mergeStateStatus,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt", status.Command, number, repository))
	return status
}

func prErrorStatus(config resolvedConfig, result CommandResult, repository string, base string, head string, title string, body string, draft bool) model.PullRequestStatus {
	status := model.PullRequestStatus{
		System:     "github",
		Repository: repository,
		Base:       strings.TrimSpace(base),
		Head:       strings.TrimSpace(head),
		Title:      strings.TrimSpace(title),
		Draft:      draft,
		State:      StateExternalFailure,
		Command:    config.Command,
		Path:       result.Path,
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
	}

	if status.Command == "" {
		status.Command = defaultCommand
	}
	if status.ExitCode == 0 {
		status.ExitCode = -1
	}

	if repository != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("repository=%s", repository))
	}
	if status.Base != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("base=%s", status.Base))
	}
	if status.Head != "" {
		status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("head=%s", status.Head))
	}
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("draft=%t", status.Draft))
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("command=%s pr create --repo %s --base %s --head %s --title %s --body %s", status.Command, repository, status.Base, status.Head, maskCommandValue(status.Title), maskCommandValue(body)))
	return status
}

func normalizeTrackerUser(raw ghIssueUser) model.TrackerUser {
	return model.TrackerUser{
		System:   "github",
		Login:    strings.TrimSpace(raw.Login),
		Name:     strings.TrimSpace(raw.Name),
		Email:    strings.TrimSpace(raw.Email),
		URL:      strings.TrimSpace(firstNonEmpty(raw.URL, raw.HTMLURL)),
		IsBot:    raw.IsBot,
		IsActive: raw.IsActive,
	}
}

func normalizeTrackerLabels(labels []ghIssueLabel) []string {
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		name := strings.TrimSpace(label.Name)
		if name != "" {
			result = append(result, name)
		}
	}

	return result
}

func normalizeLabelNames(labels []string) []string {
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

func isAuthRequired(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "not logged into any github hosts") || strings.Contains(message, "gh auth login")
}

func isRepoNotFound(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "repository not found") ||
		strings.Contains(message, "could not resolve to a repository") ||
		strings.Contains(message, "http 404")
}

func isIssueNotFound(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "could not resolve to an issue") ||
		strings.Contains(message, "could not resolve to an issue or pull request") ||
		strings.Contains(message, "issue not found") ||
		strings.Contains(message, "http 404")
}

func isPRNotFound(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "could not resolve to a pull request") ||
		strings.Contains(message, "could not resolve to a pullrequest") ||
		strings.Contains(message, "could not resolve to an issue or pull request") ||
		strings.Contains(message, "pull request not found") ||
		strings.Contains(message, "http 404")
}

func isPRAlreadyExists(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "a pull request for branch") ||
		strings.Contains(message, "already exists") && strings.Contains(message, "pull request")
}

func isPRCreateBranchNotFound(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "head sha can't be blank") ||
		strings.Contains(message, "base sha can't be blank") ||
		strings.Contains(message, "head ref must be a branch") ||
		strings.Contains(message, "not found") && strings.Contains(message, "branch")
}

func isPRCreateNoCommits(result CommandResult) bool {
	message := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(message, "no commits between")
}

func extractFirstURL(value string) string {
	for _, field := range strings.Fields(strings.TrimSpace(value)) {
		field = strings.Trim(field, "()[]<>{}\"'.,")
		if strings.HasPrefix(field, "https://") || strings.HasPrefix(field, "http://") {
			return field
		}
	}

	return ""
}

func pullRequestNumberFromURL(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	parts := strings.Split(strings.TrimRight(value, "/"), "/")
	if len(parts) == 0 {
		return 0
	}

	number, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || number <= 0 {
		return 0
	}

	return number
}

func maskCommandValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "''"
	}

	return "<provided>"
}

func integrationTypeForRequest(req model.ProviderRequest) string {
	switch {
	case isIssueRequest(req), isIssueCommentRequest(req):
		return model.IntegrationTypeTracker
	case isRepositoryRequest(req), isPullRequestRequest(req), isPullRequestCommentRequest(req):
		return model.IntegrationTypeRepository
	default:
		return ""
	}
}

func isRepositoryRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "repository" || object == "repo"
}

func isIssueRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "issue" || object == "task"
}

func isIssueCommentRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "comment" && req.IntegrationType == model.IntegrationTypeTracker
}

func isIssueLabelRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return (object == "label" || object == "task-label") && req.IntegrationType == model.IntegrationTypeTracker
}

func isPullRequestCommentRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return (object == "comment" || object == "review-remark" || object == "merge-request-comment") && req.IntegrationType == model.IntegrationTypeRepository
}

func isPullRequestRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "pr" || object == "pull-request" || object == "merge-request" || object == "mr"
}

func failureKindForGitHubError(code string) string {
	switch code {
	case ErrorCodeAuthRequired:
		return model.FailureKindAuthRequired
	case ErrorCodeNotFound:
		return model.FailureKindNotFound
	case ErrorCodeInvalidRequest:
		return model.FailureKindInvalidRequest
	case ErrorCodeTimeout:
		return model.FailureKindTemporaryUnavailable
	case ErrorCodePermissionDenied:
		return model.FailureKindPermissionDenied
	case ErrorCodeTemporaryUnavailable:
		return model.FailureKindTemporaryUnavailable
	case ErrorCodeUnsupportedOperation:
		return model.FailureKindUnsupportedOperation
	case ErrorCodeInternalIntegration:
		return model.FailureKindPartialResponse
	case ErrorCodePartialPayload:
		return model.FailureKindPartialResponse
	default:
		return model.FailureKindExternalFailure
	}
}

func methodFromConfig(config resolvedConfig) string {
	if strings.TrimSpace(config.Command) == "" {
		return defaultCommand
	}
	return strings.TrimSpace(config.Command)
}

func userFromTrackerUser(user model.TrackerUser) model.User {
	return model.User{
		System:   user.System,
		Login:    user.Login,
		Name:     user.Name,
		Email:    user.Email,
		URL:      user.URL,
		IsBot:    user.IsBot,
		IsActive: user.IsActive,
	}
}

func usersFromTrackerUsers(users []model.TrackerUser) []model.User {
	if len(users) == 0 {
		return nil
	}
	result := make([]model.User, 0, len(users))
	for _, user := range users {
		result = append(result, userFromTrackerUser(user))
	}
	return result
}
