package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/decision"
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

type stubDecisionStarter struct {
	result decision.StartResult
	err    error
}

func (s stubDecisionStarter) Start(_ context.Context, _ decision.StartInput) (decision.StartResult, error) {
	return s.result, s.err
}
