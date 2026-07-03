package localtracker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rasungatullin/progress/internal/integration/model"
)

func TestServiceSupportsTaskCommentAndLabelOperations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(model.IntegrationSystemConfig{
		Database: model.IntegrationDatabaseConfig{Driver: "sqlite", Path: filepath.Join(root, "tasks.sqlite")},
	})
	service.resolveRepoRoot = func(context.Context) (string, error) { return root, nil }

	create, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "create",
		Title:      "Локальная задача",
		Body:       "Описание",
		Labels:     []string{"backend"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if create.Task == nil || create.Task.Number != 1 || create.Task.Title != "Локальная задача" {
		t.Fatalf("unexpected created task: %#v", create.Task)
	}

	comment, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "comment",
		ObjectType: "comment",
		Operation:  "create",
		Number:     create.Task.Number,
		Body:       "Комментарий",
	})
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if len(comment.TaskComments) != 1 || comment.TaskComments[0].Body != "Комментарий" {
		t.Fatalf("unexpected created comment: %#v", comment.TaskComments)
	}

	if _, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "label",
		ObjectType: "label",
		Operation:  "add",
		Number:     create.Task.Number,
		Labels:     []string{"bug"},
	}); err != nil {
		t.Fatalf("add label: %v", err)
	}
	if _, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "label",
		ObjectType: "label",
		Operation:  "remove",
		Number:     create.Task.Number,
		Labels:     []string{"backend"},
	}); err != nil {
		t.Fatalf("remove label: %v", err)
	}

	got, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "get",
		Number:     create.Task.Number,
	})
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if len(got.Task.Traits) != 1 || got.Task.Traits[0] != "bug" {
		t.Fatalf("unexpected labels after changes: %#v", got.Task.Traits)
	}

	comments, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "comment",
		ObjectType: "comment",
		Operation:  "comments",
		Number:     create.Task.Number,
	})
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments.TaskComments) != 1 || comments.Comments[0].Body != "Комментарий" {
		t.Fatalf("unexpected comments: %#v %#v", comments.TaskComments, comments.Comments)
	}

	search, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "search",
		Query:      "Локальная",
		Labels:     []string{"bug"},
	})
	if err != nil {
		t.Fatalf("search tasks: %v", err)
	}
	if len(search.SearchResults) != 1 || search.SearchResults[0].Number != create.Task.Number {
		t.Fatalf("unexpected search results: %#v", search.SearchResults)
	}
}

func TestSearchAppliesLimitAfterLabelFilter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(model.IntegrationSystemConfig{
		Database: model.IntegrationDatabaseConfig{Driver: "sqlite", Path: filepath.Join(root, "tasks.sqlite")},
	})
	service.resolveRepoRoot = func(context.Context) (string, error) { return root, nil }

	matching, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "create",
		Title:      "Старая подходящая задача",
		Labels:     []string{"bug"},
	})
	if err != nil {
		t.Fatalf("create matching task: %v", err)
	}
	if _, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "create",
		Title:      "Новая задача без метки",
	}); err != nil {
		t.Fatalf("create newer task: %v", err)
	}

	search, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "search",
		Labels:     []string{"bug"},
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("search tasks: %v", err)
	}
	if len(search.SearchResults) != 1 || search.SearchResults[0].Number != matching.Task.Number {
		t.Fatalf("unexpected search results: %#v", search.SearchResults)
	}
}

func TestServiceListsCommentsForTaskCatalogRequest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(model.IntegrationSystemConfig{
		Database: model.IntegrationDatabaseConfig{Driver: "sqlite", Path: filepath.Join(root, "tasks.sqlite")},
	})
	service.resolveRepoRoot = func(context.Context) (string, error) { return root, nil }

	create, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "create",
		Title:      "Локальная задача",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "comment",
		ObjectType: "comment",
		Operation:  "create",
		Number:     create.Task.Number,
		Body:       "Комментарий",
	}); err != nil {
		t.Fatalf("create comment: %v", err)
	}

	for _, operation := range []string{"comments", "list"} {
		comments, err := service.Execute(context.Background(), model.ProviderRequest{
			System:     "local",
			Resource:   "task",
			ObjectType: "task",
			Operation:  operation,
			Number:     create.Task.Number,
		})
		if err != nil {
			t.Fatalf("list comments with operation %q: %v", operation, err)
		}
		if len(comments.TaskComments) != 1 || comments.TaskComments[0].Body != "Комментарий" {
			t.Fatalf("unexpected comments for operation %q: %#v", operation, comments.TaskComments)
		}
	}
}

func TestServiceUsesDefaultDatabasePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	service := NewService(model.IntegrationSystemConfig{})
	service.resolveRepoRoot = func(context.Context) (string, error) { return root, nil }

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:    "local",
		Resource:  "auth",
		Operation: "status",
	})
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	expected := filepath.Join(root, defaultDatabasePath)
	if response.AuthStatus == nil || response.AuthStatus.Path != expected {
		t.Fatalf("unexpected auth status: %#v, expected path %q", response.AuthStatus, expected)
	}
}

func TestServiceUsesDatabaseDSNBeforePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dsnPath := filepath.Join(root, "dsn", "tasks.sqlite")
	path := filepath.Join(root, "path", "tasks.sqlite")
	service := NewService(model.IntegrationSystemConfig{
		Database: model.IntegrationDatabaseConfig{Driver: "sqlite", DSN: dsnPath, Path: path},
	})
	service.resolveRepoRoot = func(context.Context) (string, error) { return root, nil }

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "create",
		Title:      "Задача из DSN",
	})
	if err != nil {
		t.Fatalf("create task with dsn: %v", err)
	}
	if response.Task == nil || response.Task.Title != "Задача из DSN" {
		t.Fatalf("unexpected task response: %#v", response.Task)
	}
	if _, err := os.Stat(dsnPath); err != nil {
		t.Fatalf("expected database from dsn: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database.path must not be used when database.dsn is set: %v", err)
	}
}

func TestServiceCreatesDirectoryForRelativeFileDSN(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dsn := "file:.progress/local-tracker/tasks.sqlite?cache=shared"
	service := NewService(model.IntegrationSystemConfig{
		Database: model.IntegrationDatabaseConfig{Driver: "sqlite", DSN: dsn},
	})
	service.resolveRepoRoot = func(context.Context) (string, error) { return root, nil }

	response, err := service.Execute(context.Background(), model.ProviderRequest{
		System:     "local",
		Resource:   "task",
		ObjectType: "task",
		Operation:  "create",
		Title:      "Задача из file-DSN",
	})
	if err != nil {
		t.Fatalf("create task with relative file dsn: %v", err)
	}
	if response.Task == nil || response.Task.Title != "Задача из file-DSN" {
		t.Fatalf("unexpected task response: %#v", response.Task)
	}
	expected := filepath.Join(root, ".progress", "local-tracker", "tasks.sqlite")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected database from relative file dsn: %v", err)
	}
}

func TestServiceRejectsUnsupportedDatabaseDriver(t *testing.T) {
	t.Parallel()

	service := NewService(model.IntegrationSystemConfig{
		Database: model.IntegrationDatabaseConfig{Driver: "postgres"},
	})
	service.resolveRepoRoot = func(context.Context) (string, error) { return t.TempDir(), nil }

	response, err := service.Execute(context.Background(), model.ProviderRequest{System: "local", Resource: "auth", Operation: "status"})
	if err == nil {
		t.Fatal("expected unsupported driver error")
	}
	if response.Failure == nil || response.Failure.Kind != model.FailureKindInvalidRequest {
		t.Fatalf("unexpected failure: %#v", response.Failure)
	}
}
