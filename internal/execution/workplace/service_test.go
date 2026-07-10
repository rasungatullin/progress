package workplace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestPrepareUsesCurrentRepositoryWhenRepoIsOmitted(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	workplace, err := service.Prepare(context.Background(), model.Invocation{Workplace: model.WorkplaceSpec{Name: "task-49"}}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "task-49")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	if workplace.RepositoryRoot != hostRepoRoot {
		t.Fatalf("unexpected repository root: %q", workplace.RepositoryRoot)
	}
	if workplace.RepositoryURL != "" {
		t.Fatalf("expected empty repository url, got %q", workplace.RepositoryURL)
	}
	if workplace.BaseRef != "main" || workplace.HeadRef != "task-49" {
		t.Fatalf("unexpected resolved branches: %#v", workplace)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "main"}},
		{dir: hostRepoRoot, args: []string{"worktree", "add", "-b", "task-49", expectedDir, "origin/main"}},
	})
}

func TestPrepareUsesRequestedBaseBranch(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	workplace, err := service.Prepare(context.Background(), model.Invocation{
		Workplace: model.WorkplaceSpec{
			Name:    "task-49",
			BaseRef: "release",
		},
	}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "task-49")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	if workplace.BaseRef != "release" || workplace.HeadRef != "task-49" {
		t.Fatalf("unexpected resolved branches: %#v", workplace)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "release"}},
		{dir: hostRepoRoot, args: []string{"worktree", "add", "-b", "task-49", expectedDir, "origin/release"}},
	})
}

func TestPrepareUsesRequestedHeadBranch(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	responses := map[gitOutputKey]string{
		{dir: hostRepoRoot, args: keyArgs("rev-parse", "--verify", "--quiet", "refs/remotes/origin/feature/foo")}: "abc123\n",
	}
	service, gitCalls := newStubService(t, hostRepoRoot, responses)

	workplace, err := service.Prepare(context.Background(), model.Invocation{
		Workplace: model.WorkplaceSpec{
			Name:    "feature-foo",
			BaseRef: "main",
			HeadRef: "feature/foo",
		},
	}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "feature-foo")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	if workplace.BaseRef != "main" || workplace.HeadRef != "feature/foo" {
		t.Fatalf("unexpected resolved branches: %#v", workplace)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "main"}},
		{dir: hostRepoRoot, args: []string{"worktree", "add", "-b", "feature/foo", expectedDir, "origin/feature/foo"}},
	})
}

func TestPrepareUsesFetchedRequestedHeadBranchWhenRemoteRefIsMissing(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	responses := map[gitOutputKey]string{
		{dir: hostRepoRoot, args: keyArgs("fetch", "origin", "feature/narrow")}: "fetched\n",
	}
	service, gitCalls := newStubService(t, hostRepoRoot, responses)

	workplace, err := service.Prepare(context.Background(), model.Invocation{
		Workplace: model.WorkplaceSpec{
			Name:    "feature-narrow",
			BaseRef: "main",
			HeadRef: "feature/narrow",
		},
	}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "feature-narrow")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "main"}},
		{dir: hostRepoRoot, args: []string{"worktree", "add", "-b", "feature/narrow", expectedDir, "FETCH_HEAD"}},
	})
}

func TestPrepareRejectsMissingRequestedHeadBranch(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	_, err := service.Prepare(context.Background(), model.Invocation{
		Workplace: model.WorkplaceSpec{
			Name:    "feature-missing",
			BaseRef: "main",
			HeadRef: "feature/missing",
		},
	}, model.Profile{}, model.Allocation{})
	if err == nil {
		t.Fatal("expected missing requested head branch error")
	}
	if !strings.Contains(err.Error(), `head branch "feature/missing" is not available for workplace preparation`) {
		t.Fatalf("unexpected error: %v", err)
	}

	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "main"}},
	})
}

func TestPrepareUsesRemoteTaskBranchWhenPresent(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	responses := map[gitOutputKey]string{
		{dir: hostRepoRoot, args: keyArgs("rev-parse", "--verify", "--quiet", "refs/remotes/origin/112")}: "abc123\n",
	}
	service, gitCalls := newStubService(t, hostRepoRoot, responses)

	workplace, err := service.Prepare(context.Background(), model.Invocation{Workplace: model.WorkplaceSpec{Name: "112"}}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "112")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "main"}},
		{dir: hostRepoRoot, args: []string{"worktree", "add", "-b", "112", expectedDir, "origin/112"}},
	})
}

func TestPrepareClonesExternalRepositoryWhenCacheIsMissing(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	workplace, err := service.Prepare(context.Background(), model.Invocation{
		Repository: model.RepositorySpec{URL: "owner/name"},
		Workplace:  model.WorkplaceSpec{Name: "task-49"},
	}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	cacheDir := filepath.Join(hostRepoRoot, ".progress", "repositories", "github-5-owner-name")
	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "github-5-owner-name", "task-49")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	if workplace.RepositoryRoot != cacheDir {
		t.Fatalf("unexpected repository root: %q", workplace.RepositoryRoot)
	}
	if workplace.RepositoryURL != "https://github.com/owner/name.git" {
		t.Fatalf("unexpected repository url: %q", workplace.RepositoryURL)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"clone", "https://github.com/owner/name.git", cacheDir}},
		{dir: cacheDir, args: []string{"fetch", "origin", "main"}},
		{dir: cacheDir, args: []string{"worktree", "add", "-b", "task-49", expectedDir, "origin/main"}},
	})
}

