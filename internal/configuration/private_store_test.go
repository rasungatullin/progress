package configuration

import (
	"io/fs"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestLoadPrivateStoreConfigPrefersSettingsAndResourcesContour(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/execution/resources.json":
			return []byte(`{"private_store":{"type":"file","path":"private/new.json"},"environments":{"local":{"type":"local","enabled":true}},"tools":{"runner":{"type":"agentic-system","enabled":true}},"resources":{"model":{"type":"model","enabled":true}},"bindings":{"default":{"tool":"runner","resource":"model"}}}`), nil
		case "/config-home/integration/systems.json":
			return []byte(`{"private_store":{"type":"file","path":"private/old.json"}}`), nil
		default:
			return nil, fs.ErrNotExist
		}
	}

	config, home, err := LoadPrivateStoreConfig("", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load private store config: %v", err)
	}
	if home != "/config-home" {
		t.Fatalf("unexpected config home: %q", home)
	}
	if config.Path != "private/new.json" {
		t.Fatalf("new settings and resources contour must win over legacy integration setting: %#v", config)
	}
}

func TestLoadPrivateStoreConfigKeepsLegacyIntegrationSetting(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/config-home/integration/systems.json" {
			return []byte(`{"private_store":{"type":"file","path":"private/old.json"}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	config, _, err := LoadPrivateStoreConfig("", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load legacy private store config: %v", err)
	}
	if config.Path != "private/old.json" {
		t.Fatalf("legacy private store setting was not preserved: %#v", config)
	}
}

func TestLoadPrivateStoreConfigUsesDefaultWhenNoContourConfigExists(t *testing.T) {
	readFile := func(string) ([]byte, error) { return nil, fs.ErrNotExist }

	config, home, err := LoadPrivateStoreConfig("", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load default private store config: %v", err)
	}
	if config != (model.ResourcePrivateStoreConfig{}) {
		t.Fatalf("unexpected default private store config: %#v", config)
	}
	if home != "/config-home" {
		t.Fatalf("unexpected config home: %q", home)
	}
}
