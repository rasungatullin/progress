package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRunnerRunAuthStatusReturnsNotInstalled(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "", fmt.Errorf("look path: %w", execErrNotFound) }

	result, config, err := runner.RunAuthStatus(context.Background())
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
	if result.Command != defaultCommand {
		t.Fatalf("unexpected result command: %q", result.Command)
	}
	assertGitHubErrorCode(t, err, ErrorCodeNotInstalled)
}

func TestRunnerRunAuthStatusSuccess(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"command":"/custom/gh","timeout":"5s"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/custom/gh", nil }
	runner.runCommand = func(context.Context, string, []string) commandRunner {
		return commandRunner{stdout: "ok", stderr: ""}
	}

	result, config, err := runner.RunAuthStatus(context.Background())
	if err != nil {
		t.Fatalf("run auth status: %v", err)
	}
	if config.Command != "/custom/gh" {
		t.Fatalf("unexpected command: %q", config.Command)
	}
	if config.Timeout != 5*time.Second {
		t.Fatalf("unexpected timeout: %s", config.Timeout)
	}
	if result.Path != "/custom/gh" {
		t.Fatalf("unexpected path: %q", result.Path)
	}
	if result.Stdout != "ok" {
		t.Fatalf("unexpected stdout: %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
}

func TestRunnerRunAuthStatusReturnsExitCodeOnCommandFailure(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(context.Context, string, []string) commandRunner {
		return commandRunner{stderr: "auth failed", err: fakeExitError{code: 1}}
	}

	result, _, err := runner.RunAuthStatus(context.Background())
	if err != nil {
		t.Fatalf("run auth status: %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if result.Stderr != "auth failed" {
		t.Fatalf("unexpected stderr: %q", result.Stderr)
	}
}

func TestRunnerRunAuthStatusReturnsTimeout(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"timeout":"10ms"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(ctx context.Context, _ string, _ []string) commandRunner {
		<-ctx.Done()
		return commandRunner{stderr: "timed out", err: ctx.Err()}
	}

	result, _, err := runner.RunAuthStatus(context.Background())
	if !result.TimedOut {
		t.Fatal("expected timeout result")
	}
	assertGitHubErrorCode(t, err, ErrorCodeTimeout)
}

func assertGitHubErrorCode(t *testing.T, err error, code string) {
	t.Helper()

	var ghErr *Error
	if !errors.As(err, &ghErr) {
		t.Fatalf("expected GitHub error, got %v", err)
	}
	if ghErr.Code != code {
		t.Fatalf("unexpected GitHub error code: %q", ghErr.Code)
	}
}

var execErrNotFound = errors.New("executable file not found")

type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func (e fakeExitError) ExitCode() int {
	return e.code
}
