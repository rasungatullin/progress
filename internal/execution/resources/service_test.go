package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestAllocateUsesExplicitModelBinding(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode", "codex"],
		"models": ["openai/gpt-5.4", "gpt-5.3-codex"],
		"bindings": {
			"default": {"runner": "opencode", "model": "openai/gpt-5.4"},
			"coder": {"runner": "codex", "model": "gpt-5.3-codex"}
		}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{
		Launch: model.LaunchSpec{ModelBinding: "coder"},
	}, model.Profile{ModelBinding: "default"})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Runner != "codex" || allocation.Model != "gpt-5.3-codex" || allocation.ModelBinding != "coder" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.Source != allocationSourceExplicitBinding || allocation.FallbackUsed {
		t.Fatalf("unexpected allocation metadata: %#v", allocation)
	}
}

func TestAllocateUsesExplicitRunnerAndModelWithoutBinding(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode", "codex"],
		"models": ["openai/gpt-5.4", "gpt-5.3-codex"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{
		Launch: model.LaunchSpec{Runner: "codex", Model: "gpt-5.3-codex"},
	}, model.Profile{ModelBinding: "default"})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Runner != "codex" || allocation.Model != "gpt-5.3-codex" || allocation.ModelBinding != "" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.Source != allocationSourceExplicitRunnerModel || allocation.FallbackUsed {
		t.Fatalf("unexpected allocation metadata: %#v", allocation)
	}
}

func TestAllocateUsesProfileBinding(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4", "openai/gpt-5.5"],
		"bindings": {
			"default": {"runner": "opencode", "model": "openai/gpt-5.4"},
			"review": {"runner": "opencode", "model": "openai/gpt-5.5"}
		}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{ModelBinding: "review"})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Runner != "opencode" || allocation.Model != "openai/gpt-5.5" || allocation.ModelBinding != "review" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.Source != allocationSourceProfileBinding || allocation.FallbackUsed {
		t.Fatalf("unexpected allocation metadata: %#v", allocation)
	}
}

func TestAllocateFallsBackToDefaultBindingWhenProfileAllowsIt(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{
		ModelBinding:       "missing",
		AllowModelFallback: true,
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Runner != "opencode" || allocation.Model != "openai/gpt-5.4" || allocation.ModelBinding != "default" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.Source != allocationSourceDefaultBinding || !allocation.FallbackUsed {
		t.Fatalf("unexpected allocation metadata: %#v", allocation)
	}
}

func TestAllocateRejectsUnknownProfileBindingWithoutFallback(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{ModelBinding: "missing"})
	if err == nil {
		t.Fatal("expected unknown binding error")
	}
	if !strings.Contains(err.Error(), "unknown execution model-binding: missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateRejectsHalfExplicitRunnerModelPair(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{
		Launch: model.LaunchSpec{Runner: "opencode"},
	}, model.Profile{})
	if err == nil {
		t.Fatal("expected explicit pair error")
	}
	if !strings.Contains(err.Error(), "launch runner and model must be provided together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "missing", "model": "openai/gpt-5.4"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), `binding "default" references unknown runner "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateRejectsEmptyProfileBindingWithoutFallback(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{
		AllowModelFallback: false,
	})
	if err == nil {
		t.Fatal("expected missing binding error")
	}
	if !strings.Contains(err.Error(), "execution model-binding is required when allow-model-fallback is false") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateFallsBackToDefaultBindingWhenProfileBindingIsEmpty(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{
		AllowModelFallback: true,
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Runner != "opencode" || allocation.Model != "openai/gpt-5.4" || allocation.ModelBinding != "default" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.Source != allocationSourceDefaultBinding || !allocation.FallbackUsed {
		t.Fatalf("unexpected allocation metadata: %#v", allocation)
	}
}

func TestAllocateRejectsFallbackWhenDefaultBindingIsNotConfigured(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{
		AllowModelFallback: true,
	})
	if err == nil {
		t.Fatal("expected missing default binding error")
	}
	if !strings.Contains(err.Error(), "defaults.model-binding is required for fallback but is not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestService(config string) *Service {
	return &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return "/repo", nil },
		readFile: func(string) ([]byte, error) {
			return []byte(config), nil
		},
	}
}
