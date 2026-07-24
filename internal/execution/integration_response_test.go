package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/integration"
)

func TestExecuteIntegrationPreservesMergeRequestCreateInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response integration.Response
		number   int
		url      string
	}{
		{
			name: "результат операции",
			response: integration.Response{OperationResult: &integration.OperationResult{
				System:     "github",
				ObjectType: "merge-request",
				Operation:  "create",
				Status:     "ok",
				ExternalID: "41",
				URL:        "https://github.com/owner/name/pull/41",
			}},
			number: 41,
			url:    "https://github.com/owner/name/pull/41",
		},
		{
			name: "частичный канонический объект",
			response: integration.Response{MergeRequest: &integration.MergeRequest{
				System: "github",
				Number: 42,
				State:  "OPEN",
				URL:    "https://github.com/owner/name/pull/42",
			}},
			number: 42,
			url:    "https://github.com/owner/name/pull/42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			operation := mergeRequestCreateIntegrationOperation()
			state := mergeRequestCreateIntegrationState(operation)
			service := &Service{integrations: &stubIntegrationExecutor{execute: func(_ context.Context, request integration.Request) (integration.Response, error) {
				if request.Repository != "owner/name" || request.Base != "main" || request.Head != "feature/canonical" ||
					request.Title != "Канонический запрос" || request.Body != "Описание запроса" {
					t.Fatalf("unexpected create request: %#v", request)
				}
				return tt.response, nil
			}}}

			if err := (builtinOperationExecutor{service: service}).executeIntegration(context.Background(), state, operation); err != nil {
				t.Fatalf("execute integration: %v", err)
			}

			mergeRequest, ok := state.data["merge_request"].(integration.MergeRequest)
			if !ok {
				t.Fatalf("merge request output is missing: %#v", state.data["merge_request"])
			}
			if mergeRequest.Repository != "owner/name" || mergeRequest.BaseRef != "main" ||
				mergeRequest.HeadRef != "feature/canonical" || mergeRequest.Title != "Канонический запрос" ||
				mergeRequest.Body != "Описание запроса" || mergeRequest.Number != tt.number || mergeRequest.URL != tt.url {
				t.Fatalf("create input must complete the integration response: %#v", mergeRequest)
			}

			invocation, ok := state.data["invocation"].(model.Invocation)
			if !ok || invocation.Assignment == nil || len(invocation.Assignment.RelatedObjects) != 1 {
				t.Fatalf("updated invocation is missing: %#v", state.data["invocation"])
			}
			object := invocation.Assignment.RelatedObjects[0]
			if object.Repository != "owner/name" || object.Number != tt.number || object.Title != "Канонический запрос" ||
				object.URL != tt.url || object.Attributes["base_ref"] != "main" ||
				object.Attributes["head_ref"] != "feature/canonical" || object.Attributes["body"] != "Описание запроса" ||
				object.Attributes["custom"] != "сохранить" {
				t.Fatalf("invocation must preserve complete merge request data: %#v", object)
			}
		})
	}
}

func TestExecuteIntegrationDoesNotAcceptCanonicalPayloadWhenExecutorReturnsError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response integration.Response
	}{
		{
			name: "отказ и запрос на слияние",
			response: integration.Response{
				Status:       "failed",
				Failure:      &integration.Failure{Kind: "external-failure", Message: "GitHub отказал в операции"},
				MergeRequest: &integration.MergeRequest{Number: 17, URL: "https://github.com/owner/name/pull/17"},
			},
		},
		{
			name: "неуспешный результат операции с URL",
			response: integration.Response{OperationResult: &integration.OperationResult{
				ObjectType: "merge-request", Operation: "create", Status: "failed", URL: "https://github.com/owner/name/pull/17",
			}},
		},
		{
			name: "вложенный отказ результата операции",
			response: integration.Response{OperationResult: &integration.OperationResult{
				ObjectType: "merge-request", Operation: "create", Status: "ok", URL: "https://github.com/owner/name/pull/17",
				Failure: &integration.Failure{Kind: "external-failure", Message: "GitHub отказал в операции"},
			}},
		},
		{
			name: "частичный ответ",
			response: integration.Response{
				Status: "partial", Partial: true,
				MergeRequest: &integration.MergeRequest{Number: 17, URL: "https://github.com/owner/name/pull/17"},
			},
		},
		{
			name: "неуспешный статус ответа",
			response: integration.Response{
				Status:       "failed",
				MergeRequest: &integration.MergeRequest{Number: 17, URL: "https://github.com/owner/name/pull/17"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			operation := mergeRequestCreateIntegrationOperation()
			state := mergeRequestCreateIntegrationState(operation)
			service := &Service{integrations: &stubIntegrationExecutor{execute: func(context.Context, integration.Request) (integration.Response, error) {
				return tt.response, errors.New("GitHub merge request create failed")
			}}}

			err := (builtinOperationExecutor{service: service}).executeIntegration(context.Background(), state, operation)
			if err == nil {
				t.Fatalf("executor error must not be accepted: %#v", tt.response)
			}
			if _, exists := state.data["merge_request"]; exists {
				t.Fatalf("failed integration must not publish a merge request: %#v", state.data["merge_request"])
			}
		})
	}
}

