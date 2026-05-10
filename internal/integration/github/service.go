package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rasungatullin/progress/internal/integration/model"
)

type ghRunner interface {
	RunAuthStatus(context.Context) (CommandResult, resolvedConfig, error)
	RunRepoView(context.Context, string) (CommandResult, resolvedConfig, error)
	RunIssueView(context.Context, string, int) (CommandResult, resolvedConfig, error)
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
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees []ghIssueUser `json:"assignees"`
	Author    *ghIssueUser  `json:"author"`
}

type ghIssueUser struct {
	Login    string `json:"login"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	URL      string `json:"url"`
	IsBot    bool   `json:"isBot"`
	IsActive bool   `json:"isActive"`
}

func NewService() *Service {
	return &Service{runner: NewRunner()}
}

func (s *Service) Execute(ctx context.Context, req model.ProviderRequest) (model.Response, error) {
	response := model.Response{
		System:    "github",
		Resource:  req.Resource,
		Operation: req.Operation,
	}

	switch {
	case req.Resource == "auth" && req.Operation == "status":
		return s.executeAuthStatus(ctx, response)
	case (req.Resource == "repo" || req.Resource == "repository") && req.Operation == "get":
		return s.executeRepoGet(ctx, response, req)
	case req.Resource == "issue" && req.Operation == "get":
		return s.executeIssueGet(ctx, response, req)
	default:
		err := &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: fmt.Sprintf("GitHub integration does not support %s %s at this stage", req.Resource, req.Operation),
		}
		return response, err
	}
}

func (s *Service) executeIssueGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := normalizeRepository(req.Repository)
	if err != nil {
		status := issueErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository), req.Number)
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "issue request rejected before invoking gh")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
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
		case isIssueNotFound(result):
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

	if raw.Number <= 0 || strings.TrimSpace(raw.Title) == "" || raw.Author == nil {
		status.State = StateExternalFailure
		status.Message = "unexpected GitHub CLI JSON response: missing issue number, title, or author"
		status.Diagnostics = append(status.Diagnostics, "gh issue view returned an incomplete issue payload")
		response.IssueStatus = &status
		return response, &Error{Code: ErrorCodeExternalFailure, Message: status.Message, Result: result}
	}

	labels := make([]string, 0, len(raw.Labels))
	for _, label := range raw.Labels {
		name := strings.TrimSpace(label.Name)
		if name != "" {
			labels = append(labels, name)
		}
	}

	assignees := make([]model.TrackerUser, 0, len(raw.Assignees))
	for _, assignee := range raw.Assignees {
		assignees = append(assignees, normalizeTrackerUser(assignee))
	}

	response.Issue = &model.TrackerIssue{
		System:     "github",
		Repository: repository,
		Number:     raw.Number,
		Title:      strings.TrimSpace(raw.Title),
		Body:       strings.TrimSpace(raw.Body),
		State:      strings.TrimSpace(raw.State),
		Labels:     labels,
		Assignees:  assignees,
		Author:     normalizeTrackerUser(*raw.Author),
		URL:        strings.TrimSpace(raw.URL),
		CreatedAt:  strings.TrimSpace(raw.CreatedAt),
		UpdatedAt:  strings.TrimSpace(raw.UpdatedAt),
	}
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
		status.Message = "GitHub CLI is installed and authentication is available"
		status.Diagnostics = append(status.Diagnostics, "gh auth status completed successfully")
		response.AuthStatus = &status
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

func (s *Service) executeRepoGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository, err := normalizeRepository(req.Repository)
	if err != nil {
		status := repositoryErrorStatus(resolvedConfig{Command: defaultCommand}, CommandResult{Command: defaultCommand, ExitCode: -1}, strings.TrimSpace(req.Repository))
		status.State = ErrorCodeInvalidRequest
		status.Message = err.Error()
		status.Diagnostics = append(status.Diagnostics, "repository request rejected before invoking gh")
		response.RepositoryStatus = &status
		return response, &Error{Code: ErrorCodeInvalidRequest, Message: status.Message, Result: CommandResult{Command: defaultCommand, ExitCode: -1}}
	}

	result, config, err := s.runner.RunRepoView(ctx, repository)
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

func normalizeTrackerUser(raw ghIssueUser) model.TrackerUser {
	return model.TrackerUser{
		System:   "github",
		Login:    strings.TrimSpace(raw.Login),
		Name:     strings.TrimSpace(raw.Name),
		Email:    strings.TrimSpace(raw.Email),
		URL:      strings.TrimSpace(raw.URL),
		IsBot:    raw.IsBot,
		IsActive: raw.IsActive,
	}
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
