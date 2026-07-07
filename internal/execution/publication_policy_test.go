package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/methodology"
)

func TestSelectTextPublicationPolicyByTargetAndStep(t *testing.T) {
	t.Parallel()

	policyPayload := []byte(`{
		"targets": ["review-response"],
		"steps": ["publish-review-responses"],
		"format": ["no-heading"],
		"exclude": ["status", "resolved", "unresolved"],
		"fallback": "use-current-publication"
	}`)
	catalog := methodology.Catalog{Entities: []methodology.Entity{{Kind: publicationPolicyEntityKind, Name: "review-response", TargetContour: "execution", Payload: policyPayload}}}
	action := model.Action{Name: ActionApplyReviewComments, Operations: []model.OperationSpec{{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses}}}

	policies := selectTextPublicationPolicies(catalog, action)
	policy, ok := policyForPublication(policies, publicationTargetReviewResponse, ActionApplyReviewComments, OperationKindPublishReviewResponses)
	if !ok {
		t.Fatal("expected publication policy")
	}
	if !policy.NoHeading || !policy.HideStatus {
		t.Fatalf("unexpected policy flags: %#v", policy)
	}
	if _, ok := policyForPublication(policies, publicationTargetReviewRemark, OperationKindPublishReviewResponses); ok {
		t.Fatal("policy must not match another target")
	}
}

func TestSelectTextPublicationPolicySkipsAnotherContour(t *testing.T) {
	t.Parallel()

	catalog := methodology.Catalog{Entities: []methodology.Entity{{
		Kind:          publicationPolicyEntityKind,
		Name:          "decision-policy",
		TargetContour: "decision",
		Payload:       []byte(`{"targets":["review-response"],"steps":["publish-review-responses"],"exclude":["status"]}`),
	}}}
	action := model.Action{Name: ActionApplyReviewComments, Operations: []model.OperationSpec{{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses}}}

	policies := selectTextPublicationPolicies(catalog, action)
	if len(policies) != 0 {
		t.Fatalf("policy for another contour must be ignored: %#v", policies)
	}
}

func TestPolicyInEntityContourWins(t *testing.T) {
	t.Parallel()

	policyPayload := []byte(`{
		"targets": ["review-response"],
		"steps": ["publish-review-responses"],
		"target_contour": "decision"
	}`)
	catalog := methodology.Catalog{Entities: []methodology.Entity{{
		Kind:          publicationPolicyEntityKind,
		Name:          "review-response",
		TargetContour: "execution",
		Payload:       policyPayload,
	}}}
	action := model.Action{Name: ActionApplyReviewComments, Operations: []model.OperationSpec{{Name: OperationKindPublishReviewResponses, Kind: OperationKindPublishReviewResponses}}}

	policies := selectTextPublicationPolicies(catalog, action)
	if len(policies) != 1 {
		t.Fatalf("expected policy to be selected by entity target_contour, got %#v", policies)
	}
	if policies[0].TargetContour != "execution" {
		t.Fatalf("expected target contour from entity to win, got %q", policies[0].TargetContour)
	}
}

func TestPolicyForStatePublicationMatchesOperationNameAndActionOperation(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action: model.Action{
			Name:       ActionApplyReviewComments,
			Operations: []model.OperationSpec{{Name: "custom-review-response-publication", Kind: OperationKindPublishReviewResponses}},
		},
		policies: []textPublicationPolicy{{
			Targets: []string{publicationTargetReviewResponse},
			Steps:   []string{"custom-review-response-publication"},
		}},
	}

	if _, ok := policyForStatePublication(state, publicationTargetReviewResponse, "custom-review-response-publication"); !ok {
		t.Fatal("expected policy matched by operation name")
	}
}

