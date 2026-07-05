package methodology

import (
	"context"
	"testing"
)

func TestServiceSelectsRouteActionAndInstruction(t *testing.T) {
	t.Parallel()

	catalog := Catalog{
		Routes: []Route{{
			Name:    "implementation",
			Title:   "Маршрут исполнения",
			Action:  "engineering-synthesis",
			Profile: "default",
			Checks:  []string{"task-ready"},
		}},
		Actions: []Action{{
			Name:       "engineering-synthesis",
			Class:      "engineering-synthesis",
			Profile:    "default",
			Operations: []ActionOperation{{Name: "prepare-data", Kind: "prepare-data"}, {Name: "launch-synthesis", Kind: "launch-synthesis"}},
		}},
		Instructions: []Instruction{{
			Name:    "default-directive",
			Profile: "default",
			Body:    "Сформировать результат.",
		}},
	}

	result, err := NewService(nil).Select(context.Background(), catalog, SelectionRequest{Route: "implementation"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Route.Name != "implementation" {
		t.Fatalf("unexpected route: %#v", result.Route)
	}
	if result.Action.Name != "engineering-synthesis" {
		t.Fatalf("unexpected action: %#v", result.Action)
	}
	if result.Profile != "default" {
		t.Fatalf("unexpected profile: %q", result.Profile)
	}
	if result.Instruction.Name != "default-directive" {
		t.Fatalf("unexpected instruction: %#v", result.Instruction)
	}
	if len(result.Diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
}

func TestServiceSelectsDefaultRouteWhenRouteNameIsEmpty(t *testing.T) {
	t.Parallel()

	result, err := NewService(nil).Select(context.Background(), Catalog{
		Routes: []Route{
			{Name: "task-processing-completed", Outcome: "completed"},
			{Name: "default", Action: "engineering-synthesis", Profile: "default"},
		},
		Actions: []Action{{
			Name:    "engineering-synthesis",
			Profile: "default",
		}},
	}, SelectionRequest{})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Route.Name != "default" {
		t.Fatalf("expected default route, got %#v", result.Route)
	}
}

func TestServiceSelectReportsMissingAction(t *testing.T) {
	t.Parallel()

	_, err := NewService(nil).Select(context.Background(), Catalog{
		Routes: []Route{{Name: "default", Action: "missing"}},
	}, SelectionRequest{})
	if err == nil {
		t.Fatal("expected missing action error")
	}
}

func TestServiceSelectDoesNotUseInstructionForAnotherProfile(t *testing.T) {
	t.Parallel()

	result, err := NewService(nil).Select(context.Background(), Catalog{
		Routes: []Route{{Name: "default", Action: "engineering-synthesis", Profile: "default"}},
		Actions: []Action{{
			Name:    "engineering-synthesis",
			Profile: "default",
		}},
		Instructions: []Instruction{{
			Name:    "review-directive",
			Profile: "review",
			Body:    "Провести ревизию.",
		}},
	}, SelectionRequest{})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result.Instruction.Name != "" {
		t.Fatalf("unexpected incompatible instruction: %#v", result.Instruction)
	}
	if len(result.Diagnostics) == 0 || result.Diagnostics[len(result.Diagnostics)-1] != "instruction-missing-for-profile=default" {
		t.Fatalf("expected missing instruction diagnostic: %#v", result.Diagnostics)
	}
}
