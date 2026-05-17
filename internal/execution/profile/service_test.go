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
			"runner": "opencode",
			"mode": "manual",
			"model": "openai/gpt-5.4",
			"prompt-additions": ["Default context.", "Always verify the result."],
			"structured-output": true,
			"structured-output-required": false,
			"structured-output-fields": ["remarks", "commands", "remarks"],
			"commit-push": false
		},
		"profiles": {
			"default": {
				"description": "Cloud profile"
			},
			"coder": {
				"description": "Coder profile",
				"runner": "codex",
				"model": "openai/gpt-5.3-codex-spark",
				"prompt-additions": ["Implement the requested change."],
				"structured-output-required": true,
				"structured-output-fields": ["commit_message", "changes"],
				"commit-push": true
			},
			"local": {
				"description": "Local profile",
				"structured-output": false,
				"structured-output-fields": [],
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
	if defaultProfile.Runner != "opencode" {
		t.Fatalf("unexpected default runner: %q", defaultProfile.Runner)
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
	if !defaultProfile.StructuredOutput {
		t.Fatal("default profile structured-output must inherit true")
	}
	if defaultProfile.StructuredOutputRequired {
		t.Fatal("default profile structured-output-required must inherit false")
	}
	if !equalStrings(defaultProfile.StructuredOutputFields, []string{"remarks", "commands"}) {
		t.Fatalf("unexpected default structured-output-fields: %#v", defaultProfile.StructuredOutputFields)
	}
	if !equalStrings(defaultProfile.PromptAdditions, []string{"Default context.", "Always verify the result."}) {
		t.Fatalf("unexpected default prompt-additions: %#v", defaultProfile.PromptAdditions)
	}

	localProfile, err := service.Resolve(context.Background(), model.Invocation{Profile: ProfileLocal})
	if err != nil {
		t.Fatalf("resolve local profile: %v", err)
	}

	if localProfile.Description != "Local profile" {
		t.Fatalf("unexpected local description: %q", localProfile.Description)
	}
	if localProfile.Runner != "opencode" {
		t.Fatalf("unexpected local runner: %q", localProfile.Runner)
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
	if localProfile.StructuredOutput {
		t.Fatal("local profile structured-output must override defaults to false")
	}
	if localProfile.StructuredOutputRequired {
		t.Fatal("local profile structured-output-required must inherit false")
	}
	if len(localProfile.StructuredOutputFields) != 0 {
		t.Fatalf("local profile structured-output-fields must allow explicit empty override: %#v", localProfile.StructuredOutputFields)
	}
	if !equalStrings(localProfile.PromptAdditions, []string{"Default context.", "Always verify the result."}) {
		t.Fatalf("unexpected local prompt-additions: %#v", localProfile.PromptAdditions)
	}

	coderProfile, err := service.Resolve(context.Background(), model.Invocation{Profile: "coder"})
	if err != nil {
		t.Fatalf("resolve coder profile: %v", err)
	}

	if coderProfile.Description != "Coder profile" {
		t.Fatalf("unexpected coder description: %q", coderProfile.Description)
	}
	if coderProfile.Runner != "codex" {
		t.Fatalf("unexpected coder runner: %q", coderProfile.Runner)
	}
	if coderProfile.Mode != "manual" {
		t.Fatalf("unexpected coder mode: %q", coderProfile.Mode)
	}
	if coderProfile.Model != "openai/gpt-5.3-codex-spark" {
		t.Fatalf("unexpected coder model: %q", coderProfile.Model)
	}
	if !coderProfile.CommitPush {
		t.Fatal("coder profile commit-push must override defaults to true")
	}
	if !coderProfile.StructuredOutput {
		t.Fatal("coder profile structured-output must inherit true from defaults")
	}
	if !coderProfile.StructuredOutputRequired {
		t.Fatal("coder profile structured-output-required must override defaults to true")
	}
	if !equalStrings(coderProfile.StructuredOutputFields, []string{"commit_message", "changes"}) {
		t.Fatalf("unexpected coder structured-output-fields: %#v", coderProfile.StructuredOutputFields)
	}
	if !equalStrings(coderProfile.PromptAdditions, []string{"Default context.", "Always verify the result.", "Implement the requested change."}) {
		t.Fatalf("unexpected coder prompt-additions: %#v", coderProfile.PromptAdditions)
	}
}

func TestResolveProfilePromptAdditionsKeepDefaultsWhenProfileListIsEmpty(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {
			"runner": "opencode",
			"mode": "manual",
			"model": "openai/gpt-5.4",
			"prompt-additions": ["Default context."]
		},
		"profiles": {
			"default": {
				"description": "Cloud profile",
				"prompt-additions": []
			}
		}
	}`)

	profile, err := service.Resolve(context.Background(), model.Invocation{})
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if !equalStrings(profile.PromptAdditions, []string{"Default context."}) {
		t.Fatalf("unexpected prompt-additions: %#v", profile.PromptAdditions)
	}
}

func TestResolveProfileReviewPresetFromRepositoryConfig(t *testing.T) {
	t.Parallel()

	profile, err := NewService().Resolve(context.Background(), model.Invocation{Profile: "review"})
	if err != nil {
		t.Fatalf("resolve review profile: %v", err)
	}
	if len(profile.PromptAdditions) == 0 {
		t.Fatal("review profile must define prompt-additions")
	}
	joined := strings.Join(profile.PromptAdditions, "\n")
	for _, expected := range []string{
		"Ты выполняешь code review.",
		"Не изменяй код",
		"bugs, behavioral regressions, missing tests",
		"предыдущих review comments",
		"conclusion status=ok/approve",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("review prompt-additions must include %q, got %q", expected, joined)
		}
	}
}

func TestResolveProfileAllowsSummaryInStructuredOutputFields(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"runner": "opencode", "mode": "manual", "model": "openai/gpt-5.4"},
		"profiles": {
			"default": {
				"description": "Cloud profile",
				"structured-output-fields": ["summary"]
			}
		}
	}`)

	profile, err := service.Resolve(context.Background(), model.Invocation{})
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if !equalStrings(profile.StructuredOutputFields, []string{"summary"}) {
		t.Fatalf("unexpected structured-output-fields: %#v", profile.StructuredOutputFields)
	}
}

func TestResolveProfileUnknownProfile(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"runner": "opencode", "mode": "manual", "model": "openai/gpt-5.4"},
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

func TestResolveProfileRequiresRunner(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"mode": "manual", "model": "openai/gpt-5.4"},
		"profiles": {"default": {"description": "Cloud profile"}}
	}`)

	_, err := service.Resolve(context.Background(), model.Invocation{})
	if err == nil {
		t.Fatal("expected empty runner error")
	}
	if !strings.Contains(err.Error(), `execution profile "default" has empty runner`) {
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

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}
