package script

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceExecutesConfiguredOperation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Project:         "ABC",
		Settings:        map[string]string{"tracker_url": "https://tracker.example"},
		Operations: map[string]model.IntegrationOperationConfig{
			"tracker.task.get": {
				Script:   "task-get.sh",
				Required: []string{"number", "project"},
				Optional: []string{"tracker_url"},
				Defaults: map[string]string{
					"project":     "${system.project}",
					"tracker_url": "${system.settings.tracker_url}",
				},
			},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return root, nil }
	service.runCommand = func(_ context.Context, path string, _ []string, env []string, dir string) commandResult {
		if path != filepath.Join(root, "task-get.sh") {
			t.Fatalf("unexpected script path: %q", path)
		}
		if dir != root {
			t.Fatalf("unexpected script workdir: %q", dir)
		}
		requestFile := envValue(env, "PROGRESS_INTEGRATION_REQUEST_FILE")
		content, err := os.ReadFile(requestFile)
		if err != nil {
			t.Fatalf("read request file: %v", err)
		}
		var envelope scriptEnvelope
		if err := json.Unmarshal(content, &envelope); err != nil {
			t.Fatalf("decode request envelope: %v", err)
		}
		if envelope.OperationName != "tracker.task.get" || envelope.Request["project"] != "ABC" || envelope.Request["tracker_url"] != "https://tracker.example" {
			t.Fatalf("unexpected request envelope: %#v", envelope)
		}
		if envelope.Request["number"].(float64) != 123 {
			t.Fatalf("unexpected request number: %#v", envelope.Request["number"])
		}
		return commandResult{stdout: `{"status":"ok","task":{"system":"work-tracker","number":123,"title":"Задача","body":"Описание","state":"open","traits":["backend"],"author":{"login":"alice"}}}`}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
		Number:          123,
	})
	if err != nil {
		t.Fatalf("execute script operation: %v", err)
	}
	if response.Task == nil || response.Task.Number != 123 || response.Task.Title != "Задача" {
		t.Fatalf("unexpected task response: %#v", response.Task)
	}
	if response.Issue == nil || response.Issue.Number != 123 || response.Issue.Labels[0] != "backend" {
		t.Fatalf("unexpected compatible issue response: %#v", response.Issue)
	}
}

func TestOperationNameForTaskCommentsRequest(t *testing.T) {
	t.Parallel()

	name := operationNameForRequest(model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "comments",
	})

	if name != "tracker.task.comment.list" {
		t.Fatalf("unexpected operation name: %q", name)
	}
}

func TestOperationNameForTaskListRequest(t *testing.T) {
	t.Parallel()

	name := operationNameForRequest(model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "list",
	})

	if name != "tracker.task.list" {
		t.Fatalf("unexpected operation name: %q", name)
	}
}

func TestServiceRejectsMissingRequiredFieldBeforeScript(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"tracker.task.get": {Script: "task-get.sh", Required: []string{"number"}},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return t.TempDir(), nil }
	service.runCommand = func(context.Context, string, []string, []string, string) commandResult {
		t.Fatal("script must not run when required field is missing")
		return commandResult{}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
	})
	if err == nil {
		t.Fatal("expected missing required field error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindInvalidRequest {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}

func TestServiceNormalizesInvalidJSONResponse(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"tracker.task.get": {Script: "task-get.sh"},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return t.TempDir(), nil }
	service.runCommand = func(context.Context, string, []string, []string, string) commandResult {
		return commandResult{stdout: `{not-json`}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
	})
	if err == nil {
		t.Fatal("expected invalid json error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindInternalIntegration {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}

func TestServiceNormalizesTimeout(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"tracker.task.get": {Script: "task-get.sh", Timeout: "1ms"},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return t.TempDir(), nil }
	service.runCommand = func(ctx context.Context, _ string, _ []string, _ []string, _ string) commandResult {
		<-ctx.Done()
		return commandResult{err: ctx.Err()}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindTimeout || !response.Failure.Retryable {
		t.Fatalf("unexpected timeout failure: %#v", response.Failure)
	}
}

func TestServiceUsesScriptFailureResponse(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"tracker.task.get": {Script: "task-get.sh"},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return t.TempDir(), nil }
	service.runCommand = func(context.Context, string, []string, []string, string) commandResult {
		return commandResult{stdout: `{"status":"failed","failure":{"kind":"not-found","message":"Задача не найдена"}}`}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
	})
	if err == nil {
		t.Fatal("expected script failure error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindNotFound {
		t.Fatalf("unexpected script failure: %#v", response.Failure)
	}
}

func TestRunCommandReportsMissingScriptAsInvalidRequestInput(t *testing.T) {
	t.Parallel()

	result := runCommand(context.Background(), filepath.Join(t.TempDir(), "missing.sh"), nil, os.Environ(), t.TempDir())
	if result.exitCode != -1 || result.err == nil {
		t.Fatalf("expected start failure for missing script, got %#v", result)
	}
	if errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("missing script must not look like timeout: %v", result.err)
	}
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}
