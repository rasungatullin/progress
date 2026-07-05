package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAPITransportUsesGitHubAppInstallationToken(t *testing.T) {
	t.Parallel()

	keyPEM := testRSAPrivateKeyPEM(t)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	var seenJWT string
	var seenRepoAuth string
	tokenRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/144549701/access_tokens":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected token method: %s", r.Method)
			}
			seenJWT = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			tokenRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":                "installation-secret",
				"expires_at":           now.Add(time.Hour).Format(time.RFC3339),
				"repository_selection": "selected",
				"permissions":          map[string]string{"contents": "read", "pull_requests": "write"},
			})
		case "/repos/owner/name/issues/123":
			seenRepoAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   123,
				"title":    "GitHub App issue",
				"state":    "open",
				"html_url": "https://github.com/owner/name/issues/123",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			BaseURL:                     server.URL,
			Repository:                  "owner/name",
			GitHubAppID:                 "12345",
			GitHubAppInstallationID:     "144549701",
			GitHubAppPrivateKeyPath:     "/keys/progress-synthesis.pem",
			GitHubAppTokenRefreshBefore: "5m",
		},
		client: server.Client(),
		readFile: func(path string) ([]byte, error) {
			if path != "/keys/progress-synthesis.pem" {
				t.Fatalf("unexpected private key path: %s", path)
			}
			return keyPEM, nil
		},
		now: func() time.Time { return now },
	}

	authResult, _, err := runner.RunAuthStatus(context.Background())
	if err != nil {
		t.Fatalf("auth status through GitHub App: %v", err)
	}
	if strings.Contains(authResult.Stdout, "installation-secret") {
		t.Fatalf("auth status must not expose installation token: %s", authResult.Stdout)
	}
	assertGitHubAppJWT(t, seenJWT, "12345", now)

	result, _, err := runner.RunIssueView(context.Background(), "", 123)
	if err != nil {
		t.Fatalf("execute issue get through GitHub App token: %v", err)
	}
	if tokenRequests != 1 {
		t.Fatalf("expected cached token after auth status, got token requests: %d", tokenRequests)
	}
	if seenRepoAuth != "Bearer installation-secret" {
		t.Fatalf("unexpected repository auth header: %q", seenRepoAuth)
	}
	var issue ghIssueView
	if err := json.Unmarshal([]byte(result.Stdout), &issue); err != nil {
		t.Fatalf("decode issue stdout: %v", err)
	}
	if issue.Title != "GitHub App issue" {
		t.Fatalf("unexpected issue: %#v", issue)
	}
}

func TestGitHubAppInstallationTokenCacheRefreshesBeforeExpiry(t *testing.T) {
	t.Parallel()

	keyPEM := testRSAPrivateKeyPEM(t)
	baseTime := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	currentTime := baseTime
	tokenRequests := 0
	var seenAuth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			tokenRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      fmt.Sprintf("installation-secret-%d", tokenRequests),
				"expires_at": baseTime.Add(time.Hour).Format(time.RFC3339),
			})
		case "/repos/owner/name/issues/123":
			seenAuth = append(seenAuth, r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 123, "title": "cached", "state": "open"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			BaseURL:                 server.URL,
			Repository:              "owner/name",
			GitHubAppClientID:       "Iv1.client",
			GitHubAppInstallationID: "42",
			GitHubAppPrivateKey:     string(keyPEM),
		},
		client: server.Client(),
		now:    func() time.Time { return currentTime },
	}

	if _, _, err := runner.RunIssueView(context.Background(), "", 123); err != nil {
		t.Fatalf("first issue get: %v", err)
	}
	currentTime = baseTime.Add(30 * time.Minute)
	if _, _, err := runner.RunIssueView(context.Background(), "", 123); err != nil {
		t.Fatalf("second issue get: %v", err)
	}
	currentTime = baseTime.Add(56 * time.Minute)
	if _, _, err := runner.RunIssueView(context.Background(), "", 123); err != nil {
		t.Fatalf("third issue get: %v", err)
	}
	if tokenRequests != 2 {
		t.Fatalf("expected one cached use and one refresh, got token requests: %d", tokenRequests)
	}
	if len(seenAuth) != 3 || seenAuth[0] != "Bearer installation-secret-1" || seenAuth[1] != "Bearer installation-secret-1" || seenAuth[2] != "Bearer installation-secret-2" {
		t.Fatalf("unexpected auth headers: %#v", seenAuth)
	}
}

