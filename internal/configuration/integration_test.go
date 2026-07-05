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
				"private_store": {
					"type": "keychain",
					"service": "progress-global"
				},
				"systems": {
					"github": {
						"type": "github",
						"command": "gh",
						"timeout": "30s",
						"project": "global-project",
						"repository": "global/repository",
						"default_repo": "global/repo",
						"token": "global-direct-token",
						"token_env": "GITHUB_TOKEN",
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
				"private_store": {
					"service": "progress-local"
				},
				"systems": {
					"github": {
						"project": "local-project",
						"repository": "local/repository",
						"default_repo": "local/repo",
						"token_private": "github_auth_token",
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
	if github.TokenPrivate != "github_auth_token" {
		t.Fatalf("unexpected private token reference: %q", github.TokenPrivate)
	}
	if github.Token != "" {
		t.Fatalf("expected private token reference to clear inherited direct token, got: %q", github.Token)
	}
	if github.TokenEnv != "" {
		t.Fatalf("expected private token reference to clear inherited token env, got: %q", github.TokenEnv)
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
	if config.ConfigHome != "/config-home" {
		t.Fatalf("unexpected config home: %q", config.ConfigHome)
	}
	if config.RepoRoot != "/repo" {
		t.Fatalf("unexpected repo root: %q", config.RepoRoot)
	}
	if config.Config.PrivateStore.Type != "keychain" {
		t.Fatalf("unexpected private store type: %q", config.Config.PrivateStore.Type)
	}
	if config.Config.PrivateStore.Service != "progress-local" {
		t.Fatalf("unexpected private store service: %q", config.Config.PrivateStore.Service)
	}
	if len(config.Layers) != 2 {
		t.Fatalf("unexpected layer count: %d", len(config.Layers))
	}
}

func TestLoadIntegrationConfigLetsLocalTokenEnvOverridePrivateToken(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/integration/systems.json":
			return []byte(`{
				"systems": {
					"mattermost": {
						"type": "mattermost",
						"base_url": "https://mattermost.example",
						"token_private": "mt_auth_token"
					}
				}
			}`), nil
		case "/repo/.progress/integration/systems.json":
			return []byte(`{
				"systems": {
					"mattermost": {
						"token_env": "MATTERMOST_TOKEN"
					}
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

	mattermost := config.Config.Systems["mattermost"]
	if mattermost.TokenEnv != "MATTERMOST_TOKEN" {
		t.Fatalf("unexpected token env: %q", mattermost.TokenEnv)
	}
	if mattermost.TokenPrivate != "" {
		t.Fatalf("expected token_env to clear inherited private token reference, got: %q", mattermost.TokenPrivate)
	}
	if mattermost.Token != "" {
		t.Fatalf("expected token_env to clear inherited direct token, got: %q", mattermost.Token)
	}
}

func TestLoadIntegrationConfigMergesGitHubAppSettings(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/integration/systems.json":
			return []byte(`{
				"systems": {
					"github-app": {
						"type": "github",
						"transport": "api",
						"github_app_id": "12345",
						"github_app_installation_id": "144549701",
						"github_app_private_key_path": "/global/key.pem",
						"github_app_token_refresh_before": "10m"
					}
				}
			}`), nil
		case "/repo/.progress/integration/systems.json":
			return []byte(`{
				"systems": {
					"github-app": {
						"github_app_client_id": "Iv1.client",
						"github_app_private_key_private": "progress_synthesis_pem"
					}
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
	system := config.Config.Systems["github-app"]
	if system.GitHubAppID != "12345" {
		t.Fatalf("unexpected app id: %q", system.GitHubAppID)
	}
	if system.GitHubAppClientID != "Iv1.client" {
		t.Fatalf("unexpected client id: %q", system.GitHubAppClientID)
	}
	if system.GitHubAppInstallationID != "144549701" {
		t.Fatalf("unexpected installation id: %q", system.GitHubAppInstallationID)
	}
	if system.GitHubAppPrivateKeyPrivate != "progress_synthesis_pem" {
		t.Fatalf("unexpected private key reference: %q", system.GitHubAppPrivateKeyPrivate)
	}
	if system.GitHubAppPrivateKeyPath != "" {
		t.Fatalf("expected private value to clear key path, got: %q", system.GitHubAppPrivateKeyPath)
	}
	if system.GitHubAppTokenRefreshBefore != "10m" {
		t.Fatalf("unexpected refresh interval: %q", system.GitHubAppTokenRefreshBefore)
	}
}

func TestLoadIntegrationConfigRejectsIncompleteGitHubAppSettings(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{
				"systems": {
					"github-app": {
						"type": "github",
						"transport": "api",
						"github_app_installation_id": "144549701"
					}
				}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != `invalid integration config after merge of 1 layers: system "github-app" uses GitHub App auth without github_app_id or github_app_client_id` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadIntegrationConfigRejectsGitHubAppWithoutAPITransport(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"omitted": "",
		"cli":     `"transport": "cli",`,
	}

	for name, transportField := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			readFile := func(path string) ([]byte, error) {
				if path == "/repo/.progress/integration/systems.json" {
					return []byte(`{
						"systems": {
							"github-app": {
								"type": "github",
								` + transportField + `
								"github_app_id": "4221694",
								"github_app_installation_id": "144549701",
								"github_app_private_key_path": "/keys/app.pem"
							}
						}
					}`), nil
				}
				return nil, fs.ErrNotExist
			}

			_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
			if err == nil {
				t.Fatal("expected invalid config error")
			}
			if err.Error() != `invalid integration config after merge of 1 layers: system "github-app" uses GitHub App auth without transport=api` {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadIntegrationConfigAllowsTokenToOverrideIncompleteGitHubAppSettings(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{
				"systems": {
					"github-app": {
						"type": "github",
						"transport": "api",
						"token": "direct-token",
						"github_app_installation_id": "144549701"
					}
				}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	config, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load integration config: %v", err)
	}
	if config.Config.Systems["github-app"].Token != "direct-token" {
		t.Fatalf("unexpected token: %q", config.Config.Systems["github-app"].Token)
	}
}

func TestLoadIntegrationConfigKeepsTokenPriorityWithinLayer(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{
				"systems": {
					"mattermost": {
						"type": "mattermost",
						"base_url": "https://mattermost.example",
						"token": "direct-token",
						"token_private": "mt_auth_token",
						"token_env": "MATTERMOST_TOKEN"
					},
					"telegram": {
						"type": "telegram",
						"token_private": "telegram_auth_token",
						"token_env": "TELEGRAM_TOKEN"
					}
				}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	config, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load integration config: %v", err)
	}

	mattermost := config.Config.Systems["mattermost"]
	if mattermost.Token != "direct-token" {
		t.Fatalf("expected direct token to win within one layer, got: %q", mattermost.Token)
	}
	if mattermost.TokenPrivate != "" || mattermost.TokenEnv != "" {
		t.Fatalf("expected direct token to clear alternative sources, got private=%q env=%q", mattermost.TokenPrivate, mattermost.TokenEnv)
	}

	telegram := config.Config.Systems["telegram"]
	if telegram.TokenPrivate != "telegram_auth_token" {
		t.Fatalf("expected private token to win over token_env within one layer, got: %q", telegram.TokenPrivate)
	}
	if telegram.Token != "" || telegram.TokenEnv != "" {
		t.Fatalf("expected private token to clear alternative sources, got token=%q env=%q", telegram.Token, telegram.TokenEnv)
	}
}

func TestLoadIntegrationPrivateStoreConfigDoesNotRequireSystems(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/config-home/integration/systems.json" {
			return []byte(`{"private_store":{"type":"file","path":"/tmp/progress-private.json"}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	config, err := LoadIntegrationPrivateStoreConfigWithHome("", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load private store config: %v", err)
	}
	if config.Config.Type != "file" {
		t.Fatalf("unexpected private store type: %q", config.Config.Type)
	}
	if config.Config.Path != "/tmp/progress-private.json" {
		t.Fatalf("unexpected private store path: %q", config.Config.Path)
	}
	if config.ConfigHome != "/config-home" {
		t.Fatalf("unexpected config home: %q", config.ConfigHome)
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

func TestLoadIntegrationConfigRejectsLocalTrackerDefaultForRepository(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{
				"default_systems": {"repository": "local"},
				"systems": {"local": {"type": "local-tracker"}}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config after merge of 1 layers: default_systems.repository references system \"local\" without matching integration type" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadIntegrationConfigRejectsScriptDefaultForRepository(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{
				"default_systems": {"repository": "work-tracker"},
				"systems": {"work-tracker": {"type": "script"}}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config after merge of 1 layers: default_systems.repository references system \"work-tracker\" without matching integration type" {
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

func TestLoadIntegrationConfigRejectsUnsupportedDatabaseDriver(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{"systems":{"local":{"type":"local-tracker","database":{"driver":"postgres"}}}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config /repo/.progress/integration/systems.json: system \"local\" uses unsupported database driver \"postgres\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadIntegrationConfigRejectsUnknownGitHubTransport(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/integration/systems.json" {
			return []byte(`{"systems":{"github":{"type":"github","transport":"socket"}}}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadIntegrationConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if err.Error() != "invalid integration config after merge of 1 layers: system \"github\" uses unknown GitHub transport \"socket\"" {
		t.Fatalf("unexpected error: %v", err)
	}
}
