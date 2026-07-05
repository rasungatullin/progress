package methodology

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
)

type SelectionRequest struct {
	RepoRoot   string `json:"repo_root,omitempty"`
	ConfigHome string `json:"config_home,omitempty"`
	Route      string `json:"route,omitempty"`
	Action     string `json:"action,omitempty"`
	Profile    string `json:"profile,omitempty"`
}

type SelectionResult struct {
	Route             Route                          `json:"route"`
	Action            Action                         `json:"action"`
	Profile           string                         `json:"profile,omitempty"`
	Instruction       Instruction                    `json:"instruction,omitempty"`
	Diagnostics       []string                       `json:"diagnostics,omitempty"`
	RouteSource       configuration.ConfigFileSource `json:"route_source,omitempty"`
	ActionSource      configuration.ConfigFileSource `json:"action_source,omitempty"`
	InstructionSource configuration.ConfigFileSource `json:"instruction_source,omitempty"`
	GlobalCatalogPath string                         `json:"global_catalog_path,omitempty"`
	LocalCatalogPath  string                         `json:"local_catalog_path,omitempty"`
}

type Service struct {
	logger          *log.Logger
	readFile        ReadFileFunc
	writeFile       WriteFileFunc
	mkdirAll        MkdirAllFunc
	resolveRepoRoot func(context.Context) (string, error)
}

func NewService(logger *log.Logger) *Service {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	return &Service{
		logger:          logger,
		readFile:        os.ReadFile,
		writeFile:       os.WriteFile,
		mkdirAll:        os.MkdirAll,
		resolveRepoRoot: resolveGitRepoRoot,
	}
}

func (s *Service) Load(ctx context.Context, request CatalogRequest) (CatalogSnapshot, error) {
	if s == nil {
		s = NewService(nil)
	}

	repoRoot := strings.TrimSpace(request.RepoRoot)
	if repoRoot == "" && s.resolveRepoRoot != nil {
		if resolved, err := s.resolveRepoRoot(ctx); err == nil {
			repoRoot = strings.TrimSpace(resolved)
		}
	}

	return LoadCatalogWithHome(repoRoot, request.ConfigHome, s.readFile)
}

func (s *Service) List(ctx context.Context, request ElementRequest) ([]ListedElement, error) {
	snapshot, err := s.Load(ctx, CatalogRequest{RepoRoot: request.RepoRoot, ConfigHome: request.ConfigHome})
	if err != nil {
		return nil, err
	}

	return ListCatalogElements(snapshot, ElementFilter{
		Kind:          request.Kind,
		EntityKind:    request.EntityKind,
		TargetContour: request.TargetContour,
	}), nil
}

func (s *Service) Get(ctx context.Context, request ElementRequest) (ListedElement, error) {
	snapshot, err := s.Load(ctx, CatalogRequest{RepoRoot: request.RepoRoot, ConfigHome: request.ConfigHome})
	if err != nil {
		return ListedElement{}, err
	}

	return GetCatalogElement(snapshot, request.Kind, request.Name, request.EntityKind)
}

func (s *Service) Save(ctx context.Context, request CatalogWriteRequest) (CatalogWriteResult, error) {
	if s == nil {
		s = NewService(nil)
	}
	if request.Catalog == nil {
		return CatalogWriteResult{}, fmt.Errorf("каталог методик должен быть задан")
	}

	repoRoot, err := s.repoRootForWrite(ctx, request.RepoRoot, request.Scope)
	if err != nil {
		return CatalogWriteResult{}, err
	}

	return SaveCatalogWithHome(repoRoot, request.ConfigHome, request.Scope, *request.Catalog, s.readFile, s.writeFile, s.mkdirAll)
}

func (s *Service) Upsert(ctx context.Context, request CatalogWriteRequest) (CatalogWriteResult, error) {
	if s == nil {
		s = NewService(nil)
	}

	repoRoot, err := s.repoRootForWrite(ctx, request.RepoRoot, request.Scope)
	if err != nil {
		return CatalogWriteResult{}, err
	}

	return UpsertCatalogElementWithHome(repoRoot, request.ConfigHome, request.Scope, request.Element, s.readFile, s.writeFile, s.mkdirAll)
}

