package ui

import (
	"context"
	"testing"
)

func TestServiceBuildsDefaultRunsView(t *testing.T) {
	t.Parallel()

	view, err := NewService().BuildView(context.Background(), ViewRequest{Workspace: "/repo"})
	if err != nil {
		t.Fatalf("build view: %v", err)
	}
	if view.Section != SectionRuns {
		t.Fatalf("unexpected section: %q", view.Section)
	}
	if view.Title != "Запуски" {
		t.Fatalf("unexpected title: %q", view.Title)
	}
	if len(view.Panels) != 3 {
		t.Fatalf("unexpected panels: %#v", view.Panels)
	}
}

func TestServiceRejectsUnknownSection(t *testing.T) {
	t.Parallel()

	if _, err := NewService().BuildView(context.Background(), ViewRequest{Section: "unknown"}); err == nil {
		t.Fatal("expected validation error")
	}
}
