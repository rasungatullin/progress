package integration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	bitbucketprovider "github.com/rasungatullin/progress/internal/integration/bitbucket"
	confluenceprovider "github.com/rasungatullin/progress/internal/integration/confluence"
	githubprovider "github.com/rasungatullin/progress/internal/integration/github"
	localtrackerprovider "github.com/rasungatullin/progress/internal/integration/localtracker"
	mattermostprovider "github.com/rasungatullin/progress/internal/integration/mattermost"
	"github.com/rasungatullin/progress/internal/integration/model"
	scriptprovider "github.com/rasungatullin/progress/internal/integration/script"
	"github.com/rasungatullin/progress/internal/integration/secrets"
	telegramprovider "github.com/rasungatullin/progress/internal/integration/telegram"
)

type Request = model.Request
type Response = model.Response
type Route = model.Route
type Failure = model.Failure
type CanonicalTask = model.CanonicalTask
type TaskComment = model.TaskComment
type Repository = model.Repository
type MergeRequest = model.MergeRequest
type ReviewRemark = model.ReviewRemark
type MessageThread = model.MessageThread
type Message = model.Message
type MessageReaction = model.MessageReaction
type WikiPage = model.WikiPage
type OperationResult = model.OperationResult
type User = model.User
type ObjectLink = model.ObjectLink
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
type OperationFilter = model.OperationFilter
type OperationDescriptor = model.OperationDescriptor
type OperationInputContract = model.OperationInputContract
type OperationOutputContract = model.OperationOutputContract
type OperationField = model.OperationField

type Provider interface {
	Execute(context.Context, ProviderRequest) (Response, error)
}

type Service struct {
	logger         *log.Logger
	providers      map[string]Provider
	defaultSystem  string
	defaultSystems map[string]string
	systems        map[string]systemState
}

type systemState struct {
	Name             string
	Type             string
	IntegrationTypes []string
	Configured       bool
	Enabled          bool
	Registered       bool
	Default          bool
	Transport        string
	TaskLabelMapping map[string]string
	Operations       map[string]model.IntegrationOperationConfig
}

func NewService(logger *log.Logger) *Service {
	service := newEmptyService(logger)
	service.defaultSystems[model.IntegrationTypeIssue] = "github"
	service.defaultSystems[model.IntegrationTypeRepo] = "github"
	service.registerConfiguredProvider("github", systemState{
		Name:             "github",
		Type:             "github",
		IntegrationTypes: []string{model.IntegrationTypeIssue, model.IntegrationTypeRepo},
		Configured:       true,
		Enabled:          true,
		Default:          true,
	}, githubprovider.NewService())
	return service
}

func NewServiceFromConfig(logger *log.Logger, config model.IntegrationConfigFile) *Service {
	return NewServiceFromConfigWithPrivateStore(logger, config, nil)
}

func NewServiceFromConfigWithPrivateStore(logger *log.Logger, config model.IntegrationConfigFile, store secrets.Store) *Service {
	service := newEmptyService(logger)
	service.defaultSystem = normalizeSystem(config.DefaultSystem)
	for integrationType, system := range config.DefaultSystems {
		legacyType := strings.TrimSpace(strings.ToLower(integrationType))
		if legacyType == "tracker" || legacyType == "repository" {
			service.logger.Printf("Предупреждение совместимости: default_systems.%s устарел; используйте default_systems.%s", legacyType, normalizeIntegrationType(legacyType))
		}
		integrationType = normalizeIntegrationType(integrationType)
		system = normalizeSystem(system)
		if integrationType == "" || system == "" {
			continue
		}
		service.defaultSystems[integrationType] = system
	}

	for name, systemConfig := range config.Systems {
		name = normalizeSystem(name)
		if name == "" {
			continue
		}
		for _, legacyType := range append([]string{systemConfig.IntegrationType}, systemConfig.IntegrationTypes...) {
			legacyType = strings.TrimSpace(strings.ToLower(legacyType))
			if legacyType == "tracker" || legacyType == "repository" {
				service.logger.Printf("Предупреждение совместимости: systems.%s integration_type=%s устарел; используйте %s", name, legacyType, normalizeIntegrationType(legacyType))
			}
		}

		state := systemState{
			Name:             name,
			Type:             normalizeSystem(systemConfig.Type),
			IntegrationTypes: normalizeIntegrationTypes(systemConfig),
			Configured:       true,
			Enabled:          systemEnabled(systemConfig),
			Default:          systemConfig.Default,
			Transport:        normalizeSystem(systemConfig.Transport),
			TaskLabelMapping: normalizeLabelMapping(systemConfig.TaskLabelMapping),
			Operations:       normalizeOperationConfigMap(systemConfig.Operations),
		}
		service.systems[name] = state

		if !state.Enabled {
			continue
		}

		providerConfig := systemConfig
		if err := resolvePrivateSystemConfig(context.Background(), name, &providerConfig, store); err != nil {
			service.registerConfiguredProvider(name, state, failingProvider{
				kind:    model.FailureKindAuthRequired,
				message: err.Error(),
			})
			continue
		}

		switch state.Type {
		case "github":
			service.registerConfiguredProvider(name, state, githubprovider.NewServiceWithConfig(providerConfig))
		case "bitbucket":
			service.registerConfiguredProvider(name, state, bitbucketprovider.NewService(providerConfig))
		case "mattermost":
			service.registerConfiguredProvider(name, state, mattermostprovider.NewService(providerConfig))
		case "telegram":
			service.registerConfiguredProvider(name, state, telegramprovider.NewService(providerConfig))
		case "confluence":
			service.registerConfiguredProvider(name, state, confluenceprovider.NewService(providerConfig))
		case "local-tracker":
			service.registerConfiguredProvider(name, state, localtrackerprovider.NewService(providerConfig))
		case "script":
			service.registerConfiguredProvider(name, state, scriptprovider.NewService(providerConfig))
		case "":
			service.systems[name] = state
		default:
			service.systems[name] = state
		}
	}

	service.applyConfiguredDefaults()
	return service
}

