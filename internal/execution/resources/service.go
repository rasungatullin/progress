package resources

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	allocationSourceExplicitBinding     = "explicit-model-binding"
	allocationSourceExplicitRunnerModel = "explicit-runner-model"
	allocationSourceProfileBinding      = "profile-model-binding"
	allocationSourceDefaultBinding      = "default-model-binding"
)

type Service struct {
	resolveRepoRoot func(context.Context) (string, error)
	readFile        func(string) ([]byte, error)
}

type resourceConfig struct {
	Config           model.ResourceConfigFile
	BindingSources   map[string]configuration.ConfigFileSource
	GlobalConfigPath string
	LocalConfigPath  string
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
		allocation.GlobalConfigPath = config.GlobalConfigPath
		allocation.LocalConfigPath = config.LocalConfigPath
		return allocation, nil
	}

	runner := strings.TrimSpace(in.Launch.Runner)
	modelName := strings.TrimSpace(in.Launch.Model)
	if runner != "" || modelName != "" {
		if runner == "" || modelName == "" {
			return model.Allocation{}, fmt.Errorf("launch runner and model must be provided together when no model-binding is used")
		}
		if !containsName(config.Config.Runners, runner) {
			return model.Allocation{}, fmt.Errorf("unknown execution runner: %s", runner)
		}
		if !containsName(config.Config.Models, modelName) {
			return model.Allocation{}, fmt.Errorf("unknown execution model: %s", modelName)
		}
		return model.Allocation{
			Resource:         "runner-model:" + runner + ":" + modelName,
			Reserved:         true,
			Runner:           runner,
			Model:            modelName,
			Source:           allocationSourceExplicitRunnerModel,
			GlobalConfigPath: config.GlobalConfigPath,
			LocalConfigPath:  config.LocalConfigPath,
			FallbackUsed:     false,
		}, nil
	}

	profileBinding := strings.TrimSpace(profile.ModelBinding)
	if profileBinding != "" {
		allocation, err := resolveBinding(config, profileBinding, allocationSourceProfileBinding)
		if err == nil {
			allocation.GlobalConfigPath = config.GlobalConfigPath
			allocation.LocalConfigPath = config.LocalConfigPath
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
		fallback.GlobalConfigPath = config.GlobalConfigPath
		fallback.LocalConfigPath = config.LocalConfigPath
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
	allocation.GlobalConfigPath = config.GlobalConfigPath
	allocation.LocalConfigPath = config.LocalConfigPath
	return allocation, nil
}

func (s *Service) loadConfig(ctx context.Context) (resourceConfig, error) {
	repoRoot, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return resourceConfig{}, fmt.Errorf("resolve git repository root for execution resources: %w", err)
	}

	loaded, err := configuration.LoadExecutionResourceConfig(repoRoot, s.readFile)
	if err != nil {
		return resourceConfig{}, err
	}

	return resourceConfig{
		Config:           loaded.Config,
		BindingSources:   loaded.BindingSources,
		GlobalConfigPath: getLayerPath(loaded.Layers, configuration.ConfigFileSourceGlobal),
		LocalConfigPath:  getLayerPath(loaded.Layers, configuration.ConfigFileSourceLocal),
	}, nil
}

func getLayerPath(layers []configuration.ExecutionResourceLayer, source configuration.ConfigFileSource) string {
	for _, layer := range layers {
		if layer.Source == source {
			return layer.Path
		}
	}
	return ""
}

func resolveBinding(config resourceConfig, bindingName string, source string) (model.Allocation, error) {
	binding, ok := config.Config.Bindings[bindingName]
	if !ok {
		return model.Allocation{}, fmt.Errorf("unknown execution model-binding: %s", bindingName)
	}

	allocation := model.Allocation{
		Resource:     "binding:" + bindingName,
		Reserved:     true,
		Runner:       binding.Runner,
		Model:        binding.Model,
		ModelBinding: bindingName,
		Source:       source,
		FallbackUsed: false,
	}
	if config.BindingSources != nil {
		allocation.BindingSource = string(config.BindingSources[bindingName])
	}
	return allocation, nil
}

func resolveDefaultBinding(config resourceConfig) (model.Allocation, error) {
	bindingName := strings.TrimSpace(config.Config.Defaults.ModelBinding)
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
