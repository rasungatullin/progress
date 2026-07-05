package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/integration/secrets"
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

func TestIntegrationDispatcherCommandPrintsJSONRoute(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "dispatcher", "--format", "json", "--system", "github", "--resource", "issue", "--operation", "get"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute dispatcher command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Route
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("dispatcher json parse: %v, output: %q", err, output)
	}
	if payload.System != "github" {
		t.Fatalf("expected json route system github, got %q", payload.System)
	}
	if payload.Provider != "github" {
		t.Fatalf("expected json route provider github, got %q", payload.Provider)
	}
	if payload.Resource != "issue" {
		t.Fatalf("expected json route resource issue, got %q", payload.Resource)
	}
	if payload.Operation != "get" {
		t.Fatalf("expected json route operation get, got %q", payload.Operation)
	}
	if payload.ExpectedResult == "" {
		t.Fatal("expected non-empty json route expected-result")
	}
}

func TestIntegrationDispatcherCommandPrintsJSONRouteOnInvalidRequest(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "dispatcher", "--format", "json", "--system", "", "--resource", "issue", "--operation", "get"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected dispatcher error")
	}
	if err.Error() != "invalid integration request: system is required" {
		t.Fatalf("unexpected dispatcher error: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Route
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("dispatcher invalid request json parse: %v, output: %q", err, output)
	}
	if payload.System != "" {
		t.Fatalf("expected empty system in json payload, got %q", payload.System)
	}
	if payload.Resource != "issue" {
		t.Fatalf("expected json route resource issue, got %q", payload.Resource)
	}
	if payload.ProviderAvailable {
		t.Fatal("expected provider unavailable for missing system")
	}
}

func TestIntegrationCommandRejectsUnknownFormat(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "dispatcher", "--format", "xml", "--system", "github", "--resource", "issue", "--operation", "get"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected integration format error")
	}
	if err.Error() != "--format supports only text or json" {
		t.Fatalf("unexpected integration format error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got %q", stdout.String())
	}
}

func TestIntegrationOperationsCommandPrintsJSONCatalog(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "operations", "--format", "json", "--system", "github", "--name", "tracker.task.get"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute operations command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload []integration.OperationDescriptor
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("operations json parse: %v, output: %q", err, output)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one operation, got %#v", payload)
	}
	if payload[0].Name != "tracker.task.get" || payload[0].System != "github" {
		t.Fatalf("unexpected operation descriptor: %#v", payload[0])
	}
	if !payload[0].Available {
		t.Fatalf("expected github operation to be available: %#v", payload[0])
	}
}

func TestIntegrationPrivateSetCommandStoresValueWithoutPrintingIt(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "private", "set", "mt_auth_token", "--value", "secret-token"})

	store := &capturingPrivateStore{values: map[string]string{}}
	original := integrationPrivateStoreFactory
	integrationPrivateStoreFactory = func(*cobra.Command) (secrets.Store, secrets.Descriptor, error) {
		return store, secrets.Descriptor{Type: "file", Location: "/tmp/progress-private.json"}, nil
	}
	t.Cleanup(func() { integrationPrivateStoreFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute private set command: %v", err)
	}
	if store.values["mt_auth_token"] != "secret-token" {
		t.Fatalf("unexpected stored private value: %q", store.values["mt_auth_token"])
	}

	output := stdout.String()
	for _, fragment := range []string{
		"status=stored\n",
		"name=mt_auth_token\n",
		"store=file\n",
		"location=/tmp/progress-private.json\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("private set output must include %q, got %q", fragment, output)
		}
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("private set output must not include stored value, got %q", output)
	}
}

