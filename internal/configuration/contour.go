package configuration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration/secrets"
	"github.com/rasungatullin/progress/internal/execution/model"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

type SnapshotInput struct {
	RepoRoot               string
	ConfigHome             string
	LoadIntegration        bool
	LoadExecutionResources bool
}

type SnapshotFailure struct {
	Scope              string
	Message            string
	Retryable          bool
	ManualIntervention bool
}

type PrivateValueSnapshot struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

type Snapshot struct {
	Integration        *IntegrationConfig
	ExecutionResources *ExecutionResourceConfig
	PrivateValues      []PrivateValueSnapshot `json:"private_values,omitempty"`
	Failures           []SnapshotFailure
}

type Service struct {
	readFile ReadFileFunc
}

func NewService(readFile ReadFileFunc) *Service {
	return &Service{readFile: readFile}
}

func (s *Service) Snapshot(ctx context.Context, input SnapshotInput) (Snapshot, error) {
	_ = ctx

	if s == nil {
		s = NewService(nil)
	}

	loadIntegration := input.LoadIntegration
	loadResources := input.LoadExecutionResources
	if !loadIntegration && !loadResources {
		loadIntegration = true
		loadResources = true
	}

	repoRoot := strings.TrimSpace(input.RepoRoot)
	snapshot := Snapshot{}
	if loadIntegration {
		config, err := LoadIntegrationConfigWithHome(repoRoot, input.ConfigHome, s.readFile)
		if err != nil {
			snapshot.Failures = append(snapshot.Failures, SnapshotFailure{
				Scope:              "integration",
				Message:            err.Error(),
				Retryable:          true,
				ManualIntervention: true,
			})
		} else {
			snapshot.Integration = &config
		}
	}
	if loadResources {
		config, err := LoadExecutionResourceConfigWithHome(repoRoot, input.ConfigHome, s.readFile)
		if err != nil {
			snapshot.Failures = append(snapshot.Failures, SnapshotFailure{
				Scope:              "execution-resources",
				Message:            err.Error(),
				Retryable:          true,
				ManualIntervention: true,
			})
		} else {
			snapshot.ExecutionResources = &config
		}
	}
	if snapshot.ExecutionResources != nil || snapshot.Integration != nil {
		config := model.ResourcePrivateStoreConfig{}
		if snapshot.ExecutionResources != nil {
			config = snapshot.ExecutionResources.Config.PrivateStore
		}
		if !hasPrivateStoreConfig(config) && snapshot.Integration != nil {
			config = snapshot.Integration.Config.PrivateStore
		}
		refs := privateValueReferences(snapshot.Integration, snapshot.ExecutionResources)
		if len(refs) > 0 {
			reader, _, err := secrets.NewStore(config, input.ConfigHome)
			for _, name := range refs {
				available := false
				if err == nil {
					value, getErr := reader.Get(ctx, name)
					available = getErr == nil && strings.TrimSpace(value) != ""
				}
				snapshot.PrivateValues = append(snapshot.PrivateValues, PrivateValueSnapshot{Name: name, Available: available})
			}
		}
	}
	redactSnapshot(snapshot)

	return snapshot, nil
}

func privateValueReferences(integration *IntegrationConfig, resources *ExecutionResourceConfig) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" {
			seen[name] = true
		}
	}
	if integration != nil {
		for _, system := range integration.Config.Systems {
			add(system.TokenPrivate)
			add(system.GitHubAppPrivateKeyPrivate)
		}
	}
	if resources != nil && resources.Config.Git != nil && resources.Config.Git.Push != nil {
		add(resources.Config.Git.Push.SSHIdentityPrivate)
	}
	refs := make([]string, 0, len(seen))
	for name := range seen {
		refs = append(refs, name)
	}
	sort.Strings(refs)
	return refs
}

func redactSnapshot(snapshot Snapshot) {
	values := snapshotPrivateValues(snapshot)
	if snapshot.Integration != nil {
		config := snapshot.Integration.Config
		redactIntegrationConfig(&config)
		snapshot.Integration.Config = config
		for index := range snapshot.Integration.Layers {
			redactIntegrationConfig(&snapshot.Integration.Layers[index].Config)
		}
	}
	if snapshot.ExecutionResources != nil {
		redactResourceConfig(&snapshot.ExecutionResources.Config)
		for index := range snapshot.ExecutionResources.Layers {
			redactResourceConfig(&snapshot.ExecutionResources.Layers[index].Config)
		}
	}
	for index := range snapshot.Failures {
		snapshot.Failures[index].Message = secrets.MaskError(
			fmt.Errorf("%s", snapshot.Failures[index].Message), values...,
		).Error()
	}
}

func snapshotPrivateValues(snapshot Snapshot) []string {
	values := make([]string, 0)
	if snapshot.Integration != nil {
		addIntegrationPrivateValues(&values, snapshot.Integration.Config)
		for _, layer := range snapshot.Integration.Layers {
			addIntegrationPrivateValues(&values, layer.Config)
		}
	}
	if snapshot.ExecutionResources != nil {
		addResourcePrivateValues(&values, snapshot.ExecutionResources.Config)
		for _, layer := range snapshot.ExecutionResources.Layers {
			addResourcePrivateValues(&values, layer.Config)
		}
	}
	return values
}

func addIntegrationPrivateValues(values *[]string, config integrationmodel.IntegrationConfigFile) {
	for _, system := range config.Systems {
		if value := strings.TrimSpace(system.Token); value != "" {
			*values = append(*values, value)
		}
		if value := strings.TrimSpace(system.GitHubAppPrivateKey); value != "" {
			*values = append(*values, value)
		}
	}
}

func addResourcePrivateValues(values *[]string, config model.ResourceConfigFile) {
	if config.Git == nil || config.Git.Push == nil {
		return
	}
	if value := strings.TrimSpace(config.Git.Push.SSHIdentityPrivateValue); value != "" {
		*values = append(*values, value)
	}
}

func redactIntegrationConfig(config *integrationmodel.IntegrationConfigFile) {
	if config == nil {
		return
	}
	for name, system := range config.Systems {
		system.Token = ""
		system.GitHubAppPrivateKey = ""
		config.Systems[name] = system
	}
}

func redactResourceConfig(config *model.ResourceConfigFile) {
	if config == nil || config.Git == nil || config.Git.Push == nil {
		return
	}
	config.Git.Push.SSHIdentityPrivateValue = ""
}
