package cli

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/integration/model"
	"github.com/spf13/cobra"
)

type contractCaptureProvider struct {
	request model.ProviderRequest
}

func (p *contractCaptureProvider) Execute(_ context.Context, request model.ProviderRequest) (model.Response, error) {
	p.request = request
	return model.Response{
		Status: model.ResponseStatusOK,
		Task:   &model.CanonicalTask{ID: request.ID, ExternalID: request.ID, Title: "issue"},
	}, nil
}

func TestIntegrationIssueCommandsUseOpaqueIDAndSystemSelection(t *testing.T) {
	provider := &contractCaptureProvider{}
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeIssue: "github"},
		Systems: map[string]model.IntegrationSystemConfig{
			"github":    {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
			"jira-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
		},
	})
	service.RegisterProvider("github", provider)
	service.RegisterProvider("jira-main", provider)

	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	for _, args := range [][]string{
		{"integration", "issue", "get", "--id", "123"},
		{"integration", "issue", "get", "--id", "ABC-123", "--system", "jira-main"},
	} {
		cmd := NewRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		if provider.request.ID != args[4] {
			t.Fatalf("expected opaque id %q, got %q", args[4], provider.request.ID)
		}
	}
}

func TestIntegrationIssueCommandReportsMissingDefaultAndUnknownSystem(t *testing.T) {
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"jira-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
		},
	})
	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing default", args: []string{"integration", "issue", "get", "--id", "123"}, want: "no default system configured"},
		{name: "unknown system", args: []string{"integration", "issue", "get", "--id", "123", "--system", "missing"}, want: "system is not configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}