func TestIntegrationPrivateSetCommandReadsValueFromStdin(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetIn(strings.NewReader("stdin-token\n"))
	cmd.SetArgs([]string{"integration", "private", "set", "mt_auth_token", "--stdin", "--format", "json"})

	store := &capturingPrivateStore{values: map[string]string{}}
	original := integrationPrivateStoreFactory
	integrationPrivateStoreFactory = func(*cobra.Command) (secrets.Store, secrets.Descriptor, error) {
		return store, secrets.Descriptor{Type: "keychain", Location: "progress"}, nil
	}
	t.Cleanup(func() { integrationPrivateStoreFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute private set command with stdin: %v", err)
	}
	if store.values["mt_auth_token"] != "stdin-token" {
		t.Fatalf("unexpected stored private value from stdin: %q", store.values["mt_auth_token"])
	}

	output := strings.TrimSpace(stdout.String())
	var payload integrationPrivateResult
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("private set json parse: %v, output: %q", err, output)
	}
	if payload.Status != "stored" || payload.Name != "mt_auth_token" || payload.Store != "keychain" {
		t.Fatalf("unexpected private set json payload: %#v", payload)
	}
	if strings.Contains(output, "stdin-token") {
		t.Fatalf("private set json output must not include stored value, got %q", output)
	}
}

func TestIntegrationGitHubAuthStatusCommandPrintsJSONResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "auth", "status", "--format", "json"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			AuthStatus: &integration.AuthStatus{
				System:      "github",
				State:       "ready",
				Available:   true,
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    0,
				Message:     "ok",
				Stdout:      "Logged in\n",
				Diagnostics: []string{"ok"},
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github auth status command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("auth status json parse: %v, output: %q", err, output)
	}
	if payload.AuthStatus == nil {
		t.Fatal("expected auth status in json response")
	}
	if payload.AuthStatus.State != "ready" {
		t.Fatalf("expected auth status state ready, got %q", payload.AuthStatus.State)
	}
	if payload.AuthStatus.Command != "gh" {
		t.Fatalf("expected auth status command gh, got %q", payload.AuthStatus.Command)
	}
}

func TestIntegrationGitHubAuthStatusCommandPrintsJSONResultOnError(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "auth", "status", "--format", "json"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			AuthStatus: &integration.AuthStatus{
				System:      "github",
				State:       "auth-required",
				Available:   true,
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    1,
				Message:     "GitHub authentication is required",
				Stderr:      "not logged in",
				Diagnostics: []string{"diagnostic"},
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

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("auth status error json parse: %v, output: %q", err, output)
	}
	if payload.AuthStatus == nil {
		t.Fatal("expected auth status in json error response")
	}
	if payload.AuthStatus.State != "auth-required" {
		t.Fatalf("expected auth status state auth-required, got %q", payload.AuthStatus.State)
	}
	if payload.AuthStatus.ExitCode != 1 {
		t.Fatalf("expected auth status exit-code 1, got %d", payload.AuthStatus.ExitCode)
	}
}

func TestIntegrationGitHubRepoGetCommandPrintsJSONResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get", "--format", "json", "--repo", "owner/name"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			RepositoryRef: &integration.TrackerRepository{
				System:        "github",
				FullName:      "owner/name",
				Owner:         "owner",
				Name:          "name",
				Description:   "desc",
				DefaultBranch: "main",
				URL:           "https://github.com/owner/name",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github repo get command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("repo get json parse: %v, output: %q", err, output)
	}
	if payload.RepositoryRef == nil {
		t.Fatal("expected repository in json response")
	}
	if payload.RepositoryRef.FullName != "owner/name" {
		t.Fatalf("unexpected repository full name: %q", payload.RepositoryRef.FullName)
	}
}

