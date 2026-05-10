package github

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceAuthStatusSuccess(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: 0, Stdout: "Logged in"},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "auth", Operation: "status"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.AuthStatus == nil {
		t.Fatal("expected auth status")
	}
	if response.AuthStatus.State != StateReady {
		t.Fatalf("unexpected state: %q", response.AuthStatus.State)
	}
	if !response.AuthStatus.Authenticated {
		t.Fatal("expected authenticated status")
	}
}

func TestServiceAuthStatusMapsAuthRequired(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: 1, Stderr: "You are not logged into any GitHub hosts. Run gh auth login."},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "auth", Operation: "status"})
	assertGitHubErrorCode(t, err, ErrorCodeAuthRequired)
	if response.AuthStatus == nil {
		t.Fatal("expected auth status")
	}
	if response.AuthStatus.State != StateAuthRequired {
		t.Fatalf("unexpected state: %q", response.AuthStatus.State)
	}
	if response.AuthStatus.Authenticated {
		t.Fatal("expected unauthenticated status")
	}
}

func TestServiceAuthStatusMapsTimeout(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: -1, TimedOut: true},
		config: resolvedConfig{Command: "gh", Timeout: 10 * time.Millisecond},
		err:    &Error{Code: ErrorCodeTimeout, Message: "GitHub CLI command timed out after 10ms"},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "auth", Operation: "status"})
	assertGitHubErrorCode(t, err, ErrorCodeTimeout)
	if response.AuthStatus == nil {
		t.Fatal("expected auth status")
	}
	if response.AuthStatus.State != StateTimeout {
		t.Fatalf("unexpected state: %q", response.AuthStatus.State)
	}
}

func TestServiceAuthStatusMapsGenericRunnerError(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		err: errors.New("parse GitHub integration config: unexpected end of JSON input"),
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "auth", Operation: "status"})
	assertGitHubErrorCode(t, err, ErrorCodeExternalFailure)
	if response.AuthStatus == nil {
		t.Fatal("expected auth status")
	}
	if response.AuthStatus.State != StateExternalFailure {
		t.Fatalf("unexpected state: %q", response.AuthStatus.State)
	}
	if response.AuthStatus.Message != "parse GitHub integration config: unexpected end of JSON input" {
		t.Fatalf("unexpected message: %q", response.AuthStatus.Message)
	}
	if response.AuthStatus.ExitCode != -1 {
		t.Fatalf("unexpected exit code: %d", response.AuthStatus.ExitCode)
	}
	if response.AuthStatus.Command != defaultCommand {
		t.Fatalf("unexpected command: %q", response.AuthStatus.Command)
	}
}

func TestServiceRejectsUnsupportedOperation(t *testing.T) {
	t.Parallel()

	service := NewService()
	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.Resource != "issue" {
		t.Fatalf("unexpected resource: %q", response.Resource)
	}
}

func TestServiceRepoGetSuccess(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 0,
			Stdout:   `{"name":"progress","owner":{"login":"rasungatullin"},"description":"Repository description","defaultBranchRef":{"name":"main"},"url":"https://github.com/rasungatullin/progress"}`,
		},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get", Repository: "rasungatullin/progress"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.RepositoryRef == nil {
		t.Fatal("expected repository")
	}
	if response.RepositoryRef.System != "github" {
		t.Fatalf("unexpected system: %q", response.RepositoryRef.System)
	}
	if response.RepositoryRef.FullName != "rasungatullin/progress" {
		t.Fatalf("unexpected full name: %q", response.RepositoryRef.FullName)
	}
	if response.RepositoryRef.DefaultBranch != "main" {
		t.Fatalf("unexpected default branch: %q", response.RepositoryRef.DefaultBranch)
	}
	if stub.repo != "rasungatullin/progress" {
		t.Fatalf("unexpected requested repo: %q", stub.repo)
	}
}

func TestServiceRepoGetRejectsEmptyRepository(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.runner = &stubRunner{}

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.RepositoryStatus == nil {
		t.Fatal("expected repository status")
	}
	if response.RepositoryStatus.State != ErrorCodeInvalidRequest {
		t.Fatalf("unexpected state: %q", response.RepositoryStatus.State)
	}
	if response.RepositoryStatus.Message != "GitHub repository is required" {
		t.Fatalf("unexpected message: %q", response.RepositoryStatus.Message)
	}
}

