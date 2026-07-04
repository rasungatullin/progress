package workplace

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/execution/model"
)

type Service struct {
	runGit          func(context.Context, string, ...string) error
	runGitOutput    func(context.Context, string, ...string) (string, error)
	resolveRepoRoot func(context.Context) (string, error)
}

func NewService() *Service {
	return &Service{runGit: runGit, runGitOutput: runGitOutput, resolveRepoRoot: resolveRepoRoot}
}

func (s *Service) Prepare(ctx context.Context, in model.Invocation, profile model.Profile, allocation model.Allocation) (model.Workplace, error) {
	environment, environmentType := selectedEnvironment(in, allocation)
	if in.Launch.Directory != "" {
		info, err := os.Stat(in.Launch.Directory)
		if err != nil {
			return model.Workplace{}, err
		}

		if !info.IsDir() {
			return model.Workplace{}, fmt.Errorf("execution directory is not a folder: %s", in.Launch.Directory)
		}

		if environmentType == "" {
			environmentType = configuration.EnvironmentTypeLocal
		}
		if environment == "" {
			environment = configuration.EnvironmentTypeLocal
		}
		if environmentType != configuration.EnvironmentTypeLocal {
			return model.Workplace{}, fmt.Errorf("execution directory can be used only with local environment")
		}

		return model.Workplace{Name: in.Launch.Directory, Environment: environment, EnvironmentType: environmentType, Ready: true}, nil
	}

	if environmentType == "" {
		environmentType = configuration.EnvironmentTypeWorktree
		if strings.TrimSpace(in.Workplace.Name) == "" && strings.TrimSpace(in.Repository.URL) == "" {
			environmentType = configuration.EnvironmentTypeLocal
		}
	}
	if environment == "" {
		environment = environmentType
	}
	if environmentType == configuration.EnvironmentTypeLocal {
		hostRepoRoot, err := s.resolveRepoRoot(ctx)
		if err != nil {
			return model.Workplace{}, err
		}
		return model.Workplace{Name: hostRepoRoot, Environment: environment, EnvironmentType: environmentType, RepositoryRoot: hostRepoRoot, Ready: true}, nil
	}
	if environmentType != configuration.EnvironmentTypeWorktree {
		return model.Workplace{}, fmt.Errorf("execution environment is unsupported by workplace preparation: %s", environment)
	}

	name := strings.TrimSpace(in.Workplace.Name)

	if err := validateWorkplaceName(name); err != nil {
		return model.Workplace{}, err
	}

	hostRepoRoot, err := s.resolveRepoRoot(ctx)
	if err != nil {
		return model.Workplace{}, err
	}

	repoSource, err := normalizeRepositoryRef(strings.TrimSpace(in.Repository.URL))
	if err != nil {
		return model.Workplace{}, err
	}

	repoRoot := hostRepoRoot
	repositoryURL := ""
	if repoSource != nil {
		repositoryURL = repoSource.CloneURL
		repoRoot, err = s.materializeRepository(ctx, hostRepoRoot, *repoSource)
		if err != nil {
			return model.Workplace{}, err
		}
	}

	baseBranch, err := s.resolveOriginDefaultBranch(ctx, repoRoot)
	if err != nil {
		return model.Workplace{}, err
	}

	targetDir := filepath.Join(repoRoot, ".progress", "workplaces", name)
	if repoSource != nil {
		targetDir = filepath.Join(hostRepoRoot, ".progress", "workplaces", repoSource.CacheKey, name)
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return model.Workplace{}, fmt.Errorf("create workplace parent directory: %w", err)
	}

	if info, err := os.Stat(targetDir); err == nil {
		if !info.IsDir() {
			return model.Workplace{}, fmt.Errorf("workplace path is not a folder: %s", targetDir)
		}

		if err := s.validateExistingWorkplace(ctx, targetDir, name, repoRoot); err != nil {
			return model.Workplace{}, err
		}

		return model.Workplace{Name: targetDir, Environment: environment, EnvironmentType: environmentType, RepositoryURL: repositoryURL, RepositoryRoot: repoRoot, Ready: true}, nil
	} else if !os.IsNotExist(err) {
		return model.Workplace{}, fmt.Errorf("check workplace directory: %w", err)
	}

	if err := s.runGit(ctx, repoRoot, "fetch", "origin", baseBranch); err != nil {
		return model.Workplace{}, fmt.Errorf("fetch origin/%s: %w", baseBranch, err)
	}

	if err := s.runGit(ctx, repoRoot, "worktree", "add", "-b", name, targetDir, "FETCH_HEAD"); err != nil {
		return model.Workplace{}, fmt.Errorf("create git worktree %q: %w", name, err)
	}

	return model.Workplace{Name: targetDir, Environment: environment, EnvironmentType: environmentType, RepositoryURL: repositoryURL, RepositoryRoot: repoRoot, Ready: true}, nil
}

