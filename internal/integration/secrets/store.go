package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rasungatullin/progress/internal/integration/model"
)

const (
	defaultServiceName = "progress"
	defaultFilePath    = "integration/private-values.json"
)

var (
	ErrNotFound        = errors.New("private value not found")
	execCommandContext = exec.CommandContext
)

type Store interface {
	Get(context.Context, string) (string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}

type Descriptor struct {
	Type     string
	Location string
}

func NewStore(config model.IntegrationPrivateStoreConfig, configHome string) (Store, Descriptor, error) {
	storeType := strings.TrimSpace(strings.ToLower(config.Type))
	if storeType == "" {
		storeType = defaultStoreType()
	}

	switch storeType {
	case "keychain":
		service := strings.TrimSpace(config.Service)
		if service == "" {
			service = defaultServiceName
		}
		return keychainStore{service: service}, Descriptor{Type: "keychain", Location: service}, nil
	case "file":
		path, err := fileStorePath(config.Path, configHome)
		if err != nil {
			return nil, Descriptor{}, err
		}
		return fileStore{path: path}, Descriptor{Type: "file", Location: path}, nil
	default:
		return nil, Descriptor{}, fmt.Errorf("private store type is not supported: %s", storeType)
	}
}

func defaultStoreType() string {
	if runtime.GOOS == "darwin" {
		return "keychain"
	}
	return "file"
}

func fileStorePath(configPath string, configHome string) (string, error) {
	path := strings.TrimSpace(configPath)
	if path != "" {
		if filepath.IsAbs(path) {
			return filepath.Clean(path), nil
		}
		home, err := defaultConfigHome(configHome)
		if err != nil {
			return "", err
		}
		return filepath.Join(home, filepath.Clean(path)), nil
	}

	home, err := defaultConfigHome(configHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultFilePath), nil
}

func defaultConfigHome(configHome string) (string, error) {
	home := strings.TrimSpace(configHome)
	if home != "" {
		return filepath.Clean(home), nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve config home for private file store: %w", err)
	}
	return filepath.Join(userHome, ".config", "progress"), nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("private value name must not be empty")
	}
	if strings.ContainsAny(name, "\x00\r\n") {
		return "", fmt.Errorf("private value name must not contain control characters")
	}
	return name, nil
}

type fileStore struct {
	path string
}

func (s fileStore) Get(_ context.Context, name string) (string, error) {
	name, err := normalizeName(name)
	if err != nil {
		return "", err
	}
	values, err := s.read()
	if err != nil {
		return "", err
	}
	value, ok := values[name]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (s fileStore) Set(_ context.Context, name string, value string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	values, err := s.read()
	if err != nil {
		return err
	}
	values[name] = value
	return s.write(values)
}

func (s fileStore) Delete(_ context.Context, name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	values, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := values[name]; !ok {
		return ErrNotFound
	}
	delete(values, name)
	return s.write(values)
}

func (s fileStore) read() (map[string]string, error) {
	content, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read private file store %s: %w", s.path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return map[string]string{}, nil
	}

	var values map[string]string
	if err := json.Unmarshal(content, &values); err != nil {
		return nil, fmt.Errorf("parse private file store %s: %w", s.path, err)
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

func (s fileStore) write(values map[string]string) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create private file store directory %s: %w", dir, err)
	}
	content, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("encode private file store: %w", err)
	}
	content = append(content, '\n')

	temp, err := os.CreateTemp(dir, ".private-values-*.json")
	if err != nil {
		return fmt.Errorf("create private file store temporary file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write private file store temporary file: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set private file store permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close private file store temporary file: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return fmt.Errorf("replace private file store %s: %w", s.path, err)
	}
	return nil
}

type keychainStore struct {
	service string
}

func (s keychainStore) Get(ctx context.Context, name string) (string, error) {
	name, err := normalizeName(name)
	if err != nil {
		return "", err
	}
	output, err := execCommandContext(ctx, "security", "find-generic-password", "-s", s.service, "-a", name, "-w").CombinedOutput()
	if err != nil {
		if isKeychainNotFound(output) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read private value %q from keychain service %q: %w: %s", name, s.service, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimRight(string(output), "\r\n"), nil
}

func (s keychainStore) Set(ctx context.Context, name string, value string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	return setKeychainValue(ctx, s.service, name, value)
}

func (s keychainStore) Delete(ctx context.Context, name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	output, err := execCommandContext(ctx, "security", "delete-generic-password", "-s", s.service, "-a", name).CombinedOutput()
	if err != nil {
		if isKeychainNotFound(output) {
			return ErrNotFound
		}
		return fmt.Errorf("delete private value %q from keychain service %q: %w: %s", name, s.service, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func isKeychainNotFound(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "could not be found") || strings.Contains(message, "item not found")
}
