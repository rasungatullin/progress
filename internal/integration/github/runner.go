package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

const (
	configRelativePath = ".progress/integration/github.json"
	defaultCommand     = "gh"
	defaultTimeout     = 30 * time.Second

	ErrorCodeNotInstalled         = "gh-not-installed"
	ErrorCodeAuthRequired         = "auth-required"
	ErrorCodeNotFound             = "not-found"
	ErrorCodeAlreadyExists        = "already-exists"
	ErrorCodeTimeout              = "timeout"
	ErrorCodePermissionDenied     = "permission-denied"
	ErrorCodeTemporaryUnavailable = "temporary-unavailable"
	ErrorCodeUnsupportedOperation = "unsupported-operation"
	ErrorCodeInternalIntegration  = "internal-integration-error"
	ErrorCodeExternalFailure      = "unexpected-external-failure"
	ErrorCodePartialPayload       = "partial-payload"
	ErrorCodeInvalidRequest       = "invalid-request"

	StateReady           = "ready"
	StateNotInstalled    = "not-installed"
	StateAuthRequired    = "auth-required"
	StateTimeout         = "timeout"
	StateExternalFailure = "external-failure"
)

type Config struct {
	Command     string `json:"command"`
	Path        string `json:"path"`
	Timeout     string `json:"timeout"`
	Repository  string `json:"repository"`
	DefaultRepo string `json:"default_repo"`
}

type resolvedConfig struct {
	Command     string
	Timeout     time.Duration
	DefaultRepo string
}

type CommandResult struct {
	Command  string
	Path     string
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

type Error struct {
	Code    string
	Message string
	Result  CommandResult
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

type commandRunner struct {
	stdout string
	stderr string
	err    error
}

type Runner struct {
	resolveRepoRoot func(context.Context) (string, error)
	readFile        func(string) ([]byte, error)
	lookPath        func(string) (string, error)
	runCommand      func(context.Context, string, []string) commandRunner
	systemConfig    *integrationmodel.IntegrationSystemConfig
}

func NewRunner() *Runner {
	return &Runner{
		resolveRepoRoot: resolveRepoRoot,
		readFile:        os.ReadFile,
		lookPath:        exec.LookPath,
		runCommand:      defaultRunCommand,
	}
}

func NewRunnerWithSystemConfig(config integrationmodel.IntegrationSystemConfig) *Runner {
	runner := NewRunner()
	runner.systemConfig = &config
	return runner
}

func (r *Runner) RunAuthStatus(ctx context.Context) (CommandResult, resolvedConfig, error) {
	return r.runCommandWithConfig(ctx, []string{"auth", "status"})
}

func (r *Runner) RunRepoView(ctx context.Context, repository string) (CommandResult, resolvedConfig, error) {
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	return r.runCommandWithResolvedConfig(ctx, config, []string{"repo", "view", repository, "--json", "name,owner,description,defaultBranchRef,url"})
}

func (r *Runner) RunIssueView(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	return r.runIssueViewByID(ctx, repository, strconv.Itoa(number))
}

func (r *Runner) RunIssueViewByID(ctx context.Context, repository string, identifier string) (CommandResult, resolvedConfig, error) {
	return r.runIssueViewByID(ctx, repository, identifier)
}

func (r *Runner) runIssueViewByID(ctx context.Context, repository string, identifier string) (CommandResult, resolvedConfig, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub issue identifier must not be empty", Result: result}
	}
	var err error
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	return r.runCommandWithResolvedConfig(ctx, config, []string{"issue", "view", identifier, "--repo", repository, "--json", "number,title,body,state,labels,assignees,author,url,createdAt,updatedAt"})
}

func (r *Runner) RunIssueList(ctx context.Context, repository string, request IssueListRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizeIssueListRequest(request)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository = firstNonEmpty(repository, config.DefaultRepo)
	if repository != "" {
		repository, err = normalizeRepository(repository)
		if err != nil {
			result := CommandResult{Command: config.Command, ExitCode: -1}
			return result, config, &Error{
				Code:    ErrorCodeInvalidRequest,
				Message: err.Error(),
				Result:  result,
			}
		}
	}

	args := []string{"issue", "list"}
	if repository != "" {
		args = append(args, "--repo", repository)
	}
	args = append(args, "--state", request.State, "--limit", strconv.Itoa(request.Limit), "--json", "number,title,state,labels,assignees,author,url,createdAt,updatedAt")
	if search := issueListSearchQuery(request); search != "" {
		args = append(args, "--search", search)
	}

	return r.runCommandWithResolvedConfig(ctx, config, args)
}

func (r *Runner) RunIssueComments(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "--paginate", "--slurp", fmt.Sprintf("repos/%s/issues/%d/comments", repository, number)})
}

