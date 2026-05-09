package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestLaunchCommitPushDisabled(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=disabled") {
		t.Fatalf("summary must include disabled git state: %q", result.Summary)
	}
}

func TestLaunchCommitPushEnabledByProfile(t *testing.T) {
	t.Parallel()

	var calls [][]string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfileWithCommitPush(true), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{{"rev-parse", "--is-inside-work-tree"}, {"branch", "--show-current"}, {"status", "--porcelain"}}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushNoChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	invocation := validInvocation(t, true)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{{"rev-parse", "--is-inside-work-tree"}, {"branch", "--show-current"}, {"status", "--porcelain"}}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushWithChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	statusCalls := 0
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				statusCalls++
				if statusCalls == 1 {
					return " M file.txt\n", nil
				}
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m Apply task result":
				return "[feature/test abc123] Apply task result\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "", nil
			case "push -u origin feature/test":
				return "branch 'feature/test' set up to track 'origin/feature/test'.\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=committed+pushed branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
		{"add", "-A"},
		{"status", "--porcelain"},
		{"commit", "-m", "Apply task result"},
		{"for-each-ref", "--format=%(upstream:short)", "refs/heads/feature/test"},
		{"push", "-u", "origin", "feature/test"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchRunnerErrorSkipsGit(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "", errors.New("launch runner failed: exit status 1")
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not run after runner error")
			return "", nil
		},
	}

	_, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected launch error")
	}
	if !strings.Contains(err.Error(), "launch runner failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLaunchPushErrorReturned(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m Apply task result":
				return "[feature/test abc123] Apply task result\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "", errors.New("exit status 1\nremote rejected")
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	_, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected push error")
	}
	if !strings.Contains(err.Error(), "git push failed") || !strings.Contains(err.Error(), "remote rejected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func validInvocation(t *testing.T, commitPush bool) model.Invocation {
	t.Helper()

	return model.Invocation{
		Launch: model.LaunchSpec{
			Directory:     tempDir(t),
			Runner:        RunnerOpenCode,
			Model:         "openai/gpt-5.4",
			Prompt:        "do work",
			CommitPush:    commitPush,
			CommitMessage: DefaultCommitMessage,
		},
	}
}

func validProfile() model.Profile {
	return validProfileWithCommitPush(false)
}

func validProfileWithCommitPush(commitPush bool) model.Profile {
	return model.Profile{Name: "default", Model: "openai/gpt-5.4", CommitPush: commitPush}
}

func validAllocation() model.Allocation {
	return model.Allocation{Resource: "external-launch", Reserved: true}
}

func validWorkplace(t *testing.T) model.Workplace {
	t.Helper()
	return model.Workplace{Name: tempDir(t), Ready: true}
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir temp repo: %v", err)
	}
	return dir
}
