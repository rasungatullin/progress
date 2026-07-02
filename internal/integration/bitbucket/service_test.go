package bitbucket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServicePullRequestCreateNormalizesResponse(t *testing.T) {
	t.Parallel()

	var seenPath string
	var seenAuth string
	var seenPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&seenPayload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": 17,
			"title": "Add integration",
			"description": "Body",
			"state": "OPEN",
			"created_on": "2026-06-01T10:00:00Z",
			"updated_on": "2026-06-01T11:00:00Z",
			"author": {"display_name": "Alice", "nickname": "alice", "links": {"html": {"href": "https://bitbucket.example/alice"}}},
			"source": {"branch": {"name": "feature"}},
			"destination": {"branch": {"name": "main"}},
			"links": {"html": {"href": "https://bitbucket.example/workspace/repo/pull-requests/17"}}
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:    server.URL,
		Token:      "token",
		Repository: "workspace/repo",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "create",
		Base:            "main",
		Head:            "feature",
		Title:           "Add integration",
		Body:            "Body",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenPath != "/repositories/workspace/repo/pullrequests" {
		t.Fatalf("unexpected path: %q", seenPath)
	}
	if seenAuth != "Bearer token" {
		t.Fatalf("unexpected authorization header: %q", seenAuth)
	}
	if seenPayload["title"] != "Add integration" {
		t.Fatalf("unexpected title payload: %#v", seenPayload)
	}
	if response.MergeRequest == nil {
		t.Fatal("expected merge request")
	}
	if response.MergeRequest.Number != 17 {
		t.Fatalf("unexpected merge request number: %d", response.MergeRequest.Number)
	}
	if response.MergeRequest.Repository != "workspace/repo" {
		t.Fatalf("unexpected repository: %q", response.MergeRequest.Repository)
	}
	if response.MergeRequest.BaseRef != "main" || response.MergeRequest.HeadRef != "feature" {
		t.Fatalf("unexpected refs: %#v", response.MergeRequest)
	}
	if response.OperationResult == nil || response.OperationResult.Status != model.ResponseStatusOK {
		t.Fatalf("unexpected operation result: %#v", response.OperationResult)
	}
}

