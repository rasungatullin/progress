package bitbucket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/integration/model"
)

const (
	defaultBaseURL = "https://api.bitbucket.org/2.0"
	defaultTimeout = 30 * time.Second
)

type Service struct {
	config model.IntegrationSystemConfig
	client *http.Client
}

type repositoryRef struct {
	workspace string
	slug      string
	fullName  string
}

type apiRepository struct {
	UUID        string `json:"uuid"`
	FullName    string `json:"full_name"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Website     string `json:"website"`
	Links       struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	MainBranch *struct {
		Name string `json:"name"`
	} `json:"mainbranch"`
	Workspace struct {
		Slug string `json:"slug"`
	} `json:"workspace"`
}

type apiPullRequest struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	State       string  `json:"state"`
	CreatedOn   string  `json:"created_on"`
	UpdatedOn   string  `json:"updated_on"`
	Author      apiUser `json:"author"`
	Links       struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"source"`
	Destination struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"destination"`
}

type apiUser struct {
	UUID        string `json:"uuid"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
	AccountID   string `json:"account_id"`
	Links       struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

type apiCommentPage struct {
	Values []apiComment `json:"values"`
}

type apiComment struct {
	ID        int     `json:"id"`
	CreatedOn string  `json:"created_on"`
	UpdatedOn string  `json:"updated_on"`
	User      apiUser `json:"user"`
	Content   struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
	Inline *struct {
		Path string `json:"path"`
		To   int    `json:"to"`
		From int    `json:"from"`
	} `json:"inline"`
}

type apiUserResponse struct {
	UUID        string `json:"uuid"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
}

func NewService(config model.IntegrationSystemConfig) *Service {
	return &Service{config: config, client: http.DefaultClient}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
	}

	switch {
	case req.Resource == "auth" && req.Operation == "status":
		return s.executeAuthStatus(ctx, response)
	case isRepositoryObject(req) && req.Operation == "get":
		return s.executeRepositoryGet(ctx, response, req)
	case isMergeRequestObject(req) && req.Operation == "get":
		return s.executePullRequestGet(ctx, response, req)
	case isMergeRequestObject(req) && req.Operation == "create":
		return s.executePullRequestCreate(ctx, response, req)
	case isMergeRequestObject(req) && req.Operation == "comments":
		return s.executePullRequestComments(ctx, response, req)
	default:
		err := fmt.Errorf("Bitbucket integration does not support %s %s", firstNonEmpty(req.ObjectType, req.Resource), req.Operation)
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
	}
}

func (s *Service) executeAuthStatus(ctx context.Context, response model.Response) (model.Response, error) {
	token := s.token()
	if token == "" {
		err := fmt.Errorf("Bitbucket token is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindAuthRequired, Message: err.Error()}
		response.AuthStatus = &model.AuthStatus{System: "bitbucket", State: model.FailureKindAuthRequired, Available: true, Authenticated: false, Message: err.Error()}
		return response, err
	}

	status, body, err := s.do(ctx, http.MethodGet, "user", nil)
	auth := &model.AuthStatus{
		System:      "bitbucket",
		State:       "ready",
		Available:   true,
		Command:     "http",
		ExitCode:    statusToExitCode(status),
		Stdout:      string(body),
		Diagnostics: []string{"endpoint=user"},
	}
	if err != nil {
		auth.State = failureKindForHTTPStatus(status)
		auth.Message = err.Error()
		response.AuthStatus = auth
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		return response, err
	}

	var raw apiUserResponse
	_ = json.Unmarshal(body, &raw)
	auth.Authenticated = true
	auth.Message = strings.TrimSpace(firstNonEmpty(raw.DisplayName, raw.Nickname, raw.UUID, "Bitbucket authorization is available"))
	response.AuthStatus = auth
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeRepositoryGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	status, body, err := s.do(ctx, http.MethodGet, fmt.Sprintf("repositories/%s/%s", url.PathEscape(repository.workspace), url.PathEscape(repository.slug)), nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
		return response, err
	}

	var raw apiRepository
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}

	owner := strings.TrimSpace(firstNonEmpty(raw.Workspace.Slug, repository.workspace))
	name := strings.TrimSpace(firstNonEmpty(raw.Slug, raw.Name, repository.slug))
	defaultBranch := ""
	if raw.MainBranch != nil {
		defaultBranch = strings.TrimSpace(raw.MainBranch.Name)
	}
	response.Repository = &model.Repository{
		System:        "bitbucket",
		ExternalID:    strings.TrimSpace(raw.UUID),
		FullName:      strings.TrimSpace(firstNonEmpty(raw.FullName, owner+"/"+name)),
		Owner:         owner,
		Name:          name,
		Description:   strings.TrimSpace(raw.Description),
		DefaultBranch: defaultBranch,
		URL:           strings.TrimSpace(firstNonEmpty(raw.Links.HTML.Href, raw.Website)),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePullRequestGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if req.Number <= 0 {
		err := fmt.Errorf("Bitbucket pull request number must be greater than zero")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests/%d", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.Number)
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
		return response, err
	}

	var raw apiPullRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}
	response.MergeRequest = mergeRequestFromAPI(repository.fullName, raw)
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePullRequestCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if strings.TrimSpace(req.Base) == "" || strings.TrimSpace(req.Head) == "" || strings.TrimSpace(req.Title) == "" {
		err := fmt.Errorf("Bitbucket pull request create requires base, head and title")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	payload := map[string]any{
		"title":       strings.TrimSpace(req.Title),
		"description": strings.TrimSpace(req.Body),
		"source": map[string]any{
			"branch": map[string]string{"name": strings.TrimSpace(req.Head)},
		},
		"destination": map[string]any{
			"branch": map[string]string{"name": strings.TrimSpace(req.Base)},
		},
	}
	content, _ := json.Marshal(payload)
	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests", url.PathEscape(repository.workspace), url.PathEscape(repository.slug))
	status, body, err := s.do(ctx, http.MethodPost, endpoint, content)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodPost, repository.fullName, err, body)
		return response, err
	}

	var raw apiPullRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}
	response.MergeRequest = mergeRequestFromAPI(repository.fullName, raw)
	response.OperationResult = &model.OperationResult{
		System:     "bitbucket",
		ObjectType: "merge-request",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: strconv.Itoa(raw.ID),
		URL:        response.MergeRequest.URL,
		HTTPStatus: status,
		Method:     http.MethodPost,
		Endpoint:   endpoint,
		Message:    fmt.Sprintf("Bitbucket pull request created for %s %s -> %s", repository.fullName, req.Head, req.Base),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePullRequestComments(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if req.Number <= 0 {
		err := fmt.Errorf("Bitbucket pull request number must be greater than zero")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests/%d/comments", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.Number)
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
		return response, err
	}

	var raw apiCommentPage
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}
	response.ReviewRemarks = make([]model.ReviewRemark, 0, len(raw.Values))
	for _, item := range raw.Values {
		remark := model.ReviewRemark{
			System:             "bitbucket",
			Repository:         repository.fullName,
			MergeRequestNumber: req.Number,
			ExternalID:         strconv.Itoa(item.ID),
			Author:             userFromAPI(item.User),
			Body:               item.Content.Raw,
			URL:                strings.TrimSpace(item.Links.HTML.Href),
			CreatedAt:          strings.TrimSpace(item.CreatedOn),
			UpdatedAt:          strings.TrimSpace(item.UpdatedOn),
		}
		if item.Inline != nil {
			remark.Path = strings.TrimSpace(item.Inline.Path)
			remark.Line = item.Inline.To
			if remark.Line == 0 {
				remark.Line = item.Inline.From
			}
		}
		response.ReviewRemarks = append(response.ReviewRemarks, remark)
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) do(ctx context.Context, method string, endpoint string, payload []byte) (int, []byte, error) {
	timeout, err := parseTimeout(s.config.Timeout)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	base := strings.TrimRight(firstNonEmpty(s.config.BaseURL, defaultBaseURL), "/")
	requestURL := base + "/" + strings.TrimLeft(endpoint, "/")
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token := s.token(); token != "" {
		if username := strings.TrimSpace(s.config.Username); username != "" {
			request.SetBasicAuth(username, token)
		} else {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	content, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return response.StatusCode, content, readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, content, fmt.Errorf("Bitbucket API returned HTTP status %d", response.StatusCode)
	}
	return response.StatusCode, content, nil
}

func (s *Service) resolveRepository(raw string, repoProvided bool) (repositoryRef, error) {
	raw = strings.TrimSpace(raw)
	if repoProvided && raw == "" {
		return repositoryRef{}, fmt.Errorf("Bitbucket repository is required")
	}
	raw = firstNonEmpty(raw, s.config.Repository, s.config.DefaultRepo)
	if raw == "" {
		return repositoryRef{}, fmt.Errorf("Bitbucket repository is required")
	}
	workspace := strings.TrimSpace(s.config.Workspace)
	parts := strings.Split(raw, "/")
	switch len(parts) {
	case 1:
		if workspace == "" {
			return repositoryRef{}, fmt.Errorf("Bitbucket repository must use workspace/repo format or define workspace")
		}
		return repositoryRef{workspace: workspace, slug: parts[0], fullName: workspace + "/" + parts[0]}, nil
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return repositoryRef{}, fmt.Errorf("Bitbucket repository must use workspace/repo format")
		}
		return repositoryRef{workspace: parts[0], slug: parts[1], fullName: parts[0] + "/" + parts[1]}, nil
	default:
		return repositoryRef{}, fmt.Errorf("Bitbucket repository must use workspace/repo format")
	}
}

func (s *Service) token() string {
	if token := strings.TrimSpace(s.config.Token); token != "" {
		return token
	}
	if env := strings.TrimSpace(s.config.TokenEnv); env != "" {
		return strings.TrimSpace(os.Getenv(env))
	}
	return ""
}

func mergeRequestFromAPI(repository string, raw apiPullRequest) *model.MergeRequest {
	return &model.MergeRequest{
		System:     "bitbucket",
		Repository: repository,
		Number:     raw.ID,
		ExternalID: strconv.Itoa(raw.ID),
		Title:      strings.TrimSpace(raw.Title),
		Body:       raw.Description,
		State:      strings.TrimSpace(raw.State),
		BaseRef:    strings.TrimSpace(raw.Destination.Branch.Name),
		HeadRef:    strings.TrimSpace(raw.Source.Branch.Name),
		Author:     userFromAPI(raw.Author),
		URL:        strings.TrimSpace(raw.Links.HTML.Href),
		CreatedAt:  strings.TrimSpace(raw.CreatedOn),
		UpdatedAt:  strings.TrimSpace(raw.UpdatedOn),
	}
}

func userFromAPI(raw apiUser) model.User {
	return model.User{
		System:   "bitbucket",
		Login:    strings.TrimSpace(firstNonEmpty(raw.Nickname, raw.AccountID, raw.UUID)),
		Name:     strings.TrimSpace(raw.DisplayName),
		URL:      strings.TrimSpace(raw.Links.HTML.Href),
		IsActive: true,
	}
}

func parseTimeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultTimeout, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse Bitbucket integration timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("parse Bitbucket integration timeout: duration must be positive")
	}
	return timeout, nil
}

func failureForHTTPStatus(status int, err error, body string) *model.Failure {
	return &model.Failure{
		Kind:        failureKindForHTTPStatus(status),
		Retryable:   status == http.StatusTooManyRequests || status >= 500 || status == 0,
		Message:     err.Error(),
		Diagnostics: []string{strings.TrimSpace(body)},
	}
}

func failureKindForHTTPStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return model.FailureKindAuthRequired
	case http.StatusForbidden:
		return model.FailureKindPermissionDenied
	case http.StatusNotFound:
		return model.FailureKindNotFound
	case http.StatusTooManyRequests:
		return model.FailureKindRateLimited
	case http.StatusConflict:
		return model.FailureKindStateConflict
	case 0:
		return model.FailureKindTemporaryUnavailable
	default:
		if status >= 500 {
			return model.FailureKindTemporaryUnavailable
		}
		return model.FailureKindExternalFailure
	}
}

func operationResult(req model.ProviderRequest, status int, method string, target string, err error, body []byte) *model.OperationResult {
	result := &model.OperationResult{
		System:       "bitbucket",
		ObjectType:   req.ObjectType,
		Operation:    req.Operation,
		Status:       model.ResponseStatusFailed,
		HTTPStatus:   status,
		Method:       method,
		Endpoint:     target,
		ResponseBody: string(body),
	}
	if err != nil {
		result.Message = err.Error()
		result.Failure = failureForHTTPStatus(status, err, string(body))
	}
	return result
}

func responseWithDecodeFailure(response model.Response, err error) (model.Response, error) {
	response.Status = model.ResponseStatusFailed
	response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: fmt.Sprintf("decode Bitbucket API response: %v", err)}
	return response, err
}

func statusToExitCode(status int) int {
	if status >= 200 && status < 300 {
		return 0
	}
	return 1
}

func isRepositoryObject(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "repository" || object == "repo"
}

func isMergeRequestObject(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "merge-request" || object == "pull-request" || object == "pr" || object == "mr"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
