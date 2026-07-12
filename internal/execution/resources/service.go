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
	Config             model.ResourceConfigFile
	EnvironmentSources map[string]configuration.ConfigFileSource
	ToolSources        map[string]configuration.ConfigFileSource
	ResourceSources    map[string]configuration.ConfigFileSource
	BindingSources     map[string]configuration.ConfigFileSource
	GlobalConfigPath   string
	LocalConfigPath    string
	ConfigHome         string
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
		allocation, err := resolveBinding(config, binding, allocationSourceExplicitBinding, in)
		if err != nil {
			return model.Allocation{}, err
		}
		allocation.GlobalConfigPath = config.GlobalConfigPath
		allocation.LocalConfigPath = config.LocalConfigPath
		allocation.ConfigHome = config.ConfigHome
		allocation.PrivateStore = config.Config.PrivateStore
		allocation.Git = config.Config.Git
		return allocation, nil
	}

	runner := strings.TrimSpace(in.Launch.Runner)
	modelName := strings.TrimSpace(in.Launch.Model)
	if runner != "" || modelName != "" {
		if runner == "" || modelName == "" {
			return model.Allocation{}, fmt.Errorf("launch runner and model must be provided together when no model-binding is used")
		}
		if _, ok := config.Config.Tools[runner]; !ok {
			return model.Allocation{}, fmt.Errorf("unknown execution runner: %s", runner)
		}
		if _, ok := config.Config.Resources[modelName]; !ok {
			return model.Allocation{}, fmt.Errorf("unknown execution model: %s", modelName)
		}
		if err := ensureToolResourceAvailable(config, runner, modelName); err != nil {
			return model.Allocation{}, err
		}
		environment, environmentType, err := resolveAllocationEnvironment(config, in, "")
		if err != nil {
			return model.Allocation{}, err
		}
		return model.Allocation{
			Resource:         "runner-model:" + runner + ":" + modelName,
			Reserved:         true,
			Runner:           runner,
			Model:            modelName,
			Environment:      environment,
			EnvironmentType:  environmentType,
			Source:           allocationSourceExplicitRunnerModel,
			GlobalConfigPath: config.GlobalConfigPath,
			LocalConfigPath:  config.LocalConfigPath,
			ConfigHome:       config.ConfigHome,
			PrivateStore:     config.Config.PrivateStore,
			FallbackUsed:     false,
			Git:              config.Config.Git,
		}, nil
	}

	profileBinding := strings.TrimSpace(profile.ModelBinding)
	if profileBinding != "" {
		allocation, err := resolveBinding(config, profileBinding, allocationSourceProfileBinding, in)
		if err == nil {
			allocation.GlobalConfigPath = config.GlobalConfigPath
			allocation.LocalConfigPath = config.LocalConfigPath
			allocation.ConfigHome = config.ConfigHome
			allocation.PrivateStore = config.Config.PrivateStore
			allocation.Git = config.Config.Git
			return allocation, nil
		}
		if !profile.AllowModelFallback {
			return model.Allocation{}, err
		}

		fallback, fallbackErr := resolveDefaultBinding(config, in)
		if fallbackErr != nil {
			return model.Allocation{}, fallbackErr
		}
		fallback.Source = allocationSourceDefaultBinding
		fallback.FallbackUsed = true
		fallback.GlobalConfigPath = config.GlobalConfigPath
		fallback.LocalConfigPath = config.LocalConfigPath
		fallback.ConfigHome = config.ConfigHome
		fallback.PrivateStore = config.Config.PrivateStore
		fallback.Git = config.Config.Git
		return fallback, nil
	}

	if !profile.AllowModelFallback {
		return model.Allocation{}, fmt.Errorf("execution model-binding is required when allow-model-fallback is false")
	}

	allocation, err := resolveDefaultBinding(config, in)
	if err != nil {
		return model.Allocation{}, err
	}
	allocation.Source = allocationSourceDefaultBinding
	allocation.FallbackUsed = true
	allocation.GlobalConfigPath = config.GlobalConfigPath
	allocation.LocalConfigPath = config.LocalConfigPath
	allocation.ConfigHome = config.ConfigHome
	allocation.PrivateStore = config.Config.PrivateStore
	allocation.Git = config.Config.Git
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
		Config:             loaded.Config,
		EnvironmentSources: loaded.EnvironmentSources,
		ToolSources:        loaded.ToolSources,
		ResourceSources:    loaded.ResourceSources,
		BindingSources:     loaded.BindingSources,
		GlobalConfigPath:   getLayerPath(loaded.Layers, configuration.ConfigFileSourceGlobal),
		LocalConfigPath:    getLayerPath(loaded.Layers, configuration.ConfigFileSourceLocal),
		ConfigHome:         loaded.ConfigHome,
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

