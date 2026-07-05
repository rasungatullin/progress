package profile

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestResolveProfileAppliesModelBindingAndFallbackDefaults(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {
			"mode": "manual",
			"model-binding": "default",
			"allow-model-fallback": true,
			"prompt-additions": ["Default context.", "Always verify the result."],
			"structured-output": true,
			"structured-output-required": false,
			"structured-output-fields": ["remarks", "commands", "remarks"]
		},
		"profiles": {
			"default": {
				"description": "Cloud profile"
			},
			"coder": {
				"description": "Coder profile",
				"model-binding": "coder",
				"allow-model-fallback": false,
				"prompt-additions": ["Implement the requested change."],
				"structured-output-required": true,
				"structured-output-fields": ["commit_message", "changes"]
			},
			"review": {
				"description": "Review profile",
				"model-binding": "review",
				"structured-output-fields": ["summary"]
			}
		}
	}`)

	defaultProfile, err := service.Resolve(context.Background(), model.Invocation{})
	if err != nil {
		t.Fatalf("resolve default profile: %v", err)
	}
	if defaultProfile.Name != ProfileDefault || defaultProfile.Description != "Cloud profile" {
		t.Fatalf("unexpected default profile: %#v", defaultProfile)
	}
	if defaultProfile.Mode != "manual" || defaultProfile.ModelBinding != "default" || !defaultProfile.AllowModelFallback {
		t.Fatalf("unexpected default binding config: %#v", defaultProfile)
	}
	if !defaultProfile.StructuredOutput || defaultProfile.StructuredOutputRequired {
		t.Fatalf("unexpected default structured output flags: %#v", defaultProfile)
	}
	if !equalStrings(defaultProfile.StructuredOutputFields, []string{"remarks", "commands"}) {
		t.Fatalf("unexpected default structured-output-fields: %#v", defaultProfile.StructuredOutputFields)
	}
	if !equalStrings(defaultProfile.PromptAdditions, []string{"Default context.", "Always verify the result."}) {
		t.Fatalf("unexpected default prompt-additions: %#v", defaultProfile.PromptAdditions)
	}

	coderProfile, err := service.Resolve(context.Background(), model.Invocation{Profile: "coder"})
	if err != nil {
		t.Fatalf("resolve coder profile: %v", err)
	}
	if coderProfile.ModelBinding != "coder" || coderProfile.AllowModelFallback {
		t.Fatalf("unexpected coder binding config: %#v", coderProfile)
	}
	if !coderProfile.StructuredOutput || !coderProfile.StructuredOutputRequired {
		t.Fatalf("unexpected coder structured flags: %#v", coderProfile)
	}
	if !equalStrings(coderProfile.StructuredOutputFields, []string{"commit_message", "changes"}) {
		t.Fatalf("unexpected coder structured-output-fields: %#v", coderProfile.StructuredOutputFields)
	}
	if !equalStrings(coderProfile.PromptAdditions, []string{"Default context.", "Always verify the result.", "Implement the requested change."}) {
		t.Fatalf("unexpected coder prompt-additions: %#v", coderProfile.PromptAdditions)
	}

	reviewProfile, err := service.Resolve(context.Background(), model.Invocation{Profile: "review"})
	if err != nil {
		t.Fatalf("resolve review profile: %v", err)
	}
	if reviewProfile.ModelBinding != "review" || !reviewProfile.AllowModelFallback {
		t.Fatalf("unexpected review binding config: %#v", reviewProfile)
	}
	if !equalStrings(reviewProfile.StructuredOutputFields, []string{"summary"}) {
		t.Fatalf("unexpected review structured-output-fields: %#v", reviewProfile.StructuredOutputFields)
	}
}

func TestResolveProfilePromptAdditionsKeepDefaultsWhenProfileListIsEmpty(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {
			"mode": "manual",
			"model-binding": "default",
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
	if profile.ModelBinding != "review" {
		t.Fatalf("unexpected review model-binding: %q", profile.ModelBinding)
	}
	joined := strings.Join(profile.PromptAdditions, "\n")
	for _, expected := range []string{
		"Ты выполняешь ревизию изменения.",
		"Не изменяй код",
		"дефекты, поведенческие регрессии, отсутствующие проверки",
		"предыдущие замечания",
		"conclusion.status=ok",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("review prompt-additions must include %q, got %q", expected, joined)
		}
	}
}

func TestResolveProfileAllowsSummaryInStructuredOutputFields(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"mode": "manual", "model-binding": "default"},
		"profiles": {
			"default": {
				"description": "Cloud profile",
				"structured-output-fields": ["summary", "review_responses"]
			}
		}
	}`)

	profile, err := service.Resolve(context.Background(), model.Invocation{})
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if !equalStrings(profile.StructuredOutputFields, []string{"summary", "review_responses"}) {
		t.Fatalf("unexpected structured-output-fields: %#v", profile.StructuredOutputFields)
	}
}

func TestResolveProfileUnknownProfile(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"mode": "manual", "model-binding": "default"},
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

func TestResolveProfileRequiresMode(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"model-binding": "default"},
		"profiles": {"default": {"description": "Cloud profile"}}
	}`)

	_, err := service.Resolve(context.Background(), model.Invocation{})
	if err == nil {
		t.Fatal("expected empty mode error")
	}
	if !strings.Contains(err.Error(), `execution profile "default" has empty mode`) {
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
