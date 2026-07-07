package methodology

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
)

const (
	ElementKindRoute       = "route"
	ElementKindAction      = "action"
	ElementKindInstruction = "instruction"
	ElementKindEntity      = "entity"
	ElementKindOperation   = "operation"
)

type Catalog struct {
	DefaultRoute string        `json:"default_route,omitempty"`
	Routes       []Route       `json:"routes,omitempty"`
	Actions      []Action      `json:"actions,omitempty"`
	Instructions []Instruction `json:"instructions,omitempty"`
	Operations   []Operation   `json:"operations,omitempty"`
	Entities     []Entity      `json:"entities,omitempty"`
}

type Route struct {
	Name            string       `json:"name"`
	Title           string       `json:"title,omitempty"`
	Action          string       `json:"action,omitempty"`
	Outcome         string       `json:"outcome,omitempty"`
	Profile         string       `json:"profile,omitempty"`
	Description     string       `json:"description,omitempty"`
	Checks          []RouteCheck `json:"checks,omitempty"`
	Step            string       `json:"step,omitempty"`
	HasFeatures     []string     `json:"has_features,omitempty"`
	MissingFeatures []string     `json:"missing_features,omitempty"`
	HasLabels       []string     `json:"has_labels,omitempty"`
	MissingLabels   []string     `json:"missing_labels,omitempty"`
	ExpectedResult  string       `json:"expected_result,omitempty"`
	Constraints     []string     `json:"constraints,omitempty"`
	ReasonCode      string       `json:"reason_code,omitempty"`
	ReasonMessage   string       `json:"reason_message,omitempty"`
}

type RouteCheck struct {
	Name            string   `json:"name"`
	Title           string   `json:"title,omitempty"`
	Action          string   `json:"action,omitempty"`
	Outcome         string   `json:"outcome,omitempty"`
	Profile         string   `json:"profile,omitempty"`
	Description     string   `json:"description,omitempty"`
	Step            string   `json:"step,omitempty"`
	HasFeatures     []string `json:"has_features,omitempty"`
	MissingFeatures []string `json:"missing_features,omitempty"`
	HasLabels       []string `json:"has_labels,omitempty"`
	MissingLabels   []string `json:"missing_labels,omitempty"`
	ExpectedResult  string   `json:"expected_result,omitempty"`
	Constraints     []string `json:"constraints,omitempty"`
	ReasonCode      string   `json:"reason_code,omitempty"`
	ReasonMessage   string   `json:"reason_message,omitempty"`
}

func (c *RouteCheck) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*c = RouteCheck{}
		return nil
	}
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return err
		}
		*c = RouteCheck{Name: name}
		return nil
	}

	type rawRouteCheck RouteCheck
	var raw rawRouteCheck
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return err
	}
	*c = RouteCheck(raw)
	return nil
}

type Action struct {
	Name              string            `json:"name"`
	Class             string            `json:"class,omitempty"`
	Profile           string            `json:"profile,omitempty"`
	Aliases           []string          `json:"aliases,omitempty"`
	Contract          ActionContract    `json:"contract,omitempty"`
	RequiresWorkplace *bool             `json:"requires_workplace,omitempty"`
	RequiresSynthesis *bool             `json:"requires_synthesis,omitempty"`
	Operations        []ActionOperation `json:"operations,omitempty"`
	Description       string            `json:"description,omitempty"`
	ExpectedResult    string            `json:"expected_result,omitempty"`
}

type ActionOperation struct {
	Name     string                     `json:"name,omitempty"`
	Kind     string                     `json:"kind,omitempty"`
	Title    string                     `json:"title,omitempty"`
	Origin   string                     `json:"origin,omitempty"`
	Required *bool                      `json:"required,omitempty"`
	In       map[string]OperationBinding `json:"in,omitempty"`
	Out      map[string]OperationBinding `json:"out,omitempty"`
}

func (o *ActionOperation) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*o = ActionOperation{}
		return nil
	}
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return err
		}
		*o = ActionOperation{Name: name, Kind: name}
		return nil
	}

	type rawActionOperation ActionOperation
	var raw rawActionOperation
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return err
	}
	*o = ActionOperation(raw)
	return nil
}

