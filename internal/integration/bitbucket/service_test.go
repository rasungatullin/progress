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