func resolvePrivateSystemConfig(ctx context.Context, system string, config *model.IntegrationSystemConfig, store secrets.Store) error {
	if config == nil {
		return nil
	}
	if strings.TrimSpace(config.Token) != "" {
		return nil
	}
	if strings.TrimSpace(config.TokenPrivate) != "" {
		if store == nil {
			return fmt.Errorf("integration system %q requires private value %q but private store is not configured", normalizeSystem(system), strings.TrimSpace(config.TokenPrivate))
		}
		value, err := store.Get(ctx, config.TokenPrivate)
		if err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				return fmt.Errorf("integration system %q references missing private value %q", normalizeSystem(system), strings.TrimSpace(config.TokenPrivate))
			}
			return fmt.Errorf("integration system %q cannot read private value %q: %w", normalizeSystem(system), strings.TrimSpace(config.TokenPrivate), err)
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("integration system %q references empty private value %q", normalizeSystem(system), strings.TrimSpace(config.TokenPrivate))
		}
		config.Token = value
		return nil
	}
	if resolvedTokenEnvValue(*config) != "" {
		return nil
	}
	return resolvePrivateGitHubAppConfig(ctx, system, config, store)
}

func resolvedTokenEnvValue(config model.IntegrationSystemConfig) string {
	if name := strings.TrimSpace(config.TokenEnv); name != "" {
		return strings.TrimSpace(os.Getenv(name))
	}
	return ""
}

func resolvePrivateGitHubAppConfig(ctx context.Context, system string, config *model.IntegrationSystemConfig, store secrets.Store) error {
	if config == nil || strings.TrimSpace(config.GitHubAppPrivateKey) != "" || strings.TrimSpace(config.GitHubAppPrivateKeyPrivate) == "" {
		return nil
	}
	if store == nil {
		return fmt.Errorf("integration system %q requires private value %q but private store is not configured", normalizeSystem(system), strings.TrimSpace(config.GitHubAppPrivateKeyPrivate))
	}
	value, err := store.Get(ctx, config.GitHubAppPrivateKeyPrivate)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return fmt.Errorf("integration system %q references missing private value %q", normalizeSystem(system), strings.TrimSpace(config.GitHubAppPrivateKeyPrivate))
		}
		return fmt.Errorf("integration system %q cannot read private value %q: %w", normalizeSystem(system), strings.TrimSpace(config.GitHubAppPrivateKeyPrivate), err)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("integration system %q references empty private value %q", normalizeSystem(system), strings.TrimSpace(config.GitHubAppPrivateKeyPrivate))
	}
	config.GitHubAppPrivateKey = value
	return nil
}

type failingProvider struct {
	kind    string
	message string
}

func (p failingProvider) Execute(_ context.Context, req ProviderRequest) (Response, error) {
	message := strings.TrimSpace(p.message)
	if message == "" {
		message = "integration provider is not configured"
	}
	return Response{
		IntegrationType: req.IntegrationType,
		System:          req.System,
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
		Status:          model.ResponseStatusFailed,
		Failure: &Failure{
			Kind:    firstNonEmpty(p.kind, model.FailureKindNotConfigured),
			Message: message,
		},
		AuthStatus: &AuthStatus{
			System:        req.System,
			State:         firstNonEmpty(p.kind, model.FailureKindNotConfigured),
			Available:     false,
			Authenticated: false,
			Message:       message,
		},
	}, fmt.Errorf("%s", message)
}

func newEmptyService(logger *log.Logger) *Service {
	return &Service{
		logger:         ensureLogger(logger),
		providers:      make(map[string]Provider),
		defaultSystems: make(map[string]string),
		systems:        make(map[string]systemState),
	}
}

func (s *Service) RegisterProvider(system string, provider Provider) {
	system = normalizeSystem(system)
	state := s.systems[system]
	state.Name = system
	if state.Type == "" {
		state.Type = system
	}
	if len(state.IntegrationTypes) == 0 {
		state.IntegrationTypes = defaultIntegrationTypesForProvider(state.Type)
	}
	state.Configured = true
	state.Enabled = true
	s.registerConfiguredProvider(system, state, provider)
}

func (s *Service) registerConfiguredProvider(system string, state systemState, provider Provider) {
	name := normalizeSystem(system)
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
	if len(state.IntegrationTypes) == 0 {
		state.IntegrationTypes = defaultIntegrationTypesForProvider(state.Type)
	}
	state.IntegrationTypes = dedupeStrings(state.IntegrationTypes)
	state.TaskLabelMapping = normalizeLabelMapping(state.TaskLabelMapping)
	s.systems[name] = state
}

func (s *Service) applyConfiguredDefaults() {
	if s.defaultSystem != "" {
		if state, ok := s.systems[s.defaultSystem]; ok {
			for _, integrationType := range state.IntegrationTypes {
				if _, exists := s.defaultSystems[integrationType]; !exists {
					s.defaultSystems[integrationType] = s.defaultSystem
				}
			}
		}
	}

	for system, state := range s.systems {
		if !state.Enabled || !state.Default {
			continue
		}
		for _, integrationType := range state.IntegrationTypes {
			if _, exists := s.defaultSystems[integrationType]; !exists {
				s.defaultSystems[integrationType] = system
			}
		}
	}

}