func TestPrepareUsesCurrentRepositoryWhenExplicitRepoMatchesOrigin(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	responses := map[gitOutputKey]string{
		{dir: hostRepoRoot, args: keyArgs("config", "--get", "remote.origin.url")}: "git@github.com:owner/name.git\n",
	}
	service, gitCalls := newStubService(t, hostRepoRoot, responses)

	workplace, err := service.Prepare(context.Background(), model.Invocation{
		Repository: model.RepositorySpec{URL: "owner/name"},
		Workplace:  model.WorkplaceSpec{Name: "task-49"},
	}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "task-49")
	if workplace.Name != expectedDir {
		t.Fatalf("unexpected workplace path: %q", workplace.Name)
	}
	if workplace.RepositoryRoot != hostRepoRoot {
		t.Fatalf("unexpected repository root: %q", workplace.RepositoryRoot)
	}
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: hostRepoRoot, args: []string{"fetch", "origin", "main"}},
		{dir: hostRepoRoot, args: []string{"worktree", "add", "-b", "task-49", expectedDir, "origin/main"}},
	})
}

func TestPrepareUsesLocalEnvironmentFromAllocation(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	workplace, err := service.Prepare(context.Background(), model.Invocation{}, model.Profile{}, model.Allocation{
		Environment:     "same-process",
		EnvironmentType: "local",
	})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	if workplace.Name != hostRepoRoot || workplace.RepositoryRoot != hostRepoRoot {
		t.Fatalf("unexpected local workplace: %#v", workplace)
	}
	if workplace.Environment != "same-process" || workplace.EnvironmentType != "local" {
		t.Fatalf("unexpected environment: %#v", workplace)
	}
	assertGitCalls(t, gitCalls, []gitCall{})
}

func TestPrepareUsesAllocationEnvironmentTypeForExplicitEnvironmentName(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	workplace, err := service.Prepare(context.Background(), model.Invocation{
		Workplace: model.WorkplaceSpec{
			Name:        "task-49",
			Environment: "same-process",
		},
	}, model.Profile{}, model.Allocation{
		Environment:     "same-process",
		EnvironmentType: "local",
	})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	if workplace.Name != hostRepoRoot || workplace.RepositoryRoot != hostRepoRoot {
		t.Fatalf("unexpected local workplace: %#v", workplace)
	}
	if workplace.Environment != "same-process" || workplace.EnvironmentType != "local" {
		t.Fatalf("unexpected environment: %#v", workplace)
	}
	assertGitCalls(t, gitCalls, []gitCall{})
}

func TestPrepareRejectsLocalEnvironmentWithRepositoryURL(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	service, gitCalls := newStubService(t, hostRepoRoot, nil)

	_, err := service.Prepare(context.Background(), model.Invocation{
		Repository: model.RepositorySpec{URL: "owner/name"},
	}, model.Profile{}, model.Allocation{
		Environment:     "same-process",
		EnvironmentType: "local",
	})
	if err == nil {
		t.Fatal("expected local repository url error")
	}
	if !strings.Contains(err.Error(), "local environment cannot use repository url") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertGitCalls(t, gitCalls, []gitCall{})
}

func TestPrepareFetchesExternalRepositoryWhenCacheExists(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	cacheDir := filepath.Join(hostRepoRoot, ".progress", "repositories", "github-5-owner-name")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	responses := map[gitOutputKey]string{
		{dir: cacheDir, args: keyArgs("rev-parse", "--is-inside-work-tree")}:   "true\n",
		{dir: cacheDir, args: keyArgs("config", "--get", "remote.origin.url")}: "https://github.com/owner/name.git\n",
	}
	service, gitCalls := newStubService(t, hostRepoRoot, responses)

	_, err := service.Prepare(context.Background(), model.Invocation{
		Repository: model.RepositorySpec{URL: "https://github.com/owner/name.git"},
		Workplace:  model.WorkplaceSpec{Name: "task-49"},
	}, model.Profile{}, model.Allocation{})
	if err != nil {
		t.Fatalf("prepare workplace: %v", err)
	}

	expectedDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "github-5-owner-name", "task-49")
	assertGitCalls(t, gitCalls, []gitCall{
		{dir: cacheDir, args: []string{"fetch", "origin"}},
		{dir: cacheDir, args: []string{"fetch", "origin", "main"}},
		{dir: cacheDir, args: []string{"worktree", "add", "-b", "task-49", expectedDir, "origin/main"}},
	})
}

