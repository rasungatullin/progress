//go:build !darwin || !cgo

package secrets

import (
	"context"
	"fmt"
)

func defaultStoreType() string {
	return "file"
}

func setKeychainValue(_ context.Context, service string, name string, _ string) error {
	return fmt.Errorf("write private value %q to keychain service %q: keychain store is supported only on macOS with cgo", name, service)
}
