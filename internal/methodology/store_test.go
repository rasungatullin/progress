package methodology

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/configuration"
)

func mustMarshalCatalogJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("build catalog json: %v", err)
	}
	return string(b)
}

func TestLoadCatalogMergesGlobalAndLocalLayersWithLocalPriority(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/methodology/catalog.json":
			return []byte(`{
				"routes": [
					{"name": "default", "title": "Глобальный маршрут", "action": "implement", "profile": "global"},
					{"name": "review", "action": "review", "profile": "review"}
				],
				"actions": [
					{"name": "implement", "class": "engineering-synthesis", "profile": "global"},
					{"name": "review", "class": "review", "profile": "review"}
				],
				"instructions": [
					{"name": "default-directive", "profile": "global", "body": "Глобальная инструкция."}
				],
				"entities": [
					{"kind": "decision-rule", "name": "description-assessment", "target_contour": "decision", "payload": {"label": "description-assessment"}}
				]
			}`), nil
		case "/repo/.progress/methodology/catalog.json":
			return []byte(`{
				"routes": [
					{"name": "default", "title": "Локальный маршрут", "action": "implement", "profile": "local"}
				],
				"actions": [
					{"name": "implement", "class": "engineering-synthesis", "profile": "local"}
				],
				"instructions": [
					{"name": "default-directive", "profile": "local", "body": "Локальная инструкция."}
				],
				"entities": [
					{"kind": "decision-rule", "name": "description-assessment", "target_contour": "decision", "payload": {"label": "local"}}
				]
			}`), nil
		default:
			return nil, fs.ErrNotExist
		}
	}

	snapshot, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	if len(snapshot.Catalog.Routes) != 2 {
		t.Fatalf("unexpected routes: %#v", snapshot.Catalog.Routes)
	}
	if snapshot.Catalog.Routes[0].Name != "default" || snapshot.Catalog.Routes[0].Profile != "local" {
		t.Fatalf("expected local route override, got: %#v", snapshot.Catalog.Routes[0])
	}
	if snapshot.Catalog.Actions[0].Profile != "local" {
		t.Fatalf("expected local action override, got: %#v", snapshot.Catalog.Actions[0])
	}
	if snapshot.Catalog.Instructions[0].Profile != "local" {
		t.Fatalf("expected local instruction override, got: %#v", snapshot.Catalog.Instructions[0])
	}
	if snapshot.Catalog.Entities[0].TargetContour != "decision" || string(snapshot.Catalog.Entities[0].Payload) != `{"label":"local"}` {
		t.Fatalf("expected local entity override, got: %#v", snapshot.Catalog.Entities[0])
	}
	if snapshot.Sources.Routes["default"] != configuration.ConfigFileSourceLocal {
		t.Fatalf("expected local route source, got: %q", snapshot.Sources.Routes["default"])
	}
	if snapshot.Sources.Routes["review"] != configuration.ConfigFileSourceGlobal {
		t.Fatalf("expected global review route source, got: %q", snapshot.Sources.Routes["review"])
	}
}

func TestLoadCatalogLoadsInstructionBodyFromMarkdown(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/repo/.progress/methodology/catalog.json":
			return []byte(`{"instructions":[{"name":"directive","body_file":"markdown/directive.md"}]}`), nil
		case "/repo/.progress/methodology/markdown/directive.md":
			return []byte("# Инструкция\n\nВыполнить действие."), nil
		default:
			return nil, fs.ErrNotExist
		}
	}

	snapshot, err := LoadCatalogWithHome("/repo", "/missing-config", readFile)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	instruction := snapshot.Catalog.Instructions[0]
	if instruction.Body != "# Инструкция\n\nВыполнить действие." || instruction.BodyFile != "markdown/directive.md" {
		t.Fatalf("unexpected instruction: %#v", instruction)
	}
}