func (s *Service) resolveRoute(req Request) (Route, error) {
	normalized, err := s.normalizeRequest(req)
	if err != nil {
		route := s.errorRoute(req, err)
		s.logger.Printf("Реестр интеграции отклонил запрос: система=%q тип=%q причина=%q", req.System, req.IntegrationType, err)
		return route, err
	}

	state, known := s.systems[normalized.System]
	if !known {
		// Неизвестная система остаётся маршрутом с диагностикой: окончательный
		// отказ формируется Execute после разрешения запроса.
		return s.routeForUnknownSystem(normalized), nil
	}
	if state.Registered && !systemSupportsOperation(state, normalized.IntegrationType, normalized.ObjectType, normalized.Operation) {
		err := fmt.Errorf("integration operation is not supported: %s.%s.%s", normalized.IntegrationType, canonicalObjectType(normalized.ObjectType), normalized.Operation)
		s.logger.Printf("Реестр интеграции отклонил запрос: система=%q причина=%q", normalized.System, err)
		return s.errorRoute(req, err), err
	}
	_, registered := s.providers[normalized.System]
	available := registered && systemSupportsIntegrationType(state, normalized.IntegrationType)

	route := Route{
		IntegrationType:   normalized.IntegrationType,
		System:            normalized.System,
		Provider:          normalized.System,
		ProviderType:      state.Type,
		ProviderAvailable: available,
		Resource:          normalized.Resource,
		ObjectType:        normalized.ObjectType,
		Operation:         normalized.Operation,
		ExpectedResult:    expectedResult(normalized.IntegrationType, normalized.ObjectType, normalized.Resource, normalized.Operation),
		Diagnostics:       buildDiagnostics(normalized.IntegrationType, normalized.System, normalized.Resource, normalized.ObjectType, normalized.Operation, available, registered, known, state, s.registeredSystems()),
	}
	s.logger.Printf("Реестр интеграции сформировал маршрут: тип=%q система=%q объект=%q операция=%q провайдер=%q доступен=%t", route.IntegrationType, route.System, route.ObjectType, route.Operation, route.Provider, route.ProviderAvailable)
	return route, nil
}

func (s *Service) Execute(ctx context.Context, req Request) (Response, error) {
	normalized, err := s.normalizeRequest(req)
	if err != nil {
		return Response{
			IntegrationType: normalizeIntegrationType(firstNonEmpty(req.IntegrationType, inferIntegrationType(firstNonEmpty(req.ObjectType, req.Resource)))),
			Resource:        normalizeResource(req.Resource),
			ObjectType:      normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource)),
			Operation:       normalizeOperation(req.Operation),
			Status:          model.ResponseStatusFailed,
			Failure:         failureFromError(model.FailureKindInvalidRequest, false, err),
			Route:           s.errorRoute(req, err),
		}, err
	}

	route, err := s.resolveRoute(req)
	if err != nil {
		return Response{}, err
	}

	normalized.Route = route
	provider, ok := s.providers[route.System]
	if !ok || !route.ProviderAvailable {
		if _, known := s.systems[route.System]; !known {
			err := fmt.Errorf("integration system is not configured: %s", route.System)
			return responseWithFailure(route, model.FailureKindNotConfigured, false, err), err
		}
		if state, known := s.systems[route.System]; known {
			switch {
			case !state.Enabled:
				err := fmt.Errorf("integration provider disabled by configuration: %s", route.System)
				return responseWithFailure(route, model.FailureKindNotConfigured, false, err), err
			case state.Type != "" && !ok:
				err := fmt.Errorf("integration provider type is not supported in current build: %s (%s)", route.System, state.Type)
				return responseWithFailure(route, model.FailureKindUnsupportedOperation, false, err), err
			case !systemSupportsIntegrationType(state, route.IntegrationType):
				err := fmt.Errorf("integration provider %s does not support integration type %s", route.System, route.IntegrationType)
				return responseWithFailure(route, model.FailureKindUnsupportedOperation, false, err), err
			}
		}
		err := fmt.Errorf("integration provider not registered: %s", route.System)
		return responseWithFailure(route, model.FailureKindNotConfigured, false, err), err
	}

	result, err := provider.Execute(ctx, normalized)
	applyRouteToResponse(&result, route)
	if state, ok := s.systems[route.System]; ok {
		applyTaskLabelMapping(&result, state.TaskLabelMapping)
	}
	if result.Status == "" {
		if err != nil || result.Failure != nil {
			result.Status = model.ResponseStatusFailed
		} else if result.Partial {
			result.Status = model.ResponseStatusPartial
		} else {
			result.Status = model.ResponseStatusOK
		}
	}
	if err != nil && result.Failure == nil {
		result.Failure = failureFromError(model.FailureKindExternalFailure, false, err)
		result.Status = model.ResponseStatusFailed
	}
	normalizeDerivedObjects(&result)
	if err != nil {
		return result, err
	}

	return result, nil
}

