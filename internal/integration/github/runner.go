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

	ErrorCodeNotInstalled    = "gh-not-installed"
	ErrorCodeAuthRequired    = "auth-required"
	ErrorCodeNotFound        = "not-found"
	ErrorCodeAlreadyExists   = "already-exists"
	ErrorCodeTimeout         = "timeout"
	ErrorCodeExternalFailure = "unexpected-external-failure"
	ErrorCodeInvalidRequest  = "invalid-request"

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

	return r.runCommandWithResolvedConfig(ctx, config, []string{"issue", "view", strconv.Itoa(number), "--repo", repository, "--json", "number,title,body,state,labels,assignees,author,url,createdAt,updatedAt"})
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

	return r.runCommandWithResolvedConfig(ctx, config, []string{"pr", "view", strconv.Itoa(number), "--repo", repository, "--json", "number,title,body,state,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"})
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

type PRCreateRequest struct {
	Base  string
	Head  string
	Title string
	Body  string
	Draft bool
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
	config.DefaultRepo = strings.TrimSpace(raw.DefaultRepo)

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
	config.DefaultRepo = strings.TrimSpace(raw.DefaultRepo)

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
