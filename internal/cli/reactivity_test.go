package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/reactivity"
	"github.com/spf13/cobra"
)

func TestReactivityProcessCommandPrintsCycles(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"reactivity", "process", "--task", "123", "--once"})

	setReactivityServiceFactory(cmd, func(*cobra.Command) reactivityCommandService {
		return &stubReactivityService{processResult: reactivity.TaskProcessingResult{
			TaskNumber: 123,
			StopReason: reactivity.StopReasonSingleCycle,
			Cycles: []reactivity.TaskProcessingCycle{{
				Index: 1,
				Issue: &integration.TrackerIssue{
					Number: 123,
					Title:  "Task",
					State:  "OPEN",
					Labels: []string{"Тестовая задача"},
				},
				Action: execution.ActionStartImplementationPR,
				ExecutionResult: &execution.ExecutionResult{
					Status: "completed",
					Action: execution.Action{Name: execution.ActionStartImplementationPR},
				},
				Execution:    &execution.LaunchResult{Status: "completed"},
				LabelChanges: []reactivity.LabelChange{{Operation: "add", Labels: []string{reactivity.LabelAwaitingReview}}},
			}},
			FinalIssue: &integration.TrackerIssue{Number: 123, Labels: []string{"Тестовая задача", reactivity.LabelAwaitingReview}},
		}}
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute reactivity process: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"task=123\n",
		"completed=false\n",
		"stop-reason=single-cycle\n",
		"cycle=1\n",
		"cycle-action=start-implementation-pr\n",
		"cycle-execution-result-status=completed\n",
		"cycle-label-add=Ожидает экспертизы\n",
		"final-issue-labels=Тестовая задача,Ожидает экспертизы\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("reactivity process output must include %q, got %q", fragment, output)
		}
	}
}

func TestReactivityProcessCommandPassesExplicitRoute(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"reactivity", "process", "--task", "123", "--route", "pull-request-review", "--once"})

	stub := &stubReactivityService{processResult: reactivity.TaskProcessingResult{TaskNumber: 123}}
	setReactivityServiceFactory(cmd, func(*cobra.Command) reactivityCommandService { return stub })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute reactivity process: %v", err)
	}
	if stub.processInput.TaskNumber != 123 || stub.processInput.Route != "pull-request-review" || !stub.processInput.Once {
		t.Fatalf("unexpected process input: %#v", stub.processInput)
	}
}

func TestReactivityProcessRunCommandPassesNameAndOnce(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"reactivity", "process", "run", "--name", "ready-implementation-cycle", "--once"})

	stub := &stubReactivityService{runResult: reactivity.ProcessRunResult{
		ProcessName: "ready-implementation-cycle",
		Completed:   true,
		StopReason:  reactivity.ProcessStopReasonSingleCycle,
		Cycles: []reactivity.ProcessRunCycleResult{{
			Index:          1,
			FoundTasks:     []int{123, 124},
			ProcessedTasks: []reactivity.TaskProcessingResult{{TaskNumber: 123}, {TaskNumber: 124}},
		}},
	}}
	setReactivityServiceFactory(cmd, func(*cobra.Command) reactivityCommandService { return stub })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute reactivity process run: %v", err)
	}
	if stub.runInput.Name != "ready-implementation-cycle" || !stub.runInput.Once || stub.runInput.WaitOnce {
		t.Fatalf("unexpected run input: %#v", stub.runInput)
	}
	for _, fragment := range []string{
		"process=ready-implementation-cycle\n",
		"stop-reason=single-cycle\n",
		"process-cycle=1\n",
		"process-cycle-found-tasks=2\n",
		"process-cycle-task-numbers=123,124\n",
		"process-cycle-processed-task-numbers=123,124\n",
	} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("process run output must include %q, got %q", fragment, stdout.String())
		}
	}
}

func TestReactivityActionCommandPassesExplicitAction(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"reactivity", "action", "--task", "123", "--action", execution.ActionReviewPullRequest})

	stub := &stubReactivityService{actionResult: reactivity.TaskProcessingResult{
		TaskNumber: 123,
		StopReason: reactivity.StopReasonSingleCycle,
		Cycles: []reactivity.TaskProcessingCycle{{
			Index:  1,
			Action: execution.ActionReviewPullRequest,
		}},
	}}
	setReactivityServiceFactory(cmd, func(*cobra.Command) reactivityCommandService { return stub })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute reactivity action: %v", err)
	}
	if stub.actionInput.TaskNumber != 123 || stub.actionInput.Action != execution.ActionReviewPullRequest {
		t.Fatalf("unexpected action input: %#v", stub.actionInput)
	}
	if !strings.Contains(stdout.String(), "cycle-action=review-pull-request\n") {
		t.Fatalf("expected action output, got %q", stdout.String())
	}
}

func TestReactivityCommandsPassCommandContext(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "command-context")

	cmd := NewRootCommand()
	cmd.SetContext(ctx)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"reactivity", "process", "--task", "123", "--once"})
	processStub := &stubReactivityService{processResult: reactivity.TaskProcessingResult{TaskNumber: 123}}
	setReactivityServiceFactory(cmd, func(*cobra.Command) reactivityCommandService { return processStub })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute reactivity process: %v", err)
	}
	if got := processStub.processCtx.Value(contextKey{}); got != "command-context" {
		t.Fatalf("process command must pass command context, got %#v", got)
	}

	cmd = NewRootCommand()
	cmd.SetContext(ctx)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"reactivity", "action", "--task", "123", "--action", execution.ActionReviewPullRequest})
	actionStub := &stubReactivityService{actionResult: reactivity.TaskProcessingResult{TaskNumber: 123}}
	setReactivityServiceFactory(cmd, func(*cobra.Command) reactivityCommandService { return actionStub })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute reactivity action: %v", err)
	}
	if got := actionStub.actionCtx.Value(contextKey{}); got != "command-context" {
		t.Fatalf("action command must pass command context, got %#v", got)
	}
}

type stubReactivityService struct {
	processResult reactivity.TaskProcessingResult
	actionResult  reactivity.TaskProcessingResult
	runResult     reactivity.ProcessRunResult
	processErr    error
	actionErr     error
	runErr        error
	processInput  reactivity.TaskProcessingInput
	actionInput   reactivity.TaskActionInput
	runInput      reactivity.ProcessRunInput
	processCtx    context.Context
	actionCtx     context.Context
	runCtx        context.Context
}

func (s *stubReactivityService) ProcessTask(ctx context.Context, input reactivity.TaskProcessingInput) (reactivity.TaskProcessingResult, error) {
	s.processCtx = ctx
	s.processInput = input
	return s.processResult, s.processErr
}

func (s *stubReactivityService) RunTaskAction(ctx context.Context, input reactivity.TaskActionInput) (reactivity.TaskProcessingResult, error) {
	s.actionCtx = ctx
	s.actionInput = input
	return s.actionResult, s.actionErr
}

func (s *stubReactivityService) RunProcess(ctx context.Context, input reactivity.ProcessRunInput) (reactivity.ProcessRunResult, error) {
	s.runCtx = ctx
	s.runInput = input
	return s.runResult, s.runErr
}
