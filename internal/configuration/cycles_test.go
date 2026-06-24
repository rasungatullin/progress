package configuration

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestLoadExecutionCycleConfigLoadsAndValidatesConfig(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/execution/cycles.json" {
			return []byte(`{
				"cycles": {
					"implementation-review": {
						"start_step": "implementation",
						"limits": {"max_executions": 6},
						"steps": [
							{
								"name": "implementation",
								"profile": "coder",
								"transitions": [{"not_in": ["ok", "approve", "approved"], "to": "review"}]
							},
							{
								"name": "review",
								"profile": "review",
								"transitions": [{"not_in": ["ok", "approve", "approved"], "to": "implementation"}]
							}
						]
					}
				}
			}`), nil
		}

		return nil, errors.New("config not found")
	}

	config, err := LoadExecutionCycleConfig("/repo", readFile)
	if err != nil {
		t.Fatalf("load cycle config: %v", err)
	}
	definition, ok := config.Cycles["implementation-review"]
	if !ok {
		t.Fatal("expected implementation-review cycle")
	}
	if definition.StartStep != "implementation" || definition.Limits.MaxExecutions != 6 || len(definition.Steps) != 2 {
		t.Fatalf("unexpected cycle definition: %#v", definition)
	}
}

func TestLoadExecutionCycleConfigFailsWhenFileMissing(t *testing.T) {
	t.Parallel()

	_, err := LoadExecutionCycleConfig("/repo", func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	})
	if err == nil {
		t.Fatal("expected missing config error")
	}
	if !strings.Contains(err.Error(), "execution cycle config not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadExecutionCycleConfigFailsWhenDefinitionHasInvalidTransition(t *testing.T) {
	t.Parallel()

	readFile := func(path string) ([]byte, error) {
		if path == "/repo/.progress/execution/cycles.json" {
			return []byte(`{
				"cycles": {
					"loop": {
						"start_step": "a",
						"limits": {"max_executions": 3},
						"steps": [
							{"name":"a","profile":"coder","transitions":[{"to":"missing","in":["ok"]}]}
						]
					}
				}
			}`), nil
		}
		return nil, errors.New("config not found")
	}

	_, err := LoadExecutionCycleConfig("/repo", readFile)
	if err == nil {
		t.Fatal("expected invalid transition error")
	}
	if !strings.Contains(err.Error(), `has transition to unknown step "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
