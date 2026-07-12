package integration

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/configuration/secrets"
	"github.com/rasungatullin/progress/internal/integration/model"
)

var (
	configuredServiceResolveRepoRoot       = resolveRepoRoot
	configuredServiceLoadIntegrationConfig = configuration.LoadIntegrationConfig
)

func NewConfiguredService(logger *log.Logger) *Service {
	logger = ensureLogger(logger)
	repoRoot, err := configuredServiceResolveRepoRoot(context.Background())
	if err != nil {
		logger.Printf("Контур интеграции не определил локальный слой конфигурации систем: %v", err)
	}

	loaded, err := configuredServiceLoadIntegrationConfig(repoRoot, os.ReadFile)
	if err != nil {
		if isMissingIntegrationConfig(err) {
			return NewService(logger)
		}
		logger.Printf("Контур интеграции отключил провайдеры из-за ошибки конфигурации систем: %v", err)
		return newEmptyService(logger)
	}

	if system, ok := configUsesLegacyToken(loaded.Config); ok {
		logger.Printf("Контур интеграции не подключил систему %q: фактическое поле token больше не поддерживается; сохраните значение в хранилище приватных значений и укажите ссылку token_private", system)
		return newEmptyService(logger)
	}

	if !configUsesPrivateValues(loaded.Config) {
		return NewServiceFromConfig(logger, loaded.Config)
	}

	privateStoreConfig, configHome, err := configuration.LoadPrivateStoreConfig(repoRoot, loaded.ConfigHome, os.ReadFile)
	if err != nil {
		logger.Printf("Контур интеграции не подключил хранилище приватных значений: %v", err)
		return NewServiceFromConfigWithPrivateStore(logger, loaded.Config, nil)
	}
	store, _, err := secrets.NewStore(privateStoreConfig, configHome)
	if err != nil {
		logger.Printf("Контур интеграции не подключил хранилище приватных значений: %v", err)
		return NewServiceFromConfigWithPrivateStore(logger, loaded.Config, nil)
	}

	return NewServiceFromConfigWithPrivateStore(logger, loaded.Config, store)
}

func ensureLogger(logger *log.Logger) *log.Logger {
	if logger != nil {
		return logger
	}
	return log.Default()
}

func resolveRepoRoot(ctx context.Context) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve git repository root for integration systems: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func isMissingIntegrationConfig(err error) bool {
	return err != nil && strings.Contains(err.Error(), "integration config not found")
}

func configUsesPrivateValues(config model.IntegrationConfigFile) bool {
	for _, system := range config.Systems {
		if strings.TrimSpace(system.TokenPrivate) != "" {
			return true
		}
		if strings.TrimSpace(system.Token) == "" && resolvedTokenEnvValue(system) == "" && strings.TrimSpace(system.GitHubAppPrivateKeyPrivate) != "" {
			return true
		}
	}
	return false
}

func configUsesLegacyToken(config model.IntegrationConfigFile) (string, bool) {
	for name, system := range config.Systems {
		if strings.TrimSpace(system.Token) != "" {
			return strings.TrimSpace(name), true
		}
	}
	return "", false
}
