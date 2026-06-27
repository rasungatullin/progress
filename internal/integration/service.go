package integration

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	githubprovider "github.com/rasungatullin/progress/internal/integration/github"
	"github.com/rasungatullin/progress/internal/integration/model"
)

type Request = model.Request
type Response = model.Response
type Route = model.Route
type TrackerIssue = model.TrackerIssue
type TrackerPullRequest = model.TrackerPullRequest
type TrackerComment = model.TrackerComment
type TrackerReview = model.TrackerReview
type TrackerRepository = model.TrackerRepository
type TrackerUser = model.TrackerUser
type TrackerSearchResult = model.TrackerSearchResult
type Artifact = model.Artifact
type ProviderRequest = model.ProviderRequest
type AuthStatus = model.AuthStatus
type RepositoryStatus = model.RepositoryStatus
type IssueStatus = model.IssueStatus
type PullRequestStatus = model.PullRequestStatus

type Provider interface {
	Execute(context.Context, ProviderRequest) (Response, error)
}

type Service struct {
	logger        *log.Logger
	providers     map[string]Provider
	defaultSystem string
	systems       map[string]systemState
}

type systemState struct {
	Name       string
	Type       string
	Configured bool
	Enabled    bool
	Registered bool
}

func NewService(logger *log.Logger) *Service {
	service := newEmptyService(logger)
	service.registerConfiguredProvider("github", systemState{Name: "github", Type: "github", Configured: true, Enabled: true}, githubprovider.NewService())
	return service
}

func NewServiceFromConfig(logger *log.Logger, config model.IntegrationConfigFile) *Service {
	service := newEmptyService(logger)
	service.defaultSystem = normalizeSystem(config.DefaultSystem)

	for name, systemConfig := range config.Systems {
		name = normalizeSystem(name)
		if name == "" {
			continue
		}

		state := systemState{
			Name:       name,
			Type:       normalizeSystem(systemConfig.Type),
			Configured: true,
			Enabled:    systemEnabled(systemConfig),
		}
		service.systems[name] = state

		if !state.Enabled {
			continue
		}

		switch state.Type {
		case "github":
			service.registerConfiguredProvider(name, state, githubprovider.NewServiceWithConfig(systemConfig))
		case "":
			service.systems[name] = state
		default:
			service.systems[name] = state
		}
	}

	return service
}

func newEmptyService(logger *log.Logger) *Service {
	service := &Service{
		logger:    logger,
		providers: make(map[string]Provider),
		systems:   make(map[string]systemState),
	}
	return service
}

func (s *Service) RegisterProvider(system string, provider Provider) {
	s.registerConfiguredProvider(system, systemState{Name: normalizeSystem(system), Type: normalizeSystem(system), Configured: true, Enabled: true}, provider)
}

func (s *Service) registerConfiguredProvider(system string, state systemState, provider Provider) {
	name := strings.TrimSpace(strings.ToLower(system))
	if name == "" || provider == nil {
		return
	}

	s.providers[name] = provider
	state.Name = name
	state.Registered = true
	state.Enabled = true
	state.Configured = true
	if state.Type == "" {
		state.Type = name
	}
	s.systems[name] = state
}

func (s *Service) Dispatch(_ context.Context, req Request) (Route, error) {
	normalized, err := s.normalizeRequest(req)
	if err != nil {
		resolvedSystem := normalizeSystem(req.System)
		if resolvedSystem == "" {
			resolvedSystem = s.defaultSystem
		}
		route := Route{
			System:         resolvedSystem,
			Provider:       resolvedSystem,
			Resource:       normalizeResource(req.Resource),
			Operation:      normalizeOperation(req.Operation),
			ExpectedResult: expectedResult(normalizeResource(req.Resource), normalizeOperation(req.Operation)),
			Diagnostics: []string{
				fmt.Sprintf("request system=%s resource=%s operation=%s", resolvedSystem, normalizeResource(req.Resource), normalizeOperation(req.Operation)),
				"dispatcher mode=diagnostic-only",
				"invalid-request missing system",
			},
		}

		s.logger.Printf("Диспетчер интеграции отклонил запрос: система=%q причина=%q", req.System, err)
		return route, err
	}

	_, ok := s.providers[normalized.System]
	state, known := s.systems[normalized.System]

	route := Route{
		System:            normalized.System,
		Provider:          normalized.System,
		ProviderAvailable: ok,
		Resource:          normalized.Resource,
		Operation:         normalized.Operation,
		ExpectedResult:    expectedResult(normalized.Resource, normalized.Operation),
		Diagnostics:       buildDiagnostics(normalized.System, normalized.Resource, normalized.Operation, ok, known, state, s.registeredSystems()),
	}
	s.logger.Printf("Диспетчер интеграции сформировал маршрут: система=%q ресурс=%q операция=%q провайдер=%q доступен=%t", route.System, route.Resource, route.Operation, route.Provider, route.ProviderAvailable)
	return route, nil
}