func TestIntegrationGitHubRepoGetCommandPrintsJSONNotFoundResultOnError(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get", "--format", "json", "--repo", "missing/repo"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			RepositoryStatus: &integration.RepositoryStatus{
				System:      "github",
				Repository:  "missing/repo",
				State:       "not-found",
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    1,
				Message:     "GitHub repository not found: missing/repo",
				Diagnostics: []string{"repository=missing/repo", "gh repo view could not resolve the requested repository"},
				Stderr:      "GraphQL: Could not resolve to a Repository",
			},
		},
		err: assertErr("GitHub repository not found: missing/repo"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github repo get error")
	}
	if err.Error() != "GitHub repository not found: missing/repo" {
		t.Fatalf("unexpected github repo get error: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("repo get not-found json parse: %v, output: %q", err, output)
	}
	if payload.RepositoryStatus == nil {
		t.Fatal("expected repository status in json error response")
	}
	if payload.RepositoryStatus.State != "not-found" {
		t.Fatalf("unexpected repository status state: %q", payload.RepositoryStatus.State)
	}
	if payload.RepositoryStatus.ExitCode != 1 {
		t.Fatalf("unexpected repository status exit-code: %d", payload.RepositoryStatus.ExitCode)
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

func TestIntegrationGitHubAuthStatusCommandPreservesGenericMultilineFields(t *testing.T) {
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
				Message:       "ok",
				Stdout:        "line one\nline two",
				Stderr:        "err one\nerr two",
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
	if !strings.Contains(output, "stdout=line one\nline two\n") {
		t.Fatalf("expected auth stdout to remain a single multiline field, got %q", output)
	}
	if strings.Contains(output, "stdout=line two\n") {
		t.Fatalf("unexpected split auth stdout field: %q", output)
	}
	if !strings.Contains(output, "stderr=err one\nerr two\n") {
		t.Fatalf("expected auth stderr to remain a single multiline field, got %q", output)
	}
	if strings.Contains(output, "stderr=err two\n") {
		t.Fatalf("unexpected split auth stderr field: %q", output)
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

func TestIntegrationGitHubRepoGetCommandPreservesDescriptionMultilineField(t *testing.T) {
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
				System:      "github",
				FullName:    "rasungatullin/progress",
				Owner:       "rasungatullin",
				Name:        "progress",
				Description: "line one\nline two",
				URL:         "https://github.com/rasungatullin/progress",
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
	if !strings.Contains(output, "description=line one\nline two\n") {
		t.Fatalf("expected description to remain a single multiline field, got %q", output)
	}
	if strings.Contains(output, "description=line two\n") {
		t.Fatalf("unexpected split description field: %q", output)
	}
}

func TestIntegrationGitHubRepoGetCommandAllowsOmittedRepoFlag(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get"})

	provider := &capturingCLIProvider{response: integration.Response{RepositoryRef: &integration.TrackerRepository{System: "github", FullName: "owner/name", Owner: "owner", Name: "name", URL: "https://github.com/owner/name"}}}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github repo get command without repo: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected empty repository request so provider layer can resolve fallback, got %q", provider.request.Repository)
	}
	if provider.request.RepoProvided {
		t.Fatal("expected omitted repo flag to stay false")
	}
}

func TestIntegrationGitHubRepoGetCommandPassesExplicitEmptyRepoToProvider(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "repo", "get", "--repo="})

	provider := &capturingCLIProvider{response: integration.Response{RepositoryStatus: &integration.RepositoryStatus{System: "github", State: "invalid-request", Command: "gh", ExitCode: -1, Message: "GitHub repository is required"}}, err: assertErr("GitHub repository is required")}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github repo get error")
	}
	if err.Error() != "GitHub repository is required" {
		t.Fatalf("unexpected github repo get error: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected explicit empty repository request, got %q", provider.request.Repository)
	}
	if !provider.request.RepoProvided {
		t.Fatal("expected explicit repo flag to stay true")
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
				Body:       "Line one\n\n  indented line\nLine two",
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
		"body=\n",
		"body=  indented line\n",
		"body=Line two\n",
		"body_raw=\"Line one\\n\\n  indented line\\nLine two\"\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github issue get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubIssueGetCommandPrintsJSONResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--format", "json", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Title:      "Fix integration",
				Body:       "body",
				State:      "OPEN",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue get command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("issue get json parse: %v, output: %q", err, output)
	}
	if payload.Issue == nil {
		t.Fatal("expected issue in json response")
	}
	if payload.Issue.Number != 123 {
		t.Fatalf("unexpected issue number: %d", payload.Issue.Number)
	}
	if payload.Issue.State != "OPEN" {
		t.Fatalf("unexpected issue state: %q", payload.Issue.State)
	}
}