func TestLoadCatalogRejectsInstructionBodyFileOutsideMethodologyRoot(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{"instructions":[{"name":"directive","body_file":"../../directive.md"}]}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/missing-config", readFile)
	if err == nil || !strings.Contains(err.Error(), "escapes methodology catalog") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsMissingInstructionBodyFile(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{"instructions":[{"name":"directive","body_file":"missing.md"}]}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/missing-config", readFile)
	if err == nil || !strings.Contains(err.Error(), "read instruction body file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsInstructionBodyAndBodyFile(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{"instructions":[{"name":"directive","body":"inline","body_file":"directive.md"}]}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/missing-config", readFile)
	if err == nil || !strings.Contains(err.Error(), "has both body and body_file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsInstructionBodyFileSymlinkOutsideMethodologyRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	methodologyDir := filepath.Join(root, ".progress", "methodology")
	outsideDir := t.TempDir()
	writeTestFile(t, filepath.Join(methodologyDir, "catalog.json"), `{"instructions":[{"name":"directive","body_file":"texts/directive.md"}]}`)
	writeTestFile(t, filepath.Join(outsideDir, "directive.md"), "внешний текст")
	if err := os.MkdirAll(filepath.Join(methodologyDir, "texts"), 0o755); err != nil {
		t.Fatalf("create instruction directory: %v", err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "directive.md"), filepath.Join(methodologyDir, "texts", "directive.md")); err != nil {
		t.Fatalf("create instruction symlink: %v", err)
	}

	_, err := LoadCatalogWithHome(root, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "escapes methodology catalog") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogAllowsInstructionBodyFileWhenMethodologyRootIsSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realMethodologyDir := t.TempDir()
	methodologyLink := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(filepath.Dir(methodologyLink), 0o755); err != nil {
		t.Fatalf("create methodology parent directory: %v", err)
	}
	if err := os.Symlink(realMethodologyDir, methodologyLink); err != nil {
		t.Fatalf("create methodology root symlink: %v", err)
	}
	writeTestFile(t, filepath.Join(methodologyLink, "catalog.json"), `{"instructions":[{"name":"directive","body_file":"texts/directive.md"}]}`)
	writeTestFile(t, filepath.Join(methodologyLink, "texts", "directive.md"), "внутренний текст")

	snapshot, err := LoadCatalogWithHome(root, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if got := snapshot.Catalog.Instructions[0].Body; got != "внутренний текст" {
		t.Fatalf("unexpected instruction body: %q", got)
	}
}

func TestWriteCatalogFilesOmitsLoadedInstructionBody(t *testing.T) {
	written := map[string][]byte{}
	writeFile := func(path string, content []byte, _ fs.FileMode) error {
		written[path] = append([]byte(nil), content...)
		return nil
	}

	err := writeCatalogFiles("/repo/.progress/methodology/catalog.json", Catalog{
		Instructions: []Instruction{{Name: "directive", Body: "загруженный текст", BodyFile: "texts/directive.md"}},
	}, writeFile, func(string, fs.FileMode) error { return nil }, func(string) error { return nil })
	if err != nil {
		t.Fatalf("write catalog files: %v", err)
	}

	content := string(written["/repo/.progress/methodology/instructions/directive.json"])
	if strings.Contains(content, `"body"`) || !strings.Contains(content, `"body_file": "texts/directive.md"`) {
		t.Fatalf("unexpected instruction content: %s", content)
	}
}

func TestLoadCatalogKeepsLocalAliasPriorityOverGlobalAlias(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		switch path {
		case "/config-home/methodology/catalog.json":
			return []byte(`{
				"actions": [
					{"name": "engineering-synthesis", "aliases": ["implement"], "profile": "global"}
				]
			}`), nil
		case "/repo/.progress/methodology/catalog.json":
			return []byte(`{
				"actions": [
					{"name": "local-implementation", "aliases": ["implement"], "profile": "local"}
				]
			}`), nil
		default:
			return nil, fs.ErrNotExist
		}
	}

	snapshot, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	action, err := selectAction(snapshot.Catalog.Actions, "implement")
	if err != nil {
		t.Fatalf("select action: %v", err)
	}
	if action.Name != "local-implementation" || action.Profile != "local" {
		t.Fatalf("local alias must win over global alias: %#v", action)
	}
}

func TestLoadCatalogRejectsDuplicateRoutesInsideSingleLayer(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"routes": [
					{"name": "default", "action": "implement"},
					{"name": "DEFAULT", "action": "review"}
				],
				"actions": [
					{"name": "implement"},
					{"name": "review"}
				]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected duplicate route error")
	}
	if !strings.Contains(err.Error(), `routes contains duplicate name "default"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsAmbiguousActionAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		actions   string
		wantError string
	}{
		{
			name: "alias conflicts with action name",
			actions: `[
				{"name": "engineering-synthesis", "aliases": ["review"]},
				{"name": "review"}
			]`,
			wantError: `action "engineering-synthesis" alias "review" conflicts with action name`,
		},
		{
			name: "alias conflicts with another alias",
			actions: `[
				{"name": "engineering-synthesis", "aliases": ["implement"]},
				{"name": "task-preparation", "aliases": ["IMPLEMENT"]}
			]`,
			wantError: `action "task-preparation" alias "implement" conflicts with action "engineering-synthesis" alias`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			readFile := func(path string) ([]byte, error) {
				if path == "/repo/.progress/methodology/catalog.json" {
					return []byte(`{"actions": ` + tt.actions + `}`), nil
				}
				return nil, fs.ErrNotExist
			}

			_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
			if err == nil {
				t.Fatal("expected alias conflict error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadCatalogRejectsInvalidActionOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		actions   string
		wantError string
	}{
		{
			name: "empty operation",
			actions: `[
				{"name": "implement", "operations": [{}]}
			]`,
			wantError: `action "implement" operations[0] must define name`,
		},
		{
			name: "duplicate operation",
			actions: `[
				{"name": "implement", "operations": ["prepare-data", {"name": "PREPARE-DATA"}]}
			]`,
			wantError: `action "implement" operations contains duplicate name "prepare-data"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			readFile := func(path string) ([]byte, error) {
				if path == "/repo/.progress/methodology/catalog.json" {
					return []byte(`{"actions": ` + tt.actions + `}`), nil
				}
				return nil, fs.ErrNotExist
			}

			_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
			if err == nil {
				t.Fatal("expected operation validation error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadCatalogAllowsLegacyActionOperationShortForm(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions": [{"name":"implement","operations":[{"name":"prepare-data"}]}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	if _, err := LoadCatalogWithHome("/repo", "/config-home", readFile); err != nil {
		t.Fatalf("load catalog with legacy operation form: %v", err)
	}
}

func TestLoadCatalogAcceptsEmptyContracts(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","contract":{},"operations":[{"name":"prepare-data"}]}],
				"operations":[{"name":"prepare-data","contract":{}}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	if _, err := LoadCatalogWithHome("/repo", "/config-home", readFile); err != nil {
		t.Fatalf("load catalog with empty contracts: %v", err)
	}
}

func TestLoadCatalogRejectsInvalidActionContractFieldType(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","contract":{"in":{"task_id":{}}}}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid contract field type error")
	}
	if !strings.Contains(err.Error(), "implement.contract.in.task_id.type must be non-empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogAcceptsActionOperationBindingByValue(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data","in":{"task_id":{"value":"task-123"}}}]}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	if _, err := LoadCatalogWithHome("/repo", "/config-home", readFile); err != nil {
		t.Fatalf("load catalog with value binding: %v", err)
	}
}

func TestLoadCatalogAcceptsActionOperationBindingByNullValue(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(mustMarshalCatalogJSON(t, map[string]any{
				"actions": []any{
					map[string]any{
						"name": "implement",
						"operations": []any{
							map[string]any{
								"name": "prepare-data",
								"in": map[string]any{
									"task_id": map[string]any{
										"value": nil,
									},
								},
							},
						},
					},
				},
				"operations": []any{
					map[string]any{"name": "prepare-data"},
				},
			})), nil
		}
		return nil, fs.ErrNotExist
	}

	if _, err := LoadCatalogWithHome("/repo", "/config-home", readFile); err != nil {
		t.Fatalf("load catalog with null value binding: %v", err)
	}
}

