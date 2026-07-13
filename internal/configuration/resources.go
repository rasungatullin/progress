package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/model"
)

var resolveUserHome = os.UserHomeDir

const (
	configDefaultHome      = ".config/progress"
	configHomeEnvVar       = "PROGRESS_CONFIG_HOME"
	executionConfigPath    = "execution/resources.json"
	executionLocalFilePath = ".progress/execution/resources.json"

	EnvironmentTypeLocal    = "local"
	EnvironmentTypeWorktree = "worktree"

	ToolTypeAgenticSystem = "agentic-system"

	ResourceTypeModel = "model"
)

const (
	defaultLocalEnvironmentName    = "local"
	defaultWorktreeEnvironmentName = "worktree"
)

type ConfigFileSource string

const (
	ConfigFileSourceGlobal ConfigFileSource = "global"
	ConfigFileSourceLocal  ConfigFileSource = "local"
)

type ReadFileFunc func(string) ([]byte, error)

type ExecutionResourceLayer struct {
	Source        ConfigFileSource
	Path          string
	Config        model.ResourceConfigFile
	GitConfigured bool
}

type ExecutionResourceConfig struct {
	Config             model.ResourceConfigFile
	Layers             []ExecutionResourceLayer
	EnvironmentSources map[string]ConfigFileSource
	ToolSources        map[string]ConfigFileSource
	ResourceSources    map[string]ConfigFileSource
	BindingSources     map[string]ConfigFileSource
	GitSource          ConfigFileSource
	ConfigHome         string
}

func NewExecutionResourceConfigFile() model.ResourceConfigFile {
	return normalizeExecutionResourceConfig(model.ResourceConfigFile{
		Defaults:     model.ResourceDefaultsConfig{},
		Environments: map[string]model.EnvironmentConfig{},
		Tools:        map[string]model.ToolConfig{},
		Resources:    map[string]model.ResourceConfig{},
		Bindings:     map[string]model.ResourceBindingConfig{},
	}, false)
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
	merged.ConfigHome = home
	if err := ValidateExecutionResourceConfig(merged.Config); err != nil {
		return ExecutionResourceConfig{}, fmt.Errorf("invalid execution resource config after merge of %d layers: %w", len(layers), err)
	}

	return merged, nil
}

func ExecutionResourceConfigPath(repoRoot, configHome string, source ConfigFileSource) (string, error) {
	switch source {
	case ConfigFileSourceLocal:
		if strings.TrimSpace(repoRoot) == "" {
			return "", fmt.Errorf("repo root is required for local execution resource config")
		}
		return filepath.Join(repoRoot, executionLocalFilePath), nil
	case ConfigFileSourceGlobal:
		home, err := resolveConfigHome(configHome)
		if err != nil {
			return "", err
		}
		return filepath.Join(home, executionConfigPath), nil
	default:
		return "", fmt.Errorf("unknown config source: %s", source)
	}
}

func LoadExecutionResourceConfigFile(path string, readFile ReadFileFunc) (model.ResourceConfigFile, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}

	content, err := readFile(path)
	if err != nil {
		return model.ResourceConfigFile{}, err
	}

	config, err := parseExecutionResourceConfig(path, content)
	if err != nil {
		return model.ResourceConfigFile{}, err
	}

	return config, nil
}

func NormalizeExecutionResourceConfig(config model.ResourceConfigFile) model.ResourceConfigFile {
	return normalizeExecutionResourceConfig(config, true)
}

func NormalizeExecutionResourceLayerConfig(config model.ResourceConfigFile) model.ResourceConfigFile {
	return normalizeExecutionResourceConfig(config, false)
}

