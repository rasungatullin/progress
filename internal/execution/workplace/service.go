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
		if strings.TrimSpace(in.Repository.URL) != "" {
			return model.Workplace{}, fmt.Errorf("local environment cannot use repository url: %s", in.Repository.URL)
		}

		return model.Workplace{Name: in.Launch.Directory, BaseRef: in.Workplace.BaseRef, HeadRef: in.Workplace.HeadRef, Environment: environment, EnvironmentType: environmentType, Ready: true}, nil
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
		if strings.TrimSpace(in.Repository.URL) != "" {
			return model.Workplace{}, fmt.Errorf("local environment cannot use repository url: %s", in.Repository.URL)
		}
		hostRepoRoot, err := s.resolveRepoRoot(ctx)
		if err != nil {
			return model.Workplace{}, err
		}
		return model.Workplace{Name: hostRepoRoot, BaseRef: in.Workplace.BaseRef, HeadRef: in.Workplace.HeadRef, Environment: environment, EnvironmentType: environmentType, RepositoryRoot: hostRepoRoot, Ready: true}, nil
	}
	if environmentType != configuration.EnvironmentTypeWorktree {
		return model.Workplace{}, fmt.Errorf("execution environment is unsupported by workplace preparation: %s", environment)
	}

	name := strings.TrimSpace(in.Workplace.Name)

	if err := validateWorkplaceName(name); err != nil {
		return model.Workplace{}, err
	}
	branchName := strings.TrimSpace(in.Workplace.HeadRef)
	requireHeadBranch := branchName != ""
	if branchName == "" {
		branchName = name
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
	useRepositoryCache := repoSource != nil
	if repoSource != nil {
		repositoryURL = repoSource.CloneURL
		if s.repositoryMatchesHost(ctx, hostRepoRoot, *repoSource) {
			useRepositoryCache = false
		} else {
			repoRoot, err = s.materializeRepository(ctx, hostRepoRoot, *repoSource)
			if err != nil {
				return model.Workplace{}, err
			}
		}
	}

	baseBranch := strings.TrimSpace(in.Workplace.BaseRef)
	if baseBranch == "" {
		baseBranch, err = s.resolveOriginDefaultBranch(ctx, repoRoot)
		if err != nil {
			return model.Workplace{}, err
		}
	}

	targetDir := filepath.Join(repoRoot, ".progress", "workplaces", name)
	if repoSource != nil && useRepositoryCache {
		targetDir = filepath.Join(hostRepoRoot, ".progress", "workplaces", repoSource.CacheKey, name)
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return model.Workplace{}, fmt.Errorf("create workplace parent directory: %w", err)
	}

	if info, err := os.Stat(targetDir); err == nil {
		if !info.IsDir() {
			return model.Workplace{}, fmt.Errorf("workplace path is not a folder: %s", targetDir)
		}

		if err := s.validateExistingWorkplace(ctx, targetDir, branchName, repoRoot); err != nil {
			return model.Workplace{}, err
		}
		if err := s.synchronizeExistingWorkplace(ctx, targetDir, branchName); err != nil {
			return model.Workplace{}, err
		}

		return model.Workplace{Name: targetDir, BaseRef: baseBranch, HeadRef: branchName, Environment: environment, EnvironmentType: environmentType, RepositoryURL: repositoryURL, RepositoryRoot: repoRoot, Ready: true}, nil
	} else if !os.IsNotExist(err) {
		return model.Workplace{}, fmt.Errorf("check workplace directory: %w", err)
	}

	if err := s.runGit(ctx, repoRoot, "fetch", "origin", baseBranch); err != nil {
		return model.Workplace{}, fmt.Errorf("fetch origin/%s: %w", baseBranch, err)
	}
	headFetchErr := s.fetchRemoteBranch(ctx, repoRoot, branchName)
	headFetched := headFetchErr == nil

	addArgs := []string{"worktree", "add", "-b", branchName, targetDir, "origin/" + baseBranch}
	if s.localBranchExists(ctx, repoRoot, branchName) {
		addArgs = []string{"worktree", "add", targetDir, branchName}
	} else if s.remoteBranchExists(ctx, repoRoot, branchName) {
		addArgs = []string{"worktree", "add", "-b", branchName, targetDir, "origin/" + branchName}
	} else if requireHeadBranch && headFetched {
		addArgs = []string{"worktree", "add", "-b", branchName, targetDir, "FETCH_HEAD"}
	} else if requireHeadBranch {
		if headFetchErr != nil {
			return model.Workplace{}, fmt.Errorf("head branch %q is not available for workplace preparation: %w", branchName, headFetchErr)
		}
		return model.Workplace{}, fmt.Errorf("head branch %q is not available for workplace preparation", branchName)
	}
	if err := s.runGit(ctx, repoRoot, addArgs...); err != nil {
		return model.Workplace{}, fmt.Errorf("create git worktree %q: %w", branchName, err)
	}
	if headFetched {
		if err := s.rebaseWorkplaceBranchOnRemote(ctx, targetDir, branchName); err != nil {
			return model.Workplace{}, fmt.Errorf("synchronize workplace branch %q: %w", branchName, err)
		}
	}

	return model.Workplace{Name: targetDir, BaseRef: baseBranch, HeadRef: branchName, Environment: environment, EnvironmentType: environmentType, RepositoryURL: repositoryURL, RepositoryRoot: repoRoot, Ready: true}, nil
}

