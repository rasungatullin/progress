package methodology

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
)

const (
	configDefaultHome       = ".config/progress"
	configHomeEnvVar        = "PROGRESS_CONFIG_HOME"
	catalogGlobalFilePath   = "methodology/catalog.json"
	catalogLocalFilePath    = ".progress/methodology/catalog.json"
	CatalogWriteScopeGlobal = configuration.ConfigFileSourceGlobal
	CatalogWriteScopeLocal  = configuration.ConfigFileSourceLocal
)

type ReadFileFunc = configuration.ReadFileFunc
type WriteFileFunc func(string, []byte, fs.FileMode) error
type MkdirAllFunc func(string, fs.FileMode) error
type ReadDirFunc func(string) ([]fs.DirEntry, error)
type RemoveAllFunc func(string) error

type CatalogRequest struct {
	RepoRoot   string
	ConfigHome string
}

type ElementRequest struct {
	RepoRoot      string
	ConfigHome    string
	Kind          string
	Name          string
	EntityKind    string
	TargetContour string
}

type ElementUpsert struct {
	Route       *Route
	Action      *Action
	Instruction *Instruction
	Entity      *Entity
}

type CatalogWriteRequest struct {
	RepoRoot   string
	ConfigHome string
	Scope      configuration.ConfigFileSource
	Catalog    *Catalog
	Element    ElementUpsert
}

type CatalogWriteResult struct {
	Scope    configuration.ConfigFileSource `json:"scope"`
	Path     string                         `json:"path"`
	Catalog  Catalog                        `json:"catalog"`
	Snapshot CatalogSnapshot                `json:"snapshot"`
}

var resolveUserHome = os.UserHomeDir

func LoadCatalog(repoRoot string, readFile ReadFileFunc) (CatalogSnapshot, error) {
	return LoadCatalogWithHome(repoRoot, "", readFile)
}

func LoadCatalogWithHome(repoRoot, configHome string, readFile ReadFileFunc) (CatalogSnapshot, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	return loadCatalogWithHome(repoRoot, configHome, readFile, os.ReadDir)
}

func loadCatalogWithHome(repoRoot, configHome string, readFile ReadFileFunc, readDir ReadDirFunc) (CatalogSnapshot, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	if readDir == nil {
		readDir = os.ReadDir
	}

	home, globalHomeErr := resolveConfigHome(configHome)
	globalPath := ""
	useGlobalLayer := globalHomeErr == nil
	if useGlobalLayer {
		globalPath = filepath.Join(home, catalogGlobalFilePath)
	}

	localPath := ""
	useLocalLayer := strings.TrimSpace(repoRoot) != ""
	if useLocalLayer {
		localPath = filepath.Join(repoRoot, catalogLocalFilePath)
	}

	layers := make([]CatalogLayer, 0, 2)
	if useGlobalLayer {
		if layer, err := readCatalogLayer(globalPath, configuration.ConfigFileSourceGlobal, readFile, readDir); err == nil {
			layers = append(layers, layer)
		} else if !isNotExistErr(err) {
			return CatalogSnapshot{}, err
		}
	}

	if useLocalLayer {
		if layer, err := readCatalogLayer(localPath, configuration.ConfigFileSourceLocal, readFile, readDir); err == nil {
			layers = append(layers, layer)
		} else if !isNotExistErr(err) {
			return CatalogSnapshot{}, err
		}
	}

	if len(layers) == 0 {
		if useGlobalLayer && useLocalLayer {
			return CatalogSnapshot{}, fmt.Errorf("methodology catalog not found: global=%s local=%s", globalPath, localPath)
		}
		if useGlobalLayer {
			return CatalogSnapshot{}, fmt.Errorf("methodology catalog not found: global=%s", globalPath)
		}
		if useLocalLayer {
			return CatalogSnapshot{}, fmt.Errorf("methodology catalog not found: global layer unavailable (%v) local=%s", globalHomeErr, localPath)
		}
		return CatalogSnapshot{}, fmt.Errorf("methodology catalog not found: global layer unavailable (%v)", globalHomeErr)
	}

	return mergeCatalogLayers(layers), nil
}