func ValidateExecutionResourceConfig(config model.ResourceConfigFile) error {
	config = normalizeExecutionResourceConfig(config, true)

	if len(config.Environments) == 0 {
		return fmt.Errorf("environments must define at least one entry")
	}
	for name, environment := range config.Environments {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("environments contains empty name")
		}
		if strings.TrimSpace(environment.Type) == "" {
			return fmt.Errorf("environment %q has empty type", name)
		}
		if environment.Enabled && !isSupportedEnvironmentType(environment.Type) {
			return fmt.Errorf("environment %q has unsupported type %q", name, environment.Type)
		}
	}

	if len(config.Tools) == 0 {
		return fmt.Errorf("tools must define at least one entry")
	}
	for name, tool := range config.Tools {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("tools contains empty name")
		}
		if strings.TrimSpace(tool.Type) == "" {
			return fmt.Errorf("tool %q has empty type", name)
		}
	}

	if len(config.Resources) == 0 {
		return fmt.Errorf("resources must define at least one entry")
	}
	for name, resource := range config.Resources {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("resources contains empty name")
		}
		if strings.TrimSpace(resource.Type) == "" {
			return fmt.Errorf("resource %q has empty type", name)
		}
		for _, tool := range resource.Tools {
			if _, ok := config.Tools[strings.TrimSpace(tool)]; !ok {
				return fmt.Errorf("resource %q references unknown tool %q", name, strings.TrimSpace(tool))
			}
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

		toolName := bindingTool(binding)
		resourceName := bindingResource(binding)
		if toolName == "" {
			return fmt.Errorf("binding %q has empty tool", name)
		}
		if resourceName == "" {
			return fmt.Errorf("binding %q has empty resource", name)
		}
		if _, ok := config.Tools[toolName]; !ok {
			if strings.TrimSpace(binding.Runner) == toolName {
				return fmt.Errorf("binding %q references unknown runner %q", name, toolName)
			}
			return fmt.Errorf("binding %q references unknown tool %q", name, toolName)
		}
		resource, ok := config.Resources[resourceName]
		if !ok {
			if strings.TrimSpace(binding.Model) == resourceName {
				return fmt.Errorf("binding %q references unknown model %q", name, resourceName)
			}
			return fmt.Errorf("binding %q references unknown resource %q", name, resourceName)
		}
		if !resourceAllowsTool(resource, toolName) {
			return fmt.Errorf("binding %q uses tool %q that is not allowed for resource %q", name, toolName, resourceName)
		}
		if environment := strings.TrimSpace(binding.Environment); environment != "" {
			if _, ok := config.Environments[environment]; !ok {
				return fmt.Errorf("binding %q references unknown environment %q", name, environment)
			}
		}
	}

	if defaultBinding := strings.TrimSpace(config.Defaults.ModelBinding); defaultBinding != "" {
		if _, ok := config.Bindings[defaultBinding]; !ok {
			return fmt.Errorf("defaults.model-binding references unknown binding %q", defaultBinding)
		}
	}
	if defaultEnvironment := strings.TrimSpace(config.Defaults.Environment); defaultEnvironment != "" {
		if _, ok := config.Environments[defaultEnvironment]; !ok {
			return fmt.Errorf("defaults.environment references unknown environment %q", defaultEnvironment)
		}
	}
	if err := validateGitConfig(config.Git); err != nil {
		return err
	}

	return nil
}

func readLayer(path string, source ConfigFileSource, readFile ReadFileFunc) (ExecutionResourceLayer, error) {
	content, err := readFile(path)
	if err != nil {
		return ExecutionResourceLayer{}, err
	}

	config, err := parseExecutionResourceConfig(path, content)
	if err != nil {
		return ExecutionResourceLayer{}, err
	}
	if err := validateExecutionResourceLayer(config); err != nil {
		return ExecutionResourceLayer{}, fmt.Errorf("invalid execution resource config %s: %w", path, err)
	}

	return ExecutionResourceLayer{Source: source, Path: path, Config: config, GitConfigured: executionResourceLayerHasGit(content)}, nil
}

func executionResourceLayerHasGit(content []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return false
	}
	_, ok := raw["git"]
	return ok
}

func parseExecutionResourceConfig(path string, content []byte) (model.ResourceConfigFile, error) {
	var config model.ResourceConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return model.ResourceConfigFile{}, fmt.Errorf("parse execution resource config %s: %w", path, err)
	}

	return normalizeExecutionResourceConfig(config, false), nil
}

