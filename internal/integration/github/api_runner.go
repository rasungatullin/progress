package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

const defaultAPIBaseURL = "https://api.github.com"
const defaultGitHubAppTokenRefreshBefore = 5 * time.Minute

type APIRunner struct {
	systemConfig integrationmodel.IntegrationSystemConfig
	client       *http.Client
	getenv       func(string) string
	readFile     func(string) ([]byte, error)
	now          func() time.Time
	tokenMu      sync.Mutex
	cachedToken  *githubAppInstallationToken
}

type apiConfig struct {
	BaseURL                 string
	Token                   string
	Timeout                 time.Duration
	DefaultRepo             string
	GitHubAppIssuer         string
	GitHubAppInstallationID string
	GitHubAppPrivateKeyPath string
	GitHubAppPrivateKey     string
	TokenRefreshBefore      time.Duration
}

type githubAppInstallationToken struct {
	Token     string
	ExpiresAt time.Time
}

type githubAppInstallationTokenResponse struct {
	Token               string            `json:"token"`
	ExpiresAt           string            `json:"expires_at"`
	Permissions         map[string]string `json:"permissions"`
	RepositorySelection string            `json:"repository_selection"`
}

func NewAPIRunnerWithSystemConfig(config integrationmodel.IntegrationSystemConfig) *APIRunner {
	return &APIRunner{systemConfig: config, client: http.DefaultClient, getenv: os.Getenv, readFile: os.ReadFile, now: time.Now}
}