func selectedEnvironment(in model.Invocation, allocation model.Allocation) (string, string) {
	if environment := strings.TrimSpace(in.Workplace.Environment); environment != "" {
		return environment, environmentTypeFromName(environment)
	}
	environment := strings.TrimSpace(allocation.Environment)
	environmentType := strings.TrimSpace(allocation.EnvironmentType)
	if environmentType == "" {
		environmentType = environmentTypeFromName(environment)
	}
	return environment, environmentType
}

func environmentTypeFromName(environment string) string {
	switch strings.TrimSpace(environment) {
	case configuration.EnvironmentTypeLocal:
		return configuration.EnvironmentTypeLocal
	case configuration.EnvironmentTypeWorktree:
		return configuration.EnvironmentTypeWorktree
	default:
		return ""
	}
}

func validateWorkplaceName(value string) error {
	if value == "" {
		return fmt.Errorf("workplace name is required")
	}

	if value == "." || value == ".." {
		return fmt.Errorf("workplace name is invalid: %s", value)
	}

	if strings.Contains(value, string(filepath.Separator)) {
		return fmt.Errorf("workplace name must not contain path separators: %s", value)
	}

	return nil
}

func resolveRepoRoot(ctx context.Context) (string, error) {
	output, err := runGitOutput(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve git repository root: %w", err)
	}

	return strings.TrimSpace(output), nil
}

func (s *Service) resolveOriginDefaultBranch(ctx context.Context, dir string) (string, error) {
	output, err := s.runGitOutput(ctx, dir, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve origin default branch: %w", err)
	}

	ref := strings.TrimSpace(output)
	branch, found := strings.CutPrefix(ref, "refs/remotes/origin/")
	if !found || branch == "" {
		return "", fmt.Errorf("resolve origin default branch: unexpected ref %q", ref)
	}

	return branch, nil
}

func (s *Service) validateExistingWorkplace(ctx context.Context, dir, expectedBranch, expectedRepoRoot string) error {
	insideWorkTree, err := s.runGitOutput(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("existing workplace is not a git worktree: %s", dir)
	}

	if strings.TrimSpace(insideWorkTree) != "true" {
		return fmt.Errorf("existing workplace is not a git worktree: %s", dir)
	}

	branch, err := s.runGitOutput(ctx, dir, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("resolve workplace branch: %w", err)
	}

	branch = strings.TrimSpace(branch)
	if branch != expectedBranch {
		return fmt.Errorf("existing workplace branch mismatch: expected %q, got %q", expectedBranch, branch)
	}

	expectedOriginURL, err := s.runGitOutput(ctx, expectedRepoRoot, "config", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("resolve repository origin url: %w", err)
	}

	workplaceOriginURL, err := s.runGitOutput(ctx, dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("resolve workplace origin url: %w", err)
	}

	if strings.TrimSpace(workplaceOriginURL) != strings.TrimSpace(expectedOriginURL) {
		return fmt.Errorf("existing workplace repository mismatch: expected %q, got %q", strings.TrimSpace(expectedOriginURL), strings.TrimSpace(workplaceOriginURL))
	}

	return nil
}