func validateExecutionResourceLayer(config model.ResourceConfigFile) error {
	config = normalizeExecutionResourceConfig(config, false)

	for _, name := range config.Runners {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("runners contains empty name")
		}
	}
	for _, name := range config.Models {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("models contains empty name")
		}
	}
	for name, environment := range config.Environments {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("environments contains empty name")
		}
		if strings.TrimSpace(environment.Type) == "" {
			return fmt.Errorf("environment %q has empty type", name)
		}
		if environment.Enabled && !isSupportedEnvironmentType(environment.Type) {
			return fmt.Errorf("environment %q has unsupported type %q", name, environment.Type)
		}
	}
	for name, tool := range config.Tools {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("tools contains empty name")
		}
		if strings.TrimSpace(tool.Type) == "" {
			return fmt.Errorf("tool %q has empty type", name)
		}
	}
	for name, resource := range config.Resources {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("resources contains empty name")
		}
		if strings.TrimSpace(resource.Type) == "" {
			return fmt.Errorf("resource %q has empty type", name)
		}
	}
	for name, binding := range config.Bindings {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("bindings contains empty name")
		}
		if bindingTool(binding) == "" {
			return fmt.Errorf("binding %q has empty tool", name)
		}
		if bindingResource(binding) == "" {
			return fmt.Errorf("binding %q has empty resource", name)
		}
	}
	if err := validateGitConfig(config.Git); err != nil {
		return err
	}

	return nil
}

func mergeExecutionResourceLayers(layers []ExecutionResourceLayer) ExecutionResourceConfig {
	merged := model.ResourceConfigFile{
		Defaults:     model.ResourceDefaultsConfig{},
		Environments: map[string]model.EnvironmentConfig{},
		Tools:        map[string]model.ToolConfig{},
		Resources:    map[string]model.ResourceConfig{},
		Bindings:     map[string]model.ResourceBindingConfig{},
	}
	environmentSources := map[string]ConfigFileSource{}
	toolSources := map[string]ConfigFileSource{}
	resourceSources := map[string]ConfigFileSource{}
	bindingSources := map[string]ConfigFileSource{}
	var gitSource ConfigFileSource

	for _, layer := range layers {
		config := normalizeExecutionResourceConfig(layer.Config, false)

		merged.Runners = mergeNameList(merged.Runners, config.Runners)
		merged.Models = mergeNameList(merged.Models, config.Models)
		for name, environment := range config.Environments {
			merged.Environments[name] = environment
			environmentSources[name] = layer.Source
		}
		for name, tool := range config.Tools {
			merged.Tools[name] = tool
			toolSources[name] = layer.Source
		}
		for name, resource := range config.Resources {
			merged.Resources[name] = resource
			resourceSources[name] = layer.Source
		}
		for name, binding := range config.Bindings {
			merged.Bindings[name] = binding
			bindingSources[name] = layer.Source
		}
		if strings.TrimSpace(config.Defaults.ModelBinding) != "" {
			merged.Defaults.ModelBinding = config.Defaults.ModelBinding
		}
		if strings.TrimSpace(config.Defaults.Environment) != "" {
			merged.Defaults.Environment = config.Defaults.Environment
		}
		if hasPrivateStoreConfig(config.PrivateStore) {
			merged.PrivateStore = mergePrivateStoreConfig(merged.PrivateStore, config.PrivateStore)
		}
		if layer.GitConfigured {
			merged.Git = config.Git
			gitSource = layer.Source
		}
	}

	merged = normalizeExecutionResourceConfig(merged, true)

	return ExecutionResourceConfig{
		Config:             merged,
		Layers:             layers,
		EnvironmentSources: environmentSources,
		ToolSources:        toolSources,
		ResourceSources:    resourceSources,
		BindingSources:     bindingSources,
		GitSource:          gitSource,
	}
}

func hasPrivateStoreConfig(config model.ResourcePrivateStoreConfig) bool {
	return strings.TrimSpace(config.Type) != "" || strings.TrimSpace(config.Service) != "" || strings.TrimSpace(config.Path) != ""
}

func mergePrivateStoreConfig(base, override model.ResourcePrivateStoreConfig) model.ResourcePrivateStoreConfig {
	merged := base
	if strings.TrimSpace(override.Type) != "" {
		merged.Type = override.Type
	}
	if strings.TrimSpace(override.Service) != "" {
		merged.Service = override.Service
	}
	if strings.TrimSpace(override.Path) != "" {
		merged.Path = override.Path
	}
	return merged
}

