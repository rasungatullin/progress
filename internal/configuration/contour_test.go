package configuration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceSnapshotLoadsAvailableLayers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".progress", "integration"), 0o755); err != nil {
		t.Fatalf("mkdir integration: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".progress", "execution"), 0o755); err != nil {
		t.Fatalf("mkdir execution: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "integration", "systems.json"), []byte(`{
		"systems": {
			"github": {
				"type": "github",
				"integration_types": ["tracker", "repository"],
				"default": true
			}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write integration config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "execution", "resources.json"), []byte(`{
		"defaults": {"model-binding": "default"},
		"runners": ["codex"],
		"models": ["openai/gpt-5.4"],
		"bindings": {"default": {"runner": "codex", "model": "openai/gpt-5.4"}}
	}`), 0o600); err != nil {
		t.Fatalf("write resource config: %v", err)
	}

	snapshot, err := NewService(nil).Snapshot(context.Background(), SnapshotInput{RepoRoot: root})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Integration == nil {
		t.Fatal("expected integration config")
	}
	if snapshot.ExecutionResources == nil {
		t.Fatal("expected execution resources")
	}
	if len(snapshot.Failures) != 0 {
		t.Fatalf("unexpected failures: %#v", snapshot.Failures)
	}
}

func TestServiceSnapshotReportsMissingConfig(t *testing.T) {
	t.Parallel()

	snapshot, err := NewService(nil).Snapshot(context.Background(), SnapshotInput{
		RepoRoot:        t.TempDir(),
		ConfigHome:      t.TempDir(),
		LoadIntegration: true,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Integration != nil {
		t.Fatal("did not expect integration config")
	}
	if len(snapshot.Failures) != 1 || snapshot.Failures[0].Scope != "integration" {
		t.Fatalf("unexpected failures: %#v", snapshot.Failures)
	}
}

func TestServiceSnapshotListsPrivateValueNamesAndAvailabilityWithoutValues(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".progress", "integration"), 0o755); err != nil {
		t.Fatalf("mkdir integration: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".progress", "execution"), 0o755); err != nil {
		t.Fatalf("mkdir execution: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "integration", "systems.json"), []byte(`{
		"private_store": {"type": "file", "path": "private.json"},
		"systems": {
			"mattermost": {"type": "mattermost", "token_private": "mt_token"},
			"github": {"type": "github", "token_private": "github-token"}
		}
	}`), 0o600); err != nil {
		t.Fatalf("write integration config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "execution", "resources.json"), []byte(`{
		"defaults": {"model-binding": "default"}, "runners": ["codex"], "models": ["model"],
		"bindings": {"default": {"runner": "codex", "model": "model"}}
	}`), 0o600); err != nil {
		t.Fatalf("write resource config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.json"), []byte(`{"mt_token":"actual-secret","github-token":"actual-token"}`), 0o600); err != nil {
		t.Fatalf("write private store: %v", err)
	}

	snapshot, err := NewService(nil).Snapshot(context.Background(), SnapshotInput{RepoRoot: root, ConfigHome: root})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.PrivateValues) != 2 || snapshot.PrivateValues[0].Name != "github-token" || !snapshot.PrivateValues[0].Available || snapshot.PrivateValues[1].Name != "mt_token" || !snapshot.PrivateValues[1].Available {
		t.Fatalf("unexpected private value snapshot: %#v", snapshot.PrivateValues)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "actual-secret") || strings.Contains(string(encoded), "actual-token") {
		t.Fatalf("snapshot contains private value: %s", encoded)
	}
}

func TestRedactSnapshotMasksPrivateValuesInLayersAndFailures(t *testing.T) {
	t.Parallel()

	config := integrationmodel.IntegrationConfigFile{
		Systems: map[string]integrationmodel.IntegrationSystemConfig{
			"github": {Token: "actual-token"},
		},
	}
	snapshot := Snapshot{
		Integration: &IntegrationConfig{
			Config: config,
			Layers: []IntegrationConfigLayer{{Config: config}},
		},
		Failures: []SnapshotFailure{{Message: "cannot use actual-token"}},
	}

	redactSnapshot(snapshot)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(encoded), "actual-token") || strings.Contains(snapshot.Failures[0].Message, "actual-token") {
		t.Fatalf("snapshot contains private value: %s", encoded)
	}
}

func TestServiceSnapshotUsesCanonicalPrivateStoreForPartialContour(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".progress", "integration"), 0o755); err != nil {
		t.Fatalf("mkdir integration: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".progress", "execution"), 0o755); err != nil {
		t.Fatalf("mkdir execution: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "integration", "systems.json"), []byte(`{
		"systems": {"github": {"type": "github", "token_private": "github-token"}}
	}`), 0o600); err != nil {
		t.Fatalf("write integration config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "execution", "resources.json"), []byte(`{
		"private_store": {"type": "file", "path": "private.json"},
		"defaults": {"model-binding": "default"},
		"runners": ["codex"], "models": ["model"],
		"bindings": {"default": {"runner": "codex", "model": "model"}}
	}`), 0o600); err != nil {
		t.Fatalf("write resource config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.json"), []byte(`{"github-token":"actual-token"}`), 0o600); err != nil {
		t.Fatalf("write private store: %v", err)
	}

	snapshot, err := NewService(nil).Snapshot(context.Background(), SnapshotInput{
		RepoRoot: root, ConfigHome: root, LoadIntegration: true,
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.PrivateValues) != 1 || snapshot.PrivateValues[0].Name != "github-token" || !snapshot.PrivateValues[0].Available {
		t.Fatalf("unexpected private value snapshot: %#v", snapshot.PrivateValues)
	}
}