func TestLoadCatalogRejectsActionOperationBindingWithoutRefOrValue(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data","in":{"task_id":{}}}]}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected binding error")
	}
	if !strings.Contains(err.Error(), `action "implement" operations[0].in.task_id must contain either ref or value`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsConflictingActionOperationBinding(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data","in":{"task_id":{"ref":"in.task_id","value":"abc"}}}]}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected conflicting binding error")
	}
	if !strings.Contains(err.Error(), `action "implement" operations[0].in.task_id has both ref and value`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsConflictingActionOperationBindingWithNullValue(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(mustMarshalCatalogJSON(t, map[string]any{
				"actions": []any{
					map[string]any{
						"name": "implement",
						"operations": []any{
							map[string]any{
								"name": "prepare-data",
								"in": map[string]any{
									"task_id": map[string]any{
										"ref":   "in.task_id",
										"value": nil,
									},
								},
							},
						},
					},
				},
				"operations": []any{
					map[string]any{"name": "prepare-data"},
				},
			})), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected conflicting binding error")
	}
	if !strings.Contains(err.Error(), `action "implement" operations[0].in.task_id has both ref and value`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsConflictingActionOperationBindingWithEmptyRef(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data","in":{"task_id":{"ref":" ","value":"abc"}}}]}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected conflicting binding error")
	}
	if !strings.Contains(err.Error(), `action "implement" operations[0].in.task_id has both ref and value`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsEmptyOperationContractFieldType(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data"}]}],
				"operations":[{"name":"prepare-data","contract":{"in":{"task_id":{}}}}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected invalid operation contract field type error")
	}
	if !strings.Contains(err.Error(), "prepare-data.contract.in.task_id.type must be non-empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsOperationContractRequiredTypeError(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data"}]}],
				"operations":[{"name":"prepare-data","contract":{"out":{"workspace":{"type":"string","required":"yes"}}}}
				]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected required type error")
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsActionContractSectionType(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","contract":{"in":[]}}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected contract section type error")
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsActionContractInNullSection(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","contract":{"in":null}}],
				"operations":[{"name":"prepare-data"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected contract section type error")
	}
	if !strings.Contains(err.Error(), "implement.contract.in must be an object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsInvalidActionContractRequiredType(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(mustMarshalCatalogJSON(t, map[string]any{
				"actions": []any{
					map[string]any{
						"name": "implement",
						"contract": map[string]any{
							"in": map[string]any{
								"task_id": map[string]any{
									"type":     "string",
									"required": "yes",
								},
							},
						},
					},
				},
				"operations": []any{
					map[string]any{"name": "prepare-data"},
				},
			})), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected required type error")
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsOperationContractOutNullSection(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"actions":[{"name":"implement","operations":[{"name":"prepare-data"}]}],
				"operations":[{"name":"prepare-data","contract":{"out":null}}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	_, err := LoadCatalogWithHome("/repo", "/config-home", readFile)
	if err == nil {
		t.Fatal("expected contract section type error")
	}
	if !strings.Contains(err.Error(), "prepare-data.contract.out must be an object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCatalogRejectsActionReferenceToUnknownOperation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	methodologyDir := filepath.Join(root, ".progress", "methodology")
	writeTestFile(t, filepath.Join(methodologyDir, "catalog.json"), `{"actions":[{"name":"implement","class":"engineering-synthesis","operations":[{"name":"prepare-data"},{"name":"missing-operation"}]}]}`)
	writeTestFile(t, filepath.Join(methodologyDir, "operations", "prepare-data.json"), `{"name":"prepare-data","kind":"prepare-data","title":"Подготовка данных","type":"builtin","required":true}`)

	_, err := LoadCatalogWithHome(root, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected unknown operation error")
	}
	if !strings.Contains(err.Error(), `action "implement" operations[1] references unknown operation "missing-operation"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveCatalogRejectsDuplicateRoutesBeforeNormalization(t *testing.T) {
	t.Parallel()

	_, err := SaveCatalogWithHome("/repo", "/config-home", CatalogWriteScopeLocal, Catalog{
		Routes: []Route{
			{Name: "default", Action: "implement"},
			{Name: "DEFAULT", Action: "review"},
		},
		Actions: []Action{
			{Name: "implement"},
			{Name: "review"},
		},
	}, nil, func(string, []byte, fs.FileMode) error {
		t.Fatal("save must not write invalid catalog")
		return nil
	}, func(string, fs.FileMode) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected duplicate route error")
	}
	if !strings.Contains(err.Error(), `routes contains duplicate name "default"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceUpsertWritesLocalCatalogElement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(nil)

	result, err := service.Upsert(context.Background(), CatalogWriteRequest{
		RepoRoot: root,
		Scope:    CatalogWriteScopeLocal,
		Element: ElementUpsert{Action: &Action{
			Name:        "implement",
			Class:       "engineering-synthesis",
			Profile:     "coder",
			Operations:  []ActionOperation{{Name: "prepare-data"}, {Name: "launch-synthesis"}},
			Description: "Выполнение инженерного изменения.",
		}},
	})
	if err != nil {
		t.Fatalf("upsert action: %v", err)
	}

	if result.Path != filepath.Join(root, ".progress", "methodology", "catalog.json") {
		t.Fatalf("unexpected write path: %q", result.Path)
	}
	content, err := os.ReadFile(filepath.Join(root, ".progress", "methodology", "actions", "implement.json"))
	if err != nil {
		t.Fatalf("read written action: %v", err)
	}
	if !containsAll(string(content), `"name": "implement"`, `"class": "engineering-synthesis"`) {
		t.Fatalf("written action does not include action fields: %s", string(content))
	}
}

func TestLoadCatalogReadsFileRegistries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	methodologyDir := filepath.Join(root, ".progress", "methodology")
	writeTestFile(t, filepath.Join(methodologyDir, "catalog.json"), `{"default_route":"task-processing"}`)
	writeTestFile(t, filepath.Join(methodologyDir, "routes", "task-processing.json"), `{"name":"task-processing","action":"implement"}`)
	writeTestFile(t, filepath.Join(methodologyDir, "actions", "implement.json"), `{"name":"implement","profile":"coder"}`)
	writeTestFile(t, filepath.Join(methodologyDir, "instructions", "implement-directive.json"), `{"name":"implement-directive","action":"implement","profile":"coder","body":"Сформировать изменение."}`)
	writeTestFile(t, filepath.Join(methodologyDir, "operations", "prepare-data.json"), `{"name":"prepare-data","kind":"prepare-data","title":"Подготовка данных","type":"builtin","required":true}`)
	writeTestFile(t, filepath.Join(methodologyDir, "entities", "decision-rule--description-assessment.json"), `{"kind":"decision-rule","name":"description-assessment","target_contour":"decision","payload":{"label":"description-assessment"}}`)

	snapshot, err := LoadCatalogWithHome(root, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if snapshot.Catalog.DefaultRoute != "task-processing" {
		t.Fatalf("unexpected default route: %q", snapshot.Catalog.DefaultRoute)
	}
	if len(snapshot.Catalog.Routes) != 1 || snapshot.Catalog.Routes[0].Name != "task-processing" {
		t.Fatalf("unexpected routes: %#v", snapshot.Catalog.Routes)
	}
	if len(snapshot.Catalog.Operations) != 1 || snapshot.Catalog.Operations[0].Name != "prepare-data" {
		t.Fatalf("unexpected operations: %#v", snapshot.Catalog.Operations)
	}
	if len(snapshot.Catalog.Entities) != 1 || entitySourceKey(snapshot.Catalog.Entities[0].Kind, snapshot.Catalog.Entities[0].Name) != "decision-rule/description-assessment" {
		t.Fatalf("unexpected entities: %#v", snapshot.Catalog.Entities)
	}
}

func TestLoadCatalogRejectsRegistryFileNameMismatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	methodologyDir := filepath.Join(root, ".progress", "methodology")
	writeTestFile(t, filepath.Join(methodologyDir, "catalog.json"), `{}`)
	writeTestFile(t, filepath.Join(methodologyDir, "routes", "task-processing.json"), `{"name":"other-route","action":"implement"}`)

	_, err := LoadCatalogWithHome(root, t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected file name mismatch error")
	}
	if !strings.Contains(err.Error(), `key "other-route" must match file name "task-processing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceUpsertRejectsEmptyActionOperation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(nil)

	_, err := service.Upsert(context.Background(), CatalogWriteRequest{
		RepoRoot: root,
		Scope:    CatalogWriteScopeLocal,
		Element: ElementUpsert{Action: &Action{
			Name:       "implement",
			Operations: []ActionOperation{{}},
		}},
	})
	if err == nil {
		t.Fatal("expected empty operation error")
	}
	if !strings.Contains(err.Error(), `action "implement" operations[0] must define name`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceResolveLoadsStoredCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configDir := filepath.Join(root, ".progress", "methodology")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir catalog dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "catalog.json"), []byte(`{
		"routes": [{"name": "default", "action": "implement", "profile": "coder"}],
		"actions": [{"name": "implement", "class": "engineering-synthesis", "profile": "coder"}],
		"instructions": [{"name": "coder-directive", "profile": "coder", "body": "Сформировать изменение."}]
	}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	result, err := NewService(nil).Resolve(context.Background(), SelectionRequest{RepoRoot: root})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if result.Route.Name != "default" || result.Action.Name != "implement" {
		t.Fatalf("unexpected selection: %#v", result)
	}
	if result.Instruction.Name != "coder-directive" {
		t.Fatalf("expected instruction selection, got: %#v", result.Instruction)
	}
	if result.RouteSource != configuration.ConfigFileSourceLocal {
		t.Fatalf("expected local route source, got: %q", result.RouteSource)
	}
}

func TestListCatalogElementsFiltersGenericEntities(t *testing.T) {
	t.Parallel()

	snapshot := mergeCatalogLayers([]CatalogLayer{{
		Source: configuration.ConfigFileSourceLocal,
		Path:   "/repo/.progress/methodology/catalog.json",
		Catalog: Catalog{Entities: []Entity{
			{Kind: "decision-rule", Name: "description-assessment", TargetContour: "decision"},
			{Kind: "ui-panel", Name: "methodology", TargetContour: "user-interface"},
		}},
	}})

	elements := ListCatalogElements(snapshot, ElementFilter{Kind: "decision-rule", TargetContour: "decision"})
	if len(elements) != 1 {
		t.Fatalf("expected one decision rule, got: %#v", elements)
	}
	if elements[0].Kind != ElementKindEntity || elements[0].EntityKind != "decision-rule" || elements[0].Name != "description-assessment" {
		t.Fatalf("unexpected entity element: %#v", elements[0])
	}
}

func TestLoadCatalogUsesLocalLayerWhenGlobalHomeMissing(t *testing.T) {
	originalResolveUserHome := resolveUserHome
	resolveUserHome = func() (string, error) {
		return "", errors.New("home not available")
	}
	t.Cleanup(func() {
		resolveUserHome = originalResolveUserHome
	})

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/methodology/catalog.json" {
			return []byte(`{
				"routes": [{"name": "default", "action": "implement"}],
				"actions": [{"name": "implement"}]
			}`), nil
		}
		return nil, fs.ErrNotExist
	}

	snapshot, err := LoadCatalogWithHome("/repo", "", readFile)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if snapshot.Sources.Routes["default"] != configuration.ConfigFileSourceLocal {
		t.Fatalf("expected local route source, got: %q", snapshot.Sources.Routes["default"])
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
