package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/spf13/cobra"
)

func TestIntegrationDispatcherCommandPrintsDiagnosticRoute(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "dispatcher", "--system", "github", "--resource", "issue", "--operation", "get"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute dispatcher command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"provider=github\n",
		"provider-available=true\n",
		"resource=issue\n",
		"operation=get\n",
		"expected-result=tracker-issue\n",
		"diagnostic=request system=github resource=issue operation=get\n",
		"diagnostic=dispatcher mode=diagnostic-only\n",
		"diagnostic=provider=github registered\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("dispatcher output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubAuthStatusCommandPrintsNormalizedSuccessResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "auth", "status"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			AuthStatus: &integration.AuthStatus{
				System:        "github",
				State:         "ready",
				Available:     true,
				Authenticated: true,
				Command:       "gh",
				Path:          "/usr/local/bin/gh",
				ExitCode:      0,
				Message:       "GitHub CLI is installed and authentication is available",
				Diagnostics:   []string{"gh auth status completed successfully"},
				Stdout:        "Logged in to github.com account test-user",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github auth status command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=auth\n",
		"operation=status\n",
		"state=ready\n",
		"available=true\n",
		"authenticated=true\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=0\n",
		"message=GitHub CLI is installed and authentication is available\n",
		"diagnostic=gh auth status completed successfully\n",
		"stdout=Logged in to github.com account test-user\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github auth status output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubAuthStatusCommandReturnsNormalizedError(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "auth", "status"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			AuthStatus: &integration.AuthStatus{
				System:        "github",
				State:         "auth-required",
				Available:     true,
				Authenticated: false,
				Command:       "gh",
				Path:          "/usr/local/bin/gh",
				ExitCode:      1,
				Message:       "GitHub authentication is required",
				Diagnostics:   []string{"gh auth status reported that no GitHub login is configured"},
				Stderr:        "You are not logged into any GitHub hosts. Run gh auth login.",
			},
		},
		err: assertErr("GitHub authentication is required"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github auth status error")
	}
	if err.Error() != "GitHub authentication is required" {
		t.Fatalf("unexpected github auth status error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"state=auth-required\n",
		"authenticated=false\n",
		"exit-code=1\n",
		"message=GitHub authentication is required\n",
		"stderr=You are not logged into any GitHub hosts. Run gh auth login.\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github auth status output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubRepoGetCommandPrintsNormalizedRepository(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get", "--repo", "rasungatullin/progress"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			RepositoryRef: &integration.TrackerRepository{
				System:        "github",
				FullName:      "rasungatullin/progress",
				Owner:         "rasungatullin",
				Name:          "progress",
				Description:   "Repository description",
				DefaultBranch: "main",
				URL:           "https://github.com/rasungatullin/progress",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github repo get command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=repo\n",
		"operation=get\n",
		"full_name=rasungatullin/progress\n",
		"owner=rasungatullin\n",
		"name=progress\n",
		"description=Repository description\n",
		"default_branch=main\n",
		"url=https://github.com/rasungatullin/progress\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github repo get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubRepoGetCommandRequiresRepoFlag(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github repo get error")
	}
	if err.Error() != "--repo is required" {
		t.Fatalf("unexpected github repo get error: %v", err)
	}
}

