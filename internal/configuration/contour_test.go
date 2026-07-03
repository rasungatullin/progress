package configuration

import (
	"context"
	"os"
	"path/filepath"
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

	snapshot, err := NewService(nil).Snapshot(context.Background(), SnapshotInput{RepoRoot: t.TempDir(), LoadIntegration: true})
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
