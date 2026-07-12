package confluence

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

const defaultTimeout = 30 * time.Second

type Service struct {
	config model.IntegrationSystemConfig
	client *http.Client
}

type apiUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	UserKey     string `json:"userKey"`
	AccountID   string `json:"accountId"`
	Email       string `json:"email"`
	Type        string `json:"type"`
}

type apiContent struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Space  struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Body struct {
		Storage struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"storage"`
	} `json:"body"`
	Version struct {
		Number int     `json:"number"`
		When   string  `json:"when"`
		By     apiUser `json:"by"`
	} `json:"version"`
	History struct {
		CreatedDate string  `json:"createdDate"`
		CreatedBy   apiUser `json:"createdBy"`
	} `json:"history"`
	Links struct {
		Base  string `json:"base"`
		Self  string `json:"self"`
		WebUI string `json:"webui"`
	} `json:"_links"`
}

type apiContentSearch struct {
	Results []apiContent `json:"results"`
	Size    int          `json:"size"`
	Limit   int          `json:"limit"`
	Start   int          `json:"start"`
}

func NewService(config model.IntegrationSystemConfig) *Service {
	return &Service{config: config, client: http.DefaultClient}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: model.IntegrationTypeWiki,
		System:          "confluence",
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
	}

	switch {
	case req.Resource == "auth" && req.Operation == "status":
		return s.executeAuthStatus(ctx, response)
	case isPageObject(req) && req.Operation == "get":
		return s.executePageGet(ctx, response, req)
	case isPageObject(req) && (req.Operation == "search" || req.Operation == "list"):
		return s.executePageSearch(ctx, response, req)
	default:
		err := fmt.Errorf("Confluence integration does not support %s %s", firstNonEmpty(req.ObjectType, req.Resource), req.Operation)
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
	}
}

func (s *Service) executeAuthStatus(ctx context.Context, response model.Response) (model.Response, error) {
	if err := s.validateAccessConfig(); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindAuthRequired, Message: err.Error()}
		response.AuthStatus = &model.AuthStatus{System: "confluence", State: model.FailureKindAuthRequired, Available: false, Authenticated: false, Message: err.Error()}
		return response, err
	}

	endpoint := "user/current"
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	auth := &model.AuthStatus{
		System:      "confluence",
		State:       "ready",
		Available:   true,
		Command:     "http",
		ExitCode:    statusToExitCode(status),
		Stdout:      string(body),
		Diagnostics: []string{"endpoint=rest/api/" + endpoint},
	}
	if err != nil {
		auth.State = failureKindForHTTPStatus(status)
		auth.Message = err.Error()
		response.AuthStatus = auth
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		return response, err
	}

	var raw apiUser
	_ = json.Unmarshal(body, &raw)
	auth.Authenticated = true
	auth.Message = strings.TrimSpace(firstNonEmpty(raw.DisplayName, raw.Username, raw.UserKey, "Confluence authorization is available"))
	response.AuthStatus = auth
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePageGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	pageID := strings.TrimSpace(req.ExternalID)
	pageID = firstNonEmpty(pageID, strings.TrimSpace(req.ID))
	if pageID == "" {
		err := fmt.Errorf("Confluence page id is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	query := url.Values{}
	query.Set("expand", "space,body.storage,version,history")
	endpoint := "content/" + url.PathEscape(pageID) + "?" + query.Encode()
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, endpoint, err, body)
		return response, err
	}

	var raw apiContent
	if err := json.Unmarshal(body, &raw); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: fmt.Sprintf("decode Confluence page response: %v", err)}
		return response, err
	}
	page := s.pageFromContent(raw)
	response.WikiPage = &page
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executePageSearch(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	cql := strings.TrimSpace(req.Query)
	if cql == "" {
		err := fmt.Errorf("Confluence CQL query is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	query := url.Values{}
	query.Set("cql", cql)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("expand", "space,version")
	endpoint := "content/search?" + query.Encode()
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, endpoint, err, body)
		return response, err
	}

	var raw apiContentSearch
	if err := json.Unmarshal(body, &raw); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: fmt.Sprintf("decode Confluence search response: %v", err)}
		return response, err
	}

	response.WikiPages = make([]model.WikiPage, 0, len(raw.Results))
	for _, item := range raw.Results {
		response.WikiPages = append(response.WikiPages, s.pageFromContent(item))
	}
	response.Metadata = map[string]string{
		"query": cql,
		"limit": strconv.Itoa(limit),
		"size":  strconv.Itoa(raw.Size),
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) pageFromContent(raw apiContent) model.WikiPage {
	page := model.WikiPage{
		System:     "confluence",
		Space:      strings.TrimSpace(firstNonEmpty(raw.Space.Key, raw.Space.Name)),
		ExternalID: strings.TrimSpace(raw.ID),
		Title:      strings.TrimSpace(raw.Title),
		Body:       raw.Body.Storage.Value,
		BodyFormat: strings.TrimSpace(raw.Body.Storage.Representation),
		Version:    raw.Version.Number,
		URL:        s.contentURL(raw),
		CreatedAt:  strings.TrimSpace(raw.History.CreatedDate),
		UpdatedAt:  strings.TrimSpace(raw.Version.When),
		UpdatedBy:  userFromAPI(raw.Version.By),
		Traits:     []string{strings.TrimSpace(raw.Type), strings.TrimSpace(raw.Status)},
		Attributes: map[string]string{
			"type":   strings.TrimSpace(raw.Type),
			"status": strings.TrimSpace(raw.Status),
		},
	}
	page.Traits = trimStrings(page.Traits)
	if raw.Space.Name != "" {
		page.Attributes["space_name"] = strings.TrimSpace(raw.Space.Name)
	}
	if page.BodyFormat == "" && page.Body != "" {
		page.BodyFormat = "storage"
	}
	if page.UpdatedBy.Login == "" {
		page.UpdatedBy = userFromAPI(raw.History.CreatedBy)
	}
	return page
}

func (s *Service) do(ctx context.Context, method string, endpoint string, payload []byte) (int, []byte, error) {
	if err := s.validateAccessConfig(); err != nil {
		return 0, nil, err
	}
	timeout, err := parseTimeout(s.config.Timeout)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	requestURL := s.apiBaseURL() + "/" + strings.TrimLeft(endpoint, "/")
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
	s.applyAuth(request)

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
		return response.StatusCode, content, fmt.Errorf("Confluence API returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, content, nil
}

func (s *Service) validateAccessConfig() error {
	if strings.TrimSpace(s.config.BaseURL) == "" {
		return fmt.Errorf("Confluence base_url is required")
	}
	if s.token() == "" {
		return fmt.Errorf("Confluence token is required")
	}
	return nil
}

func (s *Service) applyAuth(request *http.Request) {
	token := s.token()
	username := strings.TrimSpace(s.config.Username)
	if username != "" {
		request.SetBasicAuth(username, token)
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
}

func (s *Service) token() string {
	if value := strings.TrimSpace(s.config.Token); value != "" {
		return value
	}
	if env := strings.TrimSpace(s.config.TokenEnv); env != "" {
		return strings.TrimSpace(os.Getenv(env))
	}
	return ""
}

func (s *Service) apiBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(s.config.BaseURL), "/")
	if strings.HasSuffix(base, "/rest/api") {
		return base
	}
	return base + "/rest/api"
}

func (s *Service) webBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(s.config.BaseURL), "/")
	if strings.HasSuffix(base, "/rest/api") {
		return strings.TrimSuffix(base, "/rest/api")
	}
	return base
}

func (s *Service) contentURL(raw apiContent) string {
	if absoluteURL(raw.Links.WebUI) {
		return strings.TrimSpace(raw.Links.WebUI)
	}
	base := strings.TrimRight(firstNonEmpty(raw.Links.Base, s.webBaseURL()), "/")
	if raw.Links.WebUI != "" && base != "" {
		return base + "/" + strings.TrimLeft(raw.Links.WebUI, "/")
	}
	return strings.TrimSpace(raw.Links.Self)
}

func operationResult(req model.ProviderRequest, status int, method string, endpoint string, err error, body []byte) *model.OperationResult {
	result := &model.OperationResult{
		System:       "confluence",
		ObjectType:   firstNonEmpty(req.ObjectType, req.Resource),
		Operation:    req.Operation,
		Status:       model.ResponseStatusFailed,
		HTTPStatus:   status,
		Method:       method,
		Endpoint:     endpoint,
		ResponseBody: string(body),
	}
	if err != nil {
		result.Message = err.Error()
		result.Failure = failureForHTTPStatus(status, err, string(body))
	}
	return result
}

func failureForHTTPStatus(status int, err error, body string) *model.Failure {
	if err == nil {
		return nil
	}
	failure := &model.Failure{
		Kind:      failureKindForHTTPStatus(status),
		Retryable: status == http.StatusTooManyRequests || status >= 500,
		Message:   err.Error(),
	}
	if body != "" {
		failure.Diagnostics = []string{"response_body=" + body}
	}
	return failure
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
	case 0:
		return model.FailureKindInvalidRequest
	default:
		if status >= 500 {
			return model.FailureKindTemporaryUnavailable
		}
		return model.FailureKindExternalFailure
	}
}

func statusToExitCode(status int) int {
	if status >= 200 && status < 300 {
		return 0
	}
	if status == 0 {
		return -1
	}
	return status
}

func parseTimeout(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultTimeout, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse Confluence timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("Confluence timeout must be positive")
	}
	return timeout, nil
}

func userFromAPI(raw apiUser) model.User {
	return model.User{
		System:   "confluence",
		Login:    strings.TrimSpace(firstNonEmpty(raw.Username, raw.UserKey, raw.AccountID)),
		Name:     strings.TrimSpace(raw.DisplayName),
		Email:    strings.TrimSpace(raw.Email),
		IsActive: raw.Type == "" || raw.Type == "known",
	}
}

func isPageObject(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "page" || object == "wiki-page" || object == "document" || object == "doc"
}

func trimStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func absoluteURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
