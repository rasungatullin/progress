package profile

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestResolveProfileAppliesDefaultsAndOverrides(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {
			"mode": "manual",
			"model": "openai/gpt-5.4",
			"commit-push": false
		},
		"profiles": {
			"default": {
				"description": "Cloud profile"
			},
			"local": {
				"description": "Local profile",
				"model": "ollama/qwen3.5:2b"
			}
		}
	}`)

	defaultProfile, err := service.Resolve(context.Background(), model.Invocation{})
	if err != nil {
		t.Fatalf("resolve default profile: %v", err)
	}

	if defaultProfile.Name != ProfileDefault {
		t.Fatalf("unexpected default profile name: %q", defaultProfile.Name)
	}
	if defaultProfile.Description != "Cloud profile" {
		t.Fatalf("unexpected default description: %q", defaultProfile.Description)
	}
	if defaultProfile.Mode != "manual" {
		t.Fatalf("unexpected default mode: %q", defaultProfile.Mode)
	}
	if defaultProfile.Model != "openai/gpt-5.4" {
		t.Fatalf("unexpected default model: %q", defaultProfile.Model)
	}
	if defaultProfile.CommitPush {
		t.Fatal("default profile commit-push must inherit false")
	}

	localProfile, err := service.Resolve(context.Background(), model.Invocation{Profile: ProfileLocal})
	if err != nil {
		t.Fatalf("resolve local profile: %v", err)
	}

	if localProfile.Description != "Local profile" {
		t.Fatalf("unexpected local description: %q", localProfile.Description)
	}
	if localProfile.Mode != "manual" {
		t.Fatalf("unexpected local mode: %q", localProfile.Mode)
	}
	if localProfile.Model != "ollama/qwen3.5:2b" {
		t.Fatalf("unexpected local model: %q", localProfile.Model)
	}
	if localProfile.CommitPush {
		t.Fatal("local profile commit-push must inherit false")
	}
}

func TestResolveProfileUnknownProfile(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"mode": "manual", "model": "openai/gpt-5.4"},
		"profiles": {"default": {"description": "Cloud profile"}}
	}`)

	_, err := service.Resolve(context.Background(), model.Invocation{Profile: "missing"})
	if err == nil {
		t.Fatal("expected unknown profile error")
	}
	if !strings.Contains(err.Error(), "unknown execution profile: missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveProfileInvalidConfig(t *testing.T) {
	t.Parallel()

	service := newTestService(`{"defaults":`)

	_, err := service.Resolve(context.Background(), model.Invocation{})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse execution profile config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveProfileMissingConfig(t *testing.T) {
	t.Parallel()

	service := &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return "/repo", nil },
		readFile: func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
	}

	_, err := service.Resolve(context.Background(), model.Invocation{})
	if err == nil {
		t.Fatal("expected missing config error")
	}
	if !strings.Contains(err.Error(), "execution profile config not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveProfileRepoRootError(t *testing.T) {
	t.Parallel()

	service := &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return "", errors.New("git failed") },
		readFile:        os.ReadFile,
	}

	_, err := service.Resolve(context.Background(), model.Invocation{})
	if err == nil {
		t.Fatal("expected repo root error")
	}
	if !strings.Contains(err.Error(), "resolve git repository root for execution profiles") {
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