func (s *Service) normalizeRequest(req Request) (ProviderRequest, error) {
	objectType := normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource))
	integrationType := normalizeIntegrationType(req.IntegrationType)
	if integrationType == "" {
		integrationType = inferIntegrationType(objectType)
	}
	system := normalizeSystem(req.System)
	if req.SystemProvided && system == "" {
		return ProviderRequest{}, fmt.Errorf("invalid integration request: system is required")
	}
	if system == "" {
		system = s.defaultSystemForType(integrationType)
	}
	if system == "" {
		return ProviderRequest{}, fmt.Errorf("invalid integration request: no default system configured for integration type %q", integrationType)
	}
	if integrationType == "" {
		integrationType = s.firstIntegrationTypeForSystem(system)
	}
	if objectType == "canonical-object" {
		switch integrationType {
		case model.IntegrationTypeIssue:
			objectType = "issue"
		case model.IntegrationTypeRepo:
			objectType = "repository"
		}
	}

	identifier := strings.TrimSpace(firstNonEmpty(req.ID, req.ExternalID))
	normalized := ProviderRequest{
		IntegrationType:    integrationType,
		System:             system,
		SystemProvided:     req.SystemProvided,
		Resource:           normalizeResource(firstNonEmpty(req.Resource, objectType)),
		ObjectType:         objectType,
		Operation:          normalizeOperation(req.Operation),
		Repository:         strings.TrimSpace(req.Repository),
		RepoProvided:       req.RepoProvided,
		ID:                 identifier,
		MergeRequestNumber: req.MergeRequestNumber,
		ExternalID:         identifier,
		Base:               strings.TrimSpace(req.Base),
		Head:               strings.TrimSpace(req.Head),
		Title:              strings.TrimSpace(req.Title),
		Body:               strings.TrimSpace(req.Body),
		Text:               strings.TrimSpace(req.Text),
		Draft:              req.Draft,
		Query:              strings.TrimSpace(req.Query),
		State:              strings.TrimSpace(req.State),
		Scope:              strings.TrimSpace(req.Scope),
		Limit:              req.Limit,
		Path:               strings.TrimSpace(req.Path),
		Line:               req.Line,
		Side:               strings.TrimSpace(req.Side),
		ChannelID:          strings.TrimSpace(req.ChannelID),
		ThreadID:           strings.TrimSpace(req.ThreadID),
		MessageID:          strings.TrimSpace(req.MessageID),
		Reaction:           strings.TrimSpace(req.Reaction),
		Fields:             trimStrings(req.Fields),
		Labels:             trimStrings(req.Labels),
		ExcludeLabels:      trimStrings(req.ExcludeLabels),
	}
	if normalized.ObjectType == "" {
		normalized.ObjectType = normalizeObjectType(normalized.Resource)
	}
	if normalized.ObjectType == "canonical-object" {
		switch normalized.IntegrationType {
		case model.IntegrationTypeIssue:
			normalized.ObjectType, normalized.Resource = "issue", "issue"
		case model.IntegrationTypeRepo:
			normalized.ObjectType, normalized.Resource = "repository", "repository"
		}
	}
	if state, ok := s.systems[system]; ok {
		normalized.Labels = mapCanonicalLabelsToExternal(normalized.Labels, state.TaskLabelMapping)
		normalized.ExcludeLabels = mapCanonicalLabelsToExternal(normalized.ExcludeLabels, state.TaskLabelMapping)
	}

	return normalized, nil
}

func (s *Service) errorRoute(req Request, err error) Route {
	objectType := normalizeObjectType(firstNonEmpty(req.ObjectType, req.Resource))
	integrationType := normalizeIntegrationType(req.IntegrationType)
	system := normalizeSystem(req.System)
	if system == "" && !req.SystemProvided {
		system = s.defaultSystemForType(integrationType)
	}
	return Route{
		IntegrationType: integrationType,
		System:          system,
		Provider:        system,
		Resource:        normalizeResource(req.Resource),
		ObjectType:      objectType,
		Operation:       normalizeOperation(req.Operation),
		ExpectedResult:  expectedResult(integrationType, objectType, normalizeResource(req.Resource), normalizeOperation(req.Operation)),
		Diagnostics: []string{
			fmt.Sprintf("request system=%s resource=%s operation=%s", system, normalizeResource(req.Resource), normalizeOperation(req.Operation)),
			fmt.Sprintf("request type=%s system=%s resource=%s object=%s operation=%s", integrationType, system, normalizeResource(req.Resource), objectType, normalizeOperation(req.Operation)),
			"registry mode=direct-resolution",
			"invalid-request missing system",
			fmt.Sprintf("invalid-request %s", err.Error()),
		},
	}
}

func (s *Service) routeForUnknownSystem(req ProviderRequest) Route {
	return Route{
		IntegrationType: req.IntegrationType,
		System:          req.System,
		Provider:        req.System,
		Resource:        req.Resource,
		ObjectType:      req.ObjectType,
		Operation:       req.Operation,
		ExpectedResult:  expectedResult(req.IntegrationType, req.ObjectType, req.Resource, req.Operation),
		Diagnostics: []string{
			fmt.Sprintf("request system=%s resource=%s operation=%s", req.System, req.Resource, req.Operation),
			"registry mode=direct-resolution",
			fmt.Sprintf("provider=%s unknown to current integration configuration", req.System),
			fmt.Sprintf("registered systems=%s", strings.Join(s.registeredSystems(), ",")),
		},
	}
}

func (s *Service) defaultSystemForType(integrationType string) string {
	integrationType = normalizeIntegrationType(integrationType)
	if integrationType == "" {
		return ""
	}
	return normalizeSystem(s.defaultSystems[integrationType])
}

func (s *Service) firstIntegrationTypeForSystem(system string) string {
	state, ok := s.systems[normalizeSystem(system)]
	if !ok || len(state.IntegrationTypes) == 0 {
		return ""
	}
	return state.IntegrationTypes[0]
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
	return strings.TrimSpace(strings.ToLower(system))
}

func normalizeIntegrationType(integrationType string) string {
	switch value := strings.TrimSpace(strings.ToLower(integrationType)); value {
	case "tracker":
		return model.IntegrationTypeIssue
	case "repository":
		return model.IntegrationTypeRepo
	default:
		return value
	}
}

