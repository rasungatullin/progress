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
	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "comments"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.Resource != "issue" {
		t.Fatalf("unexpected resource: %q", response.Resource)
	}
}

func TestServiceIssueGetSuccess(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 0,
			Stdout:   `{"number":123,"title":"Fix integration","body":"Line one\nLine two","state":"OPEN","labels":[{"name":"bug"},{"name":"integration"}],"assignees":[{"login":"alice","name":"Alice","url":"https://github.com/alice","isBot":false,"isActive":true}],"author":{"login":"bob","name":"Bob","url":"https://github.com/bob","isBot":false,"isActive":true},"url":"https://github.com/owner/name/issues/123","createdAt":"2026-05-01T10:00:00Z","updatedAt":"2026-05-02T10:00:00Z"}`,
		},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get", Repository: "owner/name", Number: 123})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.Issue == nil {
		t.Fatal("expected issue")
	}
	if response.Issue.Repository != "owner/name" {
		t.Fatalf("unexpected repository: %q", response.Issue.Repository)
	}
	if response.Issue.Number != 123 {
		t.Fatalf("unexpected number: %d", response.Issue.Number)
	}
	if len(response.Issue.Labels) != 2 || response.Issue.Labels[0] != "bug" || response.Issue.Labels[1] != "integration" {
		t.Fatalf("unexpected labels: %#v", response.Issue.Labels)
	}
	if len(response.Issue.Assignees) != 1 || response.Issue.Assignees[0].Login != "alice" {
		t.Fatalf("unexpected assignees: %#v", response.Issue.Assignees)
	}
	if response.Issue.Author.Login != "bob" {
		t.Fatalf("unexpected author: %#v", response.Issue.Author)
	}
	if stub.repo != "owner/name" || stub.number != 123 {
		t.Fatalf("unexpected requested issue: repo=%q number=%d", stub.repo, stub.number)
	}
	if response.IssueStatus != nil {
		t.Fatal("did not expect issue status on success")
	}
}

func TestServiceIssueGetRejectsInvalidRepository(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.runner = &stubRunner{}

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get", Repository: "owner", Number: 123})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.IssueStatus == nil {
		t.Fatal("expected issue status")
	}
	if response.IssueStatus.State != ErrorCodeInvalidRequest {
		t.Fatalf("unexpected state: %q", response.IssueStatus.State)
	}
	if response.IssueStatus.Message != "GitHub repository must use owner/name format" {
		t.Fatalf("unexpected message: %q", response.IssueStatus.Message)
	}
}

func TestServiceIssueGetRejectsNonPositiveNumber(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.runner = &stubRunner{}

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get", Repository: "owner/name", Number: 0})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.IssueStatus == nil {
		t.Fatal("expected issue status")
	}
	if response.IssueStatus.State != ErrorCodeInvalidRequest {
		t.Fatalf("unexpected state: %q", response.IssueStatus.State)
	}
	if response.IssueStatus.Message != "GitHub issue number must be greater than zero" {
		t.Fatalf("unexpected message: %q", response.IssueStatus.Message)
	}
}

func TestServiceIssueGetMapsNotFound(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 1,
			Stderr:   "GraphQL: Could not resolve to an Issue with the number of 123.",
		},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get", Repository: "owner/name", Number: 123})
	assertGitHubErrorCode(t, err, ErrorCodeNotFound)
	if response.IssueStatus == nil {
		t.Fatal("expected issue status")
	}
	if response.IssueStatus.State != ErrorCodeNotFound {
		t.Fatalf("unexpected state: %q", response.IssueStatus.State)
	}
	if response.IssueStatus.Repository != "owner/name" || response.IssueStatus.Number != 123 {
		t.Fatalf("unexpected issue target: %#v", response.IssueStatus)
	}
}

func TestServiceIssueGetMapsAuthRequired(t *testing.T) {
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

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get", Repository: "owner/name", Number: 123})
	assertGitHubErrorCode(t, err, ErrorCodeAuthRequired)
	if response.IssueStatus == nil {
		t.Fatal("expected issue status")
	}
	if response.IssueStatus.State != ErrorCodeAuthRequired {
		t.Fatalf("unexpected state: %q", response.IssueStatus.State)
	}
}

