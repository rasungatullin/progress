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
