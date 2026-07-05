package methodology

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
		if layer, err := readCatalogLayer(globalPath, configuration.ConfigFileSourceGlobal, readFile); err == nil {
			layers = append(layers, layer)
		} else if !isNotExistErr(err) {
			return CatalogSnapshot{}, err
		}
	}

	if useLocalLayer {
		if layer, err := readCatalogLayer(localPath, configuration.ConfigFileSourceLocal, readFile); err == nil {
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
	if readFile == nil {
		readFile = os.ReadFile
	}
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
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
	catalog = normalizeCatalog(catalog)
	if err := writeCatalog(path, catalog, writeFile, mkdirAll); err != nil {
		return CatalogWriteResult{}, err
	}

	snapshot, err := LoadCatalogWithHome(repoRoot, configHome, readFile)
	if err != nil {
		return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog}, nil
	}
	return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog, Snapshot: snapshot}, nil
}

func UpsertCatalogElementWithHome(repoRoot, configHome string, scope configuration.ConfigFileSource, element ElementUpsert, readFile ReadFileFunc, writeFile WriteFileFunc, mkdirAll MkdirAllFunc) (CatalogWriteResult, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}
	if scope == "" {
		scope = CatalogWriteScopeLocal
	}

	path, err := catalogPathForScope(repoRoot, configHome, scope)
	if err != nil {
		return CatalogWriteResult{}, err
	}

	catalog, err := readOptionalCatalog(path, readFile)
	if err != nil {
		return CatalogWriteResult{}, err
	}
	catalog, err = applyElementUpsert(catalog, element)
	if err != nil {
		return CatalogWriteResult{}, err
	}
	if err := writeCatalog(path, catalog, writeFile, mkdirAll); err != nil {
		return CatalogWriteResult{}, err
	}

	snapshot, err := LoadCatalogWithHome(repoRoot, configHome, readFile)
	if err != nil {
		return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog}, nil
	}
	return CatalogWriteResult{Scope: scope, Path: path, Catalog: catalog, Snapshot: snapshot}, nil
}

func readCatalogLayer(path string, source configuration.ConfigFileSource, readFile ReadFileFunc) (CatalogLayer, error) {
	content, err := readFile(path)
	if err != nil {
		return CatalogLayer{}, err
	}

	catalog, err := decodeCatalog(content)
	if err != nil {
		return CatalogLayer{}, fmt.Errorf("parse methodology catalog %s: %w", path, err)
	}
	if err := validateCatalog(catalog); err != nil {
		return CatalogLayer{}, fmt.Errorf("invalid methodology catalog %s: %w", path, err)
	}
	catalog = normalizeCatalog(catalog)

	return CatalogLayer{Source: source, Path: path, Catalog: catalog}, nil
}

func readOptionalCatalog(path string, readFile ReadFileFunc) (Catalog, error) {
	content, err := readFile(path)
	if err != nil {
		if isNotExistErr(err) {
			return Catalog{}, nil
		}
		return Catalog{}, err
	}

	catalog, err := decodeCatalog(content)
	if err != nil {
		return Catalog{}, fmt.Errorf("parse methodology catalog %s: %w", path, err)
	}
	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, fmt.Errorf("invalid methodology catalog %s: %w", path, err)
	}
	return normalizeCatalog(catalog), nil
}

func decodeCatalog(content []byte) (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(content, &catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
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
