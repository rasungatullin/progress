package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestFileStoreWritesReadsAndDeletesPrivateValues(t *testing.T) {
	t.Parallel()

	store, descriptor, err := NewStore(model.IntegrationPrivateStoreConfig{Type: "file"}, t.TempDir())
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	if descriptor.Type != "file" {
		t.Fatalf("unexpected descriptor type: %q", descriptor.Type)
	}

	if err := store.Set(context.Background(), "mt_auth_token", "secret"); err != nil {
		t.Fatalf("set private value: %v", err)
	}
	value, err := store.Get(context.Background(), "mt_auth_token")
	if err != nil {
		t.Fatalf("get private value: %v", err)
	}
	if value != "secret" {
		t.Fatalf("unexpected private value: %q", value)
	}

	if err := store.Delete(context.Background(), "mt_auth_token"); err != nil {
		t.Fatalf("delete private value: %v", err)
	}
	_, err = store.Get(context.Background(), "mt_auth_token")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileStoreUsesRestrictedFilePermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, descriptor, err := NewStore(model.IntegrationPrivateStoreConfig{Type: "file"}, dir)
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	if err := store.Set(context.Background(), "token", "secret"); err != nil {
		t.Fatalf("set private value: %v", err)
	}

	info, err := os.Stat(descriptor.Location)
	if err != nil {
		t.Fatalf("stat private file store: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("unexpected private file permissions: %o", mode)
	}

	dirInfo, err := os.Stat(filepath.Dir(descriptor.Location))
	if err != nil {
		t.Fatalf("stat private file store directory: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("private file store directory must not be accessible by group or others, got %o", mode)
	}
}

func TestFileStoreResolvesRelativePathFromConfigHome(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, descriptor, err := NewStore(model.IntegrationPrivateStoreConfig{Type: "file", Path: "private/custom.json"}, dir)
	if err != nil {
		t.Fatalf("create file store with relative path: %v", err)
	}

	expected := filepath.Join(dir, "private", "custom.json")
	if descriptor.Location != expected {
		t.Fatalf("unexpected private file store path: %q", descriptor.Location)
	}
}
