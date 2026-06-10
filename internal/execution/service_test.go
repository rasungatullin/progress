package execution

import (
	"context"
	"errors"
	"log"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/history"
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

func TestServiceLaunchPreservesExistingPromptBehaviorWithoutProfileAdditions(t *testing.T) {
	t.Parallel()

	launcher := &stubLauncher{}
	service := &Service{logger: log.Default(), launcher: launcher}

	_, err := service.Launch(context.Background(), Invocation{
		Launch: LaunchSpec{
			Directory: "/tmp/work",
			Prompt:    "ship it",
		},
	}, Profile{Runner: "opencode", Model: "gpt-5.4"}, Allocation{}, Workplace{})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if len(launcher.invocation.Launch.PromptAdditions) != 0 {
		t.Fatalf("prompt-additions must stay empty when profile does not define them: %#v", launcher.invocation.Launch.PromptAdditions)
	}
}

func TestServiceStartRecordsProfileFailureInHistory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	expectedErr := errors.New("profile unavailable")
	service := &Service{
		logger:   log.Default(),
		profiles: &stubProfileResolver{err: expectedErr},
	}

	result, err := service.Start(context.Background(), Invocation{
		Profile:   "missing",
		Workplace: WorkplaceSpec{Name: "task-54"},
		Launch: LaunchSpec{
			Directory: root,
			Runner:    "opencode",
			Model:     "openai/gpt-5.4",
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected profile error, got %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed result, got %#v", result)
	}

	runs, err := history.List(context.Background(), root, history.ListFilter{Limit: 10, Status: "failed"})
	if err != nil {
		t.Fatalf("list sqlite history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one failed sqlite history run, got %d", len(runs))
	}
	if runs[0].Name != "task-54" || runs[0].Error != expectedErr.Error() || runs[0].LaunchDirectory != root {
		t.Fatalf("unexpected failed start row: %#v", runs[0])
	}
}

type stubLauncher struct {
	invocation model.Invocation
}

func (s *stubLauncher) Launch(_ context.Context, in model.Invocation, _ model.Profile, _ model.Allocation, _ model.Workplace) (model.LaunchResult, error) {
	s.invocation = in
	return model.LaunchResult{Status: "completed"}, nil
}

type stubProfileResolver struct {
	profile model.Profile
	err     error
}

func (s *stubProfileResolver) Resolve(context.Context, model.Invocation) (model.Profile, error) {
	if s.err != nil {
		return model.Profile{}, s.err
	}
	return s.profile, nil
}
