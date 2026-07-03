package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

const defaultAPIBaseURL = "https://api.github.com"

type APIRunner struct {
	systemConfig integrationmodel.IntegrationSystemConfig
	client       *http.Client
	getenv       func(string) string
}

type apiConfig struct {
	BaseURL     string
	Token       string
	Timeout     time.Duration
	DefaultRepo string
}

func NewAPIRunnerWithSystemConfig(config integrationmodel.IntegrationSystemConfig) *APIRunner {
	return &APIRunner{systemConfig: config, client: http.DefaultClient, getenv: os.Getenv}
}

func (r *APIRunner) RunAuthStatus(ctx context.Context) (CommandResult, resolvedConfig, error) {
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("auth status", config, err)
	}
	var raw map[string]any
	result, err := r.do(ctx, config, http.MethodGet, "user", nil, &raw)
	return result, apiResolvedConfig(config), err
}

func (r *APIRunner) RunRepoView(ctx context.Context, repository string) (CommandResult, resolvedConfig, error) {
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("repo view", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("repo view", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var raw apiRepository
	result, err := r.do(ctx, config, http.MethodGet, "repos/"+repository, nil, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	out := ghRepoView{Name: raw.Name, Description: raw.Description, URL: raw.HTMLURL}
	out.Owner.Login = raw.Owner.Login
	if raw.DefaultBranch != "" {
		out.DefaultBranchRef = &struct {
			Name string `json:"name"`
		}{Name: raw.DefaultBranch}
	}
	result.Stdout = mustJSON(out)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunIssueView(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		return apiErrorResult("issue view", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("issue view", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("issue view", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var raw apiIssue
	result, err := r.do(ctx, config, http.MethodGet, fmt.Sprintf("repos/%s/issues/%d", repository, number), nil, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = mustJSON(issueViewFromAPI(raw))
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunIssueComments(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		return apiErrorResult("issue comments", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("issue comments", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("issue comments", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var raw []ghIssueComment
	result, err := r.do(ctx, config, http.MethodGet, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", repository, number), nil, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = mustJSON(raw)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunIssueCommentCreate(ctx context.Context, repository string, number int, body string) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		return apiErrorResult("issue comment create", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return apiErrorResult("issue comment create", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub issue comment body is required"})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("issue comment create", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("issue comment create", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var raw ghIssueComment
	result, err := r.do(ctx, config, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/comments", repository, number), map[string]string{"body": body}, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = mustJSON(raw)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunIssueLabelsAdd(ctx context.Context, repository string, number int, labels []string) (CommandResult, resolvedConfig, error) {
	return r.runIssueLabelsChange(ctx, repository, number, labels, true)
}

func (r *APIRunner) RunIssueLabelsRemove(ctx context.Context, repository string, number int, labels []string) (CommandResult, resolvedConfig, error) {
	return r.runIssueLabelsChange(ctx, repository, number, labels, false)
}

func (r *APIRunner) runIssueLabelsChange(ctx context.Context, repository string, number int, labels []string, add bool) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		return apiErrorResult("issue labels", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	labels = normalizeIssueLabels(labels)
	if len(labels) == 0 {
		return apiErrorResult("issue labels", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub issue label is required"})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("issue labels", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("issue labels", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	result := CommandResult{Command: "http", Path: config.BaseURL, ExitCode: 0}
	if add {
		var raw any
		result, err = r.do(ctx, config, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/labels", repository, number), map[string][]string{"labels": labels}, &raw)
		return result, apiResolvedConfig(config), err
	}
	for _, label := range labels {
		var raw any
		result, err = r.do(ctx, config, http.MethodDelete, fmt.Sprintf("repos/%s/issues/%d/labels/%s", repository, number, url.PathEscape(label)), nil, &raw)
		if err != nil {
			return result, apiResolvedConfig(config), err
		}
	}
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRView(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return apiErrorResult("pr view", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("pr view", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("pr view", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var raw apiPullRequest
	result, err := r.do(ctx, config, http.MethodGet, fmt.Sprintf("repos/%s/pulls/%d", repository, number), nil, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = mustJSON(prViewFromAPI(raw))
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRList(ctx context.Context, repository string, request PRListRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRListRequest(request)
	if err != nil {
		return apiErrorResult("pr list", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	if strings.TrimSpace(request.Query) != "" || request.Scope != "all" {
		return apiErrorResult("pr list", apiConfig{}, &Error{Code: ErrorCodeUnsupportedOperation, Message: "GitHub API transport does not support pull request search query or scope yet"})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("pr list", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("pr list", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var raw []apiPullRequest
	endpoint := fmt.Sprintf("repos/%s/pulls?state=%s&per_page=%d", repository, request.State, request.Limit)
	result, err := r.do(ctx, config, http.MethodGet, endpoint, nil, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	views := make([]ghPRView, 0, len(raw))
	for _, item := range raw {
		views = append(views, prViewFromAPI(item))
	}
	result.Stdout = mustJSON(views)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRCreate(ctx context.Context, repository string, request PRCreateRequest) (CommandResult, resolvedConfig, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		return apiErrorResult("pr create", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	request, err = normalizePRCreateRequest(request)
	if err != nil {
		return apiErrorResult("pr create", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("pr create", config, err)
	}
	var raw apiPullRequest
	result, err := r.do(ctx, config, http.MethodPost, "repos/"+repository+"/pulls", map[string]any{"base": request.Base, "head": request.Head, "title": request.Title, "body": request.Body, "draft": request.Draft}, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = raw.HTMLURL
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRReviewThreads(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return apiErrorResult("pr review threads", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("pr review threads", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("pr review threads", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	owner, name, err := splitRepository(repository)
	if err != nil {
		return apiErrorResult("pr review threads", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	query := `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) { reviewThreads(first: 100) { nodes { id isResolved isOutdated path line comments(first: 100) { nodes { id body url path line author { login url } createdAt updatedAt } } } } } } }`
	var raw json.RawMessage
	result, err := r.graphql(ctx, config, query, map[string]any{"owner": owner, "name": name, "number": number}, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = string(raw)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRCommentCreate(ctx context.Context, repository string, number int, request PRCommentCreateRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRCommentCreateRequest(request)
	if err != nil {
		return apiErrorResult("pr comment create", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	if request.Path == "" && request.Line == 0 {
		return r.RunIssueCommentCreate(ctx, repository, number, request.Body)
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("pr comment create", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("pr comment create", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	owner, name, err := splitRepository(repository)
	if err != nil {
		return apiErrorResult("pr comment create", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	nodeQuery := `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) { id } } }`
	var node ghPullRequestNodeResponse
	if _, err := r.graphql(ctx, config, nodeQuery, map[string]any{"owner": owner, "name": name, "number": number}, &node); err != nil {
		return apiErrorResult("pr comment create", config, err)
	}
	pullRequestID := strings.TrimSpace(node.Data.Repository.PullRequest.ID)
	if pullRequestID == "" {
		return apiErrorResult("pr comment create", config, &Error{Code: ErrorCodeNotFound, Message: fmt.Sprintf("GitHub pull request not found: %s#%d", repository, number)})
	}
	mutation := `mutation($pullRequestId: ID!, $body: String!, $path: String!, $line: Int!, $side: DiffSide!) { addPullRequestReviewThread(input: {pullRequestId: $pullRequestId, body: $body, path: $path, line: $line, side: $side}) { thread { id isResolved comments(first: 1) { nodes { id body url path line author { login url } createdAt updatedAt } } } } }`
	var raw json.RawMessage
	result, err := r.graphql(ctx, config, mutation, map[string]any{"pullRequestId": pullRequestID, "body": request.Body, "path": request.Path, "line": request.Line, "side": request.Side}, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = string(raw)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRReviewThreadResolve(ctx context.Context, threadID string) (CommandResult, resolvedConfig, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return apiErrorResult("pr review thread resolve", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub pull request review thread id is required"})
	}
	config, err := r.resolveConfig()
	if err != nil {
		return apiErrorResult("pr review thread resolve", config, err)
	}
	mutation := `mutation($threadId: ID!) { resolveReviewThread(input: {threadId: $threadId}) { thread { id isResolved } } }`
	var raw json.RawMessage
	result, err := r.graphql(ctx, config, mutation, map[string]any{"threadId": threadID}, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = string(raw)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) resolveConfig() (apiConfig, error) {
	timeout := defaultTimeout
	if value := strings.TrimSpace(r.systemConfig.Timeout); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return apiConfig{}, fmt.Errorf("parse GitHub API timeout: %w", err)
		}
		timeout = parsed
	}
	token := strings.TrimSpace(r.systemConfig.Token)
	if token == "" && strings.TrimSpace(r.systemConfig.TokenEnv) != "" {
		token = strings.TrimSpace(r.getenv(strings.TrimSpace(r.systemConfig.TokenEnv)))
	}
	if token == "" {
		return apiConfig{Timeout: timeout}, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub API token is required"}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(r.systemConfig.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	return apiConfig{
		BaseURL:     baseURL,
		Token:       token,
		Timeout:     timeout,
		DefaultRepo: firstNonEmpty(strings.TrimSpace(r.systemConfig.Repository), strings.TrimSpace(r.systemConfig.DefaultRepo)),
	}, nil
}

func (r *APIRunner) do(ctx context.Context, config apiConfig, method string, endpoint string, payload any, out any) (CommandResult, error) {
	body, err := encodePayload(payload)
	if err != nil {
		return CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error(), Err: err}
	}
	reqCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, config.BaseURL+"/"+strings.TrimLeft(endpoint, "/"), body)
	if err != nil {
		return CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error(), Err: err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+config.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	result := CommandResult{Command: "http", Path: config.BaseURL, Args: []string{method, endpoint}, ExitCode: 0}
	if err != nil {
		result.ExitCode = -1
		code := ErrorCodeExternalFailure
		if isTimeoutError(err, reqCtx) {
			code = ErrorCodeTimeout
		}
		return result, &Error{Code: code, Message: err.Error(), Err: err, Result: result}
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(resp.Body)
	result.Stdout = strings.TrimSpace(string(content))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ExitCode = resp.StatusCode
		return result, errorForHTTPStatus(resp.StatusCode, result.Stdout, result)
	}
	if out != nil && len(content) > 0 {
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], content...)
		} else if err := json.Unmarshal(content, out); err != nil {
			result.ExitCode = -1
			return result, &Error{Code: ErrorCodeInternalIntegration, Message: fmt.Sprintf("decode GitHub API response: %v", err), Err: err, Result: result}
		}
	}
	return result, nil
}

func (r *APIRunner) graphql(ctx context.Context, config apiConfig, query string, variables map[string]any, out any) (CommandResult, error) {
	endpoint := strings.TrimRight(config.BaseURL, "/")
	if strings.HasSuffix(endpoint, "/api/v3") {
		endpoint = strings.TrimSuffix(endpoint, "/api/v3") + "/api/graphql"
	} else {
		endpoint += "/graphql"
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	reqCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error(), Err: err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+config.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	result := CommandResult{Command: "http", Path: config.BaseURL, Args: []string{http.MethodPost, "graphql"}}
	if err != nil {
		result.ExitCode = -1
		code := ErrorCodeExternalFailure
		if isTimeoutError(err, reqCtx) {
			code = ErrorCodeTimeout
		}
		return result, &Error{Code: code, Message: err.Error(), Err: err, Result: result}
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(resp.Body)
	result.Stdout = strings.TrimSpace(string(content))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ExitCode = resp.StatusCode
		return result, errorForHTTPStatus(resp.StatusCode, result.Stdout, result)
	}
	if out != nil {
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], content...)
		} else if err := json.Unmarshal(content, out); err != nil {
			result.ExitCode = -1
			return result, &Error{Code: ErrorCodeInternalIntegration, Message: fmt.Sprintf("decode GitHub GraphQL response: %v", err), Err: err, Result: result}
		}
	}
	return result, nil
}

type apiRepository struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type apiIssue struct {
	Number    int            `json:"number"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	State     string         `json:"state"`
	HTMLURL   string         `json:"html_url"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	Labels    []ghIssueLabel `json:"labels"`
	Assignees []ghIssueUser  `json:"assignees"`
	User      *ghIssueUser   `json:"user"`
}

type apiPullRequest struct {
	Number    int          `json:"number"`
	Title     string       `json:"title"`
	Body      string       `json:"body"`
	State     string       `json:"state"`
	HTMLURL   string       `json:"html_url"`
	CreatedAt string       `json:"created_at"`
	UpdatedAt string       `json:"updated_at"`
	User      *ghIssueUser `json:"user"`
	Base      struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

func issueViewFromAPI(raw apiIssue) ghIssueView {
	return ghIssueView{Number: raw.Number, Title: raw.Title, Body: raw.Body, State: raw.State, URL: raw.HTMLURL, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt, Labels: raw.Labels, Assignees: raw.Assignees, Author: raw.User}
}

func prViewFromAPI(raw apiPullRequest) ghPRView {
	return ghPRView{Number: raw.Number, Title: raw.Title, Body: raw.Body, State: raw.State, Author: raw.User, BaseRefName: raw.Base.Ref, HeadRefName: raw.Head.Ref, URL: raw.HTMLURL, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt}
}

func encodePayload(payload any) (io.Reader, error) {
	if payload == nil {
		return nil, nil
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(content), nil
}

func errorForHTTPStatus(status int, body string, result CommandResult) *Error {
	code := ErrorCodeExternalFailure
	switch status {
	case http.StatusUnauthorized:
		code = ErrorCodeAuthRequired
	case http.StatusForbidden:
		if isRateLimitBody(body) {
			code = ErrorCodeTemporaryUnavailable
		} else {
			code = ErrorCodePermissionDenied
		}
	case http.StatusNotFound:
		code = ErrorCodeNotFound
	case http.StatusTooManyRequests:
		code = ErrorCodeTemporaryUnavailable
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		code = ErrorCodeInvalidRequest
	default:
		if status >= 500 {
			code = ErrorCodeTemporaryUnavailable
		}
	}
	return &Error{Code: code, Message: fmt.Sprintf("GitHub API returned status %d: %s", status, body), Result: result}
}

func isTimeoutError(err error, ctx context.Context) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func isRateLimitBody(body string) bool {
	body = strings.ToLower(body)
	return strings.Contains(body, "rate limit") || strings.Contains(body, "secondary rate limit")
}

func apiResolvedConfig(config apiConfig) resolvedConfig {
	return resolvedConfig{Command: "http", Timeout: config.Timeout, DefaultRepo: config.DefaultRepo}
}

func apiErrorResult(command string, config apiConfig, err error) (CommandResult, resolvedConfig, error) {
	return CommandResult{Command: "http", Path: config.BaseURL, Args: []string{command}, ExitCode: -1}, apiResolvedConfig(config), err
}

func mustJSON(value any) string {
	content, _ := json.Marshal(value)
	return string(content)
}
