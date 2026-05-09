package profile

import (
	"context"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestResolveDefaultProfileIncludesCommitPush(t *testing.T) {
	t.Parallel()

	service := NewService()
	profile, err := service.Resolve(context.Background(), model.Invocation{})
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}

	if profile.Name != ProfileDefault {
		t.Fatalf("unexpected profile name: %q", profile.Name)
	}
	if profile.CommitPush {
		t.Fatal("default profile commit-push must be disabled")
	}
}
