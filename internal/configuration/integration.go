package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

const (
	integrationConfigPath    = "integration/systems.json"
	integrationLocalFilePath = ".progress/integration/systems.json"
)

type IntegrationConfigLayer struct {
	Source ConfigFileSource
	Path   string
	Config integrationmodel.IntegrationConfigFile
}

type IntegrationConfig struct {
	Config           integrationmodel.IntegrationConfigFile
	Layers           []IntegrationConfigLayer
	SystemSources    map[string]ConfigFileSource
	GlobalConfigPath string
	LocalConfigPath  string
}

func LoadIntegrationConfig(repoRoot string, readFile ReadFileFunc) (IntegrationConfig, error) {
	return LoadIntegrationConfigWithHome(repoRoot, "", readFile)
}

func LoadIntegrationConfigWithHome(repoRoot, configHome string, readFile ReadFileFunc) (IntegrationConfig, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}

	home, globalHomeErr := resolveConfigHome(configHome)

	globalPath := ""
	useGlobalLayer := globalHomeErr == nil
	localPath := ""
	useLocalLayer := strings.TrimSpace(repoRoot) != ""
	if useLocalLayer {
		localPath = filepath.Join(repoRoot, integrationLocalFilePath)
	}

	if useGlobalLayer {
		globalPath = filepath.Join(home, integrationConfigPath)
	}

	var layers []IntegrationConfigLayer

	if useGlobalLayer {
		if config, err := readIntegrationLayer(globalPath, ConfigFileSourceGlobal, readFile); err == nil {
			layers = append(layers, config)
		} else if !isNotExistErr(err) {
			return IntegrationConfig{}, err
		}
	}

	if useLocalLayer {
		if config, err := readIntegrationLayer(localPath, ConfigFileSourceLocal, readFile); err == nil {
			layers = append(layers, config)
		} else if !isNotExistErr(err) {
			return IntegrationConfig{}, err
		}
	}

	if len(layers) == 0 {
		if useGlobalLayer && useLocalLayer {
			return IntegrationConfig{}, fmt.Errorf("integration config not found: global=%s local=%s", globalPath, localPath)
		}
		if useGlobalLayer {
			return IntegrationConfig{}, fmt.Errorf("integration config not found: global=%s", globalPath)
		}
		if useLocalLayer {
			return IntegrationConfig{}, fmt.Errorf("integration config not found: global layer unavailable (%v) local=%s", globalHomeErr, localPath)
		}
		return IntegrationConfig{}, fmt.Errorf("integration config not found: global layer unavailable (%v)", globalHomeErr)
	}

	merged := mergeIntegrationLayers(layers)
	if err := validateIntegrationConfig(merged.Config); err != nil {
		return IntegrationConfig{}, fmt.Errorf("invalid integration config after merge of %d layers: %w", len(layers), err)
	}

	merged.GlobalConfigPath = getIntegrationLayerPath(merged.Layers, ConfigFileSourceGlobal)
	merged.LocalConfigPath = getIntegrationLayerPath(merged.Layers, ConfigFileSourceLocal)
	return merged, nil
}

func readIntegrationLayer(path string, source ConfigFileSource, readFile ReadFileFunc) (IntegrationConfigLayer, error) {
	content, err := readFile(path)
	if err != nil {
		return IntegrationConfigLayer{}, err
	}

	var config integrationmodel.IntegrationConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return IntegrationConfigLayer{}, fmt.Errorf("parse integration config %s: %w", path, err)
	}
	if err := validateIntegrationLayer(config); err != nil {
		return IntegrationConfigLayer{}, fmt.Errorf("invalid integration config %s: %w", path, err)
	}

	return IntegrationConfigLayer{Source: source, Path: path, Config: config}, nil
}

