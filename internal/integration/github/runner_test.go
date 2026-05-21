package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
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

func TestRunnerRunAuthStatusReturnsTimeoutForKilledProcess(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"timeout":"10ms"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(ctx context.Context, _ string, _ []string) commandRunner {
		return defaultRunCommand(ctx, "/bin/sh", []string{"-c", "sleep 1"})
	}

	result, _, err := runner.RunAuthStatus(context.Background())
	if !result.TimedOut {
		t.Fatal("expected timeout result")
	}
	if result.ExitCode != -1 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	assertGitHubErrorCode(t, err, ErrorCodeTimeout)
}

func TestRunnerRunAuthStatusReturnsConfigReadError(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrPermission }

	_, _, err := runner.RunAuthStatus(context.Background())
	if err == nil {
		t.Fatal("expected config read error")
	}
	if !strings.Contains(err.Error(), "read GitHub integration config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunnerRunAuthStatusReturnsConfigParseError(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"timeout":`), nil }

	_, _, err := runner.RunAuthStatus(context.Background())
	if err == nil {
		t.Fatal("expected config parse error")
	}
	if !strings.Contains(err.Error(), "parse GitHub integration config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunnerRunRepoViewBuildsJSONCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"repo", "view", "owner/name", "--json", "name,owner,description,defaultBranchRef,url"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"name":"name"}`}
	}

	result, config, err := runner.RunRepoView(context.Background(), "owner/name")
	if err != nil {
		t.Fatalf("run repo view: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunRepoViewRejectsEmptyRepositoryWithoutDefault(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	called := false
	runner.runCommand = func(context.Context, string, []string) commandRunner {
		called = true
		return commandRunner{}
	}

	_, _, err := runner.RunRepoView(context.Background(), " ")
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if called {
		t.Fatal("did not expect gh invocation")
	}
}

func TestRunnerRunRepoViewUsesConfiguredDefaultRepository(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"default_repo":"owner/name"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"repo", "view", "owner/name", "--json", "name,owner,description,defaultBranchRef,url"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"name":"name"}`}
	}

	_, config, err := runner.RunRepoView(context.Background(), " ")
	if err != nil {
		t.Fatalf("run repo view: %v", err)
	}
	if config.DefaultRepo != "owner/name" {
		t.Fatalf("unexpected default repo: %q", config.DefaultRepo)
	}
}

func TestRunnerRunRepoViewRejectsMalformedRepositoryFormat(t *testing.T) {
	t.Parallel()

	invalid := []string{"owner", "owner/", "/name", "owner/name/extra", "owner /name"}
	for _, repository := range invalid {
		repository := repository
		t.Run(repository, func(t *testing.T) {
			t.Parallel()

			runner := NewRunner()
			called := false
			runner.runCommand = func(context.Context, string, []string) commandRunner {
				called = true
				return commandRunner{}
			}

			_, _, err := runner.RunRepoView(context.Background(), repository)
			assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
			if err.Error() != "GitHub repository must use owner/name format" {
				t.Fatalf("unexpected error: %v", err)
			}
			if called {
				t.Fatal("did not expect gh invocation")
			}
		})
	}
}

func TestRunnerRunIssueViewBuildsJSONCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"issue", "view", "123", "--repo", "owner/name", "--json", "number,title,body,state,labels,assignees,author,url,createdAt,updatedAt"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"number":123}`}
	}

	result, config, err := runner.RunIssueView(context.Background(), "owner/name", 123)
	if err != nil {
		t.Fatalf("run issue view: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunIssueViewRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	_, _, err := runner.RunIssueView(context.Background(), "owner", 123)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunIssueView(context.Background(), "owner/name", 0)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
}

func TestRunnerRunIssueViewUsesConfiguredDefaultRepository(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"default_repo":"owner/name"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"issue", "view", "123", "--repo", "owner/name", "--json", "number,title,body,state,labels,assignees,author,url,createdAt,updatedAt"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"number":123}`}
	}

	_, config, err := runner.RunIssueView(context.Background(), "", 123)
	if err != nil {
		t.Fatalf("run issue view: %v", err)
	}
	if config.DefaultRepo != "owner/name" {
		t.Fatalf("unexpected default repo: %q", config.DefaultRepo)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunIssueCommentsBuildsAPICommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"api", "repos/owner/name/issues/123/comments"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `[]`}
	}

	result, config, err := runner.RunIssueComments(context.Background(), "owner/name", 123)
	if err != nil {
		t.Fatalf("run issue comments: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunIssueCommentsRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	_, _, err := runner.RunIssueComments(context.Background(), "owner", 123)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunIssueComments(context.Background(), "owner/name", 0)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
}

func TestRunnerRunIssueCommentsUsesConfiguredDefaultRepository(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"default_repo":"owner/name"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"api", "repos/owner/name/issues/123/comments"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `[]`}
	}

	_, config, err := runner.RunIssueComments(context.Background(), "", 123)
	if err != nil {
		t.Fatalf("run issue comments: %v", err)
	}
	if config.DefaultRepo != "owner/name" {
		t.Fatalf("unexpected default repo: %q", config.DefaultRepo)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunPRViewBuildsJSONCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"pr", "view", "123", "--repo", "owner/name", "--json", "number,title,body,state,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"number":123}`}
	}

	result, config, err := runner.RunPRView(context.Background(), "owner/name", 123)
	if err != nil {
		t.Fatalf("run pr view: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunPRViewRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	_, _, err := runner.RunPRView(context.Background(), "owner", 123)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunPRView(context.Background(), "owner/name", 0)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
}

func TestRunnerRunPRViewUsesConfiguredDefaultRepository(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return []byte(`{"default_repo":"owner/name"}`), nil }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"pr", "view", "123", "--repo", "owner/name", "--json", "number,title,body,state,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"number":123}`}
	}

	_, config, err := runner.RunPRView(context.Background(), "", 123)
	if err != nil {
		t.Fatalf("run pr view: %v", err)
	}
	if config.DefaultRepo != "owner/name" {
		t.Fatalf("unexpected default repo: %q", config.DefaultRepo)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunPRCreateBuildsCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"pr", "create", "--repo", "owner/name", "--base", "main", "--head", "feature/branch", "--title", "Add feature", "--body", "Implements the feature", "--draft"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: "https://github.com/owner/name/pull/12"}
	}

	result, config, err := runner.RunPRCreate(context.Background(), "owner/name", PRCreateRequest{Base: "main", Head: "feature/branch", Title: "Add feature", Body: "Implements the feature", Draft: true})
	if err != nil {
		t.Fatalf("run pr create: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunPRCreateRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	runner := NewRunner()

	_, _, err := runner.RunPRCreate(context.Background(), "owner", PRCreateRequest{Base: "main", Head: "feature", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunPRCreate(context.Background(), "owner/name", PRCreateRequest{Head: "feature", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunPRCreate(context.Background(), "owner/name", PRCreateRequest{Base: "main", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunPRCreate(context.Background(), "owner/name", PRCreateRequest{Base: "main", Head: "feature", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunPRCreate(context.Background(), "owner/name", PRCreateRequest{Base: "main", Head: "feature", Title: "Title"})
	if err != nil {
		t.Fatalf("empty body must be accepted: %v", err)
	}

	_, _, err = runner.RunPRCreate(context.Background(), "owner/name", PRCreateRequest{Base: "main", Head: "main", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
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
