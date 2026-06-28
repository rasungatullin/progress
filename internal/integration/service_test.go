package integration

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
	"github.com/rasungatullin/progress/internal/logging"
)

func boolPtr(value bool) *bool {
	return &value
}

func TestDispatchWithoutRegisteredProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.Dispatch(context.Background(), Request{System: "gitlab", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if route.System != "gitlab" {
		t.Fatalf("unexpected system: %q", route.System)
	}
	if route.ProviderAvailable {
		t.Fatal("provider must be unavailable")
	}
	if route.ExpectedResult != "tracker-issue" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
	if len(route.Diagnostics) < 3 {
		t.Fatalf("expected diagnostic details, got %#v", route.Diagnostics)
	}
	if !contains(route.Diagnostics, "provider=gitlab unknown to current integration configuration") {
		t.Fatalf("expected provider diagnostic, got %#v", route.Diagnostics)
	}
	if !contains(route.Diagnostics, "registered systems=github") {
		t.Fatalf("expected registered systems diagnostic, got %#v", route.Diagnostics)
	}
}

func TestNewServiceFromConfigUsesDefaultSystem(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystem: "github",
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github"},
		},
	})
	service.RegisterProvider("github", stubProvider{})

	route, err := service.Dispatch(context.Background(), Request{Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.System != "github" {
		t.Fatalf("expected default system github, got %q", route.System)
	}
}

func TestDispatchReportsDisabledConfiguredSystem(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Enabled: boolPtr(false)},
		},
	})

	route, err := service.Dispatch(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.ProviderAvailable {
		t.Fatal("provider must be unavailable for disabled system")
	}
	if !contains(route.Diagnostics, "provider=github disabled by integration configuration") {
		t.Fatalf("expected disabled-system diagnostic, got %#v", route.Diagnostics)
	}
}

func TestExecuteReturnsDisabledSystemError(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Enabled: boolPtr(false)},
		},
	})

	_, err := service.Execute(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})
	if err == nil {
		t.Fatal("expected disabled-system error")
	}
	if err.Error() != "integration provider disabled by configuration: github" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDispatchReportsUnsupportedConfiguredSystemType(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"gitlab": {Type: "script"},
		},
	})

	route, err := service.Dispatch(context.Background(), Request{System: "gitlab", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !contains(route.Diagnostics, "provider=gitlab configured with unsupported type=script") {
		t.Fatalf("expected unsupported-type diagnostic, got %#v", route.Diagnostics)
	}
}

func TestExecuteUsesRegisteredProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	service.RegisterProvider("github", stubProvider{
		response: Response{
			Issue: &TrackerIssue{Number: 42, Title: "Example"},
		},
	})

	result, err := service.Execute(context.Background(), Request{System: " GitHub ", Resource: " Issue ", Operation: " GET "})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Route.System != "github" {
		t.Fatalf("unexpected route system: %q", result.Route.System)
	}
	if !result.Route.ProviderAvailable {
		t.Fatal("provider must be available")
	}
	if result.Issue == nil || result.Issue.Number != 42 {
		t.Fatalf("unexpected issue payload: %#v", result.Issue)
	}
	if result.System != "github" {
		t.Fatalf("unexpected result system: %q", result.System)
	}
	if result.Resource != "issue" {
		t.Fatalf("unexpected result resource: %q", result.Resource)
	}
	if result.Operation != "get" {
		t.Fatalf("unexpected result operation: %q", result.Operation)
	}
}

