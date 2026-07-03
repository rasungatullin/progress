package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceUsesAPITransportForIssueGet(t *testing.T) {
	t.Parallel()

	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/repos/owner/name/issues/123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":     123,
			"title":      "API issue",
			"body":       "Body",
			"state":      "open",
			"html_url":   "https://github.com/owner/name/issues/123",
			"created_at": "2026-07-03T07:00:00Z",
			"updated_at": "2026-07-03T07:01:00Z",
			"labels":     []map[string]string{{"name": "bug"}},
			"user":       map[string]string{"login": "alice", "html_url": "https://github.com/alice"},
		})
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    server.URL,
		Token:      "secret",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "github",
		Resource:   "issue",
		ObjectType: "issue",
		Operation:  "get",
		Number:     123,
	})
	if err != nil {
		t.Fatalf("execute issue get through api transport: %v", err)
	}
	if seenAuth != "Bearer secret" {
		t.Fatalf("unexpected auth header: %q", seenAuth)
	}
	if response.Issue == nil || response.Issue.Title != "API issue" || response.Issue.Labels[0] != "bug" {
		t.Fatalf("unexpected issue response: %#v", response.Issue)
	}
	if response.Task == nil || response.Task.Title != "API issue" {
		t.Fatalf("expected compatible canonical task: %#v", response.Task)
	}
}

func TestAPITransportRequiresToken(t *testing.T) {
	t.Parallel()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    "https://api.invalid",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "github",
		Resource:   "issue",
		ObjectType: "issue",
		Operation:  "get",
		Number:     123,
	})
	if err == nil {
		t.Fatal("expected token error")
	}
	if response.IssueStatus == nil || response.IssueStatus.State != ErrorCodeAuthRequired {
		t.Fatalf("unexpected issue status: %#v", response.IssueStatus)
	}
}

func TestAPITransportUsesTokenEnv(t *testing.T) {
	t.Parallel()

	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/repos/owner/name/issues/123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   123,
			"title":    "API issue",
			"state":    "open",
			"html_url": "https://github.com/owner/name/issues/123",
		})
	}))
	defer server.Close()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			BaseURL:    server.URL,
			TokenEnv:   "GITHUB_TOKEN",
			Repository: "owner/name",
		},
		client: server.Client(),
		getenv: func(name string) string {
			if name == "GITHUB_TOKEN" {
				return "env-secret"
			}
			return ""
		},
	}

	result, _, err := runner.RunIssueView(context.Background(), "", 123)
	if err != nil {
		t.Fatalf("execute issue get through token_env: %v", err)
	}
	if seenAuth != "Bearer env-secret" {
		t.Fatalf("unexpected auth header: %q", seenAuth)
	}
	var issue ghIssueView
	if err := json.Unmarshal([]byte(result.Stdout), &issue); err != nil {
		t.Fatalf("decode issue stdout: %v", err)
	}
	if issue.Number != 123 || issue.Title != "API issue" {
		t.Fatalf("unexpected issue payload: %#v", issue)
	}
}

func TestAPITransportMapsNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    server.URL,
		Token:      "secret",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "github",
		Resource:   "issue",
		ObjectType: "issue",
		Operation:  "get",
		Number:     123,
	})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Code != ErrorCodeNotFound {
		t.Fatalf("unexpected error: %#v", err)
	}
	if response.IssueStatus == nil || response.IssueStatus.State != ErrorCodeNotFound {
		t.Fatalf("unexpected issue status: %#v", response.IssueStatus)
	}
}

func TestAPITransportMapsRateLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    server.URL,
		Token:      "secret",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "github",
		Resource:   "issue",
		ObjectType: "issue",
		Operation:  "get",
		Number:     123,
	})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Code != ErrorCodeTemporaryUnavailable {
		t.Fatalf("unexpected error: %#v", err)
	}
	if response.IssueStatus == nil || response.IssueStatus.State != model.FailureKindTemporaryUnavailable {
		t.Fatalf("unexpected issue status: %#v", response.IssueStatus)
	}
}

func TestAPITransportMapsTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 123, "title": "late"})
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    server.URL,
		Token:      "secret",
		Repository: "owner/name",
		Timeout:    "1ms",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "github",
		Resource:   "issue",
		ObjectType: "issue",
		Operation:  "get",
		Number:     123,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Code != ErrorCodeTimeout {
		t.Fatalf("unexpected error: %#v", err)
	}
	if response.IssueStatus == nil || response.IssueStatus.State != StateTimeout {
		t.Fatalf("unexpected issue status: %#v", response.IssueStatus)
	}
}

