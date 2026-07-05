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

type stubReactivityService struct {
	processResult reactivity.TaskProcessingResult
	actionResult  reactivity.TaskProcessingResult
	processErr    error
	actionErr     error
	processInput  reactivity.TaskProcessingInput
	actionInput   reactivity.TaskActionInput
}

func (s *stubReactivityService) ProcessTask(_ context.Context, input reactivity.TaskProcessingInput) (reactivity.TaskProcessingResult, error) {
	s.processInput = input
	return s.processResult, s.processErr
}

func (s *stubReactivityService) RunTaskAction(_ context.Context, input reactivity.TaskActionInput) (reactivity.TaskProcessingResult, error) {
	s.actionInput = input
	return s.actionResult, s.actionErr
}
