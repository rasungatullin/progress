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
	route := service.Dispatch(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})

	if route.System != "github" {
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
}

func TestExecuteUsesRegisteredProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	service.RegisterProvider("github", stubProvider{
		response: Response{
			Issue: &TrackerIssue{Number: 42, Title: "Example"},
		},
	})

	result, err := service.Execute(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})
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

func TestExecuteReturnsErrorForUnknownProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	_, err := service.Execute(context.Background(), Request{System: "github"})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "integration provider not registered: github" {
		t.Fatalf("unexpected error: %v", err)
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

func (p stubProvider) Execute(context.Context, Request) (Response, error) {
	if p.err != nil {
		return Response{}, p.err
	}

	return p.response, nil
}