type repositoryRef struct {
	CloneURL string
	CacheKey string
}

func (s *Service) materializeRepository(ctx context.Context, hostRepoRoot string, repository repositoryRef) (string, error) {
	cacheDir := filepath.Join(hostRepoRoot, ".progress", "repositories", repository.CacheKey)
	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return "", fmt.Errorf("create repository cache parent directory: %w", err)
	}

	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		if err := s.runGit(ctx, hostRepoRoot, "clone", repository.CloneURL, cacheDir); err != nil {
			return "", fmt.Errorf("clone repository %q: %w", repository.CloneURL, err)
		}
		return cacheDir, nil
	} else if err != nil {
		return "", fmt.Errorf("check repository cache directory: %w", err)
	}

	insideWorkTree, err := s.runGitOutput(ctx, cacheDir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(insideWorkTree) != "true" {
		return "", fmt.Errorf("repository cache is not a git repository: %s", cacheDir)
	}

	originURL, err := s.runGitOutput(ctx, cacheDir, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", fmt.Errorf("resolve repository cache origin url: %w", err)
	}
	if strings.TrimSpace(originURL) != repository.CloneURL {
		return "", fmt.Errorf("repository cache origin mismatch: expected %q, got %q", repository.CloneURL, strings.TrimSpace(originURL))
	}

	if err := s.runGit(ctx, cacheDir, "fetch", "origin"); err != nil {
		return "", fmt.Errorf("fetch repository cache %q: %w", repository.CloneURL, err)
	}

	return cacheDir, nil
}

func normalizeRepositoryRef(raw string) (*repositoryRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.ContainsAny(raw, "\r\n\t") {
		return nil, fmt.Errorf("repository ref is invalid: %q", raw)
	}

	if owner, name, ok := parseRepositoryShorthand(raw); ok {
		return &repositoryRef{CloneURL: githubCloneURL(owner, name), CacheKey: repositoryCacheKey(owner, name)}, nil
	}

	if owner, name, ok := parseGitHubSSHRef(raw); ok {
		return &repositoryRef{CloneURL: githubCloneURL(owner, name), CacheKey: repositoryCacheKey(owner, name)}, nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("repository ref is invalid: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Host, "github.com") {
		return nil, fmt.Errorf("repository ref is invalid: %q", raw)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return nil, fmt.Errorf("repository ref is invalid: %q", raw)
	}

	owner, name, ok := parseRepositoryPath(parsed.EscapedPath())
	if !ok {
		return nil, fmt.Errorf("repository ref is invalid: %q", raw)
	}

	return &repositoryRef{CloneURL: githubCloneURL(owner, name), CacheKey: repositoryCacheKey(owner, name)}, nil
}

func parseRepositoryShorthand(raw string) (string, string, bool) {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "git@") {
		return "", "", false
	}
	return parseRepositoryPath(raw)
}

func parseGitHubSSHRef(raw string) (string, string, bool) {
	const prefix = "git@github.com:"
	if !strings.HasPrefix(raw, prefix) {
		return "", "", false
	}
	return parseRepositoryPath(strings.TrimPrefix(raw, prefix))
}

func parseRepositoryPath(raw string) (string, string, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	raw = strings.TrimSuffix(raw, ".git")
	parts := strings.Split(raw, "/")
	if len(parts) != 2 || !isRepositoryPart(parts[0]) || !isRepositoryPart(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isRepositoryPart(value string) bool {
	if value == "" || value == "." || value == ".." || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsAny(value, `/\\ `)
}

func githubCloneURL(owner, name string) string {
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, name)
}

func repositoryCacheKey(owner, name string) string {
	owner = strings.ToLower(owner)
	name = strings.ToLower(name)
	return fmt.Sprintf("github-%d-%s-%s", len(owner), owner, name)
}

func runGit(ctx context.Context, dir string, args ...string) error {
	_, err := runGitOutput(ctx, dir, args...)
	return err
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}
