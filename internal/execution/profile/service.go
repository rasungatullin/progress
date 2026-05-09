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
		Name:        name,
		Description: entry.Description,
		Mode:        firstNonEmpty(entry.Mode, config.Defaults.Mode),
		Model:       firstNonEmpty(entry.Model, config.Defaults.Model),
		CommitPush:  resolveCommitPush(config.Defaults.CommitPush, entry.CommitPush),
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

func resolveCommitPush(defaultValue, overrideValue *bool) bool {
	if overrideValue != nil {
		return *overrideValue
	}
	if defaultValue != nil {
		return *defaultValue
	}

	return false
}