type Instruction struct {
	Name          string `json:"name"`
	Profile       string `json:"profile,omitempty"`
	Action        string `json:"action,omitempty"`
	TargetContour string `json:"target_contour,omitempty"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	Body          string `json:"body,omitempty"`
}

type Operation struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Contract OperationContract `json:"contract,omitempty"`
	Title    string `json:"title,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Required *bool  `json:"required,omitempty"`
}

type OperationContract struct {
	In  map[string]ContractField `json:"in,omitempty"`
	Out map[string]ContractField `json:"out,omitempty"`
}

type ActionContract struct {
	In   map[string]ContractField `json:"in,omitempty"`
	Data map[string]ContractField `json:"data,omitempty"`
	Out  map[string]ContractField `json:"out,omitempty"`
}

type ContractField struct {
	Type        string `json:"type,omitempty"`
	Required    *bool  `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

type OperationBinding struct {
	Ref   *string           `json:"ref,omitempty"`
	Value json.RawMessage   `json:"value,omitempty"`
}

type Entity struct {
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	TargetContour string          `json:"target_contour,omitempty"`
	Title         string          `json:"title,omitempty"`
	Description   string          `json:"description,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type CatalogLayer struct {
	Source  configuration.ConfigFileSource `json:"source"`
	Path    string                         `json:"path"`
	Catalog Catalog                        `json:"catalog"`
}

type CatalogSources struct {
	Routes       map[string]configuration.ConfigFileSource `json:"routes,omitempty"`
	Actions      map[string]configuration.ConfigFileSource `json:"actions,omitempty"`
	Instructions map[string]configuration.ConfigFileSource `json:"instructions,omitempty"`
	Operations   map[string]configuration.ConfigFileSource `json:"operations,omitempty"`
	Entities     map[string]configuration.ConfigFileSource `json:"entities,omitempty"`
}

type CatalogSnapshot struct {
	Catalog           Catalog        `json:"catalog"`
	Layers            []CatalogLayer `json:"layers,omitempty"`
	Sources           CatalogSources `json:"sources,omitempty"`
	GlobalCatalogPath string         `json:"global_catalog_path,omitempty"`
	LocalCatalogPath  string         `json:"local_catalog_path,omitempty"`
}

type ElementFilter struct {
	Kind          string
	EntityKind    string
	TargetContour string
}

type ListedElement struct {
	Kind          string                         `json:"kind"`
	Name          string                         `json:"name"`
	Source        configuration.ConfigFileSource `json:"source,omitempty"`
	EntityKind    string                         `json:"entity_kind,omitempty"`
	TargetContour string                         `json:"target_contour,omitempty"`
	Title         string                         `json:"title,omitempty"`
	Description   string                         `json:"description,omitempty"`
	Profile       string                         `json:"profile,omitempty"`
	Action        string                         `json:"action,omitempty"`
	Outcome       string                         `json:"outcome,omitempty"`
	Class         string                         `json:"class,omitempty"`
	Route         *Route                         `json:"route,omitempty"`
	ActionEntry   *Action                        `json:"action_entry,omitempty"`
	Instruction   *Instruction                   `json:"instruction,omitempty"`
	Entity        *Entity                        `json:"entity,omitempty"`
}

