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
)

type Catalog struct {
	Routes       []Route       `json:"routes,omitempty"`
	Actions      []Action      `json:"actions,omitempty"`
	Instructions []Instruction `json:"instructions,omitempty"`
	Entities     []Entity      `json:"entities,omitempty"`
}

type Route struct {
	Name            string   `json:"name"`
	Title           string   `json:"title,omitempty"`
	Action          string   `json:"action,omitempty"`
	Outcome         string   `json:"outcome,omitempty"`
	Profile         string   `json:"profile,omitempty"`
	Description     string   `json:"description,omitempty"`
	Checks          []string `json:"checks,omitempty"`
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

type Action struct {
	Name           string   `json:"name"`
	Class          string   `json:"class,omitempty"`
	Profile        string   `json:"profile,omitempty"`
	Operations     []string `json:"operations,omitempty"`
	Description    string   `json:"description,omitempty"`
	ExpectedResult string   `json:"expected_result,omitempty"`
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
			Entities:     map[string]configuration.ConfigFileSource{},
		},
		Layers: append([]CatalogLayer(nil), layers...),
	}

	routeIndexes := map[string]int{}
	actionIndexes := map[string]int{}
	instructionIndexes := map[string]int{}
	entityIndexes := map[string]int{}

	for _, layer := range layers {
		if layer.Source == configuration.ConfigFileSourceGlobal {
			snapshot.GlobalCatalogPath = layer.Path
		}
		if layer.Source == configuration.ConfigFileSourceLocal {
			snapshot.LocalCatalogPath = layer.Path
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
	result := Catalog{}
	routeIndexes := map[string]int{}
	actionIndexes := map[string]int{}
	instructionIndexes := map[string]int{}
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
		if route.Action == "" && route.Outcome == "" {
			return fmt.Errorf("route %q must define action or outcome", route.Name)
		}
	}

	seenActions := map[string]struct{}{}
	for index, action := range catalog.Actions {
		action = normalizeAction(action)
		if action.Name == "" {
			return fmt.Errorf("actions[%d].name must be non-empty", index)
		}
		if _, ok := seenActions[action.Name]; ok {
			return fmt.Errorf("actions contains duplicate name %q", action.Name)
		}
		seenActions[action.Name] = struct{}{}
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
	route.Checks = normalizeStringList(route.Checks)
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

func normalizeAction(action Action) Action {
	action.Name = normalizeName(action.Name)
	action.Class = strings.TrimSpace(action.Class)
	action.Profile = strings.TrimSpace(action.Profile)
	action.Operations = normalizeStringList(action.Operations)
	action.Description = strings.TrimSpace(action.Description)
	action.ExpectedResult = strings.TrimSpace(action.ExpectedResult)
	return action
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