func normalizeExecutionResourceConfig(config model.ResourceConfigFile, addDefaultEnvironments bool) model.ResourceConfigFile {
	normalized := config
	normalized.Defaults.ModelBinding = strings.TrimSpace(normalized.Defaults.ModelBinding)
	normalized.Defaults.Environment = strings.TrimSpace(normalized.Defaults.Environment)
	normalized.PrivateStore.Type = strings.TrimSpace(normalized.PrivateStore.Type)
	normalized.PrivateStore.Service = strings.TrimSpace(normalized.PrivateStore.Service)
	normalized.PrivateStore.Path = strings.TrimSpace(normalized.PrivateStore.Path)

	normalized.Runners = normalizeStringList(normalized.Runners)
	normalized.Models = normalizeStringList(normalized.Models)

	normalized.Environments = normalizeEnvironments(normalized.Environments)
	normalized.Tools = normalizeTools(normalized.Tools)
	normalized.Resources = normalizeResources(normalized.Resources)
	normalized.Bindings = normalizeBindings(normalized.Bindings)
	normalized.Git = normalizeGitConfig(normalized.Git)

	if addDefaultEnvironments {
		if _, ok := normalized.Environments[defaultLocalEnvironmentName]; !ok {
			normalized.Environments[defaultLocalEnvironmentName] = model.EnvironmentConfig{
				Type:    EnvironmentTypeLocal,
				Enabled: true,
			}
		}
		if _, ok := normalized.Environments[defaultWorktreeEnvironmentName]; !ok {
			normalized.Environments[defaultWorktreeEnvironmentName] = model.EnvironmentConfig{
				Type:    EnvironmentTypeWorktree,
				Enabled: true,
			}
		}
	}

	for _, name := range normalized.Runners {
		if _, ok := normalized.Tools[name]; !ok {
			normalized.Tools[name] = model.ToolConfig{Type: ToolTypeAgenticSystem, Enabled: true}
		}
	}
	for _, name := range normalized.Models {
		if _, ok := normalized.Resources[name]; !ok {
			normalized.Resources[name] = model.ResourceConfig{Type: ResourceTypeModel, Enabled: true}
		}
	}

	normalized.Runners = mergeNameList(normalized.Runners, sortedMapKeys(normalized.Tools))
	normalized.Models = mergeNameList(normalized.Models, sortedMapKeys(normalized.Resources))

	return normalized
}

func normalizeEnvironments(values map[string]model.EnvironmentConfig) map[string]model.EnvironmentConfig {
	normalized := map[string]model.EnvironmentConfig{}
	for name, environment := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			normalized[name] = environment
			continue
		}
		environment.Type = strings.TrimSpace(environment.Type)
		if environment.Type == "" {
			switch name {
			case defaultLocalEnvironmentName:
				environment.Type = EnvironmentTypeLocal
			case defaultWorktreeEnvironmentName:
				environment.Type = EnvironmentTypeWorktree
			}
		}
		normalized[name] = environment
	}
	return normalized
}

func normalizeTools(values map[string]model.ToolConfig) map[string]model.ToolConfig {
	normalized := map[string]model.ToolConfig{}
	for name, tool := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			normalized[name] = tool
			continue
		}
		tool.Type = strings.TrimSpace(tool.Type)
		if tool.Type == "" {
			tool.Type = ToolTypeAgenticSystem
		}
		normalized[name] = tool
	}
	return normalized
}

func normalizeResources(values map[string]model.ResourceConfig) map[string]model.ResourceConfig {
	normalized := map[string]model.ResourceConfig{}
	for name, resource := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			normalized[name] = resource
			continue
		}
		resource.Type = strings.TrimSpace(resource.Type)
		if resource.Type == "" {
			resource.Type = ResourceTypeModel
		}
		resource.Tools = normalizeStringList(resource.Tools)
		resource.Traits = normalizeStringList(resource.Traits)
		normalized[name] = resource
	}
	return normalized
}

func normalizeBindings(values map[string]model.ResourceBindingConfig) map[string]model.ResourceBindingConfig {
	normalized := map[string]model.ResourceBindingConfig{}
	for name, binding := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			normalized[name] = binding
			continue
		}
		binding.Runner = strings.TrimSpace(binding.Runner)
		binding.Model = strings.TrimSpace(binding.Model)
		binding.Tool = strings.TrimSpace(binding.Tool)
		binding.Resource = strings.TrimSpace(binding.Resource)
		binding.Environment = strings.TrimSpace(binding.Environment)
		if binding.Tool == "" {
			binding.Tool = binding.Runner
		}
		if binding.Runner == "" {
			binding.Runner = binding.Tool
		}
		if binding.Resource == "" {
			binding.Resource = binding.Model
		}
		if binding.Model == "" {
			binding.Model = binding.Resource
		}
		normalized[name] = binding
	}
	return normalized
}