func mergeCatalogLayers(layers []CatalogLayer) CatalogSnapshot {
	snapshot := CatalogSnapshot{
		Sources: CatalogSources{
			Routes:       map[string]configuration.ConfigFileSource{},
			Actions:      map[string]configuration.ConfigFileSource{},
			Instructions: map[string]configuration.ConfigFileSource{},
			Operations:   map[string]configuration.ConfigFileSource{},
			Entities:     map[string]configuration.ConfigFileSource{},
		},
		Layers: append([]CatalogLayer(nil), layers...),
	}

	routeIndexes := map[string]int{}
	actionIndexes := map[string]int{}
	instructionIndexes := map[string]int{}
	operationIndexes := map[string]int{}
	entityIndexes := map[string]int{}

	for _, layer := range layers {
		if layer.Source == configuration.ConfigFileSourceGlobal {
			snapshot.GlobalCatalogPath = layer.Path
		}
		if layer.Source == configuration.ConfigFileSourceLocal {
			snapshot.LocalCatalogPath = layer.Path
		}

		if defaultRoute := normalizeName(layer.Catalog.DefaultRoute); defaultRoute != "" {
			snapshot.Catalog.DefaultRoute = defaultRoute
		}

		for _, route := range layer.Catalog.Routes {
			route = normalizeRoute(route)
			upsertRoute(&snapshot.Catalog, routeIndexes, route)
			snapshot.Sources.Routes[route.Name] = layer.Source
		}
		for _, action := range layer.Catalog.Actions {
			action = normalizeAction(action)
			upsertAction(&snapshot.Catalog, actionIndexes, action)
			snapshot.Sources.Actions[action.Name] = layer.Source
		}
		for _, instruction := range layer.Catalog.Instructions {
			instruction = normalizeInstruction(instruction)
			upsertInstruction(&snapshot.Catalog, instructionIndexes, instruction)
			snapshot.Sources.Instructions[instruction.Name] = layer.Source
		}
		for _, operation := range layer.Catalog.Operations {
			operation = normalizeOperation(operation)
			upsertOperation(&snapshot.Catalog, operationIndexes, operation)
			snapshot.Sources.Operations[operationSourceKey(operation.Name)] = layer.Source
		}
		for _, entity := range layer.Catalog.Entities {
			entity = normalizeEntity(entity)
			key := entitySourceKey(entity.Kind, entity.Name)
			upsertEntity(&snapshot.Catalog, entityIndexes, entity)
			snapshot.Sources.Entities[key] = layer.Source
		}
	}

	return snapshot
}

func ListCatalogElements(snapshot CatalogSnapshot, filter ElementFilter) []ListedElement {
	filter.Kind = normalizeKind(filter.Kind)
	filter.EntityKind = normalizeKind(filter.EntityKind)
	filter.TargetContour = normalizeKind(filter.TargetContour)

	elements := make([]ListedElement, 0, len(snapshot.Catalog.Routes)+len(snapshot.Catalog.Actions)+len(snapshot.Catalog.Instructions)+len(snapshot.Catalog.Entities))

	if filterAllowsTypedKind(filter.Kind, ElementKindRoute) {
		for _, route := range snapshot.Catalog.Routes {
			route := route
			elements = append(elements, ListedElement{
				Kind:        ElementKindRoute,
				Name:        route.Name,
				Source:      snapshot.Sources.Routes[route.Name],
				Title:       route.Title,
				Description: route.Description,
				Profile:     route.Profile,
				Action:      route.Action,
				Outcome:     route.Outcome,
				Route:       &route,
			})
		}
	}

	if filterAllowsTypedKind(filter.Kind, ElementKindAction) {
		for _, action := range snapshot.Catalog.Actions {
			action := action
			elements = append(elements, ListedElement{
				Kind:        ElementKindAction,
				Name:        action.Name,
				Source:      snapshot.Sources.Actions[action.Name],
				Description: action.Description,
				Profile:     action.Profile,
				Class:       action.Class,
				ActionEntry: &action,
			})
		}
	}

	if filterAllowsTypedKind(filter.Kind, ElementKindInstruction) {
		for _, instruction := range snapshot.Catalog.Instructions {
			instruction := instruction
			if filter.TargetContour != "" && normalizeKind(instruction.TargetContour) != filter.TargetContour {
				continue
			}
			elements = append(elements, ListedElement{
				Kind:          ElementKindInstruction,
				Name:          instruction.Name,
				Source:        snapshot.Sources.Instructions[instruction.Name],
				TargetContour: instruction.TargetContour,
				Title:         instruction.Title,
				Description:   instruction.Description,
				Profile:       instruction.Profile,
				Action:        instruction.Action,
				Instruction:   &instruction,
			})
		}
	}

	if filterAllowsEntityKind(filter.Kind) {
		for _, entity := range snapshot.Catalog.Entities {
			entity := entity
			entityKind := normalizeKind(entity.Kind)
			if filter.EntityKind != "" && entityKind != filter.EntityKind {
				continue
			}
			if filter.Kind != "" && filter.Kind != ElementKindEntity && !isTypedElementKind(filter.Kind) && entityKind != filter.Kind {
				continue
			}
			if filter.TargetContour != "" && normalizeKind(entity.TargetContour) != filter.TargetContour {
				continue
			}
			elements = append(elements, ListedElement{
				Kind:          ElementKindEntity,
				Name:          entity.Name,
				Source:        snapshot.Sources.Entities[entitySourceKey(entity.Kind, entity.Name)],
				EntityKind:    entity.Kind,
				TargetContour: entity.TargetContour,
				Title:         entity.Title,
				Description:   entity.Description,
				Entity:        &entity,
			})
		}
	}

	sort.SliceStable(elements, func(i, j int) bool {
		left := elementSortKey(elements[i])
		right := elementSortKey(elements[j])
		return left < right
	})

	return elements
}

