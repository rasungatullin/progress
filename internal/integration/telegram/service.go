package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/integration/model"
)

const (
	defaultBaseURL = "https://api.telegram.org"
	defaultTimeout = 30 * time.Second
)

type Service struct {
	config model.IntegrationSystemConfig
	client *http.Client
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      T      `json:"result"`
}

type apiUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type apiMessage struct {
	MessageID int64   `json:"message_id"`
	Date      int64   `json:"date"`
	Text      string  `json:"text"`
	From      apiUser `json:"from"`
	Chat      struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Title    string `json:"title"`
		Type     string `json:"type"`
	} `json:"chat"`
	ReplyToMessage *apiMessage `json:"reply_to_message"`
}

func NewService(config model.IntegrationSystemConfig) *Service {
	return &Service{config: config, client: http.DefaultClient}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: model.IntegrationTypeMessenger,
		System:          "telegram",
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
	}

	switch {
	case req.Resource == "auth" && req.Operation == "status":
		return s.executeAuthStatus(ctx, response)
	case isMessageObject(req) && req.Operation == "create":
		return s.executeMessageCreate(ctx, response, req)
	case isThreadObject(req) && req.Operation == "get":
		err := fmt.Errorf("Telegram Bot API does not support reading arbitrary message threads")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
	default:
		err := fmt.Errorf("Telegram integration does not support %s %s", firstNonEmpty(req.ObjectType, req.Resource), req.Operation)
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindUnsupportedOperation, Message: err.Error()}
		return response, err
	}
}

func (s *Service) executeAuthStatus(ctx context.Context, response model.Response) (model.Response, error) {
	if s.token() == "" {
		err := fmt.Errorf("Telegram bot token is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindAuthRequired, Message: err.Error()}
		response.AuthStatus = &model.AuthStatus{System: "telegram", State: model.FailureKindAuthRequired, Available: false, Authenticated: false, Message: err.Error()}
		return response, err
	}

	status, body, err := s.do(ctx, http.MethodGet, "getMe", nil)
	auth := &model.AuthStatus{
		System:      "telegram",
		State:       "ready",
		Available:   true,
		Command:     "http",
		ExitCode:    statusToExitCode(status),
		Stdout:      string(body),
		Diagnostics: []string{"endpoint=getMe"},
	}
	if err != nil {
		auth.State = failureKindForHTTPStatus(status)
		auth.Message = err.Error()
		response.AuthStatus = auth
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		return response, err
	}

	var raw apiResponse[apiUser]
	if err := json.Unmarshal(body, &raw); err == nil && raw.OK {
		auth.Authenticated = true
		auth.Message = strings.TrimSpace(firstNonEmpty(raw.Result.Username, raw.Result.FirstName, "Telegram authorization is available"))
	} else {
		auth.Authenticated = true
		auth.Message = "Telegram authorization is available"
	}
	response.AuthStatus = auth
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) executeMessageCreate(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	chatID := strings.TrimSpace(firstNonEmpty(req.ChannelID, s.config.ChatID, s.config.ChannelID))
	text := strings.TrimSpace(firstNonEmpty(req.Text, req.Body))
	if chatID == "" {
		err := fmt.Errorf("Telegram chat id is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}
	if text == "" {
		err := fmt.Errorf("Telegram message text is required")
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindInvalidRequest, Message: err.Error()}
		return response, err
	}

	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if replyID := strings.TrimSpace(firstNonEmpty(req.ThreadID, req.MessageID)); replyID != "" {
		if numeric, err := strconv.ParseInt(replyID, 10, 64); err == nil {
			payload["reply_to_message_id"] = numeric
		} else {
			payload["reply_to_message_id"] = replyID
		}
	}
	content, _ := json.Marshal(payload)
	status, body, err := s.do(ctx, http.MethodPost, "sendMessage", content)
	if err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = failureForHTTPStatus(status, err, string(body))
		response.OperationResult = operationResult(req, status, http.MethodPost, "sendMessage", err, body)
		return response, err
	}

	var raw apiResponse[apiMessage]
	if err := json.Unmarshal(body, &raw); err != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindPartialResponse, Retryable: true, Message: fmt.Sprintf("decode Telegram sendMessage response: %v", err)}
		return response, err
	}
	if !raw.OK {
		err := fmt.Errorf("Telegram API returned unsuccessful response: %s", strings.TrimSpace(raw.Description))
		response.Status = model.ResponseStatusFailed
		response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: err.Error()}
		return response, err
	}
	message := messageFromAPI(raw.Result)
	response.Message = &message
	response.OperationResult = &model.OperationResult{
		System:     "telegram",
		ObjectType: "message",
		Operation:  "create",
		Status:     model.ResponseStatusOK,
		ExternalID: strconv.FormatInt(raw.Result.MessageID, 10),
		HTTPStatus: status,
		Method:     http.MethodPost,
		Endpoint:   "sendMessage",
		Message:    "Telegram message created",
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) do(ctx context.Context, method string, endpoint string, payload []byte) (int, []byte, error) {
	token := s.token()
	if token == "" {
		return 0, nil, fmt.Errorf("Telegram bot token is required")
	}
	timeout, err := parseTimeout(s.config.Timeout)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	base := strings.TrimRight(firstNonEmpty(s.config.BaseURL, defaultBaseURL), "/")
	requestURL := base + "/bot" + token + "/" + strings.TrimLeft(endpoint, "/")
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
		return result.StatusCode, content, fmt.Errorf("Telegram API returned HTTP status %d", result.StatusCode)
	}
	return result.StatusCode, content, nil
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

func messageFromAPI(raw apiMessage) model.Message {
	chatID := strconv.FormatInt(raw.Chat.ID, 10)
	messageID := strconv.FormatInt(raw.MessageID, 10)
	threadID := messageID
	if raw.ReplyToMessage != nil && raw.ReplyToMessage.MessageID > 0 {
		threadID = strconv.FormatInt(raw.ReplyToMessage.MessageID, 10)
	}
	return model.Message{
		System:    "telegram",
		SpaceID:   chatID,
		ThreadID:  threadID,
		MessageID: messageID,
		Author: model.User{
			System: "telegram",
			Login:  strings.TrimSpace(raw.From.Username),
			Name:   strings.TrimSpace(strings.Join([]string{raw.From.FirstName, raw.From.LastName}, " ")),
			IsBot:  raw.From.IsBot,
		},
		Body:      firstNonEmpty(raw.Text),
		CreatedAt: unixSeconds(raw.Date),
	}
}

func unixSeconds(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func parseTimeout(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultTimeout, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse Telegram integration timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("parse Telegram integration timeout: duration must be positive")
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
		System:       "telegram",
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
