package resources

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
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

func TestAllocateUsesGlobalResourcesWhenLocalIsMissing(t *testing.T) {
	t.Setenv("PROGRESS_CONFIG_HOME", "/global")
	service := newTestServiceWithGlobalFallback(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode", "codex"],
		"models": ["openai/gpt-5.4", "gpt-5.3-codex"],
		"bindings": {
			"default": {"runner": "opencode", "model": "openai/gpt-5.4"},
			"coder": {"runner": "codex", "model": "gpt-5.3-codex"}
		}
	}`, "")

	allocation, err := service.Allocate(context.Background(), model.Invocation{
		Launch: model.LaunchSpec{ModelBinding: "coder"},
	}, model.Profile{ModelBinding: "default"})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.ModelBinding != "coder" || allocation.Source != allocationSourceExplicitBinding {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.GlobalConfigPath == "" || allocation.LocalConfigPath != "" {
		t.Fatalf("unexpected config sources: %#v", allocation)
	}
}

func TestAllocateMergesLayersAndOverwritesLocalBinding(t *testing.T) {
	t.Setenv("PROGRESS_CONFIG_HOME", "/global")
	service := newTestServiceWithGlobalFallback(`{
		"defaults": {"model-binding": "default"},
		"runners": ["opencode", "codex"],
		"models": ["openai/gpt-5.4", "openai/gpt-5.5"],
		"bindings": {
			"default": {"runner": "opencode", "model": "openai/gpt-5.4"},
			"review": {"runner": "opencode", "model": "openai/gpt-5.5"}
		}
	}`, `{
		"defaults": {"model-binding": "coder"},
		"runners": ["codex"],
		"models": ["gpt-5.3-codex"],
		"bindings": {
			"default": {"runner": "codex", "model": "gpt-5.3-codex"},
			"coder": {"runner": "codex", "model": "gpt-5.3-codex"}
		}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{
		Launch: model.LaunchSpec{},
	}, model.Profile{
		ModelBinding:       "default",
		AllowModelFallback: true,
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.ModelBinding != "default" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.Runner != "codex" || allocation.Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	if allocation.BindingSource != "local" {
		t.Fatalf("expected binding source=local, got: %q", allocation.BindingSource)
	}
	if allocation.GlobalConfigPath == "" || allocation.LocalConfigPath == "" {
		t.Fatalf("expected both config paths: %#v", allocation)
	}
}

func TestAllocateReturnsConfigSourceErrorWhenNoConfigsExist(t *testing.T) {
	t.Setenv("PROGRESS_CONFIG_HOME", "/global")
	service := newTestService("")

	_, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{AllowModelFallback: true})
	if err == nil {
		t.Fatal("expected missing config error")
	}
	if !strings.Contains(err.Error(), "execution resource config not found") {
		t.Fatalf("unexpected error: %v", err)
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

func TestAllocateRejectsDisabledResource(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"tools": {"opencode": {"type": "agentic-system", "enabled": true}},
		"resources": {"qwen": {"type": "model", "enabled": false, "tools": ["opencode"]}},
		"bindings": {"default": {"tool": "opencode", "resource": "qwen"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{
		ModelBinding: "default",
	})
	if err == nil {
		t.Fatal("expected disabled model error")
	}
	if !strings.Contains(err.Error(), "execution model is disabled: qwen") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateRejectsExplicitRunnerModelWhenResourceDoesNotAllowTool(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"tools": {
			"opencode": {"type": "agentic-system", "enabled": true},
			"codex": {"type": "agentic-system", "enabled": true}
		},
		"resources": {"qwen": {"type": "model", "enabled": true, "tools": ["opencode"]}},
		"bindings": {"default": {"tool": "opencode", "resource": "qwen"}}
	}`)

	_, err := service.Allocate(context.Background(), model.Invocation{
		Launch: model.LaunchSpec{Runner: "codex", Model: "qwen"},
	}, model.Profile{ModelBinding: "default"})
	if err == nil {
		t.Fatal("expected tool restriction error")
	}
	if !strings.Contains(err.Error(), "execution model qwen is not available for runner codex") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateReturnsConfiguredEnvironment(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default", "environment": "same-process"},
		"environments": {
			"same-process": {"type": "local", "enabled": true},
			"isolated-tree": {"type": "worktree", "enabled": true}
		},
		"tools": {"opencode": {"type": "agentic-system", "enabled": true}},
		"resources": {"qwen": {"type": "model", "enabled": true, "tools": ["opencode"]}},
		"bindings": {"default": {"tool": "opencode", "resource": "qwen", "environment": "isolated-tree"}}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{
		ModelBinding: "default",
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Environment != "isolated-tree" || allocation.EnvironmentType != "worktree" {
		t.Fatalf("unexpected environment: %#v", allocation)
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

func TestAllocateResolvesGitPushPrivateIdentity(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "private-values.json")
	if err := os.WriteFile(storePath, []byte(`{"progress_push_key":"PRIVATE KEY"}`), 0o600); err != nil {
		t.Fatalf("write private store: %v", err)
	}
	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"private_store": {"type": "file", "path": "` + storePath + `"},
		"runners": ["opencode"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}},
		"git": {"push": {"ssh-identity-private": "progress_push_key"}}
	}`)

	allocation, err := service.Allocate(context.Background(), model.Invocation{}, model.Profile{AllowModelFallback: true})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if allocation.Git == nil || allocation.Git.Push == nil || allocation.Git.Push.SSHIdentityPrivateValue != "PRIVATE KEY" {
		t.Fatalf("private git identity was not resolved: %#v", allocation.Git)
	}
}

func newTestService(config string) *Service {
	return &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return "/repo", nil },
		readFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "/.progress/execution/resources.json") {
				if config == "" {
					return nil, fs.ErrNotExist
				}
				return []byte(config), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

func newTestServiceWithGlobalFallback(globalConfig, localConfig string) *Service {
	return &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return "/repo", nil },
		readFile: func(path string) ([]byte, error) {
			switch {
			case strings.HasSuffix(path, "/.progress/execution/resources.json"):
				if localConfig == "" {
					return nil, fs.ErrNotExist
				}
				return []byte(localConfig), nil
			case strings.Contains(path, "/global/execution/resources.json"):
				if globalConfig == "" {
					return nil, fs.ErrNotExist
				}
				return []byte(globalConfig), nil
			default:
				return nil, fs.ErrNotExist
			}
		},
	}
}