func (r *Runner) RunIssueCommentCreate(ctx context.Context, repository string, number int, body string) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	body = strings.TrimSpace(body)
	if body == "" {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: "GitHub issue comment body is required",
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", fmt.Sprintf("repos/%s/issues/%d/comments", repository, number), "--method", "POST", "-f", "body=" + body})
}

func (r *Runner) RunIssueLabelsAdd(ctx context.Context, repository string, number int, labels []string) (CommandResult, resolvedConfig, error) {
	return r.runIssueLabelsChange(ctx, repository, number, labels, true)
}

func (r *Runner) RunIssueLabelsRemove(ctx context.Context, repository string, number int, labels []string) (CommandResult, resolvedConfig, error) {
	return r.runIssueLabelsChange(ctx, repository, number, labels, false)
}

func (r *Runner) runIssueLabelsChange(ctx context.Context, repository string, number int, labels []string, add bool) (CommandResult, resolvedConfig, error) {
	number, err := normalizeIssueNumber(number)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	labels = normalizeIssueLabels(labels)
	if len(labels) == 0 {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: "GitHub issue label is required",
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	flag := "--remove-label"
	if add {
		flag = "--add-label"
	}
	args := []string{"issue", "edit", strconv.Itoa(number), "--repo", repository}
	for _, label := range labels {
		args = append(args, flag, label)
	}
	return r.runCommandWithResolvedConfig(ctx, config, args)
}

func (r *Runner) RunPRView(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	return r.runCommandWithResolvedConfig(ctx, config, []string{"pr", "view", strconv.Itoa(number), "--repo", repository, "--json", "number,title,body,state,mergeable,mergeStateStatus,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"})
}

func (r *Runner) RunPRList(ctx context.Context, repository string, request PRListRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRListRequest(request)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository = firstNonEmpty(repository, config.DefaultRepo)
	if repository != "" {
		repository, err = normalizeRepository(repository)
		if err != nil {
			result := CommandResult{Command: config.Command, ExitCode: -1}
			return result, config, &Error{
				Code:    ErrorCodeInvalidRequest,
				Message: err.Error(),
				Result:  result,
			}
		}
	}

	args := []string{"pr", "list"}
	if repository != "" {
		args = append(args, "--repo", repository)
	}
	args = append(args, "--state", request.State, "--limit", strconv.Itoa(request.Limit), "--json", "number,title,body,state,mergeable,mergeStateStatus,author,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt")
	search := request.Query
	switch request.Scope {
	case "authored":
		args = append(args, "--author", "@me")
	case "reviewer":
		search = strings.TrimSpace(strings.Join([]string{search, "reviewed-by:@me"}, " "))
	}
	if search != "" {
		args = append(args, "--search", search)
	}

	return r.runCommandWithResolvedConfig(ctx, config, args)
}

func (r *Runner) RunPRReviewThreads(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}
	owner, name, err := splitRepository(repository)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	query := `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          isOutdated
          path
          line
          comments(first: 100) {
            nodes {
              id
              body
              url
              path
              line
              author {
                login
                url
              }
              createdAt
              updatedAt
            }
          }
        }
      }
    }
  }
}`
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "graphql", "-f", "query=" + query, "-f", "owner=" + owner, "-f", "name=" + name, "-F", "number=" + strconv.Itoa(number)})
}

func (r *Runner) RunPRReviews(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return CommandResult{Command: defaultCommand, ExitCode: -1}, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return CommandResult{Command: config.Command, ExitCode: -1}, config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "--paginate", "--slurp", fmt.Sprintf("repos/%s/pulls/%d/reviews", repository, number)})
}