func TestIntegrationGitHubIssueGetCommandPrintsJSONMalformedResponseResultOnError(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--format", "json", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			IssueStatus: &integration.IssueStatus{
				System:      "github",
				Repository:  "owner/name",
				Number:      123,
				State:       "external-failure",
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    0,
				Message:     "unexpected GitHub CLI JSON response: invalid character",
				Diagnostics: []string{"gh issue view returned malformed JSON"},
				Stdout:      "{not-json",
			},
		},
		err: assertErr("unexpected GitHub CLI JSON response: invalid character"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github issue get error")
	}
	if err.Error() != "unexpected GitHub CLI JSON response: invalid character" {
		t.Fatalf("unexpected github issue get error: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("issue get malformed response json parse: %v, output: %q", err, output)
	}
	if payload.IssueStatus == nil {
		t.Fatal("expected issue status in json error response")
	}
	if payload.IssueStatus.State != "external-failure" {
		t.Fatalf("unexpected issue status state: %q", payload.IssueStatus.State)
	}
	if payload.IssueStatus.Stdout != "{not-json" {
		t.Fatalf("unexpected issue status stdout: %q", payload.IssueStatus.Stdout)
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

func TestIntegrationGitHubIssueGetCommandAllowsOmittedRepoFlag(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--number", "123"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Title:      "Title",
			},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue get command without repo: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected empty repository request so provider layer can resolve fallback, got %q", provider.request.Repository)
	}
	if provider.request.RepoProvided {
		t.Fatal("expected omitted repo flag to stay false")
	}
}

func TestIntegrationGitHubIssueGetCommandPassesExplicitEmptyRepoToProvider(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--repo=", "--number", "123"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			IssueStatus: &integration.IssueStatus{
				System:   "github",
				State:    "invalid-request",
				Command:  "gh",
				ExitCode: -1,
				Message:  "GitHub repository is required",
			},
		},
		err: assertErr("GitHub repository is required"),
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github issue get error")
	}
	if err.Error() != "GitHub repository is required" {
		t.Fatalf("unexpected github issue get error: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected explicit empty repository request, got %q", provider.request.Repository)
	}
	if !provider.request.RepoProvided {
		t.Fatal("expected explicit repo flag to stay true")
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

func TestIntegrationGitHubIssueCommentsCommandPrintsNormalizedComments(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			System:    "github",
			Resource:  "issue",
			Operation: "comments",
			Comments: []integration.TrackerComment{{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Author:     integration.TrackerUser{System: "github", Login: "alice", Name: "Alice", URL: "https://github.com/alice"},
				Body:       "First line\nSecond line",
				URL:        "https://github.com/owner/name/issues/123#issuecomment-1",
				CreatedAt:  "2026-05-01T10:00:00Z",
				UpdatedAt:  "2026-05-02T10:00:00Z",
			}},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue comments command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=issue\n",
		"operation=comments\n",
		"repository=owner/name\n",
		"number=123\n",
		"comment_count=1\n",
		"comment_author_login=alice\n",
		"comment_author_name=Alice\n",
		"comment_author_url=https://github.com/alice\n",
		"comment_url=https://github.com/owner/name/issues/123#issuecomment-1\n",
		"comment_created_at=2026-05-01T10:00:00Z\n",
		"comment_updated_at=2026-05-02T10:00:00Z\n",
		"comment_body=First line\n",
		"comment_body=Second line\n",
		"comment_body_raw=\"First line\\nSecond line\"\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github issue comments output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubIssueCommentsCommandPrintsEmptyResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			System:    "github",
			Resource:  "issue",
			Operation: "comments",
			Comments:  []integration.TrackerComment{},
			Metadata: map[string]string{
				"repository": "owner/name",
				"number":     "123",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue comments command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=issue\n",
		"operation=comments\n",
		"repository=owner/name\n",
		"number=123\n",
		"comment_count=0\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github issue comments output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubIssueCommentsCommandPrintsJSONResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--format", "json", "--repo", "owner/name", "--number", "123"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			Comments: []integration.TrackerComment{{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Author:     integration.TrackerUser{System: "github", Login: "alice"},
				Body:       "body",
			}},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue comments command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("issue comments json parse: %v, output: %q", err, output)
	}
	if len(payload.Comments) != 1 {
		t.Fatalf("expected one comment in json response, got %#v", payload.Comments)
	}
	if payload.Comments[0].Author.Login != "alice" {
		t.Fatalf("unexpected comment author: %#v", payload.Comments[0].Author)
	}
}

func TestIntegrationGitHubIssueCommentsCommandRequiresFlags(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--repo", "owner/name"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github issue comments error")
	}
	if err.Error() != "--number is required" {
		t.Fatalf("unexpected github issue comments error: %v", err)
	}
}