func TestAPITransportOperationResultUsesHTTPMethod(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/name/issues/123/comments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         10,
			"body":       "created",
			"html_url":   "https://github.com/owner/name/issues/123#issuecomment-10",
			"created_at": "2026-07-03T07:00:00Z",
			"updated_at": "2026-07-03T07:00:00Z",
			"user":       map[string]string{"login": "alice"},
		})
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    server.URL,
		Token:      "secret",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeTracker,
		System:          "github",
		Resource:        "comment",
		ObjectType:      "comment",
		Operation:       "create",
		Number:          123,
		Text:            "created",
	})
	if err != nil {
		t.Fatalf("execute issue comment create through api transport: %v", err)
	}
	if response.OperationResult == nil || response.OperationResult.Method != "http" {
		t.Fatalf("unexpected operation result: %#v", response.OperationResult)
	}
}

func TestAPITransportReportsUnsupportedPRSearch(t *testing.T) {
	t.Parallel()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		Token:      "secret",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "pr",
		ObjectType:      "merge-request",
		Operation:       "search",
		Query:           "label:bug",
	})
	if err == nil {
		t.Fatal("expected unsupported operation error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindUnsupportedOperation {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}

func TestAPITransportPaginatesIssueComments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/name/issues/123/comments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			comments := make([]map[string]any, 100)
			for i := range comments {
				comments[i] = map[string]any{"id": i + 1, "body": fmt.Sprintf("page one %d", i+1)}
			}
			_ = json.NewEncoder(w).Encode(comments)
		case "2":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 101, "body": "page two"}})
		default:
			t.Fatalf("unexpected page: %q", page)
		}
	}))
	defer server.Close()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			BaseURL:    server.URL,
			Token:      "secret",
			Repository: "owner/name",
		},
		client: server.Client(),
		getenv: func(string) string { return "" },
	}

	result, _, err := runner.RunIssueComments(context.Background(), "", 123)
	if err != nil {
		t.Fatalf("list issue comments: %v", err)
	}
	var comments []ghIssueComment
	if err := json.Unmarshal([]byte(result.Stdout), &comments); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	if len(comments) != 101 || comments[100].Body != "page two" {
		t.Fatalf("unexpected comments: len=%d last=%#v", len(comments), comments[len(comments)-1])
	}
}

func TestAPITransportMapsGraphQLErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]string{{"message": "Could not resolve to a node with the global id"}},
		})
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport: "api",
		BaseURL:   server.URL,
		Token:     "secret",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "comment",
		ObjectType:      "comment",
		Operation:       "resolve",
		ThreadID:        "thread-id",
	})
	if err == nil {
		t.Fatal("expected graphql error")
	}
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Code != ErrorCodeNotFound {
		t.Fatalf("unexpected error: %#v", err)
	}
	if response.OperationResult != nil || response.Status == model.ResponseStatusOK {
		t.Fatalf("graphql errors must not produce successful operation result: %#v", response.OperationResult)
	}
}

func TestAPITransportDoesNotSendMergedAsRESTState(t *testing.T) {
	t.Parallel()

	var seenState string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/name/pulls" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seenState = r.URL.Query().Get("state")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":    1,
				"title":     "closed",
				"state":     "closed",
				"html_url":  "https://github.com/owner/name/pull/1",
				"merged_at": "",
			},
			{
				"number":    2,
				"title":     "merged",
				"state":     "closed",
				"html_url":  "https://github.com/owner/name/pull/2",
				"merged_at": "2026-07-03T08:00:00Z",
			},
		})
	}))
	defer server.Close()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			BaseURL:    server.URL,
			Token:      "secret",
			Repository: "owner/name",
		},
		client: server.Client(),
		getenv: func(string) string { return "" },
	}

	result, _, err := runner.RunPRList(context.Background(), "", PRListRequest{State: "merged", Limit: 10})
	if err != nil {
		t.Fatalf("list merged pull requests: %v", err)
	}
	if seenState != "closed" {
		t.Fatalf("merged state must be requested from REST as closed, got %q", seenState)
	}
	var pulls []ghPRView
	if err := json.Unmarshal([]byte(result.Stdout), &pulls); err != nil {
		t.Fatalf("decode pulls: %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 2 {
		t.Fatalf("unexpected pulls: %#v", pulls)
	}
}