func GetCatalogElement(snapshot CatalogSnapshot, kind string, name string, entityKind string) (ListedElement, error) {
	name = normalizeName(name)
	if name == "" {
		return ListedElement{}, fmt.Errorf("имя сущности методики должно быть задано")
	}

	elements := ListCatalogElements(snapshot, ElementFilter{Kind: kind, EntityKind: entityKind})
	for _, element := range elements {
		if element.Name == name {
			return element, nil
		}
	}

	if normalizeKind(kind) == "" {
		return ListedElement{}, fmt.Errorf("сущность методики %q не найдена", name)
	}
	return ListedElement{}, fmt.Errorf("сущность методики %s/%s не найдена", normalizeKind(kind), name)
}

func normalizeCatalog(catalog Catalog) Catalog {
	result := Catalog{DefaultRoute: normalizeName(catalog.DefaultRoute)}
	routeIndexes := map[string]int{}
	actionIndexes := map[string]int{}
	instructionIndexes := map[string]int{}
	operationIndexes := map[string]int{}
	entityIndexes := map[string]int{}

	for _, route := range catalog.Routes {
		route = normalizeRoute(route)
		if route.Name == "" {
			result.Routes = append(result.Routes, route)
			continue
		}
		upsertRoute(&result, routeIndexes, route)
	}
	for _, action := range catalog.Actions {
		action = normalizeAction(action)
		if action.Name == "" {
			result.Actions = append(result.Actions, action)
			continue
		}
		upsertAction(&result, actionIndexes, action)
	}
	for _, instruction := range catalog.Instructions {
		instruction = normalizeInstruction(instruction)
		if instruction.Name == "" {
			result.Instructions = append(result.Instructions, instruction)
			continue
		}
		upsertInstruction(&result, instructionIndexes, instruction)
	}
	for _, operation := range catalog.Operations {
		operation = normalizeOperation(operation)
		if operation.Name == "" {
			result.Operations = append(result.Operations, operation)
			continue
		}
		upsertOperation(&result, operationIndexes, operation)
	}
	for _, entity := range catalog.Entities {
		entity = normalizeEntity(entity)
		if entity.Kind == "" || entity.Name == "" {
			result.Entities = append(result.Entities, entity)
			continue
		}
		upsertEntity(&result, entityIndexes, entity)
	}

	return result
}