func TestIntegrationGitHubRepoGetCommandPrintsNormalizedErrorResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get", "--repo", "owner/name"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			RepositoryStatus: &integration.RepositoryStatus{
				System:      "github",
				Repository:  "owner/name",
				State:       "auth-required",
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    1,
				Message:     "GitHub authentication is required",
				Diagnostics: []string{"repository=owner/name", "gh repo view reported that no GitHub login is configured"},
				Stderr:      "You are not logged into any GitHub hosts. Run gh auth login.",
			},
		},
		err: assertErr("GitHub authentication is required"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github repo get error")
	}
	if err.Error() != "GitHub authentication is required" {
		t.Fatalf("unexpected github repo get error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=repo\n",
		"operation=get\n",
		"repository=owner/name\n",
		"state=auth-required\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=1\n",
		"message=GitHub authentication is required\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=gh repo view reported that no GitHub login is configured\n",
		"stderr=You are not logged into any GitHub hosts. Run gh auth login.\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github repo get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubRepoGetCommandPrintsNormalizedNotInstalledResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get", "--repo", "owner/name"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			RepositoryStatus: &integration.RepositoryStatus{
				System:      "github",
				Repository:  "owner/name",
				State:       "not-installed",
				Command:     "gh",
				ExitCode:    -1,
				Message:     "GitHub CLI not found: gh",
				Diagnostics: []string{"repository=owner/name", "gh repo view failed before returning a repository payload"},
			},
		},
		err: assertErr("GitHub CLI not found: gh"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github repo get error")
	}
	if err.Error() != "GitHub CLI not found: gh" {
		t.Fatalf("unexpected github repo get error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=repo\n",
		"operation=get\n",
		"repository=owner/name\n",
		"state=not-installed\n",
		"command=gh\n",
		"path=\n",
		"exit-code=-1\n",
		"message=GitHub CLI not found: gh\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=gh repo view failed before returning a repository payload\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github repo get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubRepoGetCommandPrintsNormalizedInvalidRepositoryResult(t *testing.T) {
	for _, repository := range []string{"owner", "owner/", "owner/name/extra"} {
		repository := repository
		t.Run(repository, func(t *testing.T) {
			cmd := NewRootCommand()
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs([]string{"integration", "github", "repo", "get", "--repo", repository})

			service := newIntegrationService(cmd)
			service.RegisterProvider("github", validatingCLIProvider{})

			original := integrationServiceFactory
			integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
			t.Cleanup(func() { integrationServiceFactory = original })

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected github repo get error")
			}
			if err.Error() != "GitHub repository must use owner/name format" {
				t.Fatalf("unexpected github repo get error: %v", err)
			}

			output := stdout.String()
			for _, fragment := range []string{
				"system=github\n",
				"resource=repo\n",
				"operation=get\n",
				"repository=" + repository + "\n",
				"state=invalid-request\n",
				"command=gh\n",
				"path=\n",
				"exit-code=-1\n",
				"message=GitHub repository must use owner/name format\n",
				"diagnostic=repository request rejected before invoking gh\n",
			} {
				if !strings.Contains(output, fragment) {
					t.Fatalf("github repo get output must include %q, got %q", fragment, output)
				}
			}
		})
	}
}

func TestIntegrationGitHubIssueGetCommandPrintsNormalizedIssue(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Title:      "Fix integration",
				Body:       "Line one\nLine two",
				State:      "OPEN",
				Labels:     []string{"bug", "integration"},
				Assignees: []integration.TrackerUser{{
					System: "github",
					Login:  "alice",
					Name:   "Alice",
					URL:    "https://github.com/alice",
				}},
				Author:    integration.TrackerUser{System: "github", Login: "bob", Name: "Bob", URL: "https://github.com/bob"},
				URL:       "https://github.com/owner/name/issues/123",
				CreatedAt: "2026-05-01T10:00:00Z",
				UpdatedAt: "2026-05-02T10:00:00Z",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue get command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=issue\n",
		"operation=get\n",
		"repository=owner/name\n",
		"number=123\n",
		"title=Fix integration\n",
		"state=OPEN\n",
		"author_login=bob\n",
		"author_name=Bob\n",
		"author_url=https://github.com/bob\n",
		"label=bug\n",
		"label=integration\n",
		"assignee_login=alice\n",
		"assignee_name=Alice\n",
		"assignee_url=https://github.com/alice\n",
		"url=https://github.com/owner/name/issues/123\n",
		"created_at=2026-05-01T10:00:00Z\n",
		"updated_at=2026-05-02T10:00:00Z\n",
		"body=Line one\n",
		"body=Line two\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github issue get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubIssueGetCommandRequiresFlags(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--repo", "owner/name"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github issue get error")
	}
	if err.Error() != "--number is required" {
		t.Fatalf("unexpected github issue get error: %v", err)
	}
}

