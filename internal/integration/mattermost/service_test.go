package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceMessageCreateNormalizesPost(t *testing.T) {
	t.Parallel()

	var seenAuth string
	var seenPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/posts" {
			t.Fatalf("unexpected path: %q", r.URL.Path)
		}
		seenAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&seenPayload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"id": "post-1",
			"root_id": "root-1",
			"channel_id": "channel-1",
			"user_id": "user-1",
			"message": "Принято в обработку",
			"create_at": 1782925329000,
			"update_at": 1782925335000
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL:   server.URL,
		Token:     "token",
		ChannelID: "channel-1",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeMessenger,
		System:          "mattermost",
		Resource:        "message",
		ObjectType:      "message",
		Operation:       "create",
		ThreadID:        "root-1",
		Text:            "Принято в обработку",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenAuth != "Bearer token" {
		t.Fatalf("unexpected authorization header: %q", seenAuth)
	}
	if seenPayload["channel_id"] != "channel-1" || seenPayload["root_id"] != "root-1" {
		t.Fatalf("unexpected payload: %#v", seenPayload)
	}
	if response.Message == nil {
		t.Fatal("expected message")
	}
	if response.Message.MessageID != "post-1" {
		t.Fatalf("unexpected message id: %q", response.Message.MessageID)
	}
	if response.Message.ThreadID != "root-1" {
		t.Fatalf("unexpected thread id: %q", response.Message.ThreadID)
	}
	if response.Message.Body != "Принято в обработку" {
		t.Fatalf("unexpected body: %q", response.Message.Body)
	}
}
