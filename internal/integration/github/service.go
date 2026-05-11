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
	RunPRCreate(context.Context, string, PRCreateRequest) (CommandResult, resolvedConfig, error)
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
	case (req.Resource == "pr" || req.Resource == "pull-request") && req.Operation == "create":
		return s.executePRCreate(ctx, response, req)
	default:
		err := &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: fmt.Sprintf("GitHub integration does not support %s %s at this stage", req.Resource, req.Operation),
		}
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
	return response, nil
}

func (s *Service) executeIssueGet(ctx context.Context, response model.Response, req model.ProviderRequest) (model.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	if repository != "" {
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
