package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

var resolveUserHome = os.UserHomeDir

const (
	configDefaultHome      = ".config/progress"
	configHomeEnvVar       = "PROGRESS_CONFIG_HOME"
	executionConfigPath    = "execution/resources.json"
	executionLocalFilePath = ".progress/execution/resources.json"
)

type ConfigFileSource string

const (
	ConfigFileSourceGlobal ConfigFileSource = "global"
	ConfigFileSourceLocal  ConfigFileSource = "local"
)

type ReadFileFunc func(string) ([]byte, error)

type ExecutionResourceLayer struct {
	Source ConfigFileSource
	Path   string
	Config model.ResourceConfigFile
}

type ExecutionResourceConfig struct {
	Config         model.ResourceConfigFile
	Layers         []ExecutionResourceLayer
	BindingSources map[string]ConfigFileSource
}

func LoadExecutionResourceConfig(repoRoot string, readFile ReadFileFunc) (ExecutionResourceConfig, error) {
	return LoadExecutionResourceConfigWithHome(repoRoot, "", readFile)
}

func LoadExecutionResourceConfigWithHome(repoRoot, configHome string, readFile ReadFileFunc) (ExecutionResourceConfig, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}

	home, globalHomeErr := resolveConfigHome(configHome)

	globalPath := ""
	useGlobalLayer := globalHomeErr == nil
	localPath := filepath.Join(repoRoot, executionLocalFilePath)

	if useGlobalLayer {
		globalPath = filepath.Join(home, executionConfigPath)
	}

	var layers []ExecutionResourceLayer

	if useGlobalLayer {
		if config, err := readLayer(globalPath, ConfigFileSourceGlobal, readFile); err == nil {
			layers = append(layers, config)
		} else if !isNotExistErr(err) {
			return ExecutionResourceConfig{}, err
		}
	}

	if config, err := readLayer(localPath, ConfigFileSourceLocal, readFile); err == nil {
		layers = append(layers, config)
	} else if !isNotExistErr(err) {
		return ExecutionResourceConfig{}, err
	}

	if len(layers) == 0 {
		if useGlobalLayer {
			return ExecutionResourceConfig{}, fmt.Errorf("execution resource config not found: global=%s local=%s", globalPath, localPath)
		}
		return ExecutionResourceConfig{}, fmt.Errorf("execution resource config not found: global layer unavailable (%v) local=%s", globalHomeErr, localPath)
	}

	merged := mergeExecutionResourceLayers(layers)
	if err := validateResourceConfig(merged.Config); err != nil {
		return ExecutionResourceConfig{}, fmt.Errorf("invalid execution resource config after merge of %d layers: %w", len(layers), err)
	}

	return merged, nil
}

func readLayer(path string, source ConfigFileSource, readFile ReadFileFunc) (ExecutionResourceLayer, error) {
	content, err := readFile(path)
	if err != nil {
		return ExecutionResourceLayer{}, err
	}

	var config model.ResourceConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return ExecutionResourceLayer{}, fmt.Errorf("parse execution resource config %s: %w", path, err)
	}
	if err := validateResourceConfig(config); err != nil {
		return ExecutionResourceLayer{}, fmt.Errorf("invalid execution resource config %s: %w", path, err)
	}

	return ExecutionResourceLayer{Source: source, Path: path, Config: config}, nil
}

func mergeExecutionResourceLayers(layers []ExecutionResourceLayer) ExecutionResourceConfig {
	merged := model.ResourceConfigFile{
		Defaults: model.ResourceDefaultsConfig{},
		Runners:  map[string]model.RunnerConfig{},
		Bindings: map[string]model.ResourceBindingConfig{},
	}
	bindingSources := map[string]ConfigFileSource{}

	modelSeen := map[string]struct{}{}

	for _, layer := range layers {
		for name, runner := range layer.Config.Runners {
			name = strings.TrimSpace(name)
			merged.Runners[name] = runner
		}
		for _, name := range layer.Config.Models {
			name = strings.TrimSpace(name)
			if _, ok := modelSeen[name]; !ok {
				modelSeen[name] = struct{}{}
				merged.Models = append(merged.Models, name)
			}
		}
		for name, binding := range layer.Config.Bindings {
			merged.Bindings[name] = binding
			bindingSources[name] = layer.Source
		}
		if strings.TrimSpace(layer.Config.Defaults.ModelBinding) != "" {
			merged.Defaults.ModelBinding = layer.Config.Defaults.ModelBinding
		}
	}

	return ExecutionResourceConfig{
		Config:         merged,
		Layers:         layers,
		BindingSources: bindingSources,
	}
}

func isNotExistErr(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func resolveConfigHome(configHome string) (string, error) {
	if strings.TrimSpace(configHome) != "" {
		return configHome, nil
	}

	envHome := strings.TrimSpace(os.Getenv(configHomeEnvVar))
	if envHome != "" {
		return envHome, nil
	}

	userHome, err := resolveUserHome()
	if err != nil {
		return "", fmt.Errorf("resolve current user home: %w", err)
	}
	return filepath.Join(userHome, configDefaultHome), nil
}

func validateResourceConfig(config model.ResourceConfigFile) error {
	if len(config.Runners) == 0 {
		return fmt.Errorf("runners must define at least one entry")
	}
	for name, runner := range config.Runners {
		name = strings.TrimSpace(name)
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("runners contains empty name")
		}
		runnerType := strings.TrimSpace(runner.Type)
		if runnerType == "" {
			return fmt.Errorf("runner %q has empty type", name)
		}
		switch runnerType {
		case "opencode":
			if strings.TrimSpace(runner.Script) != "" {
				return fmt.Errorf("runner %q with type opencode must not define script", name)
			}
			if err := validateRunnerSettings(name, runnerType, runner.Settings); err != nil {
				return err
			}
		case "codex":
			if strings.TrimSpace(runner.Script) != "" {
				return fmt.Errorf("runner %q with type codex must not define script", name)
			}
			if err := validateRunnerSettings(name, runnerType, runner.Settings); err != nil {
				return err
			}
		case "script":
			if strings.TrimSpace(runner.Script) == "" {
				return fmt.Errorf("runner %q with type script requires script", name)
			}
			if len(runner.Settings) > 0 {
				return fmt.Errorf("runner %q with type script must not define settings", name)
			}
		default:
			return fmt.Errorf("runner %q has unsupported type %q", name, runnerType)
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
		bindingRunner := strings.TrimSpace(binding.Runner)
		if !containsRunner(config.Runners, bindingRunner) {
			return fmt.Errorf("binding %q references unknown runner %q", name, binding.Runner)
		}
		if !runnerEnabled(config.Runners[bindingRunner]) {
			return fmt.Errorf("binding %q references disabled runner %q", name, binding.Runner)
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

func validateRunnerSettings(name, runnerType string, settings map[string]string) error {
	for key := range settings {
		key = strings.TrimSpace(key)
		switch runnerType {
		case "codex":
			if key != "sandbox" {
				return fmt.Errorf("runner %q with type codex has unsupported setting %q", name, key)
			}
		case "opencode":
			return fmt.Errorf("runner %q with type opencode has unsupported setting %q", name, key)
		}
	}
	return nil
}

func containsRunner(values map[string]model.RunnerConfig, expected string) bool {
	_, ok := values[strings.TrimSpace(expected)]
	return ok
}

func runnerEnabled(runner model.RunnerConfig) bool {
	if runner.Enabled == nil {
		return true
	}
	return *runner.Enabled
}

func containsName(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