func validateCatalog(catalog Catalog) error {
	seenRoutes := map[string]struct{}{}
	for index, route := range catalog.Routes {
		route = normalizeRoute(route)
		if route.Name == "" {
			return fmt.Errorf("routes[%d].name must be non-empty", index)
		}
		if _, ok := seenRoutes[route.Name]; ok {
			return fmt.Errorf("routes contains duplicate name %q", route.Name)
		}
		seenRoutes[route.Name] = struct{}{}
		if route.Action == "" && route.Outcome == "" && len(route.Checks) == 0 {
			return fmt.Errorf("route %q must define action, outcome or checks", route.Name)
		}
		seenChecks := map[string]struct{}{}
		for checkIndex, check := range route.Checks {
			check = normalizeRouteCheck(check)
			if check.Name == "" {
				return fmt.Errorf("route %q checks[%d].name must be non-empty", route.Name, checkIndex)
			}
			if _, ok := seenChecks[check.Name]; ok {
				return fmt.Errorf("route %q checks contains duplicate name %q", route.Name, check.Name)
			}
			seenChecks[check.Name] = struct{}{}
			if check.Action == "" && check.Outcome == "" && !routeCheckIsReference(check) {
				return fmt.Errorf("route %q check %q must define action or outcome", route.Name, check.Name)
			}
		}
	}

	seenOperations := map[string]struct{}{}
	for index, rawOperation := range catalog.Operations {
		operation := normalizeOperation(rawOperation)
		if operation.Name == "" {
			return fmt.Errorf("operations[%d].name must be non-empty", index)
		}
		if err := validateContractFields("operation", operation.Name, "contract.in", operation.Contract.In); err != nil {
			return err
		}
		if err := validateContractFields("operation", operation.Name, "contract.out", operation.Contract.Out); err != nil {
			return err
		}
		if _, ok := seenOperations[operation.Name]; ok {
			return fmt.Errorf("operations contains duplicate name %q", operation.Name)
		}
		seenOperations[operation.Name] = struct{}{}
	}
	hasOperationRegistry := len(seenOperations) > 0

	seenActions := map[string]struct{}{}
	normalizedActions := make([]Action, 0, len(catalog.Actions))
	for index, rawAction := range catalog.Actions {
		action := normalizeAction(rawAction)
		normalizedActions = append(normalizedActions, action)
		if action.Name == "" {
			return fmt.Errorf("actions[%d].name must be non-empty", index)
		}
		if _, ok := seenActions[action.Name]; ok {
			return fmt.Errorf("actions contains duplicate name %q", action.Name)
		}
		seenActions[action.Name] = struct{}{}
	}
	seenActionAliases := map[string]string{}
	for actionIndex, action := range normalizedActions {
		seenAliases := map[string]struct{}{}
		seenOperationRefs := map[string]struct{}{}
		if err := validateContractFields("action", action.Name, "contract.in", action.Contract.In); err != nil {
			return err
		}
		if err := validateContractFields("action", action.Name, "contract.data", action.Contract.Data); err != nil {
			return err
		}
		if err := validateContractFields("action", action.Name, "contract.out", action.Contract.Out); err != nil {
			return err
		}
		for operationIndex, operation := range catalog.Actions[actionIndex].Operations {
			operation = normalizeActionOperation(operation)
			if operation.Name == "" && operation.Kind == "" {
				return fmt.Errorf("action %q operations[%d] must define name or kind", action.Name, operationIndex)
			}
			if err := validateActionOperationBindings(action.Name, operationIndex, operation); err != nil {
				return err
			}
			if _, ok := seenOperationRefs[operation.Name]; ok {
				return fmt.Errorf("action %q operations contains duplicate name %q", action.Name, operation.Name)
			}
			seenOperationRefs[operation.Name] = struct{}{}
			if hasOperationRegistry && operation.Name != "" && operation.Origin == "" && operation.Title == "" && operation.Required == nil {
				if _, ok := seenOperations[operation.Name]; !ok {
					return fmt.Errorf("action %q operations[%d] references unknown operation %q", action.Name, operationIndex, operation.Name)
				}
			}
		}
		for _, alias := range action.Aliases {
			if alias == action.Name {
				return fmt.Errorf("action %q aliases must not repeat action name", action.Name)
			}
			if _, ok := seenAliases[alias]; ok {
				return fmt.Errorf("action %q aliases contains duplicate name %q", action.Name, alias)
			}
			seenAliases[alias] = struct{}{}
			if _, ok := seenActions[alias]; ok {
				return fmt.Errorf("action %q alias %q conflicts with action name", action.Name, alias)
			}
			if owner, ok := seenActionAliases[alias]; ok {
				return fmt.Errorf("action %q alias %q conflicts with action %q alias", action.Name, alias, owner)
			}
			seenActionAliases[alias] = action.Name
		}
	}

	seenInstructions := map[string]struct{}{}
	for index, instruction := range catalog.Instructions {
		instruction = normalizeInstruction(instruction)
		if instruction.Name == "" {
			return fmt.Errorf("instructions[%d].name must be non-empty", index)
		}
		if _, ok := seenInstructions[instruction.Name]; ok {
			return fmt.Errorf("instructions contains duplicate name %q", instruction.Name)
		}
		seenInstructions[instruction.Name] = struct{}{}
	}

	seenEntities := map[string]struct{}{}
	for index, entity := range catalog.Entities {
		entity = normalizeEntity(entity)
		if entity.Kind == "" {
			return fmt.Errorf("entities[%d].kind must be non-empty", index)
		}
		if entity.Name == "" {
			return fmt.Errorf("entities[%d].name must be non-empty", index)
		}
		key := entitySourceKey(entity.Kind, entity.Name)
		if _, ok := seenEntities[key]; ok {
			return fmt.Errorf("entities contains duplicate key %q", key)
		}
		seenEntities[key] = struct{}{}
		if payload := bytes.TrimSpace(entity.Payload); len(payload) > 0 && !json.Valid(payload) {
			return fmt.Errorf("entity %q contains invalid payload JSON", key)
		}
	}

	return nil
}

