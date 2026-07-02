package confluence

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServicePageGetSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/confluence/rest/api/content/123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("expand") != "space,body.storage,version,history" {
			t.Fatalf("unexpected expand: %q", r.URL.Query().Get("expand"))
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "123",
			"type": "page",
			"status": "current",
			"title": "Architecture",
			"space": {"key": "ENG", "name": "Engineering"},
			"body": {"storage": {"value": "<p>Body</p>", "representation": "storage"}},
			"version": {"number": 7, "when": "2026-06-01T10:00:00.000+0300", "by": {"username": "alice", "displayName": "Alice"}},
			"history": {"createdDate": "2026-05-01T10:00:00.000+0300"},
			"_links": {"base": "` + serverBasePlaceholder + `", "webui": "/display/ENG/Architecture"}
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{BaseURL: server.URL + "/confluence", Token: "token"})
	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeWiki,
		System:          "confluence",
		Resource:        "page",
		ObjectType:      "page",
		Operation:       "get",
		ExternalID:      "123",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.WikiPage == nil {
		t.Fatal("expected wiki page")
	}
	if response.WikiPage.ExternalID != "123" || response.WikiPage.Title != "Architecture" {
		t.Fatalf("unexpected page: %#v", response.WikiPage)
	}
	if response.WikiPage.Space != "ENG" {
		t.Fatalf("unexpected space: %q", response.WikiPage.Space)
	}
	if response.WikiPage.Body != "<p>Body</p>" || response.WikiPage.BodyFormat != "storage" {
		t.Fatalf("unexpected body: %#v", response.WikiPage)
	}
	if response.WikiPage.Version != 7 || response.WikiPage.UpdatedBy.Login != "alice" {
		t.Fatalf("unexpected version data: %#v", response.WikiPage)
	}
}

func TestServicePageSearchUsesCQLAndLimit(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/content/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("cql") != `type=page and text ~ "integration"` {
			t.Fatalf("unexpected cql: %q", r.URL.Query().Get("cql"))
		}
		if r.URL.Query().Get("limit") != "5" {
			t.Fatalf("unexpected limit: %q", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"size": 1,
			"results": [
				{"id": "456", "type": "page", "status": "current", "title": "Integration contour", "space": {"key": "ENG"}, "version": {"number": 2}, "_links": {"base": "` + server.URL + `", "webui": "/display/ENG/Page"}}
			]
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{BaseURL: server.URL, Token: "token"})
	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeWiki,
		System:          "confluence",
		Resource:        "page",
		ObjectType:      "page",
		Operation:       "search",
		Query:           `type=page and text ~ "integration"`,
		Limit:           5,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(response.WikiPages) != 1 {
		t.Fatalf("unexpected pages: %#v", response.WikiPages)
	}
	if response.WikiPages[0].ExternalID != "456" || response.WikiPages[0].Title != "Integration contour" {
		t.Fatalf("unexpected page: %#v", response.WikiPages[0])
	}
	if response.Metadata["size"] != "1" {
		t.Fatalf("unexpected metadata: %#v", response.Metadata)
	}
}

func TestServiceAuthStatusUsesBasicAuthWhenUsernameConfigured(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/user/current" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
		if r.Header.Get("Authorization") != expected {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"alice","displayName":"Alice"}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{BaseURL: server.URL, Username: "alice", Token: "secret"})
	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "confluence", Resource: "auth", Operation: "status"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response.AuthStatus == nil || !response.AuthStatus.Authenticated {
		t.Fatalf("unexpected auth status: %#v", response.AuthStatus)
	}
	if !strings.Contains(response.AuthStatus.Message, "Alice") {
		t.Fatalf("unexpected message: %q", response.AuthStatus.Message)
	}
}

const serverBasePlaceholder = "http://example.invalid/confluence"