func TestGitHubAppAuthRequiresCompleteSettings(t *testing.T) {
	t.Parallel()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			BaseURL:                 "https://api.invalid",
			Repository:              "owner/name",
			GitHubAppInstallationID: "42",
			GitHubAppPrivateKey:     string(testRSAPrivateKeyPEM(t)),
		},
	}

	_, _, err := runner.RunIssueView(context.Background(), "", 123)
	var ghErr *Error
	if !errors.As(err, &ghErr) || ghErr.Code != ErrorCodeAuthRequired {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestGitHubAppIssuerPrefersAppIDWhenBothIDsAreConfigured(t *testing.T) {
	t.Parallel()

	runner := &APIRunner{
		systemConfig: model.IntegrationSystemConfig{
			GitHubAppID:       "4221694",
			GitHubAppClientID: "Iv23liRLhoM9JEx89zu",
		},
	}

	config, err := runner.resolveBaseConfig()
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if config.GitHubAppIssuer != "4221694" {
		t.Fatalf("unexpected issuer: %q", config.GitHubAppIssuer)
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
	if response.Failure == nil || response.Failure.Kind != model.FailureKindNotFound {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}

func TestAPITransportPRGetEnrichesLabelsAndReviewDecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/name/pulls/7":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":     7,
				"title":      "API PR",
				"body":       "Body",
				"state":      "open",
				"html_url":   "https://github.com/owner/name/pull/7",
				"created_at": "2026-07-03T07:00:00Z",
				"updated_at": "2026-07-03T07:01:00Z",
				"user":       map[string]string{"login": "alice"},
				"base":       map[string]string{"ref": "main"},
				"head":       map[string]string{"ref": "feature"},
			})
		case "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"reviewDecision": "APPROVED",
							"labels": map[string]any{
								"nodes": []map[string]string{{"name": "backend"}},
							},
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
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
		Resource:   "pr",
		ObjectType: "pr",
		Operation:  "get",
		Number:     7,
	})
	if err != nil {
		t.Fatalf("execute pr get through api transport: %v", err)
	}
	if response.PullRequest == nil || response.PullRequest.ReviewDecision != "APPROVED" || len(response.PullRequest.Labels) != 1 || response.PullRequest.Labels[0] != "backend" {
		t.Fatalf("unexpected pull request response: %#v", response.PullRequest)
	}
	if response.MergeRequest == nil || response.MergeRequest.ReviewDecision != "APPROVED" || len(response.MergeRequest.Traits) != 1 || response.MergeRequest.Traits[0] != "backend" {
		t.Fatalf("unexpected merge request response: %#v", response.MergeRequest)
	}
}

func TestAPITransportPRGetPreservesHTTPFailureKind(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible"}`))
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
		Resource:   "pr",
		ObjectType: "pr",
		Operation:  "get",
		Number:     7,
	})
	if err == nil {
		t.Fatal("expected permission error")
	}
	if response.PullRequestStatus == nil || response.PullRequestStatus.State != model.FailureKindPermissionDenied {
		t.Fatalf("unexpected pull request status: %#v", response.PullRequestStatus)
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindPermissionDenied {
		t.Fatalf("unexpected failure: %#v", response.Failure)
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

func TestAPITransportPRSearchSupportsHeadQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"reviewDecision": "APPROVED",
							"labels":         map[string]any{"nodes": []map[string]string{}},
						},
					},
				},
			})
			return
		}
		if r.URL.Path != "/repos/owner/name/pulls" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("head"); got != "owner:119" {
			t.Fatalf("unexpected head query: %q", got)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Fatalf("unexpected state query: %q", got)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"number":     120,
			"title":      "PR",
			"state":      "open",
			"html_url":   "https://github.com/owner/name/pull/120",
			"base":       map[string]string{"ref": "main"},
			"head":       map[string]string{"ref": "119"},
			"created_at": "2026-07-05T12:00:00Z",
			"updated_at": "2026-07-05T12:00:00Z",
			"user":       map[string]string{"login": "alice"},
		}})
	}))
	defer server.Close()

	service := NewServiceWithConfig(model.IntegrationSystemConfig{
		Transport:  "api",
		BaseURL:    server.URL,
		Token:      "secret",
		Repository: "owner/name",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "github",
		Resource:        "pr",
		ObjectType:      "merge-request",
		Operation:       "search",
		Query:           "head:119",
		State:           "open",
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("execute pr search through api transport: %v", err)
	}
	if len(response.MergeRequests) != 1 || response.MergeRequests[0].Number != 120 || response.MergeRequests[0].HeadRef != "119" {
		t.Fatalf("unexpected merge requests: %#v", response.MergeRequests)
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
		switch r.URL.Path {
		case "/repos/owner/name/pulls":
		case "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"reviewDecision": "REVIEW_REQUIRED",
							"labels":         map[string]any{"nodes": []map[string]string{{"name": "release"}}},
						},
					},
				},
			})
			return
		default:
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
	if pulls[0].State != "merged" {
		t.Fatalf("merged pull request must be exposed as merged, got %q", pulls[0].State)
	}
	if pulls[0].ReviewDecision != "REVIEW_REQUIRED" || len(pulls[0].Labels) != 1 || pulls[0].Labels[0].Name != "release" {
		t.Fatalf("expected enriched pull request metadata, got %#v", pulls[0])
	}
}

