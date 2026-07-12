package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestFileStoreWritesReadsAndDeletesPrivateValues(t *testing.T) {
	t.Parallel()

	store, descriptor, err := NewStore(model.ResourcePrivateStoreConfig{Type: "file"}, t.TempDir())
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

func TestMaskErrorHidesPrivateValuesAndPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("backend returned actual-secret")
	err := MaskError(fmt.Errorf("read failed: %w", cause), "actual-secret")
	if strings.Contains(err.Error(), "actual-secret") {
		t.Fatalf("masked error contains private value: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("masked error did not preserve cause: %v", err)
	}
}

func TestFileStoreUsesRestrictedFilePermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, descriptor, err := NewStore(model.ResourcePrivateStoreConfig{Type: "file"}, dir)
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
	_, descriptor, err := NewStore(model.ResourcePrivateStoreConfig{Type: "file", Path: "private/custom.json"}, dir)
	if err != nil {
		t.Fatalf("create file store with relative path: %v", err)
	}

	expected := filepath.Join(dir, "private", "custom.json")
	if descriptor.Location != expected {
		t.Fatalf("unexpected private file store path: %q", descriptor.Location)
	}
}

func TestFileStoreSerializesConcurrentMutations(t *testing.T) {
	t.Parallel()

	store, descriptor, err := NewStore(model.ResourcePrivateStoreConfig{Type: "file"}, t.TempDir())
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}

	const count = 40
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("token_%02d", index)
			if err := store.Set(context.Background(), name, "secret"); err != nil {
				errs <- fmt.Errorf("set %s: %w", name, err)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	content, err := os.ReadFile(descriptor.Location)
	if err != nil {
		t.Fatalf("read private file store: %v", err)
	}
	var values map[string]string
	if err := json.Unmarshal(content, &values); err != nil {
		t.Fatalf("parse private file store: %v", err)
	}
	if len(values) != count {
		t.Fatalf("file store must keep every concurrent mutation, got %d values: %#v", len(values), values)
	}
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("token_%02d", index)
		if values[name] != "secret" {
			t.Fatalf("missing value %s in private file store: %#v", name, values)
		}
	}
}

func TestFileStoreLockHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	store, descriptor, err := NewStore(model.ResourcePrivateStoreConfig{Type: "file"}, t.TempDir())
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(descriptor.Location), 0o700); err != nil {
		t.Fatalf("create private file store directory: %v", err)
	}
	lockDir := descriptor.Location + ".lock"
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(lockDir)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = store.Set(ctx, "token", "secret")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline while waiting for file store lock, got %v", err)
	}
}

func TestFileStoreRecoversStaleLock(t *testing.T) {
	t.Parallel()

	store, descriptor, err := NewStore(model.ResourcePrivateStoreConfig{Type: "file"}, t.TempDir())
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(descriptor.Location), 0o700); err != nil {
		t.Fatalf("create private file store directory: %v", err)
	}
	lockDir := descriptor.Location + ".lock"
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatalf("create stale lock directory: %v", err)
	}
	staleTime := time.Now().Add(-fileStoreLockStaleAfter - time.Second)
	if err := os.Chtimes(lockDir, staleTime, staleTime); err != nil {
		t.Fatalf("mark lock directory stale: %v", err)
	}

	if err := store.Set(context.Background(), "token", "secret"); err != nil {
		t.Fatalf("set private value with stale lock: %v", err)
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("stale lock must be removed after write, got %v", err)
	}

	value, err := store.Get(context.Background(), "token")
	if err != nil {
		t.Fatalf("get private value: %v", err)
	}
	if value != "secret" {
		t.Fatalf("unexpected private value: %q", value)
	}
}