func validateContractFields(entityType, entityName, section string, fields map[string]ContractField) error {
	for fieldName, field := range fields {
		if strings.TrimSpace(field.Type) == "" {
			return fmt.Errorf("%s %q %s.%s.type must be non-empty", entityType, entityName, section, fieldName)
		}
	}
	return nil
}

func validateActionOperationBindings(actionName string, operationIndex int, operation ActionOperation) error {
	if err := validateActionOperationBindingMap(fmt.Sprintf("action %q operations[%d] in", actionName, operationIndex), operation.In); err != nil {
		return err
	}
	if err := validateActionOperationBindingMap(fmt.Sprintf("action %q operations[%d] out", actionName, operationIndex), operation.Out); err != nil {
		return err
	}
	return nil
}

func validateActionOperationBindingMap(prefix string, bindings map[string]OperationBinding) error {
	for fieldName, binding := range bindings {
		hasRef := binding.Ref != nil
		hasValue := len(bytes.TrimSpace(binding.Value)) > 0
		if hasRef == hasValue {
			if hasRef {
				return fmt.Errorf("%s.%q has both ref and value", prefix, fieldName)
			}
			return fmt.Errorf("%s.%q must specify either ref or value", prefix, fieldName)
		}
		if hasRef && strings.TrimSpace(*binding.Ref) == "" {
			return fmt.Errorf("%s.%q.ref must be non-empty", prefix, fieldName)
		}
	}
	return nil
}

func upsertRoute(catalog *Catalog, indexes map[string]int, route Route) {
	if index, ok := indexes[route.Name]; ok {
		catalog.Routes[index] = route
		return
	}
	indexes[route.Name] = len(catalog.Routes)
	catalog.Routes = append(catalog.Routes, route)
}

func upsertAction(catalog *Catalog, indexes map[string]int, action Action) {
	if index, ok := indexes[action.Name]; ok {
		catalog.Actions[index] = action
		return
	}
	indexes[action.Name] = len(catalog.Actions)
	catalog.Actions = append(catalog.Actions, action)
}

func upsertInstruction(catalog *Catalog, indexes map[string]int, instruction Instruction) {
	if index, ok := indexes[instruction.Name]; ok {
		catalog.Instructions[index] = instruction
		return
	}
	indexes[instruction.Name] = len(catalog.Instructions)
	catalog.Instructions = append(catalog.Instructions, instruction)
}

func upsertOperation(catalog *Catalog, indexes map[string]int, operation Operation) {
	key := operationSourceKey(operation.Name)
	if index, ok := indexes[key]; ok {
		catalog.Operations[index] = operation
		return
	}
	indexes[key] = len(catalog.Operations)
	catalog.Operations = append(catalog.Operations, operation)
}

func upsertEntity(catalog *Catalog, indexes map[string]int, entity Entity) {
	key := entitySourceKey(entity.Kind, entity.Name)
	if index, ok := indexes[key]; ok {
		catalog.Entities[index] = entity
		return
	}
	indexes[key] = len(catalog.Entities)
	catalog.Entities = append(catalog.Entities, entity)
}

