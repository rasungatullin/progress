package taskbank

import (
	"context"
	"testing"
)

func TestServiceSelectsCasesByTags(t *testing.T) {
	t.Parallel()

	cases, err := NewService().Select(context.Background(), Bank{
		Name: "regression",
		Cases: []Case{
			{ID: "1", Title: "A", Tags: []string{"review", "go"}},
			{ID: "2", Title: "B", Tags: []string{"docs"}},
		},
	}, Query{Tags: []string{"review"}})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "1" {
		t.Fatalf("unexpected cases: %#v", cases)
	}
}

func TestServiceRejectsUnnamedBank(t *testing.T) {
	t.Parallel()

	if _, err := NewService().Select(context.Background(), Bank{}, Query{}); err == nil {
		t.Fatal("expected validation error")
	}
}