func TestBuildDirectiveAppendsPublicationPolicyContext(t *testing.T) {
	t.Parallel()

	service := &Service{
		methodology: func(context.Context, methodology.CatalogRequest) (methodology.CatalogSnapshot, error) {
			return methodology.CatalogSnapshot{Catalog: methodology.Catalog{Entities: []methodology.Entity{{
				Kind:    publicationPolicyEntityKind,
				Name:    "merge-request-description",
				Payload: []byte(`{"targets":["merge-request-description"],"steps":["start-implementation-pr"],"format":["short","task-link"]}`),
			}}}}, nil
		},
	}
	state := &operationExecution{
		in:         invocation{Launch: launchSpec{StructuredInput: &StructuredInput{Task: "Выполнить задачу."}}},
		assignment: &ExecutionAssignment{StructuredInput: &StructuredInput{Task: "Выполнить задачу."}},
		action: model.Action{
			Name:              ActionStartImplementationPR,
			RequiresSynthesis: true,
			Operations:        []model.OperationSpec{{Name: OperationKindBuildDirective, Kind: OperationKindBuildDirective}},
		},
		allocation: allocation{Runner: "opencode", Model: "openai/gpt-5.5"},
		tracker:    newOperationTracker(model.Action{Operations: []model.OperationSpec{{Name: OperationKindBuildDirective, Kind: OperationKindBuildDirective}}}),
	}

	err := (builtinOperationExecutor{service: service}).buildDirective(context.Background(), state, OperationKindBuildDirective)
	if err != nil {
		t.Fatalf("build directive: %v", err)
	}
	if len(state.in.Launch.StructuredInput.OperationalContext) != 1 {
		t.Fatalf("expected publication policy context: %#v", state.in.Launch.StructuredInput)
	}
	contextItem := state.in.Launch.StructuredInput.OperationalContext[0]
	if contextItem.Title != "Политика внешних текстовых публикаций" || !strings.Contains(contextItem.Body, "merge-request-description") {
		t.Fatalf("unexpected policy context: %#v", contextItem)
	}
}

func TestPullRequestBodyUsesCompactTaskLinkPolicy(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action: model.Action{Name: ActionStartImplementationPR},
		assignment: &ExecutionAssignment{CanonicalTask: &ObjectRef{
			Type:       "task",
			Number:     143,
			URL:        "https://github.com/rasungatullin/progress/issues/143",
			Attributes: map[string]string{"body": "Полное описание задачи."},
		}},
		policies: []textPublicationPolicy{{Targets: []string{publicationTargetMergeRequestDescription}, Steps: []string{ActionStartImplementationPR, OperationKindPublishMergeRequest}, TaskLinkOnly: true}},
		result: LaunchResult{StructuredOutput: &StructuredOutput{
			Summary: "Синтез выполнен.",
			Changes: []StructuredChange{{Summary: "Изменение."}},
		}},
	}

	body := pullRequestBody(state, OperationKindPublishMergeRequest)
	if !strings.Contains(body, "Задача: #143") || !strings.Contains(body, "Ссылка на задачу: https://github.com/rasungatullin/progress/issues/143") {
		t.Fatalf("expected compact task reference, got %q", body)
	}
	if strings.Contains(body, "Полное описание задачи") || strings.Contains(body, "Изменения:") {
		t.Fatalf("compact policy must omit full context, got %q", body)
	}
}

func TestPullRequestBodyForRefAppliesCompactPolicyToExistingBody(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action: model.Action{Name: ActionStartImplementationPR},
		assignment: &ExecutionAssignment{CanonicalTask: &ObjectRef{
			Type:       "task",
			Number:     143,
			URL:        "https://github.com/rasungatullin/progress/issues/143",
			Attributes: map[string]string{"body": "Полное описание задачи."},
		}},
		policies: []textPublicationPolicy{{Targets: []string{publicationTargetMergeRequestDescription}, Steps: []string{ActionStartImplementationPR, OperationKindPublishMergeRequest}, TaskLinkOnly: true}},
	}

	body := pullRequestBodyForRef(state, OperationKindPublishMergeRequest, pullRequestRef{Body: "Полное описание задачи."})
	if strings.Contains(body, "Полное описание задачи") {
		t.Fatalf("policy must replace existing full body with compact reference, got %q", body)
	}
	if !strings.Contains(body, "Задача: #143") || !strings.Contains(body, "Ссылка на задачу: https://github.com/rasungatullin/progress/issues/143") {
		t.Fatalf("expected compact task reference, got %q", body)
	}
}

