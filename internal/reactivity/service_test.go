package reactivity

import (
	"context"
	"testing"
	"time"
)

func TestServiceNormalizeBuildsSignal(t *testing.T) {
	t.Parallel()

	service := NewService(nil)
	service.now = func() time.Time { return time.Unix(10, 0) }

	result, err := service.Normalize(context.Background(), Event{
		Source:     "github",
		Kind:       "issue-comment",
		ObjectType: "issue",
		ObjectID:   "123",
		Metadata:   map[string]string{"repository": "owner/name"},
	}, Process{
		Name:            "github-issue-events",
		EventSource:     "github",
		EventKind:       "issue-comment",
		SignalKind:      "task-updated",
		IntegrationType: "tracker",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Status != StatusAccepted {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Signal == nil {
		t.Fatal("expected signal")
	}
	if result.Signal.Process != "github-issue-events" {
		t.Fatalf("unexpected process: %q", result.Signal.Process)
	}
	if result.Signal.Kind != "task-updated" {
		t.Fatalf("unexpected signal kind: %q", result.Signal.Kind)
	}
	if result.Signal.IntegrationType != "tracker" {
		t.Fatalf("unexpected integration type: %q", result.Signal.IntegrationType)
	}
	if result.Signal.Metadata["repository"] != "owner/name" {
		t.Fatalf("metadata was not copied: %#v", result.Signal.Metadata)
	}
	if !result.Signal.OccurredAt.Equal(time.Unix(10, 0)) {
		t.Fatalf("unexpected occurrence time: %s", result.Signal.OccurredAt)
	}
}

func TestServiceNormalizeIgnoresForeignEvent(t *testing.T) {
	t.Parallel()

	result, err := NewService(nil).Normalize(context.Background(), Event{
		Source:   "mattermost",
		Kind:     "message",
		ObjectID: "abc",
	}, Process{
		Name:        "github-events",
		EventSource: "github",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Status != StatusIgnored {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Signal != nil {
		t.Fatalf("ignored event must not return signal: %#v", result.Signal)
	}
}

func TestServiceNormalizeIgnoresForeignEventBeforeObjectValidation(t *testing.T) {
	t.Parallel()

	result, err := NewService(nil).Normalize(context.Background(), Event{
		Source: "mattermost",
		Kind:   "message",
	}, Process{
		Name:        "github-events",
		EventSource: "github",
		EventKind:   "issue-comment",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if result.Status != StatusIgnored {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if len(result.Reasons) == 0 || result.Reasons[0].Code != "source_mismatch" {
		t.Fatalf("unexpected reasons: %#v", result.Reasons)
	}
}