func TestIntegrationGitHubIssueCommentsCommandAllowsOmittedRepoFlag(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--number", "123"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			System:    "github",
			Resource:  "issue",
			Operation: "comments",
			Comments:  []integration.TrackerComment{},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue comments command without repo: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected empty repository request so provider layer can resolve fallback, got %q", provider.request.Repository)
	}
	if provider.request.RepoProvided {
		t.Fatal("expected omitted repo flag to stay false")
	}
	if provider.request.Operation != "comments" {
		t.Fatalf("unexpected operation: %q", provider.request.Operation)
	}
}

func TestIntegrationGitHubIssueCommentsCommandPassesExplicitEmptyRepoToProvider(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--repo=", "--number", "123"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			IssueStatus: &integration.IssueStatus{
				System:   "github",
				State:    "invalid-request",
				Command:  "gh",
				ExitCode: -1,
				Message:  "GitHub repository is required",
			},
		},
		err: assertErr("GitHub repository is required"),
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github issue comments error")
	}
	if err.Error() != "GitHub repository is required" {
		t.Fatalf("unexpected github issue comments error: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected explicit empty repository request, got %q", provider.request.Repository)
	}
	if !provider.request.RepoProvided {
		t.Fatal("expected explicit repo flag to stay true")
	}
}

func TestIntegrationGitHubIssueCommentsCommandPrintsNormalizedErrorResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "comments", "--repo", "owner/name", "--number", "123"})

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
				Diagnostics: []string{"repository=owner/name", "number=123", "gh issue comments reported that no GitHub login is configured"},
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
		t.Fatal("expected github issue comments error")
	}
	if err.Error() != "GitHub authentication is required" {
		t.Fatalf("unexpected github issue comments error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=issue\n",
		"operation=comments\n",
		"repository=owner/name\n",
		"number=123\n",
		"state=auth-required\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=1\n",
		"message=GitHub authentication is required\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=number=123\n",
		"diagnostic=gh issue comments reported that no GitHub login is configured\n",
		"stderr=You are not logged into any GitHub hosts. Run gh auth login.\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github issue comments output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubPRGetCommandPrintsNormalizedPullRequest(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "get", "--repo", "owner/name", "--number", "42"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			PullRequest: &integration.TrackerPullRequest{
				System:         "github",
				Repository:     "owner/name",
				Number:         42,
				Title:          "Add integration",
				Body:           "Line one\nLine two",
				State:          "OPEN",
				Author:         integration.TrackerUser{System: "github", Login: "bob", Name: "Bob", URL: "https://github.com/bob"},
				ReviewDecision: "APPROVED",
				BaseRef:        "main",
				HeadRef:        "feature/pr-get",
				Labels:         []string{"integration"},
				URL:            "https://github.com/owner/name/pull/42",
				CreatedAt:      "2026-05-01T10:00:00Z",
				UpdatedAt:      "2026-05-02T10:00:00Z",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr get command: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=pr\n",
		"operation=get\n",
		"repository=owner/name\n",
		"number=42\n",
		"title=Add integration\n",
		"state=OPEN\n",
		"author_login=bob\n",
		"author_name=Bob\n",
		"author_url=https://github.com/bob\n",
		"review_decision=APPROVED\n",
		"base_ref=main\n",
		"head_ref=feature/pr-get\n",
		"label=integration\n",
		"url=https://github.com/owner/name/pull/42\n",
		"created_at=2026-05-01T10:00:00Z\n",
		"updated_at=2026-05-02T10:00:00Z\n",
		"body=Line one\n",
		"body=Line two\n",
		"body_raw=\"Line one\\nLine two\"\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github pr get output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubPRGetCommandPrintsJSONResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "get", "--format", "json", "--repo", "owner/name", "--number", "42"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			PullRequest: &integration.TrackerPullRequest{
				System:     "github",
				Repository: "owner/name",
				Number:     42,
				Title:      "Add integration",
				State:      "OPEN",
			},
		},
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr get command: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("pr get json parse: %v, output: %q", err, output)
	}
	if payload.PullRequest == nil {
		t.Fatal("expected pull request in json response")
	}
	if payload.PullRequest.Number != 42 {
		t.Fatalf("unexpected pull request number: %d", payload.PullRequest.Number)
	}
}

