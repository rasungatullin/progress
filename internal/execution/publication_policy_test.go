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

	body := pullRequestBody(state)
	if !strings.Contains(body, "Задача: #143") || !strings.Contains(body, "Ссылка на задачу: https://github.com/rasungatullin/progress/issues/143") {
		t.Fatalf("expected compact task reference, got %q", body)
	}
	if strings.Contains(body, "Полное описание задачи") || strings.Contains(body, "Изменения:") {
		t.Fatalf("compact policy must omit full context, got %q", body)
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

	comments := reviewRemarkComments(state)
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