func TestExecuteOverwritesSystemFromRouteForNestedObjects(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"enterprise": {Type: "github"},
		},
	})
	service.RegisterProvider("enterprise", stubProvider{
		response: Response{
			System:            "github",
			AuthStatus:        &AuthStatus{System: "github"},
			RepositoryStatus:  &RepositoryStatus{System: "github"},
			IssueStatus:       &IssueStatus{System: "github"},
			PullRequestStatus: &PullRequestStatus{System: "github"},
			Issue: &TrackerIssue{
				System:    "github",
				Author:    TrackerUser{System: "github"},
				Assignees: []TrackerUser{{System: "github"}},
			},
			PullRequest: &TrackerPullRequest{System: "github", Author: TrackerUser{System: "github"}},
			Comments:    []TrackerComment{{System: "github", Author: TrackerUser{System: "github"}}},
			Reviews:     []TrackerReview{{System: "github", Author: TrackerUser{System: "github"}}},
			RepositoryRef: &TrackerRepository{
				System: "github",
			},
			SearchResults: []TrackerSearchResult{{System: "github"}},
			Artifacts:     []Artifact{{System: "github"}},
		},
	})

	result, err := service.Execute(context.Background(), Request{System: "enterprise", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if result.System != "enterprise" || result.AuthStatus.System != "enterprise" || result.RepositoryStatus.System != "enterprise" || result.IssueStatus.System != "enterprise" || result.PullRequestStatus.System != "enterprise" {
		t.Fatalf("expected top-level status systems to use route name, got %#v", result)
	}
	if result.Issue == nil || result.Issue.System != "enterprise" || result.Issue.Author.System != "enterprise" || result.Issue.Assignees[0].System != "enterprise" {
		t.Fatalf("expected issue payload systems to use route name, got %#v", result.Issue)
	}
	if result.PullRequest == nil || result.PullRequest.System != "enterprise" || result.PullRequest.Author.System != "enterprise" {
		t.Fatalf("expected pull request payload systems to use route name, got %#v", result.PullRequest)
	}
	if result.Comments[0].System != "enterprise" || result.Comments[0].Author.System != "enterprise" {
		t.Fatalf("expected comment payload systems to use route name, got %#v", result.Comments)
	}
	if result.Reviews[0].System != "enterprise" || result.Reviews[0].Author.System != "enterprise" {
		t.Fatalf("expected review payload systems to use route name, got %#v", result.Reviews)
	}
	if result.RepositoryRef == nil || result.RepositoryRef.System != "enterprise" {
		t.Fatalf("expected repository payload system to use route name, got %#v", result.RepositoryRef)
	}
	if result.SearchResults[0].System != "enterprise" || result.Artifacts[0].System != "enterprise" {
		t.Fatalf("expected search results and artifacts to use route name, got %#v %#v", result.SearchResults, result.Artifacts)
	}
}

func TestDispatchReturnsErrorForMissingSystem(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	_, err := service.Dispatch(context.Background(), Request{Resource: "issue", Operation: "get"})
	if err == nil {
		t.Fatal("expected dispatch error")
	}
	if err.Error() != "invalid integration request: system is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteReturnsErrorForMissingSystem(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	_, err := service.Execute(context.Background(), Request{Resource: "issue", Operation: "get"})
	if err == nil {
		t.Fatal("expected execute error")
	}
	if err.Error() != "invalid integration request: system is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteReturnsErrorForUnknownProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	_, err := service.Execute(context.Background(), Request{System: "gitlab"})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "integration provider not registered: gitlab" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewServiceRegistersGitHubProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.Dispatch(context.Background(), Request{System: "github", Resource: "auth", Operation: "status"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if !route.ProviderAvailable {
		t.Fatal("github provider must be available")
	}
	if route.ExpectedResult != "integration-auth-status" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
}

func TestDispatchPRCreateUsesStatusResultContract(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.Dispatch(context.Background(), Request{System: "github", Resource: "pr", Operation: "create"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if route.ExpectedResult != "integration-pull-request-status" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
}

func TestExecutePropagatesProviderError(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	service.RegisterProvider("github", stubProvider{err: errors.New("provider failed")})

	_, err := service.Execute(context.Background(), Request{System: "github"})
	if err == nil {
		t.Fatal("expected provider error")
	}
	if err.Error() != "provider failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

type stubProvider struct {
	response Response
	err      error
}

func (p stubProvider) Execute(context.Context, ProviderRequest) (Response, error) {
	if p.err != nil {
		return Response{}, p.err
	}

	return p.response, nil
}

type capturingProvider struct {
	seen ProviderRequest
}

func (p *capturingProvider) Execute(_ context.Context, req ProviderRequest) (Response, error) {
	p.seen = req
	return Response{}, nil
}

func TestExecutePassesNormalizedRequestToProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	provider := &capturingProvider{}
	service.RegisterProvider("github", provider)

	_, err := service.Execute(context.Background(), Request{
		System:       " GitHub ",
		Resource:     " Issue ",
		Operation:    " GET ",
		Repository:   " owner/name ",
		RepoProvided: true,
		Query:        " is:open ",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if provider.seen.System != "github" {
		t.Fatalf("unexpected normalized system: %q", provider.seen.System)
	}
	if provider.seen.Resource != "issue" {
		t.Fatalf("unexpected normalized resource: %q", provider.seen.Resource)
	}
	if provider.seen.Operation != "get" {
		t.Fatalf("unexpected normalized operation: %q", provider.seen.Operation)
	}
	if provider.seen.Repository != "owner/name" {
		t.Fatalf("unexpected normalized repository: %q", provider.seen.Repository)
	}
	if !provider.seen.RepoProvided {
		t.Fatal("expected repo-provided flag to be preserved")
	}
	if provider.seen.Query != "is:open" {
		t.Fatalf("unexpected normalized query: %q", provider.seen.Query)
	}
	if provider.seen.Route.System != "github" {
		t.Fatalf("unexpected route system: %q", provider.seen.Route.System)
	}
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}

	return false
}