func TestIntegrationGitHubIssueGetCommandPrintsNormalizedErrorResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			IssueStatus: &integration.IssueStatus{
				System:      "github",
				Repository:  "owner/name",
				Number:      123,
				State:       "auth-required",
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    1,
				Message:     "GitHub authentication is required",
				Diagnostics: []string{"repository=owner/name", "number=123", "gh issue view reported that no GitHub login is configured"},
				Stderr:      "You are not logged into any GitHub hosts. Run gh auth login.",
			},
		},
		err: assertErr("GitHub authentication is required"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github issue get error")
	}
	if err.Error() != "GitHub authentication is required" {
		t.Fatalf("unexpected github issue get error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=issue\n",
		"operation=get\n",
		"repository=owner/name\n",
		"number=123\n",
		"state=auth-required\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=1\n",
		"message=GitHub authentication is required\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=number=123\n",
		"diagnostic=gh issue view reported that no GitHub login is configured\n",
		"stderr=You are not logged into any GitHub hosts. Run gh auth login.\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github issue get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubPRCreateCommandPrintsNormalizedSuccessResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "create", "--repo", "owner/name", "--base", "main", "--head", "feature/branch", "--title", "Add integration", "--body", "Implements pr create", "--draft"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			PullRequestStatus: &integration.PullRequestStatus{
				System:     "github",
				Repository: "owner/name",
				Base:       "main",
				Head:       "feature/branch",
				Title:      "Add integration",
				Draft:      true,
				Number:     42,
				State:      "OPEN",
				URL:        "https://github.com/owner/name/pull/42",
				Command:    "gh",
				Path:       "/usr/local/bin/gh",
				ExitCode:   0,
				Message:    "GitHub pull request created for owner/name feature/branch -> main",
				Diagnostics: []string{
					"repository=owner/name",
					"gh pr create completed successfully",
				},
				Stdout: "https://github.com/owner/name/pull/42",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr create command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=pr\n",
		"operation=create\n",
		"repository=owner/name\n",
		"base=main\n",
		"head=feature/branch\n",
		"title=Add integration\n",
		"draft=true\n",
		"state=OPEN\n",
		"number=42\n",
		"url=https://github.com/owner/name/pull/42\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=0\n",
		"message=GitHub pull request created for owner/name feature/branch -> main\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=gh pr create completed successfully\n",
		"stdout=https://github.com/owner/name/pull/42\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github pr create output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubPRCreateCommandRejectsUnsafeTitle(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "create", "--repo", "owner/name", "--base", "main", "--head", "feature", "--title", "bad\ntitle", "--body", "Body"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github pr create error")
	}
	if err.Error() != "--title must not contain control characters or line breaks" {
		t.Fatalf("unexpected github pr create error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got %q", stdout.String())
	}
}

func TestIntegrationGitHubPRCreateCommandRequiresFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "repo", args: []string{"integration", "github", "pr", "create"}, want: "--repo is required"},
		{name: "base", args: []string{"integration", "github", "pr", "create", "--repo", "owner/name"}, want: "--base is required"},
		{name: "head", args: []string{"integration", "github", "pr", "create", "--repo", "owner/name", "--base", "main"}, want: "--head is required"},
		{name: "title", args: []string{"integration", "github", "pr", "create", "--repo", "owner/name", "--base", "main", "--head", "feature"}, want: "--title is required"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCommand()
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected github pr create error")
			}
			if err.Error() != tt.want {
				t.Fatalf("unexpected github pr create error: %v", err)
			}
		})
	}
}

func TestIntegrationGitHubPRCreateCommandAllowsEmptyBody(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "create", "--repo", "owner/name", "--base", "main", "--head", "feature", "--title", "Title"})

	provider := &capturingCLIProvider{response: integration.Response{PullRequestStatus: &integration.PullRequestStatus{System: "github", Repository: "owner/name", Base: "main", Head: "feature", Title: "Title", State: "OPEN", Number: 42, URL: "https://github.com/owner/name/pull/42", Command: "gh", ExitCode: 0, Message: "GitHub pull request created for owner/name feature -> main"}}}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr create command: %v", err)
	}
	if provider.request.Body != "" {
		t.Fatalf("expected empty body request, got %q", provider.request.Body)
	}
}

