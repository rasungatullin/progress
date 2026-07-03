package observability

import (
	"context"
	"testing"
	"time"
)

func TestServiceRecordsAndFiltersEvents(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.nowFunc = func() time.Time { return time.Unix(20, 0) }

	event, err := service.Record(context.Background(), RecordInput{
		Contour:   "execution",
		Module:    "dispatcher",
		Operation: "resolve-action",
		Message:   "Действие определено.",
		Metadata:  map[string]string{"action": "engineering-synthesis"},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if event.ID != "1" {
		t.Fatalf("unexpected id: %q", event.ID)
	}
	if event.Status != StatusRecorded {
		t.Fatalf("unexpected status: %q", event.Status)
	}

	events := service.List(context.Background(), Filter{Contour: "execution"})
	if len(events) != 1 {
		t.Fatalf("unexpected events: %#v", events)
	}
	if events[0].Metadata["action"] != "engineering-synthesis" {
		t.Fatalf("metadata was not copied: %#v", events[0].Metadata)
	}
}

func TestServiceRejectsIncompleteEvent(t *testing.T) {
	t.Parallel()

	if _, err := NewService().Record(context.Background(), RecordInput{Contour: "execution"}); err == nil {
		t.Fatal("expected validation error")
	}
}