func mergeIntegrationLayers(layers []IntegrationConfigLayer) IntegrationConfig {
	merged := integrationmodel.IntegrationConfigFile{
		Systems: map[string]integrationmodel.IntegrationSystemConfig{},
	}
	sources := map[string]ConfigFileSource{}

	for _, layer := range layers {
		if strings.TrimSpace(layer.Config.DefaultSystem) != "" {
			merged.DefaultSystem = strings.TrimSpace(layer.Config.DefaultSystem)
		}
		if len(layer.Config.DefaultSystems) > 0 {
			if merged.DefaultSystems == nil {
				merged.DefaultSystems = map[string]string{}
			}
			for integrationType, system := range layer.Config.DefaultSystems {
				integrationType = normalizeIntegrationTypeName(integrationType)
				system = normalizeSystemName(system)
				if integrationType == "" || system == "" {
					continue
				}
				merged.DefaultSystems[integrationType] = system
			}
		}

		for name, system := range layer.Config.Systems {
			name = normalizeSystemName(name)
			base, ok := merged.Systems[name]
			if !ok {
				base = integrationmodel.IntegrationSystemConfig{}
			}
			merged.Systems[name] = mergeIntegrationSystemConfig(base, system)
			sources[name] = layer.Source
		}
	}

	return IntegrationConfig{
		Config:        merged,
		Layers:        layers,
		SystemSources: sources,
	}
}

func mergeIntegrationSystemConfig(base, override integrationmodel.IntegrationSystemConfig) integrationmodel.IntegrationSystemConfig {
	merged := base

	if value := strings.TrimSpace(override.Type); value != "" {
		merged.Type = value
	}
	if value := strings.TrimSpace(override.IntegrationType); value != "" {
		merged.IntegrationType = value
	}
	if len(override.IntegrationTypes) > 0 {
		merged.IntegrationTypes = append([]string(nil), override.IntegrationTypes...)
	}
	if override.Default {
		merged.Default = true
	}
	if override.Enabled != nil {
		value := *override.Enabled
		merged.Enabled = &value
	}
	if value := strings.TrimSpace(override.Command); value != "" {
		merged.Command = value
	}
	if value := strings.TrimSpace(override.Path); value != "" {
		merged.Path = value
	}
	if value := strings.TrimSpace(override.Timeout); value != "" {
		merged.Timeout = value
	}
	if value := strings.TrimSpace(override.BaseURL); value != "" {
		merged.BaseURL = value
	}
	if value := strings.TrimSpace(override.Token); value != "" {
		merged.Token = value
	}
	if value := strings.TrimSpace(override.TokenEnv); value != "" {
		merged.TokenEnv = value
	}
	if value := strings.TrimSpace(override.Username); value != "" {
		merged.Username = value
	}
	if value := strings.TrimSpace(override.Repository); value != "" {
		merged.Repository = value
	}
	if value := strings.TrimSpace(override.Workspace); value != "" {
		merged.Workspace = value
	}
	if value := strings.TrimSpace(override.Project); value != "" {
		merged.Project = value
	}
	if value := strings.TrimSpace(override.DefaultRepo); value != "" {
		merged.DefaultRepo = value
	}
	if value := strings.TrimSpace(override.ChannelID); value != "" {
		merged.ChannelID = value
	}
	if value := strings.TrimSpace(override.ChatID); value != "" {
		merged.ChatID = value
	}

	if len(base.Operations) > 0 || len(override.Operations) > 0 {
		merged.Operations = map[string]integrationmodel.IntegrationOperationConfig{}
		for name, operation := range base.Operations {
			merged.Operations[normalizeOperationName(name)] = operation
		}
		for name, operation := range override.Operations {
			name = normalizeOperationName(name)
			merged.Operations[name] = mergeIntegrationOperationConfig(merged.Operations[name], operation)
		}
	}

	return merged
}

func mergeIntegrationOperationConfig(base, override integrationmodel.IntegrationOperationConfig) integrationmodel.IntegrationOperationConfig {
	merged := base
	if value := strings.TrimSpace(override.Type); value != "" {
		merged.Type = value
	}
	if value := strings.TrimSpace(override.Command); value != "" {
		merged.Command = value
	}
	if value := strings.TrimSpace(override.Path); value != "" {
		merged.Path = value
	}
	if value := strings.TrimSpace(override.Timeout); value != "" {
		merged.Timeout = value
	}
	if value := strings.TrimSpace(override.Script); value != "" {
		merged.Script = value
	}
	return merged
}

