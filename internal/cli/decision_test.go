package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/decision"
	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
	"github.com/spf13/cobra"
)

func TestDecisionStartCommandPrintsContext(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"decision", "start", "--task", "123"})

	original := decisionServiceFactory
	decisionServiceFactory = func(*cobra.Command) decisionStarter {
		return stubDecisionStarter{result: decision.StartResult{
			Ready: true,
			Context: decision.DecisionContext{
				Signal: decision.Signal{Source: decision.SignalSourceTask, Kind: decision.SignalKindTask, TaskNumber: 123},
				Issue: &integration.TrackerIssue{
					Number: 123,
					Title:  "Implement stage 1",
					State:  "OPEN",
					URL:    "https://github.com/owner/name/issues/123",
				},
			},
			Decision: &decision.Decision{
				Type: decision.DecisionType(decision.DecisionTypeExecute),
				Reasons: []decision.DecisionReason{{
					Code:    "issue_context_ready",
					Message: "Issue-backed decision context is ready for direct execution handoff.",
				}},
				ExecutionPlan: &decision.ExecutionPlan{Profile: "default"},
			},
			Execution: &execution.LaunchResult{Status: "completed", Summary: "profile=default runner=opencode"},
		}}
	}
	t.Cleanup(func() { decisionServiceFactory = original })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute decision start: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"task=123\n",
		"signal-source=task\n",
		"signal-kind=task-number\n",
		"context-ready=true\n",
		"decision-type=execute\n",
		"decision-reason=issue_context_ready:Issue-backed decision context is ready for direct execution handoff.\n",
		"execution-profile=default\n",
		"execution-status=completed\n",
		"execution-summary=profile=default runner=opencode\n",
		"issue-number=123\n",
		"issue-title=Implement stage 1\n",
		"issue-state=OPEN\n",
		"issue-url=https://github.com/owner/name/issues/123\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("decision start output must include %q, got %q", fragment, output)
		}
	}
}

func TestDecisionStartCommandRequiresTaskFlag(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"decision", "start"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing task error")
	}
	if !strings.Contains(err.Error(), "required flag(s) \"task\" not set") {
		t.Fatalf("unexpected missing task error: %v", err)
	}
}

func TestDecisionStartCommandPropagatesServiceError(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"decision", "start", "--task", "42"})

	original := decisionServiceFactory
	decisionServiceFactory = func(*cobra.Command) decisionStarter {
		return stubDecisionStarter{err: assertErr("decision start failed")}
	}
	t.Cleanup(func() { decisionServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected decision service error")
	}
	if err.Error() != "decision start failed" {
		t.Fatalf("unexpected decision service error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("did not expect output on failure, got %q", stdout.String())
	}
}

func TestDecisionStartCommandPrintsPartialResultOnError(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"decision", "start", "--task", "77"})

	original := decisionServiceFactory
	decisionServiceFactory = func(*cobra.Command) decisionStarter {
		return stubDecisionStarter{
			result: decision.StartResult{
				Context: decision.DecisionContext{
					Signal: decision.Signal{Source: decision.SignalSourceTask, Kind: decision.SignalKindTask, TaskNumber: 77},
					Issue:  &integration.TrackerIssue{Number: 77, Title: "Fix execution handoff", State: "OPEN"},
				},
				Ready: true,
				Decision: &decision.Decision{
					Type:          decision.DecisionType(decision.DecisionTypeExecute),
					ExecutionPlan: &decision.ExecutionPlan{Profile: "default"},
				},
				Execution: &execution.LaunchResult{
					Status:  "failed",
					Summary: "Applied the requested changes.",
					StructuredOutput: &execution.StructuredOutput{
						ProtocolVersion: execution.StructuredIOVersion,
						Summary:         "Need follow-up.",
						Remarks: []execution.StructuredRemark{{
							ID:    "remark-1",
							Title: "Rollback plan",
							Body:  "Still missing.",
						}},
					},
				},
			},
			err: assertErr("execution failed"),
		}
	}
	t.Cleanup(func() { decisionServiceFactory = original })

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected decision service error")
	}
	if err.Error() != "execution failed" {
		t.Fatalf("unexpected decision service error: %v", err)
	}

	output := stdout.String()
	for _, fragment := range []string{
		"task=77\n",
		"decision-type=execute\n",
		"execution-status=failed\n",
		"execution-summary=Applied the requested changes.\n",
		"structured-output:\n",
		"protocol-version=review-cycle/v1\n",
		"summary-field=Need follow-up.\n",
		`remark={"id":"remark-1","title":"Rollback plan","body":"Still missing."}` + "\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("decision start output must include %q, got %q", fragment, output)
		}
	}
}

type stubDecisionStarter struct {
	result decision.StartResult
	err    error
}

func (s stubDecisionStarter) Start(_ context.Context, _ decision.StartInput) (decision.StartResult, error) {
	return s.result, s.err
}
