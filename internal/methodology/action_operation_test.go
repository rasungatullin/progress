package methodology

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCatalogAcceptsActionOperationHandler(t *testing.T) {
	t.Parallel()

	required := true
	catalog := Catalog{
		Operations: []Operation{{
			Name: "execute-code", Type: "action", Kind: "engineering-synthesis",
			Contract: OperationContract{
				In:  map[string]OperationContractField{"input": {Type: "object", Required: &required}},
				Out: map[string]OperationContractField{"result": {Type: "object", Required: &required}},
			},
		}},
		Actions: []Action{
			{
				Name: "engineering-synthesis",
				Contract: ActionContract{
					In:  map[string]ActionContractField{"input": {Type: "object", Required: &required}},
					Out: map[string]ActionContractField{"result": {Type: "object", Required: &required}},
				},
			},
			{
				Name: "implement",
				Operations: []ActionOperation{{
					Name: "execute-code",
					In:   map[string]ActionMapping{"input": mappingWithValue(`{}`)},
					Out:  map[string]ActionMapping{"result": mappingWithRef("data.result")},
				}},
			},
		},
	}

	if err := validateCatalog(catalog); err != nil {
		t.Fatalf("validate action operation handler: %v", err)
	}
}

func TestValidateCatalogRejectsActionOperationCycle(t *testing.T) {
	t.Parallel()

	catalog := Catalog{
		Operations: []Operation{
			{Name: "call-a", Type: "action", Kind: "action-a"},
			{Name: "call-b", Type: "action", Kind: "action-b"},
		},
		Actions: []Action{
			{Name: "action-a", Operations: []ActionOperation{{Name: "call-b"}}},
			{Name: "action-b", Operations: []ActionOperation{{Name: "call-a"}}},
		},
	}

	err := validateCatalog(catalog)
	if err == nil || !strings.Contains(err.Error(), "action operation cycle") {
		t.Fatalf("expected action operation cycle, got %v", err)
	}
}

func mappingWithRef(ref string) ActionMapping {
	return ActionMapping{Ref: &ref, hasRef: true}
}

func mappingWithValue(value string) ActionMapping {
	message := json.RawMessage(value)
	return ActionMapping{Value: &message, hasValue: true}
}