func TestAPITransportFiltersMergedPullRequestsFromClosedState(t *testing.T) {
	t.Parallel()

	var seenState string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/name/pulls":
		case "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"reviewDecision": "",
							"labels":         map[string]any{"nodes": []map[string]string{}},
						},
					},
				},
			})
			return
		default:
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

	result, _, err := runner.RunPRList(context.Background(), "", PRListRequest{State: "closed", Limit: 10})
	if err != nil {
		t.Fatalf("list closed pull requests: %v", err)
	}
	if seenState != "closed" {
		t.Fatalf("closed state must be requested from REST as closed, got %q", seenState)
	}
	var pulls []ghPRView
	if err := json.Unmarshal([]byte(result.Stdout), &pulls); err != nil {
		t.Fatalf("decode pulls: %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 1 {
		t.Fatalf("unexpected pulls: %#v", pulls)
	}
	if pulls[0].State != "closed" {
		t.Fatalf("closed pull request must be exposed as closed, got %q", pulls[0].State)
	}
}

func TestAPITransportPRCreateMapsValidationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           map[string]any
		expectedCode   string
		expectedState  string
		expectedURL    string
		expectedNumber int
	}{
		{
			name: "already exists",
			body: map[string]any{
				"message": "Validation Failed",
				"errors": []map[string]string{
					{"message": "A pull request already exists for branch feature into branch main: https://github.com/owner/name/pull/15"},
				},
			},
			expectedCode:   ErrorCodeAlreadyExists,
			expectedState:  ErrorCodeAlreadyExists,
			expectedURL:    "https://github.com/owner/name/pull/15",
			expectedNumber: 15,
		},
		{
			name: "branch not found",
			body: map[string]any{
				"message": "Validation Failed",
				"errors": []map[string]string{
					{"message": "Head ref must be a branch"},
				},
			},
			expectedCode:  ErrorCodeNotFound,
			expectedState: ErrorCodeNotFound,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/name/pulls" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(tt.body)
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
				Resource:   "pr",
				ObjectType: "pr",
				Operation:  "create",
				Repository: "owner/name",
				Base:       "main",
				Head:       "feature",
				Title:      "Title",
				Body:       "Body",
			})
			assertGitHubErrorCode(t, err, tt.expectedCode)
			if response.PullRequestStatus == nil {
				t.Fatal("expected pull request status")
			}
			if response.PullRequestStatus.State != tt.expectedState {
				t.Fatalf("unexpected state: %q", response.PullRequestStatus.State)
			}
			if response.PullRequestStatus.URL != tt.expectedURL {
				t.Fatalf("unexpected url: %q", response.PullRequestStatus.URL)
			}
			if response.PullRequestStatus.Number != tt.expectedNumber {
				t.Fatalf("unexpected number: %d", response.PullRequestStatus.Number)
			}
		})
	}
}

func testRSAPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func assertGitHubAppJWT(t *testing.T, token string, expectedIssuer string, now time.Time) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected jwt parts: %q", token)
	}
	var header map[string]string
	decodeJWTPart(t, parts[0], &header)
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Fatalf("unexpected jwt header: %#v", header)
	}
	var payload map[string]any
	decodeJWTPart(t, parts[1], &payload)
	if payload["iss"] != expectedIssuer {
		t.Fatalf("unexpected issuer: %#v", payload)
	}
	if int64(payload["iat"].(float64)) != now.Add(-time.Minute).Unix() {
		t.Fatalf("unexpected iat: %#v", payload)
	}
	if int64(payload["exp"].(float64)) != now.Add(9*time.Minute).Unix() {
		t.Fatalf("unexpected exp: %#v", payload)
	}
}

func decodeJWTPart(t *testing.T, part string, out any) {
	t.Helper()
	content, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		t.Fatalf("decode jwt part: %v", err)
	}
	if err := json.Unmarshal(content, out); err != nil {
		t.Fatalf("decode jwt json: %v", err)
	}
}
