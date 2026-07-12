package execution

import (
	"context"
	"encoding/json"
	"errors"
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
		default:
			return "", nil
		}
	}}
	state := rebaseTestState(service, model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", Push: true, ForceWithLease: true, Git: &model.GitConfig{Push: &model.GitPushConfig{AllowForceWithLease: true}}})

	if err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(model.RebaseInput{Directory: "/tmp/work", BaseRef: "main", Push: true, ForceWithLease: true, Git: &model.GitConfig{Push: &model.GitPushConfig{AllowForceWithLease: true}}}), "rebase"); err != nil {
		t.Fatalf("rebase: %v", err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "fetch origin main") || !strings.Contains(joined, "rebase -- FETCH_HEAD") || !strings.Contains(joined, "push --force-with-lease origin HEAD:feature") {
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
	state := rebaseTestState(service, model.RebaseInput{Directory: "/tmp/work", BaseRef: "main"})
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(model.RebaseInput{Directory: "/tmp/work", BaseRef: "main"}), "rebase")
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("expected dirty-worktree refusal, got %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "fetch") {
		t.Fatalf("dirty worktree must not fetch: %v", calls)
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
		case "rebase -- FETCH_HEAD":
			return "", errors.New("conflict")
		default:
			return "", nil
		}
	}}
	state := rebaseTestState(service, model.RebaseInput{Directory: "/tmp/work", BaseRef: "main"})
	err := (builtinOperationExecutor{service: service}).rebase(context.Background(), state, rebaseTestOperation(model.RebaseInput{Directory: "/tmp/work", BaseRef: "main"}), "rebase")
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict refusal, got %v", err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "rebase --abort") {
		t.Fatalf("conflict must abort rebase: %v", calls)
	}
}

func rebaseTestOperation(input model.RebaseInput) OperationSpec {
	return OperationSpec{
		Name: "rebase", Kind: OperationKindRebase,
		In: model.OperationMap{
			"directory": rebaseMapping(input.Directory), "base_ref": rebaseMapping(input.BaseRef),
			"push": rebaseMapping(input.Push), "force_with_lease": rebaseMapping(input.ForceWithLease), "git": rebaseMapping(input.Git),
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
