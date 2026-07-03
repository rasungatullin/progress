package configuration

import (
	"errors"
	"io/fs"
	"testing"
)

func TestLoadIntegrationConfigMergesLayersAndTracksSources(t *testing.T) {
	t.Parallel()

	falseValue := false
	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/integration/systems.json":
			return []byte(`{
				"default_system": "github",
				"systems": {
					"github": {
						"type": "github",
						"command": "gh",
						"timeout": "30s",
						"project": "global-project",
						"repository": "global/repository",
						"default_repo": "global/repo",
						"operations": {
							"issue.get": {"timeout": "20s"},
							"issue.comments": {"type": "script", "script": "./global.sh"}
						}
					},
					"gitlab": {"type": "script", "enabled": true}
				}
			}`), nil
		case "/repo/.progress/integration/systems.json":
			return []byte(`{
				"default_system": "github",
				"systems": {
					"github": {
						"project": "local-project",
						"repository": "local/repository",
						"default_repo": "local/repo",
						"task_label_mapping": {
							"bug": "defect",
							"triage": ""
						},
						"operations": {
							"issue.get": {"timeout": "10s", "command": "/usr/local/bin/gh"}
						}
					},
					"gitlab": {"enabled": false}
				}
			}`), nil
		default:
			return nil, fs.ErrNotExist
		}
	}

	config, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load integration config: %v", err)
	}

	github := config.Config.Systems["github"]
	if config.Config.DefaultSystem != "github" {
		t.Fatalf("unexpected default system: %q", config.Config.DefaultSystem)
	}
	if github.Type != "github" {
		t.Fatalf("unexpected github type: %q", github.Type)
	}
	if github.DefaultRepo != "local/repo" {
		t.Fatalf("unexpected default repo: %q", github.DefaultRepo)
	}
	if github.Repository != "local/repository" {
		t.Fatalf("unexpected repository: %q", github.Repository)
	}
	if github.Project != "local-project" {
		t.Fatalf("unexpected project: %q", github.Project)
	}
	if github.Operations["issue.get"].Timeout != "10s" {
		t.Fatalf("unexpected merged issue.get timeout: %q", github.Operations["issue.get"].Timeout)
	}
	if github.Operations["issue.get"].Command != "/usr/local/bin/gh" {
		t.Fatalf("unexpected merged issue.get command: %q", github.Operations["issue.get"].Command)
	}
	if github.Operations["issue.comments"].Script != "./global.sh" {
		t.Fatalf("expected global issue.comments script to remain, got: %q", github.Operations["issue.comments"].Script)
	}
	if github.TaskLabelMapping["bug"] != "defect" {
		t.Fatalf("unexpected bug mapping: %q", github.TaskLabelMapping["bug"])
	}
	if value, ok := github.TaskLabelMapping["triage"]; !ok || value != "" {
		t.Fatalf("expected triage mapping to ignore label, got value=%q ok=%t", value, ok)
	}
	if config.SystemSources["github"] != ConfigFileSourceLocal {
		t.Fatalf("expected github source local, got: %q", config.SystemSources["github"])
	}
	if config.SystemSources["gitlab"] != ConfigFileSourceLocal {
		t.Fatalf("expected gitlab source local, got: %q", config.SystemSources["gitlab"])
	}
	if config.Config.Systems["gitlab"].Enabled == nil || *config.Config.Systems["gitlab"].Enabled != falseValue {
		t.Fatal("expected local layer to disable gitlab")
	}
	if config.GlobalConfigPath != "/config-home/integration/systems.json" {
		t.Fatalf("unexpected global config path: %q", config.GlobalConfigPath)
	}
	if config.LocalConfigPath != "/repo/.progress/integration/systems.json" {
		t.Fatalf("unexpected local config path: %q", config.LocalConfigPath)
	}
	if len(config.Layers) != 2 {
		t.Fatalf("unexpected layer count: %d", len(config.Layers))
	}
}

func TestLoadIntegrationConfigRejectsDisabledDefaultSystem(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{"default_system":"github","systems":{"github":{"enabled":false}}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config after merge of 1 layers: default_system references disabled system \"github\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadIntegrationConfigUsesLocalLayerWhenGlobalHomeMissing(t *testing.T) {
	originalResolveUserHome := resolveUserHome
	resolveUserHome = func() (string, error) {
		return "", errors.New("home not available")
	}
	t.Cleanup(func() {
		resolveUserHome = originalResolveUserHome
	})

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{"systems":{"github":{"type":"github"}}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	config, err := LoadIntegrationConfigWithHome("/repo", "", readFile)
	if err != nil {
		t.Fatalf("load integration config: %v", err)
	}
	if config.SystemSources["github"] != ConfigFileSourceLocal {
		t.Fatalf("expected github source local, got: %q", config.SystemSources["github"])
	}
	if config.Config.Systems["github"].Type != "github" {
		t.Fatalf("unexpected github type: %q", config.Config.Systems["github"].Type)
	}
}

func TestLoadIntegrationConfigRejectsEmptySystems(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{"default_system":"github","systems":{}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config after merge of 1 layers: systems must not be empty" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadIntegrationConfigRejectsUnknownSystemType(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{"systems":{"github":{"type":"github-enterprise"}}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config after merge of 1 layers: system \"github\" uses unknown type \"github-enterprise\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}
