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
