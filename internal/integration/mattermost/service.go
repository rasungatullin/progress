package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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
	ID       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
}

type apiPostThread struct {
	Order []string           `json:"order"`
	Posts map[string]apiPost `json:"posts"`
}

type apiPost struct {
	ID        string         `json:"id"`
	RootID    string         `json:"root_id"`
	ChannelID string         `json:"channel_id"`
	UserID    string         `json:"user_id"`
	Message   string         `json:"message"`
	CreateAt  int64          `json:"create_at"`
	UpdateAt  int64          `json:"update_at"`
	Props     map[string]any `json:"props"`
}

func NewService(config model.IntegrationSystemConfig) *Service {
	return &Service{config: config, client: http.DefaultClient}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: model.IntegrationTypeMessenger,
		System:          "mattermost",
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
	}

	switch {
	case req.Resource == "auth" && req.Operation == "status":
		return s.executeAuthStatus(ctx, response)
	case isThreadObject(req) && req.Operation == "get":
		return s.executeThreadGet(ctx, response, req)
	case isMessageObject(req) && req.Operation == "create":
		return s.executeMessageCreate(ctx, response, req)
	default:
		err := fmt.Errorf("Mattermost integration does not support %s %s", firstNonEmpty(req.ObjectType, req.Resource), req.Operation)
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
	}
}

func (s *Service) executeAuthStatus(ctx context.Context, response model.Response) (model.Response, error) {
	if err := s.validateAccessConfig(); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindAuthRequired, Message: err.Error()}
		response.AuthStatus = &model.AuthStatus{System: "mattermost", State: model.FailureKindAuthRequired, Available: false, Authenticated: false, Message: err.Error()}
		return response, err
	}

	status, body, err := s.do(ctx, http.MethodGet, "api/v4/users/me", nil)
	auth := &model.AuthStatus{
		System:      "mattermost",
		State:       "ready",
		Available:   true,
		Command:     "http",
		ExitCode:    statusToExitCode(status),
		Stdout:      string(body),
		Diagnostics: []string{"endpoint=api/v4/users/me"},
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
	auth.Message = strings.TrimSpace(firstNonEmpty(raw.Username, raw.Nickname, raw.ID, "Mattermost authorization is available"))
	response.AuthStatus = auth
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeThreadGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	threadID := strings.TrimSpace(firstNonEmpty(req.ThreadID, req.MessageID, req.ExternalID))
	if threadID == "" {
		err := fmt.Errorf("Mattermost thread id is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	endpoint := "api/v4/posts/" + threadID + "/thread"
	status, body, err := s.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodGet, endpoint, err, body)
		return response, err
	}

	var raw apiPostThread
	if err := json.Unmarshal(body, &raw); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: fmt.Sprintf("decode Mattermost thread response: %v", err)}
		return response, err
	}

	messages := make([]model.Message, 0, len(raw.Posts))
	order := raw.Order
	if len(order) == 0 {
		order = make([]string, 0, len(raw.Posts))
		for id := range raw.Posts {
			order = append(order, id)
		}
		sort.Strings(order)
	}
	for _, id := range order {
		post, ok := raw.Posts[id]
		if !ok {
			continue
		}
		messages = append(messages, messageFromPost(post))
	}

	response.Conversation = &model.MessageThread{
		System:   "mattermost",
		ThreadID: threadID,
		RootID:   threadID,
		Messages: messages,
	}
	if len(messages) > 0 {
		response.Conversation.SpaceID = messages[0].SpaceID
	}
	response.Messages = messages
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeMessageCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	channelID := strings.TrimSpace(firstNonEmpty(req.ChannelID, s.config.ChannelID))
	message := strings.TrimSpace(firstNonEmpty(req.Text, req.Body))
	if channelID == "" {
		err := fmt.Errorf("Mattermost channel id is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if message == "" {
		err := fmt.Errorf("Mattermost message text is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	payload := map[string]any{
		"channel_id": channelID,
		"message":    message,
	}
	if rootID := strings.TrimSpace(firstNonEmpty(req.ThreadID, req.MessageID)); rootID != "" {
		payload["root_id"] = rootID
	}
	content, _ := json.Marshal(payload)
	endpoint := "api/v4/posts"
	status, body, err := s.do(ctx, http.MethodPost, endpoint, content)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodPost, endpoint, err, body)
		return response, err
	}

	var raw apiPost
	if err := json.Unmarshal(body, &raw); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: fmt.Sprintf("decode Mattermost post response: %v", err)}
		return response, err
	}
	msg := messageFromPost(raw)
	response.Message = &msg
	response.OperationResult = &model.OperationResult{
		System:     "mattermost",
		ObjectType: "message",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: raw.ID,
		HTTPStatus: status,
		Method:     http.MethodPost,
		Endpoint:   endpoint,
		Message:    "Mattermost message created",
	}
	response.Status = model.ResponseStatusOK
	return response, nil
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

	base := strings.TrimRight(s.config.BaseURL, "/")
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
	request.Header.Set("Authorization", "Bearer "+s.token())

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	result, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer result.Body.Close()
	content, readErr := io.ReadAll(result.Body)
	if readErr != nil {
		return result.StatusCode, content, readErr
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return result.StatusCode, content, fmt.Errorf("Mattermost API returned HTTP status %d", result.StatusCode)
	}
	return result.StatusCode, content, nil
}

func (s *Service) validateAccessConfig() error {
	if strings.TrimSpace(s.config.BaseURL) == "" {
		return fmt.Errorf("Mattermost base_url is required")
	}
	if s.token() == "" {
		return fmt.Errorf("Mattermost token is required")
	}
	return nil
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

func messageFromPost(post apiPost) model.Message {
	threadID := strings.TrimSpace(post.RootID)
	if threadID == "" {
		threadID = strings.TrimSpace(post.ID)
	}
	return model.Message{
		System:    "mattermost",
		SpaceID:   strings.TrimSpace(post.ChannelID),
		ThreadID:  threadID,
		MessageID: strings.TrimSpace(post.ID),
		Author: model.User{
			System: "mattermost",
			Login:  strings.TrimSpace(post.UserID),
		},
		Body:      post.Message,
		CreatedAt: unixMillis(post.CreateAt),
		UpdatedAt: unixMillis(post.UpdateAt),
	}
}

func unixMillis(value int64) string {
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
		return 0, fmt.Errorf("parse Mattermost integration timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("parse Mattermost integration timeout: duration must be positive")
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
	case 0:
		return model.FailureKindTemporaryUnavailable
	default:
		if status >= 500 {
			return model.FailureKindTemporaryUnavailable
		}
		return model.FailureKindExternalFailure
	}
}

func operationResult(req model.ProviderRequest, status int, method string, endpoint string, err error, body []byte) *model.OperationResult {
	result := &model.OperationResult{
		System:       "mattermost",
		ObjectType:   req.ObjectType,
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

func statusToExitCode(status int) int {
	if status >= 200 && status < 300 {
		return 0
	}
	return 1
}

func isThreadObject(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "thread" || object == "discussion"
}

func isMessageObject(req model.ProviderRequest) bool {
	object := strings.TrimSpace(firstNonEmpty(req.ObjectType, req.Resource))
	return object == "message"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