func SaveCatalogWithHome(repoRoot, configHome string, scope configuration.ConfigFileSource, catalog Catalog, readFile ReadFileFunc, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) (CatalogWriteResult, error) {
	return SaveCatalogWithHomeFS(repoRoot, configHome, scope, catalog, readFile, writeFile, mkdirAll, os.RemoveAll, os.ReadDir)
}

func SaveCatalogWithHomeFS(repoRoot, configHome string, scope configuration.ConfigFileSource, catalog Catalog, readFile ReadFileFunc, writeFile WriteFileFunc, mkdirAll MkdirAllFunc, removeAll RemoveAllFunc, readDir ReadDirFunc) (CatalogWriteResult, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	if readDir == nil {
		readDir = os.ReadDir
	}
	if scope == "" {
		scope = CatalogWriteScopeLocal
	}

	path, err := catalogPathForScope(repoRoot, configHome, scope)
	if err != nil {
		return CatalogWriteResult{}, err
	}

	if err := validateCatalog(catalog); err != nil {
		return CatalogWriteResult{}, fmt.Errorf("invalid methodology catalog: %w", err)
	}
	if err := validateCatalogInstructionBodyFiles(path, catalog); err != nil {
		return CatalogWriteResult{}, fmt.Errorf("invalid methodology catalog: %w", err)
	}
	catalog = normalizeCatalog(catalog)
	if err := writeCatalogFiles(path, catalog, writeFile, mkdirAll, removeAll); err != nil {
		return CatalogWriteResult{}, err
	}

	snapshot, err := loadCatalogWithHome(repoRoot, configHome, readFile, readDir)
	if err != nil {
		return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog}, nil
	}
	return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog, Snapshot: snapshot}, nil
}

func UpsertCatalogElementWithHome(repoRoot, configHome string, scope configuration.ConfigFileSource, element ElementUpsert, readFile ReadFileFunc, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) (CatalogWriteResult, error) {
	return UpsertCatalogElementWithHomeFS(repoRoot, configHome, scope, element, readFile, writeFile, mkdirAll, os.ReadDir, os.RemoveAll)
}

func UpsertCatalogElementWithHomeFS(repoRoot, configHome string, scope configuration.ConfigFileSource, element ElementUpsert, readFile ReadFileFunc, writeFile WriteFileFunc, mkdirAll MkdirAllFunc, readDir ReadDirFunc, removeAll RemoveAllFunc) (CatalogWriteResult, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}
	if readDir == nil {
		readDir = os.ReadDir
	}
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	if scope == "" {
		scope = CatalogWriteScopeLocal
	}

	path, err := catalogPathForScope(repoRoot, configHome, scope)
	if err != nil {
		return CatalogWriteResult{}, err
	}

	layer, legacy, err := readOptionalCatalogLayer(path, scope, readFile, readDir)
	if err != nil {
		return CatalogWriteResult{}, err
	}
	catalog := layer.Catalog
	catalog, err = applyElementUpsert(catalog, element)
	if err != nil {
		return CatalogWriteResult{}, err
	}
	if err := validateCatalogInstructionBodyFiles(path, catalog); err != nil {
		return CatalogWriteResult{}, fmt.Errorf("invalid methodology catalog: %w", err)
	}
	if legacy {
		err = writeCatalogFiles(path, catalog, writeFile, mkdirAll, removeAll)
	} else {
		err = writeCatalogConfig(path, catalog, writeFile, mkdirAll)
		if err == nil {
			err = writeCatalogElement(path, element, writeFile, mkdirAll)
		}
	}
	if err != nil {
		return CatalogWriteResult{}, err
	}

	snapshot, err := loadCatalogWithHome(repoRoot, configHome, readFile, readDir)
	if err != nil {
		return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog}, nil
	}
	return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog, Snapshot: snapshot}, nil
}

