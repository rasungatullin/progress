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
	Step          string   `json:"step"`
	Profile       string   `json:"profile"`
	HasLabels     []string `json:"has_labels"`
	MissingLabels []string `json:"missing_labels"`
	ReasonCode    string   `json:"reason_code"`
	ReasonMessage string   `json:"reason_message"`
}

type selectedWorkflowRoute struct {
	Step          string
	Profile       string
	ReasonCode    string
	ReasonMessage string
}

func (s *Service) selectWorkflowRoute(ctx context.Context, issue *integration.TrackerIssue) (selectedWorkflowRoute, error) {
	config, err := s.loadWorkflowConfig(ctx)
	if err != nil {
		return selectedWorkflowRoute{}, err
	}

	selected := selectedWorkflowRoute{
		Step:          strings.TrimSpace(config.Defaults.Step),
		Profile:       strings.TrimSpace(config.Defaults.Profile),
		ReasonCode:    strings.TrimSpace(config.Defaults.ReasonCode),
		ReasonMessage: strings.TrimSpace(config.Defaults.ReasonMessage),
	}
	for _, route := range config.Routes {
		if !workflowRouteMatchesIssue(route, issue) {
			continue
		}

		selected = selectedWorkflowRoute{
			Step:          strings.TrimSpace(route.Step),
			Profile:       strings.TrimSpace(route.Profile),
			ReasonCode:    strings.TrimSpace(route.ReasonCode),
			ReasonMessage: strings.TrimSpace(route.ReasonMessage),
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
			return fmt.Errorf("routes[%d] must declare at least one matcher", index)
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

func workflowRouteMatchesIssue(route workflowRouteConfig, issue *integration.TrackerIssue) bool {
	issueLabels := issueLabelSet(issue)
	for _, label := range normalizeLabels(route.HasLabels) {
		if _, ok := issueLabels[label]; !ok {
			return false
		}
	}
	for _, label := range normalizeLabels(route.MissingLabels) {
		if _, ok := issueLabels[label]; ok {
			return false
		}
	}

	return true
}

func issueLabelSet(issue *integration.TrackerIssue) map[string]struct{} {
	labels := normalizeLabels(nil)
	if issue != nil {
		labels = normalizeLabels(issue.Labels)
	}

	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		set[label] = struct{}{}
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