func normalizeObjectType(objectType string) string {
	objectType = strings.TrimSpace(strings.ToLower(objectType))
	switch objectType {
	case "task":
		return "task"
	case "issue":
		return "issue"
	case "pull-request", "pr", "merge-request", "mr":
		return "merge-request"
	case "repo":
		return "repository"
	case "comment":
		return "comment"
	case "thread", "discussion":
		return "thread"
	case "wiki-page", "document", "doc":
		return "page"
	case "task-label", "label":
		return "label"
	default:
		return objectType
	}
}

func normalizeResource(resource string) string {
	resource = strings.TrimSpace(strings.ToLower(resource))
	if resource == "" {
		return "canonical-object"
	}
	return resource
}

func normalizeOperation(operation string) string {
	operation = strings.TrimSpace(strings.ToLower(operation))
	if operation == "" {
		return "get"
	}
	switch operation {
	case "list-comments":
		return "comments"
	case "add-label", "add-labels":
		return "add"
	case "remove-label", "remove-labels", "delete-label", "delete-labels":
		return "remove"
	case "send", "post":
		return "create"
	default:
		return operation
	}
}

func inferIntegrationType(objectType string) string {
	switch normalizeObjectType(objectType) {
	case "task", "issue", "comment", "label":
		return model.IntegrationTypeTracker
	case "repository", "branch", "commit", "pull-request", "pr", "merge-request", "mr":
		return model.IntegrationTypeRepository
	case "channel", "thread", "message", "reaction":
		return model.IntegrationTypeMessenger
	case "page", "wiki-page", "document", "doc":
		return model.IntegrationTypeWiki
	default:
		return ""
	}
}

func normalizeIntegrationTypes(config model.IntegrationSystemConfig) []string {
	var values []string
	if config.IntegrationType != "" {
		values = append(values, config.IntegrationType)
	}
	values = append(values, config.IntegrationTypes...)
	if len(values) == 0 {
		values = defaultIntegrationTypesForProvider(normalizeSystem(config.Type))
	}
	for i := range values {
		values[i] = normalizeIntegrationType(values[i])
	}
	return dedupeStrings(values)
}

func normalizeOperationConfigMap(operations map[string]model.IntegrationOperationConfig) map[string]model.IntegrationOperationConfig {
	if len(operations) == 0 {
		return nil
	}

	result := make(map[string]model.IntegrationOperationConfig, len(operations))
	for name, operation := range operations {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		result[name] = operation
	}
	return result
}

func defaultIntegrationTypesForProvider(providerType string) []string {
	switch normalizeSystem(providerType) {
	case "github":
		return []string{model.IntegrationTypeTracker, model.IntegrationTypeRepository}
	case "bitbucket":
		return []string{model.IntegrationTypeRepository}
	case "mattermost", "telegram":
		return []string{model.IntegrationTypeMessenger}
	case "confluence":
		return []string{model.IntegrationTypeWiki}
	case "local-tracker", "script":
		return []string{model.IntegrationTypeTracker}
	default:
		return nil
	}
}

func systemSupportsIntegrationType(state systemState, integrationType string) bool {
	integrationType = normalizeIntegrationType(integrationType)
	if integrationType == "" {
		return true
	}
	for _, item := range state.IntegrationTypes {
		if item == integrationType {
			return true
		}
	}
	return false
}

func systemSupportsOperation(state systemState, integrationType string, objectType string, operation string) bool {
	// Состояние авторизации является общей служебной операцией адаптера и не
	// относится к типо-ориентированным объектам.
	if normalizeObjectType(objectType) == "auth" && normalizeOperation(operation) == "status" {
		return true
	}
	// Сценарный адаптер получает каталог операций из конфигурации. Пустой
	// каталог не ограничивает контракт: это позволяет проверять маршрут до
	// подключения конкретного сценария.
	if state.Type == "script" && len(state.Operations) == 0 {
		return true
	}
	integrationType = normalizeIntegrationType(integrationType)
	objectType = canonicalObjectType(objectType)
	operation = normalizeOperation(operation)
	for _, template := range builtinOperationTemplates(state.Type) {
		if normalizeIntegrationType(template.IntegrationType) == integrationType &&
			canonicalObjectType(template.ObjectType) == objectType &&
			normalizeOperation(template.Operation) == operation {
			return true
		}
	}
	// Операции комментариев запроса на слияние публикуются как вложенные
	// имена каталога, тогда как адаптеры принимают канонический объект
	// merge-request-comment. Сопоставляем обе формы до вызова адаптера.
	if integrationType == model.IntegrationTypeRepo && objectType == "comment" {
		for _, template := range builtinOperationTemplates(state.Type) {
			if strings.HasPrefix(template.Name, "repo.merge-request.comment.") && normalizeOperation(template.Operation) == operation {
				return true
			}
		}
	}
	for name := range state.Operations {
		configuredType, configuredObject, configuredOperation := parseOperationName(name)
		if configuredType == integrationType && configuredObject == objectType && configuredOperation == operation {
			return true
		}
	}
	return false
}