func TestIntegrationGitHubPRGetCommandRequiresFlags(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "get", "--repo", "owner/name"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github pr get error")
	}
	if err.Error() != "--number is required" {
		t.Fatalf("unexpected github pr get error: %v", err)
	}
}

func TestIntegrationGitHubPRGetCommandAllowsOmittedRepoFlag(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "get", "--number", "42"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			PullRequest: &integration.TrackerPullRequest{
				System:     "github",
				Repository: "owner/name",
				Number:     42,
				Title:      "Title",
			},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr get command without repo: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected empty repository request so provider layer can resolve fallback, got %q", provider.request.Repository)
	}
	if provider.request.RepoProvided {
		t.Fatal("expected omitted repo flag to stay false")
	}
}

func TestIntegrationGitHubPRGetCommandPassesExplicitEmptyRepoToProvider(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "get", "--repo=", "--number", "42"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			PullRequestStatus: &integration.PullRequestStatus{
				System:   "github",
				State:    "invalid-request",
				Command:  "gh",
				ExitCode: -1,
				Message:  "GitHub repository is required",
			},
		},
		err: assertErr("GitHub repository is required"),
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github pr get error")
	}
	if err.Error() != "GitHub repository is required" {
		t.Fatalf("unexpected github pr get error: %v", err)
	}
	if provider.request.Repository != "" {
		t.Fatalf("expected explicit empty repository request, got %q", provider.request.Repository)
	}
	if !provider.request.RepoProvided {
		t.Fatal("expected explicit repo flag to stay true")
	}
}

func TestIntegrationGitHubPRGetCommandPrintsNormalizedErrorResult(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "get", "--repo", "owner/name", "--number", "42"})

	service := newIntegrationService(cmd)
	service.RegisterProvider("github", stubCLIProvider{
		response: integration.Response{
			PullRequestStatus: &integration.PullRequestStatus{
				System:      "github",
				Repository:  "owner/name",
				Number:      42,
				State:       "auth-required",
				Command:     "gh",
				Path:        "/usr/local/bin/gh",
				ExitCode:    1,
				Message:     "GitHub authentication is required",
				Diagnostics: []string{"repository=owner/name", "number=42", "gh pr view reported that no GitHub login is configured"},
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
		t.Fatal("expected github pr get error")
	}
	if err.Error() != "GitHub authentication is required" {
		t.Fatalf("unexpected github pr get error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"system=github\n",
		"resource=pr\n",
		"operation=get\n",
		"repository=owner/name\n",
		"number=42\n",
		"state=auth-required\n",
		"command=gh\n",
		"path=/usr/local/bin/gh\n",
		"exit-code=1\n",
		"message=GitHub authentication is required\n",
		"diagnostic=repository=owner/name\n",
		"diagnostic=number=42\n",
		"diagnostic=gh pr view reported that no GitHub login is configured\n",
		"stderr=You are not logged into any GitHub hosts. Run gh auth login.\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github pr get output must include %q, got %q", fragment, output)
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

func TestIntegrationGitHubPRCreateCommandPrintsJSONResultOnError(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "create", "--format", "json", "--repo", "owner/name", "--base", "main", "--head", "feature", "--title", "Title", "--body", "Body"})

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
				Message:    "GitHub pull request already exists",
				Diagnostics: []string{
					"repository=owner/name",
				},
				Stderr: "pull request already exists",
			},
		},
		err: assertErr("GitHub pull request already exists"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github pr create error")
	}
	if err.Error() != "GitHub pull request already exists" {
		t.Fatalf("unexpected github pr create error: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	var payload integration.Response
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("pr create json parse: %v, output: %q", err, output)
	}
	if payload.PullRequestStatus == nil {
		t.Fatal("expected pull request status in json response")
	}
	if payload.PullRequestStatus.State != "already-exists" {
		t.Fatalf("unexpected pull request state: %q", payload.PullRequestStatus.State)
	}
	if payload.PullRequestStatus.ExitCode != 1 {
		t.Fatalf("unexpected pull request exit-code: %d", payload.PullRequestStatus.ExitCode)
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

func TestIntegrationGitHubPRCreateCommandPreservesGenericMultilineStderrField(t *testing.T) {
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
				State:      "already-exists",
				Command:    "gh",
				Path:       "/usr/local/bin/gh",
				ExitCode:   1,
				Message:    "failed",
				Stderr:     "line one\nline two",
			},
		},
		err: assertErr("failed"),
	})

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected github pr create error")
	}

	output := stdout.String()
	if !strings.Contains(output, "stderr=line one\nline two\n") {
		t.Fatalf("expected pr stderr to remain a single multiline field, got %q", output)
	}
	if strings.Contains(output, "stderr=line two\n") {
		t.Fatalf("unexpected split pr stderr field: %q", output)
	}
}

