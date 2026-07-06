package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/methodology"
)

const workflowConfigRelativePath = ".progress/decision/workflows.json"

type workflowConfigFile struct {
	DefaultRoute string                          `json:"default_route"`
	Defaults     workflowRouteCheckConfig        `json:"defaults"`
	Routes       []workflowProcessingRouteConfig `json:"routes"`
}

type workflowProcessingRouteConfig struct {
	Name            string                     `json:"name"`
	Title           string                     `json:"title"`
	Description     string                     `json:"description"`
	Action          string                     `json:"action,omitempty"`
	Outcome         string                     `json:"outcome,omitempty"`
	HasFeatures     []string                   `json:"has_features,omitempty"`
	MissingFeatures []string                   `json:"missing_features,omitempty"`
	HasLabels       []string                   `json:"has_labels,omitempty"`
	MissingLabels   []string                   `json:"missing_labels,omitempty"`
	ExpectedResult  string                     `json:"expected_result,omitempty"`
	Constraints     []string                   `json:"constraints,omitempty"`
	ReasonCode      string                     `json:"reason_code,omitempty"`
	ReasonMessage   string                     `json:"reason_message,omitempty"`
	Checks          []workflowRouteCheckConfig `json:"checks"`
	Source          string                     `json:"-"`
}

type workflowRouteCheckConfig struct {
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Action          string   `json:"action"`
	Outcome         string   `json:"outcome"`
	HasFeatures     []string `json:"has_features"`
	MissingFeatures []string `json:"missing_features"`
	HasLabels       []string `json:"has_labels"`
	MissingLabels   []string `json:"missing_labels"`
	ExpectedResult  string   `json:"expected_result"`
	Constraints     []string `json:"constraints"`
	ReasonCode      string   `json:"reason_code"`
	ReasonMessage   string   `json:"reason_message"`
	Source          string   `json:"-"`
}

type selectedWorkflowRoute struct {
	Action         string
	Outcome        string
	ExpectedResult string
	Constraints    []string
	ReasonCode     string
	ReasonMessage  string
	Route          ProcessingRoute
	Checks         []RouteCheckResult
	RouteSource    string
	CheckSources   map[string]string
}

type workflowRouteResolutionError struct {
	code    string
	message string
}

func (e workflowRouteResolutionError) Error() string { return e.message }

func (e workflowRouteResolutionError) Code() string { return e.code }

func (s *Service) selectWorkflowRoute(ctx context.Context, task integration.CanonicalTask, routeName string) (selectedWorkflowRoute, error) {
	config, err := s.loadWorkflowConfig(ctx)
	if err != nil {
		return selectedWorkflowRoute{}, err
	}

	requestedRoute := strings.TrimSpace(routeName)
	if requestedRoute == "" {
		requestedRoute = strings.TrimSpace(config.DefaultRoute)
		if requestedRoute == "" {
			return selectedWorkflowRoute{}, workflowRouteResolutionError{code: "default_route_not_configured", message: "маршрут обработки по умолчанию не настроен"}
		}
	}

	var processingRoute workflowProcessingRouteConfig
	found := false
	for _, route := range config.Routes {
		if strings.TrimSpace(route.Name) != requestedRoute {
			continue
		}
		processingRoute = route
		found = true
		break
	}
	if !found {
		code := "route_not_found"
		if strings.TrimSpace(routeName) == "" {
			code = "default_route_not_found"
		}
		return selectedWorkflowRoute{}, workflowRouteResolutionError{code: code, message: fmt.Sprintf("маршрут обработки %q не найден", requestedRoute)}
	}

	bestScore := -1
	var selected selectedWorkflowRoute
	for index, checkConfig := range processingRoute.Checks {
		check := evaluateWorkflowRoute(checkConfig, task)
		if check.Status != RouteCheckStatusPassed {
			continue
		}
		score := workflowRouteScore(checkConfig)
		if score <= bestScore {
			continue
		}

		selected = selectedWorkflowRoute{
			Action:         strings.TrimSpace(checkConfig.Action),
			Outcome:        strings.TrimSpace(checkConfig.Outcome),
			ExpectedResult: strings.TrimSpace(checkConfig.ExpectedResult),
			Constraints:    normalizeRouteConstraints(checkConfig.Constraints),
			ReasonCode:     strings.TrimSpace(checkConfig.ReasonCode),
			ReasonMessage:  strings.TrimSpace(checkConfig.ReasonMessage),
			Route: ProcessingRoute{
				Name:        strings.TrimSpace(processingRoute.Name),
				Title:       strings.TrimSpace(processingRoute.Title),
				Description: strings.TrimSpace(processingRoute.Description),
			},
			RouteSource: strings.TrimSpace(processingRoute.Source),
			Checks:      []RouteCheckResult{check},
			CheckSources: map[string]string{
				firstNonEmpty(strings.TrimSpace(checkConfig.Name), fmt.Sprintf("check-%d", index+1)): firstNonEmpty(strings.TrimSpace(checkConfig.Source), strings.TrimSpace(processingRoute.Source)),
			},
		}
		bestScore = score
	}
	if bestScore < 0 {
		return selectedWorkflowRoute{}, workflowRouteResolutionError{code: "route_check_not_found", message: fmt.Sprintf("маршрут обработки %q не содержит подходящую проверку", requestedRoute)}
	}

	return selected, nil
}