func TestReviewRemarkUsesPolicyWithoutArtificialHeading(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action:   model.Action{Name: ActionReviewPullRequest},
		policies: []textPublicationPolicy{{Targets: []string{publicationTargetReviewRemark}, Steps: []string{ActionReviewPullRequest, OperationKindPublishReviewRemarks}, NoHeading: true, OptionalHeading: true}},
		result: LaunchResult{StructuredOutput: &StructuredOutput{Remarks: []StructuredRemark{{
			ID:    "remark-1",
			Title: "",
			Body:  "Проверить обработку ошибки.",
		}}}},
	}

	comments := reviewRemarkComments(state, OperationKindPublishReviewRemarks)
	if len(comments) != 1 {
		t.Fatalf("expected one comment, got %#v", comments)
	}
	if strings.Contains(comments[0].Body, "## Замечание ревизии") || strings.Contains(comments[0].Body, "Заголовок:") {
		t.Fatalf("policy must omit artificial heading, got %q", comments[0].Body)
	}
	if !strings.Contains(comments[0].Body, "Проверить обработку ошибки.") {
		t.Fatalf("remark body lost: %q", comments[0].Body)
	}
}

func TestReviewRemarkConclusionPolicyOmitsStatus(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action:   model.Action{Name: ActionReviewPullRequest},
		policies: []textPublicationPolicy{{Targets: []string{publicationTargetReviewRemark}, Steps: []string{ActionReviewPullRequest, OperationKindPublishReviewRemarks}, NoHeading: true, HideStatus: true}},
		result: LaunchResult{StructuredOutput: &StructuredOutput{Conclusion: &StructuredConclusion{
			Status:  "approve",
			Summary: "Замечаний нет.",
		}}},
	}

	comments := reviewRemarkComments(state, OperationKindPublishReviewRemarks)
	if len(comments) != 1 {
		t.Fatalf("expected one conclusion comment, got %#v", comments)
	}
	if strings.Contains(comments[0].Body, "approve") || strings.Contains(comments[0].Body, "## Заключение") {
		t.Fatalf("policy must omit service status and heading, got %q", comments[0].Body)
	}
	if !strings.Contains(comments[0].Body, "Замечаний нет.") {
		t.Fatalf("conclusion summary lost: %q", comments[0].Body)
	}
}

func TestReviewResponsePolicyOmitsStatusField(t *testing.T) {
	t.Parallel()

	body := reviewResponseCommentBodyWithPolicy(StructuredResponse{
		RemarkID: "remark-1",
		Status:   "resolved",
		Summary:  "Исправлено.",
		Body:     "Добавлена проверка.",
	}, textPublicationPolicy{NoHeading: true, HideStatus: true})

	if strings.Contains(body, "## Ответ") || strings.Contains(body, "Состояние:") || strings.Contains(body, "resolved") {
		t.Fatalf("policy must omit heading and service status, got %q", body)
	}
	if !strings.Contains(body, "Исправлено.") || !strings.Contains(body, "Добавлена проверка.") {
		t.Fatalf("response content lost: %q", body)
	}
}

func TestReviewResponsePolicySkipsStatusOnlyBody(t *testing.T) {
	t.Parallel()

	body := reviewResponseCommentBodyWithPolicy(StructuredResponse{
		RemarkID: "remark-1",
		Status:   "resolved",
	}, textPublicationPolicy{NoHeading: true, HideStatus: true})

	if body != "" {
		t.Fatalf("status-only response must be skipped after policy cleanup, got %q", body)
	}
}

