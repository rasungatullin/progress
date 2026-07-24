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
			"issue.issue.get": {
				Script:   "task-get.sh",
				Required: []string{"id", "project"},
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
		if envelope.OperationName != "issue.issue.get" || envelope.Request["project"] != "ABC" || envelope.Request["tracker_url"] != "https://tracker.example" {
			t.Fatalf("unexpected request envelope: %#v", envelope)
		}
		if envelope.Request["id"] != "ABC-123" {
			t.Fatalf("unexpected request id: %#v", envelope.Request["id"])
		}
		return commandResult{stdout: `{"status":"ok","task":{"system":"work-tracker","id":"ABC-123","title":"Задача","body":"Описание","state":"open","traits":["backend"],"author":{"login":"alice"}}}`}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
		ID:              "ABC-123",
	})
	if err != nil {
		t.Fatalf("execute script operation: %v", err)
	}
	if response.Task == nil || response.Task.ID != "ABC-123" || response.Task.Title != "Задача" {
		t.Fatalf("unexpected task response: %#v", response.Task)
	}
}

func TestServiceRunsConfiguredCommandWithoutJoiningWorkdir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"issue.issue.get": {Command: "tracker-get"},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return root, nil }
	service.runCommand = func(_ context.Context, path string, _ []string, _ []string, dir string) commandResult {
		if path != "tracker-get" {
			t.Fatalf("unexpected command path: %q", path)
		}
		if dir != root {
			t.Fatalf("unexpected command workdir: %q", dir)
		}
		return commandResult{stdout: `{"status":"ok","task":{"system":"work-tracker","id":"ABC-123","title":"Задача","state":"open"}}`}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
		ID:              "ABC-123",
	})
	if err != nil {
		t.Fatalf("execute script operation: %v", err)
	}
	if response.Task == nil || response.Task.ID != "ABC-123" {
		t.Fatalf("unexpected task response: %#v", response.Task)
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

	if name != "issue.issue.comment.list" {
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

	if name != "issue.issue.list" {
		t.Fatalf("unexpected operation name: %q", name)
	}
}

func TestServicePreservesEmptyTaskCollections(t *testing.T) {
	t.Parallel()

	for _, stdout := range []string{
		`{"status":"ok","tasks":[]}`,
	} {
		stdout := stdout
		t.Run(stdout, func(t *testing.T) {
			t.Parallel()

			service := NewService(model.IntegrationSystemConfig{
				IntegrationType: model.IntegrationTypeTracker,
				Operations: map[string]model.IntegrationOperationConfig{
					"issue.issue.search": {Script: "task-search.sh"},
				},
			})
			service.resolveWorkdir = func(context.Context) (string, error) { return t.TempDir(), nil }
			service.runCommand = func(context.Context, string, []string, []string, string) commandResult {
				return commandResult{stdout: stdout}
			}

			response, err := service.Execute(context.Background(), model.ProviderRequest{
				IntegrationType: model.IntegrationTypeTracker,
				System:          "work-tracker",
				Resource:        "task",
				ObjectType:      "task",
				Operation:       "search",
			})
			if err != nil {
				t.Fatalf("execute script operation: %v", err)
			}
			if response.Tasks == nil || len(response.Tasks) != 0 {
				t.Fatalf("expected empty canonical task list, got %#v", response.Tasks)
			}
		})
	}
}

func TestOperationNameForTaskCommentCreateRequest(t *testing.T) {
	t.Parallel()

	name := operationNameForRequest(model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		Resource:        "task-comment",
		ObjectType:      "task-comment",
		Operation:       "create",
	})

	if name != "issue.issue.comment.create" {
		t.Fatalf("unexpected operation name: %q", name)
	}
}

func TestServiceRejectsMissingRequiredFieldBeforeScript(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"issue.issue.get": {Script: "task-get.sh", Required: []string{"id"}},
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

func TestServiceRejectsEmptySuccessfulTaskResponse(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"issue.issue.get": {Script: "task-get.sh"},
		},
	})
	service.resolveWorkdir = func(context.Context) (string, error) { return t.TempDir(), nil }
	service.runCommand = func(context.Context, string, []string, []string, string) commandResult {
		return commandResult{stdout: `{"status":"ok"}`}
	}

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
		ID:              "ABC-123",
	})
	if err == nil {
		t.Fatal("expected empty successful response error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindExternalFailure {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}

func TestServiceNormalizesInvalidJSONResponse(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		IntegrationType: model.IntegrationTypeTracker,
		Operations: map[string]model.IntegrationOperationConfig{
			"issue.issue.get": {Script: "task-get.sh"},
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
			"issue.issue.get": {Script: "task-get.sh", Timeout: "1ms"},
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
			"issue.issue.get": {Script: "task-get.sh"},
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
