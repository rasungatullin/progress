package integration

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rasungatullin/progress/internal/logging"
)

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
	if !contains(route.Diagnostics, "provider=gitlab not registered in current build") {
		t.Fatalf("expected provider diagnostic, got %#v", route.Diagnostics)
	}
	if !contains(route.Diagnostics, "registered systems=github") {
		t.Fatalf("expected registered systems diagnostic, got %#v", route.Diagnostics)
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
		System:     " GitHub ",
		Resource:   " Issue ",
		Operation:  " GET ",
		Repository: " owner/name ",
		Query:      " is:open ",
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
