package integration

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

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

type Provider interface {
	Execute(context.Context, Request) (Response, error)
}

type Service struct {
	logger    *log.Logger
	providers map[string]Provider
}

func NewService(logger *log.Logger) *Service {
	return &Service{
		logger:    logger,
		providers: make(map[string]Provider),
	}
}

func (s *Service) RegisterProvider(system string, provider Provider) {
	name := normalizeSystem(system)
	if name == "" || provider == nil {
		return
	}

	s.providers[name] = provider
}

func (s *Service) Dispatch(_ context.Context, req Request) Route {
	system := normalizeSystem(req.System)
	resource := normalizeResource(req.Resource)
	operation := normalizeOperation(req.Operation)
	provider, ok := s.providers[system]

	route := Route{
		System:            system,
		Provider:          system,
		ProviderAvailable: ok,
		Resource:          resource,
		Operation:         operation,
		ExpectedResult:    expectedResult(resource, operation),
		Diagnostics:       buildDiagnostics(system, resource, operation, ok, s.registeredSystems()),
	}

	if ok {
		_ = provider
	}

	s.logger.Printf("Диспетчер интеграции сформировал маршрут: система=%q ресурс=%q операция=%q провайдер=%q доступен=%t", route.System, route.Resource, route.Operation, route.Provider, route.ProviderAvailable)
	return route
}

func (s *Service) Execute(ctx context.Context, req Request) (Response, error) {
	route := s.Dispatch(ctx, req)
	provider, ok := s.providers[route.System]
	if !ok {
		return Response{System: route.System, Resource: route.Resource, Operation: route.Operation, Route: route}, fmt.Errorf("integration provider not registered: %s", route.System)
	}

	result, err := provider.Execute(ctx, req)
	if err != nil {
		return Response{}, err
	}

	result.System = firstNonEmpty(result.System, route.System)
	result.Resource = firstNonEmpty(result.Resource, route.Resource)
	result.Operation = firstNonEmpty(result.Operation, route.Operation)
	result.Route = route
	return result, nil
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
	if system == "" {
		return "unknown"
	}

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
		if operation == "search" {
			return "tracker-search-result[]"
		}
		return "tracker-issue"
	case "pull-request", "pr":
		if operation == "search" {
			return "tracker-search-result[]"
		}
		return "tracker-pull-request"
	case "comment":
		return "tracker-comment[]"
	case "review":
		return "tracker-review[]"
	case "repository", "repo":
		return "tracker-repository"
	default:
		return "normalized-response"
	}
}

func buildDiagnostics(system string, resource string, operation string, available bool, registered []string) []string {
	diagnostics := []string{
		fmt.Sprintf("request system=%s resource=%s operation=%s", system, resource, operation),
		"dispatcher mode=diagnostic-only",
	}

	if available {
		diagnostics = append(diagnostics, fmt.Sprintf("provider=%s registered", system))
		return diagnostics
	}

	diagnostics = append(diagnostics, fmt.Sprintf("provider=%s not registered in current build", system))
	if len(registered) == 0 {
		diagnostics = append(diagnostics, "registered systems=<none>")
		return diagnostics
	}

	diagnostics = append(diagnostics, fmt.Sprintf("registered systems=%s", strings.Join(registered, ",")))
	return diagnostics
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
