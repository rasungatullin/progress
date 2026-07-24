package execution

import (
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/integration"
)

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
	}, PullRequestStatus: &integration.PullRequestStatus{
		System:     "legacy",
		Repository: "wrong/repository",
		Number:     999,
		State:      "WRONG",
		URL:        "https://example.invalid/pull/999",
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
	if !pullRequestAlreadyAvailable(response) {
		t.Fatal("canonical operation result with URL must make the published merge request available")
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