func (r *APIRunner) RunAuthStatus(ctx context.Context) (CommandResult, resolvedConfig, error) {
	config, err := r.resolveBaseConfig()
	if err != nil {
		return apiErrorResult("auth status", config, err)
	}
	if config.Token == "" && githubAppAuthConfigured(config) {
		_, result, err := r.installationAccessToken(ctx, config)
		return result, apiResolvedConfig(config), err
	}
	if config.Token == "" {
		return apiErrorResult("auth status", config, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub API token or GitHub App settings are required"})
	}
	var raw map[string]any
	result, err := r.do(ctx, config, http.MethodGet, "user", nil, &raw)
	return result, apiResolvedConfig(config), err
}

func (r *APIRunner) RunRepoView(ctx context.Context, repository string) (CommandResult, resolvedConfig, error) {
	config, err := r.resolveConfig(ctx)
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
	config, err := r.resolveConfig(ctx)
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
	config, err := r.resolveConfig(ctx)
	if err != nil {
		return apiErrorResult("issue comments", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("issue comments", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	var result CommandResult
	var comments []ghIssueComment
	for page := 1; ; page++ {
		var raw []ghIssueComment
		result, err = r.do(ctx, config, http.MethodGet, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100&page=%d", repository, number, page), nil, &raw)
		if err != nil {
			return result, apiResolvedConfig(config), err
		}
		comments = append(comments, raw...)
		if len(raw) < 100 {
			break
		}
	}
	result.Stdout = mustJSON(comments)
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
	config, err := r.resolveConfig(ctx)
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
	config, err := r.resolveConfig(ctx)
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
	config, err := r.resolveConfig(ctx)
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
	view := prViewFromAPI(raw)
	metadataResult, err := r.enrichPullRequestView(ctx, config, repository, &view)
	if err != nil {
		return metadataResult, apiResolvedConfig(config), err
	}
	result.Stdout = mustJSON(view)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) RunPRList(ctx context.Context, repository string, request PRListRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRListRequest(request)
	if err != nil {
		return apiErrorResult("pr list", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	headRef, querySupported := apiPRListHeadQuery(request.Query)
	if (strings.TrimSpace(request.Query) != "" && !querySupported) || request.Scope != "all" {
		return apiErrorResult("pr list", apiConfig{}, &Error{Code: ErrorCodeUnsupportedOperation, Message: "GitHub API transport does not support pull request search query or scope yet"})
	}
	config, err := r.resolveConfig(ctx)
	if err != nil {
		return apiErrorResult("pr list", config, err)
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return apiErrorResult("pr list", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	owner, _, err := splitRepository(repository)
	if err != nil {
		return apiErrorResult("pr list", config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	apiState := request.State
	filterMerged := false
	filterClosed := false
	if apiState == "merged" {
		apiState = "closed"
		filterMerged = true
	} else if apiState == "closed" {
		filterClosed = true
	}
	perPage := request.Limit
	if perPage <= 0 || perPage > 100 {
		perPage = 100
	}
	var result CommandResult
	var pullRequests []apiPullRequest
	for page := 1; len(pullRequests) < request.Limit; page++ {
		var raw []apiPullRequest
		query := url.Values{}
		query.Set("state", apiState)
		query.Set("per_page", fmt.Sprintf("%d", perPage))
		query.Set("page", fmt.Sprintf("%d", page))
		if headRef != "" {
			query.Set("head", owner+":"+headRef)
		}
		endpoint := fmt.Sprintf("repos/%s/pulls?%s", repository, query.Encode())
		result, err = r.do(ctx, config, http.MethodGet, endpoint, nil, &raw)
		if err != nil {
			return result, apiResolvedConfig(config), err
		}
		for _, item := range raw {
			merged := strings.TrimSpace(item.MergedAt) != ""
			if filterMerged && !merged {
				continue
			}
			if filterClosed && merged {
				continue
			}
			pullRequests = append(pullRequests, item)
			if len(pullRequests) >= request.Limit {
				break
			}
		}
		if len(raw) < perPage {
			break
		}
	}
	views := make([]ghPRView, 0, len(pullRequests))
	for _, item := range pullRequests {
		view := prViewFromAPI(item)
		metadataResult, err := r.enrichPullRequestView(ctx, config, repository, &view)
		if err != nil {
			return metadataResult, apiResolvedConfig(config), err
		}
		views = append(views, view)
	}
	result.Stdout = mustJSON(views)
	return result, apiResolvedConfig(config), nil
}

func apiPRListHeadQuery(query string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", true
	}
	parts := strings.Fields(query)
	if len(parts) != 1 {
		return "", false
	}
	head, ok := strings.CutPrefix(parts[0], "head:")
	if !ok || strings.TrimSpace(head) == "" {
		return "", false
	}
	return strings.TrimSpace(head), true
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
	config, err := r.resolveConfig(ctx)
	if err != nil {
		return apiErrorResult("pr create", config, err)
	}
	var raw apiPullRequest
	result, err := r.do(ctx, config, http.MethodPost, "repos/"+repository+"/pulls", map[string]any{"base": request.Base, "head": request.Head, "title": request.Title, "body": request.Body, "draft": request.Draft}, &raw)
	if err != nil {
		if classifiedResult, ok := prCreateHTTPFailureForServiceClassification(result); ok {
			result = classifiedResult
			return result, apiResolvedConfig(config), nil
		}
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = raw.HTMLURL
	return result, apiResolvedConfig(config), nil
}

func prCreateHTTPFailureForServiceClassification(result CommandResult) (CommandResult, bool) {
	if result.ExitCode != http.StatusUnprocessableEntity {
		return result, false
	}
	if message := apiErrorMessage(result.Stdout); message != "" {
		result.Stderr = message
		result.Stdout = ""
	}
	return result, isPRAlreadyExists(result) || isPRCreateBranchNotFound(result) || isPRCreateNoCommits(result) || isRepoNotFound(result)
}

func apiErrorMessage(body string) string {
	var payload struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	messages := make([]string, 0, 1+len(payload.Errors))
	if message := strings.TrimSpace(payload.Message); message != "" {
		messages = append(messages, message)
	}
	for _, item := range payload.Errors {
		if message := strings.TrimSpace(item.Message); message != "" {
			messages = append(messages, message)
		}
	}
	return strings.Join(messages, "\n")
}

func (r *APIRunner) RunPRReviewThreads(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return apiErrorResult("pr review threads", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig(ctx)
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

func (r *APIRunner) enrichPullRequestView(ctx context.Context, config apiConfig, repository string, view *ghPRView) (CommandResult, error) {
	if view == nil || view.Number <= 0 {
		return CommandResult{}, nil
	}
	owner, name, err := splitRepository(repository)
	if err != nil {
		return CommandResult{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	query := `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) { reviewDecision labels(first: 100) { nodes { name } } } } }`
	var metadata apiPullRequestMetadataResponse
	result, err := r.graphql(ctx, config, query, map[string]any{"owner": owner, "name": name, "number": view.Number}, &metadata)
	if err != nil {
		return result, err
	}
	pr := metadata.Data.Repository.PullRequest
	if pr == nil {
		return result, &Error{Code: ErrorCodeNotFound, Message: fmt.Sprintf("GitHub pull request not found: %s#%d", repository, view.Number), Result: result}
	}
	view.ReviewDecision = strings.TrimSpace(pr.ReviewDecision)
	view.Labels = append([]ghIssueLabel(nil), pr.Labels.Nodes...)
	return result, nil
}

func (r *APIRunner) RunPRCommentCreate(ctx context.Context, repository string, number int, request PRCommentCreateRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRCommentCreateRequest(request)
	if err != nil {
		return apiErrorResult("pr comment create", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	if request.Path == "" && request.Line == 0 {
		return r.RunIssueCommentCreate(ctx, repository, number, request.Body)
	}
	config, err := r.resolveConfig(ctx)
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
	config, err := r.resolveConfig(ctx)
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

func (r *APIRunner) RunPRReviewThreadReply(ctx context.Context, request PRReviewThreadReplyRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRReviewThreadReplyRequest(request)
	if err != nil {
		return apiErrorResult("pr review thread reply", apiConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()})
	}
	config, err := r.resolveConfig(ctx)
	if err != nil {
		return apiErrorResult("pr review thread reply", config, err)
	}
	mutation := `mutation($threadId: ID!, $body: String!) { addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $threadId, body: $body}) { comment { id body url path line author { login url } createdAt updatedAt } } }`
	var raw json.RawMessage
	result, err := r.graphql(ctx, config, mutation, map[string]any{"threadId": request.ThreadID, "body": request.Body}, &raw)
	if err != nil {
		return result, apiResolvedConfig(config), err
	}
	result.Stdout = string(raw)
	return result, apiResolvedConfig(config), nil
}

func (r *APIRunner) resolveConfig(ctx context.Context) (apiConfig, error) {
	config, err := r.resolveBaseConfig()
	if err != nil {
		return config, err
	}
	if config.Token != "" {
		return config, nil
	}
	if !githubAppAuthConfigured(config) {
		return config, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub API token or GitHub App settings are required"}
	}
	token, _, err := r.installationAccessToken(ctx, config)
	if err != nil {
		return config, err
	}
	config.Token = token.Token
	return config, nil
}

func (r *APIRunner) resolveBaseConfig() (apiConfig, error) {
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
		token = strings.TrimSpace(r.env(strings.TrimSpace(r.systemConfig.TokenEnv)))
	}
	baseURL := strings.TrimRight(strings.TrimSpace(r.systemConfig.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	refreshBefore := defaultGitHubAppTokenRefreshBefore
	if token == "" {
		var err error
		refreshBefore, err = parseGitHubAppTokenRefreshBefore(r.systemConfig.GitHubAppTokenRefreshBefore)
		if err != nil {
			return apiConfig{BaseURL: baseURL, Timeout: timeout}, err
		}
	}
	return apiConfig{
		BaseURL:                 baseURL,
		Token:                   token,
		Timeout:                 timeout,
		DefaultRepo:             firstNonEmpty(strings.TrimSpace(r.systemConfig.Repository), strings.TrimSpace(r.systemConfig.DefaultRepo)),
		GitHubAppIssuer:         firstNonEmpty(strings.TrimSpace(r.systemConfig.GitHubAppID), strings.TrimSpace(r.systemConfig.GitHubAppClientID)),
		GitHubAppInstallationID: strings.TrimSpace(r.systemConfig.GitHubAppInstallationID),
		GitHubAppPrivateKeyPath: strings.TrimSpace(r.systemConfig.GitHubAppPrivateKeyPath),
		GitHubAppPrivateKey:     strings.TrimSpace(r.systemConfig.GitHubAppPrivateKey),
		TokenRefreshBefore:      refreshBefore,
	}, nil
}

func parseGitHubAppTokenRefreshBefore(raw string) (time.Duration, error) {
	refreshBefore := defaultGitHubAppTokenRefreshBefore
	if value := strings.TrimSpace(raw); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("parse GitHub App token refresh interval: %w", err)
		}
		if parsed <= 0 {
			return 0, fmt.Errorf("GitHub App token refresh interval must be positive")
		}
		refreshBefore = parsed
	}
	return refreshBefore, nil
}

func (r *APIRunner) installationAccessToken(ctx context.Context, config apiConfig) (githubAppInstallationToken, CommandResult, error) {
	now := r.currentTime()
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()

	if r.cachedToken != nil && strings.TrimSpace(r.cachedToken.Token) != "" && now.Add(config.TokenRefreshBefore).Before(r.cachedToken.ExpiresAt) {
		result := CommandResult{Command: "http", Path: config.BaseURL, Args: []string{http.MethodPost, "app/installations/" + config.GitHubAppInstallationID + "/access_tokens"}, ExitCode: 0}
		result.Stdout = mustJSON(map[string]string{"expires_at": r.cachedToken.ExpiresAt.Format(time.RFC3339), "source": "cache"})
		return *r.cachedToken, result, nil
	}

	if strings.TrimSpace(config.GitHubAppIssuer) == "" {
		return githubAppInstallationToken{}, CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App issuer is required"}
	}
	if strings.TrimSpace(config.GitHubAppInstallationID) == "" {
		return githubAppInstallationToken{}, CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App installation id is required"}
	}

	privateKey, err := r.githubAppPrivateKey(config)
	if err != nil {
		return githubAppInstallationToken{}, CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, err
	}
	jwt, err := githubAppJWT(privateKey, config.GitHubAppIssuer, now)
	if err != nil {
		return githubAppInstallationToken{}, CommandResult{Command: "http", Path: config.BaseURL, ExitCode: -1}, err
	}

	result, response, err := r.createInstallationAccessToken(ctx, config, jwt)
	if err != nil {
		return githubAppInstallationToken{}, result, err
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(response.ExpiresAt))
	if err != nil {
		result.ExitCode = -1
		return githubAppInstallationToken{}, result, &Error{Code: ErrorCodeInternalIntegration, Message: fmt.Sprintf("decode GitHub App installation token expiry: %v", err), Err: err, Result: result}
	}
	if strings.TrimSpace(response.Token) == "" {
		result.ExitCode = -1
		return githubAppInstallationToken{}, result, &Error{Code: ErrorCodeInternalIntegration, Message: "GitHub App installation token response does not contain token", Result: result}
	}

	token := githubAppInstallationToken{Token: strings.TrimSpace(response.Token), ExpiresAt: expiresAt}
	r.cachedToken = &token
	result.Stdout = mustJSON(map[string]any{
		"expires_at":           response.ExpiresAt,
		"repository_selection": response.RepositorySelection,
		"permissions":          response.Permissions,
	})
	return token, result, nil
}

func (r *APIRunner) createInstallationAccessToken(ctx context.Context, config apiConfig, jwt string) (CommandResult, githubAppInstallationTokenResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	endpoint := "app/installations/" + strings.TrimSpace(config.GitHubAppInstallationID) + "/access_tokens"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, config.BaseURL+"/"+endpoint, nil)
	if err != nil {
		result := CommandResult{Command: "http", Path: config.BaseURL, Args: []string{http.MethodPost, endpoint}, ExitCode: -1}
		return result, githubAppInstallationTokenResponse{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error(), Err: err, Result: result}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)

	resp, err := r.httpClient().Do(req)
	result := CommandResult{Command: "http", Path: config.BaseURL, Args: []string{http.MethodPost, endpoint}, ExitCode: 0}
	if err != nil {
		result.ExitCode = -1
		code := ErrorCodeExternalFailure
		if isTimeoutError(err, reqCtx) {
			code = ErrorCodeTimeout
		}
		return result, githubAppInstallationTokenResponse{}, &Error{Code: code, Message: err.Error(), Err: err, Result: result}
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.ExitCode = resp.StatusCode
		result.Stdout = strings.TrimSpace(string(content))
		return result, githubAppInstallationTokenResponse{}, errorForHTTPStatus(resp.StatusCode, result.Stdout, result)
	}
	var response githubAppInstallationTokenResponse
	if err := json.Unmarshal(content, &response); err != nil {
		result.ExitCode = -1
		return result, githubAppInstallationTokenResponse{}, &Error{Code: ErrorCodeInternalIntegration, Message: fmt.Sprintf("decode GitHub App installation token response: %v", err), Err: err, Result: result}
	}
	return result, response, nil
}

func (r *APIRunner) githubAppPrivateKey(config apiConfig) (*rsa.PrivateKey, error) {
	content := strings.TrimSpace(config.GitHubAppPrivateKey)
	if content == "" {
		path, err := expandUserPath(config.GitHubAppPrivateKeyPath)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(path) == "" {
			return nil, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App private key path or private value is required"}
		}
		raw, err := r.fileReader()(path)
		if err != nil {
			return nil, &Error{Code: ErrorCodeAuthRequired, Message: fmt.Sprintf("read GitHub App private key: %v", err), Err: err}
		}
		content = strings.TrimSpace(string(raw))
	}
	return parseGitHubAppPrivateKey([]byte(content))
}

func parseGitHubAppPrivateKey(content []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(content)
	if block == nil {
		return nil, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App private key is not a PEM block"}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, &Error{Code: ErrorCodeAuthRequired, Message: fmt.Sprintf("parse GitHub App private key: %v", err), Err: err}
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App private key must be RSA"}
	}
	return key, nil
}

func githubAppJWT(privateKey *rsa.PrivateKey, issuer string, now time.Time) (string, error) {
	if privateKey == nil {
		return "", &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App private key is required"}
	}
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return "", &Error{Code: ErrorCodeAuthRequired, Message: "GitHub App issuer is required"}
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	payload := map[string]any{
		"iat": now.Add(-1 * time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": issuer,
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString([]byte(mustJSON(header)))
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(mustJSON(payload)))
	signingInput := encodedHeader + "." + encodedPayload
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(nil, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", &Error{Code: ErrorCodeAuthRequired, Message: fmt.Sprintf("sign GitHub App JWT: %v", err), Err: err}
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func githubAppAuthConfigured(config apiConfig) bool {
	return strings.TrimSpace(config.GitHubAppIssuer) != "" ||
		strings.TrimSpace(config.GitHubAppInstallationID) != "" ||
		strings.TrimSpace(config.GitHubAppPrivateKeyPath) != "" ||
		strings.TrimSpace(config.GitHubAppPrivateKey) != ""
}

func (r *APIRunner) httpClient() *http.Client {
	if r.client != nil {
		return r.client
	}
	return http.DefaultClient
}

func (r *APIRunner) env(name string) string {
	if r.getenv != nil {
		return r.getenv(name)
	}
	return os.Getenv(name)
}

func (r *APIRunner) fileReader() func(string) ([]byte, error) {
	if r.readFile != nil {
		return r.readFile
	}
	return os.ReadFile
}

func (r *APIRunner) currentTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func expandUserPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "~" {
		return path, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory for GitHub App private key: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return filepath.Clean(path), nil
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
	resp, err := r.httpClient().Do(req)
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
	resp, err := r.httpClient().Do(req)
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
	var envelope graphqlErrorEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		result.ExitCode = -1
		return result, &Error{Code: ErrorCodeInternalIntegration, Message: fmt.Sprintf("decode GitHub GraphQL response: %v", err), Err: err, Result: result}
	}
	if len(envelope.Errors) > 0 {
		return result, &Error{Code: errorCodeForGraphQLErrors(envelope.Errors), Message: "GitHub GraphQL returned errors: " + graphqlErrorsMessage(envelope.Errors), Result: result}
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
	MergedAt string `json:"merged_at"`
}

type apiPullRequestMetadataResponse struct {
	Data struct {
		Repository struct {
			PullRequest *struct {
				ReviewDecision string `json:"reviewDecision"`
				Labels         struct {
					Nodes []ghIssueLabel `json:"nodes"`
				} `json:"labels"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

type graphqlErrorEnvelope struct {
	Errors []graphqlError `json:"errors"`
}

type graphqlError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func issueViewFromAPI(raw apiIssue) ghIssueView {
	return ghIssueView{Number: raw.Number, Title: raw.Title, Body: raw.Body, State: raw.State, URL: raw.HTMLURL, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt, Labels: raw.Labels, Assignees: raw.Assignees, Author: raw.User}
}

func prViewFromAPI(raw apiPullRequest) ghPRView {
	return ghPRView{Number: raw.Number, Title: raw.Title, Body: raw.Body, State: prStateFromAPI(raw), Author: raw.User, BaseRefName: raw.Base.Ref, HeadRefName: raw.Head.Ref, URL: raw.HTMLURL, CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt}
}

func prStateFromAPI(raw apiPullRequest) string {
	state := strings.TrimSpace(raw.State)
	if strings.EqualFold(state, "closed") && strings.TrimSpace(raw.MergedAt) != "" {
		return "merged"
	}
	return state
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

func errorCodeForGraphQLErrors(errors []graphqlError) string {
	message := strings.ToLower(graphqlErrorsMessage(errors))
	switch {
	case strings.Contains(message, "rate limit"):
		return ErrorCodeTemporaryUnavailable
	case strings.Contains(message, "resource not accessible") || strings.Contains(message, "permission") || strings.Contains(message, "forbidden"):
		return ErrorCodePermissionDenied
	case strings.Contains(message, "not found") || strings.Contains(message, "could not resolve"):
		return ErrorCodeNotFound
	default:
		return ErrorCodeExternalFailure
	}
}

func graphqlErrorsMessage(errors []graphqlError) string {
	messages := make([]string, 0, len(errors))
	for _, item := range errors {
		message := strings.TrimSpace(item.Message)
		if message == "" {
			message = strings.TrimSpace(item.Type)
		}
		if message != "" {
			messages = append(messages, message)
		}
	}
	return strings.Join(messages, "; ")
}

func apiResolvedConfig(config apiConfig) resolvedConfig {
	return resolvedConfig{Command: "http", Timeout: config.Timeout, DefaultRepo: config.DefaultRepo}
}

func apiErrorResult(command string, config apiConfig, err error) (CommandResult, resolvedConfig, error) {
	result := CommandResult{Command: "http", Path: config.BaseURL, Args: []string{command}, ExitCode: -1}
	var ghErr *Error
	if errors.As(err, &ghErr) {
		if ghErr.Result.Command != "" || ghErr.Result.Path != "" || ghErr.Result.ExitCode != 0 || ghErr.Result.Stdout != "" || ghErr.Result.Stderr != "" {
			result = ghErr.Result
			if result.Command == "" {
				result.Command = "http"
			}
			if result.Path == "" {
				result.Path = config.BaseURL
			}
			if len(result.Args) == 0 {
				result.Args = []string{command}
			}
		}
	}
	return result, apiResolvedConfig(config), err
}

func mustJSON(value any) string {
	content, _ := json.Marshal(value)
	return string(content)
}