func TestPrepareRejectsInvalidRepositoryRef(t *testing.T) {
	t.Parallel()

	service, _ := newStubService(t, t.TempDir(), nil)

	_, err := service.Prepare(context.Background(), model.Invocation{
		Repository: model.RepositorySpec{URL: "../owner/name"},
		Workplace:  model.WorkplaceSpec{Name: "task-49"},
	}, model.Profile{}, model.Allocation{})
	if err == nil {
		t.Fatal("expected invalid repository error")
	}
	if !strings.Contains(err.Error(), "repository ref is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeRepositoryRefUsesUnambiguousCacheKeys(t *testing.T) {
	t.Parallel()

	first, err := normalizeRepositoryRef("foo/bar-baz")
	if err != nil {
		t.Fatalf("normalize first repository: %v", err)
	}
	second, err := normalizeRepositoryRef("foo-bar/baz")
	if err != nil {
		t.Fatalf("normalize second repository: %v", err)
	}

	if first.CacheKey == second.CacheKey {
		t.Fatalf("cache keys must not collide: %q", first.CacheKey)
	}
}

func TestPrepareRejectsExistingWorkplaceWithDifferentBranch(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	existingDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "task-49")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir workplace: %v", err)
	}

	responses := map[gitOutputKey]string{
		{dir: existingDir, args: keyArgs("rev-parse", "--is-inside-work-tree")}: "true\n",
		{dir: existingDir, args: keyArgs("branch", "--show-current")}:           "other-branch\n",
	}
	service, _ := newStubService(t, hostRepoRoot, responses)

	_, err := service.Prepare(context.Background(), model.Invocation{Workplace: model.WorkplaceSpec{Name: "task-49"}}, model.Profile{}, model.Allocation{})
	if err == nil {
		t.Fatal("expected branch mismatch error")
	}
	if !strings.Contains(err.Error(), `existing workplace branch mismatch: expected "task-49", got "other-branch"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsExistingWorkplaceFromDifferentRepository(t *testing.T) {
	t.Parallel()

	hostRepoRoot := t.TempDir()
	cacheDir := filepath.Join(hostRepoRoot, ".progress", "repositories", "github-5-owner-name")
	existingDir := filepath.Join(hostRepoRoot, ".progress", "workplaces", "github-5-owner-name", "task-49")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir workplace: %v", err)
	}

	responses := map[gitOutputKey]string{
		{dir: cacheDir, args: keyArgs("rev-parse", "--is-inside-work-tree")}:      "true\n",
		{dir: cacheDir, args: keyArgs("config", "--get", "remote.origin.url")}:    "https://github.com/owner/name.git\n",
		{dir: existingDir, args: keyArgs("rev-parse", "--is-inside-work-tree")}:   "true\n",
		{dir: existingDir, args: keyArgs("branch", "--show-current")}:             "task-49\n",
		{dir: existingDir, args: keyArgs("config", "--get", "remote.origin.url")}: "https://github.com/other/repo.git\n",
	}
	service, _ := newStubService(t, hostRepoRoot, responses)

	_, err := service.Prepare(context.Background(), model.Invocation{
		Repository: model.RepositorySpec{URL: "owner/name"},
		Workplace:  model.WorkplaceSpec{Name: "task-49"},
	}, model.Profile{}, model.Allocation{})
	if err == nil {
		t.Fatal("expected repository mismatch error")
	}
	if !strings.Contains(err.Error(), `existing workplace repository mismatch: expected "https://github.com/owner/name.git", got "https://github.com/other/repo.git"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

type gitCall struct {
	dir  string
	args []string
}

type gitOutputKey struct {
	dir  string
	args string
}

func newStubService(t *testing.T, hostRepoRoot string, responses map[gitOutputKey]string) (*Service, *[]gitCall) {
	t.Helper()

	if responses == nil {
		responses = map[gitOutputKey]string{}
	}
	responses[gitOutputKey{dir: hostRepoRoot, args: keyArgs("symbolic-ref", "refs/remotes/origin/HEAD")}] = "refs/remotes/origin/main\n"
	responses[gitOutputKey{dir: filepath.Join(hostRepoRoot, ".progress", "repositories", "github-5-owner-name"), args: keyArgs("symbolic-ref", "refs/remotes/origin/HEAD")}] = "refs/remotes/origin/main\n"

	gitCalls := &[]gitCall{}
	service := &Service{
		resolveRepoRoot: func(context.Context) (string, error) { return hostRepoRoot, nil },
		runGit: func(_ context.Context, dir string, args ...string) error {
			*gitCalls = append(*gitCalls, gitCall{dir: dir, args: append([]string(nil), args...)})
			return nil
		},
		runGitOutput: func(_ context.Context, dir string, args ...string) (string, error) {
			if output, ok := responses[gitOutputKey{dir: dir, args: keyArgs(args...)}]; ok {
				return output, nil
			}
			return "", fmt.Errorf("unexpected git output call: dir=%s args=%v", dir, args)
		},
	}

	return service, gitCalls
}

func keyArgs(args ...string) string {
	return strings.Join(args, "\x00")
}

func assertGitCalls(t *testing.T, actual *[]gitCall, expected []gitCall) {
	t.Helper()
	if !reflect.DeepEqual(*actual, expected) {
		t.Fatalf("unexpected git calls:\nactual: %#v\nexpected: %#v", *actual, expected)
	}
}
