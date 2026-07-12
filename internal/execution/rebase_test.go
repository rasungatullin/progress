package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestRebaseFetchesBaseRebasesAndUsesForceWithLeaseOnlyWhenAllowed(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "rev-parse --is-inside-work-tree":
			return "true", nil
		case "branch --show-current":
			return "feature", nil
		case "rev-parse --verify refs/remotes/origin/feature":
			return "0123456789abcdef0123456789abcdef01234567", nil
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", HeadRef: "feature", Push: true, ForceWithLease: true, Git: &model.GitConfig{Push: &model.GitPushConfig{AllowForceWithLease: true}}}
	state := rebaseTestState(service, input)

	if err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(input), "rebase"); err != nil {
		t.Fatalf("rebase: %v", err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "fetch origin main") || !strings.Contains(joined, "rebase -- FETCH_HEAD") || !strings.Contains(joined, "push --force-with-lease=refs/heads/feature:0123456789abcdef0123456789abcdef01234567 origin HEAD:feature") {
		t.Fatalf("unexpected git sequence: %v", calls)
	}
	if !strings.Contains(state.data["rebase_summary"].(string), "force-with-lease") {
		t.Fatalf("rebase summary does not record push policy: %#v", state.data)
	}
}

func TestRebaseRejectsDirtyWorktreeAndDoesNotFetch(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if strings.HasPrefix(strings.Join(args, " "), "status ") {
			return " M file.txt\x00", nil
		}
		return "true", nil
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", HeadRef: "feature"}
	state := rebaseTestState(service, input)
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("expected dirty-worktree refusal, got %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "fetch") {
		t.Fatalf("dirty worktree must not fetch: %v", calls)
	}
}

func TestRebaseRejectsProtectedBranchWhenBaseRefIsSHA(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "rev-parse --is-inside-work-tree":
			return "true", nil
		case "branch --show-current":
			return "main", nil
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "0123456789abcdef0123456789abcdef01234567", HeadRef: "feature"}
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "workplace branch") {
		t.Fatalf("expected protected-branch refusal, got %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "fetch") {
		t.Fatalf("protected branch must not fetch: %v", calls)
	}
}

func TestRebaseRequiresHeadBranch(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "true", nil
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main"}
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "head ref is required") {
		t.Fatalf("expected head-branch requirement, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("missing head branch must fail before Git access: %v", calls)
	}
}

func TestRebaseAbortsAfterConflict(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "rev-parse --is-inside-work-tree":
			return "true", nil
		case "branch --show-current":
			return "feature", nil
		case "rev-parse --verify refs/remotes/origin/feature":
			return "0123456789abcdef0123456789abcdef01234567", nil
		case "rebase -- FETCH_HEAD":
			return "", errors.New("conflict")
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", HeadRef: "feature"}
	state := rebaseTestState(service, input)
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict refusal, got %v", err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "rebase --abort") {
		t.Fatalf("conflict must abort rebase: %v", calls)
	}
}

func TestRebaseRejectsBaseBranch(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "rev-parse --is-inside-work-tree":
			return "true", nil
		case "branch --show-current":
			return "main", nil
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", HeadRef: "main"}
	state := rebaseTestState(service, input)
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "base branch") {
		t.Fatalf("expected base-branch refusal, got %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "fetch") {
		t.Fatalf("base branch must not fetch: %v", calls)
	}
}

func TestRebaseConflictAbortsInTemporaryRepository(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	work := filepath.Join(root, "work")
	gitTestCommand(t, ctx, "", "init", "--bare", remote)
	gitTestCommand(t, ctx, "", "init", "-b", "main", seed)
	gitTestCommand(t, ctx, seed, "config", "user.name", "Test")
	gitTestCommand(t, ctx, seed, "config", "user.email", "test@example.com")
	writeGitTestFile(t, filepath.Join(seed, "conflict.txt"), "base\n")
	gitTestCommand(t, ctx, seed, "add", ".")
	gitTestCommand(t, ctx, seed, "commit", "-m", "base")
	gitTestCommand(t, ctx, seed, "remote", "add", "origin", remote)
	gitTestCommand(t, ctx, seed, "push", "-u", "origin", "main")
	gitTestCommand(t, ctx, seed, "checkout", "-b", "feature")
	writeGitTestFile(t, filepath.Join(seed, "conflict.txt"), "feature\n")
	gitTestCommand(t, ctx, seed, "commit", "-am", "feature")
	gitTestCommand(t, ctx, seed, "push", "-u", "origin", "feature")
	gitTestCommand(t, ctx, "", "clone", remote, work)
	gitTestCommand(t, ctx, work, "checkout", "feature")
	gitTestCommand(t, ctx, work, "config", "user.name", "Test")
	gitTestCommand(t, ctx, work, "config", "user.email", "test@example.com")
	gitTestCommand(t, ctx, seed, "checkout", "main")
	writeGitTestFile(t, filepath.Join(seed, "conflict.txt"), "main\n")
	gitTestCommand(t, ctx, seed, "commit", "-am", "main")
	gitTestCommand(t, ctx, seed, "push", "origin", "main")

	service := &Service{runGitOutput: runGitOutput}
	input := model.RebaseInput{Directory: work, BaseRef: "main", HeadRef: "feature"}
	err := (builtinOperationExecutor{service: service}).rebase(ctx, rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict refusal, got %v", err)
	}
	status, err := runGitOutput(ctx, work, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) != "" {
		t.Fatalf("conflicting rebase must leave a clean worktree, status=%q err=%v", status, err)
	}
	branch, err := runGitOutput(ctx, work, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) != "feature" {
		t.Fatalf("conflicting rebase must preserve feature branch, branch=%q err=%v", branch, err)
	}
}

func gitTestCommand(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writeGitTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func rebaseTestOperation(input model.RebaseInput) OperationSpec {
	return OperationSpec{
		Name: "rebase", Kind: OperationKindRebase,
		In: model.OperationMap{
			"directory": rebaseMapping(input.Directory), "base_ref": rebaseMapping(input.BaseRef),
			"head_ref": rebaseMapping(input.HeadRef),
			"push":     rebaseMapping(input.Push), "force_with_lease": rebaseMapping(input.ForceWithLease), "git": rebaseMapping(input.Git),
		},
		Out: model.OperationMap{"rebase_summary": {Ref: "data.rebase_summary"}},
	}
}

func rebaseTestState(service *Service, input model.RebaseInput) *operationExecution {
	return &operationExecution{
		data:    map[string]any{"allocation": allocation{Git: input.Git}},
		tracker: newOperationTracker(Action{Operations: []OperationSpec{rebaseTestOperation(input)}}),
	}
}

func rebaseMapping(value any) model.OperationMapping {
	payload, _ := json.Marshal(value)
	return model.OperationMapping{Value: payload}
}