func TestIntegrationGitHubPRCreateCommandPrintsNormalizedErrorResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "create", "--repo", "owner/name", "--base", "main", "--head", "feature", "--title", "Title", "--body", "Body"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			PullRequestStatus: &integration.PullRequestStatus{
				System:     "github",
				Repository: "owner/name",
				Base:       "main",
				Head:       "feature",
				Title:      "Title",
				Draft:      false,
				State:      "already-exists",
				Number:     15,
				URL:        "https://github.com/owner/name/pull/15",
				Command:    "gh",
				Path:       "/usr/local/bin/gh",
				ExitCode:   1,
				Message:    "GitHub pull request already exists for owner/name feature -> main",
				Diagnostics: []string{
					"repository=owner/name",
					"gh pr create reported an existing pull request between the requested branches",
				},
				Stderr: "a pull request for branch \"feature\" into branch \"main\" already exists",
			},
		},
		err: assertErr("GitHub pull request already exists for owner/name feature -> main"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github pr create error")
	}
	if err.Error() != "GitHub pull request already exists for owner/name feature -> main" {
		t.Fatalf("unexpected github pr create error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=pr\n",
		"operation=create\n",
		"repository=owner/name\n",
		"base=main\n",
		"head=feature\n",
		"title=Title\n",
		"draft=false\n",
		"state=already-exists\n",
		"number=15\n",
		"url=https://github.com/owner/name/pull/15\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=1\n",
		"message=GitHub pull request already exists for owner/name feature -> main\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=gh pr create reported an existing pull request between the requested branches\n",
		"stderr=a pull request for branch \"feature\" into branch \"main\" already exists\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github pr create output must include %q, got %q", fragment, output)
		}
	}
}

type stubCLIProvider struct {
	response integration.Response
	err      error
}

func (p stubCLIProvider) Execute(_ context.Context, _ integration.ProviderRequest) (integration.Response, error) {
	return p.response, p.err
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}

type validatingCLIProvider struct{}

func (validatingCLIProvider) Execute(_ context.Context, req integration.ProviderRequest) (integration.Response, error) {
	repository := strings.TrimSpace(req.Repository)
	parts := strings.Split(repository, "/")
	valid := len(parts) == 2 && parts[0] != "" && parts[1] != ""
	if valid {
		for _, part := range parts {
			if strings.TrimSpace(part) != part || strings.ContainsAny(part, " \t\r\n") {
				valid = false
				break
			}
		}
	}
	if valid {
		return integration.Response{RepositoryRef: &integration.TrackerRepository{System: "github", FullName: repository}}, nil
	}

	return integration.Response{
		RepositoryStatus: &integration.RepositoryStatus{
			System:     "github",
			Repository: repository,
			State:      "invalid-request",
			Command:    "gh",
			ExitCode:   -1,
			Message:    "GitHub repository must use owner/name format",
			Diagnostics: []string{
				"repository=" + repository,
				"command=gh repo view " + repository + " --json name,owner,description,defaultBranchRef,url",
				"repository request rejected before invoking gh",
			},
		},
	}, assertErr("GitHub repository must use owner/name format")
}

type capturingCLIProvider struct {
	request  integration.ProviderRequest
	response integration.Response
	err      error
}

func (p *capturingCLIProvider) Execute(_ context.Context, req integration.ProviderRequest) (integration.Response, error) {
	p.request = req
	return p.response, p.err
}

func TestIntegrationDispatcherCommandPrintsDiagnosticsOnInvalidRequest(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "dispatcher", "--system", "", "--resource", "issue", "--operation", "get"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected dispatcher error")
	}
	if err.Error() != "invalid integration request: system is required" {
		t.Fatalf("unexpected dispatcher error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=\n",
		"provider=\n",
		"provider-available=false\n",
		"resource=issue\n",
		"operation=get\n",
		"expected-result=tracker-issue\n",
		"diagnostic=request system= resource=issue operation=get\n",
		"diagnostic=dispatcher mode=diagnostic-only\n",
		"diagnostic=invalid-request missing system\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("dispatcher output must include %q, got %q", fragment, output)
		}
	}
}
