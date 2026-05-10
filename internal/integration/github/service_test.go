package github

import (
	"context"
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

func TestServiceRejectsUnsupportedOperation(t *testing.T) {
	t.Parallel()

	service := NewService()
	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "github", Resource: "issue", Operation: "get"})
	assertGitHubErrorCode(t, err, ErrorCodeInvalidRequest)
	if response.Resource != "issue" {
		t.Fatalf("unexpected resource: %q", response.Resource)
	}
}

type stubRunner struct {
	result CommandResult
	config resolvedConfig
	err    error
}

func (r *stubRunner) RunAuthStatus(context.Context) (CommandResult, resolvedConfig, error) {
	return r.result, r.config, r.err
}
