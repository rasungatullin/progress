package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestConfigurationResourcesCLIWritesLocalResourceConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "tool", "set", "opencode")
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "resource", "set", "qwen", "--tool", "opencode", "--disabled")
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "binding", "set", "default", "--tool", "opencode", "--resource", "qwen", "--environment", "worktree")
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "defaults", "set", "--model-binding", "default", "--environment", "worktree")

	configPath := filepath.Join(root, ".progress", "execution", "resources.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var config model.ResourceConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if !config.Tools["opencode"].Enabled {
		t.Fatalf("tool must be enabled: %#v", config.Tools["opencode"])
	}
	if config.Resources["qwen"].Enabled {
		t.Fatalf("resource must be disabled: %#v", config.Resources["qwen"])
	}
	if len(config.Resources["qwen"].Tools) != 1 || config.Resources["qwen"].Tools[0] != "opencode" {
		t.Fatalf("unexpected resource tools: %#v", config.Resources["qwen"])
	}
	if config.Bindings["default"].Tool != "opencode" || config.Bindings["default"].Resource != "qwen" || config.Bindings["default"].Environment != "worktree" {
		t.Fatalf("unexpected binding: %#v", config.Bindings["default"])
	}
	if config.Defaults.ModelBinding != "default" || config.Defaults.Environment != "worktree" {
		t.Fatalf("unexpected defaults: %#v", config.Defaults)
	}
	if len(config.Environments) != 0 {
		t.Fatalf("local layer must stay partial until environment is explicitly set: %#v", config.Environments)
	}

	output := runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "list")
	for _, fragment := range []string{"defaults.model-binding=default", "defaults.environment=worktree", "opencode", "qwen"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("list output must include %q, got %q", fragment, output)
		}
	}
}

func TestConfigurationResourcesCLIHonorsGlobalScope(t *testing.T) {
	t.Parallel()

	configHome := t.TempDir()
	runConfigurationCommand(t, "configuration", "resources", "--scope", "global", "--config-home", configHome, "environment", "set", "isolated-tree", "--type", "worktree")

	configPath := filepath.Join(configHome, "execution", "resources.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read global config: %v", err)
	}
	if !strings.Contains(string(content), `"isolated-tree"`) {
		t.Fatalf("global config must contain environment: %s", string(content))
	}
}

func runConfigurationCommand(t *testing.T, args ...string) string {
	t.Helper()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v\nstderr=%s", args, err, stderr.String())
	}
	return stdout.String()
}