func normalizeRoute(route Route) Route {
	route.Name = normalizeName(route.Name)
	route.Title = strings.TrimSpace(route.Title)
	route.Action = normalizeName(route.Action)
	route.Outcome = normalizeName(route.Outcome)
	route.Profile = strings.TrimSpace(route.Profile)
	route.Description = strings.TrimSpace(route.Description)
	route.Checks = normalizeRouteChecks(route.Checks)
	route.Step = strings.TrimSpace(route.Step)
	route.HasFeatures = normalizeNameList(route.HasFeatures)
	route.MissingFeatures = normalizeNameList(route.MissingFeatures)
	route.HasLabels = normalizeNameList(route.HasLabels)
	route.MissingLabels = normalizeNameList(route.MissingLabels)
	route.ExpectedResult = strings.TrimSpace(route.ExpectedResult)
	route.Constraints = normalizeStringList(route.Constraints)
	route.ReasonCode = strings.TrimSpace(route.ReasonCode)
	route.ReasonMessage = strings.TrimSpace(route.ReasonMessage)
	return route
}

func normalizeRouteChecks(checks []RouteCheck) []RouteCheck {
	result := make([]RouteCheck, 0, len(checks))
	for _, check := range checks {
		check = normalizeRouteCheck(check)
		if check.Name == "" {
			continue
		}
		result = append(result, check)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeRouteCheck(check RouteCheck) RouteCheck {
	check.Name = normalizeName(check.Name)
	check.Title = strings.TrimSpace(check.Title)
	check.Action = normalizeName(check.Action)
	check.Outcome = normalizeName(check.Outcome)
	check.Profile = strings.TrimSpace(check.Profile)
	check.Description = strings.TrimSpace(check.Description)
	check.Step = strings.TrimSpace(check.Step)
	check.HasFeatures = normalizeNameList(check.HasFeatures)
	check.MissingFeatures = normalizeNameList(check.MissingFeatures)
	check.HasLabels = normalizeNameList(check.HasLabels)
	check.MissingLabels = normalizeNameList(check.MissingLabels)
	check.ExpectedResult = strings.TrimSpace(check.ExpectedResult)
	check.Constraints = normalizeStringList(check.Constraints)
	check.ReasonCode = strings.TrimSpace(check.ReasonCode)
	check.ReasonMessage = strings.TrimSpace(check.ReasonMessage)
	return check
}

func routeCheckIsReference(check RouteCheck) bool {
	return check.Name != "" &&
		check.Title == "" &&
		check.Action == "" &&
		check.Outcome == "" &&
		check.Profile == "" &&
		check.Description == "" &&
		check.Step == "" &&
		len(check.HasFeatures) == 0 &&
		len(check.MissingFeatures) == 0 &&
		len(check.HasLabels) == 0 &&
		len(check.MissingLabels) == 0 &&
		check.ExpectedResult == "" &&
		len(check.Constraints) == 0 &&
		check.ReasonCode == "" &&
		check.ReasonMessage == ""
}

func normalizeAction(action Action) Action {
	action.Name = normalizeName(action.Name)
	action.Class = strings.TrimSpace(action.Class)
	action.Profile = strings.TrimSpace(action.Profile)
	action.Aliases = normalizeNameList(action.Aliases)
	action.Contract = normalizeActionContract(action.Contract)
	action.Operations = normalizeActionOperations(action.Operations)
	action.Description = strings.TrimSpace(action.Description)
	action.ExpectedResult = strings.TrimSpace(action.ExpectedResult)
	return action
}

func normalizeActionOperations(operations []ActionOperation) []ActionOperation {
	result := make([]ActionOperation, 0, len(operations))
	for _, operation := range operations {
		operation = normalizeActionOperation(operation)
		if len(operation.In) == 0 {
			operation.In = nil
		}
		if len(operation.Out) == 0 {
			operation.Out = nil
		}
		if operation.Name == "" && operation.Kind == "" {
			continue
		}
		result = append(result, operation)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeActionOperation(operation ActionOperation) ActionOperation {
	operation.Name = normalizeName(operation.Name)
	operation.Kind = normalizeName(operation.Kind)
	if operation.Name == "" {
		operation.Name = operation.Kind
	}
	if operation.Kind == "" {
		operation.Kind = operation.Name
	}
	operation.Title = strings.TrimSpace(operation.Title)
	operation.Origin = strings.TrimSpace(operation.Origin)
	operation.In = normalizeActionOperationBindings(operation.In)
	operation.Out = normalizeActionOperationBindings(operation.Out)
	return operation
}

func normalizeActionContract(contract ActionContract) ActionContract {
	contract.In = normalizeContractFields(contract.In)
	contract.Data = normalizeContractFields(contract.Data)
	contract.Out = normalizeContractFields(contract.Out)
	return contract
}

func normalizeContractFields(fields map[string]ContractField) map[string]ContractField {
	if len(fields) == 0 {
		return fields
	}
	for key, field := range fields {
		field.Type = strings.TrimSpace(field.Type)
		field.Description = strings.TrimSpace(field.Description)
		fields[key] = field
	}
	return fields
}

func normalizeActionOperationBindings(bindings map[string]OperationBinding) map[string]OperationBinding {
	if len(bindings) == 0 {
		return bindings
	}
	for field, binding := range bindings {
		if binding.Ref != nil {
			ref := strings.TrimSpace(*binding.Ref)
			binding.Ref = &ref
		}
		bindings[field] = binding
	}
	return bindings
}

func normalizeOperation(operation Operation) Operation {
	operation.Name = normalizeName(operation.Name)
	operation.Kind = normalizeName(operation.Kind)
	if operation.Name == "" {
		operation.Name = operation.Kind
	}
	if operation.Kind == "" {
		operation.Kind = operation.Name
	}
	operation.Contract.In = normalizeContractFields(operation.Contract.In)
	operation.Contract.Out = normalizeContractFields(operation.Contract.Out)
	operation.Title = strings.TrimSpace(operation.Title)
	operation.Origin = strings.TrimSpace(operation.Origin)
	return operation
}

func normalizeInstruction(instruction Instruction) Instruction {
	instruction.Name = normalizeName(instruction.Name)
	instruction.Profile = strings.TrimSpace(instruction.Profile)
	instruction.Action = normalizeName(instruction.Action)
	instruction.TargetContour = normalizeKind(instruction.TargetContour)
	instruction.Title = strings.TrimSpace(instruction.Title)
	instruction.Description = strings.TrimSpace(instruction.Description)
	instruction.Body = strings.TrimSpace(instruction.Body)
	return instruction
}

func normalizeEntity(entity Entity) Entity {
	entity.Kind = normalizeKind(entity.Kind)
	entity.Name = normalizeName(entity.Name)
	entity.TargetContour = normalizeKind(entity.TargetContour)
	entity.Title = strings.TrimSpace(entity.Title)
	entity.Description = strings.TrimSpace(entity.Description)
	entity.Payload = bytes.TrimSpace(entity.Payload)
	if len(entity.Payload) > 0 && json.Valid(entity.Payload) {
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, entity.Payload); err == nil {
			entity.Payload = compacted.Bytes()
		}
	}
	return entity
}

func normalizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeNameList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalizeName(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func normalizeKind(kind string) string {
	return strings.TrimSpace(strings.ToLower(kind))
}

func operationSourceKey(operationName string) string {
	return normalizeName(operationName)
}

func entitySourceKey(kind string, name string) string {
	return normalizeKind(kind) + "/" + normalizeName(name)
}

func filterAllowsTypedKind(filterKind string, typedKind string) bool {
	filterKind = normalizeKind(filterKind)
	if filterKind == "" {
		return true
	}
	return filterKind == typedKind
}

func filterAllowsEntityKind(filterKind string) bool {
	filterKind = normalizeKind(filterKind)
	return filterKind == "" || filterKind == ElementKindEntity || !isTypedElementKind(filterKind)
}

func isTypedElementKind(kind string) bool {
	switch normalizeKind(kind) {
	case ElementKindRoute, ElementKindAction, ElementKindInstruction:
		return true
	default:
		return false
	}
}

func elementSortKey(element ListedElement) string {
	keyKind := element.Kind
	if element.Kind == ElementKindEntity {
		keyKind = ElementKindEntity + ":" + element.EntityKind
	}
	return keyKind + "/" + element.Name
}
