package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/integration"
)

const workflowConfigRelativePath = ".progress/decision/workflows.json"

type workflowConfigFile struct {
	Defaults workflowRouteConfig   `json:"defaults"`
	Routes   []workflowRouteConfig `json:"routes"`
}

type workflowRouteConfig struct {
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Step            string   `json:"step"`
	Action          string   `json:"action"`
	Profile         string   `json:"profile"`
	HasFeatures     []string `json:"has_features"`
	MissingFeatures []string `json:"missing_features"`
	HasLabels       []string `json:"has_labels"`
	MissingLabels   []string `json:"missing_labels"`
	ExpectedResult  string   `json:"expected_result"`
	Constraints     []string `json:"constraints"`
	ReasonCode      string   `json:"reason_code"`
	ReasonMessage   string   `json:"reason_message"`
}

type selectedWorkflowRoute struct {
	Step           string
	Action         string
	Profile        string
	ExpectedResult string
	Constraints    []string
	ReasonCode     string
	ReasonMessage  string
	Route          ProcessingRoute
	Checks         []RouteCheckResult
}

func (s *Service) selectWorkflowRoute(ctx context.Context, task integration.CanonicalTask) (selectedWorkflowRoute, error) {
	config, err := s.loadWorkflowConfig(ctx)
	if err != nil {
		return selectedWorkflowRoute{}, err
	}

	selected := selectedWorkflowRoute{
		Action:         strings.TrimSpace(config.Defaults.Action),
		Step:           strings.TrimSpace(config.Defaults.Step),
		Profile:        strings.TrimSpace(config.Defaults.Profile),
		ExpectedResult: strings.TrimSpace(config.Defaults.ExpectedResult),
		Constraints:    normalizeRouteConstraints(config.Defaults.Constraints),
		ReasonCode:     strings.TrimSpace(config.Defaults.ReasonCode),
		ReasonMessage:  strings.TrimSpace(config.Defaults.ReasonMessage),
		Route: ProcessingRoute{
			Name:        firstNonEmpty(strings.TrimSpace(config.Defaults.Name), "default"),
			Title:       firstNonEmpty(strings.TrimSpace(config.Defaults.Title), "Маршрут по умолчанию"),
			Description: strings.TrimSpace(config.Defaults.Description),
		},
		Checks: []RouteCheckResult{{
			Name:   "default-route",
			Status: RouteCheckStatusPassed,
			Reasons: []DecisionReason{{
				Code:    "default_route_available",
				Message: "Маршрут по умолчанию доступен для задачи.",
			}},
		}},
	}
	for index, route := range config.Routes {
		check := evaluateWorkflowRoute(route, task)
		if check.Status != RouteCheckStatusPassed {
			continue
		}

		selected = selectedWorkflowRoute{
			Action:         strings.TrimSpace(route.Action),
			Step:           strings.TrimSpace(route.Step),
			Profile:        strings.TrimSpace(route.Profile),
			ExpectedResult: strings.TrimSpace(route.ExpectedResult),
			Constraints:    normalizeRouteConstraints(route.Constraints),
			ReasonCode:     strings.TrimSpace(route.ReasonCode),
			ReasonMessage:  strings.TrimSpace(route.ReasonMessage),
			Route: ProcessingRoute{
				Name:        firstNonEmpty(strings.TrimSpace(route.Name), fmt.Sprintf("route-%d", index+1)),
				Title:       firstNonEmpty(strings.TrimSpace(route.Title), strings.TrimSpace(route.Step)),
				Description: strings.TrimSpace(route.Description),
			},
			Checks: []RouteCheckResult{check},
		}
		break
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
	if err := validateWorkflowConfig(config); err != nil {
		return workflowConfigFile{}, fmt.Errorf("invalid decision workflow config %s: %w", configPath, err)
	}

	return config, nil
}

func validateWorkflowConfig(config workflowConfigFile) error {
	if strings.TrimSpace(config.Defaults.Step) == "" {
		return fmt.Errorf("defaults.step must be non-empty")
	}
	if strings.TrimSpace(config.Defaults.Profile) == "" {
		return fmt.Errorf("defaults.profile must be non-empty")
	}
	if strings.TrimSpace(config.Defaults.ReasonCode) == "" {
		return fmt.Errorf("defaults.reason_code must be non-empty")
	}
	if strings.TrimSpace(config.Defaults.ReasonMessage) == "" {
		return fmt.Errorf("defaults.reason_message must be non-empty")
	}

	for index, route := range config.Routes {
		if strings.TrimSpace(route.Step) == "" {
			return fmt.Errorf("routes[%d].step must be non-empty", index)
		}
		if strings.TrimSpace(route.Profile) == "" {
			return fmt.Errorf("routes[%d].profile must be non-empty", index)
		}
		if len(normalizeLabels(route.HasLabels)) == 0 && len(normalizeLabels(route.MissingLabels)) == 0 {
			if len(normalizeFeatures(route.HasFeatures)) == 0 && len(normalizeFeatures(route.MissingFeatures)) == 0 {
				return fmt.Errorf("routes[%d] must declare at least one matcher", index)
			}
		}
		if strings.TrimSpace(route.ReasonCode) == "" {
			return fmt.Errorf("routes[%d].reason_code must be non-empty", index)
		}
		if strings.TrimSpace(route.ReasonMessage) == "" {
			return fmt.Errorf("routes[%d].reason_message must be non-empty", index)
		}
	}

	return nil
}

func evaluateWorkflowRoute(route workflowRouteConfig, task integration.CanonicalTask) RouteCheckResult {
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