func (s *Service) Execute(ctx context.Context, req Request) (Response, error) {
	normalized, err := s.normalizeRequest(req)
	if err != nil {
		resolvedSystem := normalizeSystem(req.System)
		if resolvedSystem == "" {
			resolvedSystem = s.defaultSystem
		}
		return Response{
			Resource:  normalizeResource(req.Resource),
			Operation: normalizeOperation(req.Operation),
			Route: Route{
				System:         resolvedSystem,
				Provider:       resolvedSystem,
				Resource:       normalizeResource(req.Resource),
				Operation:      normalizeOperation(req.Operation),
				ExpectedResult: expectedResult(normalizeResource(req.Resource), normalizeOperation(req.Operation)),
				Diagnostics: []string{
					fmt.Sprintf("request system=%s resource=%s operation=%s", resolvedSystem, normalizeResource(req.Resource), normalizeOperation(req.Operation)),
					"dispatcher mode=diagnostic-only",
					"invalid-request missing system",
				},
			},
		}, err
	}

	route, err := s.Dispatch(ctx, req)
	if err != nil {
		return Response{}, err
	}

	normalized.Route = route
	provider, ok := s.providers[route.System]
	if !ok {
		if state, known := s.systems[route.System]; known {
			switch {
			case !state.Enabled:
				return Response{System: route.System, Resource: route.Resource, Operation: route.Operation, Route: route}, fmt.Errorf("integration provider disabled by configuration: %s", route.System)
			case state.Type != "" && state.Type != route.System:
				return Response{System: route.System, Resource: route.Resource, Operation: route.Operation, Route: route}, fmt.Errorf("integration provider type is not supported in current build: %s (%s)", route.System, state.Type)
			}
		}
		return Response{System: route.System, Resource: route.Resource, Operation: route.Operation, Route: route}, fmt.Errorf("integration provider not registered: %s", route.System)
	}

	result, err := provider.Execute(ctx, normalized)
	result.System = firstNonEmpty(result.System, route.System)
	result.Resource = firstNonEmpty(result.Resource, route.Resource)
	result.Operation = firstNonEmpty(result.Operation, route.Operation)
	result.Route = route
	if err != nil {
		return result, err
	}

	return result, nil
}

func (s *Service) normalizeRequest(req Request) (ProviderRequest, error) {
	normalized := ProviderRequest{
		System:       normalizeSystem(firstNonEmpty(req.System, s.defaultSystem)),
		Resource:     normalizeResource(req.Resource),
		Operation:    normalizeOperation(req.Operation),
		Repository:   strings.TrimSpace(req.Repository),
		RepoProvided: req.RepoProvided,
		Number:       req.Number,
		Base:         strings.TrimSpace(req.Base),
		Head:         strings.TrimSpace(req.Head),
		Title:        strings.TrimSpace(req.Title),
		Body:         strings.TrimSpace(req.Body),
		Draft:        req.Draft,
		Query:        strings.TrimSpace(req.Query),
		Limit:        req.Limit,
	}

	if normalized.System == "" {
		return ProviderRequest{}, fmt.Errorf("invalid integration request: system is required")
	}

	return normalized, nil
}

func (s *Service) registeredSystems() []string {
	if len(s.providers) == 0 {
		return nil
	}

	items := make([]string, 0, len(s.providers))
	for system := range s.providers {
		items = append(items, system)
	}

	sort.Strings(items)
	return items
}

func normalizeSystem(system string) string {
	system = strings.TrimSpace(strings.ToLower(system))
	return system
}

func normalizeResource(resource string) string {
	resource = strings.TrimSpace(strings.ToLower(resource))
	if resource == "" {
		return "tracker-object"
	}

	return resource
}

func normalizeOperation(operation string) string {
	operation = strings.TrimSpace(strings.ToLower(operation))
	if operation == "" {
		return "get"
	}

	return operation
}

func expectedResult(resource string, operation string) string {
	switch resource {
	case "issue":
		if operation == "comments" {
			return "tracker-comment[]"
		}
		if operation == "search" {
			return "tracker-search-result[]"
		}
		return "tracker-issue"
	case "pull-request", "pr":
		if operation == "search" {
			return "tracker-search-result[]"
		}
		if operation == "create" {
			return "integration-pull-request-status"
		}
		return "tracker-pull-request"
	case "comment":
		return "tracker-comment[]"
	case "review":
		return "tracker-review[]"
	case "auth":
		if operation == "status" {
			return "integration-auth-status"
		}
		return "normalized-response"
	case "repository", "repo":
		return "tracker-repository"
	default:
		return "normalized-response"
	}
}

func buildDiagnostics(system string, resource string, operation string, available bool, known bool, state systemState, registered []string) []string {
	diagnostics := []string{
		fmt.Sprintf("request system=%s resource=%s operation=%s", system, resource, operation),
		"dispatcher mode=diagnostic-only",
	}

	if available {
		diagnostics = append(diagnostics, fmt.Sprintf("provider=%s registered", system))
		return diagnostics
	}

	if known {
		switch {
		case !state.Enabled:
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s disabled by integration configuration", system))
		case state.Type != "" && state.Type != system:
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s configured with unsupported type=%s", system, state.Type))
		default:
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s not registered in current build", system))
		}
	} else {
		diagnostics = append(diagnostics, fmt.Sprintf("provider=%s unknown to current integration configuration", system))
	}

	if len(registered) == 0 {
		diagnostics = append(diagnostics, "registered systems=<none>")
		return diagnostics
	}

	diagnostics = append(diagnostics, fmt.Sprintf("registered systems=%s", strings.Join(registered, ",")))
	return diagnostics
}

func systemEnabled(config model.IntegrationSystemConfig) bool {
	return config.Enabled == nil || *config.Enabled
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