func (s *Service) loadWorkflowConfig(ctx context.Context) (workflowConfigFile, error) {
	resolveRepoRoot := s.resolveRepoRoot
	if resolveRepoRoot == nil {
		resolveRepoRoot = resolveDecisionRepoRoot
	}
	readFile := s.readFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	repoRoot, err := resolveRepoRoot(ctx)
	if err != nil {
		return workflowConfigFile{}, fmt.Errorf("resolve git repository root for decision workflows: %w", err)
	}

	if config, err := s.loadWorkflowConfigFromMethodology(ctx, repoRoot); err == nil {
		return config, nil
	} else if !isMethodologyCatalogNotFound(err) {
		return workflowConfigFile{}, err
	}

	configPath := filepath.Join(repoRoot, workflowConfigRelativePath)
	content, err := readFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return workflowConfigFile{}, fmt.Errorf("decision workflow config not found: %s", configPath)
		}

		return workflowConfigFile{}, fmt.Errorf("read decision workflow config %s: %w", configPath, err)
	}

	var config workflowConfigFile
	if err := json.Unmarshal(content, &config); err != nil {
		return workflowConfigFile{}, fmt.Errorf("parse decision workflow config %s: %w", configPath, err)
	}
	config = normalizeWorkflowConfig(config)
	if err := validateWorkflowConfig(config); err != nil {
		return workflowConfigFile{}, fmt.Errorf("invalid decision workflow config %s: %w", configPath, err)
	}

	return config, nil
}

func (s *Service) loadWorkflowConfigFromMethodology(ctx context.Context, repoRoot string) (workflowConfigFile, error) {
	snapshot, err := methodology.NewService(s.logger).Load(ctx, methodology.CatalogRequest{RepoRoot: repoRoot})
	if err != nil {
		return workflowConfigFile{}, err
	}

	config := workflowConfigFile{DefaultRoute: strings.TrimSpace(snapshot.Catalog.DefaultRoute)}
	legacyChecks := make([]workflowRouteCheckConfig, 0, len(snapshot.Catalog.Routes))
	for _, route := range snapshot.Catalog.Routes {
		if len(route.Checks) != 0 {
			routeConfig := workflowProcessingRouteConfig{
				Name:        route.Name,
				Title:       route.Title,
				Description: route.Description,
				Checks:      workflowChecksFromMethodologyChecks(route.Checks, string(snapshot.Sources.Routes[route.Name])),
				Source:      string(snapshot.Sources.Routes[route.Name]),
			}
			config.Routes = append(config.Routes, routeConfig)
			continue
		}

		checkConfig := workflowRouteCheckConfig{
			Name:            route.Name,
			Title:           route.Title,
			Description:     route.Description,
			Action:          route.Action,
			Outcome:         route.Outcome,
			HasFeatures:     append([]string(nil), route.HasFeatures...),
			MissingFeatures: append([]string(nil), route.MissingFeatures...),
			HasLabels:       append([]string(nil), route.HasLabels...),
			MissingLabels:   append([]string(nil), route.MissingLabels...),
			ExpectedResult:  route.ExpectedResult,
			Constraints:     append([]string(nil), route.Constraints...),
			ReasonCode:      route.ReasonCode,
			ReasonMessage:   route.ReasonMessage,
			Source:          string(snapshot.Sources.Routes[route.Name]),
		}
		if checkConfig.Name == "default" {
			config.Defaults = checkConfig
			continue
		}
		if !workflowRouteHasMatchers(checkConfig) {
			continue
		}
		legacyChecks = append(legacyChecks, checkConfig)
	}
	if len(config.Routes) == 0 && len(legacyChecks) != 0 {
		config.Routes = append(config.Routes, workflowProcessingRouteConfig{
			Name:        "task-processing",
			Title:       "Обработка задачи",
			Description: "Совместимый маршрут обработки, собранный из плоского списка проверок.",
			Checks:      legacyChecks,
		})
	}
	if len(config.Routes) == 0 && (config.Defaults.Action != "" || config.Defaults.Outcome != "") {
		config.Routes = append(config.Routes, workflowProcessingRouteConfig{
			Name:   "default",
			Title:  "Маршрут по умолчанию",
			Checks: []workflowRouteCheckConfig{config.Defaults},
		})
	}
	config = normalizeWorkflowConfig(config)
	if err := validateWorkflowConfig(config); err != nil {
		return workflowConfigFile{}, fmt.Errorf("invalid methodology decision routes: %w", err)
	}

	return config, nil
}