func TestPolicyForStatePublicationUsesOnlyCurrentOperation(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action: model.Action{
			Name: ActionApplyReviewComments,
			Operations: []model.OperationSpec{
				{Name: "publish-review-remarks", Kind: OperationKindPublishReviewRemarks},
				{Name: "publish-review-remarks-secondary", Kind: OperationKindPublishReviewRemarks},
			},
		},
		policies: []textPublicationPolicy{
			{
				Targets:  []string{publicationTargetReviewRemark},
				Steps:    []string{"publish-review-remarks"},
				NoHeading: false,
			},
			{
				Targets:  []string{publicationTargetReviewRemark},
				Steps:    []string{"publish-review-remarks-secondary"},
				NoHeading: true,
			},
		},
	}

	policy, ok := policyForStatePublication(state, publicationTargetReviewRemark, "publish-review-remarks")
	if !ok {
		t.Fatal("expected policy matched for current operation")
	}
	if policy.NoHeading {
		t.Fatalf("current operation should not inherit secondary operation policy: %#v", policy)
	}
}

func TestPolicyForStatePublicationIgnoresSecondaryOperationByKindMatch(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action: model.Action{
			Name: ActionApplyReviewComments,
			Operations: []model.OperationSpec{
				{Name: "publish-review-remarks", Kind: OperationKindPublishReviewRemarks},
				{Name: "publish-review-remarks-secondary", Kind: OperationKindPublishReviewRemarks},
			},
		},
		policies: []textPublicationPolicy{
			{
				Targets:   []string{publicationTargetReviewRemark},
				Steps:     []string{"publish-review-remarks", string(OperationKindPublishReviewRemarks)},
				NoHeading: false,
			},
			{
				Targets:   []string{publicationTargetReviewRemark},
				Steps:     []string{string(OperationKindPublishReviewRemarks)},
				NoHeading: true,
			},
		},
	}

	policy, ok := policyForStatePublication(state, publicationTargetReviewRemark, "publish-review-remarks", string(OperationKindPublishReviewRemarks))
	if !ok {
		t.Fatal("expected policy matched for current operation")
	}
	if policy.NoHeading {
		t.Fatalf("kind-only policy from secondary operation must not override current operation policy: %#v", policy)
	}
}

func TestPolicyForStatePublicationPrefersSpecificStepOverGeneralPolicy(t *testing.T) {
	t.Parallel()

	state := &operationExecution{
		action: model.Action{
			Name: ActionApplyReviewComments,
			Operations: []model.OperationSpec{
				{Name: "publish-review-remarks", Kind: OperationKindPublishReviewRemarks},
			},
		},
		policies: []textPublicationPolicy{
			{
				Targets:   []string{publicationTargetReviewRemark},
				NoHeading: true,
			},
			{
				Targets:   []string{publicationTargetReviewRemark},
				Steps:     []string{"publish-review-remarks"},
				NoHeading: false,
			},
		},
	}

	policy, ok := policyForStatePublication(state, publicationTargetReviewRemark, "publish-review-remarks")
	if !ok {
		t.Fatal("expected policy matched for current operation")
	}
	if policy.NoHeading {
		t.Fatalf("specific operation policy should override general publication policy: %#v", policy)
	}
}

func TestLoadTextPublicationPoliciesUsesActionCatalogRoot(t *testing.T) {
	t.Parallel()

	marker := "load-from-action-root"
	var loadedRoot string
	service := &Service{
		methodology: func(_ context.Context, request methodology.CatalogRequest) (methodology.CatalogSnapshot, error) {
			loadedRoot = request.RepoRoot
			return methodology.CatalogSnapshot{Catalog: methodology.Catalog{Entities: []methodology.Entity{{
				Kind: publicationPolicyEntityKind,
				Name: marker,
				Payload: []byte(`{"targets":["task-comment"],"steps":["review-pull-request"],"format":["short"]}`),
			}}}}, nil
		},
	}
	state := &operationExecution{
		action:          model.Action{Name: ActionReviewPullRequest},
		actionCatalogRoot: "/controller/repo",
		workplace:        workplace{RepositoryRoot: "/worktree/repo"},
	}
	policies := service.loadTextPublicationPolicies(context.Background(), state)
	if loadedRoot != "/controller/repo" {
		t.Fatalf("expected action catalog root to be used, got %q", loadedRoot)
	}
	if len(policies) != 1 {
		t.Fatalf("expected one publication policy loaded from action catalog, got %#v", policies)
	}
	if policies[0].Name != marker {
		t.Fatalf("expected policy from action catalog, got %q", policies[0].Name)
	}
}