func buildDiagnostics(integrationType string, system string, resource string, objectType string, operation string, available bool, registered bool, known bool, state systemState, registeredSystems []string) []string {
	diagnostics := []string{
		fmt.Sprintf("request system=%s resource=%s operation=%s", system, resource, operation),
		fmt.Sprintf("request type=%s system=%s resource=%s object=%s operation=%s", integrationType, system, resource, objectType, operation),
		"registry mode=direct-resolution",
	}

	if available {
		diagnostics = append(diagnostics, fmt.Sprintf("provider=%s registered", system))
		if state.Type != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("provider-type=%s", state.Type))
		}
		if state.Transport != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("transport=%s", state.Transport))
		}
		return diagnostics
	}

	if known {
		switch {
		case !state.Enabled:
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s disabled by integration configuration", system))
		case registered && !systemSupportsIntegrationType(state, integrationType):
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s does not support integration type=%s", system, integrationType))
		case state.Type != "" && !registered:
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s configured with unsupported type=%s", system, state.Type))
		default:
			diagnostics = append(diagnostics, fmt.Sprintf("provider=%s not registered in current build", system))
		}
	} else {
		diagnostics = append(diagnostics, fmt.Sprintf("provider=%s unknown to current integration configuration", system))
	}

	if len(registeredSystems) == 0 {
		diagnostics = append(diagnostics, "registered systems=<none>")
		return diagnostics
	}

	diagnostics = append(diagnostics, fmt.Sprintf("registered systems=%s", strings.Join(registeredSystems, ",")))
	return diagnostics
}

func expectedResult(integrationType string, objectType string, resource string, operation string) string {
	switch normalizeIntegrationType(integrationType) {
	case model.IntegrationTypeTracker:
		switch normalizeObjectType(firstNonEmpty(objectType, resource)) {
		case "issue":
			if operation == "comments" {
				return "issue-comment[]"
			}
			if operation == "search" {
				return "issue-search-result[]"
			}
			return "issue"
		case "task":
			if operation == "comments" {
				return "task-comment[]"
			}
			if operation == "search" || operation == "list" {
				return "tracker-search-result[]"
			}
			return "canonical-task"
		case "comment":
			return "task-comment"
		case "label":
			if operation == "add" || operation == "remove" {
				return "integration-operation-result"
			}
			return "task-label"
		}
	case model.IntegrationTypeRepository:
		switch normalizeObjectType(firstNonEmpty(objectType, resource)) {
		case "repository":
			return "canonical-repository"
		case "merge-request":
			if operation == "comments" {
				return "review-remark[]"
			}
			if operation == "search" || operation == "list" {
				return "canonical-merge-request[]"
			}
			if resource == "pr" || resource == "pull-request" {
				if operation == "create" {
					return "integration-pull-request-status"
				}
			}
			if operation == "create" {
				return "integration-operation-result"
			}
			return "canonical-merge-request"
		case "comment", "review", "review-remark", "merge-request-comment":
			if operation == "create" || operation == "resolve" || operation == "reply" {
				return "integration-operation-result"
			}
			return "review-remark[]"
		}
	case model.IntegrationTypeMessenger:
		switch normalizeObjectType(firstNonEmpty(objectType, resource)) {
		case "thread":
			return "message-thread"
		case "message":
			if operation == "create" {
				return "message"
			}
			return "message[]"
		case "reaction":
			return "integration-operation-result"
		}
	case model.IntegrationTypeWiki:
		switch normalizeObjectType(firstNonEmpty(objectType, resource)) {
		case "page":
			if operation == "search" || operation == "list" {
				return "wiki-page[]"
			}
			return "wiki-page"
		}
	}

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
	case "auth":
		return "integration-auth-status"
	case "repository", "repo":
		return "tracker-repository"
	default:
		return "normalized-response"
	}
}

func applyRouteToResponse(result *Response, route Route) {
	if result == nil {
		return
	}

	result.IntegrationType = firstNonEmpty(result.IntegrationType, route.IntegrationType)
	result.System = normalizeSystem(firstNonEmpty(result.System, route.System))
	result.Resource = firstNonEmpty(result.Resource, route.Resource)
	result.ObjectType = firstNonEmpty(result.ObjectType, route.ObjectType)
	result.Operation = firstNonEmpty(result.Operation, route.Operation)
	result.Route = route
	applyResponseSystem(result, route.System)
}

func applyResponseSystem(result *Response, system string) {
	system = normalizeSystem(system)
	if result == nil || system == "" {
		return
	}

	result.System = system
	if result.Task != nil {
		result.Task.System = system
		result.Task.Author.System = system
		for i := range result.Task.Assignees {
			result.Task.Assignees[i].System = system
		}
	}
	for i := range result.TaskComments {
		result.TaskComments[i].System = system
		result.TaskComments[i].Author.System = system
	}
	if result.Repository != nil {
		result.Repository.System = system
	}
	if result.MergeRequest != nil {
		result.MergeRequest.System = system
		result.MergeRequest.Author.System = system
	}
	for i := range result.ReviewRemarks {
		result.ReviewRemarks[i].System = system
		result.ReviewRemarks[i].Author.System = system
	}
	if result.Conversation != nil {
		result.Conversation.System = system
		for i := range result.Conversation.Messages {
			result.Conversation.Messages[i].System = system
			result.Conversation.Messages[i].Author.System = system
		}
		for i := range result.Conversation.Reactions {
			result.Conversation.Reactions[i].System = system
			result.Conversation.Reactions[i].User.System = system
		}
	}
	for i := range result.Messages {
		result.Messages[i].System = system
		result.Messages[i].Author.System = system
	}
	if result.Message != nil {
		result.Message.System = system
		result.Message.Author.System = system
	}
	if result.WikiPage != nil {
		result.WikiPage.System = system
		result.WikiPage.UpdatedBy.System = system
	}
	for i := range result.WikiPages {
		result.WikiPages[i].System = system
		result.WikiPages[i].UpdatedBy.System = system
	}
	if result.OperationResult != nil {
		result.OperationResult.System = system
	}
	if result.AuthStatus != nil {
		result.AuthStatus.System = system
	}
	if result.RepositoryStatus != nil {
		result.RepositoryStatus.System = system
	}
	if result.IssueStatus != nil {
		result.IssueStatus.System = system
	}
	if result.PullRequestStatus != nil {
		result.PullRequestStatus.System = system
	}
	if result.Issue != nil {
		result.Issue.System = system
		result.Issue.Author.System = system
		for i := range result.Issue.Assignees {
			result.Issue.Assignees[i].System = system
		}
	}
	if result.PullRequest != nil {
		result.PullRequest.System = system
		result.PullRequest.Author.System = system
	}
	for i := range result.Comments {
		result.Comments[i].System = system
		result.Comments[i].Author.System = system
	}
	for i := range result.Reviews {
		result.Reviews[i].System = system
		result.Reviews[i].Author.System = system
	}
	if result.RepositoryRef != nil {
		result.RepositoryRef.System = system
	}
	for i := range result.SearchResults {
		result.SearchResults[i].System = system
		result.SearchResults[i].Author.System = system
		for j := range result.SearchResults[i].Assignees {
			result.SearchResults[i].Assignees[j].System = system
		}
	}
	for i := range result.Artifacts {
		result.Artifacts[i].System = system
	}
}