func (s *Service) synchronizeExistingWorkplace(ctx context.Context, directory, branch string) error {
	if err := s.fetchRemoteBranch(ctx, directory, branch); err != nil {
		return fmt.Errorf("fetch origin/%s: %w", branch, err)
	}
	if err := s.rebaseWorkplaceBranchOnRemote(ctx, directory, branch); err != nil {
		return fmt.Errorf("rebase on origin/%s: %w", branch, err)
	}
	return nil
}

func (s *Service) rebaseWorkplaceBranchOnRemote(ctx context.Context, directory, branch string) error {
	remoteRef := "refs/remotes/origin/" + branch
	if _, err := s.runGitOutput(ctx, directory, "rebase", "--", remoteRef); err == nil {
		return nil
	} else {
		paths, pathsErr := s.runGitOutput(ctx, directory, "diff", "--name-only", "--diff-filter=U")
		if pathsErr != nil || strings.TrimSpace(paths) == "" {
			_, abortErr := s.runGitOutput(ctx, directory, "rebase", "--abort")
			if abortErr != nil {
				err = fmt.Errorf("%w; additionally failed to abort rebase: %v", err, abortErr)
			}
			if pathsErr != nil {
				err = fmt.Errorf("%w; additionally failed to inspect conflict paths: %v", err, pathsErr)
			}
			return err
		}
		if _, abortErr := s.runGitOutput(ctx, directory, "rebase", "--abort"); abortErr != nil {
			return fmt.Errorf("%w; additionally failed to abort rebase: %v", err, abortErr)
		}
		if _, resetErr := s.runGitOutput(ctx, directory, "reset", "--hard", remoteRef); resetErr != nil {
			return fmt.Errorf("%w; additionally failed to reset to remote branch: %v", err, resetErr)
		}
		return nil
	}
}

func (s *Service) localBranchExists(ctx context.Context, dir string, name string) bool {
	_, err := s.runGitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

func (s *Service) remoteBranchExists(ctx context.Context, dir string, name string) bool {
	_, err := s.runGitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name)
	return err == nil
}

func (s *Service) fetchRemoteBranch(ctx context.Context, dir string, name string) error {
	_, err := s.runGitOutput(ctx, dir, "fetch", "origin", name)
	return err
}

func selectedEnvironment(in model.Invocation, allocation model.Allocation) (string, string) {
	requestedEnvironment := strings.TrimSpace(in.Workplace.Environment)
	allocatedEnvironment := strings.TrimSpace(allocation.Environment)
	allocatedEnvironmentType := strings.TrimSpace(allocation.EnvironmentType)
	if requestedEnvironment != "" {
		if allocatedEnvironmentType != "" && (allocatedEnvironment == "" || allocatedEnvironment == requestedEnvironment) {
			return requestedEnvironment, allocatedEnvironmentType
		}
		return requestedEnvironment, environmentTypeFromName(requestedEnvironment)
	}
	if allocatedEnvironmentType == "" {
		allocatedEnvironmentType = environmentTypeFromName(allocatedEnvironment)
	}
	return allocatedEnvironment, allocatedEnvironmentType
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

func (s *Service) repositoryMatchesHost(ctx context.Context, hostRepoRoot string, repository repositoryRef) bool {
	originURL, err := s.runGitOutput(ctx, hostRepoRoot, "config", "--get", "remote.origin.url")
	if err != nil {
		return false
	}

	hostRepository, err := normalizeRepositoryRef(originURL)
	if err != nil || hostRepository == nil {
		return false
	}

	return hostRepository.CacheKey == repository.CacheKey
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
