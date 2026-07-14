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
	response := model.Response{
		Status: model.ResponseStatusOK,
		Task:   &model.CanonicalTask{ID: request.ID, ExternalID: request.ID, Title: "issue"},
	}
	switch request.IntegrationType {
	case model.IntegrationTypeRepo:
		response.Repository = &model.Repository{FullName: request.Repository}
	case model.IntegrationTypeMessenger:
		response.Message = &model.Message{MessageID: "message-1", Body: request.Text}
	case model.IntegrationTypeWiki:
		response.WikiPages = []model.WikiPage{{ExternalID: "page-1", Title: "Page"}}
	}
	return response, nil
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

func TestIntegrationTypeOrientedTreeExcludesDispatcherAndPrivate(t *testing.T) {
	root := NewRootCommand()
	integrationCommand, _, err := root.Find([]string{"integration"})
	if err != nil {
		t.Fatalf("find integration command: %v", err)
	}
	for _, name := range []string{"dispatcher", "dispatch", "private"} {
		for _, child := range integrationCommand.Commands() {
			if child.Name() == name {
				t.Fatalf("obsolete integration command %q is still public", name)
			}
		}
	}
	for _, name := range []string{"issue", "repo", "messenger", "wiki"} {
		found := false
		for _, child := range integrationCommand.Commands() {
			if child.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("type-oriented command %q is missing", name)
		}
	}
}

func TestIntegrationTypeOrientedCommandsResolveConfiguredSystems(t *testing.T) {
	provider := &contractCaptureProvider{}
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{
			model.IntegrationTypeRepo:      "github-main",
			model.IntegrationTypeMessenger: "mattermost-main",
			model.IntegrationTypeWiki:      "confluence-main",
		},
		Systems: map[string]model.IntegrationSystemConfig{
			"github-main":     {Type: "script", IntegrationTypes: []string{model.IntegrationTypeRepo}},
			"mattermost-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeMessenger}},
			"confluence-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeWiki}},
			"wiki-explicit":   {Type: "script", IntegrationTypes: []string{model.IntegrationTypeWiki}},
		},
	})
	for _, system := range []string{"github-main", "mattermost-main", "confluence-main", "wiki-explicit"} {
		service.RegisterProvider(system, provider)
	}
	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	for _, tc := range []struct {
		args       []string
		wantType   string
		wantSystem string
	}{
		{args: []string{"integration", "repo", "get", "--repo", "owner/name"}, wantType: model.IntegrationTypeRepo, wantSystem: "github-main"},
		{args: []string{"integration", "messenger", "message", "create", "--text", "Состояние обновлено"}, wantType: model.IntegrationTypeMessenger, wantSystem: "mattermost-main"},
		{args: []string{"integration", "wiki", "page", "search", "--query", "эксплуатация"}, wantType: model.IntegrationTypeWiki, wantSystem: "confluence-main"},
		{args: []string{"integration", "wiki", "page", "search", "--query", "эксплуатация", "--system", "wiki-explicit"}, wantType: model.IntegrationTypeWiki, wantSystem: "wiki-explicit"},
	} {
		cmd := NewRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("выполнить %v: %v", tc.args, err)
		}
		if provider.request.IntegrationType != tc.wantType || provider.request.System != tc.wantSystem {
			t.Fatalf("неожиданный разрешённый запрос для %v: %#v", tc.args, provider.request)
		}
	}
}

func TestLegacySystemCommandReportsTypeOrientedReplacement(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"integration", "github", "issue", "get", "--id", "123"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("устаревшая команда по имени системы должна отсутствовать")
	}
}