func normalizeGitConfig(config *model.GitConfig) *model.GitConfig {
	if config == nil {
		return nil
	}
	normalized := *config
	if normalized.Identity != nil {
		identity := *normalized.Identity
		identity.AuthorName = strings.TrimSpace(identity.AuthorName)
		identity.AuthorEmail = strings.TrimSpace(identity.AuthorEmail)
		identity.CommitterName = strings.TrimSpace(identity.CommitterName)
		identity.CommitterEmail = strings.TrimSpace(identity.CommitterEmail)
		normalized.Identity = &identity
	}
	if normalized.Signing != nil {
		signing := *normalized.Signing
		signing.Format = strings.TrimSpace(signing.Format)
		signing.SigningKey = strings.TrimSpace(signing.SigningKey)
		signing.Program = strings.TrimSpace(signing.Program)
		normalized.Signing = &signing
	}
	if normalized.Push != nil {
		push := *normalized.Push
		push.SSHIdentityFile = strings.TrimSpace(push.SSHIdentityFile)
		push.SSHIdentityPrivate = strings.TrimSpace(push.SSHIdentityPrivate)
		push.KnownHostsFile = strings.TrimSpace(push.KnownHostsFile)
		push.RetryDelay = strings.TrimSpace(push.RetryDelay)
		normalized.Push = &push
	}
	if isGitConfigEmpty(&normalized) {
		return nil
	}
	return &normalized
}

func validateGitConfig(config *model.GitConfig) error {
	if config == nil {
		return nil
	}
	if identity := config.Identity; identity != nil {
		values := []string{identity.AuthorName, identity.AuthorEmail, identity.CommitterName, identity.CommitterEmail}
		filled := 0
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				filled++
			}
		}
		if filled > 0 && filled != len(values) {
			return fmt.Errorf("git.identity must define author-name, author-email, committer-name and committer-email together")
		}
	}
	if signing := config.Signing; signing != nil && signing.Enabled {
		if strings.TrimSpace(signing.Format) == "" {
			return fmt.Errorf("git.signing.format is required when git.signing.enabled is true")
		}
		if strings.TrimSpace(signing.SigningKey) == "" {
			return fmt.Errorf("git.signing.signing-key is required when git.signing.enabled is true")
		}
	}
	if push := config.Push; push != nil && strings.TrimSpace(push.SSHIdentityFile) != "" && strings.TrimSpace(push.SSHIdentityPrivate) != "" {
		return fmt.Errorf("git.push must define only one of ssh-identity-file and ssh-identity-private")
	}
	if push := config.Push; push != nil {
		if push.MaxAttempts < 0 || (push.MaxAttempts == 0 && push.MaxAttemptsSet) {
			return fmt.Errorf("git.push.max-attempts must be greater than zero")
		}
		if value := strings.TrimSpace(push.RetryDelay); value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("git.push.retry-delay must be a duration: %w", err)
			}
			if parsed < 0 {
				return fmt.Errorf("git.push.retry-delay must not be negative")
			}
		} else if push.RetryDelaySet {
			return fmt.Errorf("git.push.retry-delay must not be empty")
		}
	}
	return nil
}

func isGitConfigEmpty(config *model.GitConfig) bool {
	if config == nil {
		return true
	}
	identityEmpty := config.Identity == nil || (config.Identity.AuthorName == "" && config.Identity.AuthorEmail == "" && config.Identity.CommitterName == "" && config.Identity.CommitterEmail == "")
	signingEmpty := config.Signing == nil || (!config.Signing.Enabled && config.Signing.Format == "" && config.Signing.SigningKey == "" && config.Signing.Program == "")
	pushEmpty := config.Push == nil || (config.Push.SSHIdentityFile == "" && config.Push.SSHIdentityPrivate == "" && config.Push.KnownHostsFile == "" && !config.Push.IdentitiesOnly && !config.Push.AllowForceWithLease && config.Push.MaxAttempts == 0 && !config.Push.MaxAttemptsSet && config.Push.RetryDelay == "" && !config.Push.RetryDelaySet)
	return identityEmpty && signingEmpty && pushEmpty
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			normalized = append(normalized, value)
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func mergeNameList(existing []string, additions []string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0, len(existing)+len(additions))
	for _, value := range existing {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	return merged
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	return containsName(resource.Tools, tool)
}

func isSupportedEnvironmentType(value string) bool {
	switch strings.TrimSpace(value) {
	case EnvironmentTypeLocal, EnvironmentTypeWorktree:
		return true
	default:
		return false
	}
}

func containsName(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}