func TestServiceIssueGetMapsMalformedJSONToNormalizedError(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 0,
			Stdout:   `{"number":`,
		},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get", Repository: "owner/name", Number: 123})
	assertGitHubErrorCode(t, err, ErrorCodeExternalFailure)
	if response.IssueStatus == nil {
		t.Fatal("expected issue status")
	}
	if response.IssueStatus.State != StateExternalFailure {
		t.Fatalf("unexpected state: %q", response.IssueStatus.State)
	}
	if !strings.Contains(response.IssueStatus.Message, "unexpected GitHub CLI JSON response") {
		t.Fatalf("unexpected message: %q", response.IssueStatus.Message)
	}
	if response.Issue != nil {
		t.Fatal("did not expect issue")
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

func TestServicePRCreateSuccess(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 0,
			Stdout:   "https://github.com/owner/name/pull/42",
		},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature/branch", Title: "Add integration", Body: "Implements pr create", Draft: true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.PullRequestStatus == nil {
		t.Fatal("expected pull request status")
	}
	if response.PullRequestStatus.State != "OPEN" {
		t.Fatalf("unexpected state: %q", response.PullRequestStatus.State)
	}
	if response.PullRequestStatus.URL != "https://github.com/owner/name/pull/42" {
		t.Fatalf("unexpected url: %q", response.PullRequestStatus.URL)
	}
	if response.PullRequestStatus.Number != 42 {
		t.Fatalf("unexpected number: %d", response.PullRequestStatus.Number)
	}
	if response.PullRequestStatus.Draft != true {
		t.Fatal("expected draft status")
	}
	if response.PullRequest != nil {
		t.Fatalf("did not expect pull request payload: %#v", response.PullRequest)
	}
	if stub.repo != "owner/name" || stub.base != "main" || stub.head != "feature/branch" || stub.title != "Add integration" || stub.body != "Implements pr create" || !stub.draft {
		t.Fatalf("unexpected pr create request: %#v", stub)
	}
}

func TestServicePRCreateRejectsInvalidRepository(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.runner = &stubRunner{}

	for _, tt := range []struct {
		repository string
		message    string
	}{
		{repository: "", message: "GitHub repository is required"},
		{repository: "owner", message: "GitHub repository must use owner/name format"},
	} {
		tt := tt
		t.Run(tt.repository, func(t *testing.T) {
			response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: tt.repository, Base: "main", Head: "feature", Title: "Title", Body: "Body"})
			assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
			if response.PullRequestStatus == nil {
				t.Fatal("expected pull request status")
			}
			if response.PullRequestStatus.Message != tt.message {
				t.Fatalf("unexpected message: %q", response.PullRequestStatus.Message)
			}
		})
	}
}

func TestServicePRCreateRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request model.ProviderRequest
		message string
	}{
		{name: "missing base", request: model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Head: "feature", Title: "Title", Body: "Body"}, message: "GitHub pull request base branch is required"},
		{name: "missing head", request: model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Title: "Title", Body: "Body"}, message: "GitHub pull request head branch is required"},
		{name: "missing title", request: model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Body: "Body"}, message: "GitHub pull request title is required"},
		{name: "missing body", request: model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title"}, message: "GitHub pull request body is required"},
		{name: "same branches", request: model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "main", Title: "Title", Body: "Body"}, message: "GitHub pull request base and head branches must differ"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			service := NewService()
			service.runner = &stubRunner{}

			response, err := service.Execute(context.Background(), tt.request)
			assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
			if response.PullRequestStatus == nil {
				t.Fatal("expected pull request status")
			}
			if response.PullRequestStatus.Message != tt.message {
				t.Fatalf("unexpected message: %q", response.PullRequestStatus.Message)
			}
		})
	}
}

func TestServicePRCreateMapsAuthRequired(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: 1, Stderr: "You are not logged into any GitHub hosts. Run gh auth login."}}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeAuthRequired)
	if response.PullRequestStatus == nil || response.PullRequestStatus.State != ErrorCodeAuthRequired {
		t.Fatalf("unexpected status: %#v", response.PullRequestStatus)
	}
}

func TestServicePRCreateMapsAlreadyExists(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: 1, Stderr: "a pull request for branch \"feature\" into branch \"main\" already exists:\nhttps://github.com/owner/name/pull/15"}}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeAlreadyExists)
	if response.PullRequestStatus == nil {
		t.Fatal("expected pull request status")
	}
	if response.PullRequestStatus.State != ErrorCodeAlreadyExists {
		t.Fatalf("unexpected state: %q", response.PullRequestStatus.State)
	}
	if response.PullRequestStatus.URL != "https://github.com/owner/name/pull/15" {
		t.Fatalf("unexpected url: %q", response.PullRequestStatus.URL)
	}
	if response.PullRequestStatus.Number != 15 {
		t.Fatalf("unexpected number: %d", response.PullRequestStatus.Number)
	}
}