func validateIntegrationLayer(config integrationmodel.IntegrationConfigFile) error {
	for integrationType, system := range config.DefaultSystems {
		if normalizeIntegrationTypeName(integrationType) == "" {
			return fmt.Errorf("default_systems contains empty integration type")
		}
		if normalizeSystemName(system) == "" {
			return fmt.Errorf("default_systems.%s contains empty system name", normalizeIntegrationTypeName(integrationType))
		}
	}
	for name, system := range config.Systems {
		if normalizeSystemName(name) == "" {
			return fmt.Errorf("systems contains empty name")
		}
		if err := validateIntegrationTypeList(system, name); err != nil {
			return err
		}
		if err := validateIntegrationOperations(system.Operations, name); err != nil {
			return err
		}
	}
	return nil
}

func validateIntegrationConfig(config integrationmodel.IntegrationConfigFile) error {
	if len(config.Systems) == 0 {
		return fmt.Errorf("systems must not be empty")
	}

	for name, system := range config.Systems {
		name = normalizeSystemName(name)
		if name == "" {
			return fmt.Errorf("systems contains empty name")
		}
		if err := validateIntegrationSystemType(name, system.Type); err != nil {
			return err
		}
		if !isSystemEnabled(system) {
			continue
		}
		if strings.TrimSpace(system.Type) == "" {
			return fmt.Errorf("system %q must define type when enabled", name)
		}
		if err := validateIntegrationOperations(system.Operations, name); err != nil {
			return err
		}
	}

	if defaultSystem := normalizeSystemName(config.DefaultSystem); defaultSystem != "" {
		system, ok := config.Systems[defaultSystem]
		if !ok {
			return fmt.Errorf("default_system references unknown system %q", defaultSystem)
		}
		if !isSystemEnabled(system) {
			return fmt.Errorf("default_system references disabled system %q", defaultSystem)
		}
	}
	for integrationType, defaultSystem := range config.DefaultSystems {
		integrationType = normalizeIntegrationTypeName(integrationType)
		defaultSystem = normalizeSystemName(defaultSystem)
		system, ok := config.Systems[defaultSystem]
		if !ok {
			return fmt.Errorf("default_systems.%s references unknown system %q", integrationType, defaultSystem)
		}
		if !isSystemEnabled(system) {
			return fmt.Errorf("default_systems.%s references disabled system %q", integrationType, defaultSystem)
		}
		if !systemDeclaresIntegrationType(system, integrationType) {
			return fmt.Errorf("default_systems.%s references system %q without matching integration type", integrationType, defaultSystem)
		}
	}

	return nil
}

func validateIntegrationOperations(operations map[string]integrationmodel.IntegrationOperationConfig, systemName string) error {
	for name := range operations {
		if normalizeOperationName(name) == "" {
			return fmt.Errorf("system %q contains operation with empty name", normalizeSystemName(systemName))
		}
	}
	return nil
}

func validateIntegrationSystemType(systemName, systemType string) error {
	systemType = normalizeSystemName(systemType)
	if systemType == "" {
		return nil
	}

	switch systemType {
	case "github", "bitbucket", "mattermost", "telegram", "script":
		return nil
	default:
		return fmt.Errorf("system %q uses unknown type %q", normalizeSystemName(systemName), systemType)
	}
}

func validateIntegrationTypeList(_ integrationmodel.IntegrationSystemConfig, _ string) error {
	return nil
}

func systemDeclaresIntegrationType(system integrationmodel.IntegrationSystemConfig, integrationType string) bool {
	integrationType = normalizeIntegrationTypeName(integrationType)
	if integrationType == "" {
		return true
	}
	for _, value := range append([]string{system.IntegrationType}, system.IntegrationTypes...) {
		if normalizeIntegrationTypeName(value) == integrationType {
			return true
		}
	}
	if strings.TrimSpace(system.IntegrationType) != "" || len(system.IntegrationTypes) > 0 {
		return false
	}
	switch normalizeSystemName(system.Type) {
	case "github":
		return integrationType == "tracker" || integrationType == "repository"
	case "bitbucket":
		return integrationType == "repository"
	case "mattermost", "telegram":
		return integrationType == "messenger"
	default:
		return true
	}
}

func isSystemEnabled(system integrationmodel.IntegrationSystemConfig) bool {
	return system.Enabled == nil || *system.Enabled
}

func normalizeSystemName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func normalizeOperationName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func normalizeIntegrationTypeName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func getIntegrationLayerPath(layers []IntegrationConfigLayer, source ConfigFileSource) string {
	for _, layer := range layers {
		if layer.Source == source {
			return layer.Path
		}
	}
	return ""
}