func readCatalogLayer(path string, source configuration.ConfigFileSource, readFile ReadFileFunc, readDir ReadDirFunc) (CatalogLayer, error) {
	layer, _, err := readCatalogLayerDetailed(path, source, readFile, readDir)
	return layer, err
}

func readCatalogLayerDetailed(path string, source configuration.ConfigFileSource, readFile ReadFileFunc, readDir ReadDirFunc) (CatalogLayer, bool, error) {
	catalog := Catalog{}
	legacy := false
	rootFound := false

	content, err := readFile(path)
	if err == nil {
		rootFound = true
		rootCatalog, err := decodeCatalog(content)
		if err != nil {
			return CatalogLayer{}, false, fmt.Errorf("parse methodology catalog %s: %w", path, err)
		}
		legacy = catalogHasObjects(rootCatalog)
		catalog.DefaultRoute = rootCatalog.DefaultRoute
		catalog.Routes = append(catalog.Routes, rootCatalog.Routes...)
		catalog.Actions = append(catalog.Actions, rootCatalog.Actions...)
		catalog.Instructions = append(catalog.Instructions, rootCatalog.Instructions...)
		catalog.Operations = append(catalog.Operations, rootCatalog.Operations...)
		catalog.Entities = append(catalog.Entities, rootCatalog.Entities...)
	} else if !isNotExistErr(err) {
		return CatalogLayer{}, false, err
	}
	for index, instruction := range catalog.Instructions {
		catalog.Instructions[index], err = loadInstructionBody(instruction, path, filepath.Dir(path), readFile)
		if err != nil {
			return CatalogLayer{}, false, err
		}
	}

	registryCatalog, registryFound, err := readCatalogRegistries(filepath.Dir(path), readFile, readDir)
	if err != nil {
		return CatalogLayer{}, false, err
	}
	if !rootFound && !registryFound {
		return CatalogLayer{}, false, fs.ErrNotExist
	}
	catalog.Routes = append(catalog.Routes, registryCatalog.Routes...)
	catalog.Actions = append(catalog.Actions, registryCatalog.Actions...)
	for _, instruction := range registryCatalog.Instructions {
		instruction, err = loadInstructionBody(instruction, filepath.Join(filepath.Dir(path), "instructions", instructionRegistryKey(instruction)+".json"), filepath.Dir(path), readFile)
		if err != nil {
			return CatalogLayer{}, false, err
		}
		catalog.Instructions = append(catalog.Instructions, instruction)
	}
	catalog.Operations = append(catalog.Operations, registryCatalog.Operations...)
	catalog.Entities = append(catalog.Entities, registryCatalog.Entities...)

	if err := validateCatalog(catalog); err != nil {
		return CatalogLayer{}, false, fmt.Errorf("invalid methodology catalog %s: %w", path, err)
	}
	catalog = normalizeCatalog(catalog)

	return CatalogLayer{Source: source, Path: path, Catalog: catalog}, legacy, nil
}

func readOptionalCatalogLayer(path string, source configuration.ConfigFileSource, readFile ReadFileFunc, readDir ReadDirFunc) (CatalogLayer, bool, error) {
	layer, legacy, err := readCatalogLayerDetailed(path, source, readFile, readDir)
	if err != nil {
		if isNotExistErr(err) {
			return CatalogLayer{Source: source, Path: path}, false, nil
		}
		return CatalogLayer{}, false, err
	}
	return layer, legacy, nil
}