func TestServicePRCreateMapsNoCommitsBetweenBranches(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: 1, Stderr: "GraphQL: No commits between main and feature"}}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.PullRequestStatus == nil {
		t.Fatal("expected pull request status")
	}
	if response.PullRequestStatus.State != ErrorCodeInvalidRequest {
		t.Fatalf("unexpected state: %q", response.PullRequestStatus.State)
	}
	if response.PullRequestStatus.Message != "GitHub pull request cannot be created because feature has no commits ahead of main" {
		t.Fatalf("unexpected message: %q", response.PullRequestStatus.Message)
	}
}

func TestServicePRCreateRejectsSuccessfulResponseWithoutParseablePRURL(t *testing.T) {
	t.Parallel()

	stub := &stubRunner{
		result: CommandResult{
			Command:  "gh",
			Path:     "/usr/bin/gh",
			ExitCode: 0,
			Stdout:   "created pull request successfully",
		},
		config: resolvedConfig{Command: "gh", Timeout: 30 * time.Second},
	}
	service := NewService()
	service.runner = stub

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title", Body: "Body"})
	assertGitHubErrorCode(t, err, ErrorCodeExternalFailure)
	if response.PullRequestStatus == nil {
		t.Fatal("expected pull request status")
	}
	if response.PullRequestStatus.State != StateExternalFailure {
		t.Fatalf("unexpected state: %q", response.PullRequestStatus.State)
	}
	if response.PullRequestStatus.Message != "unexpected GitHub CLI response: missing pull request URL or number" {
		t.Fatalf("unexpected message: %q", response.PullRequestStatus.Message)
	}
	if response.PullRequestStatus.URL != "" {
		t.Fatalf("unexpected url: %q", response.PullRequestStatus.URL)
	}
	if response.PullRequestStatus.Number != 0 {
		t.Fatalf("unexpected number: %d", response.PullRequestStatus.Number)
	}
}

func TestServicePRCreateMapsMissingRepositoryOrBranchToNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stderr string
	}{
		{name: "repository", stderr: "GraphQL: Could not resolve to a Repository with the name 'owner/name'."},
		{name: "base branch", stderr: "GraphQL: Base ref must be a branch (not found)"},
		{name: "head branch", stderr: "GraphQL: Head ref must be a branch (not found)"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubRunner{result: CommandResult{Command: "gh", Path: "/usr/bin/gh", ExitCode: 1, Stderr: tt.stderr}}
			service := NewService()
			service.runner = stub

			response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "pr", Operation: "create", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title", Body: "Body"})
			assertGitHubErrorCode(t, err, ErrorCodeNotFound)
			if response.PullRequestStatus == nil {
				t.Fatal("expected pull request status")
			}
			if response.PullRequestStatus.State != ErrorCodeNotFound {
				t.Fatalf("unexpected state: %q", response.PullRequestStatus.State)
			}
			if response.PullRequestStatus.Message != "GitHub repository or branch not found for pull request creation: owner/name feature -> main" {
				t.Fatalf("unexpected message: %q", response.PullRequestStatus.Message)
			}
		})
	}
}

type stubRunner struct {
	result CommandResult
	config resolvedConfig
	err    error
	repo   string
	number int
	base   string
	head   string
	title  string
	body   string
	draft  bool
}

func (r *stubRunner) RunAuthStatus(context.Context) (CommandResult, resolvedConfig, error) {
	return r.result, r.config, r.err
}

func (r *stubRunner) RunRepoView(_ context.Context, repository string) (CommandResult, resolvedConfig, error) {
	r.repo = repository
	return r.result, r.config, r.err
}

func (r *stubRunner) RunIssueView(_ context.Context, repository string, number int) (CommandResult, resolvedConfig, error) {
	r.repo = repository
	r.number = number
	return r.result, r.config, r.err
}

func (r *stubRunner) RunPRCreate(_ context.Context, repository string, request PRCreateRequest) (CommandResult, resolvedConfig, error) {
	r.repo = repository
	r.base = request.Base
	r.head = request.Head
	r.title = request.Title
	r.body = request.Body
	r.draft = request.Draft
	return r.result, r.config, r.err
}
