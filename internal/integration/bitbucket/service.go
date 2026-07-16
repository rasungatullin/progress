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

	apiVariantCloud  = "cloud"
	apiVariantServer = "server"
	serverAPIPrefix  = "rest/api/1.0"
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

type serverLinks struct {
	Self []struct {
		Href string `json:"href"`
	} `json:"self"`
	Clone []struct {
		Name string `json:"name"`
		Href string `json:"href"`
	} `json:"clone"`
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

type apiPullRequestPage struct {
	Values []apiPullRequest `json:"values"`
	Next   string           `json:"next"`
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
	Next   string       `json:"next"`
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
	Parent *struct {
		ID int `json:"id"`
	} `json:"parent"`
}

type apiUserResponse struct {
	UUID        string `json:"uuid"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
}

type serverRepository struct {
	ID          int    `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SCMID       string `json:"scmId"`
	State       string `json:"state"`
	Public      bool   `json:"public"`
	Project     struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"project"`
	Links serverLinks `json:"links"`
}

type serverBranch struct {
	ID        string `json:"id"`
	DisplayID string `json:"displayId"`
	IsDefault bool   `json:"isDefault"`
	Type      string `json:"type"`
}

type serverUser struct {
	Name         string      `json:"name"`
	Slug         string      `json:"slug"`
	DisplayName  string      `json:"displayName"`
	EmailAddress string      `json:"emailAddress"`
	Active       bool        `json:"active"`
	Links        serverLinks `json:"links"`
}

type serverPullRequest struct {
	ID          int    `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	CreatedDate int64  `json:"createdDate"`
	UpdatedDate int64  `json:"updatedDate"`
	Author      struct {
		User serverUser `json:"user"`
	} `json:"author"`
	FromRef struct {
		ID        string `json:"id"`
		DisplayID string `json:"displayId"`
	} `json:"fromRef"`
	ToRef struct {
		ID        string `json:"id"`
		DisplayID string `json:"displayId"`
	} `json:"toRef"`
	Links serverLinks `json:"links"`
}

type serverActivityPage struct {
	Values        []serverActivity `json:"values"`
	IsLastPage    bool             `json:"isLastPage"`
	NextPageStart int              `json:"nextPageStart"`
}

type serverPullRequestPage struct {
	Values        []serverPullRequest `json:"values"`
	IsLastPage    bool                `json:"isLastPage"`
	NextPageStart int                 `json:"nextPageStart"`
}

type serverActivity struct {
	ID          int            `json:"id"`
	CreatedDate int64          `json:"createdDate"`
	User        serverUser     `json:"user"`
	Comment     *serverComment `json:"comment"`
}

type serverComment struct {
	ID          int             `json:"id"`
	Text        string          `json:"text"`
	Author      serverUser      `json:"author"`
	CreatedDate int64           `json:"createdDate"`
	UpdatedDate int64           `json:"updatedDate"`
	Anchor      *serverAnchor   `json:"anchor"`
	Comments    []serverComment `json:"comments"`
	Links       serverLinks     `json:"links"`
}

type serverAnchor struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	LineType string `json:"lineType"`
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
	case isMergeRequestObject(req) && (req.Operation == "list" || req.Operation == "search"):
		return s.executePullRequestList(ctx, response, req)
	case isMergeRequestObject(req) && req.Operation == "create":
		return s.executePullRequestCreate(ctx, response, req)
	case (isMergeRequestObject(req) && req.Operation == "comments") || (isMergeRequestCommentObject(req) && (req.Operation == "list" || req.Operation == "comments")):
		return s.executePullRequestComments(ctx, response, req)
	case isMergeRequestCommentObject(req) && req.Operation == "create":
		return s.executePullRequestCommentCreate(ctx, response, req)
	case isMergeRequestCommentObject(req) && req.Operation == "resolve":
		err := fmt.Errorf("Bitbucket comment resolve is not supported by current integration adapter")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
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
	if s.apiVariant() == apiVariantServer {
		return s.executeServerAuthStatus(ctx, response)
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

func (s *Service) executeServerAuthStatus(ctx context.Context, response model.Response) (model.Response, error) {
	endpoint := s.serverEndpoint("projects?limit=1")
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	auth := &model.AuthStatus{
		System:      "bitbucket",
		State:       "ready",
		Available:   true,
		Command:     "http",
		ExitCode:    statusToExitCode(status),
		Stdout:      string(body),
		Diagnostics: []string{"endpoint=" + endpoint, "api_variant=server"},
	}
	if err != nil {
		auth.State = failureKindForHTTPStatus(status)
		auth.Message = err.Error()
		response.AuthStatus = auth
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		return response, err
	}

	auth.Authenticated = true
	auth.Message = "Bitbucket Server authorization is available"
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
	if s.apiVariant() == apiVariantServer {
		return s.executeServerRepositoryGet(ctx, response, req, repository)
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

func (s *Service) executeServerRepositoryGet(ctx context.Context, response model.Response, req model.ProviderRequest, repository repositoryRef) (model.Response, error) {
	endpoint := s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s", url.PathEscape(repository.workspace), url.PathEscape(repository.slug)))
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
		return response, err
	}

	var raw serverRepository
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}

	defaultBranch := ""
	branchEndpoint := s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/branches/default", url.PathEscape(repository.workspace), url.PathEscape(repository.slug)))
	if _, branchBody, branchErr := s.do(ctx, http.MethodGet, branchEndpoint, nil); branchErr == nil {
		var branch serverBranch
		if json.Unmarshal(branchBody, &branch) == nil {
			defaultBranch = strings.TrimSpace(firstNonEmpty(branch.DisplayID, branch.ID))
		}
	}

	owner := strings.TrimSpace(firstNonEmpty(raw.Project.Key, repository.workspace))
	name := strings.TrimSpace(firstNonEmpty(raw.Slug, raw.Name, repository.slug))
	response.Repository = &model.Repository{
		System:        "bitbucket",
		ExternalID:    strconv.Itoa(raw.ID),
		FullName:      owner + "/" + name,
		Owner:         owner,
		Name:          name,
		Description:   strings.TrimSpace(raw.Description),
		DefaultBranch: defaultBranch,
		URL:           firstServerCloneURL(raw.Links),
		Traits:        []string{"server"},
		Attributes: map[string]string{
			"api_variant": apiVariantServer,
			"scm_id":      strings.TrimSpace(raw.SCMID),
			"state":       strings.TrimSpace(raw.State),
			"public":      strconv.FormatBool(raw.Public),
		},
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func firstServerCloneURL(links serverLinks) string {
	for _, link := range links.Clone {
		if strings.EqualFold(strings.TrimSpace(link.Name), "ssh") && strings.TrimSpace(link.Href) != "" {
			return strings.TrimSpace(link.Href)
		}
	}
	for _, link := range links.Clone {
		if strings.TrimSpace(link.Href) != "" {
			return strings.TrimSpace(link.Href)
		}
	}
	return firstServerSelfLink(links)
}

func (s *Service) executePullRequestGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if req.MergeRequestNumber <= 0 {
		err := fmt.Errorf("Bitbucket pull request number must be greater than zero")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if s.apiVariant() == apiVariantServer {
		return s.executeServerPullRequestGet(ctx, response, req, repository)
	}

	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests/%d", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.MergeRequestNumber)
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

func (s *Service) executeServerPullRequestGet(ctx context.Context, response model.Response, req model.ProviderRequest, repository repositoryRef) (model.Response, error) {
	endpoint := s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/pull-requests/%d", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.MergeRequestNumber))
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
		return response, err
	}

	var raw serverPullRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}
	response.MergeRequest = mergeRequestFromServerAPI(repository.fullName, raw)
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePullRequestList(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	state, states, err := normalizePullRequestListState(req.State)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	scope, err := normalizePullRequestListScope(req.Scope)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 30
	}

	if s.apiVariant() == apiVariantServer {
		return s.executeServerPullRequestList(ctx, response, req, repository, state, scope, limit)
	}

	query := url.Values{}
	for _, item := range states {
		query.Add("state", item)
	}
	query.Set("pagelen", strconv.Itoa(limit))
	filter := strings.TrimSpace(req.Query)
	if scope != "all" {
		currentUser, userErr := s.currentCloudUser(ctx)
		if userErr != nil {
			response.Status = model.ResponseStatusFailed
			response.Failure = failureForHTTPStatus(0, userErr, "")
			return response, userErr
		}
		userUUID := strings.TrimSpace(currentUser.UUID)
		if userUUID == "" {
			err := fmt.Errorf("Bitbucket current user uuid is required for pull request scope %s", scope)
			response.Status = model.ResponseStatusFailed
			response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: err.Error()}
			return response, err
		}
		scopeFilter := fmt.Sprintf(`author.uuid="%s"`, userUUID)
		if scope == "reviewer" {
			scopeFilter = fmt.Sprintf(`reviewers.uuid="%s"`, userUUID)
		}
		filter = combineBitbucketFilters(filter, scopeFilter)
	}
	if filter != "" {
		query.Set("q", filter)
	}

	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests?%s", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), query.Encode())
	items := make([]apiPullRequest, 0, limit)
	for endpoint != "" && len(items) < limit {
		status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			response.Status = model.ResponseStatusFailed
			response.Failure = failureForHTTPStatus(status, err, string(body))
			response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
			return response, err
		}

		var raw apiPullRequestPage
		if err := json.Unmarshal(body, &raw); err != nil {
			return responseWithDecodeFailure(response, err)
		}
		for _, item := range raw.Values {
			if len(items) >= limit {
				break
			}
			items = append(items, item)
		}
		endpoint = strings.TrimSpace(raw.Next)
	}

	response.MergeRequests = make([]model.MergeRequest, 0, len(items))
	response.SearchResults = make([]model.TrackerSearchResult, 0, len(items))
	for _, item := range items {
		pr := *mergeRequestFromAPI(repository.fullName, item)
		response.MergeRequests = append(response.MergeRequests, pr)
		response.SearchResults = append(response.SearchResults, model.TrackerSearchResult{
			System:     "bitbucket",
			Repository: repository.fullName,
			Kind:       "merge-request",
			ID:         strconv.Itoa(pr.Number),
			Title:      pr.Title,
			State:      pr.State,
			URL:        pr.URL,
			UpdatedAt:  pr.UpdatedAt,
		})
	}
	response.Metadata = map[string]string{
		"repository": repository.fullName,
		"state":      state,
		"scope":      scope,
		"limit":      strconv.Itoa(limit),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeServerPullRequestList(ctx context.Context, response model.Response, req model.ProviderRequest, repository repositoryRef, state string, scope string, limit int) (model.Response, error) {
	query := url.Values{}
	query.Set("state", bitbucketServerListState(state))
	query.Set("limit", strconv.Itoa(limit))
	if strings.TrimSpace(req.Query) != "" {
		query.Set("filterText", strings.TrimSpace(req.Query))
	}
	if scope == "authored" {
		query.Set("role", "AUTHOR")
	} else if scope == "reviewer" {
		query.Set("role", "REVIEWER")
	}
	endpoint := s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/pull-requests?%s", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), query.Encode()))
	items := make([]serverPullRequest, 0, limit)
	for endpoint != "" && len(items) < limit {
		status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			response.Status = model.ResponseStatusFailed
			response.Failure = failureForHTTPStatus(status, err, string(body))
			response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
			return response, err
		}

		var raw serverPullRequestPage
		if err := json.Unmarshal(body, &raw); err != nil {
			return responseWithDecodeFailure(response, err)
		}
		for _, item := range raw.Values {
			if len(items) >= limit {
				break
			}
			if state == "closed" && item.State != "MERGED" && item.State != "DECLINED" {
				continue
			}
			items = append(items, item)
		}
		if raw.IsLastPage || raw.NextPageStart <= 0 {
			break
		}
		nextQuery := url.Values{}
		nextQuery.Set("state", bitbucketServerListState(state))
		nextQuery.Set("limit", strconv.Itoa(limit))
		if strings.TrimSpace(req.Query) != "" {
			nextQuery.Set("filterText", strings.TrimSpace(req.Query))
		}
		if scope == "authored" {
			nextQuery.Set("role", "AUTHOR")
		} else if scope == "reviewer" {
			nextQuery.Set("role", "REVIEWER")
		}
		nextQuery.Set("start", strconv.Itoa(raw.NextPageStart))
		endpoint = s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/pull-requests?%s", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), nextQuery.Encode()))
	}

	response.MergeRequests = make([]model.MergeRequest, 0, len(items))
	response.SearchResults = make([]model.TrackerSearchResult, 0, len(items))
	for _, item := range items {
		pr := *mergeRequestFromServerAPI(repository.fullName, item)
		response.MergeRequests = append(response.MergeRequests, pr)
		response.SearchResults = append(response.SearchResults, model.TrackerSearchResult{
			System:     "bitbucket",
			Repository: repository.fullName,
			Kind:       "merge-request",
			ID:         strconv.Itoa(pr.Number),
			Title:      pr.Title,
			State:      pr.State,
			URL:        pr.URL,
			UpdatedAt:  pr.UpdatedAt,
		})
	}
	response.Metadata = map[string]string{
		"repository": repository.fullName,
		"state":      state,
		"scope":      scope,
		"limit":      strconv.Itoa(limit),
	}
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
	if s.apiVariant() == apiVariantServer {
		return s.executeServerPullRequestCreate(ctx, response, req, repository)
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
	if req.Draft {
		payload["draft"] = true
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

func (s *Service) executeServerPullRequestCreate(ctx context.Context, response model.Response, req model.ProviderRequest, repository repositoryRef) (model.Response, error) {
	payload := map[string]any{
		"title":       strings.TrimSpace(req.Title),
		"description": strings.TrimSpace(req.Body),
		"fromRef": map[string]any{
			"id": branchRefID(req.Head),
			"repository": map[string]any{
				"slug": repository.slug,
				"project": map[string]string{
					"key": repository.workspace,
				},
			},
		},
		"toRef": map[string]any{
			"id": branchRefID(req.Base),
			"repository": map[string]any{
				"slug": repository.slug,
				"project": map[string]string{
					"key": repository.workspace,
				},
			},
		},
	}
	if req.Draft {
		payload["draft"] = true
	}
	content, _ := json.Marshal(payload)
	endpoint := s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/pull-requests", url.PathEscape(repository.workspace), url.PathEscape(repository.slug)))
	status, body, err := s.do(ctx, http.MethodPost, endpoint, content)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodPost, repository.fullName, err, body)
		return response, err
	}

	var raw serverPullRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}
	response.MergeRequest = mergeRequestFromServerAPI(repository.fullName, raw)
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
	if req.MergeRequestNumber <= 0 {
		err := fmt.Errorf("Bitbucket pull request number must be greater than zero")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if s.apiVariant() == apiVariantServer {
		return s.executeServerPullRequestComments(ctx, response, req, repository)
	}

	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests/%d/comments", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.MergeRequestNumber)
	comments := make([]apiComment, 0)
	for endpoint != "" {
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
		comments = append(comments, raw.Values...)
		endpoint = strings.TrimSpace(raw.Next)
	}
	response.ReviewRemarks = []model.ReviewRemark{}
	byID := make(map[int]apiComment, len(comments))
	for _, item := range comments {
		byID[item.ID] = item
	}
	for _, item := range comments {
		inline := commentIsInline(item, byID)
		if isFilteredCommentRequest(req) && isReviewRemarkRequest(req) != inline {
			continue
		}
		remark := reviewRemarkFromAPIComment(repository.fullName, req.MergeRequestNumber, item)
		if item.Parent != nil {
			remark.ReplyToID = strconv.Itoa(item.Parent.ID)
		}
		if inline && remark.Path == "" {
			remark.Type = "inline-reply"
		}
		response.ReviewRemarks = append(response.ReviewRemarks, remark)
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func commentIsInline(item apiComment, byID map[int]apiComment) bool {
	seen := map[int]bool{}
	for {
		if item.Inline != nil {
			return true
		}
		if item.Parent == nil || item.Parent.ID == 0 || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
		parent, ok := byID[item.Parent.ID]
		if !ok {
			return false
		}
		item = parent
	}
}

func (s *Service) executePullRequestCommentCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := s.resolveRepository(req.Repository, req.RepoProvided)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if req.MergeRequestNumber <= 0 {
		err := fmt.Errorf("Bitbucket pull request number must be greater than zero")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	body := strings.TrimSpace(firstNonEmpty(req.Text, req.Body))
	if body == "" {
		err := fmt.Errorf("Bitbucket pull request comment body is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if strings.TrimSpace(req.Path) == "" && req.Line > 0 {
		err := fmt.Errorf("Bitbucket pull request inline comment path is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if s.apiVariant() == apiVariantServer {
		err := fmt.Errorf("Bitbucket Server pull request comment create is not supported by current integration adapter")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
	}

	payload := map[string]any{
		"content": map[string]string{"raw": body},
	}
	if strings.TrimSpace(req.Path) != "" {
		if req.Line <= 0 {
			err := fmt.Errorf("Bitbucket pull request inline comment line must be greater than zero")
			response.Status = model.ResponseStatusFailed
			response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
			return response, err
		}
		inline := map[string]any{"path": strings.TrimSpace(req.Path)}
		if strings.EqualFold(strings.TrimSpace(req.Side), "LEFT") {
			inline["from"] = req.Line
		} else {
			inline["to"] = req.Line
		}
		payload["inline"] = inline
	}
	content, _ := json.Marshal(payload)
	endpoint := fmt.Sprintf("repositories/%s/%s/pullrequests/%d/comments", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.MergeRequestNumber)
	status, responseBody, err := s.do(ctx, http.MethodPost, endpoint, content)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(responseBody))
		response.OperationResult = operationResult(req, status, http.MethodPost, repository.fullName, err, responseBody)
		return response, err
	}

	var raw apiComment
	if err := json.Unmarshal(responseBody, &raw); err != nil {
		return responseWithDecodeFailure(response, err)
	}
	remark := reviewRemarkFromAPIComment(repository.fullName, req.MergeRequestNumber, raw)
	response.ReviewRemarks = []model.ReviewRemark{remark}
	response.OperationResult = &model.OperationResult{
		System:     "bitbucket",
		ObjectType: "review-remark",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: remark.ExternalID,
		URL:        remark.URL,
		HTTPStatus: status,
		Method:     http.MethodPost,
		Endpoint:   endpoint,
		Message:    fmt.Sprintf("Bitbucket pull request comment created for %s#%d", repository.fullName, req.MergeRequestNumber),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeServerPullRequestComments(ctx context.Context, response model.Response, req model.ProviderRequest, repository repositoryRef) (model.Response, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	endpoint := s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/pull-requests/%d/activities?limit=%d", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.MergeRequestNumber, limit))
	var pages []serverActivity
	for endpoint != "" {
		status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			response.Status = model.ResponseStatusFailed
			response.Failure = failureForHTTPStatus(status, err, string(body))
			response.OperationResult = operationResult(req, status, http.MethodGet, repository.fullName, err, body)
			return response, err
		}

		var raw serverActivityPage
		if err := json.Unmarshal(body, &raw); err != nil {
			return responseWithDecodeFailure(response, err)
		}
		pages = append(pages, raw.Values...)
		if raw.IsLastPage || raw.NextPageStart <= 0 {
			break
		}
		endpoint = s.serverEndpoint(fmt.Sprintf("projects/%s/repos/%s/pull-requests/%d/activities?limit=%d&start=%d", url.PathEscape(repository.workspace), url.PathEscape(repository.slug), req.MergeRequestNumber, limit, raw.NextPageStart))
	}
	response.ReviewRemarks = []model.ReviewRemark{}
	for _, item := range pages {
		if item.Comment == nil {
			continue
		}
		remarks := appendServerCommentRemarks(nil, repository.fullName, req.MergeRequestNumber, *item.Comment)
		for _, remark := range remarks {
			if !isFilteredCommentRequest(req) || isReviewRemarkRequest(req) == (strings.TrimSpace(remark.Path) != "") {
				response.ReviewRemarks = append(response.ReviewRemarks, remark)
			}
		}
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func isReviewRemarkRequest(req model.ProviderRequest) bool {
	return strings.TrimSpace(req.ObjectType) == "review-remark"
}

func isFilteredCommentRequest(req model.ProviderRequest) bool {
	object := strings.TrimSpace(req.ObjectType)
	return object == "review-remark" || object == "merge-request-comment"
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
	if strings.HasPrefix(strings.ToLower(endpoint), "http://") || strings.HasPrefix(strings.ToLower(endpoint), "https://") {
		requestURL = endpoint
	}
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
	if s.apiVariant() == apiVariantServer {
		workspace = strings.TrimSpace(firstNonEmpty(s.config.Project, s.config.Workspace))
	}
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

func (s *Service) apiVariant() string {
	raw := strings.TrimSpace(strings.ToLower(s.config.APIVariant))
	switch raw {
	case apiVariantCloud, "bitbucket-cloud":
		return apiVariantCloud
	case apiVariantServer, "bitbucket-server", "data-center", "datacenter", "stash":
		return apiVariantServer
	}

	base := strings.TrimRight(strings.ToLower(strings.TrimSpace(s.config.BaseURL)), "/")
	if strings.Contains(base, "/rest/api/") || strings.Contains(base, "://stash.") || strings.Contains(base, ".stash.") {
		return apiVariantServer
	}
	return apiVariantCloud
}

func (s *Service) serverEndpoint(endpoint string) string {
	endpoint = strings.TrimLeft(endpoint, "/")
	base := strings.TrimRight(strings.ToLower(strings.TrimSpace(s.config.BaseURL)), "/")
	if strings.HasSuffix(base, "/rest/api/1.0") || strings.HasSuffix(base, "/rest/api/latest") {
		return endpoint
	}
	return serverAPIPrefix + "/" + endpoint
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

func (s *Service) currentCloudUser(ctx context.Context) (apiUserResponse, error) {
	status, body, err := s.do(ctx, http.MethodGet, "user", nil)
	if err != nil {
		return apiUserResponse{}, fmt.Errorf("Bitbucket current user request failed with status %d: %w", status, err)
	}
	var raw apiUserResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return apiUserResponse{}, err
	}
	return raw, nil
}

func normalizePullRequestListState(raw string) (string, []string, error) {
	state := strings.TrimSpace(strings.ToLower(raw))
	switch state {
	case "":
		return "closed", []string{"MERGED", "DECLINED"}, nil
	case "open":
		return "open", []string{"OPEN"}, nil
	case "closed":
		return "closed", []string{"MERGED", "DECLINED"}, nil
	case "merged":
		return "merged", []string{"MERGED"}, nil
	case "declined":
		return "declined", []string{"DECLINED"}, nil
	case "all":
		return "all", []string{"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}, nil
	default:
		return "", nil, fmt.Errorf("Bitbucket pull request state must be one of open, closed, merged, declined or all")
	}
}

func normalizePullRequestListScope(raw string) (string, error) {
	scope := strings.TrimSpace(strings.ToLower(raw))
	switch scope {
	case "", "all":
		return "all", nil
	case "author", "authored", "mine":
		return "authored", nil
	case "reviewer", "reviewed", "review":
		return "reviewer", nil
	default:
		return "", fmt.Errorf("Bitbucket pull request scope must be one of all, authored or reviewer")
	}
}

func combineBitbucketFilters(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch {
	case left == "":
		return right
	case right == "":
		return left
	default:
		return "(" + left + ") AND (" + right + ")"
	}
}

func bitbucketServerListState(state string) string {
	switch state {
	case "open":
		return "OPEN"
	case "merged":
		return "MERGED"
	case "declined":
		return "DECLINED"
	default:
		return "ALL"
	}
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

func reviewRemarkFromAPIComment(repository string, number int, item apiComment) model.ReviewRemark {
	remark := model.ReviewRemark{
		System:             "bitbucket",
		Repository:         repository,
		MergeRequestNumber: number,
		ExternalID:         strconv.Itoa(item.ID),
		Author:             userFromAPI(item.User),
		State:              "unresolved",
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
	return remark
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

func mergeRequestFromServerAPI(repository string, raw serverPullRequest) *model.MergeRequest {
	return &model.MergeRequest{
		System:     "bitbucket",
		Repository: repository,
		Number:     raw.ID,
		ExternalID: strconv.Itoa(raw.ID),
		Title:      strings.TrimSpace(raw.Title),
		Body:       raw.Description,
		State:      strings.TrimSpace(raw.State),
		Traits:     []string{"server"},
		Attributes: map[string]string{
			"api_variant": apiVariantServer,
			"version":     strconv.Itoa(raw.Version),
		},
		BaseRef:   strings.TrimSpace(firstNonEmpty(raw.ToRef.DisplayID, raw.ToRef.ID)),
		HeadRef:   strings.TrimSpace(firstNonEmpty(raw.FromRef.DisplayID, raw.FromRef.ID)),
		Author:    userFromServerAPI(raw.Author.User),
		URL:       firstServerSelfLink(raw.Links),
		CreatedAt: timestampMillisToRFC3339(raw.CreatedDate),
		UpdatedAt: timestampMillisToRFC3339(raw.UpdatedDate),
	}
}

func userFromServerAPI(raw serverUser) model.User {
	return model.User{
		System:   "bitbucket",
		Login:    strings.TrimSpace(firstNonEmpty(raw.Name, raw.Slug)),
		Name:     strings.TrimSpace(raw.DisplayName),
		Email:    strings.TrimSpace(raw.EmailAddress),
		URL:      firstServerSelfLink(raw.Links),
		IsActive: raw.Active,
	}
}

func appendServerCommentRemarks(remarks []model.ReviewRemark, repository string, number int, raw serverComment) []model.ReviewRemark {
	return appendServerCommentRemarksWithAnchor(remarks, repository, number, raw, raw.Anchor)
}

func appendServerCommentRemarksWithAnchor(remarks []model.ReviewRemark, repository string, number int, raw serverComment, inherited *serverAnchor) []model.ReviewRemark {
	remark := model.ReviewRemark{
		System:             "bitbucket",
		Repository:         repository,
		MergeRequestNumber: number,
		ExternalID:         strconv.Itoa(raw.ID),
		Author:             userFromServerAPI(raw.Author),
		Body:               raw.Text,
		URL:                firstServerSelfLink(raw.Links),
		CreatedAt:          timestampMillisToRFC3339(raw.CreatedDate),
		UpdatedAt:          timestampMillisToRFC3339(raw.UpdatedDate),
	}
	anchor := raw.Anchor
	if anchor == nil {
		anchor = inherited
	}
	if anchor != nil {
		remark.Path = strings.TrimSpace(anchor.Path)
		remark.Line = anchor.Line
		remark.Side = strings.TrimSpace(anchor.LineType)
	}
	remarks = append(remarks, remark)
	for _, child := range raw.Comments {
		remarks = appendServerCommentRemarksWithAnchor(remarks, repository, number, child, anchor)
	}
	return remarks
}

func firstServerSelfLink(links serverLinks) string {
	for _, link := range links.Self {
		if strings.TrimSpace(link.Href) != "" {
			return strings.TrimSpace(link.Href)
		}
	}
	return ""
}

func branchRefID(branch string) string {
	branch = strings.TrimSpace(branch)
	if strings.HasPrefix(branch, "refs/") {
		return branch
	}
	return "refs/heads/" + branch
}

func timestampMillisToRFC3339(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339)
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

func isMergeRequestCommentObject(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return (object == "comment" || object == "review-remark" || object == "merge-request-comment") && req.IntegrationType == model.IntegrationTypeRepository
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
