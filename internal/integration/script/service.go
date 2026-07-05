package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/integration/model"
)

const defaultTimeout = 30 * time.Second

type Service struct {
	config          model.IntegrationSystemConfig
	resolveWorkdir  func(context.Context) (string, error)
	runCommand      func(context.Context, string, []string, []string, string) commandResult
	createInputFile func([]byte) (string, func(), error)
}

type commandResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

type scriptEnvelope struct {
	System          string            `json:"system"`
	IntegrationType string            `json:"integration_type"`
	OperationName   string            `json:"operation_name"`
	ObjectType      string            `json:"object_type"`
	Operation       string            `json:"operation"`
	Request         map[string]any    `json:"request"`
	Settings        map[string]string `json:"settings,omitempty"`
}

type scriptResponse struct {
	Status          string                 `json:"status"`
	Failure         *model.Failure         `json:"failure"`
	Task            *scriptTask            `json:"task"`
	Tasks           []scriptTask           `json:"tasks"`
	TaskComments    []scriptComment        `json:"task_comments"`
	Comments        []scriptComment        `json:"comments"`
	SearchResults   []scriptSearchResult   `json:"search_results"`
	OperationResult *scriptOperationResult `json:"operation_result"`
}

type scriptTask struct {
	System     string            `json:"system"`
	Repository string            `json:"repository"`
	Number     int               `json:"number"`
	ExternalID string            `json:"external_id"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	State      string            `json:"state"`
	Traits     []string          `json:"traits"`
	Labels     []string          `json:"labels"`
	Attributes map[string]string `json:"attributes"`
	Author     scriptUser        `json:"author"`
	Assignees  []scriptUser      `json:"assignees"`
	URL        string            `json:"url"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
}

type scriptComment struct {
	System     string     `json:"system"`
	Repository string     `json:"repository"`
	TaskNumber int        `json:"task_number"`
	Number     int        `json:"number"`
	ExternalID string     `json:"external_id"`
	Author     scriptUser `json:"author"`
	Body       string     `json:"body"`
	URL        string     `json:"url"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at"`
}

