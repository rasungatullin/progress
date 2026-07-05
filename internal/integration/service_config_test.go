package integration

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/integration/model"
	"github.com/rasungatullin/progress/internal/logging"
)

func TestNewConfiguredServiceDisablesProvidersOnInvalidConfig(t *testing.T) {
	t.Parallel()

	originalResolveRepoRoot := configuredServiceResolveRepoRoot
	originalLoadIntegrationConfig := configuredServiceLoadIntegrationConfig
	configuredServiceResolveRepoRoot = func(context.Context) (string, error) {
		return "/repo", nil
	}
	configuredServiceLoadIntegrationConfig = func(string, configuration.ReadFileFunc) (configuration.IntegrationConfig, error) {
		return configuration.IntegrationConfig{}, errors.New("invalid integration config after merge of 2 layers: default_system references disabled system \"github\"")
	}
	t.Cleanup(func() {
		configuredServiceResolveRepoRoot = originalResolveRepoRoot
		configuredServiceLoadIntegrationConfig = originalLoadIntegrationConfig
	})

	service := NewConfiguredService(logging.New(io.Discard))

	route, err := service.Dispatch(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if route.ProviderAvailable {
		t.Fatal("provider must be unavailable when config is invalid")
	}

	_, err = service.Execute(context.Background(), Request{System: "github", Resource: "issue", Operation: "get"})
	if err == nil {
		t.Fatal("expected execute error")
	}
	if err.Error() != "integration provider not registered: github" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewConfiguredServiceLoadsGlobalLayerWhenRepoRootUnavailable(t *testing.T) {
	t.Parallel()

	originalResolveRepoRoot := configuredServiceResolveRepoRoot
	originalLoadIntegrationConfig := configuredServiceLoadIntegrationConfig
	configuredServiceResolveRepoRoot = func(context.Context) (string, error) {
		return "", errors.New("not a git repository")
	}
	configuredServiceLoadIntegrationConfig = func(repoRoot string, readFile configuration.ReadFileFunc) (configuration.IntegrationConfig, error) {
		return configuration.LoadIntegrationConfigWithHome(repoRoot, "/config-home", func(path string) ([]byte, error) {
			switch path {
			case "/config-home/integration/systems.json":
				return []byte(`{"systems":{"enterprise":{"type":"github"}}}`), nil
			default:
				return nil, fs.ErrNotExist
			}
		})
	}
	t.Cleanup(func() {
		configuredServiceResolveRepoRoot = originalResolveRepoRoot
		configuredServiceLoadIntegrationConfig = originalLoadIntegrationConfig
	})

	service := NewConfiguredService(logging.New(io.Discard))

	route, err := service.Dispatch(context.Background(), Request{System: "enterprise", Resource: "issue", Operation: "get"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !route.ProviderAvailable {
		t.Fatal("provider from global layer must be available even without repo root")
	}
}

func TestConfigUsesPrivateValuesDetectsGitHubAppPrivateKey(t *testing.T) {
	t.Parallel()

	config := model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github-app": {
				Type:                       "github",
				GitHubAppPrivateKeyPrivate: "progress_synthesis_pem",
			},
		},
	}

	if !configUsesPrivateValues(config) {
		t.Fatal("expected GitHub App private key reference to require private store")
	}
}

func TestConfigUsesPrivateValuesSkipsGitHubAppPrivateKeyWhenTokenIsConfigured(t *testing.T) {
	t.Parallel()

	config := model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github-app": {
				Type:                       "github",
				TokenEnv:                   "GITHUB_TOKEN",
				GitHubAppPrivateKeyPrivate: "progress_synthesis_pem",
			},
		},
	}

	if configUsesPrivateValues(config) {
		t.Fatal("token_env must not require GitHub App private key store")
	}
}
