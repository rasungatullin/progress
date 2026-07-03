package configuration

import (
	"context"
	"strings"
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

type Snapshot struct {
	Integration        *IntegrationConfig
	ExecutionResources *ExecutionResourceConfig
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

	return snapshot, nil
}