func TestIntegrationGitHubPRListCommandPassesFilters(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "list", "--repo", "owner/name", "--state", "open", "--scope", "reviewer", "--limit", "5", "--query", "label:bug"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			MergeRequests: []integration.MergeRequest{{
				System:     "github",
				Repository: "owner/name",
				Number:     42,
				Title:      "Add integration",
				State:      "OPEN",
				BaseRef:    "main",
				HeadRef:    "feature",
				URL:        "https://github.com/owner/name/pull/42",
			}},
			Metadata: map[string]string{"repository": "owner/name", "state": "open", "scope": "reviewer"},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr list command: %v", err)
	}

	if provider.request.IntegrationType != "repository" || provider.request.Operation != "search" {
		t.Fatalf("unexpected request: %#v", provider.request)
	}
	if provider.request.State != "open" || provider.request.Scope != "reviewer" || provider.request.Limit != 5 || provider.request.Query != "label:bug" {
		t.Fatalf("unexpected filters: %#v", provider.request)
	}
	output := stdout.String()
	for _, fragment := range []string{
		"merge_request_count=1\n",
		"merge_request_number=42\n",
		"merge_request_title=Add integration\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("github pr list output must include %q, got %q", fragment, output)
		}
	}
}

func TestIntegrationGitHubPRCommentCreateCommandPassesInlineLocation(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "comment", "create", "--repo", "owner/name", "--number", "42", "--body", "Fix this", "--path", "file.go", "--line", "12"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			ReviewRemarks: []integration.ReviewRemark{{
				System:             "github",
				Repository:         "owner/name",
				MergeRequestNumber: 42,
				ExternalID:         "comment-1",
				ReplyToID:          "thread-1",
				State:              "unresolved",
				Path:               "file.go",
				Line:               12,
				Body:               "Fix this",
			}},
			OperationResult: &integration.OperationResult{System: "github", ObjectType: "review-remark", Operation: "create", Status: "ok", ExternalID: "comment-1"},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr comment create command: %v", err)
	}
	if provider.request.IntegrationType != "repository" || provider.request.Resource != "comment" || provider.request.Operation != "create" {
		t.Fatalf("unexpected request: %#v", provider.request)
	}
	if provider.request.Path != "file.go" || provider.request.Line != 12 || provider.request.Body != "Fix this" {
		t.Fatalf("unexpected inline request: %#v", provider.request)
	}
	output := stdout.String()
	if !strings.Contains(output, "remark_thread_id=thread-1\n") || !strings.Contains(output, "operation=create\n") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestIntegrationGitHubPRCommentResolveCommandPassesThread(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "comment", "resolve", "--thread", "thread-1"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			ReviewRemarks:   []integration.ReviewRemark{{System: "github", ExternalID: "thread-1", ReplyToID: "thread-1", State: "resolved"}},
			OperationResult: &integration.OperationResult{System: "github", ObjectType: "review-remark", Operation: "resolve", Status: "ok", ExternalID: "thread-1"},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr comment resolve command: %v", err)
	}
	if provider.request.ThreadID != "thread-1" || provider.request.Operation != "resolve" || provider.request.IntegrationType != "repository" {
		t.Fatalf("unexpected request: %#v", provider.request)
	}
	output := stdout.String()
	if !strings.Contains(output, "remark_state=resolved\n") || !strings.Contains(output, "operation=resolve\n") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestIntegrationGitHubPRCommentReplyCommandPassesThread(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "pr", "comment", "reply", "--thread", "thread-1", "--body", "Reply body"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			ReviewRemarks:   []integration.ReviewRemark{{System: "github", ExternalID: "comment-1", ReplyToID: "thread-1", State: "reply", Body: "Reply body"}},
			OperationResult: &integration.OperationResult{System: "github", ObjectType: "review-remark", Operation: "reply", Status: "ok", ExternalID: "comment-1"},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github pr comment reply command: %v", err)
	}
	if provider.request.ThreadID != "thread-1" || provider.request.Body != "Reply body" || provider.request.Operation != "reply" || provider.request.IntegrationType != "repository" {
		t.Fatalf("unexpected request: %#v", provider.request)
	}
	output := stdout.String()
	if !strings.Contains(output, "remark_thread_id=thread-1\n") || !strings.Contains(output, "operation=reply\n") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestIntegrationGitHubIssueLabelAddCommandSendsCanonicalLabels(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "github", "issue", "label", "add", "--repo", "owner/name", "--number", "123", "--label", "bug", "--label", "backend"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			OperationResult: &integration.OperationResult{System: "github", ObjectType: "label", Operation: "add", Status: "ok", ExternalID: "123", Message: "labels updated"},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("github", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute github issue label add command: %v", err)
	}
	if provider.request.Operation != "add" || provider.request.IntegrationType != "tracker" {
		t.Fatalf("unexpected request: %#v", provider.request)
	}
	if strings.Join(provider.request.Labels, ",") != "bug,backend" {
		t.Fatalf("unexpected labels: %#v", provider.request.Labels)
	}
	output := stdout.String()
	if !strings.Contains(output, "object=label\n") || !strings.Contains(output, "operation=add\n") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestIntegrationConfluencePageGetCommandPrintsWikiPage(t *testing.T) {
	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"integration", "confluence", "page", "get", "--id", "123"})

	provider := &capturingCLIProvider{
		response: integration.Response{
			WikiPage: &integration.WikiPage{
				System:     "confluence",
				Space:      "ENG",
				ExternalID: "123",
				Title:      "Architecture",
				BodyFormat: "storage",
				Version:    7,
				URL:        "https://confluence.example/display/ENG/Architecture",
				UpdatedBy:  integration.User{System: "confluence", Login: "alice", Name: "Alice"},
			},
		},
	}
	service := newIntegrationService(cmd)
	service.RegisterProvider("confluence", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute confluence page get command: %v", err)
	}
	if provider.request.IntegrationType != "wiki" || provider.request.ExternalID != "123" {
		t.Fatalf("unexpected request: %#v", provider.request)
	}
	output := stdout.String()
	for _, fragment := range []string{
		"system=confluence\n",
		"resource=page\n",
		"operation=get\n",
		"page_id=123\n",
		"space=ENG\n",
		"title=Architecture\n",
		"version=7\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("confluence output must include %q, got %q", fragment, output)
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

type capturingPrivateStore struct {
	values map[string]string
}

func (s *capturingPrivateStore) Get(_ context.Context, name string) (string, error) {
	value, ok := s.values[name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}

func (s *capturingPrivateStore) Set(_ context.Context, name string, value string) error {
	s.values[name] = value
	return nil
}

func (s *capturingPrivateStore) Delete(_ context.Context, name string) error {
	delete(s.values, name)
	return nil
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