func mergeRequestCreateIntegrationOperation() model.OperationSpec {
	return model.OperationSpec{
		Name:     "publish-merge-request",
		Type:     model.OperationType(OperationTypeIntegration),
		Kind:     "repo.merge-request.create",
		Required: true,
		In: model.OperationMap{
			"repository": {Ref: "data.repository_name"},
			"base_ref":   {Ref: "data.base_ref"},
			"head_ref":   {Ref: "data.head_ref"},
			"title":      {Ref: "data.title"},
			"body":       {Ref: "data.body"},
		},
		Out: model.OperationMap{
			"merge_request": {Ref: "data.merge_request"},
			"invocation":    {Ref: "data.invocation"},
		},
	}
}

func mergeRequestCreateIntegrationState(operation model.OperationSpec) *operationExecution {
	return &operationExecution{
		in: model.Invocation{Assignment: &model.ExecutionAssignment{RelatedObjects: []model.ObjectRef{{
			Type:       "merge-request",
			Attributes: map[string]string{"custom": "сохранить"},
		}}}},
		data: map[string]any{
			"repository_name": "owner/name",
			"base_ref":        "main",
			"head_ref":        "feature/canonical",
			"title":           "Канонический запрос",
			"body":            "Описание запроса",
		},
		tracker: newOperationTracker(model.Action{Operations: []model.OperationSpec{operation}}),
	}
}

func TestWriteIntegrationResponsePublishesCanonicalFields(t *testing.T) {
	t.Parallel()

	task := &integration.CanonicalTask{System: "tracker", ID: "TASK-17", Title: "Каноническая задача"}
	tasks := []integration.CanonicalTask{{System: "tracker", ID: "TASK-18"}}
	comments := []integration.TaskComment{{System: "tracker", TaskID: "TASK-17", ExternalID: "comment-1"}}
	repository := &integration.Repository{System: "github", FullName: "owner/name"}
	mergeRequest := &integration.MergeRequest{System: "github", Repository: "owner/name", Number: 42}
	mergeRequests := []integration.MergeRequest{{System: "github", Repository: "owner/name", Number: 43}}
	remarks := []integration.ReviewRemark{{System: "github", Repository: "owner/name", MergeRequestNumber: 42, ExternalID: "remark-1"}}
	operationResult := &integration.OperationResult{System: "github", ObjectType: "merge-request", Operation: "create", Status: "ok", ExternalID: "42"}
	operation := model.OperationSpec{Out: model.OperationMap{
		"task":             {Ref: "data.task"},
		"tasks":            {Ref: "data.tasks"},
		"task_comments":    {Ref: "data.task_comments"},
		"repository":       {Ref: "data.repository"},
		"merge_request":    {Ref: "data.merge_request"},
		"merge_requests":   {Ref: "data.merge_requests"},
		"review_remarks":   {Ref: "data.review_remarks"},
		"operation_result": {Ref: "data.operation_result"},
	}}
	state := &operationExecution{data: map[string]any{}}

	writeIntegrationResponse(state, operation, integration.Response{
		Task:            task,
		Tasks:           tasks,
		TaskComments:    comments,
		Repository:      repository,
		MergeRequest:    mergeRequest,
		MergeRequests:   mergeRequests,
		ReviewRemarks:   remarks,
		OperationResult: operationResult,
	})

	if state.data["task"] != task {
		t.Fatalf("canonical task must be written without a compatible copy: %#v", state.data["task"])
	}
	if got := state.data["tasks"].([]integration.CanonicalTask); len(got) != 1 || got[0].ID != "TASK-18" {
		t.Fatalf("unexpected canonical tasks: %#v", got)
	}
	if got := state.data["task_comments"].([]integration.TaskComment); len(got) != 1 || got[0].ExternalID != "comment-1" {
		t.Fatalf("unexpected canonical task comments: %#v", got)
	}
	if state.data["repository"] != repository {
		t.Fatalf("canonical repository must be written without a compatible copy: %#v", state.data["repository"])
	}
	if got := state.data["merge_request"].(integration.MergeRequest); got.Number != 42 {
		t.Fatalf("unexpected canonical merge request: %#v", got)
	}
	if got := state.data["merge_requests"].([]integration.MergeRequest); len(got) != 1 || got[0].Number != 43 {
		t.Fatalf("unexpected canonical merge requests: %#v", got)
	}
	if got := state.data["review_remarks"].([]integration.ReviewRemark); len(got) != 1 || got[0].ExternalID != "remark-1" {
		t.Fatalf("unexpected canonical review remarks: %#v", got)
	}
	if state.data["operation_result"] != operationResult {
		t.Fatalf("canonical operation result must be written without a compatible status: %#v", state.data["operation_result"])
	}
}

