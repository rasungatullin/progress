package contours

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRegistryIsValid(t *testing.T) {
	t.Parallel()

	if err := DefaultRegistry().Validate(); err != nil {
		t.Fatalf("validate default registry: %v", err)
	}
}

func TestDefaultRegistryReferencesExistingImplementationAndDocumentation(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, contour := range DefaultRegistry().Contours {
		if _, err := os.Stat(filepath.Join(root, contour.Package)); err != nil {
			t.Fatalf("implementation path for %s: %v", contour.Name, err)
		}
		if _, err := os.Stat(filepath.Join(root, contour.Documentation)); err != nil {
			t.Fatalf("documentation path for %s: %v", contour.Name, err)
		}
	}
}

func TestDefaultRegistryContainsBaseCycle(t *testing.T) {
	t.Parallel()

	contracts := map[string]Contract{}
	for _, contract := range DefaultRegistry().Contracts {
		contracts[contract.ID] = contract
	}

	expected := map[string][2]string{
		"reactivity-to-integration-restore-request":    {Reactivity, Integration},
		"reactivity-to-decision-consideration-context": {Reactivity, Decision},
		"reactivity-to-execution-dispatch":             {Reactivity, Execution},
		"integration-to-decision-canonical-context":    {Integration, Decision},
		"decision-to-execution-assignment":             {Decision, Execution},
		"execution-to-decision-result":                 {Execution, Decision},
		"execution-to-integration-operation":           {Execution, Integration},
		"decision-to-execution-queue-item":             {Decision, ExecutionQueue},
		"execution-queue-to-execution-assignment":      {ExecutionQueue, Execution},
		"reactivity-to-observability-events":           {Reactivity, Observability},
		"integration-to-observability-events":          {Integration, Observability},
		"decision-to-observability-events":             {Decision, Observability},
		"execution-to-observability-events":            {Execution, Observability},
	}
	for id, pair := range expected {
		contract, ok := contracts[id]
		if !ok {
			t.Fatalf("expected contract %q", id)
		}
		if contract.Source != pair[0] || contract.Target != pair[1] {
			t.Fatalf("unexpected direction for %s: %#v", id, contract)
		}
	}
}

func TestRegistryValidationRejectsUnknownContour(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registry.Contracts = append(registry.Contracts, Contract{
		ID:         "bad",
		Source:     "unknown",
		Target:     Execution,
		Kind:       ContractKindRead,
		Object:     "объект",
		Summary:    "Описание.",
		Invariants: []string{"инвариант"},
	})

	if err := registry.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRegistryValidationRejectsDuplicateExchange(t *testing.T) {
	t.Parallel()

	registry := DefaultRegistry()
	registry.Contracts = append(registry.Contracts, Contract{
		ID:         "duplicate-exchange",
		Source:     Reactivity,
		Target:     Integration,
		Kind:       ContractKindRead,
		Object:     "параметры восстановления данных",
		Summary:    "Повтор обмена.",
		Invariants: []string{"инвариант"},
	})

	if err := registry.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
