package integration

import (
	"bytes"
	"context"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

type contractProvider struct {
	request model.ProviderRequest
}

func (p *contractProvider) Execute(_ context.Context, request model.ProviderRequest) (model.Response, error) {
	p.request = request
	return model.Response{Status: model.ResponseStatusOK}, nil
}

func TestIssueRequestResolvesDefaultSystemAndKeepsOpaqueID(t *testing.T) {
	provider := &contractProvider{}
	service := NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeIssue: "jira-main"},
		Systems: map[string]model.IntegrationSystemConfig{
			"jira-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
		},
	})
	service.RegisterProvider("jira-main", provider)

	_, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeIssue,
		Resource:        "issue",
		ObjectType:      "issue",
		Operation:       "get",
		ID:              "ABC-123",
	})
	if err != nil {
		t.Fatalf("execute issue request: %v", err)
	}
	if provider.request.System != "jira-main" || provider.request.ID != "ABC-123" {
		t.Fatalf("unexpected resolved request: %#v", provider.request)
	}
}

func TestIssueRequestSelectsExplicitSystem(t *testing.T) {
	provider := &contractProvider{}
	service := NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{model.IntegrationTypeIssue: "github"},
		Systems: map[string]model.IntegrationSystemConfig{
			"github":    {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
			"jira-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
		},
	})
	service.RegisterProvider("jira-main", provider)

	_, err := service.Execute(context.Background(), Request{
		IntegrationType: model.IntegrationTypeIssue,
		System:          "jira-main",
		SystemProvided:  true,
		Resource:        "issue",
		Operation:       "get",
		ID:              "123",
	})
	if err != nil {
		t.Fatalf("execute issue request: %v", err)
	}
	if provider.request.System != "jira-main" || provider.request.ID != "123" {
		t.Fatalf("unexpected explicitly resolved request: %#v", provider.request)
	}
}

func TestIssueRequestWithoutDefaultSystemFailsDiagnostically(t *testing.T) {
	service := NewServiceFromConfig(log.New(io.Discard, "", 0), model.IntegrationConfigFile{
		Systems: map[string]model.IntegrationSystemConfig{
			"jira-main": {Type: "script", IntegrationTypes: []string{model.IntegrationTypeIssue}},
		},
	})

	_, err := service.Execute(context.Background(), Request{IntegrationType: model.IntegrationTypeIssue, Resource: "issue", Operation: "search"})
	if err == nil || err.Error() != `invalid integration request: no default system configured for integration type "issue"` {
		t.Fatalf("unexpected missing default error: %v", err)
	}
}

func TestLegacyIntegrationTypeSettingsAreNormalizedWithDiagnostic(t *testing.T) {
	var diagnostics bytes.Buffer
	service := NewServiceFromConfig(log.New(&diagnostics, "", 0), model.IntegrationConfigFile{
		DefaultSystems: map[string]string{"tracker": "work-tracker"},
		Systems: map[string]model.IntegrationSystemConfig{
			"work-tracker": {Type: "script", IntegrationTypes: []string{"tracker"}},
		},
	})
	service.RegisterProvider("work-tracker", &contractProvider{})

	if _, err := service.Execute(context.Background(), Request{IntegrationType: model.IntegrationTypeIssue, Resource: "issue", Operation: "get", ID: "ABC-123"}); err != nil {
		t.Fatalf("выполнить запрос через старую настройку: %v", err)
	}
	output := diagnostics.String()
	if !strings.Contains(output, "default_systems.tracker устарел") || !strings.Contains(output, "integration_type=tracker устарел") {
		t.Fatalf("отсутствует диагностика перехода: %q", output)
	}
}