func normalizeDerivedObjects(result *Response) {
	if result == nil {
		return
	}
	if result.Task == nil && result.Issue != nil {
		result.Task = canonicalTaskFromTrackerIssue(*result.Issue)
	}
	if result.Issue == nil && result.Task != nil {
		issue := trackerIssueFromCanonicalTask(*result.Task)
		result.Issue = &issue
	}
	if len(result.TaskComments) == 0 && len(result.Comments) > 0 {
		result.TaskComments = make([]TaskComment, 0, len(result.Comments))
		for _, comment := range result.Comments {
			result.TaskComments = append(result.TaskComments, taskCommentFromTrackerComment(comment))
		}
	}
	if len(result.Comments) == 0 && len(result.TaskComments) > 0 {
		result.Comments = make([]TrackerComment, 0, len(result.TaskComments))
		for _, comment := range result.TaskComments {
			result.Comments = append(result.Comments, trackerCommentFromTaskComment(comment))
		}
	}
	if result.Repository == nil && result.RepositoryRef != nil {
		result.Repository = repositoryFromTrackerRepository(*result.RepositoryRef)
	}
	if result.RepositoryRef == nil && result.Repository != nil {
		repository := trackerRepositoryFromRepository(*result.Repository)
		result.RepositoryRef = &repository
	}
	if result.MergeRequest == nil && result.PullRequest != nil {
		result.MergeRequest = mergeRequestFromTrackerPullRequest(*result.PullRequest)
	}
	if result.PullRequest == nil && result.MergeRequest != nil {
		pr := trackerPullRequestFromMergeRequest(*result.MergeRequest)
		result.PullRequest = &pr
	}
}

func applyTaskLabelMapping(result *Response, mapping map[string]string) {
	if result == nil {
		return
	}
	if result.Issue != nil {
		externalLabels := append([]string(nil), result.Issue.Labels...)
		canonicalLabels := mapExternalLabelsToCanonical(result.Issue.Labels, mapping)
		result.Issue.Labels = canonicalLabels
		if result.Task != nil && (len(result.Task.Traits) == 0 || sameStrings(result.Task.Traits, externalLabels)) {
			result.Task.Traits = append([]string(nil), canonicalLabels...)
		}
		return
	}
	if result.Task != nil {
		result.Task.Traits = mapExternalLabelsToCanonical(result.Task.Traits, mapping)
	}
	for i := range result.SearchResults {
		result.SearchResults[i].Labels = mapExternalLabelsToCanonical(result.SearchResults[i].Labels, mapping)
	}
}

func canonicalTaskFromTrackerIssue(issue TrackerIssue) *CanonicalTask {
	return &CanonicalTask{
		System:     issue.System,
		Repository: issue.Repository,
		ID:         firstNonEmpty(issue.ID, issue.ExternalID),
		ExternalID: issue.ExternalID,
		Title:      issue.Title,
		Body:       issue.Body,
		State:      issue.State,
		Traits:     append([]string(nil), issue.Labels...),
		Assignees:  usersFromTrackerUsers(issue.Assignees),
		Author:     userFromTrackerUser(issue.Author),
		URL:        issue.URL,
		CreatedAt:  issue.CreatedAt,
		UpdatedAt:  issue.UpdatedAt,
	}
}

func trackerIssueFromCanonicalTask(task CanonicalTask) TrackerIssue {
	return TrackerIssue{
		System:     task.System,
		Repository: task.Repository,
		ID:         task.ID,
		ExternalID: task.ExternalID,
		Title:      task.Title,
		Body:       task.Body,
		State:      task.State,
		Labels:     append([]string(nil), task.Traits...),
		Assignees:  trackerUsersFromUsers(task.Assignees),
		Author:     trackerUserFromUser(task.Author),
		URL:        task.URL,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
	}
}

func taskCommentFromTrackerComment(comment TrackerComment) TaskComment {
	return TaskComment{
		System:     comment.System,
		Repository: comment.Repository,
		TaskID:     comment.TaskID,
		Author:     userFromTrackerUser(comment.Author),
		Body:       comment.Body,
		URL:        comment.URL,
		CreatedAt:  comment.CreatedAt,
		UpdatedAt:  comment.UpdatedAt,
	}
}

func trackerCommentFromTaskComment(comment TaskComment) TrackerComment {
	return TrackerComment{
		System:     comment.System,
		Repository: comment.Repository,
		TaskID:     comment.TaskID,
		Author:     trackerUserFromUser(comment.Author),
		Body:       comment.Body,
		URL:        comment.URL,
		CreatedAt:  comment.CreatedAt,
		UpdatedAt:  comment.UpdatedAt,
	}
}

