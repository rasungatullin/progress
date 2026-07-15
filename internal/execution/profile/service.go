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

	config, repoRoot, err := s.loadConfig(ctx)
	if err != nil {
		return model.Profile{}, err
	}

	entry, ok := config.Profiles[name]
	if !ok {
		return model.Profile{}, fmt.Errorf("unknown execution profile: %s", name)
	}
	promptAdditions, err := s.resolvePromptAdditions(repoRoot, config.Defaults, entry)
	if err != nil {
		return model.Profile{}, fmt.Errorf("execution profile %q has invalid prompt additions: %w", name, err)
	}

	profile := model.Profile{
		Name:                     name,
		Description:              entry.Description,
		Mode:                     firstNonEmpty(entry.Mode, config.Defaults.Mode),
		ModelBinding:             firstNonEmpty(entry.ModelBinding, config.Defaults.ModelBinding),
		AllowModelFallback:       resolveBool(config.Defaults.AllowModelFallback, entry.AllowModelFallback),
		PromptAdditions:          promptAdditions,
		StructuredOutput:         resolveBool(config.Defaults.StructuredOutput, entry.StructuredOutput),
		StructuredOutputRequired: resolveBool(config.Defaults.StructuredOutputRequired, entry.StructuredOutputRequired),
		StartupTimeout:           firstNonEmpty(entry.StartupTimeout, config.Defaults.StartupTimeout),
		Timeout:                  firstNonEmpty(entry.Timeout, config.Defaults.Timeout),
		NoOutputTimeout:          firstNonEmpty(entry.NoOutputTimeout, config.Defaults.NoOutputTimeout),
		StructuredOutputTimeout:  firstNonEmpty(entry.StructuredOutputTimeout, config.Defaults.StructuredOutputTimeout),
	}

	if profile.Mode == "" {
		return model.Profile{}, fmt.Errorf("execution profile %q has empty mode", name)
	}

	return profile, nil
}

func (s *Service) loadConfig(ctx context.Context) (model.ProfileConfigFile, string, error) {
	repoRoot, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return model.ProfileConfigFile{}, "", fmt.Errorf("resolve git repository root for execution profiles: %w", err)
	}

	configPath := filepath.Join(repoRoot, configRelativePath)
	content, err := s.readFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return model.ProfileConfigFile{}, "", fmt.Errorf("execution profile config not found: %s", configPath)
		}

		return model.ProfileConfigFile{}, "", fmt.Errorf("read execution profile config %s: %w", configPath, err)
	}

	var config model.ProfileConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return model.ProfileConfigFile{}, "", fmt.Errorf("parse execution profile config %s: %w", configPath, err)
	}

	if len(config.Profiles) == 0 {
		return model.ProfileConfigFile{}, "", fmt.Errorf("execution profile config %s does not define any profiles", configPath)
	}

	return config, repoRoot, nil
}

func (s *Service) resolvePromptAdditions(repoRoot string, defaults model.ProfileOptions, entry model.ProfileConfig) ([]string, error) {
	resolved := make([]string, 0)
	for _, source := range []struct {
		values *[]string
		file   string
	}{
		{values: defaults.PromptAdditions, file: defaults.PromptAdditionsFile},
		{values: entry.PromptAdditions, file: entry.PromptAdditionsFile},
	} {
		if source.values != nil {
			resolved = append(resolved, normalizePromptAdditions(*source.values)...)
		}
		addition, err := s.readPromptAddition(repoRoot, source.file)
		if err != nil {
			return nil, err
		}
		if addition != "" {
			resolved = append(resolved, addition)
		}
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	return resolved, nil
}

func (s *Service) readPromptAddition(repoRoot, configuredPath string) (string, error) {
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" {
		return "", nil
	}
	if filepath.IsAbs(configuredPath) {
		return "", fmt.Errorf("prompt-additions-file %q must be relative to repository root", configuredPath)
	}

	root := filepath.Clean(repoRoot)
	promptPath := filepath.Clean(filepath.Join(root, configuredPath))
	relative, err := filepath.Rel(root, promptPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("prompt-additions-file %q escapes repository root", configuredPath)
	}
	evaluatedRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr == nil {
		root = evaluatedRoot
	} else if !os.IsNotExist(rootErr) {
		return "", fmt.Errorf("resolve repository root %s: %w", root, rootErr)
	}
	evaluatedPath, pathErr := filepath.EvalSymlinks(promptPath)
	if pathErr == nil {
		evaluatedRelative, relativeErr := filepath.Rel(root, evaluatedPath)
		if relativeErr != nil || evaluatedRelative == ".." || strings.HasPrefix(evaluatedRelative, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("prompt-additions-file %q escapes repository root", configuredPath)
		}
		promptPath = evaluatedPath
	} else if !os.IsNotExist(pathErr) {
		return "", fmt.Errorf("resolve prompt-additions-file %s: %w", promptPath, pathErr)
	}

	content, err := s.readFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("read prompt-additions-file %s: %w", promptPath, err)
	}
	return strings.TrimSpace(string(content)), nil
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
