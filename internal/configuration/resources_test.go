package configuration

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func TestLoadExecutionResourceConfigMergesGlobalAndLocalLayersAndSetsBindingSource(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/execution/resources.json":
			return []byte(`{
				"defaults": {"model-binding": "default"},
				"runners": ["opencode", "codex"],
				"models": ["openai/gpt-5.4", "openai/gpt-5.5"],
				"bindings": {
					"default": {"runner": "opencode", "model": "openai/gpt-5.4"},
					"review": {"runner": "opencode", "model": "openai/gpt-5.5"}
				}
			}`), nil
		case "/repo/.progress/execution/resources.json":
			return []byte(`{
				"defaults": {"model-binding": "coder"},
				"runners": ["codex"],
				"models": ["gpt-5.3-codex-spark"],
				"bindings": {
					"default": {"runner": "codex", "model": "gpt-5.3-codex-spark"},
					"coder": {"runner": "codex", "model": "gpt-5.3-codex-spark"}
				}
			}`), nil
		default:
			return nil, errors.New("config not found")
		}
	}

	config, err := LoadExecutionResourceConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load resources config: %v", err)
	}

	if config.BindingSources["default"] != ConfigFileSourceLocal {
		t.Fatalf("expected binding default from local, got: %q", config.BindingSources["default"])
	}
	if len(config.Config.Runners) != 2 || config.Config.Runners[0] != "opencode" || config.Config.Runners[1] != "codex" {
		t.Fatalf("unexpected merged runners: %#v", config.Config.Runners)
	}
	if len(config.Config.Models) != 3 {
		t.Fatalf("unexpected merged models: %#v", config.Config.Models)
	}
	if config.Config.Defaults.ModelBinding != "coder" {
		t.Fatalf("expected local default binding override, got: %q", config.Config.Defaults.ModelBinding)
	}
}

func TestLoadExecutionResourceConfigUsesGlobalLayerOnly(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/execution/resources.json":
			return []byte(`{
				"defaults": {"model-binding": "default"},
				"runners": ["opencode"],
				"models": ["openai/gpt-5.4"],
				"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
			}`), nil
		case "/repo/.progress/execution/resources.json":
			return nil, fs.ErrNotExist
		default:
			return nil, fs.ErrNotExist
		}
	}

	config, err := LoadExecutionResourceConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load resources config: %v", err)
	}

	if config.Config.Defaults.ModelBinding != "default" {
		t.Fatalf("unexpected default binding: %q", config.Config.Defaults.ModelBinding)
	}
}

func TestLoadExecutionResourceConfigSupportsEnvironmentToolAndResourceSections(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/execution/resources.json":
			return []byte(`{
				"defaults": {"model-binding": "default", "environment": "isolated-worktree"},
				"environments": {
					"isolated-worktree": {"type": "worktree", "enabled": true}
				},
				"tools": {
					"opencode": {"type": "agentic-system", "enabled": true},
					"codex": {"type": "agentic-system", "enabled": true}
				},
				"resources": {
					"qwen": {"type": "model", "enabled": true, "tools": ["opencode"]},
					"gpt-5.5": {"type": "model", "enabled": true, "tools": ["codex", "opencode"], "traits": ["reasoning"]}
				},
				"bindings": {
					"default": {"tool": "opencode", "resource": "qwen", "environment": "isolated-worktree"},
					"review": {"tool": "codex", "resource": "gpt-5.5"}
				}
			}`), nil
		case "/repo/.progress/execution/resources.json":
			return nil, fs.ErrNotExist
		default:
			return nil, fs.ErrNotExist
		}
	}

	config, err := LoadExecutionResourceConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load resources config: %v", err)
	}

	if config.Config.Defaults.Environment != "isolated-worktree" {
		t.Fatalf("unexpected default environment: %q", config.Config.Defaults.Environment)
	}
	if config.Config.Environments["isolated-worktree"].Type != EnvironmentTypeWorktree {
		t.Fatalf("unexpected environment config: %#v", config.Config.Environments["isolated-worktree"])
	}
	if !containsName(config.Config.Resources["gpt-5.5"].Tools, "codex") || !containsName(config.Config.Resources["gpt-5.5"].Tools, "opencode") {
		t.Fatalf("resource tools were not preserved: %#v", config.Config.Resources["gpt-5.5"])
	}
	if config.EnvironmentSources["isolated-worktree"] != ConfigFileSourceGlobal || config.ResourceSources["qwen"] != ConfigFileSourceGlobal {
		t.Fatalf("unexpected sources: environments=%#v resources=%#v", config.EnvironmentSources, config.ResourceSources)
	}
}

func TestLoadExecutionResourceConfigLetsLocalLayerDisableGlobalResource(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/execution/resources.json":
			return []byte(`{
				"defaults": {"model-binding": "default"},
				"tools": {"opencode": {"type": "agentic-system", "enabled": true}},
				"resources": {"qwen": {"type": "model", "enabled": true, "tools": ["opencode"]}},
				"bindings": {"default": {"tool": "opencode", "resource": "qwen"}}
			}`), nil
		case "/repo/.progress/execution/resources.json":
			return []byte(`{
				"resources": {"qwen": {"type": "model", "enabled": false, "tools": ["opencode"]}}
			}`), nil
		default:
			return nil, fs.ErrNotExist
		}
	}

	config, err := LoadExecutionResourceConfigWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load resources config: %v", err)
	}
	if config.Config.Resources["qwen"].Enabled {
		t.Fatalf("local layer must disable qwen: %#v", config.Config.Resources["qwen"])
	}
	if config.ResourceSources["qwen"] != ConfigFileSourceLocal {
		t.Fatalf("expected qwen source local, got: %q", config.ResourceSources["qwen"])
	}
}

func TestLoadExecutionResourceConfigFailsWhenLayerHasInvalidBinding(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/config-home/execution/resources.json" {
			return []byte(`{
				"defaults": {"model-binding": "default"},
				"runners": ["opencode"],
				"models": ["openai/gpt-5.4"],
				"bindings": {"default": {"runner": "missing", "model": "openai/gpt-5.4"}}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadExecutionResourceConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid-layer error")
	}
	if !strings.Contains(err.Error(), `binding "default" references unknown runner "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadExecutionResourceConfigFailsWhenNoLayerExists(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		return nil, fs.ErrNotExist
	}

	_, err := LoadExecutionResourceConfigWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected no config error")
	}
	if !strings.Contains(err.Error(), "execution resource config not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadExecutionResourceConfigUsesLocalLayerWhenGlobalHomeMissing(t *testing.T) {
	originalResolveUserHome := resolveUserHome
	resolveUserHome = func() (string, error) {
		return "", errors.New("home not available")
	}
	t.Cleanup(func() {
		resolveUserHome = originalResolveUserHome
	})

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/execution/resources.json" {
			return []byte(`{
				"defaults": {"model-binding": "default"},
				"runners": ["opencode"],
				"models": ["openai/gpt-5.4"],
				"bindings": {"default": {"runner": "opencode", "model": "openai/gpt-5.4"}}
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	config, err := LoadExecutionResourceConfigWithHome("/repo", "", readFile)
	if err != nil {
		t.Fatalf("load resources config: %v", err)
	}
	if config.Config.Defaults.ModelBinding != "default" {
		t.Fatalf("unexpected default binding: %q", config.Config.Defaults.ModelBinding)
	}
	if config.BindingSources["default"] != ConfigFileSourceLocal {
		t.Fatalf("expected binding source local, got: %q", config.BindingSources["default"])
	}
}
