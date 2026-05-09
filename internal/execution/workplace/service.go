package workplace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

type Service struct {
	runGit func(context.Context, string, ...string) error
}

func NewService() *Service {
	return &Service{runGit: runGit}
}

func (s *Service) Prepare(ctx context.Context, in model.Invocation, profile model.Profile, _ model.Allocation) (model.Workplace, error) {
	if in.Launch.Directory != "" {
		info, err := os.Stat(in.Launch.Directory)
		if err != nil {
			return model.Workplace{}, err
		}

		if !info.IsDir() {
			return model.Workplace{}, fmt.Errorf("execution directory is not a folder: %s", in.Launch.Directory)
		}

		return model.Workplace{Name: in.Launch.Directory, Ready: true}, nil
	}

	name := strings.TrimSpace(in.Workplace.Name)

	if err := validateWorkplaceName(name); err != nil {
		return model.Workplace{}, err
	}

	repoRoot, err := resolveRepoRoot(ctx)
	if err != nil {
		return model.Workplace{}, err
	}

	baseBranch, err := resolveOriginDefaultBranch(ctx, repoRoot)
	if err != nil {
		return model.Workplace{}, err
	}

	targetDir := filepath.Join(repoRoot, ".progress", "workplaces", name)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return model.Workplace{}, fmt.Errorf("create workplace parent directory: %w", err)
	}

	if info, err := os.Stat(targetDir); err == nil {
		if !info.IsDir() {
			return model.Workplace{}, fmt.Errorf("workplace path is not a folder: %s", targetDir)
		}

		if err := validateExistingWorkplace(ctx, targetDir, name); err != nil {
			return model.Workplace{}, err
		}

		return model.Workplace{Name: targetDir, Ready: true}, nil
	} else if !os.IsNotExist(err) {
		return model.Workplace{}, fmt.Errorf("check workplace directory: %w", err)
	}

	if err := s.runGit(ctx, repoRoot, "fetch", "origin", baseBranch); err != nil {
		return model.Workplace{}, fmt.Errorf("fetch origin/%s: %w", baseBranch, err)
	}

	if err := s.runGit(ctx, repoRoot, "worktree", "add", "-b", name, targetDir, "FETCH_HEAD"); err != nil {
		return model.Workplace{}, fmt.Errorf("create git worktree %q: %w", name, err)
	}

	return model.Workplace{Name: targetDir, Ready: true}, nil
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

func resolveOriginDefaultBranch(ctx context.Context, dir string) (string, error) {
	output, err := runGitOutput(ctx, dir, "symbolic-ref", "refs/remotes/origin/HEAD")
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

func validateExistingWorkplace(ctx context.Context, dir, expectedBranch string) error {
	insideWorkTree, err := runGitOutput(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("existing workplace is not a git worktree: %s", dir)
	}

	if strings.TrimSpace(insideWorkTree) != "true" {
		return fmt.Errorf("existing workplace is not a git worktree: %s", dir)
	}

	branch, err := runGitOutput(ctx, dir, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("resolve workplace branch: %w", err)
	}

	branch = strings.TrimSpace(branch)
	if branch != expectedBranch {
		return fmt.Errorf("existing workplace branch mismatch: expected %q, got %q", expectedBranch, branch)
	}

	return nil
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