func readCatalogRegistries(root string, readFile ReadFileFunc, readDir ReadDirFunc) (Catalog, bool, error) {
	catalog := Catalog{}
	found := false

	routes, ok, err := readRegistryFiles[Route](root, "routes", readFile, readDir, routeRegistryKey)
	if err != nil {
		return Catalog{}, false, err
	}
	found = found || ok
	catalog.Routes = routes

	actions, ok, err := readRegistryFiles[Action](root, "actions", readFile, readDir, actionRegistryKey)
	if err != nil {
		return Catalog{}, false, err
	}
	found = found || ok
	catalog.Actions = actions

	instructions, ok, err := readRegistryFiles[Instruction](root, "instructions", readFile, readDir, instructionRegistryKey)
	if err != nil {
		return Catalog{}, false, err
	}
	found = found || ok
	catalog.Instructions = instructions

	entities, ok, err := readRegistryFiles[Entity](root, "entities", readFile, readDir, entityRegistryKey)
	if err != nil {
		return Catalog{}, false, err
	}
	found = found || ok
	catalog.Entities = entities

	operations, ok, err := readRegistryFiles[Operation](root, "operations", readFile, readDir, operationRegistryKey)
	if err != nil {
		return Catalog{}, false, err
	}
	found = found || ok
	catalog.Operations = operations

	return catalog, found, nil
}

