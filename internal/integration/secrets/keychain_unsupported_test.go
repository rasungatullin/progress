//go:build !darwin || !cgo

package secrets

import (
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestNewStoreRejectsExplicitKeychainWhenUnsupported(t *testing.T) {
	t.Parallel()

	_, _, err := NewStore(model.IntegrationPrivateStoreConfig{Type: "keychain"}, t.TempDir())
	if err == nil {
		t.Fatal("expected unsupported keychain error")
	}
	if !strings.Contains(err.Error(), `private store type "keychain" is not supported in current build`) {
		t.Fatalf("unexpected keychain error: %v", err)
	}
}