func (r *Runner) RunPRReviewComments(ctx context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return CommandResult{Command: defaultCommand, ExitCode: -1}, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return CommandResult{Command: config.Command, ExitCode: -1}, config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "--paginate", "--slurp", fmt.Sprintf("repos/%s/pulls/%d/comments", repository, number)})
}

func resolveRepository(repository string, fallback string) (string, error) {
	return normalizeRepository(firstNonEmpty(repository, fallback))
}

func (r *Runner) RunPRCreate(ctx context.Context, repository string, request PRCreateRequest) (CommandResult, resolvedConfig, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	request, err = normalizePRCreateRequest(request)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	args := []string{"pr", "create", "--repo", repository, "--base", request.Base, "--head", request.Head, "--title", request.Title, "--body", request.Body}
	if request.Draft {
		args = append(args, "--draft")
	}

	return r.runCommandWithConfig(ctx, args)
}

func (r *Runner) RunPRCommentCreate(ctx context.Context, repository string, number int, request PRCommentCreateRequest) (CommandResult, resolvedConfig, error) {
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	request, err = normalizePRCommentCreateRequest(request)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	if request.Path == "" && request.Line == 0 {
		return r.RunIssueCommentCreate(ctx, repository, number, request.Body)
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		result := CommandResult{Command: config.Command, ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}
	headResult, _, err := r.runCommandWithResolvedConfig(ctx, config, []string{"pr", "view", strconv.Itoa(number), "--repo", repository, "--json", "headRefOid"})
	if err != nil {
		return headResult, config, err
	}
	if headResult.ExitCode != 0 {
		return headResult, config, nil
	}
	var head struct {
		OID string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(headResult.Stdout), &head); err != nil {
		return headResult, config, &Error{Code: ErrorCodeExternalFailure, Message: fmt.Sprintf("unexpected GitHub CLI head JSON response: %v", err), Result: headResult, Err: err}
	}
	request.CommitID = strings.TrimSpace(head.OID)
	if request.CommitID == "" {
		return headResult, config, &Error{Code: ErrorCodePartialPayload, Message: "GitHub pull request head SHA is missing", Result: headResult}
	}
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/comments", repository, number)
	args := []string{"api", "--method", "POST", endpoint, "-f", "body=" + request.Body, "-f", "commit_id=" + request.CommitID, "-f", "path=" + request.Path, "-F", "line=" + strconv.Itoa(request.Line), "-f", "side=" + request.Side}
	return r.runCommandWithResolvedConfig(ctx, config, args)
}

func (r *Runner) RunPRReviewThreadResolve(ctx context.Context, threadID string) (CommandResult, resolvedConfig, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: "GitHub pull request review thread id is required",
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	mutation := `mutation($threadId: ID!) {
  resolveReviewThread(input: {threadId: $threadId}) {
    thread {
      id
      isResolved
    }
  }
}`
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "graphql", "-f", "query=" + mutation, "-f", "threadId=" + threadID})
}

func (r *Runner) RunPRReviewThreadUnresolve(ctx context.Context, threadID string) (CommandResult, resolvedConfig, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub pull request review thread id is required", Result: result}
	}
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}
	mutation := `mutation($threadId: ID!) {
  unresolveReviewThread(input: {threadId: $threadId}) {
    thread { id isResolved }
  }
}`
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "graphql", "-f", "query=" + mutation, "-f", "threadId=" + threadID})
}

func (r *Runner) RunPRReviewSubmit(ctx context.Context, repository string, number int, reviewID int64) (CommandResult, resolvedConfig, error) {
	if reviewID <= 0 {
		return CommandResult{Command: defaultCommand, ExitCode: -1}, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub pull request review id must be greater than zero"}
	}
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return CommandResult{Command: defaultCommand, ExitCode: -1}, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return CommandResult{Command: config.Command, ExitCode: -1}, config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "--method", "POST", fmt.Sprintf("repos/%s/pulls/%d/reviews/%d/events", repository, number, reviewID), "-f", "event=COMMENT"})
}

