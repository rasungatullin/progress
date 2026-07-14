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
			"startup-timeout": "150ms",
			"structured-output-timeout": "900ms",
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
				"startup-timeout": "300ms",
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
	if defaultProfile.StartupTimeout != "150ms" {
		t.Fatalf("unexpected default startup-timeout: %q", defaultProfile.StartupTimeout)
	}
	if defaultProfile.StructuredOutputTimeout != "900ms" {
		t.Fatalf("unexpected structured-output-timeout: %q", defaultProfile.StructuredOutputTimeout)
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
	if coderProfile.StartupTimeout != "300ms" {
		t.Fatalf("unexpected coder startup-timeout: %q", coderProfile.StartupTimeout)
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
		"Сделай **критическую ревизию кода**",
		"Не останавливайся после первого найденного дефекта",
		"привяжи замечание к конкретному файлу и строке",
		"Верни список всех выявленных замечаний",
		"заключением ревизии",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("review prompt must include %q, got %q", expected, joined)
		}
	}
	for _, unexpected := range []string{"структурированный вывод", "conclusion.status", "remarks, questions"} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("review prompt must not duplicate structured output requirement %q: %q", unexpected, joined)
		}
	}
}

func TestResolveProfileCoderPresetFromRepositoryConfig(t *testing.T) {
	t.Parallel()

	profile, err := NewService().Resolve(context.Background(), model.Invocation{Profile: "coder"})
	if err != nil {
		t.Fatalf("resolve coder profile: %v", err)
	}
	if profile.ModelBinding != "coder" {
		t.Fatalf("unexpected coder model-binding: %q", profile.ModelBinding)
	}
	joined := strings.Join(profile.PromptAdditions, "\n")
	for _, expected := range []string{
		"Выполни задачу полностью",
		"Реализуй требование целостно",
		"Найди и исправь первопричину",
		"Добавь или обнови проверки",
		"готового к публикации",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("coder prompt must include %q, got %q", expected, joined)
		}
	}
	for _, unexpected := range []string{"структурированный вывод", "commit_message", "conclusion.status"} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("coder prompt must not duplicate structured output requirement %q: %q", unexpected, joined)
		}
	}
}

func TestResolveProfileReviewReworkPresetFromRepositoryConfig(t *testing.T) {
	t.Parallel()

	profile, err := NewService().Resolve(context.Background(), model.Invocation{Profile: "review-rework"})
	if err != nil {
		t.Fatalf("resolve review rework profile: %v", err)
	}
	if profile.ModelBinding != "coder" {
		t.Fatalf("unexpected review rework model-binding: %q", profile.ModelBinding)
	}
	joined := strings.Join(profile.PromptAdditions, "\n")
	for _, expected := range []string{
		"Исправь все полученные замечания ревизии",
		"Не останавливайся после первого замечания",
		"весь доступный набор",
		"Исправляй первопричину",
		"не создали новых регрессий",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("review rework prompt must include %q, got %q", expected, joined)
		}
	}
	for _, unexpected := range []string{"структурированный вывод", "review_responses", "conclusion.status"} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("review rework prompt must not duplicate structured output requirement %q: %q", unexpected, joined)
		}
	}
}

func TestResolveProfileLoadsPromptAdditionsFileInsideRepository(t *testing.T) {
	t.Parallel()

	service := &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return "/repo", nil },
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "/repo/.progress/execution/profiles.json":
				return []byte(`{
					"defaults": {"mode":"manual","prompt-additions":["Базовая инструкция."],"prompt-additions-file":"prompts/default.md"},
					"profiles": {"review":{"prompt-additions":["Инструкция профиля."],"prompt-additions-file":"prompts/review.md"}}
				}`), nil
			case "/repo/prompts/default.md":
				return []byte("Файл базовой инструкции."), nil
			case "/repo/prompts/review.md":
				return []byte("# Ревизия\n\nФайл инструкции профиля."), nil
			default:
				return nil, os.ErrNotExist
			}
		},
	}

	profile, err := service.Resolve(context.Background(), model.Invocation{Profile: "review"})
	if err != nil {
		t.Fatalf("resolve review profile: %v", err)
	}
	want := []string{"Базовая инструкция.", "Файл базовой инструкции.", "Инструкция профиля.", "# Ревизия\n\nФайл инструкции профиля."}
	if !equalStrings(profile.PromptAdditions, want) {
		t.Fatalf("unexpected prompt additions: %#v", profile.PromptAdditions)
	}
}

func TestResolveProfileRejectsPromptAdditionsFileOutsideRepository(t *testing.T) {
	t.Parallel()

	service := newTestService(`{
		"defaults": {"mode":"manual"},
		"profiles": {"review":{"prompt-additions-file":"../review.md"}}
	}`)
	_, err := service.Resolve(context.Background(), model.Invocation{Profile: "review"})
	if err == nil || !strings.Contains(err.Error(), "escapes repository root") {
		t.Fatalf("unexpected error: %v", err)
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