func TestServiceRepoGetMapsNotFound(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 1,
			Stderr:   "GraphQL: Could not resolve to a Repository with the name 'missing/repo'.",
		},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get", Repository: "missing/repo"})
	assertGitHubErrorCode(t, err, ErrorCodeNotFound)
	if response.RepositoryStatus == nil {
		t.Fatal("expected repository status")
	}
	if response.RepositoryStatus.State != ErrorCodeNotFound {
		t.Fatalf("unexpected state: %q", response.RepositoryStatus.State)
	}
	if response.RepositoryStatus.Repository != "missing/repo" {
		t.Fatalf("unexpected repository: %q", response.RepositoryStatus.Repository)
	}
}

func TestServiceRepoGetMapsAuthRequired(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 1,
			Stderr:   "You are not logged into any GitHub hosts. Run gh auth login.",
		},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get", Repository: "owner/name"})
	assertGitHubErrorCode(t, err, ErrorCodeAuthRequired)
	if response.RepositoryStatus == nil {
		t.Fatal("expected repository status")
	}
	if response.RepositoryStatus.State != ErrorCodeAuthRequired {
		t.Fatalf("unexpected state: %q", response.RepositoryStatus.State)
	}
	if response.RepositoryStatus.ExitCode != 1 {
		t.Fatalf("unexpected exit code: %d", response.RepositoryStatus.ExitCode)
	}
}

func TestServiceRepoGetMapsNotInstalled(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{Command: "gh", ExitCode: -1},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
		err: &Error{
			Code:    ErrorCodeNotInstalled,
			Message: "GitHub CLI not found: gh",
			Result:  CommandResult{Command: "gh", ExitCode: -1},
		},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get", Repository: "owner/name"})
	assertGitHubErrorCode(t, err, ErrorCodeNotInstalled)
	if response.RepositoryStatus == nil {
		t.Fatal("expected repository status")
	}
	if response.RepositoryStatus.State != StateNotInstalled {
		t.Fatalf("unexpected state: %q", response.RepositoryStatus.State)
	}
	if response.RepositoryStatus.Message != "GitHub CLI not found: gh" {
		t.Fatalf("unexpected message: %q", response.RepositoryStatus.Message)
	}
	if response.RepositoryStatus.ExitCode != -1 {
		t.Fatalf("unexpected exit code: %d", response.RepositoryStatus.ExitCode)
	}
	if response.RepositoryRef != nil {
		t.Fatal("did not expect repository ref")
	}
	if stub.repo != "owner/name" {
		t.Fatalf("unexpected requested repo: %q", stub.repo)
	}
}

func TestServiceRepoGetMapsMalformedJSONToNormalizedError(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 0,
			Stdout:   `{"name":`,
		},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get", Repository: "owner/name"})
	assertGitHubErrorCode(t, err, ErrorCodeExternalFailure)
	if response.RepositoryStatus == nil {
		t.Fatal("expected repository status")
	}
	if response.RepositoryStatus.State != StateExternalFailure {
		t.Fatalf("unexpected state: %q", response.RepositoryStatus.State)
	}
	if !strings.Contains(response.RepositoryStatus.Message, "unexpected GitHub CLI JSON response") {
		t.Fatalf("unexpected message: %q", response.RepositoryStatus.Message)
	}
	if response.RepositoryStatus.Stdout != `{"name":` {
		t.Fatalf("unexpected stdout: %q", response.RepositoryStatus.Stdout)
	}
	if response.RepositoryRef != nil {
		t.Fatal("did not expect repository ref")
	}
}

func TestServiceRepoGetMapsUnexpectedExternalFailureToNormalizedError(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{Command: "gh", ExitCode: -1},
		err:    errors.New("gh spawn failed"),
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "repo", Operation: "get", Repository: "owner/name"})
	assertGitHubErrorCode(t, err, ErrorCodeExternalFailure)
	if response.RepositoryStatus == nil {
		t.Fatal("expected repository status")
	}
	if response.RepositoryStatus.State != StateExternalFailure {
		t.Fatalf("unexpected state: %q", response.RepositoryStatus.State)
	}
	if response.RepositoryStatus.Message != "gh spawn failed" {
		t.Fatalf("unexpected message: %q", response.RepositoryStatus.Message)
	}
}

type stubRunner struct {
	result CommandResult
	config resolvedConfig
	err    error
	repo   string
}

func (r *stubRunner) RunAuthStatus(context.Context) (CommandResult, resolvedConfig, error) {
	return r.result, r.config, r.err
}

func (r *stubRunner) RunRepoView(_ context.Context, repository string) (CommandResult, resolvedConfig, error) {
	r.repo = repository
	return r.result, r.config, r.err
}
