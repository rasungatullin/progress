package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
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

func TestRunnerUsesSystemConfigWithoutLegacyFile(t *testing.T) {
	t.Parallel()

	runner := NewRunnerWithSystemConfig(integrationmodel.IntegrationSystemConfig{
		Type:       "github",
		Command:    "/custom/gh",
		Timeout:    "5s",
		Repository: "owner/name",
	})
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	runner.lookPath = func(string) (string, error) { return "/custom/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/custom/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		return commandRunner{stdout: "ok"}
	}

	result, config, err := runner.RunRepoView(context.Background(), "")
	if err != nil {
		t.Fatalf("run repo view: %v", err)
	}
	if config.Command != "/custom/gh" {
		t.Fatalf("unexpected command: %q", config.Command)
	}
	if config.DefaultRepo != "owner/name" {
		t.Fatalf("unexpected default repo: %q", config.DefaultRepo)
	}
	if config.Timeout != 5*time.Second {
		t.Fatalf("unexpected timeout: %s", config.Timeout)
	}
	if result.Command != "/custom/gh" {
		t.Fatalf("unexpected result command: %q", result.Command)
	}
}

func TestRunnerUsesRepositoryFieldBeforeDefaultRepo(t *testing.T) {
	t.Parallel()

	runner := NewRunnerWithSystemConfig(integrationmodel.IntegrationSystemConfig{
		Type:        "github",
		Repository:  "owner/from-repository",
		DefaultRepo: "owner/from-default-repo",
	})
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"repo", "view", "owner/from-repository", "--json", "name,owner,description,defaultBranchRef,url"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"name":"name"}`}
	}

	_, config, err := runner.RunRepoView(context.Background(), "")
	if err != nil {
		t.Fatalf("run repo view: %v", err)
	}
	if config.DefaultRepo != "owner/from-repository" {
		t.Fatalf("unexpected default repo: %q", config.DefaultRepo)
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

func TestRunnerRunIssueListBuildsSearchCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"issue", "list", "--repo", "owner/name", "--state", "open", "--limit", "5", "--json", "number,title,state,labels,assignees,author,url,createdAt,updatedAt", "--search", `author:@me label:"ready" label:"backend" -label:"blocked" -label:"needs info"`}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `[]`}
	}

	result, config, err := runner.RunIssueList(context.Background(), "owner/name", IssueListRequest{State: "", Query: "author:@me", Labels: []string{"ready", "backend"}, ExcludeLabels: []string{"blocked", "needs info"}, Limit: 5})
	if err != nil {
		t.Fatalf("run issue list: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
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
		expected := []string{"api", "--paginate", "--slurp", "repos/owner/name/issues/123/comments"}
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
		expected := []string{"api", "--paginate", "--slurp", "repos/owner/name/issues/123/comments"}
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

func TestRunnerRunIssueLabelsAddBuildsEditCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"issue", "edit", "123", "--repo", "owner/name", "--add-label", "external-bug", "--add-label", "backend"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{}
	}

	result, config, err := runner.RunIssueLabelsAdd(context.Background(), "owner/name", 123, []string{"external-bug", "backend"})
	if err != nil {
		t.Fatalf("run issue label add: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunIssueLabelsRemoveBuildsEditCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"issue", "edit", "123", "--repo", "owner/name", "--remove-label", "external-bug"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{}
	}

	_, _, err := runner.RunIssueLabelsRemove(context.Background(), "owner/name", 123, []string{"external-bug"})
	if err != nil {
		t.Fatalf("run issue label remove: %v", err)
	}
}

func TestRunnerRunIssueLabelsRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	_, _, err := runner.RunIssueLabelsAdd(context.Background(), "owner/name", 0, []string{"bug"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunIssueLabelsAdd(context.Background(), "owner/name", 123, nil)
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)

	_, _, err = runner.RunIssueLabelsAdd(context.Background(), "owner", 123, []string{"bug"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
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
		expected := []string{"pr", "view", "123", "--repo", "owner/name", "--json", "number,title,body,state,mergeable,mergeStateStatus,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"}
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
		expected := []string{"pr", "view", "123", "--repo", "owner/name", "--json", "number,title,body,state,mergeable,mergeStateStatus,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"}
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

func TestRunnerRunPRListBuildsCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"pr", "list", "--repo", "owner/name", "--state", "closed", "--limit", "5", "--json", "number,title,body,state,mergeable,mergeStateStatus,author,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt", "--search", "label:bug reviewed-by:@me"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `[]`}
	}

	result, config, err := runner.RunPRList(context.Background(), "owner/name", PRListRequest{State: "", Scope: "reviewer", Query: "label:bug", Limit: 5})
	if err != nil {
		t.Fatalf("run pr list: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunPRListCanUseCurrentRepository(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"pr", "list", "--state", "closed", "--limit", "30", "--json", "number,title,body,state,mergeable,mergeStateStatus,author,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `[]`}
	}

	if _, _, err := runner.RunPRList(context.Background(), "", PRListRequest{}); err != nil {
		t.Fatalf("run pr list: %v", err)
	}
}

func TestRunnerRunPRReviewThreadResolveBuildsGraphQLCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		if len(args) < 6 || args[0] != "api" || args[1] != "graphql" {
			t.Fatalf("unexpected args: %#v", args)
		}
		if !strings.Contains(fmt.Sprint(args), "resolveReviewThread") || !strings.Contains(fmt.Sprint(args), "threadId=thread-1") {
			t.Fatalf("unexpected graphql args: %#v", args)
		}
		return commandRunner{stdout: `{"data":{"resolveReviewThread":{"thread":{"id":"thread-1","isResolved":true}}}}`}
	}

	result, config, err := runner.RunPRReviewThreadResolve(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("run pr review thread resolve: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
	}
	if config.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", config.Command)
	}
}

func TestRunnerRunPRCommentCreateUsesRESTEndpoint(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		expected := []string{"api", "--method", "POST", "repos/owner/name/pulls/42/comments", "-f", "body=Inline remark", "-f", "path=file.go", "-F", "line=12", "-f", "side=RIGHT"}
		if fmt.Sprint(args) != fmt.Sprint(expected) {
			t.Fatalf("unexpected args: %#v", args)
		}
		return commandRunner{stdout: `{"id":101,"node_id":"PRRC_comment-1"}`}
	}

	result, _, err := runner.RunPRCommentCreate(context.Background(), "owner/name", 42, PRCommentCreateRequest{Body: "Inline remark", Path: "file.go", Line: 12, Side: "RIGHT"})
	if err != nil {
		t.Fatalf("create inline comment: %v", err)
	}
	if !strings.Contains(result.Stdout, "PRRC_comment-1") {
		t.Fatalf("unexpected response: %s", result.Stdout)
	}
}

func TestRunnerRunPRReviewThreadReplyBuildsGraphQLCommand(t *testing.T) {
	t.Parallel()

	runner := NewRunner()
	runner.resolveRepoRoot = func(context.Context) (string, error) { return "/repo", nil }
	runner.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	runner.lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	runner.runCommand = func(_ context.Context, path string, args []string) commandRunner {
		if path != "/usr/bin/gh" {
			t.Fatalf("unexpected path: %q", path)
		}
		if len(args) < 6 || args[0] != "api" || args[1] != "graphql" {
			t.Fatalf("unexpected args: %#v", args)
		}
		joined := fmt.Sprint(args)
		if !strings.Contains(joined, "addPullRequestReviewThreadReply") || !strings.Contains(joined, "threadId=thread-1") || !strings.Contains(joined, "body=Reply body") {
			t.Fatalf("unexpected graphql args: %#v", args)
		}
		return commandRunner{stdout: `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"comment-1","body":"Reply body","url":"https://github.com/owner/name/pull/42#discussion_r1"}}}}`}
	}

	result, config, err := runner.RunPRReviewThreadReply(context.Background(), PRReviewThreadReplyRequest{ThreadID: "thread-1", Body: "Reply body"})
	if err != nil {
		t.Fatalf("run pr review thread reply: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", result.ExitCode)
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
