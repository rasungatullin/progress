package authorization

import (
	"context"
	"testing"
)

func TestServiceAllowsMatchingPermission(t *testing.T) {
	t.Parallel()

	decision, err := NewService().Authorize(context.Background(), Policy{Roles: map[string]Role{
		"operator": {
			Name: "operator",
			Permissions: []Permission{{
				Contour: "execution",
				Action:  "start",
				Scope:   "*",
			}},
		},
	}}, Principal{ID: "u1", Roles: []string{"operator"}}, Operation{
		Contour: "execution",
		Action:  "start",
		Scope:   "project",
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allowed decision: %#v", decision)
	}
}

func TestServiceDeniesMissingPermission(t *testing.T) {
	t.Parallel()

	decision, err := NewService().Authorize(context.Background(), Policy{Roles: map[string]Role{}}, Principal{ID: "u1"}, Operation{
		Contour: "integration",
		Action:  "write",
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if decision.Allowed {
		t.Fatalf("expected denied decision: %#v", decision)
	}
}
