package execution

import (
	"context"
	"log"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestServiceLaunchInheritsRunnerFromProfile(t *testing.T) {
	t.Parallel()

	launcher := &stubLauncher{}
	service := &Service{logger: log.Default(), launcher: launcher}

	_, err := service.Launch(context.Background(), Invocation{
		Launch: LaunchSpec{
			Directory: "/tmp/work",
			Model:     "",
			Prompt:    "ship it",
		},
	}, Profile{Runner: "codex", Model: "gpt-5.3-codex"}, Allocation{}, Workplace{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	if launcher.invocation.Launch.Runner != "codex" {
		t.Fatalf("expected runner to be inherited from profile, got %q", launcher.invocation.Launch.Runner)
	}
	if launcher.invocation.Launch.Model != "gpt-5.3-codex" {
		t.Fatalf("expected model to be inherited from profile, got %q", launcher.invocation.Launch.Model)
	}
}

type stubLauncher struct {
	invocation model.Invocation
}

func (s *stubLauncher) Launch(_ context.Context, in model.Invocation, _ model.Profile, _ model.Allocation, _ model.Workplace) (model.LaunchResult, error) {
	s.invocation = in
	return model.LaunchResult{Status: "completed"}, nil
}