func (s *Service) Resolve(ctx context.Context, request SelectionRequest) (SelectionResult, error) {
	snapshot, err := s.Load(ctx, CatalogRequest{RepoRoot: request.RepoRoot, ConfigHome: request.ConfigHome})
	if err != nil {
		return SelectionResult{}, err
	}

	result, err := s.Select(ctx, snapshot.Catalog, request)
	if err != nil {
		return SelectionResult{}, err
	}
	result.RouteSource = snapshot.Sources.Routes[result.Route.Name]
	result.ActionSource = snapshot.Sources.Actions[result.Action.Name]
	if result.Instruction.Name != "" {
		result.InstructionSource = snapshot.Sources.Instructions[result.Instruction.Name]
	}
	result.GlobalCatalogPath = snapshot.GlobalCatalogPath
	result.LocalCatalogPath = snapshot.LocalCatalogPath
	if result.RouteSource != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("route-source=%s", result.RouteSource))
	}
	if result.ActionSource != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("action-source=%s", result.ActionSource))
	}
	if result.InstructionSource != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("instruction-source=%s", result.InstructionSource))
	}
	return result, nil
}

func (s *Service) Select(ctx context.Context, catalog Catalog, request SelectionRequest) (SelectionResult, error) {
	_ = ctx

	if s == nil {
		s = NewService(nil)
	}

	catalog = normalizeCatalog(catalog)
	route, err := selectRoute(catalog.Routes, request.Route)
	if err != nil {
		return SelectionResult{}, err
	}

	actionName := firstNonEmpty(request.Action, route.Action)
	action, err := selectAction(catalog.Actions, actionName)
	if err != nil {
		return SelectionResult{}, err
	}

	profile := firstNonEmpty(request.Profile, action.Profile, route.Profile)
	instruction := selectInstruction(catalog.Instructions, action.Name, profile)
	result := SelectionResult{
		Route:       route,
		Action:      action,
		Profile:     profile,
		Instruction: instruction,
		Diagnostics: []string{
			fmt.Sprintf("route=%s", route.Name),
			fmt.Sprintf("action=%s", action.Name),
			fmt.Sprintf("profile=%s", profile),
		},
	}
	if instruction.Name != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("instruction=%s", instruction.Name))
	} else if profile != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("instruction-missing-for-profile=%s", profile))
	}

	s.logger.Printf("Контур методик выбрал маршрут: маршрут=%q действие=%q профиль=%q", route.Name, action.Name, profile)
	return result, nil
}

func (s *Service) repoRootForWrite(ctx context.Context, repoRoot string, scope configuration.ConfigFileSource) (string, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if scope == "" {
		scope = CatalogWriteScopeLocal
	}
	if scope != CatalogWriteScopeLocal || repoRoot != "" {
		return repoRoot, nil
	}
	if s.resolveRepoRoot == nil {
		return "", fmt.Errorf("repo root is required for local methodology catalog")
	}

	resolved, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve git repository root for methodology catalog: %w", err)
	}
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return "", fmt.Errorf("repo root is required for local methodology catalog")
	}
	return resolved, nil
}

func selectRoute(routes []Route, name string) (Route, error) {
	name = normalizeName(name)
	for _, route := range routes {
		route = normalizeRoute(route)
		if route.Name == "" {
			continue
		}
		if name == "" || route.Name == name {
			return route, nil
		}
	}
	if name == "" {
		return Route{}, fmt.Errorf("каталог методик не содержит маршруты")
	}
	return Route{}, fmt.Errorf("маршрут методики %q не найден", name)
}

func selectAction(actions []Action, name string) (Action, error) {
	name = normalizeName(name)
	if name == "" {
		return Action{}, fmt.Errorf("действие методики должно быть задано")
	}
	for _, action := range actions {
		action = normalizeAction(action)
		if action.Name == name {
			return action, nil
		}
	}
	return Action{}, fmt.Errorf("действие методики %q не найдено", name)
}

func selectInstruction(instructions []Instruction, actionName string, profile string) Instruction {
	actionName = normalizeName(actionName)
	profile = strings.TrimSpace(profile)

	for _, instruction := range instructions {
		instruction = normalizeInstruction(instruction)
		if instruction.Action == actionName && instruction.Profile == profile {
			return instruction
		}
	}
	for _, instruction := range instructions {
		instruction = normalizeInstruction(instruction)
		if instruction.Action == "" && instruction.Profile == profile {
			return instruction
		}
	}
	return Instruction{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
