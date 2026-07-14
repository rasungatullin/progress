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

func TestRebaseAllowsLeaseCaptureBeforeConflictResolution(t *testing.T) {
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
		case "rev-parse --verify FETCH_HEAD":
			return "fedcba9876543210fedcba9876543210fedcba98", nil
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", HeadRef: "feature", ForceWithLease: true, AllowConflict: true, Git: &model.GitConfig{Push: &model.GitPushConfig{AllowForceWithLease: true}}}
	if err := (builtinOperationExecutor{service: service}).rebase(context.Background(), rebaseTestState(service, input), rebaseTestOperation(input), "rebase"); err != nil {
		t.Fatalf("rebase preparation: %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "push ") {
		t.Fatalf("conflict preparation must not push: %v", calls)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "fetch origin main") || !strings.Contains(joined, "fetch origin feature") {
		t.Fatalf("conflict preparation must refresh both base and head refs: %v", calls)
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

func TestRebaseRejectsConfiguredProtectedBranchWhenBaseRefIsSHA(t *testing.T) {
	var calls []string
	service := &Service{runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "rev-parse --is-inside-work-tree":
			return "true", nil
		case "branch --show-current":
			return "master", nil
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "0123456789abcdef0123456789abcdef01234567", HeadRef: "master", ProtectedRef: "master"}
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "rebase base branch") {
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

func TestRebaseAbortsAfterCanceledRebaseWithIndependentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var abortContextErr error
	service := &Service{runGitOutput: func(callCtx context.Context, _ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch call {
		case "rev-parse --is-inside-work-tree":
			return "true", nil
		case "branch --show-current":
			return "feature", nil
		case "rebase -- FETCH_HEAD":
			cancel()
			return "", context.Canceled
		case "rebase --abort":
			abortContextErr = callCtx.Err()
			return "", nil
		default:
			return "", nil
		}
	}}
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", HeadRef: "feature"}
	err := (builtinOperationExecutor{service: service}).rebase(ctx, rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled rebase refusal, got %v", err)
	}
	if abortContextErr != nil {
		t.Fatalf("abort must use an independent context, got %v", abortContextErr)
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

func TestRebaseRejectsProtectedBranchWhenBaseRefIsSHAAndHeadRefIsMain(t *testing.T) {
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
	input := model.RebaseInput{Directory: "/tmp/work", BaseRef: "0123456789abcdef0123456789abcdef01234567", HeadRef: "main"}
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "base branch") {
		t.Fatalf("expected protected-branch refusal, got %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "fetch") {
		t.Fatalf("protected branch must not fetch: %v", calls)
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

func TestRebaseForceWithLeaseRejectsConcurrentRemoteUpdate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	work := filepath.Join(root, "work")
	gitTestCommand(t, ctx, "", "init", "--bare", remote)
	gitTestCommand(t, ctx, "", "init", "-b", "main", seed)
	gitTestCommand(t, ctx, seed, "config", "user.name", "Test")
	gitTestCommand(t, ctx, seed, "config", "user.email", "test@example.com")
	writeGitTestFile(t, filepath.Join(seed, "base.txt"), "base\n")
	gitTestCommand(t, ctx, seed, "add", ".")
	gitTestCommand(t, ctx, seed, "commit", "-m", "base")
	gitTestCommand(t, ctx, seed, "remote", "add", "origin", remote)
	gitTestCommand(t, ctx, seed, "push", "-u", "origin", "main")
	gitTestCommand(t, ctx, seed, "checkout", "-b", "feature")
	writeGitTestFile(t, filepath.Join(seed, "feature.txt"), "feature\n")
	gitTestCommand(t, ctx, seed, "add", ".")
	gitTestCommand(t, ctx, seed, "commit", "-m", "feature")
	gitTestCommand(t, ctx, seed, "push", "-u", "origin", "feature")
	gitTestCommand(t, ctx, "", "clone", remote, work)
	gitTestCommand(t, ctx, work, "checkout", "feature")
	gitTestCommand(t, ctx, work, "config", "user.name", "Test")
	gitTestCommand(t, ctx, work, "config", "user.email", "test@example.com")

	var expectedOID, competitorOID string
	service := &Service{runGitOutput: func(ctx context.Context, dir string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		output, err := runGitOutput(ctx, dir, args...)
		if call == "rev-parse --verify refs/remotes/origin/feature" && err == nil && competitorOID == "" {
			expectedOID = strings.TrimSpace(output)
			writeGitTestFile(t, filepath.Join(seed, "competitor.txt"), "competitor\n")
			gitTestCommand(t, ctx, seed, "add", ".")
			gitTestCommand(t, ctx, seed, "commit", "-m", "competitor")
			competitorOID = strings.TrimSpace(mustGitTestOutput(t, ctx, seed, "rev-parse", "HEAD"))
			gitTestCommand(t, ctx, seed, "push", "origin", "feature")
		}
		return output, err
	}}
	input := model.RebaseInput{Directory: work, BaseRef: "main", HeadRef: "feature", Push: true, ForceWithLease: true, Git: &model.GitConfig{Push: &model.GitPushConfig{AllowForceWithLease: true}}}
	err := (builtinOperationExecutor{service: service}).rebase(ctx, rebaseTestState(service, input), rebaseTestOperation(input), "rebase")
	if err == nil || !strings.Contains(err.Error(), "rebase_push_failed") && !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected force-with-lease refusal, got %v", err)
	}
	if expectedOID == "" || competitorOID == "" || expectedOID == competitorOID {
		t.Fatalf("concurrent update was not established: expected=%q competitor=%q", expectedOID, competitorOID)
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

func mustGitTestOutput(t *testing.T, ctx context.Context, dir string, args ...string) string {
	t.Helper()
	output, err := runGitOutput(ctx, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return output
}

func rebaseTestOperation(input model.RebaseInput) OperationSpec {
	return OperationSpec{
		Name: "rebase", Kind: OperationKindRebase,
		In: model.OperationMap{
			"directory": rebaseMapping(input.Directory), "base_ref": rebaseMapping(input.BaseRef),
			"head_ref":      rebaseMapping(input.HeadRef),
			"protected_ref": rebaseMapping(input.ProtectedRef),
			"push":          rebaseMapping(input.Push), "force_with_lease": rebaseMapping(input.ForceWithLease), "allow_conflict": rebaseMapping(input.AllowConflict), "git": rebaseMapping(input.Git),
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