func repositoryFromTrackerRepository(repository TrackerRepository) *Repository {
	return &Repository{
		System:        repository.System,
		FullName:      repository.FullName,
		Owner:         repository.Owner,
		Name:          repository.Name,
		Description:   repository.Description,
		DefaultBranch: repository.DefaultBranch,
		URL:           repository.URL,
	}
}

func trackerRepositoryFromRepository(repository Repository) TrackerRepository {
	return TrackerRepository{
		System:        repository.System,
		FullName:      repository.FullName,
		Owner:         repository.Owner,
		Name:          repository.Name,
		Description:   repository.Description,
		DefaultBranch: repository.DefaultBranch,
		URL:           repository.URL,
	}
}

func mergeRequestFromTrackerPullRequest(pr TrackerPullRequest) *MergeRequest {
	return &MergeRequest{
		System:         pr.System,
		Repository:     pr.Repository,
		Number:         pr.Number,
		Title:          pr.Title,
		Body:           pr.Body,
		State:          pr.State,
		Traits:         append([]string(nil), pr.Labels...),
		Author:         userFromTrackerUser(pr.Author),
		ReviewDecision: pr.ReviewDecision,
		BaseRef:        pr.BaseRef,
		HeadRef:        pr.HeadRef,
		URL:            pr.URL,
		CreatedAt:      pr.CreatedAt,
		UpdatedAt:      pr.UpdatedAt,
	}
}

func trackerPullRequestFromMergeRequest(pr MergeRequest) TrackerPullRequest {
	return TrackerPullRequest{
		System:         pr.System,
		Repository:     pr.Repository,
		Number:         pr.Number,
		Title:          pr.Title,
		Body:           pr.Body,
		State:          pr.State,
		Author:         trackerUserFromUser(pr.Author),
		ReviewDecision: pr.ReviewDecision,
		BaseRef:        pr.BaseRef,
		HeadRef:        pr.HeadRef,
		Labels:         append([]string(nil), pr.Traits...),
		URL:            pr.URL,
		CreatedAt:      pr.CreatedAt,
		UpdatedAt:      pr.UpdatedAt,
	}
}

func userFromTrackerUser(user TrackerUser) User {
	return User{
		System:   user.System,
		Login:    user.Login,
		Name:     user.Name,
		Email:    user.Email,
		URL:      user.URL,
		IsBot:    user.IsBot,
		IsActive: user.IsActive,
	}
}

func trackerUserFromUser(user User) TrackerUser {
	return TrackerUser{
		System:   user.System,
		Login:    user.Login,
		Name:     user.Name,
		Email:    user.Email,
		URL:      user.URL,
		IsBot:    user.IsBot,
		IsActive: user.IsActive,
	}
}

func usersFromTrackerUsers(users []TrackerUser) []User {
	if len(users) == 0 {
		return nil
	}
	result := make([]User, 0, len(users))
	for _, user := range users {
		result = append(result, userFromTrackerUser(user))
	}
	return result
}

func trackerUsersFromUsers(users []User) []TrackerUser {
	if len(users) == 0 {
		return nil
	}
	result := make([]TrackerUser, 0, len(users))
	for _, user := range users {
		result = append(result, trackerUserFromUser(user))
	}
	return result
}

func normalizeLabelMapping(mapping map[string]string) map[string]string {
	if len(mapping) == 0 {
		return nil
	}
	result := make(map[string]string, len(mapping))
	for external, canonical := range mapping {
		external = strings.TrimSpace(external)
		if external == "" {
			continue
		}
		result[external] = strings.TrimSpace(canonical)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func mapExternalLabelsToCanonical(labels []string, mapping map[string]string) []string {
	if len(labels) == 0 {
		return nil
	}
	lookup := make(map[string]string, len(mapping))
	for external, canonical := range mapping {
		lookup[strings.ToLower(strings.TrimSpace(external))] = strings.TrimSpace(canonical)
	}

	result := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if canonical, ok := lookup[strings.ToLower(label)]; ok {
			if canonical == "" {
				continue
			}
			label = canonical
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, label)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func mapCanonicalLabelsToExternal(labels []string, mapping map[string]string) []string {
	if len(labels) == 0 {
		return nil
	}
	reverse := make(map[string][]string, len(mapping))
	for external, canonical := range mapping {
		external = strings.TrimSpace(external)
		canonical = strings.TrimSpace(canonical)
		if external == "" || canonical == "" {
			continue
		}
		key := strings.ToLower(canonical)
		reverse[key] = append(reverse[key], external)
	}
	for key := range reverse {
		sort.Slice(reverse[key], func(i, j int) bool {
			return strings.ToLower(reverse[key][i]) < strings.ToLower(reverse[key][j])
		})
	}

	result := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if external := reverse[strings.ToLower(label)]; len(external) > 0 {
			label = external[0]
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, label)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if strings.TrimSpace(left[i]) != strings.TrimSpace(right[i]) {
			return false
		}
	}
	return true
}

func responseWithFailure(route Route, kind string, retryable bool, err error) Response {
	return Response{
		IntegrationType: route.IntegrationType,
		System:          route.System,
		Resource:        route.Resource,
		ObjectType:      route.ObjectType,
		Operation:       route.Operation,
		Status:          model.ResponseStatusFailed,
		Failure:         failureFromError(kind, retryable, err),
		Route:           route,
	}
}

func failureFromError(kind string, retryable bool, err error) *Failure {
	if err == nil {
		return nil
	}
	return &Failure{Kind: kind, Retryable: retryable, Message: err.Error()}
}

func systemEnabled(config model.IntegrationSystemConfig) bool {
	return config.Enabled == nil || *config.Enabled
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func trimStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
