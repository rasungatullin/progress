package resources

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
	configRelativePath = ".progress/execution/resources.json"

	allocationSourceExplicitBinding     = "explicit-model-binding"
	allocationSourceExplicitRunnerModel = "explicit-runner-model"
	allocationSourceProfileBinding      = "profile-model-binding"
	allocationSourceDefaultBinding      = "default-model-binding"
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

func (s *Service) Allocate(ctx context.Context, in model.Invocation, profile model.Profile) (model.Allocation, error) {
	config, err := s.loadConfig(ctx)
	if err != nil {
		return model.Allocation{}, err
	}

	if binding := strings.TrimSpace(in.Launch.ModelBinding); binding != "" {
		allocation, err := resolveBinding(config, binding, allocationSourceExplicitBinding)
		if err != nil {
			return model.Allocation{}, err
		}
		return allocation, nil
	}

	runner := strings.TrimSpace(in.Launch.Runner)
	modelName := strings.TrimSpace(in.Launch.Model)
	if runner != "" || modelName != "" {
		if runner == "" || modelName == "" {
			return model.Allocation{}, fmt.Errorf("launch runner and model must be provided together when no model-binding is used")
		}
		if !containsName(config.Runners, runner) {
			return model.Allocation{}, fmt.Errorf("unknown execution runner: %s", runner)
		}
		if !containsName(config.Models, modelName) {
			return model.Allocation{}, fmt.Errorf("unknown execution model: %s", modelName)
		}
		return model.Allocation{
			Resource:     "runner-model:" + runner + ":" + modelName,
			Reserved:     true,
			Runner:       runner,
			Model:        modelName,
			Source:       allocationSourceExplicitRunnerModel,
			FallbackUsed: false,
		}, nil
	}

	profileBinding := strings.TrimSpace(profile.ModelBinding)
	if profileBinding != "" {
		allocation, err := resolveBinding(config, profileBinding, allocationSourceProfileBinding)
		if err == nil {
			return allocation, nil
		}
		if !profile.AllowModelFallback {
			return model.Allocation{}, err
		}

		fallback, fallbackErr := resolveDefaultBinding(config)
		if fallbackErr != nil {
			return model.Allocation{}, fallbackErr
		}
		fallback.Source = allocationSourceDefaultBinding
		fallback.FallbackUsed = true
		return fallback, nil
	}

	if !profile.AllowModelFallback {
		return model.Allocation{}, fmt.Errorf("execution model-binding is required when allow-model-fallback is false")
	}

	allocation, err := resolveDefaultBinding(config)
	if err != nil {
		return model.Allocation{}, err
	}
	allocation.Source = allocationSourceDefaultBinding
	allocation.FallbackUsed = true
	return allocation, nil
}

func (s *Service) loadConfig(ctx context.Context) (model.ResourceConfigFile, error) {
	repoRoot, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return model.ResourceConfigFile{}, fmt.Errorf("resolve git repository root for execution resources: %w", err)
	}

	configPath := filepath.Join(repoRoot, configRelativePath)
	content, err := s.readFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return model.ResourceConfigFile{}, fmt.Errorf("execution resource config not found: %s", configPath)
		}
		return model.ResourceConfigFile{}, fmt.Errorf("read execution resource config %s: %w", configPath, err)
	}

	var config model.ResourceConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return model.ResourceConfigFile{}, fmt.Errorf("parse execution resource config %s: %w", configPath, err)
	}
	if err := validateConfig(config); err != nil {
		return model.ResourceConfigFile{}, fmt.Errorf("invalid execution resource config %s: %w", configPath, err)
	}
	return config, nil
}

func validateConfig(config model.ResourceConfigFile) error {
	if len(config.Runners) == 0 {
		return fmt.Errorf("runners must define at least one entry")
	}
	for _, name := range config.Runners {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("runners contains empty name")
		}
	}

	if len(config.Models) == 0 {
		return fmt.Errorf("models must define at least one entry")
	}
	for _, name := range config.Models {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("models contains empty name")
		}
	}

	if len(config.Bindings) == 0 {
		return fmt.Errorf("bindings must define at least one entry")
	}
	for name, binding := range config.Bindings {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("bindings contains empty name")
		}
		if strings.TrimSpace(binding.Runner) == "" {
			return fmt.Errorf("binding %q has empty runner", name)
		}
		if strings.TrimSpace(binding.Model) == "" {
			return fmt.Errorf("binding %q has empty model", name)
		}
		if !containsName(config.Runners, binding.Runner) {
			return fmt.Errorf("binding %q references unknown runner %q", name, binding.Runner)
		}
		if !containsName(config.Models, binding.Model) {
			return fmt.Errorf("binding %q references unknown model %q", name, binding.Model)
		}
	}

	if defaultBinding := strings.TrimSpace(config.Defaults.ModelBinding); defaultBinding != "" {
		if _, ok := config.Bindings[defaultBinding]; !ok {
			return fmt.Errorf("defaults.model-binding references unknown binding %q", defaultBinding)
		}
	}

	return nil
}

func resolveBinding(config model.ResourceConfigFile, bindingName string, source string) (model.Allocation, error) {
	binding, ok := config.Bindings[bindingName]
	if !ok {
		return model.Allocation{}, fmt.Errorf("unknown execution model-binding: %s", bindingName)
	}
	return model.Allocation{
		Resource:     "binding:" + bindingName,
		Reserved:     true,
		Runner:       binding.Runner,
		Model:        binding.Model,
		ModelBinding: bindingName,
		Source:       source,
		FallbackUsed: false,
	}, nil
}

func resolveDefaultBinding(config model.ResourceConfigFile) (model.Allocation, error) {
	bindingName := strings.TrimSpace(config.Defaults.ModelBinding)
	if bindingName == "" {
		return model.Allocation{}, fmt.Errorf("defaults.model-binding is required for fallback but is not configured")
	}
	return resolveBinding(config, bindingName, allocationSourceDefaultBinding)
}

func containsName(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
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
