package integration

import (
	"io"
	"log"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestTmpSupportsConfiguredCommentOperation(t *testing.T) {
	service := NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{"repo": "repo-system"},
		Systems: map[string]model.IntegrationSystemConfig{
			"repo-system": {
				Type:             "script",
				IntegrationTypes: []string{"repo"},
				Operations: map[string]model.IntegrationOperationConfig{
					"repo.merge-request.comment.create": {Required: []string{"number", "body"}, Optional: []string{"repository", "path", "line", "side"}, Command: "unused"},
				},
			},
		},
	})
	state := service.systems["repo-system"]
	t.Logf("state type=%q registered=%t object=%q lenOps=%d", state.Type, state.Registered, state.IntegrationTypes, len(state.Operations))
	for name := range state.Operations {
		t.Logf("operation key=%q", name)
	}
	for i, operation := range state.Operations {
		_ = i
		_ = operation
	}
	t.Logf("systemSupports=%v", systemSupportsOperation(state, "repo", "comment", "create"))
	rq := Request{System: "repo-system", IntegrationType: "repo", Resource: "comment", ObjectType: "comment", Operation: "create", Extra: map[string]any{"number": float64(7), "body": "ok"}}
	_, err := service.Execute(nil, rq)
	t.Logf("execute err=%v", err)
}
