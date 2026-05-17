package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	ProfileDefault = "default"
	ProfileLocal   = "local"

	configRelativePath = ".progress/execution/profiles.json"
)

type Service struct {
	resolveRepoRoot func(context.Context) (string, error)
	readFile        func(string) ([]byte, error)
}

func NewService() *Service {
	return &Service{
		resolveRepoRoot: resolveRepoRoot,
		readFile:        os.ReadFile,
	}
}

func (s *Service) Resolve(ctx context.Context, in model.Invocation) (model.Profile, error) {
	name := strings.TrimSpace(in.Profile)
	if name == "" {
		name = ProfileDefault
	}

	config, err := s.loadConfig(ctx)
	if err != nil {
		return model.Profile{}, err
	}

	entry, ok := config.Profiles[name]
	if !ok {
		return model.Profile{}, fmt.Errorf("unknown execution profile: %s", name)
	}

	profile := model.Profile{
		Name:                     name,
		Description:              entry.Description,
		Runner:                   firstNonEmpty(entry.Runner, config.Defaults.Runner),
		Mode:                     firstNonEmpty(entry.Mode, config.Defaults.Mode),
		Model:                    firstNonEmpty(entry.Model, config.Defaults.Model),
		PromptAdditions:          resolvePromptAdditions(config.Defaults.PromptAdditions, entry.PromptAdditions),
		StructuredOutput:         resolveBool(config.Defaults.StructuredOutput, entry.StructuredOutput),
		StructuredOutputRequired: resolveBool(config.Defaults.StructuredOutputRequired, entry.StructuredOutputRequired),
		CommitPush:               resolveBool(config.Defaults.CommitPush, entry.CommitPush),
	}

	fields, err := resolveStructuredOutputFields(config.Defaults.StructuredOutputFields, entry.StructuredOutputFields)
	if err != nil {
		return model.Profile{}, fmt.Errorf("execution profile %q has invalid structured-output-fields: %w", name, err)
	}
	profile.StructuredOutputFields = fields

	if profile.Runner == "" {
		return model.Profile{}, fmt.Errorf("execution profile %q has empty runner", name)
	}

	if profile.Mode == "" {
		return model.Profile{}, fmt.Errorf("execution profile %q has empty mode", name)
	}

	if profile.Model == "" {
		return model.Profile{}, fmt.Errorf("execution profile %q has empty model", name)
	}

	return profile, nil
}

func (s *Service) loadConfig(ctx context.Context) (model.ProfileConfigFile, error) {
	repoRoot, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return model.ProfileConfigFile{}, fmt.Errorf("resolve git repository root for execution profiles: %w", err)
	}

	configPath := filepath.Join(repoRoot, configRelativePath)
	content, err := s.readFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return model.ProfileConfigFile{}, fmt.Errorf("execution profile config not found: %s", configPath)
		}

		return model.ProfileConfigFile{}, fmt.Errorf("read execution profile config %s: %w", configPath, err)
	}

	var config model.ProfileConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return model.ProfileConfigFile{}, fmt.Errorf("parse execution profile config %s: %w", configPath, err)
	}

	if len(config.Profiles) == 0 {
		return model.ProfileConfigFile{}, fmt.Errorf("execution profile config %s does not define any profiles", configPath)
	}

	return config, nil
}

func resolveRepoRoot(ctx context.Context) (string, error) {
	output, err := runGitOutput(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func resolveBool(defaultValue, overrideValue *bool) bool {
	if overrideValue != nil {
		return *overrideValue
	}
	if defaultValue != nil {
		return *defaultValue
	}

	return false
}

func resolveStructuredOutputFields(defaultValue, overrideValue *[]string) ([]string, error) {
	selected := defaultValue
	if overrideValue != nil {
		selected = overrideValue
	}
	if selected == nil {
		return nil, nil
	}

	fields := append([]string(nil), (*selected)...)
	return normalizeStructuredOutputFields(fields)
}

func resolvePromptAdditions(defaultValue, overrideValue *[]string) []string {
	merged := make([]string, 0)
	if defaultValue != nil {
		merged = append(merged, normalizePromptAdditions(*defaultValue)...)
	}
	if overrideValue != nil {
		merged = append(merged, normalizePromptAdditions(*overrideValue)...)
	}
	if len(merged) == 0 {
		return nil
	}

	return merged
}

func normalizePromptAdditions(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func normalizeStructuredOutputFields(fields []string) ([]string, error) {
	if fields == nil {
		return nil, nil
	}

	allowed := map[string]struct{}{
		"summary":           {},
		"commit_message":    {},
		"remarks":           {},
		"questions":         {},
		"follow_up_actions": {},
		"changes":           {},
		"commands":          {},
		"conclusion":        {},
		"extensions":        {},
	}

	normalized := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for index, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("field at index %d must be non-empty", index)
		}
		if _, ok := allowed[field]; !ok {
			return nil, fmt.Errorf("unsupported field %q", field)
		}
		if _, ok := seen[field]; ok {
			continue
		}

		seen[field] = struct{}{}
		normalized = append(normalized, field)
	}

	return normalized, nil
}