func readRegistryFiles[T any](root string, dir string, readFile ReadFileFunc, readDir ReadDirFunc, keyFunc func(T) string) ([]T, bool, error) {
	dirPath := filepath.Join(root, dir)
	entries, err := readDir(dirPath)
	if err != nil {
		if isNotExistErr(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read methodology registry directory %s: %w", dirPath, err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	items := make([]T, 0, len(files))
	for _, file := range files {
		path := filepath.Join(dirPath, file)
		content, err := readFile(path)
		if err != nil {
			return nil, false, err
		}
		var item T
		if err := json.Unmarshal(content, &item); err != nil {
			return nil, false, fmt.Errorf("parse methodology registry file %s: %w", path, err)
		}
		key := keyFunc(item)
		fileKey := strings.TrimSuffix(file, ".json")
		if key == "" || key != fileKey {
			return nil, false, fmt.Errorf("methodology registry file %s key %q must match file name %q", path, key, fileKey)
		}
		items = append(items, item)
	}
	return items, true, nil
}

func decodeCatalog(content []byte) (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(content, &catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func loadInstructionBody(instruction Instruction, descriptionPath, methodologyRoot string, readFile ReadFileFunc) (Instruction, error) {
	instruction = normalizeInstruction(instruction)
	if instruction.BodyFile == "" {
		return instruction, nil
	}
	if instruction.Body != "" {
		return Instruction{}, fmt.Errorf("instruction %q has both body and body_file", instruction.Name)
	}
	if filepath.IsAbs(instruction.BodyFile) {
		return Instruction{}, fmt.Errorf("instruction %q body_file %q must stay inside methodology catalog", instruction.Name, instruction.BodyFile)
	}
	bodyPath := filepath.Clean(filepath.Join(filepath.Dir(descriptionPath), instruction.BodyFile))
	root := filepath.Clean(methodologyRoot)
	relative, err := filepath.Rel(root, bodyPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Instruction{}, fmt.Errorf("instruction %q body_file %q escapes methodology catalog", instruction.Name, instruction.BodyFile)
	}
	if evaluatedRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = evaluatedRoot
	} else if !os.IsNotExist(err) {
		return Instruction{}, fmt.Errorf("resolve methodology catalog root %s: %w", root, err)
	}
	if evaluatedPath, err := filepath.EvalSymlinks(bodyPath); err == nil {
		evaluatedRelative, relativeErr := filepath.Rel(root, evaluatedPath)
		if relativeErr != nil || evaluatedRelative == ".." || strings.HasPrefix(evaluatedRelative, ".."+string(filepath.Separator)) {
			return Instruction{}, fmt.Errorf("instruction %q body_file %q escapes methodology catalog", instruction.Name, instruction.BodyFile)
		}
		bodyPath = evaluatedPath
	} else if !os.IsNotExist(err) {
		return Instruction{}, fmt.Errorf("resolve instruction body file %s: %w", bodyPath, err)
	}
	content, err := readFile(bodyPath)
	if err != nil {
		return Instruction{}, fmt.Errorf("read instruction body file %s: %w", bodyPath, err)
	}
	instruction.Body = strings.TrimSpace(string(content))
	instruction.bodyLoaded = true
	relativeBodyPath, err := filepath.Rel(root, bodyPath)
	if err != nil {
		return Instruction{}, fmt.Errorf("relativize instruction body file %s: %w", bodyPath, err)
	}
	instruction.BodyFile = relativeBodyPath
	return instruction, nil
}

func validateCatalogInstructionBodyFiles(catalogPath string, catalog Catalog) error {
	methodologyRoot := filepath.Dir(catalogPath)
	lexicalRoot := filepath.Clean(methodologyRoot)
	root := lexicalRoot
	if evaluatedRoot, err := filepath.EvalSymlinks(lexicalRoot); err == nil {
		root = evaluatedRoot
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("resolve methodology catalog root %s: %w", root, err)
	}

	for _, rawInstruction := range catalog.Instructions {
		instruction := normalizeInstruction(rawInstruction)
		if instruction.BodyFile == "" {
			continue
		}
		if filepath.IsAbs(instruction.BodyFile) {
			return fmt.Errorf("instruction %q body_file %q must stay inside methodology catalog", instruction.Name, instruction.BodyFile)
		}
		bodyPath := filepath.Clean(filepath.Join(methodologyRoot, instruction.BodyFile))
		if err := ensurePathInsideRoot(lexicalRoot, bodyPath); err != nil {
			return fmt.Errorf("instruction %q body_file %q: %w", instruction.Name, instruction.BodyFile, err)
		}
		if evaluatedPath, err := filepath.EvalSymlinks(bodyPath); err == nil {
			if err := ensurePathInsideRoot(root, evaluatedPath); err != nil {
				return fmt.Errorf("instruction %q body_file %q: %w", instruction.Name, instruction.BodyFile, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("resolve instruction body file %s: %w", bodyPath, err)
		}
	}
	return nil
}

func ensurePathInsideRoot(root, path string) error {
	relative, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("escapes methodology catalog")
	}
	return nil
}

func writeCatalog(path string, catalog Catalog, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) error {
	content, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal methodology catalog %s: %w", path, err)
	}
	content = append(content, '\n')

	if err := mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create methodology catalog directory %s: %w", filepath.Dir(path), err)
	}
	if err := writeFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write methodology catalog %s: %w", path, err)
	}
	return nil
}

func writeCatalogFiles(path string, catalog Catalog, writeFile WriteFileFunc, mkdirAll MkdirAllFunc, removeAll RemoveAllFunc) error {
	root := filepath.Dir(path)
	for _, dir := range []string{"routes", "actions", "instructions", "operations", "entities"} {
		dirPath := filepath.Join(root, dir)
		if err := removeAll(dirPath); err != nil {
			return fmt.Errorf("remove methodology registry directory %s: %w", dirPath, err)
		}
	}
	if err := writeCatalogConfig(path, catalog, writeFile, mkdirAll); err != nil {
		return err
	}
	for _, route := range catalog.Routes {
		path, err := registryFilePath(root, "routes", routeRegistryKey(route))
		if err != nil {
			return err
		}
		if err := writeRegistryObject(path, route, writeFile, mkdirAll); err != nil {
			return err
		}
	}
	for _, action := range catalog.Actions {
		path, err := registryFilePath(root, "actions", actionRegistryKey(action))
		if err != nil {
			return err
		}
		if err := writeRegistryObject(path, action, writeFile, mkdirAll); err != nil {
			return err
		}
	}
	for _, instruction := range catalog.Instructions {
		if instruction.BodyFile != "" {
			bodyPath := filepath.Join(root, instruction.BodyFile)
			relativeBodyPath, err := filepath.Rel(filepath.Join(root, "instructions"), bodyPath)
			if err != nil {
				return fmt.Errorf("relativize instruction body file %q: %w", instruction.BodyFile, err)
			}
			instruction.BodyFile = relativeBodyPath
			instruction.Body = ""
		}
		path, err := registryFilePath(root, "instructions", instructionRegistryKey(instruction))
		if err != nil {
			return err
		}
		if err := writeRegistryObject(path, instruction, writeFile, mkdirAll); err != nil {
			return err
		}
	}
	for _, entity := range catalog.Entities {
		path, err := registryFilePath(root, "entities", entityRegistryKey(entity))
		if err != nil {
			return err
		}
		if err := writeRegistryObject(path, entity, writeFile, mkdirAll); err != nil {
			return err
		}
	}
	for _, operation := range catalog.Operations {
		path, err := registryFilePath(root, "operations", operationRegistryKey(operation))
		if err != nil {
			return err
		}
		if err := writeRegistryObject(path, operation, writeFile, mkdirAll); err != nil {
			return err
		}
	}
	return nil
}

func writeCatalogConfig(path string, catalog Catalog, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) error {
	return writeCatalog(path, Catalog{DefaultRoute: catalog.DefaultRoute}, writeFile, mkdirAll)
}

func writeCatalogElement(path string, element ElementUpsert, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) error {
	root := filepath.Dir(path)
	switch {
	case element.Route != nil:
		route := normalizeRoute(*element.Route)
		path, err := registryFilePath(root, "routes", routeRegistryKey(route))
		if err != nil {
			return err
		}
		return writeRegistryObject(path, route, writeFile, mkdirAll)
	case element.Action != nil:
		action := normalizeAction(*element.Action)
		path, err := registryFilePath(root, "actions", actionRegistryKey(action))
		if err != nil {
			return err
		}
		return writeRegistryObject(path, action, writeFile, mkdirAll)
	case element.Instruction != nil:
		instruction := normalizeInstruction(*element.Instruction)
		if err := validateCatalog(Catalog{Instructions: []Instruction{instruction}}); err != nil {
			return err
		}
		if instruction.BodyFile != "" {
			instruction.Body = ""
		}
		path, err := registryFilePath(root, "instructions", instructionRegistryKey(instruction))
		if err != nil {
			return err
		}
		return writeRegistryObject(path, instruction, writeFile, mkdirAll)
	case element.Entity != nil:
		entity := normalizeEntity(*element.Entity)
		path, err := registryFilePath(root, "entities", entityRegistryKey(entity))
		if err != nil {
			return err
		}
		return writeRegistryObject(path, entity, writeFile, mkdirAll)
	default:
		return fmt.Errorf("должна быть задана ровно одна сущность методики")
	}
}

func registryFilePath(root string, dir string, key string) (string, error) {
	if key == "" || key == "." || key == ".." || strings.ContainsAny(key, `/\`) {
		return "", fmt.Errorf("methodology registry key %q is not safe for file name", key)
	}
	return filepath.Join(root, dir, key+".json"), nil
}

func writeRegistryObject(path string, value any, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) error {
	if err := mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create methodology registry directory %s: %w", filepath.Dir(path), err)
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal methodology registry file %s: %w", path, err)
	}
	content = append(content, '\n')
	if err := writeFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write methodology registry file %s: %w", path, err)
	}
	return nil
}

func applyElementUpsert(catalog Catalog, element ElementUpsert) (Catalog, error) {
	selected := 0
	if element.Route != nil {
		selected++
	}
	if element.Action != nil {
		selected++
	}
	if element.Instruction != nil {
		selected++
	}
	if element.Entity != nil {
		selected++
	}
	if selected != 1 {
		return Catalog{}, fmt.Errorf("должна быть задана ровно одна сущность методики")
	}

	catalog = normalizeCatalog(catalog)
	routeIndexes := indexRoutes(catalog.Routes)
	actionIndexes := indexActions(catalog.Actions)
	instructionIndexes := indexInstructions(catalog.Instructions)
	entityIndexes := indexEntities(catalog.Entities)

	if element.Route != nil {
		route := normalizeRoute(*element.Route)
		if err := validateCatalog(Catalog{Routes: []Route{route}}); err != nil {
			return Catalog{}, err
		}
		upsertRoute(&catalog, routeIndexes, route)
	}
	if element.Action != nil {
		action := *element.Action
		if err := validateCatalog(Catalog{Actions: []Action{action}}); err != nil {
			return Catalog{}, err
		}
		action = normalizeAction(action)
		upsertAction(&catalog, actionIndexes, action)
	}
	if element.Instruction != nil {
		instruction := normalizeInstruction(*element.Instruction)
		if err := validateCatalog(Catalog{Instructions: []Instruction{instruction}}); err != nil {
			return Catalog{}, err
		}
		upsertInstruction(&catalog, instructionIndexes, instruction)
	}
	if element.Entity != nil {
		entity := normalizeEntity(*element.Entity)
		if err := validateCatalog(Catalog{Entities: []Entity{entity}}); err != nil {
			return Catalog{}, err
		}
		upsertEntity(&catalog, entityIndexes, entity)
	}

	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func indexRoutes(routes []Route) map[string]int {
	indexes := map[string]int{}
	for index, route := range routes {
		indexes[route.Name] = index
	}
	return indexes
}

func indexActions(actions []Action) map[string]int {
	indexes := map[string]int{}
	for index, action := range actions {
		indexes[action.Name] = index
	}
	return indexes
}

func indexInstructions(instructions []Instruction) map[string]int {
	indexes := map[string]int{}
	for index, instruction := range instructions {
		indexes[instruction.Name] = index
	}
	return indexes
}

func indexEntities(entities []Entity) map[string]int {
	indexes := map[string]int{}
	for index, entity := range entities {
		indexes[entitySourceKey(entity.Kind, entity.Name)] = index
	}
	return indexes
}

func catalogHasObjects(catalog Catalog) bool {
	return len(catalog.Routes) > 0 || len(catalog.Actions) > 0 || len(catalog.Operations) > 0 || len(catalog.Instructions) > 0 || len(catalog.Entities) > 0
}

func routeRegistryKey(route Route) string {
	return normalizeName(route.Name)
}

func actionRegistryKey(action Action) string {
	return normalizeName(action.Name)
}

func instructionRegistryKey(instruction Instruction) string {
	return normalizeName(instruction.Name)
}

func operationRegistryKey(operation Operation) string {
	return operationSourceKey(operation.Name)
}

func entityRegistryKey(entity Entity) string {
	kind := normalizeKind(entity.Kind)
	name := normalizeName(entity.Name)
	if kind == "" || name == "" {
		return ""
	}
	return kind + "--" + name
}

func catalogPathForScope(repoRoot, configHome string, scope configuration.ConfigFileSource) (string, error) {
	switch scope {
	case configuration.ConfigFileSourceGlobal:
		home, err := resolveConfigHome(configHome)
		if err != nil {
			return "", err
		}
		return filepath.Join(home, catalogGlobalFilePath), nil
	case configuration.ConfigFileSourceLocal:
		repoRoot = strings.TrimSpace(repoRoot)
		if repoRoot == "" {
			return "", fmt.Errorf("repo root is required for local methodology catalog")
		}
		return filepath.Join(repoRoot, catalogLocalFilePath), nil
	default:
		return "", fmt.Errorf("unknown methodology catalog scope %q", scope)
	}
}

func isNotExistErr(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func resolveConfigHome(configHome string) (string, error) {
	if strings.TrimSpace(configHome) != "" {
		return configHome, nil
	}

	envHome := strings.TrimSpace(os.Getenv(configHomeEnvVar))
	if envHome != "" {
		return envHome, nil
	}

	userHome, err := resolveUserHome()
	if err != nil {
		return "", fmt.Errorf("resolve current user home: %w", err)
	}
	return filepath.Join(userHome, configDefaultHome), nil
}

func resolveGitRepoRoot(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