func TestServiceServerRepositoryGetNormalizesResponse(t *testing.T) {
	t.Parallel()

	var seenPaths []string
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths = append(seenPaths, r.URL.String())
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/1.0/projects/PROJ/repos/repo":
			_, _ = w.Write([]byte(`{
				"id": 42,
				"slug": "repo",
				"name": "Repository",
				"description": "Repository description",
				"scmId": "git",
				"state": "AVAILABLE",
				"public": false,
				"project": {"key": "PROJ", "name": "Project"},
				"links": {"self": [{"href": "https://stash.example/projects/PROJ/repos/repo/browse"}]}
			}`))
		case "/rest/api/1.0/projects/PROJ/repos/repo/branches/default":
			_, _ = w.Write([]byte(`{
				"id": "refs/heads/main",
				"displayId": "main",
				"isDefault": true,
				"type": "BRANCH"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:    server.URL,
		APIVariant: "server",
		Token:      "token",
		Project:    "PROJ",
		Repository: "repo",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        "repository",
		ObjectType:      "repository",
		Operation:       "get",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenAuth != "Bearer token" {
		t.Fatalf("unexpected authorization header: %q", seenAuth)
	}
	if len(seenPaths) != 2 {
		t.Fatalf("unexpected paths: %#v", seenPaths)
	}
	if seenPaths[0] != "/rest/api/1.0/projects/PROJ/repos/repo" {
		t.Fatalf("unexpected repository path: %q", seenPaths[0])
	}
	if seenPaths[1] != "/rest/api/1.0/projects/PROJ/repos/repo/branches/default" {
		t.Fatalf("unexpected branch path: %q", seenPaths[1])
	}
	if response.Repository == nil {
		t.Fatal("expected repository")
	}
	if response.Repository.FullName != "PROJ/repo" {
		t.Fatalf("unexpected full name: %q", response.Repository.FullName)
	}
	if response.Repository.DefaultBranch != "main" {
		t.Fatalf("unexpected default branch: %q", response.Repository.DefaultBranch)
	}
	if response.Repository.Attributes["api_variant"] != "server" {
		t.Fatalf("unexpected attributes: %#v", response.Repository.Attributes)
	}
}

func TestServiceServerPullRequestGetNormalizesResponse(t *testing.T) {
	t.Parallel()

	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 5,
			"version": 3,
			"title": "Add server adapter",
			"description": "Body",
			"state": "OPEN",
			"createdDate": 1780308000000,
			"updatedDate": 1780311600000,
			"author": {"user": {"name": "alice", "displayName": "Alice", "emailAddress": "alice@example.test", "active": true}},
			"fromRef": {"id": "refs/heads/feature", "displayId": "feature"},
			"toRef": {"id": "refs/heads/main", "displayId": "main"},
			"links": {"self": [{"href": "https://stash.example/projects/PROJ/repos/repo/pull-requests/5"}]}
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:    server.URL,
		APIVariant: "server",
		Token:      "token",
		Repository: "PROJ/repo",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "get",
		Number:          5,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenPath != "/rest/api/1.0/projects/PROJ/repos/repo/pull-requests/5" {
		t.Fatalf("unexpected path: %q", seenPath)
	}
	if response.MergeRequest == nil {
		t.Fatal("expected merge request")
	}
	if response.MergeRequest.Repository != "PROJ/repo" {
		t.Fatalf("unexpected repository: %q", response.MergeRequest.Repository)
	}
	if response.MergeRequest.BaseRef != "main" || response.MergeRequest.HeadRef != "feature" {
		t.Fatalf("unexpected refs: %#v", response.MergeRequest)
	}
	if response.MergeRequest.Author.Login != "alice" {
		t.Fatalf("unexpected author: %#v", response.MergeRequest.Author)
	}
	if response.MergeRequest.Attributes["api_variant"] != "server" {
		t.Fatalf("unexpected attributes: %#v", response.MergeRequest.Attributes)
	}
}

func TestServiceServerPullRequestCommentsNormalizesActivities(t *testing.T) {
	t.Parallel()

	var seenPath string
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"values": [{
				"id": 100,
				"comment": {
					"id": 7,
					"text": "Fix this",
					"author": {"name": "alice", "displayName": "Alice", "active": true},
					"createdDate": 1780308000000,
					"updatedDate": 1780311600000,
					"anchor": {"path": "internal/integration/bitbucket/service.go", "line": 42, "lineType": "ADDED"},
					"comments": [{
						"id": 8,
						"text": "Done",
						"author": {"name": "bob", "displayName": "Bob", "active": true}
					}]
				}
			}]
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:    server.URL,
		APIVariant: "server",
		Token:      "token",
		Repository: "PROJ/repo",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "comments",
		Number:          5,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenPath != "/rest/api/1.0/projects/PROJ/repos/repo/pull-requests/5/activities" {
		t.Fatalf("unexpected path: %q", seenPath)
	}
	if seenQuery != "limit=100" {
		t.Fatalf("unexpected query: %q", seenQuery)
	}
	if len(response.ReviewRemarks) != 2 {
		t.Fatalf("unexpected remarks: %#v", response.ReviewRemarks)
	}
	if response.ReviewRemarks[0].Path != "internal/integration/bitbucket/service.go" || response.ReviewRemarks[0].Line != 42 {
		t.Fatalf("unexpected anchor: %#v", response.ReviewRemarks[0])
	}
	if response.ReviewRemarks[1].Author.Login != "bob" {
		t.Fatalf("unexpected reply author: %#v", response.ReviewRemarks[1])
	}
}

func TestServiceCloudPullRequestCommentsFollowsPagination(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	var seenPaths []string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths = append(seenPaths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "2":
			_, _ = w.Write([]byte(`{
				"values": [{
					"id": 8,
					"content": {"raw": "Second page"},
					"user": {"display_name": "Bob", "nickname": "bob"},
					"links": {"html": {"href": "https://bitbucket.example/workspace/repo/pull-requests/5/_/diff#comment-8"}}
				}]
			}`))
		default:
			_, _ = w.Write([]byte(`{
				"next": "` + server.URL + `/repositories/workspace/repo/pullrequests/5/comments?page=2",
				"values": [{
					"id": 7,
					"content": {"raw": "First page"},
					"user": {"display_name": "Alice", "nickname": "alice"},
					"inline": {"path": "file.go", "to": 10},
					"links": {"html": {"href": "https://bitbucket.example/workspace/repo/pull-requests/5/_/diff#comment-7"}}
				}]
			}`))
		}
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:    server.URL,
		Token:      "token",
		Repository: "workspace/repo",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "comments",
		Number:          5,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(seenPaths) != 2 {
		t.Fatalf("expected two paginated requests, got %#v", seenPaths)
	}
	if len(response.ReviewRemarks) != 2 {
		t.Fatalf("unexpected remarks: %#v", response.ReviewRemarks)
	}
	if response.ReviewRemarks[0].Path != "file.go" || response.ReviewRemarks[0].Line != 10 {
		t.Fatalf("unexpected inline location: %#v", response.ReviewRemarks[0])
	}
	if response.ReviewRemarks[1].Body != "Second page" {
		t.Fatalf("unexpected second remark: %#v", response.ReviewRemarks[1])
	}
}

func TestServiceCloudPullRequestListDefaultsToClosed(t *testing.T) {
	t.Parallel()

	var seenStates []string
	var seenPagelen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenStates = r.URL.Query()["state"]
		seenPagelen = r.URL.Query().Get("pagelen")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"values": [{
				"id": 5,
				"title": "Merged PR",
				"description": "Body",
				"state": "MERGED",
				"updated_on": "2026-06-01T11:00:00Z",
				"author": {"display_name": "Alice", "nickname": "alice"},
				"source": {"branch": {"name": "feature"}},
				"destination": {"branch": {"name": "main"}},
				"links": {"html": {"href": "https://bitbucket.example/workspace/repo/pull-requests/5"}}
			}]
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:    server.URL,
		Token:      "token",
		Repository: "workspace/repo",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeRepository,
		System:          "bitbucket",
		Resource:        "merge-request",
		ObjectType:      "merge-request",
		Operation:       "search",
		Limit:           2,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenPagelen != "2" {
		t.Fatalf("unexpected pagelen: %q", seenPagelen)
	}
	if len(seenStates) != 2 || seenStates[0] != "MERGED" || seenStates[1] != "DECLINED" {
		t.Fatalf("unexpected states: %#v", seenStates)
	}
	if len(response.MergeRequests) != 1 {
		t.Fatalf("unexpected merge requests: %#v", response.MergeRequests)
	}
	if response.MergeRequests[0].State != "MERGED" {
		t.Fatalf("unexpected state: %#v", response.MergeRequests[0])
	}
}