func (r *Runner) RunPRReviewDelete(ctx context.Context, repository string, number int, reviewID int64) (CommandResult, resolvedConfig, error) {
	if reviewID <= 0 {
		return CommandResult{Command: defaultCommand, ExitCode: -1}, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: "GitHub pull request review id must be greater than zero"}
	}
	number, err := normalizePullRequestNumber(number)
	if err != nil {
		return CommandResult{Command: defaultCommand, ExitCode: -1}, resolvedConfig{}, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}
	repository, err = resolveRepository(repository, config.DefaultRepo)
	if err != nil {
		return CommandResult{Command: config.Command, ExitCode: -1}, config, &Error{Code: ErrorCodeInvalidRequest, Message: err.Error()}
	}
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "--method", "DELETE", fmt.Sprintf("repos/%s/pulls/%d/reviews/%d", repository, number, reviewID)})
}

func (r *Runner) RunPRReviewThreadReply(ctx context.Context, request PRReviewThreadReplyRequest) (CommandResult, resolvedConfig, error) {
	request, err := normalizePRReviewThreadReplyRequest(request)
	if err != nil {
		result := CommandResult{Command: defaultCommand, ExitCode: -1}
		return result, resolvedConfig{}, &Error{
			Code:    ErrorCodeInvalidRequest,
			Message: err.Error(),
			Result:  result,
		}
	}

	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	mutation := `mutation($threadId: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $threadId, body: $body}) {
    comment {
      id
      body
      url
      path
      line
      author {
        login
        url
      }
      createdAt
      updatedAt
    }
  }
}`
	return r.runCommandWithResolvedConfig(ctx, config, []string{"api", "graphql", "-f", "query=" + mutation, "-f", "threadId=" + request.ThreadID, "-f", "body=" + request.Body})
}

type PRCreateRequest struct {
	Base  string
	Head  string
	Title string
	Body  string
	Draft bool
}

type IssueListRequest struct {
	State         string
	Query         string
	Labels        []string
	ExcludeLabels []string
	Limit         int
}

type PRListRequest struct {
	State string
	Scope string
	Query string
	Limit int
}

type PRCommentCreateRequest struct {
	Body     string
	Path     string
	Line     int
	Side     string
	CommitID string
}

type PRReviewThreadReplyRequest struct {
	ThreadID string
	Body     string
}

type ghPullRequestNodeResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ID string `json:"id"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

func normalizeRepository(repository string) (string, error) {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		return "", fmt.Errorf("GitHub repository is required")
	}

	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !isRepositoryPart(parts[0]) || !isRepositoryPart(parts[1]) {
		return "", fmt.Errorf("GitHub repository must use owner/name format")
	}

	return repository, nil
}

func isRepositoryPart(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, " \t\r\n")
}

func normalizeIssueNumber(number int) (int, error) {
	if number <= 0 {
		return 0, fmt.Errorf("GitHub issue number must be greater than zero")
	}

	return number, nil
}

func normalizeIssueLabels(labels []string) []string {
	result := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, label)
	}
	return result
}

func normalizeIssueListRequest(request IssueListRequest) (IssueListRequest, error) {
	request.State = strings.TrimSpace(strings.ToLower(request.State))
	request.Query = strings.TrimSpace(request.Query)
	request.Labels = normalizeIssueLabels(request.Labels)
	request.ExcludeLabels = normalizeIssueLabels(request.ExcludeLabels)

	switch request.State {
	case "":
		request.State = "open"
	case "open", "closed", "all":
	default:
		return IssueListRequest{}, fmt.Errorf("GitHub issue state must be one of open, closed or all")
	}

	if request.Limit <= 0 {
		request.Limit = 30
	}

	return request, nil
}

func issueListSearchQuery(request IssueListRequest) string {
	parts := make([]string, 0, 1+len(request.Labels)+len(request.ExcludeLabels))
	if request.Query != "" {
		parts = append(parts, request.Query)
	}
	for _, label := range request.Labels {
		parts = append(parts, fmt.Sprintf("label:%q", label))
	}
	for _, label := range request.ExcludeLabels {
		parts = append(parts, fmt.Sprintf("-label:%q", label))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func normalizePullRequestNumber(number int) (int, error) {
	if number <= 0 {
		return 0, fmt.Errorf("GitHub pull request number must be greater than zero")
	}

	return number, nil
}

func normalizePRCreateRequest(request PRCreateRequest) (PRCreateRequest, error) {
	request.Base = strings.TrimSpace(request.Base)
	request.Head = strings.TrimSpace(request.Head)
	request.Title = strings.TrimSpace(request.Title)
	request.Body = strings.TrimSpace(request.Body)

	if request.Base == "" {
		return PRCreateRequest{}, fmt.Errorf("GitHub pull request base branch is required")
	}
	if request.Head == "" {
		return PRCreateRequest{}, fmt.Errorf("GitHub pull request head branch is required")
	}
	if request.Title == "" {
		return PRCreateRequest{}, fmt.Errorf("GitHub pull request title is required")
	}
	if request.Base == request.Head {
		return PRCreateRequest{}, fmt.Errorf("GitHub pull request base and head branches must differ")
	}

	return request, nil
}

func normalizePRListRequest(request PRListRequest) (PRListRequest, error) {
	request.State = strings.TrimSpace(strings.ToLower(request.State))
	request.Scope = strings.TrimSpace(strings.ToLower(request.Scope))
	request.Query = strings.TrimSpace(request.Query)

	switch request.State {
	case "":
		request.State = "closed"
	case "open", "closed", "merged", "all":
	default:
		return PRListRequest{}, fmt.Errorf("GitHub pull request state must be one of open, closed, merged or all")
	}

	switch request.Scope {
	case "", "all":
		request.Scope = "all"
	case "author", "authored", "mine":
		request.Scope = "authored"
	case "reviewer", "reviewed", "review":
		request.Scope = "reviewer"
	default:
		return PRListRequest{}, fmt.Errorf("GitHub pull request scope must be one of all, authored or reviewer")
	}

	if request.Limit <= 0 {
		request.Limit = 30
	}

	return request, nil
}

func normalizePRCommentCreateRequest(request PRCommentCreateRequest) (PRCommentCreateRequest, error) {
	request.Body = strings.TrimSpace(request.Body)
	request.Path = strings.TrimSpace(request.Path)
	request.Side = strings.TrimSpace(strings.ToUpper(request.Side))

	if request.Body == "" {
		return PRCommentCreateRequest{}, fmt.Errorf("GitHub pull request comment body is required")
	}
	if request.Path == "" && request.Line > 0 {
		return PRCommentCreateRequest{}, fmt.Errorf("GitHub pull request inline comment path is required")
	}
	if request.Path != "" && request.Line <= 0 {
		return PRCommentCreateRequest{}, fmt.Errorf("GitHub pull request inline comment line must be greater than zero")
	}
	switch request.Side {
	case "":
		request.Side = "RIGHT"
	case "LEFT", "RIGHT":
	default:
		return PRCommentCreateRequest{}, fmt.Errorf("GitHub pull request inline comment side must be LEFT or RIGHT")
	}

	return request, nil
}

func normalizePRReviewThreadReplyRequest(request PRReviewThreadReplyRequest) (PRReviewThreadReplyRequest, error) {
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	request.Body = strings.TrimSpace(request.Body)
	if request.ThreadID == "" {
		return PRReviewThreadReplyRequest{}, fmt.Errorf("GitHub pull request review thread id is required")
	}
	if request.Body == "" {
		return PRReviewThreadReplyRequest{}, fmt.Errorf("GitHub pull request review thread reply body is required")
	}
	return request, nil
}

func splitRepository(repository string) (string, string, error) {
	repository, err := normalizeRepository(repository)
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(repository, "/")
	return parts[0], parts[1], nil
}

func (r *Runner) runCommandWithConfig(ctx context.Context, args []string) (CommandResult, resolvedConfig, error) {
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	return r.runCommandWithResolvedConfig(ctx, config, args)
}

func (r *Runner) runCommandWithResolvedConfig(ctx context.Context, config resolvedConfig, args []string) (CommandResult, resolvedConfig, error) {

	path, err := r.lookPath(config.Command)
	if err != nil {
		result := CommandResult{Command: config.Command, Args: append([]string(nil), args...), ExitCode: -1}
		return result, config, &Error{
			Code:    ErrorCodeNotInstalled,
			Message: fmt.Sprintf("GitHub CLI not found: %s", config.Command),
			Result:  result,
			Err:     err,
		}
	}

	result := CommandResult{
		Command:  config.Command,
		Path:     path,
		Args:     append([]string(nil), args...),
		ExitCode: 0,
	}

	runCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	outcome := r.runCommand(runCtx, path, result.Args)
	result.Stdout = strings.TrimSpace(outcome.stdout)
	result.Stderr = strings.TrimSpace(outcome.stderr)

	if outcome.err == nil {
		return result, config, nil
	}

	if errors.Is(outcome.err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		result.ExitCode = -1
		return result, config, &Error{
			Code:    ErrorCodeTimeout,
			Message: fmt.Sprintf("GitHub CLI command timed out after %s", config.Timeout),
			Result:  result,
			Err:     context.DeadlineExceeded,
		}
	}

	var exitErr *exec.ExitError
	if errors.As(outcome.err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, config, nil
	}

	type exitCoder interface{ ExitCode() int }
	var codedErr exitCoder
	if errors.As(outcome.err, &codedErr) {
		result.ExitCode = codedErr.ExitCode()
		return result, config, nil
	}

	return result, config, &Error{
		Code:    ErrorCodeExternalFailure,
		Message: fmt.Sprintf("GitHub CLI command failed to start: %v", outcome.err),
		Result:  result,
		Err:     outcome.err,
	}
}

func (r *Runner) loadConfig(ctx context.Context) (resolvedConfig, error) {
	config := resolvedConfig{Command: defaultCommand, Timeout: defaultTimeout}
	if r.systemConfig != nil {
		return resolveSystemConfig(*r.systemConfig)
	}

	repoRoot, err := r.resolveRepoRoot(ctx)
	if err != nil {
		return config, nil
	}

	content, err := r.readFile(filepath.Join(repoRoot, configRelativePath))
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}

		return resolvedConfig{}, fmt.Errorf("read GitHub integration config: %w", err)
	}

	var raw Config
	if err := json.Unmarshal(content, &raw); err != nil {
		return resolvedConfig{}, fmt.Errorf("parse GitHub integration config: %w", err)
	}

	command := firstNonEmpty(strings.TrimSpace(raw.Path), strings.TrimSpace(raw.Command), defaultCommand)
	config.Command = command
	config.DefaultRepo = firstNonEmpty(strings.TrimSpace(raw.Repository), strings.TrimSpace(raw.DefaultRepo))

	if strings.TrimSpace(raw.Timeout) == "" {
		return config, nil
	}

	timeout, err := time.ParseDuration(strings.TrimSpace(raw.Timeout))
	if err != nil {
		return resolvedConfig{}, fmt.Errorf("parse GitHub integration timeout: %w", err)
	}
	if timeout <= 0 {
		return resolvedConfig{}, fmt.Errorf("parse GitHub integration timeout: duration must be positive")
	}

	config.Timeout = timeout
	return config, nil
}

func resolveSystemConfig(raw integrationmodel.IntegrationSystemConfig) (resolvedConfig, error) {
	config := resolvedConfig{Command: defaultCommand, Timeout: defaultTimeout}
	config.Command = firstNonEmpty(strings.TrimSpace(raw.Path), strings.TrimSpace(raw.Command), defaultCommand)
	config.DefaultRepo = firstNonEmpty(strings.TrimSpace(raw.Repository), strings.TrimSpace(raw.DefaultRepo))

	if strings.TrimSpace(raw.Timeout) == "" {
		return config, nil
	}

	timeout, err := time.ParseDuration(strings.TrimSpace(raw.Timeout))
	if err != nil {
		return resolvedConfig{}, fmt.Errorf("parse GitHub integration timeout: %w", err)
	}
	if timeout <= 0 {
		return resolvedConfig{}, fmt.Errorf("parse GitHub integration timeout: duration must be positive")
	}

	config.Timeout = timeout
	return config, nil
}

func resolveRepoRoot(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve git repository root: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func defaultRunCommand(ctx context.Context, path string, args []string) commandRunner {
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return commandRunner{
		stdout: stdout.String(),
		stderr: stderr.String(),
		err:    err,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
