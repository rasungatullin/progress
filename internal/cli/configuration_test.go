package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configcontour "github.com/rasungatullin/progress/internal/configuration"
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

func TestConfigurationResourcesCLIRejectsCustomEnvironmentWithoutType(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, _, err := runConfigurationCommandWithError(t, "configuration", "resources", "--repo-root", root, "environment", "set", "isolated-tree")
	if err == nil {
		t.Fatal("expected custom environment type error")
	}
	if !strings.Contains(err.Error(), `environment type is required for custom environment "isolated-tree"`) {
		t.Fatalf("unexpected error: %v", err)
	}

	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "environment", "set", "worktree")
}

func TestConfigurationResourcesCLIPreservesTypesOnPartialUpdate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "tool", "set", "runner-x", "--type", "custom-tool")
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "resource", "set", "secret-x", "--type", "secret")

	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "tool", "set", "runner-x", "--disabled")
	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "resource", "set", "secret-x", "--tool", "runner-x")

	configPath := filepath.Join(root, ".progress", "execution", "resources.json")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var config model.ResourceConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if config.Tools["runner-x"].Type != "custom-tool" {
		t.Fatalf("tool type must be preserved: %#v", config.Tools["runner-x"])
	}
	if config.Tools["runner-x"].Enabled {
		t.Fatalf("partial tool update must still apply enabled flag: %#v", config.Tools["runner-x"])
	}
	if config.Resources["secret-x"].Type != "secret" {
		t.Fatalf("resource type must be preserved: %#v", config.Resources["secret-x"])
	}
	if len(config.Resources["secret-x"].Tools) != 1 || config.Resources["secret-x"].Tools[0] != "runner-x" {
		t.Fatalf("partial resource update must still apply tools: %#v", config.Resources["secret-x"])
	}
}

func TestConfigurationResourcesCLIPreservesGlobalResourceToolsOnLocalPartialUpdate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configHome := t.TempDir()
	runConfigurationCommand(t, "configuration", "resources", "--scope", "global", "--config-home", configHome, "tool", "set", "opencode")
	runConfigurationCommand(t, "configuration", "resources", "--scope", "global", "--config-home", configHome, "resource", "set", "qwen", "--tool", "opencode")
	runConfigurationCommand(t, "configuration", "resources", "--scope", "global", "--config-home", configHome, "binding", "set", "default", "--tool", "opencode", "--resource", "qwen")
	runConfigurationCommand(t, "configuration", "resources", "--scope", "global", "--config-home", configHome, "defaults", "set", "--model-binding", "default")

	runConfigurationCommand(t, "configuration", "resources", "--repo-root", root, "--config-home", configHome, "resource", "set", "qwen", "--enabled")

	loaded, err := configcontour.LoadExecutionResourceConfigWithHome(root, configHome, os.ReadFile)
	if err != nil {
		t.Fatalf("load merged config: %v", err)
	}
	resource := loaded.Config.Resources["qwen"]
	if len(resource.Tools) != 1 || resource.Tools[0] != "opencode" {
		t.Fatalf("local partial update must preserve global resource tools: %#v", resource)
	}
	if loaded.ResourceSources["qwen"] != configcontour.ConfigFileSourceLocal {
		t.Fatalf("expected local resource override, got: %q", loaded.ResourceSources["qwen"])
	}
}

func runConfigurationCommand(t *testing.T, args ...string) string {
	t.Helper()

	stdout, stderr, err := runConfigurationCommandWithError(t, args...)
	if err != nil {
		t.Fatalf("execute %v: %v\nstderr=%s", args, err, stderr)
	}
	return stdout
}

func runConfigurationCommandWithError(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}