type scriptUser struct {
	System   string `json:"system"`
	Login    string `json:"login"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	URL      string `json:"url"`
	IsBot    bool   `json:"is_bot"`
	IsActive bool   `json:"is_active"`
}

type scriptSearchResult struct {
	System     string `json:"system"`
	Repository string `json:"repository"`
	Kind       string `json:"kind"`
	Number     int    `json:"number"`
	ExternalID string `json:"external_id"`
	Title      string `json:"title"`
	State      string `json:"state"`
	URL        string `json:"url"`
	UpdatedAt  string `json:"updated_at"`
}

type scriptOperationResult struct {
	Status      string         `json:"status"`
	ExternalID  string         `json:"external_id"`
	URL         string         `json:"url"`
	Message     string         `json:"message"`
	Diagnostics []string       `json:"diagnostics"`
	Failure     *model.Failure `json:"failure"`
}

func NewService(config model.IntegrationSystemConfig) *Service {
	return &Service{
		config:          config,
		resolveWorkdir:  resolveRepoRoot,
		runCommand:      runCommand,
		createInputFile: createInputFile,
	}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		IntegrationType: firstNonEmpty(req.IntegrationType, s.config.IntegrationType, model.IntegrationTypeTracker),
		System:          req.System,
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
	}

	if req.Resource == "auth" && req.Operation == "status" {
		return s.executeAuthStatus(response)
	}

	operationName := operationNameForRequest(req)
	operation, ok := s.config.Operations[operationName]
	if !ok {
		return failureResponse(response, model.FailureKindUnsupportedOperation, false, fmt.Errorf("script operation is not configured: %s", operationName), nil)
	}

	timeout, err := resolveTimeout(firstNonEmpty(operation.Timeout, s.config.Timeout))
	if err != nil {
		return failureResponse(response, model.FailureKindInvalidRequest, false, err, nil)
	}

	workdir, err := s.resolveWorkdir(ctx)
	if err != nil {
		return failureResponse(response, model.FailureKindInvalidRequest, false, err, nil)
	}
	workdir = resolveConfiguredWorkdir(workdir, s.config.Path)

	commandPath, err := resolveCommandPath(workdir, operation)
	if err != nil {
		return failureResponse(response, model.FailureKindInvalidRequest, false, err, nil)
	}

	envelope := s.buildEnvelope(req, operationName, operation)
	if err := validateRequiredFields(envelope.Request, operation.Required); err != nil {
		return failureResponse(response, model.FailureKindInvalidRequest, false, err, nil)
	}

	content, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return failureResponse(response, model.FailureKindInternalIntegration, false, fmt.Errorf("encode script integration request: %w", err), nil)
	}
	inputPath, cleanup, err := s.createInputFile(content)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return failureResponse(response, model.FailureKindInternalIntegration, false, fmt.Errorf("create script integration request file: %w", err), nil)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := append(os.Environ(),
		"PROGRESS_INTEGRATION_SYSTEM="+req.System,
		"PROGRESS_INTEGRATION_TYPE="+envelope.IntegrationType,
		"PROGRESS_INTEGRATION_OPERATION="+operationName,
		"PROGRESS_INTEGRATION_REQUEST_FILE="+inputPath,
		"PROGRESS_INTEGRATION_TIMEOUT="+timeout.String(),
	)
	result := s.runCommand(runCtx, commandPath, nil, env, workdir)
	if errors.Is(result.err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return failureResponse(response, model.FailureKindTimeout, true, fmt.Errorf("script operation timed out after %s", timeout), []string{"script=" + commandPath})
	}
	if result.err != nil {
		kind := model.FailureKindExternalFailure
		if result.exitCode == -1 {
			kind = model.FailureKindInvalidRequest
		}
		return failureResponse(response, kind, false, fmt.Errorf("script operation failed: %w", result.err), []string{"script=" + commandPath, "stderr=" + strings.TrimSpace(result.stderr)})
	}
	if result.exitCode != 0 {
		return failureResponse(response, model.FailureKindExternalFailure, false, fmt.Errorf("script operation exited with code %d", result.exitCode), []string{"script=" + commandPath, "stderr=" + strings.TrimSpace(result.stderr)})
	}

	return decodeScriptResponse(response, result.stdout)
}

func (s *Service) executeAuthStatus(response model.Response) (model.Response, error) {
	if len(s.config.Operations) == 0 {
		return failureResponse(response, model.FailureKindNotConfigured, false, fmt.Errorf("script system has no configured operations"), nil)
	}
	response.AuthStatus = &model.AuthStatus{
		System:        response.System,
		State:         "ready",
		Available:     true,
		Authenticated: true,
		Command:       "script",
		Message:       "script integration operations are configured",
	}
	response.Status = model.ResponseStatusOK
	return response, nil
}

func (s *Service) buildEnvelope(req model.ProviderRequest, operationName string, operation model.IntegrationOperationConfig) scriptEnvelope {
	integrationType, objectType, action := parseOperationName(operationName)
	request := requestMap(req)
	settings := systemSettings(s.config)
	for field, value := range operation.Defaults {
		field = strings.TrimSpace(field)
		if field == "" || !isEmptyValue(request[field]) {
			continue
		}
		request[field] = resolveDefaultValue(value, s.config, settings)
	}

	return scriptEnvelope{
		System:          req.System,
		IntegrationType: integrationType,
		OperationName:   operationName,
		ObjectType:      objectType,
		Operation:       action,
		Request:         request,
		Settings:        settings,
	}
}

func requestMap(req model.ProviderRequest) map[string]any {
	request := map[string]any{}
	putString(request, "system", req.System)
	putString(request, "repository", req.Repository)
	if req.Number > 0 {
		request["number"] = req.Number
	}
	putString(request, "external_id", req.ExternalID)
	putString(request, "base", req.Base)
	putString(request, "head", req.Head)
	putString(request, "title", req.Title)
	putString(request, "body", firstNonEmpty(req.Body, req.Text))
	putString(request, "text", firstNonEmpty(req.Text, req.Body))
	if req.Draft {
		request["draft"] = true
	}
	putString(request, "query", req.Query)
	putString(request, "state", req.State)
	putString(request, "scope", req.Scope)
	if req.Limit > 0 {
		request["limit"] = req.Limit
	}
	putString(request, "path", req.Path)
	if req.Line > 0 {
		request["line"] = req.Line
	}
	putString(request, "side", req.Side)
	putString(request, "channel_id", req.ChannelID)
	putString(request, "thread_id", req.ThreadID)
	putString(request, "message_id", req.MessageID)
	putString(request, "reaction", req.Reaction)
	if len(req.Fields) > 0 {
		request["fields"] = append([]string(nil), req.Fields...)
	}
	if len(req.Labels) > 0 {
		request["labels"] = append([]string(nil), req.Labels...)
	}
	return request
}

func validateRequiredFields(request map[string]any, required []string) error {
	for _, field := range required {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if isEmptyValue(request[field]) {
			return fmt.Errorf("script operation required field is missing: %s", field)
		}
	}
	return nil
}

func decodeScriptResponse(response model.Response, stdout string) (model.Response, error) {
	var raw scriptResponse
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return failureResponse(response, model.FailureKindInternalIntegration, false, fmt.Errorf("decode script integration response: %w", err), nil)
	}
	if raw.Failure != nil {
		response.Status = model.ResponseStatusFailed
		response.Failure = raw.Failure
		return response, errors.New(raw.Failure.Message)
	}

	if raw.Task != nil {
		task := raw.Task.toCanonicalTask()
		response.Task = &task
		issue := trackerIssueFromTask(task)
		response.Issue = &issue
	}
	for _, task := range raw.Tasks {
		canonical := task.toCanonicalTask()
		response.SearchResults = append(response.SearchResults, searchResultFromTask(canonical))
	}
	for _, item := range raw.SearchResults {
		response.SearchResults = append(response.SearchResults, item.toTrackerSearchResult())
	}
	for _, item := range append(raw.TaskComments, raw.Comments...) {
		comment := item.toTaskComment()
		response.TaskComments = append(response.TaskComments, comment)
		response.Comments = append(response.Comments, trackerCommentFromTaskComment(comment))
	}
	if raw.OperationResult != nil {
		response.OperationResult = raw.OperationResult.toOperationResult(response)
	}

	switch strings.TrimSpace(raw.Status) {
	case "", model.ResponseStatusOK:
		response.Status = model.ResponseStatusOK
	case model.ResponseStatusPartial:
		response.Status = model.ResponseStatusPartial
		response.Partial = true
	case model.ResponseStatusFailed:
		response.Status = model.ResponseStatusFailed
		if response.Failure == nil {
			response.Failure = &model.Failure{Kind: model.FailureKindExternalFailure, Message: "script operation returned failed status"}
		}
		return response, errors.New(response.Failure.Message)
	default:
		return failureResponse(response, model.FailureKindInternalIntegration, false, fmt.Errorf("unsupported script response status: %s", raw.Status), nil)
	}
	if response.Status == model.ResponseStatusOK {
		if err := validateSuccessfulResponsePayload(response); err != nil {
			return failureResponse(response, model.FailureKindExternalFailure, false, err, nil)
		}
	}
	return response, nil
}

func validateSuccessfulResponsePayload(response model.Response) error {
	integrationType := normalizeIntegrationType(firstNonEmpty(response.IntegrationType, model.IntegrationTypeTracker))
	objectType := normalizeObjectType(firstNonEmpty(response.ObjectType, response.Resource))
	operation := normalizeOperation(response.Operation)
	switch integrationType {
	case model.IntegrationTypeTracker:
		switch objectType {
		case "task":
			switch operation {
			case "create", "get", "update":
				if response.Task == nil && response.Issue == nil {
					return fmt.Errorf("script operation %s.%s.%s returned ok without task payload", integrationType, objectType, operation)
				}
			}
		case "comment":
			if operation == "create" && response.OperationResult == nil && len(response.TaskComments) == 0 && len(response.Comments) == 0 {
				return fmt.Errorf("script operation %s.%s.%s returned ok without comment payload", integrationType, objectType, operation)
			}
		case "label":
			if (operation == "add" || operation == "remove") && response.OperationResult == nil {
				return fmt.Errorf("script operation %s.%s.%s returned ok without operation result", integrationType, objectType, operation)
			}
		}
	}
	return nil
}

func operationNameForRequest(req model.ProviderRequest) string {
	integrationType := normalizeIntegrationType(firstNonEmpty(req.IntegrationType, model.IntegrationTypeTracker))
	objectType := normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource))
	rawOperation := strings.TrimSpace(strings.ToLower(req.Operation))
	operation := normalizeOperation(req.Operation)
	switch integrationType {
	case model.IntegrationTypeTracker:
		switch objectType {
		case "task", "issue":
			if rawOperation == "comments" || rawOperation == "list-comments" {
				return "tracker.task.comment.list"
			}
			return "tracker.task." + operation
		case "comment":
			if operation == "comments" || operation == "list" {
				return "tracker.task.comment.list"
			}
			return "tracker.task.comment." + operation
		case "label":
			return "tracker.task.label." + operation
		}
	}
	if integrationType == "" || objectType == "" {
		return operation
	}
	return integrationType + "." + objectType + "." + operation
}

func resolveCommandPath(workdir string, operation model.IntegrationOperationConfig) (string, error) {
	if path := strings.TrimSpace(operation.Script); path != "" {
		return resolveFilePath(workdir, path), nil
	}
	if command := strings.TrimSpace(operation.Command); command != "" {
		return command, nil
	}
	if path := strings.TrimSpace(operation.Path); path != "" {
		return resolveFilePath(workdir, path), nil
	}
	return "", fmt.Errorf("script operation must define script, command or path")
}

func resolveFilePath(workdir string, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workdir, path)
}

func resolveConfiguredWorkdir(repoRoot string, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return repoRoot
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(repoRoot, configured)
}

func resolveTimeout(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultTimeout, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse script operation timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("script operation timeout must be positive")
	}
	return timeout, nil
}

func systemSettings(config model.IntegrationSystemConfig) map[string]string {
	settings := map[string]string{}
	putSetting(settings, "project", config.Project)
	putSetting(settings, "repository", config.Repository)
	putSetting(settings, "default_repo", config.DefaultRepo)
	for name, value := range config.Settings {
		putSetting(settings, name, value)
	}
	return settings
}

func resolveDefaultValue(value string, config model.IntegrationSystemConfig, settings map[string]string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "${system.project}":
		return strings.TrimSpace(config.Project)
	case value == "${system.repository}":
		return strings.TrimSpace(config.Repository)
	case value == "${system.default_repo}":
		return strings.TrimSpace(config.DefaultRepo)
	case strings.HasPrefix(value, "${system.settings.") && strings.HasSuffix(value, "}"):
		name := strings.TrimSuffix(strings.TrimPrefix(value, "${system.settings."), "}")
		return settings[name]
	default:
		return value
	}
}

func failureResponse(response model.Response, kind string, retryable bool, err error, diagnostics []string) (model.Response, error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	response.Status = model.ResponseStatusFailed
	response.Failure = &model.Failure{Kind: kind, Retryable: retryable, Message: message, Diagnostics: compactStrings(diagnostics)}
	response.OperationResult = &model.OperationResult{
		System:      response.System,
		ObjectType:  response.ObjectType,
		Operation:   response.Operation,
		Status:      model.ResponseStatusFailed,
		Message:     message,
		Diagnostics: compactStrings(diagnostics),
		Failure:     response.Failure,
	}
	return response, err
}

func (task scriptTask) toCanonicalTask() model.CanonicalTask {
	traits := append([]string(nil), task.Traits...)
	if len(traits) == 0 {
		traits = append([]string(nil), task.Labels...)
	}
	assignees := make([]model.User, 0, len(task.Assignees))
	for _, assignee := range task.Assignees {
		assignees = append(assignees, assignee.toUser())
	}
	return model.CanonicalTask{
		System:     task.System,
		Repository: task.Repository,
		Number:     task.Number,
		ExternalID: task.ExternalID,
		Title:      strings.TrimSpace(task.Title),
		Body:       task.Body,
		State:      strings.TrimSpace(task.State),
		Traits:     traits,
		Attributes: task.Attributes,
		Assignees:  assignees,
		Author:     task.Author.toUser(),
		URL:        task.URL,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
	}
}

func (comment scriptComment) toTaskComment() model.TaskComment {
	number := comment.TaskNumber
	if number == 0 {
		number = comment.Number
	}
	return model.TaskComment{
		System:     comment.System,
		Repository: comment.Repository,
		TaskNumber: number,
		ExternalID: comment.ExternalID,
		Author:     comment.Author.toUser(),
		Body:       comment.Body,
		URL:        comment.URL,
		CreatedAt:  comment.CreatedAt,
		UpdatedAt:  comment.UpdatedAt,
	}
}

func (user scriptUser) toUser() model.User {
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

func (result scriptSearchResult) toTrackerSearchResult() model.TrackerSearchResult {
	return model.TrackerSearchResult{
		System:     result.System,
		Repository: result.Repository,
		Kind:       firstNonEmpty(result.Kind, "task"),
		Number:     result.Number,
		Title:      result.Title,
		State:      result.State,
		URL:        result.URL,
		UpdatedAt:  result.UpdatedAt,
	}
}

func (result scriptOperationResult) toOperationResult(response model.Response) *model.OperationResult {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = model.ResponseStatusOK
	}
	return &model.OperationResult{
		System:      response.System,
		ObjectType:  response.ObjectType,
		Operation:   response.Operation,
		Status:      status,
		ExternalID:  result.ExternalID,
		URL:         result.URL,
		Method:      "script",
		Message:     result.Message,
		Diagnostics: result.Diagnostics,
		Failure:     result.Failure,
	}
}

func trackerIssueFromTask(task model.CanonicalTask) model.TrackerIssue {
	return model.TrackerIssue{
		System:     task.System,
		Repository: task.Repository,
		Number:     task.Number,
		Title:      task.Title,
		Body:       task.Body,
		State:      task.State,
		Labels:     append([]string(nil), task.Traits...),
		Assignees:  trackerUsersFromUsers(task.Assignees),
		Author:     trackerUserFromUser(task.Author),
		URL:        task.URL,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
	}
}

func trackerCommentFromTaskComment(comment model.TaskComment) model.TrackerComment {
	return model.TrackerComment{
		System:     comment.System,
		Repository: comment.Repository,
		Number:     comment.TaskNumber,
		Author:     trackerUserFromUser(comment.Author),
		Body:       comment.Body,
		URL:        comment.URL,
		CreatedAt:  comment.CreatedAt,
		UpdatedAt:  comment.UpdatedAt,
	}
}

func searchResultFromTask(task model.CanonicalTask) model.TrackerSearchResult {
	return model.TrackerSearchResult{
		System:     task.System,
		Repository: task.Repository,
		Kind:       "task",
		Number:     task.Number,
		Title:      task.Title,
		State:      task.State,
		URL:        task.URL,
		UpdatedAt:  task.UpdatedAt,
	}
}

func trackerUsersFromUsers(users []model.User) []model.TrackerUser {
	if len(users) == 0 {
		return nil
	}
	result := make([]model.TrackerUser, 0, len(users))
	for _, user := range users {
		result = append(result, trackerUserFromUser(user))
	}
	return result
}

func trackerUserFromUser(user model.User) model.TrackerUser {
	return model.TrackerUser{
		System:   user.System,
		Login:    user.Login,
		Name:     user.Name,
		Email:    user.Email,
		URL:      user.URL,
		IsBot:    user.IsBot,
		IsActive: user.IsActive,
	}
}

func parseOperationName(name string) (string, string, string) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(name)), ".")
	if len(parts) < 3 {
		return "", "", ""
	}
	return normalizeIntegrationType(parts[0]), strings.Join(parts[1:len(parts)-1], "-"), normalizeOperation(parts[len(parts)-1])
}

func normalizeIntegrationType(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func normalizeObjectType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "issue":
		return "task"
	case "task-comment":
		return "comment"
	case "task-label":
		return "label"
	default:
		return value
	}
}

func normalizeOperation(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "comments":
		return "list"
	case "add-label", "add-labels":
		return "add"
	case "remove-label", "remove-labels":
		return "remove"
	default:
		return value
	}
}

func putString(target map[string]any, name string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		target[name] = value
	}
}

func putSetting(target map[string]string, name string, value string) {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name != "" && value != "" {
		target[name] = value
	}
}

func isEmptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(typed) == 0
	case int:
		return typed == 0
	case bool:
		return false
	default:
		return false
	}
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func resolveRepoRoot(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return "", fmt.Errorf("resolve script integration working directory: %w", err)
		}
		return wd, nil
	}
	return strings.TrimSpace(string(output)), nil
}

func createInputFile(content []byte) (string, func(), error) {
	file, err := os.CreateTemp("", "progress-script-request-*.json")
	if err != nil {
		return "", nil, err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, nil
}

func runCommand(ctx context.Context, path string, args []string, env []string, dir string) commandResult {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Dir = dir
	output, err := cmd.Output()
	result := commandResult{stdout: strings.TrimSpace(string(output))}
	if err == nil {
		return result
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.stderr = strings.TrimSpace(string(exitErr.Stderr))
		result.exitCode = exitErr.ExitCode()
		result.err = nil
		return result
	}
	result.exitCode = -1
	result.err = err
	return result
}
