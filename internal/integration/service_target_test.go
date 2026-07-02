package integration

import (
	"context"
	"io"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
	"github.com/rasungatullin/progress/internal/logging"
)

func TestDispatchUsesDefaultSystemByIntegrationType(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{
			"tracker":    "github",
			"repository": "bitbucket",
			"messenger":  "telegram",
		},
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {
				Type:             "github",
				IntegrationTypes: []string{"tracker", "repository"},
			},
			"bitbucket": {
				Type:            "bitbucket",
				IntegrationType: "repository",
			},
			"telegram": {
				Type:            "telegram",
				IntegrationType: "messenger",
			},
		},
	})

	cases := []struct {
		name           string
		request        Request
		expectedSystem string
		expectedType   string
		expectedObject string
		expectedResult string
	}{
		{
			name:           "tracker",
			request:        Request{IntegrationType: "tracker", ObjectType: "task", Operation: "get"},
			expectedSystem: "github",
			expectedType:   "tracker",
			expectedObject: "task",
			expectedResult: "canonical-task",
		},
		{
			name:           "repository",
			request:        Request{IntegrationType: "repository", ObjectType: "repository", Operation: "get"},
			expectedSystem: "bitbucket",
			expectedType:   "repository",
			expectedObject: "repository",
			expectedResult: "canonical-repository",
		},
		{
			name:           "messenger",
			request:        Request{IntegrationType: "messenger", ObjectType: "message", Operation: "create"},
			expectedSystem: "telegram",
			expectedType:   "messenger",
			expectedObject: "message",
			expectedResult: "message",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			route, err := service.Dispatch(context.Background(), tc.request)
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if route.System != tc.expectedSystem {
				t.Fatalf("unexpected system: %q", route.System)
			}
			if route.IntegrationType != tc.expectedType {
				t.Fatalf("unexpected integration type: %q", route.IntegrationType)
			}
			if route.ObjectType != tc.expectedObject {
				t.Fatalf("unexpected object type: %q", route.ObjectType)
			}
			if route.ExpectedResult != tc.expectedResult {
				t.Fatalf("unexpected expected result: %q", route.ExpectedResult)
			}
			if !route.ProviderAvailable {
				t.Fatal("provider must be available")
			}
		})
	}
}

func TestDispatchInfersRepositoryTypeFromObjectBeforeSystemDefault(t *testing.T) {
	t.Parallel()

	service := NewServiceFromConfig(logging.New(io.Discard), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"github": {
				Type:             "github",
				IntegrationTypes: []string{"tracker", "repository"},
			},
		},
	})

	for _, request := range []Request{
		{System: "github", Resource: "repo", Operation: "get"},
		{System: "github", Resource: "pr", Operation: "get"},
	} {
		route, err := service.Dispatch(context.Background(), request)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if route.IntegrationType != model.IntegrationTypeRepository {
			t.Fatalf("expected repository integration type for resource %q, got %q", request.Resource, route.IntegrationType)
		}
	}
}