func resolveBinding(config resourceConfig, bindingName string, source string, in model.Invocation) (model.Allocation, error) {
	binding, ok := config.Config.Bindings[bindingName]
	if !ok {
		return model.Allocation{}, fmt.Errorf("unknown execution model-binding: %s", bindingName)
	}
	tool := bindingTool(binding)
	resource := bindingResource(binding)
	if err := ensureToolResourceAvailable(config, tool, resource); err != nil {
		return model.Allocation{}, err
	}
	if err := validateReasoningEffort(tool, resource, binding.ReasoningEffort); err != nil {
		return model.Allocation{}, fmt.Errorf("binding %q has invalid reasoning-effort: %w", bindingName, err)
	}
	environment, environmentType, err := resolveAllocationEnvironment(config, in, binding.Environment)
	if err != nil {
		return model.Allocation{}, err
	}

	allocation := model.Allocation{
		Resource:        "binding:" + bindingName,
		Reserved:        true,
		Runner:          tool,
		Model:           resource,
		ModelBinding:    bindingName,
		ReasoningEffort: binding.ReasoningEffort,
		Environment:     environment,
		EnvironmentType: environmentType,
		Source:          source,
		FallbackUsed:    false,
	}
	if config.BindingSources != nil {
		allocation.BindingSource = string(config.BindingSources[bindingName])
	}
	return allocation, nil
}

func validateReasoningEffort(runner, modelName, effort string) error {
	return model.ValidateReasoningEffort(runner, modelName, effort)
}

func resolveDefaultBinding(config resourceConfig, in model.Invocation) (model.Allocation, error) {
	bindingName := strings.TrimSpace(config.Config.Defaults.ModelBinding)
	if bindingName == "" {
		return model.Allocation{}, fmt.Errorf("defaults.model-binding is required for fallback but is not configured")
	}
	return resolveBinding(config, bindingName, allocationSourceDefaultBinding, in)
}

func ensureToolResourceAvailable(config resourceConfig, toolName string, resourceName string) error {
	tool, ok := config.Config.Tools[toolName]
	if !ok {
		return fmt.Errorf("unknown execution runner: %s", toolName)
	}
	if !tool.Enabled {
		return fmt.Errorf("execution tool is disabled: %s", toolName)
	}

	resource, ok := config.Config.Resources[resourceName]
	if !ok {
		return fmt.Errorf("unknown execution model: %s", resourceName)
	}
	if !resource.Enabled {
		return fmt.Errorf("execution model is disabled: %s", resourceName)
	}
	if !resourceAllowsTool(resource, toolName) {
		return fmt.Errorf("execution model %s is not available for runner %s", resourceName, toolName)
	}

	return nil
}

func resolveAllocationEnvironment(config resourceConfig, in model.Invocation, bindingEnvironment string) (string, string, error) {
	for _, candidate := range []string{
		strings.TrimSpace(in.Workplace.Environment),
		strings.TrimSpace(bindingEnvironment),
		strings.TrimSpace(config.Config.Defaults.Environment),
		inferredEnvironment(in),
		configuration.EnvironmentTypeLocal,
	} {
		if candidate == "" {
			continue
		}
		environment, ok := config.Config.Environments[candidate]
		if !ok {
			return "", "", fmt.Errorf("unknown execution environment: %s", candidate)
		}
		if !environment.Enabled {
			return "", "", fmt.Errorf("execution environment is disabled: %s", candidate)
		}
		return candidate, strings.TrimSpace(environment.Type), nil
	}

	return "", "", fmt.Errorf("execution environment is not configured")
}

func inferredEnvironment(in model.Invocation) string {
	if strings.TrimSpace(in.Workplace.Name) != "" || strings.TrimSpace(in.Repository.URL) != "" {
		return configuration.EnvironmentTypeWorktree
	}
	return configuration.EnvironmentTypeLocal
}

func bindingTool(binding model.ResourceBindingConfig) string {
	if tool := strings.TrimSpace(binding.Tool); tool != "" {
		return tool
	}
	return strings.TrimSpace(binding.Runner)
}

func bindingResource(binding model.ResourceBindingConfig) string {
	if resource := strings.TrimSpace(binding.Resource); resource != "" {
		return resource
	}
	return strings.TrimSpace(binding.Model)
}

func resourceAllowsTool(resource model.ResourceConfig, tool string) bool {
	tool = strings.TrimSpace(tool)
	if len(resource.Tools) == 0 {
		return true
	}
	for _, allowed := range resource.Tools {
		if strings.TrimSpace(allowed) == tool {
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