func workflowChecksFromMethodologyChecks(checks []methodology.RouteCheck, source string) []workflowRouteCheckConfig {
	result := make([]workflowRouteCheckConfig, 0, len(checks))
	for _, check := range checks {
		result = append(result, workflowRouteCheckConfig{
			Name:            check.Name,
			Title:           check.Title,
			Description:     check.Description,
			Action:          check.Action,
			Outcome:         check.Outcome,
			HasFeatures:     append([]string(nil), check.HasFeatures...),
			MissingFeatures: append([]string(nil), check.MissingFeatures...),
			HasLabels:       append([]string(nil), check.HasLabels...),
			MissingLabels:   append([]string(nil), check.MissingLabels...),
			ExpectedResult:  check.ExpectedResult,
			Constraints:     append([]string(nil), check.Constraints...),
			ReasonCode:      check.ReasonCode,
			ReasonMessage:   check.ReasonMessage,
			Source:          source,
		})
	}
	return result
}

func normalizeWorkflowConfig(config workflowConfigFile) workflowConfigFile {
	config.DefaultRoute = strings.TrimSpace(config.DefaultRoute)
	for routeIndex, route := range config.Routes {
		if len(route.Checks) == 0 && (strings.TrimSpace(route.Action) != "" || strings.TrimSpace(route.Outcome) != "") {
			route.Checks = []workflowRouteCheckConfig{{
				Name:            route.Name,
				Title:           route.Title,
				Description:     route.Description,
				Action:          route.Action,
				Outcome:         route.Outcome,
				HasFeatures:     route.HasFeatures,
				MissingFeatures: route.MissingFeatures,
				HasLabels:       route.HasLabels,
				MissingLabels:   route.MissingLabels,
				ExpectedResult:  route.ExpectedResult,
				Constraints:     route.Constraints,
				ReasonCode:      route.ReasonCode,
				ReasonMessage:   route.ReasonMessage,
			}}
		}
		config.Routes[routeIndex] = route
	}
	return config
}

func workflowRouteHasMatchers(route workflowRouteCheckConfig) bool {
	return len(normalizeLabels(route.HasLabels)) != 0 ||
		len(normalizeLabels(route.MissingLabels)) != 0 ||
		len(normalizeFeatures(route.HasFeatures)) != 0 ||
		len(normalizeFeatures(route.MissingFeatures)) != 0
}

func workflowRouteScore(route workflowRouteCheckConfig) int {
	required := len(normalizeLabels(route.HasLabels)) + len(normalizeFeatures(route.HasFeatures))
	missing := len(normalizeLabels(route.MissingLabels)) + len(normalizeFeatures(route.MissingFeatures))
	return required*10 + missing
}

func isMethodologyCatalogNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "methodology catalog not found")
}

func validateWorkflowConfig(config workflowConfigFile) error {
	for index, route := range config.Routes {
		if strings.TrimSpace(route.Name) == "" {
			return fmt.Errorf("routes[%d].name must be non-empty", index)
		}
		if len(route.Checks) == 0 {
			return fmt.Errorf("routes[%d].checks must be non-empty", index)
		}
		for checkIndex, check := range route.Checks {
			if strings.TrimSpace(check.Action) == "" && strings.TrimSpace(check.Outcome) == "" {
				return fmt.Errorf("routes[%d].checks[%d].action or outcome must be non-empty", index, checkIndex)
			}
			if strings.TrimSpace(check.ReasonCode) == "" {
				return fmt.Errorf("routes[%d].checks[%d].reason_code must be non-empty", index, checkIndex)
			}
			if strings.TrimSpace(check.ReasonMessage) == "" {
				return fmt.Errorf("routes[%d].checks[%d].reason_message must be non-empty", index, checkIndex)
			}
		}
	}

	return nil
}

func evaluateWorkflowRoute(route workflowRouteCheckConfig, task integration.CanonicalTask) RouteCheckResult {
	taskFeatures := taskFeatureSet(task)
	reasons := make([]DecisionReason, 0)
	for _, feature := range normalizeFeatures(append(route.HasFeatures, route.HasLabels...)) {
		if _, ok := taskFeatures[feature]; !ok {
			reasons = append(reasons, DecisionReason{
				Code:    "required_feature_missing",
				Message: fmt.Sprintf("У задачи отсутствует обязательный признак %q.", feature),
			})
		}
	}
	for _, feature := range normalizeFeatures(append(route.MissingFeatures, route.MissingLabels...)) {
		if _, ok := taskFeatures[feature]; ok {
			reasons = append(reasons, DecisionReason{
				Code:    "forbidden_feature_present",
				Message: fmt.Sprintf("У задачи присутствует запрещающий признак %q.", feature),
			})
		}
	}

	var status RouteCheckStatus = RouteCheckStatusPassed
	if len(reasons) != 0 {
		status = RouteCheckStatusFailed
	}

	return RouteCheckResult{
		Name:    firstNonEmpty(strings.TrimSpace(route.Name), "labels"),
		Status:  status,
		Reasons: reasons,
	}
}

func taskFeatureSet(task integration.CanonicalTask) map[string]struct{} {
	features := normalizeFeatures(task.Traits)
	set := make(map[string]struct{}, len(features))
	for _, feature := range features {
		set[feature] = struct{}{}
	}

	return set
}

func normalizeLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}

	result := make([]string, 0, len(labels))
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}

	return result
}

func normalizeFeatures(features []string) []string {
	return normalizeLabels(features)
}

func normalizeRouteConstraints(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}
