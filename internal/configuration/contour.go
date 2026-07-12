package configuration

import (
	"context"
	"sort"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration/secrets"
	"github.com/rasungatullin/progress/internal/execution/model"
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
	if snapshot.Integration != nil {
		config := snapshot.Integration.Config
		for name, system := range config.Systems {
			system.Token = ""
			system.GitHubAppPrivateKey = ""
			config.Systems[name] = system
		}
		snapshot.Integration.Config = config
	}
}
