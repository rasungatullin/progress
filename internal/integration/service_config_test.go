package integration

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rasungatullin/progress/internal/configuration"
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