func TestWriteIntegrationResponsePreservesEmptyCanonicalCollections(t *testing.T) {
	t.Parallel()

	operation := model.OperationSpec{Out: model.OperationMap{
		"tasks":          {Ref: "data.tasks"},
		"task_comments":  {Ref: "data.task_comments"},
		"merge_requests": {Ref: "data.merge_requests"},
		"review_remarks": {Ref: "data.review_remarks"},
	}}
	state := &operationExecution{data: map[string]any{}}

	writeIntegrationResponse(state, operation, integration.Response{
		Tasks:         []integration.CanonicalTask{},
		TaskComments:  []integration.TaskComment{},
		MergeRequests: []integration.MergeRequest{},
		ReviewRemarks: []integration.ReviewRemark{},
	})

	if tasks, ok := state.data["tasks"].([]integration.CanonicalTask); !ok || tasks == nil || len(tasks) != 0 {
		t.Fatalf("empty canonical tasks must remain an explicit empty collection: %#v", state.data["tasks"])
	}
	if comments, ok := state.data["task_comments"].([]integration.TaskComment); !ok || comments == nil || len(comments) != 0 {
		t.Fatalf("empty canonical task comments must remain an explicit empty collection: %#v", state.data["task_comments"])
	}
	if mergeRequests, ok := state.data["merge_requests"].([]integration.MergeRequest); !ok || mergeRequests == nil || len(mergeRequests) != 0 {
		t.Fatalf("empty canonical merge requests must remain an explicit empty collection: %#v", state.data["merge_requests"])
	}
	if remarks, ok := state.data["review_remarks"].([]integration.ReviewRemark); !ok || remarks == nil || len(remarks) != 0 {
		t.Fatalf("empty canonical review remarks must remain an explicit empty collection: %#v", state.data["review_remarks"])
	}
}

func TestMergeRequestFromPublishResponseUsesCanonicalOperationResult(t *testing.T) {
	t.Parallel()

	response := integration.Response{OperationResult: &integration.OperationResult{
		System:     "github",
		ObjectType: "merge-request",
		Operation:  "create",
		Status:     "ok",
		ExternalID: "27",
		URL:        "https://github.com/owner/name/pull/27",
	}}

	mergeRequest := mergeRequestFromPublishResponse(response, pullRequestRef{
		Repository: "owner/name",
		Base:       "main",
		Head:       "feature/canonical",
		Title:      "Канонический результат",
		Body:       "Описание",
	})

	if mergeRequest.System != "github" || mergeRequest.Repository != "owner/name" || mergeRequest.Number != 27 ||
		mergeRequest.State != "OPEN" || mergeRequest.BaseRef != "main" || mergeRequest.HeadRef != "feature/canonical" ||
		mergeRequest.Title != "Канонический результат" || mergeRequest.Body != "Описание" ||
		mergeRequest.URL != "https://github.com/owner/name/pull/27" {
		t.Fatalf("unexpected merge request from canonical operation result: %#v", mergeRequest)
	}
	if summary := pullRequestPublishSummary(response); summary != "pull-request=27 url=https://github.com/owner/name/pull/27 status=ok" {
		t.Fatalf("unexpected canonical publication summary: %q", summary)
	}
}

func TestMergeRequestFromPublishResponseCompletesPartialCanonicalObject(t *testing.T) {
	t.Parallel()

	response := integration.Response{MergeRequest: &integration.MergeRequest{
		System: "github",
		Number: 31,
		State:  "OPEN",
		URL:    "https://github.com/owner/name/pull/31",
	}}

	mergeRequest := mergeRequestFromPublishResponse(response, pullRequestRef{
		Repository: "owner/name",
		Base:       "main",
		Head:       "feature/partial",
		Title:      "Частичный ответ",
		Body:       "Описание",
	})

	if mergeRequest.System != "github" || mergeRequest.Repository != "owner/name" || mergeRequest.Number != 31 ||
		mergeRequest.State != "OPEN" || mergeRequest.BaseRef != "main" || mergeRequest.HeadRef != "feature/partial" ||
		mergeRequest.Title != "Частичный ответ" || mergeRequest.Body != "Описание" ||
		mergeRequest.URL != "https://github.com/owner/name/pull/31" {
		t.Fatalf("partial canonical merge request must retain request context: %#v", mergeRequest)
	}
}
