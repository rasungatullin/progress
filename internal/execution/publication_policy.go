package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/methodology"
)

const (
	publicationPolicyEntityKind = "text-publication-policy"
	publicationPolicyContour    = "execution"

	publicationTargetTaskComment             = "task-comment"
	publicationTargetMergeRequestDescription = "merge-request-description"
	publicationTargetMergeRequestComment     = "merge-request-comment"
	publicationTargetReviewRemark            = "review-remark"
	publicationTargetReviewResponse          = "review-response"
)

type textPublicationPolicy struct {
	Name            string   `json:"name,omitempty"`
	Title           string   `json:"title,omitempty"`
	Description     string   `json:"description,omitempty"`
	TargetContour   string   `json:"target_contour,omitempty"`
	Targets         []string `json:"targets,omitempty"`
	Steps           []string `json:"steps,omitempty"`
	Format          []string `json:"format,omitempty"`
	Include         []string `json:"include,omitempty"`
	Exclude         []string `json:"exclude,omitempty"`
	Fallback        string   `json:"fallback,omitempty"`
	Compact         bool     `json:"-"`
	NoHeading       bool     `json:"-"`
	TaskLinkOnly    bool     `json:"-"`
	HideStatus      bool     `json:"-"`
	OptionalHeading bool     `json:"-"`
	MeaningfulTitle  bool     `json:"-"`
}

func selectTextPublicationPolicies(catalog methodology.Catalog, action model.Action) []textPublicationPolicy {
	steps := actionPolicySteps(action)
	policies := make([]textPublicationPolicy, 0)
	for _, entity := range catalog.Entities {
		if strings.TrimSpace(entity.Kind) != publicationPolicyEntityKind {
			continue
		}
		policy, err := textPublicationPolicyFromEntity(entity)
		if err != nil || !policyMatchesContour(policy) || !policyMatchesAnyStep(policy, steps) {
			continue
		}
		policies = append(policies, policy)
	}
	return policies
}

func policyMatchesContour(policy textPublicationPolicy) bool {
	contour := strings.TrimSpace(strings.ToLower(policy.TargetContour))
	return contour == "" || contour == publicationPolicyContour
}

func textPublicationPolicyFromEntity(entity methodology.Entity) (textPublicationPolicy, error) {
	var policy textPublicationPolicy
	if len(entity.Payload) != 0 {
		if err := json.Unmarshal(entity.Payload, &policy); err != nil {
			return textPublicationPolicy{}, fmt.Errorf("parse text publication policy %q: %w", entity.Name, err)
		}
	}
	policy.Name = strings.TrimSpace(firstNonEmptyTrimmed(policy.Name, entity.Name))
	policy.Title = strings.TrimSpace(firstNonEmptyTrimmed(policy.Title, entity.Title))
	policy.Description = strings.TrimSpace(firstNonEmptyTrimmed(policy.Description, entity.Description))
	policy.TargetContour = strings.TrimSpace(firstNonEmptyTrimmed(entity.TargetContour, policy.TargetContour))
	policy.Targets = normalizePolicyList(policy.Targets)
	policy.Steps = normalizePolicyList(policy.Steps)
	policy.Format = normalizePolicyList(policy.Format)
	policy.Include = normalizePolicyList(policy.Include)
	policy.Exclude = normalizePolicyList(policy.Exclude)
	policy.Fallback = strings.TrimSpace(policy.Fallback)
	policy.Compact = policyHas(policy.Format, "short") || policyHas(policy.Format, "compact") || policyHas(policy.Format, "brief")
	policy.NoHeading = policyHas(policy.Format, "no-heading") || policyHas(policy.Exclude, "heading")
	policy.TaskLinkOnly = policyHas(policy.Format, "task-link") || policyHas(policy.Include, "task-link")
	policy.HideStatus = policyHas(policy.Exclude, "status") || policyHas(policy.Exclude, "resolved") || policyHas(policy.Exclude, "unresolved")
	policy.OptionalHeading = policyHas(policy.Format, "optional-heading")
	policy.MeaningfulTitle = policyHas(policy.Include, "meaningful-review-title")
	return policy, nil
}

func policyForPublication(policies []textPublicationPolicy, target string, steps ...string) (textPublicationPolicy, bool) {
	target = strings.TrimSpace(target)
	normalizedSteps := normalizePolicyList(steps)
	for index := len(policies) - 1; index >= 0; index-- {
		policy := policies[index]
		if !policyMatchesTarget(policy, target) || !policyMatchesAnyStep(policy, normalizedSteps) {
			continue
		}
		return policy, true
	}
	return textPublicationPolicy{}, false
}

func policyMatchesTarget(policy textPublicationPolicy, target string) bool {
	if strings.TrimSpace(target) == "" || len(policy.Targets) == 0 {
		return true
	}
	return policyHas(policy.Targets, target)
}

func policyMatchesAnyStep(policy textPublicationPolicy, steps []string) bool {
	if len(policy.Steps) == 0 || len(steps) == 0 {
		return true
	}
	for _, step := range steps {
		if policyHas(policy.Steps, step) {
			return true
		}
	}
	return false
}

func actionPolicySteps(action model.Action) []string {
	steps := []string{strings.TrimSpace(action.Name)}
	for _, operation := range action.Operations {
		steps = append(steps, operationResultName(operation), string(operationKind(operation)))
	}
	return normalizePolicyList(steps)
}

func normalizePolicyList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
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

func policyHas(values []string, value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	for _, candidate := range values {
		if strings.TrimSpace(strings.ToLower(candidate)) == value {
			return true
		}
	}
	return false
}

func appendPublicationPolicyContext(input *StructuredInput, policies []textPublicationPolicy) {
	if input == nil || len(policies) == 0 {
		return
	}
	payload, err := json.Marshal(policies)
	if err != nil {
		return
	}
	input.OperationalContext = append(input.OperationalContext, StructuredContext{
		Title: "Политика внешних текстовых публикаций",
		Body:  string(payload),
	})
}

func (s *Service) loadTextPublicationPolicies(ctx context.Context, state *operationExecution) []textPublicationPolicy {
	if s == nil || s.methodology == nil || state == nil {
		return nil
	}
	repoRoot := strings.TrimSpace(state.actionCatalogRoot)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(state.workplace.RepositoryRoot)
	}
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(state.workplace.Name)
	}
	snapshot, err := s.methodology(ctx, methodology.CatalogRequest{RepoRoot: repoRoot})
	if err != nil {
		return nil
	}
	return selectTextPublicationPolicies(snapshot.Catalog, state.action)
}
