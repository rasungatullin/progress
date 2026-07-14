package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestIntegrationIssueFlagsUseCatalogNames(t *testing.T) {
	root := NewRootCommand()
	for _, path := range [][]string{
		{"integration", "issue", "create"},
		{"integration", "issue", "update"},
		{"integration", "issue", "search"},
		{"integration", "issue", "label", "add"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if cmd.Flags().Lookup("labels") == nil {
			t.Fatalf("%v must publish --labels", path)
		}
		if cmd.Flags().Lookup("label") != nil {
			t.Fatalf("%v must not publish parallel --label", path)
		}
	}
	cmd, _, err := root.Find([]string{"integration", "issue", "create"})
	if err != nil {
		t.Fatalf("find issue create: %v", err)
	}
	if cmd.Flags().Lookup("external_id") == nil || cmd.Flags().Lookup("external-id") != nil {
		t.Fatal("issue create must publish only --external_id")
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

func TestIntegrationIssueSearchPassesRepeatedLabelFields(t *testing.T) {
	provider := &contractCaptureProvider{}
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeIssue: "tracker"},
		Systems: map[string]model.IntegrationSystemConfig{
			"tracker": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
		},
	})
	service.RegisterProvider("tracker", provider)
	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"integration", "issue", "search", "--labels", "one", "--labels", "two", "--exclude_labels", "three", "--exclude_labels", "four"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute issue search: %v", err)
	}
	if got, want := strings.Join(provider.request.Labels, ","), "one,two"; got != want {
		t.Fatalf("labels = %q, want %q", got, want)
	}
	if got, want := strings.Join(provider.request.ExcludeLabels, ","), "three,four"; got != want {
		t.Fatalf("exclude labels = %q, want %q", got, want)
	}
}

func TestIntegrationInvokeInputFailurePreservesCatalogRoute(t *testing.T) {
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeRepo: "repo-system"},
		Systems: map[string]model.IntegrationSystemConfig{
			"repo-system": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeRepo}, Operations: map[string]model.IntegrationOperationConfig{
				"repo.custom.field.get": {Required: []string{"repository"}, Command: "unused"},
			}},
		},
	})
	provider := &contractCaptureProvider{}
	service.RegisterProvider("repo-system", provider)
	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand()
	cmd.SetOut(stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"integration", "invoke", "--name", "repo.custom.field.get", "--input", `{}`, "--format", "json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invoke must reject a missing required field")
	}
	var response integration.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode invoke response: %v; output=%q", err, stdout.String())
	}
	if response.Failure == nil || response.Failure.Kind != "invalid-request" {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
	if response.Route.ProviderType != "script" || !response.Route.ProviderAvailable || response.Route.ObjectType != "custom.field" {
		t.Fatalf("incomplete invoke route: %#v", response.Route)
	}
}

func TestIntegrationInvokeOrdinaryCommentDropsInlineCoordinates(t *testing.T) {
	provider := &contractCaptureProvider{}
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeRepo: "repo-system"},
		Systems: map[string]model.IntegrationSystemConfig{
			"repo-system": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeRepo}, Operations: map[string]model.IntegrationOperationConfig{
				"repo.merge-request.comment.create": {Required: []string{"number", "body"}, Optional: []string{"repository", "path", "line", "side"}, Command: "unused"},
			}},
		},
	})
	service.RegisterProvider("repo-system", provider)
	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"integration", "invoke", "--name", "repo.merge-request.comment.create", "--input", `{"number":7,"body":"обычный комментарий","path":"file.go","line":12,"side":"RIGHT"}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute invoke: %v", err)
	}
	if provider.request.Path != "" || provider.request.Line != 0 || provider.request.Side != "" {
		t.Fatalf("ordinary comment retained inline coordinates: %#v", provider.request)
	}
	if provider.request.Extra["path"] != "file.go" || provider.request.Extra["line"] != float64(12) || provider.request.Extra["side"] != "RIGHT" {
		t.Fatalf("ordinary comment lost contract fields for script: %#v", provider.request.Extra)
	}
}

func TestIntegrationInvokeReviewRemarkUsesReviewRemarkObject(t *testing.T) {
	provider := &contractCaptureProvider{}
	service := integration.NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeRepo: "repo-system"},
		Systems: map[string]model.IntegrationSystemConfig{
			"repo-system": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeRepo}, Operations: map[string]model.IntegrationOperationConfig{
				"repo.review-remark.create": {Required: []string{"number", "body", "path", "line"}, Command: "unused"},
			}},
		},
	})
	service.RegisterProvider("repo-system", provider)
	original := integrationServiceFactory
	integrationServiceFactory = func(*cobra.Command) *integration.Service { return service }
	t.Cleanup(func() { integrationServiceFactory = original })

	cmd := NewRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"integration", "invoke", "--name", "repo.review-remark.create", "--input", `{"number":7,"body":"замечание","path":"file.go","line":12}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute invoke: %v", err)
	}
	if provider.request.ObjectType != "review-remark" || provider.request.Operation != "create" {
		t.Fatalf("review remark was routed as another object: %#v", provider.request)
	}
}
