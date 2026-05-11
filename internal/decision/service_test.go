package decision

import (
	"context"
	"errors"
	"log"
	"testing"

	"github.com/rasungatullin/progress/internal/integration"
)

func TestServiceStartBuildsReadyContext(t *testing.T) {
	t.Parallel()

	integrationStub := &stubIntegrationExecutor{
		response: integration.Response{
			Issue: &integration.TrackerIssue{
				System:     "github",
				Repository: "owner/name",
				Number:     123,
				Title:      "Implement decision start",
				State:      "OPEN",
				URL:        "https://github.com/owner/name/issues/123",
			},
		},
	}
	service := &Service{logger: log.Default(), integration: integrationStub}

	result, err := service.Start(context.Background(), StartInput{TaskNumber: 123})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !result.Ready {
		t.Fatal("expected ready result")
	}
	if result.Context.Signal.Source != SignalSourceTask {
		t.Fatalf("unexpected signal source: %q", result.Context.Signal.Source)
	}
	if result.Context.Signal.Kind != SignalKindTask {
		t.Fatalf("unexpected signal kind: %q", result.Context.Signal.Kind)
	}
	if result.Context.Signal.TaskNumber != 123 {
		t.Fatalf("unexpected task number: %d", result.Context.Signal.TaskNumber)
	}
	if result.Context.Issue == nil {
		t.Fatal("expected issue in context")
	}
	if result.Context.Issue.Number != 123 {
		t.Fatalf("unexpected issue number: %d", result.Context.Issue.Number)
	}
	if integrationStub.request.System != "github" || integrationStub.request.Resource != "issue" || integrationStub.request.Operation != "get" {
		t.Fatalf("unexpected integration request: %#v", integrationStub.request)
	}
	if integrationStub.request.Number != 123 {
		t.Fatalf("unexpected integration request number: %d", integrationStub.request.Number)
	}
}

func TestServiceStartRejectsNonPositiveTaskNumber(t *testing.T) {
	t.Parallel()

	service := &Service{logger: log.Default(), integration: &stubIntegrationExecutor{}}

	_, err := service.Start(context.Background(), StartInput{TaskNumber: 0})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if err.Error() != "task number must be greater than zero" {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestServiceStartPropagatesIntegrationError(t *testing.T) {
	t.Parallel()

	service := &Service{
		logger:      log.Default(),
		integration: &stubIntegrationExecutor{err: errors.New("integration failed")},
	}

	_, err := service.Start(context.Background(), StartInput{TaskNumber: 42})
	if err == nil {
		t.Fatal("expected integration error")
	}
	if err.Error() != "integration failed" {
		t.Fatalf("unexpected integration error: %v", err)
	}
}

type stubIntegrationExecutor struct {
	response integration.Response
	err      error
	request  integration.Request
}

func (s *stubIntegrationExecutor) Execute(_ context.Context, request integration.Request) (integration.Response, error) {
	s.request = request
	return s.response, s.err
}
