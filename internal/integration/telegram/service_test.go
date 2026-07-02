package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceMessageCreateNormalizesSendMessage(t *testing.T) {
	t.Parallel()

	var seenPath string
	var seenPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&seenPayload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"result": {
				"message_id": 42,
				"date": 1782925329,
				"text": "Done",
				"from": {"id": 7, "is_bot": true, "first_name": "Progress", "username": "progress_bot"},
				"chat": {"id": 100, "type": "group", "title": "Engineering"},
				"reply_to_message": {"message_id": 41, "date": 1782925300, "chat": {"id": 100}}
			}
		}`))
	}))
	defer server.Close()

	service := NewService(model.IntegrationSystemConfig{
		BaseURL: server.URL,
		Token:   "secret",
		ChatID:  "100",
	})

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeMessenger,
		System:          "telegram",
		Resource:        "message",
		ObjectType:      "message",
		Operation:       "create",
		MessageID:       "41",
		Text:            "Done",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seenPath != "/botsecret/sendMessage" {
		t.Fatalf("unexpected path: %q", seenPath)
	}
	if seenPayload["chat_id"] != "100" || seenPayload["text"] != "Done" {
		t.Fatalf("unexpected payload: %#v", seenPayload)
	}
	if response.Message == nil {
		t.Fatal("expected message")
	}
	if response.Message.MessageID != "42" {
		t.Fatalf("unexpected message id: %q", response.Message.MessageID)
	}
	if response.Message.ThreadID != "41" {
		t.Fatalf("unexpected thread id: %q", response.Message.ThreadID)
	}
	if response.Message.Author.Login != "progress_bot" {
		t.Fatalf("unexpected author: %#v", response.Message.Author)
	}
}

func TestServiceThreadGetReturnsUnsupportedOperation(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{Token: "secret"})
	response, err := service.Execute(context.Background(), model.ProviderRequest{
		IntegrationType: model.IntegrationTypeMessenger,
		System:          "telegram",
		Resource:        "thread",
		ObjectType:      "thread",
		Operation:       "get",
		ThreadID:        "41",
	})
	if err == nil {
		t.Fatal("expected unsupported operation error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindUnsupportedOperation {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}
