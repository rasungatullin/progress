package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
	"github.com/rasungatullin/progress/internal/integration/secrets"
	"github.com/rasungatullin/progress/internal/logging"
)

func boolPtr(value bool) *bool {
	return &value
}

func TestDispatchWithoutRegisteredProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.resolveRoute(Request{System: "gitlab", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if route.System != "gitlab" {
		t.Fatalf("unexpected system: %q", route.System)
	}
	if route.ProviderAvailable {
		t.Fatal("provider must be unavailable")
	}
	if route.ExpectedResult != "canonical-task" {
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

	route, err := service.resolveRoute(Request{Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.System != "github" {
		t.Fatalf("expected default system github, got %q", route.System)
	}
}

func TestNewServiceFromConfigWithPrivateStoreResolvesMattermostToken(t *testing.T) {
	t.Parallel()

	seenAuth := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"u1","username":"service-user"}`))
	}))
	t.Cleanup(server.Close)

	service := NewServiceFromConfigWithPrivateStore(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"mattermost": {
				Type:            "mattermost",
				IntegrationType: "messenger",
				BaseURL:         server.URL,
				TokenPrivate:    "mt_auth_token",
			},
		},
	}, mapPrivateStore{values: map[string]string{"mt_auth_token": "resolved-token"}})

	result, err := service.Execute(context.Background(), Request{System: "mattermost", Resource: "auth", Operation: "status"})
	if err != nil {
		t.Fatalf("execute mattermost auth status: %v", err)
	}
	if result.AuthStatus == nil || !result.AuthStatus.Authenticated {
		t.Fatalf("expected authenticated status, got %#v", result.AuthStatus)
	}
	if seenAuth != "Bearer resolved-token" {
		t.Fatalf("expected private token in authorization header, got %q", seenAuth)
	}
}

func TestNewServiceFromConfigWithPrivateStoreReportsMissingPrivateValue(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfigWithPrivateStore(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"mattermost": {
				Type:            "mattermost",
				IntegrationType: "messenger",
				BaseURL:         "https://mattermost.example",
				TokenPrivate:    "missing",
			},
		},
	}, mapPrivateStore{values: map[string]string{}})

	result, err := service.Execute(context.Background(), Request{System: "mattermost", Resource: "auth", Operation: "status"})
	if err == nil {
		t.Fatal("expected missing private value error")
	}
	if result.Failure == nil || result.Failure.Kind != model.FailureKindAuthRequired {
		t.Fatalf("expected auth-required failure, got %#v", result.Failure)
	}
	if result.AuthStatus == nil || result.AuthStatus.Authenticated {
		t.Fatalf("expected unavailable auth status, got %#v", result.AuthStatus)
	}
}

func TestResolvePrivateSystemConfigSkipsGitHubAppPrivateKeyAfterPrivateToken(t *testing.T) {
	t.Parallel()

	config := model.IntegrationSystemConfig{
		Type:                       "github",
		TokenPrivate:               "github_auth_token",
		GitHubAppPrivateKeyPrivate: "github_app_key",
	}

	err := resolvePrivateSystemConfig(context.Background(), "github", &config, mapPrivateStore{values: map[string]string{
		"github_auth_token": "resolved-token",
	}})
	if err != nil {
		t.Fatalf("resolve private config: %v", err)
	}
	if config.Token != "resolved-token" {
		t.Fatalf("unexpected resolved token: %q", config.Token)
	}
	if config.GitHubAppPrivateKey != "" {
		t.Fatalf("GitHub App private key must not be read after private token, got: %q", config.GitHubAppPrivateKey)
	}
}

func TestResolvePrivateSystemConfigReadsGitHubAppPrivateKeyWhenTokenEnvIsEmpty(t *testing.T) {
	t.Setenv("PROGRESS_TEST_EMPTY_GITHUB_TOKEN", "")

	config := model.IntegrationSystemConfig{
		Type:                       "github",
		TokenEnv:                   "PROGRESS_TEST_EMPTY_GITHUB_TOKEN",
		GitHubAppPrivateKeyPrivate: "github_app_key",
	}

	err := resolvePrivateSystemConfig(context.Background(), "github", &config, mapPrivateStore{values: map[string]string{
		"github_app_key": "resolved-private-key",
	}})
	if err != nil {
		t.Fatalf("resolve private config: %v", err)
	}
	if config.GitHubAppPrivateKey != "resolved-private-key" {
		t.Fatalf("unexpected GitHub App private key: %q", config.GitHubAppPrivateKey)
	}
}

func TestResolvePrivateSystemConfigSkipsGitHubAppPrivateKeyWhenTokenEnvHasValue(t *testing.T) {
	t.Setenv("PROGRESS_TEST_GITHUB_TOKEN", "direct-token")

	config := model.IntegrationSystemConfig{
		Type:                       "github",
		TokenEnv:                   "PROGRESS_TEST_GITHUB_TOKEN",
		GitHubAppPrivateKeyPrivate: "github_app_key",
	}

	if err := resolvePrivateSystemConfig(context.Background(), "github", &config, nil); err != nil {
		t.Fatalf("resolve private config: %v", err)
	}
	if config.GitHubAppPrivateKey != "" {
		t.Fatalf("GitHub App private key must not be read when token_env has value, got: %q", config.GitHubAppPrivateKey)
	}
}

func TestDispatchReportsDisabledConfiguredSystem(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Enabled: boolPtr(false)},
		},
	})

	route, err := service.resolveRoute(Request{System: "github", Resource: "issue", Operation: "get"})
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

type mapPrivateStore struct {
	values map[string]string
}

func (s mapPrivateStore) Get(_ context.Context, name string) (string, error) {
	value, ok := s.values[name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return value, nil
}

func (s mapPrivateStore) Set(_ context.Context, name string, value string) error {
	s.values[name] = value
	return nil
}

func (s mapPrivateStore) Delete(_ context.Context, name string) error {
	delete(s.values, name)
	return nil
}

func TestDispatchReportsConfiguredTransport(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Transport: "api"},
		},
	})

	route, err := service.resolveRoute(Request{System: "github", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !contains(route.Diagnostics, "transport=api") {
		t.Fatalf("expected transport diagnostic, got %#v", route.Diagnostics)
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
			"gitlab": {Type: "custom"},
		},
	})

	route, err := service.resolveRoute(Request{System: "gitlab", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !contains(route.Diagnostics, "provider=gitlab configured with unsupported type=custom") {
		t.Fatalf("expected unsupported-type diagnostic, got %#v", route.Diagnostics)
	}
}

func TestDispatchRepositoryCommentReplyReturnsOperationResult(t *testing.T) {
	t.Parallel()

	service := newEmptyService(logging.New(io.Discard))
	route, err := service.resolveRoute(Request{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "comment",
		ObjectType:      "comment",
		Operation:       "reply",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.ExpectedResult != "integration-operation-result" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
	if route.Operation != "reply" || route.ObjectType != "comment" {
		t.Fatalf("unexpected route: %#v", route)
	}
}

func TestDispatchRepositoryReviewRemarkReplyReturnsOperationResult(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.resolveRoute(Request{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "comment",
		ObjectType:      "review-remark",
		Operation:       "reply",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.ExpectedResult != "integration-operation-result" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
	if route.Operation != "reply" || route.ObjectType != "review-remark" {
		t.Fatalf("unexpected route: %#v", route)
	}
}

func TestDispatchRepositoryReviewRemarkListUsesTypedRegistryOperation(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.resolveRoute(Request{
		IntegrationType: "repo",
		System:          "github",
		Resource:        "review-remark",
		ObjectType:      "review-remark",
		Operation:       "list",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.IntegrationType != model.IntegrationTypeRepository || route.ObjectType != "review-remark" || route.Operation != "list" {
		t.Fatalf("unexpected typed review remark route: %#v", route)
	}
}

func TestExecuteUsesRegisteredProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	service.RegisterProvider("github", stubProvider{
		response: Response{
			Task: &CanonicalTask{ID: "42", Title: "Example"},
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
	if result.Task == nil || result.Task.ID != "42" {
		t.Fatalf("unexpected task payload: %#v", result.Task)
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

func TestExecuteMapsCanonicalTaskSearchResults(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {
				Type:             "github",
				TaskLabelMapping: map[string]string{"external-ready": "ready"},
			},
		},
	})
	service.RegisterProvider("github", stubProvider{
		response: Response{
			Tasks: []CanonicalTask{{
				ID:     "42",
				Title:  "Каноническая задача",
				Traits: []string{"external-ready"},
				Author: User{Login: "alice"},
			}},
		},
	})

	result, err := service.Execute(context.Background(), Request{System: "github", Resource: "issue", Operation: "search"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(result.Tasks) != 1 || result.Tasks[0].System != "github" || result.Tasks[0].Traits[0] != "ready" {
		t.Fatalf("unexpected canonical tasks: %#v", result.Tasks)
	}
}

func TestExecuteGitHubPRCreateAlreadyExistsReturnsCanonicalIdempotentResult(t *testing.T) {
	t.Parallel()

	command := filepath.Join(t.TempDir(), "gh-already-exists")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' 'a pull request for branch \"feature\" into branch \"main\" already exists:' 'https://github.com/owner/name/pull/15' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write GitHub command: %v", err)
	}

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeRepository: "github"},
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Command: command, IntegrationTypes: []string{model.IntegrationTypeRepository}},
		},
	})
	result, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "create",
		Repository:      "owner/name",
		Base:            "main",
		Head:            "feature",
		Title:           "Канонический запрос",
	})
	if err != nil {
		t.Fatalf("already existing pull request must be idempotent: %v", err)
	}
	if result.Failure != nil || result.Status != model.ResponseStatusOK {
		t.Fatalf("unexpected failure: %#v", result)
	}
	if result.MergeRequest == nil || result.MergeRequest.Number != 15 || result.MergeRequest.URL != "https://github.com/owner/name/pull/15" {
		t.Fatalf("unexpected merge request: %#v", result.MergeRequest)
	}
	if result.OperationResult == nil || !result.OperationResult.Idempotent || result.OperationResult.Status != model.ResponseStatusOK {
		t.Fatalf("unexpected operation result: %#v", result.OperationResult)
	}
}

func TestExecuteGitHubPRCreateDoesNotMaskExternalFailureContainingURL(t *testing.T) {
	t.Parallel()

	command := filepath.Join(t.TempDir(), "gh-external-failure")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' 'request failed; details: https://github.com/owner/name/pull/15' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write GitHub command: %v", err)
	}

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeRepository: "github"},
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Command: command, IntegrationTypes: []string{model.IntegrationTypeRepository}},
		},
	})
	result, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "create",
		Repository:      "owner/name",
		Base:            "main",
		Head:            "feature",
		Title:           "Канонический запрос",
	})
	if err == nil {
		t.Fatal("external failure containing URL must remain a failure")
	}
	if result.Failure == nil || result.Status != model.ResponseStatusFailed {
		t.Fatalf("unexpected response: %#v", result)
	}
	if result.MergeRequest != nil || result.OperationResult != nil {
		t.Fatalf("failed response must not publish a canonical result: %#v", result)
	}
}

func TestExecuteAppliesRouteSystemToMergeRequestList(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"enterprise": {Type: "github"},
		},
	})
	service.RegisterProvider("enterprise", stubProvider{
		response: Response{
			MergeRequests: []MergeRequest{
				{System: "github", Number: 41, Author: User{System: "github", Login: "alice"}},
				{System: "github", Number: 42, Author: User{System: "github", Login: "bob"}},
			},
		},
	})

	result, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "enterprise",
		ObjectType:      "merge-request",
		Operation:       "search",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(result.MergeRequests) != 2 {
		t.Fatalf("unexpected merge requests: %#v", result.MergeRequests)
	}
	for _, mergeRequest := range result.MergeRequests {
		if mergeRequest.System != "enterprise" || mergeRequest.Author.System != "enterprise" {
			t.Fatalf("expected merge request to use route name: %#v", mergeRequest)
		}
	}
}

func TestExecuteMapsExternalTaskLabelsToCanonical(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {
				Type: "github",
				TaskLabelMapping: map[string]string{
					"external-bug": "bug",
					"noise":        "",
				},
			},
		},
	})
	service.RegisterProvider("github", stubProvider{
		response: Response{
			Task: &CanonicalTask{
				Traits: []string{"external-bug", "noise", "plain"},
			},
		},
	})

	result, err := service.Execute(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Task == nil || len(result.Task.Traits) != 2 || result.Task.Traits[0] != "bug" || result.Task.Traits[1] != "plain" {
		t.Fatalf("unexpected canonical task traits: %#v", result.Task)
	}
}

func TestExecuteMapsCanonicalTaskLabelsToExternal(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {
				Type: "github",
				TaskLabelMapping: map[string]string{
					"external-bug": "bug",
					"noise":        "",
				},
			},
		},
	})
	provider := &capturingProvider{}
	service.RegisterProvider("github", provider)

	_, err := service.Execute(context.Background(), Request{
		IntegrationType: "tracker",
		System:          "github",
		Resource:        "label",
		ObjectType:      "label",
		Operation:       "add",
		Labels:          []string{"bug", "plain"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(provider.seen.Labels) != 2 || provider.seen.Labels[0] != "external-bug" || provider.seen.Labels[1] != "plain" {
		t.Fatalf("unexpected provider labels: %#v", provider.seen.Labels)
	}
}

func TestExecuteMapsCanonicalTaskSearchExcludeLabelsToExternal(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {
				Type: "github",
				TaskLabelMapping: map[string]string{
					"external-ready":   "ready",
					"external-blocked": "blocked",
				},
			},
		},
	})
	provider := &capturingProvider{}
	service.RegisterProvider("github", provider)

	_, err := service.Execute(context.Background(), Request{
		IntegrationType: "tracker",
		System:          "github",
		Resource:        "issue",
		Operation:       "search",
		Labels:          []string{"ready"},
		ExcludeLabels:   []string{"blocked", "plain"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fmt.Sprint(provider.seen.Labels) != "[external-ready]" {
		t.Fatalf("unexpected provider labels: %#v", provider.seen.Labels)
	}
	if fmt.Sprint(provider.seen.ExcludeLabels) != "[external-blocked plain]" {
		t.Fatalf("unexpected provider exclude labels: %#v", provider.seen.ExcludeLabels)
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
			System:           "github",
			AuthStatus:       &AuthStatus{System: "github"},
			RepositoryStatus: &RepositoryStatus{System: "github"},
			IssueStatus:      &IssueStatus{System: "github"},
			Task: &CanonicalTask{
				System:    "github",
				Author:    User{System: "github"},
				Assignees: []User{{System: "github"}},
			},
			MergeRequest:  &MergeRequest{System: "github", Author: User{System: "github"}},
			TaskComments:  []TaskComment{{System: "github", Author: User{System: "github"}}},
			ReviewRemarks: []ReviewRemark{{System: "github", Author: User{System: "github"}}},
			Repository:    &Repository{System: "github"},
			Tasks: []CanonicalTask{{
				System: "github", Author: User{System: "github"}, Assignees: []User{{System: "github"}},
			}},
			Artifacts: []Artifact{{System: "github"}},
		},
	})

	result, err := service.Execute(context.Background(), Request{System: "enterprise", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if result.System != "enterprise" || result.AuthStatus.System != "enterprise" || result.RepositoryStatus.System != "enterprise" || result.IssueStatus.System != "enterprise" {
		t.Fatalf("expected top-level status systems to use route name, got %#v", result)
	}
	if result.Task == nil || result.Task.System != "enterprise" || result.Task.Author.System != "enterprise" || result.Task.Assignees[0].System != "enterprise" {
		t.Fatalf("expected task payload systems to use route name, got %#v", result.Task)
	}
	if result.MergeRequest == nil || result.MergeRequest.System != "enterprise" || result.MergeRequest.Author.System != "enterprise" {
		t.Fatalf("expected merge request payload systems to use route name, got %#v", result.MergeRequest)
	}
	if result.TaskComments[0].System != "enterprise" || result.TaskComments[0].Author.System != "enterprise" {
		t.Fatalf("expected comment payload systems to use route name, got %#v", result.TaskComments)
	}
	if result.ReviewRemarks[0].System != "enterprise" || result.ReviewRemarks[0].Author.System != "enterprise" {
		t.Fatalf("expected review payload systems to use route name, got %#v", result.ReviewRemarks)
	}
	if result.Repository == nil || result.Repository.System != "enterprise" {
		t.Fatalf("expected repository payload system to use route name, got %#v", result.Repository)
	}
	if result.Tasks[0].System != "enterprise" || result.Tasks[0].Author.System != "enterprise" || result.Tasks[0].Assignees[0].System != "enterprise" || result.Artifacts[0].System != "enterprise" {
		t.Fatalf("expected tasks and artifacts to use route name, got %#v %#v", result.Tasks, result.Artifacts)
	}
}

func TestDispatchReturnsErrorForMissingSystem(t *testing.T) {
	t.Parallel()

	service := newEmptyService(logging.New(io.Discard))
	_, err := service.resolveRoute(Request{Resource: "issue", Operation: "get"})
	if err == nil {
		t.Fatal("expected dispatch error")
	}
	if err.Error() != "invalid integration request: no default system configured for integration type \"issue\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteReturnsErrorForMissingSystem(t *testing.T) {
	t.Parallel()

	service := newEmptyService(logging.New(io.Discard))
	_, err := service.Execute(context.Background(), Request{Resource: "issue", Operation: "get"})
	if err == nil {
		t.Fatal("expected execute error")
	}
	if err.Error() != "invalid integration request: no default system configured for integration type \"issue\"" {
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
	if err.Error() != "integration system is not configured: gitlab" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewServiceRegistersGitHubProvider(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	route, err := service.resolveRoute(Request{System: "github", Resource: "auth", Operation: "status"})
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
	route, err := service.resolveRoute(Request{System: "github", Resource: "pr", Operation: "create"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if route.ExpectedResult != "integration-pull-request-status" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
}

func TestDispatchWikiPageUsesWikiResultContract(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{"wiki": "docs"},
		Systems: map[string]model.IntegrationSystemConfig{
			"docs": {Type: "confluence"},
		},
	})
	route, err := service.resolveRoute(Request{IntegrationType: "wiki", Resource: "page", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if route.System != "docs" {
		t.Fatalf("unexpected system: %q", route.System)
	}
	if route.ExpectedResult != "wiki-page" {
		t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
	}
}

func TestOperationsCatalogIncludesBuiltInGitHubOperation(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	operations := service.Operations(context.Background(), OperationFilter{System: "github", Name: "tracker.task.get"})

	if len(operations) != 1 {
		t.Fatalf("expected one operation, got %#v", operations)
	}
	operation := operations[0]
	if operation.Name != "issue.issue.get" || operation.IntegrationType != model.IntegrationTypeIssue {
		t.Fatalf("unexpected operation identity: %#v", operation)
	}
	if operation.System != "github" || operation.AdapterType != "github" || !operation.Enabled || !operation.Available {
		t.Fatalf("unexpected operation availability: %#v", operation)
	}
	if operation.Output.Shape != "CanonicalTask" {
		t.Fatalf("unexpected output shape: %q", operation.Output.Shape)
	}
	if len(operation.Input.Required) != 1 || operation.Input.Required[0].Name != "id" || operation.Input.Required[0].Type != "string" {
		t.Fatalf("unexpected required fields: %#v", operation.Input.Required)
	}
}

func TestOperationsCatalogDoesNotPublishTelegramThreadRead(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"telegram": {Type: "telegram"},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "telegram", Name: "messenger.thread.get"})

	if len(operations) != 0 {
		t.Fatalf("telegram must not publish thread read operation: %#v", operations)
	}

	messageOperations := service.Operations(context.Background(), OperationFilter{System: "telegram", Name: "messenger.message.create"})
	if len(messageOperations) != 1 || !messageOperations[0].Available {
		t.Fatalf("telegram message create operation must stay available: %#v", messageOperations)
	}
}

func TestOperationsCatalogPublishesExecutableGitHubTaskCommentsRead(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	operations := service.Operations(context.Background(), OperationFilter{System: "github", Name: "tracker.task.comment.list"})

	if len(operations) != 1 {
		t.Fatalf("expected one operation, got %#v", operations)
	}
	operation := operations[0]
	if operation.ObjectType != "issue.comment" || operation.Operation != "list" {
		t.Fatalf("task comment list operation must use executable GitHub route, got %#v", operation)
	}
	if operation.Output.Resource != "task-comment" || operation.Output.Shape != "TaskComment[]" {
		t.Fatalf("unexpected comment output contract: %#v", operation.Output)
	}
}

func TestOperationsCatalogPublishesGitHubTaskSearch(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	operations := service.Operations(context.Background(), OperationFilter{System: "github", Name: "tracker.task.search"})

	if len(operations) != 1 {
		t.Fatalf("expected one operation, got %#v", operations)
	}
	operation := operations[0]
	if operation.Output.Resource != "task" || operation.Output.Shape != "CanonicalTask[]" {
		t.Fatalf("unexpected search output contract: %#v", operation.Output)
	}
	optional := map[string]model.OperationField{}
	for _, field := range operation.Input.Optional {
		optional[field.Name] = field
	}
	for _, name := range []string{"labels", "exclude_labels"} {
		field, ok := optional[name]
		if !ok || field.Type != "string[]" || !field.Repeated {
			t.Fatalf("expected repeated string list field %q, got %#v", name, field)
		}
	}
}

func TestOperationsCatalogDoesNotPublishLocalTrackerExcludeLabels(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"local": {
				Type:            "local-tracker",
				IntegrationType: model.IntegrationTypeTracker,
				Database:        model.IntegrationDatabaseConfig{Driver: "sqlite", Path: filepath.Join(root, "tasks.sqlite")},
			},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "local", Name: "tracker.task.search"})

	if len(operations) != 1 {
		t.Fatalf("expected one operation, got %#v", operations)
	}
	for _, field := range operations[0].Input.Optional {
		if field.Name == "exclude_labels" {
			t.Fatalf("local tracker must not advertise unsupported exclude_labels field: %#v", operations[0].Input.Optional)
		}
	}
}

func TestOperationsCatalogMarksDisabledSystemUnavailable(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {Type: "github", Enabled: boolPtr(false)},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "github", Name: "tracker.task.get"})

	if len(operations) != 1 {
		t.Fatalf("expected one disabled operation, got %#v", operations)
	}
	if operations[0].Enabled || operations[0].Available {
		t.Fatalf("disabled system must not publish operation as available: %#v", operations[0])
	}
	if !contains(operations[0].Diagnostics, "system disabled by integration configuration") {
		t.Fatalf("expected disabled diagnostic, got %#v", operations[0].Diagnostics)
	}
}

func TestOperationsCatalogIncludesScriptOperationConfig(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"work-tracker": {
				Type:            "script",
				IntegrationType: model.IntegrationTypeTracker,
				Operations: map[string]model.IntegrationOperationConfig{
					"tracker.task.get": {
						Script:   ".progress/integration/work-tracker/task-get.sh",
						Required: []string{"number"},
						Optional: []string{"project"},
						Defaults: map[string]string{"project": "${system.project}"},
					},
				},
			},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "work-tracker"})

	if len(operations) != 1 {
		t.Fatalf("expected one script operation, got %#v", operations)
	}
	operation := operations[0]
	if operation.Name != "issue.issue.get" || operation.AdapterType != "script" {
		t.Fatalf("unexpected script operation: %#v", operation)
	}
	if !operation.Available {
		t.Fatalf("script operation must be available after adapter registration: %#v", operation)
	}
	if len(operation.Input.Required) != 1 || operation.Input.Required[0].Name != "id" {
		t.Fatalf("unexpected required fields: %#v", operation.Input.Required)
	}
	if len(operation.Input.Optional) != 1 || operation.Input.Optional[0].Name != "project" || operation.Input.Optional[0].Default != "${system.project}" {
		t.Fatalf("unexpected optional fields: %#v", operation.Input.Optional)
	}
	if operation.Output.Shape != "CanonicalTask" {
		t.Fatalf("unexpected script operation output shape: %q", operation.Output.Shape)
	}
	if !contains(operation.Diagnostics, "script=.progress/integration/work-tracker/task-get.sh") {
		t.Fatalf("expected script diagnostic, got %#v", operation.Diagnostics)
	}
}

func TestNewServiceFromConfigRegistersLocalTrackerProvider(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeTracker: "local"},
		Systems: map[string]model.IntegrationSystemConfig{
			"local": {
				Type:            "local-tracker",
				IntegrationType: model.IntegrationTypeTracker,
				Database:        model.IntegrationDatabaseConfig{Driver: "sqlite", Path: filepath.Join(root, "tasks.sqlite")},
			},
		},
	})

	operations := service.Operations(context.Background(), OperationFilter{System: "local", Name: "tracker.task.create"})
	if len(operations) != 1 || !operations[0].Available {
		t.Fatalf("expected available local tracker operation, got %#v", operations)
	}
	searchOperations := service.Operations(context.Background(), OperationFilter{System: "local", Name: "tracker.task.search"})
	if len(searchOperations) != 1 || searchOperations[0].Output.Shape != "CanonicalTask[]" {
		t.Fatalf("expected local tracker search result contract, got %#v", searchOperations)
	}
	searchRoute, err := service.resolveRoute(Request{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "local",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "search",
	})
	if err != nil {
		t.Fatalf("dispatch local tracker search: %v", err)
	}
	if searchRoute.ExpectedResult != "canonical-task[]" {
		t.Fatalf("expected local tracker search route contract, got %q", searchRoute.ExpectedResult)
	}

	result, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeTracker,
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "create",
		Title:           "Локальная задача",
	})
	if err != nil {
		t.Fatalf("execute local tracker provider: %v", err)
	}
	if result.Task == nil || result.Task.ID != "1" || result.Task.Title != "Локальная задача" {
		t.Fatalf("unexpected local tracker task: %#v", result.Task)
	}
	if result.Route.System != "local" {
		t.Fatalf("expected default local tracker route, got %#v", result.Route)
	}
}

func TestOperationsCatalogMarksScriptOperationWithoutExecutableUnavailable(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"work-tracker": {
				Type:            "script",
				IntegrationType: model.IntegrationTypeTracker,
				Operations: map[string]model.IntegrationOperationConfig{
					"tracker.task.get": {Required: []string{"number"}},
				},
			},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "work-tracker", Name: "tracker.task.get"})

	if len(operations) != 1 {
		t.Fatalf("expected one script operation, got %#v", operations)
	}
	if operations[0].Available {
		t.Fatalf("script operation without executable must be unavailable: %#v", operations[0])
	}
	if !contains(operations[0].Diagnostics, "script operation has no script, command or path") {
		t.Fatalf("expected missing executable diagnostic, got %#v", operations[0].Diagnostics)
	}
}

func TestOperationsCatalogMarksUnsupportedConfiguredOperationUnavailable(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"work-tracker": {
				Type: "script",
				Operations: map[string]model.IntegrationOperationConfig{
					"repository.merge-request.get": {Script: ".progress/integration/work-tracker/pr-get.sh"},
				},
			},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "work-tracker", Name: "repository.merge-request.get"})

	if len(operations) != 1 {
		t.Fatalf("expected one script operation, got %#v", operations)
	}
	if operations[0].Available {
		t.Fatalf("unsupported script operation must be unavailable: %#v", operations[0])
	}
	if !contains(operations[0].Diagnostics, "system does not support integration type=repo") {
		t.Fatalf("expected unsupported integration type diagnostic, got %#v", operations[0].Diagnostics)
	}
}

func TestOperationsCatalogUsesSearchResultContractForTaskSearch(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"work-tracker": {
				Type:            "script",
				IntegrationType: model.IntegrationTypeTracker,
				Operations: map[string]model.IntegrationOperationConfig{
					"tracker.task.search": {Script: ".progress/integration/work-tracker/task-search.sh"},
				},
			},
		},
	})
	operations := service.Operations(context.Background(), OperationFilter{System: "work-tracker", Name: "tracker.task.search"})

	if len(operations) != 1 {
		t.Fatalf("expected one script search operation, got %#v", operations)
	}
	if operations[0].Output.Shape != "CanonicalTask[]" {
		t.Fatalf("unexpected search output shape: %#v", operations[0].Output)
	}
	route, err := service.resolveRoute(Request{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "work-tracker",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "search",
	})
	if err != nil {
		t.Fatalf("dispatch task search: %v", err)
	}
	if route.ExpectedResult != "canonical-task[]" {
		t.Fatalf("unexpected task search result contract: %q", route.ExpectedResult)
	}
}

func TestNewServiceFromConfigRegistersScriptProvider(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptPath := filepath.Join(root, "task-get.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '%s\\n' '{\"status\":\"ok\",\"task\":{\"system\":\"work-tracker\",\"number\":7,\"title\":\"Script task\",\"state\":\"open\"}}'\n"), 0o755); err != nil {
		t.Fatalf("write script fixture: %v", err)
	}

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeTracker: "work-tracker"},
		Systems: map[string]model.IntegrationSystemConfig{
			"work-tracker": {
				Type:            "script",
				IntegrationType: model.IntegrationTypeTracker,
				Path:            root,
				Operations: map[string]model.IntegrationOperationConfig{
					"tracker.task.get": {Script: "task-get.sh", Required: []string{"number"}},
				},
			},
		},
	})

	operations := service.Operations(context.Background(), OperationFilter{System: "work-tracker", Name: "tracker.task.get"})
	if len(operations) != 1 || !operations[0].Available {
		t.Fatalf("expected available script operation, got %#v", operations)
	}

	result, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeTracker,
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "get",
		ID:              "7",
	})
	if err != nil {
		t.Fatalf("execute script provider: %v", err)
	}
	if result.Task == nil || result.Task.ID != "7" || result.Task.Title != "Script task" {
		t.Fatalf("unexpected script task: %#v", result.Task)
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

func TestExecutePreservesOpaqueIssueIdentifierAndUsesDefaultSystem(t *testing.T) {
	t.Parallel()

	service := NewService(logging.New(io.Discard))
	provider := &capturingProvider{}
	service.RegisterProvider("github", provider)

	for _, identifier := range []string{"123", "ABC-123"} {
		_, err := service.Execute(context.Background(), Request{
			IntegrationType: model.IntegrationTypeIssue,
			Resource:        "issue",
			ObjectType:      "issue",
			Operation:       "get",
			ID:              identifier,
		})
		if err != nil {
			t.Fatalf("execute issue %q: %v", identifier, err)
		}
		if provider.seen.System != "github" || provider.seen.ID != identifier || provider.seen.ExternalID != identifier {
			t.Fatalf("unexpected normalized request for %q: %#v", identifier, provider.seen)
		}
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
