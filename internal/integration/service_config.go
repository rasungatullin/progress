package integration

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
)

func NewConfiguredService(logger *log.Logger) *Service {
	logger = ensureLogger(logger)
	repoRoot, err := resolveRepoRoot(context.Background())
	if err != nil {
		logger.Printf("Контур интеграции использует переходный режим: конфигурация систем недоступна: %v", err)
		return NewService(logger)
	}

	loaded, err := configuration.LoadIntegrationConfig(repoRoot, os.ReadFile)
	if err != nil {
		if isMissingIntegrationConfig(err) {
			return NewService(logger)
		}
		logger.Printf("Контур интеграции использует переходный режим: конфигурация систем не загружена: %v", err)
		return NewService(logger)
	}

	return NewServiceFromConfig(logger, loaded.Config)
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
