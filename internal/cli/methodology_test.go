package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMethodologyCLIAddsListsAndSelectsLocalCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configHome := t.TempDir()

	if output, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"add", "action",
		"--name", "implement",
		"--class", "engineering-synthesis",
		"--profile", "coder",
		"--operation", "prepare-data",
	); err != nil {
		t.Fatalf("add action: %v\n%s", err, output)
	}

	if output, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"add", "route",
		"--name", "default",
		"--title", "Маршрут по умолчанию",
		"--action", "implement",
		"--profile", "coder",
	); err != nil {
		t.Fatalf("add route: %v\n%s", err, output)
	}

	listOutput, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"list",
	)
	if err != nil {
		t.Fatalf("list catalog: %v\n%s", err, listOutput)
	}
	for _, fragment := range []string{"route", "default", "action", "implement", "local"} {
		if !strings.Contains(listOutput, fragment) {
			t.Fatalf("list output must include %q, got %q", fragment, listOutput)
		}
	}

	selectOutput, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"select", "--route", "default",
	)
	if err != nil {
		t.Fatalf("select catalog: %v\n%s", err, selectOutput)
	}
	for _, fragment := range []string{
		"route=default\n",
		"route-source=local\n",
		"action=implement\n",
		"action-source=local\n",
		"profile=coder\n",
	} {
		if !strings.Contains(selectOutput, fragment) {
			t.Fatalf("select output must include %q, got %q", fragment, selectOutput)
		}
	}

	overrideOutput, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"select", "--route", "default", "--profile", "review",
	)
	if err != nil {
		t.Fatalf("select catalog with profile override: %v\n%s", err, overrideOutput)
	}
	for _, fragment := range []string{
		"profile=review\n",
		"diagnostic=profile=review\n",
	} {
		if !strings.Contains(overrideOutput, fragment) {
			t.Fatalf("select override output must include %q, got %q", fragment, overrideOutput)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".progress", "methodology", "catalog.json")); err != nil {
		t.Fatalf("expected local methodology catalog: %v", err)
	}
}

func TestMethodologyCLIUpdateActionPreservesConfiguredFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configHome := t.TempDir()
	catalogDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("create catalog dir: %v", err)
	}
	catalogPath := filepath.Join(catalogDir, "catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{
		"actions": [{
			"name": "engineering-synthesis",
			"class": "engineering-synthesis",
			"profile": "default",
			"aliases": ["implement"],
			"requires_workplace": true,
			"operations": [
				{"name": "prepare-data", "kind": "prepare-data", "title": "Подготовка данных", "type":"builtin", "required": true}
			],
			"description": "Старое описание."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	if output, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"add", "action",
		"--name", "engineering-synthesis",
		"--description", "Новое описание.",
	); err != nil {
		t.Fatalf("update action: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(catalogDir, "actions", "engineering-synthesis.json"))
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	for _, fragment := range []string{
		`"aliases":`,
		`"implement"`,
		`"requires_workplace": true`,
		`"title": "Подготовка данных"`,
		`"description": "Новое описание."`,
	} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("updated action must include %q, got %s", fragment, string(content))
		}
	}
}

func TestMethodologyCLIUpdateGlobalActionIgnoresLocalOverride(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configHome := t.TempDir()
	globalDir := filepath.Join(configHome, "methodology")
	localDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("create global catalog dir: %v", err)
	}
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("create local catalog dir: %v", err)
	}
	globalPath := filepath.Join(globalDir, "catalog.json")
	if err := os.WriteFile(globalPath, []byte(`{
		"actions": [{
			"name": "engineering-synthesis",
			"aliases": ["implement"],
			"operations": [{"name": "prepare-data", "kind": "prepare-data"}],
			"description": "Глобальное описание."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write global catalog: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "catalog.json"), []byte(`{
		"actions": [{
			"name": "engineering-synthesis",
			"aliases": ["local-implement"],
			"operations": [{"name": "local-operation", "kind": "prepare-data"}],
			"description": "Локальное описание."
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write local catalog: %v", err)
	}

	if output, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"add", "action",
		"--scope", "global",
		"--name", "engineering-synthesis",
		"--description", "Новое глобальное описание.",
	); err != nil {
		t.Fatalf("update global action: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(globalDir, "actions", "engineering-synthesis.json"))
	if err != nil {
		t.Fatalf("read global action: %v", err)
	}
	for _, fragment := range []string{
		`"implement"`,
		`"prepare-data"`,
		`"description": "Новое глобальное описание."`,
	} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("global action must include %q, got %s", fragment, string(content))
		}
	}
	for _, fragment := range []string{`"local-implement"`, `"local-operation"`} {
		if strings.Contains(string(content), fragment) {
			t.Fatalf("global action must not inherit local fragment %q: %s", fragment, string(content))
		}
	}
}

func TestMethodologyCLIAddsGenericEntityForTargetContour(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configHome := t.TempDir()

	output, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"add", "entity",
		"--kind", "decision-rule",
		"--name", "description-assessment",
		"--target-contour", "decision",
		"--payload", `{"has_label":"description-assessment"}`,
	)
	if err != nil {
		t.Fatalf("add entity: %v\n%s", err, output)
	}

	listOutput, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"list", "--kind", "decision-rule", "--target-contour", "decision",
	)
	if err != nil {
		t.Fatalf("list entity: %v\n%s", err, listOutput)
	}
	for _, fragment := range []string{"entity", "decision-rule", "description-assessment", "decision", "local"} {
		if !strings.Contains(listOutput, fragment) {
			t.Fatalf("entity list output must include %q, got %q", fragment, listOutput)
		}
	}

	showOutput, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"show", "--kind", "decision-rule", "--name", "description-assessment",
	)
	if err != nil {
		t.Fatalf("show entity: %v\n%s", err, showOutput)
	}
	for _, fragment := range []string{
		"kind=entity\n",
		"entity-kind=decision-rule\n",
		"target-contour=decision\n",
		`"has_label": "description-assessment"`,
	} {
		if !strings.Contains(showOutput, fragment) {
			t.Fatalf("entity show output must include %q, got %q", fragment, showOutput)
		}
	}
}

func TestMethodologyCLISavesCatalogFileToGlobalScope(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configHome := t.TempDir()
	source := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(source, []byte(`{
		"routes": [{"name": "default", "action": "implement"}],
		"actions": [{"name": "implement"}]
	}`), 0o600); err != nil {
		t.Fatalf("write source catalog: %v", err)
	}

	output, err := executeMethodologyCommand(t,
		"methodology", "--repo-root", root, "--config-home", configHome,
		"save", "--scope", "global", "--file", source,
	)
	if err != nil {
		t.Fatalf("save catalog: %v\n%s", err, output)
	}
	expectedPath := filepath.Join(configHome, "methodology", "catalog.json")
	if !strings.Contains(output, "scope=global\n") || !strings.Contains(output, "path="+expectedPath+"\n") {
		t.Fatalf("unexpected save output: %q", output)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("expected global methodology catalog: %v", err)
	}
}

func executeMethodologyCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return stdout.String() + stderr.String(), err
}
