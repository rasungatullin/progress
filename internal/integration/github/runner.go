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
	"strings"
	"time"
)

const (
	configRelativePath = ".progress/integration/github.json"
	defaultCommand     = "gh"
	defaultTimeout     = 30 * time.Second

	ErrorCodeNotInstalled    = "gh-not-installed"
	ErrorCodeAuthRequired    = "auth-required"
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
}

func NewRunner() *Runner {
	return &Runner{
		resolveRepoRoot: resolveRepoRoot,
		readFile:        os.ReadFile,
		lookPath:        exec.LookPath,
		runCommand:      defaultRunCommand,
	}
}

func (r *Runner) RunAuthStatus(ctx context.Context) (CommandResult, resolvedConfig, error) {
	config, err := r.loadConfig(ctx)
	if err != nil {
		return CommandResult{}, resolvedConfig{}, err
	}

	path, err := r.lookPath(config.Command)
	if err != nil {
		result := CommandResult{Command: config.Command, Args: []string{"auth", "status"}, ExitCode: -1}
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
		Args:     []string{"auth", "status"},
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

	return result, config, &Error{
		Code:    ErrorCodeExternalFailure,
		Message: fmt.Sprintf("GitHub CLI command failed to start: %v", outcome.err),
		Result:  result,
		Err:     outcome.err,
	}
}

func (r *Runner) loadConfig(ctx context.Context) (resolvedConfig, error) {
	config := resolvedConfig{Command: defaultCommand, Timeout: defaultTimeout}

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
