package configuration

import (
	"fmt"
	"os"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

// LoadPrivateStoreConfig загружает общую настройку хранилища приватных значений.
// Поле в старой конфигурации интеграции читается только как переходный вариант.
func LoadPrivateStoreConfig(repoRoot, configHome string, readFile ReadFileFunc) (model.ResourcePrivateStoreConfig, string, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}

	resources, err := LoadExecutionResourceConfigWithHome(repoRoot, configHome, readFile)
	if err == nil {
		if hasPrivateStoreConfig(resources.Config.PrivateStore) {
			return resources.Config.PrivateStore, resources.ConfigHome, nil
		}
	} else if !strings.Contains(err.Error(), "execution resource config not found") {
		return model.ResourcePrivateStoreConfig{}, "", fmt.Errorf("load settings and resources contour: %w", err)
	}

	legacy, legacyErr := LoadIntegrationPrivateStoreConfigWithHome(repoRoot, configHome, readFile)
	if legacyErr != nil {
		return model.ResourcePrivateStoreConfig{}, "", legacyErr
	}
	return legacy.Config, legacy.ConfigHome, nil
}
