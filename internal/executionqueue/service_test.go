package executionqueue

import (
	"context"
	"testing"
	"time"
)

func TestServiceSelectsHighestPriorityReadyItem(t *testing.T) {
	t.Parallel()

	service := NewService()
	service.nowFunc = func() time.Time { return time.Unix(1, 0) }
	if _, err := service.Enqueue(context.Background(), EnqueueRequest{Task: TaskRef{Number: 1}, Priority: 1, AssignmentID: "assignment-1"}); err != nil {
		t.Fatalf("enqueue low priority: %v", err)
	}
	high, err := service.Enqueue(context.Background(), EnqueueRequest{Task: TaskRef{Number: 2}, Priority: 10, AssignmentID: "assignment-2"})
	if err != nil {
		t.Fatalf("enqueue high priority: %v", err)
	}

	next, ok := service.Next(context.Background(), time.Unix(2, 0))
	if !ok {
		t.Fatal("expected queue item")
	}
	if next.ID != high.ID {
		t.Fatalf("unexpected next item: %#v", next)
	}
	if next.Status != StatusRunning {
		t.Fatalf("unexpected status: %q", next.Status)
	}
}

func TestServiceMovesExhaustedItemToManualIntervention(t *testing.T) {
	t.Parallel()

	service := NewService()
	item, err := service.Enqueue(context.Background(), EnqueueRequest{Task: TaskRef{ExternalID: "task-1"}, MaxAttempts: 1, AssignmentID: "assignment-1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	item, err = service.MarkAttempt(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("mark attempt: %v", err)
	}
	if item.Status != StatusManualIntervention {
		t.Fatalf("unexpected status: %q", item.Status)
	}
}

func TestServiceRejectsItemWithoutAssignment(t *testing.T) {
	t.Parallel()

	_, err := NewService().Enqueue(context.Background(), EnqueueRequest{Task: TaskRef{Number: 1}, AssignmentID: "   "})
	if err == nil {
		t.Fatal("expected missing assignment error")
	}
}
