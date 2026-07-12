package configuration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"systems": {"mattermost": {"type": "mattermost", "token_private": "mt_token"}}
	}`), 0o600); err != nil {
		t.Fatalf("write integration config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".progress", "execution", "resources.json"), []byte(`{
		"defaults": {"model-binding": "default"}, "runners": ["codex"], "models": ["model"],
		"bindings": {"default": {"runner": "codex", "model": "model"}}
	}`), 0o600); err != nil {
		t.Fatalf("write resource config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "private.json"), []byte(`{"mt_token":"actual-secret"}`), 0o600); err != nil {
		t.Fatalf("write private store: %v", err)
	}

	snapshot, err := NewService(nil).Snapshot(context.Background(), SnapshotInput{RepoRoot: root, ConfigHome: root})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.PrivateValues) != 1 || snapshot.PrivateValues[0].Name != "mt_token" || !snapshot.PrivateValues[0].Available {
		t.Fatalf("unexpected private value snapshot: %#v", snapshot.PrivateValues)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "actual-secret") {
		t.Fatalf("snapshot contains private value: %s", encoded)
	}
}
