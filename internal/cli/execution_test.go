package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/spf13/cobra"
)

func TestBindLaunchFlagsAndInvocation(t *testing.T) {
	t.Parallel()

	flags := newLaunchFlags()
	cmd := &cobra.Command{Use: "launch"}
	bindLaunchFlags(cmd, flags)

	err := cmd.ParseFlags([]string{
		"--dir", "/tmp/work",
		"--runner", "opencode",
		"--model", "openai/gpt-5.4",
		"--prompt", "ship it",
		"--structured-output",
		"--structured-protocol", "legacy",
		"--structured-mode", "reply",
		"--structured-output-required",
		"--commit-push",
		"--commit-message", "Custom message",
	})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation := invocationFromLaunchFlags(flags)
	if invocation.Launch.Directory != "/tmp/work" {
		t.Fatalf("unexpected directory: %q", invocation.Launch.Directory)
	}
	if !invocation.Launch.CommitPush {
		t.Fatal("expected commit-push to be enabled")
	}
	if invocation.Launch.CommitMessage != "Custom message" {
		t.Fatalf("unexpected commit message: %q", invocation.Launch.CommitMessage)
	}
	if invocation.Launch.Prompt != "ship it" {
		t.Fatalf("unexpected prompt: %q", invocation.Launch.Prompt)
	}
	if !invocation.Launch.StructuredOutput {
		t.Fatal("expected structured-output to be enabled")
	}
	if invocation.Launch.StructuredProtocol != "legacy" {
		t.Fatalf("unexpected structured protocol: %q", invocation.Launch.StructuredProtocol)
	}
	if invocation.Launch.StructuredMode != "reply" {
		t.Fatalf("unexpected structured mode: %q", invocation.Launch.StructuredMode)
	}
	if !invocation.Launch.StructuredOutputRequired {
		t.Fatal("expected structured-output-required to be enabled")
	}
}

func TestNewLaunchFlagsDefaults(t *testing.T) {
	t.Parallel()

	flags := newLaunchFlags()
	if flags.commitPush {
		t.Fatal("commit-push must be disabled by default")
	}
	if flags.commitMessage != "" {
		t.Fatalf("commit-message flag value should be empty before binding, got %q", flags.commitMessage)
	}

	cmd := &cobra.Command{Use: "launch"}
	bindLaunchFlags(cmd, flags)
	if err := cmd.ParseFlags([]string{"--dir", "/tmp/work", "--prompt", "task"}); err != nil {
		t.Fatalf("parse default flags: %v", err)
	}

	invocation := invocationFromLaunchFlags(flags)
	if invocation.Launch.CommitPush {
		t.Fatal("commit-push must stay disabled by default")
	}
	if invocation.Launch.CommitMessage != launch.DefaultCommitMessage {
		t.Fatalf("unexpected default commit message: %q", invocation.Launch.CommitMessage)
	}
	if invocation.Launch.StructuredProtocol != "" {
		t.Fatalf("unexpected default structured protocol: %q", invocation.Launch.StructuredProtocol)
	}
	if invocation.Launch.StructuredMode != "" {
		t.Fatalf("structured mode must be empty by default: %q", invocation.Launch.StructuredMode)
	}
	if invocation.Launch.StructuredOutput {
		t.Fatal("structured-output must be disabled by default")
	}
	if invocation.Launch.StructuredOutputRequired {
		t.Fatal("structured-output-required must be disabled by default")
	}
}

func TestExecutionLaunchHelpIncludesStructuredFlags(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "launch", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute launch help: %v", err)
	}

	help := stdout.String()
	for _, fragment := range []string{
		"--structured-output",
		"--structured-protocol string",
		"--structured-mode string",
		"--structured-output-required",
		"по умолчанию review-cycle вместе с --structured-output",
	} {
		if !strings.Contains(help, fragment) {
			t.Fatalf("launch help must include %q, got %q", fragment, help)
		}
	}
}

func TestExecutionLaunchRejectsStructuredProtocolWithoutStructuredOutput(t *testing.T) {
	t.Parallel()

	for _, protocol := range []string{"legacy", execution.StructuredProtocolReviewCycle} {
		cmd := NewRootCommand()
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs([]string{"execution", "launch", "--dir", t.TempDir(), "--prompt", "task", "--structured-protocol", protocol})

		err := cmd.Execute()
		if err == nil {
			t.Fatalf("expected launch error for protocol %q", protocol)
		}
		if !strings.Contains(err.Error(), "structured protocol requires structured output") {
			t.Fatalf("unexpected error for protocol %q: %v", protocol, err)
		}
	}
}

func TestExecutionLaunchRejectsStructuredModeWithoutStructuredOutput(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "launch", "--dir", t.TempDir(), "--prompt", "task", "--structured-mode", "review"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected launch error for structured mode without structured-output")
	}
	if !strings.Contains(err.Error(), "structured mode requires structured output") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutionProfileCommandPrintsResolvedProfile(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "profile", "--profile", "default"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute profile command: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "description=Базовый профиль исполнения через облачную модель по умолчанию\n") {
		t.Fatalf("profile output must include description, got %q", output)
	}
	if !strings.Contains(output, "model=openai/gpt-5.4\n") {
		t.Fatalf("profile output must include resolved model, got %q", output)
	}
	if !strings.Contains(output, "commit-push=false\n") {
		t.Fatalf("profile output must include commit-push flag, got %q", output)
	}
}

func TestPrintLaunchResultWithoutStructuredOutput(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "profile=default git=disabled\nApplied the requested changes.",
	})

	output := stdout.String()
	if !strings.Contains(output, "state=completed\n") {
		t.Fatalf("output must include state: %q", output)
	}
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nprofile=default git=disabled\nApplied the requested changes.\nPROGRESS_SUMMARY\n") {
		t.Fatalf("output must include summary: %q", output)
	}
	if strings.Contains(output, "structured-output:\n") {
		t.Fatalf("output must omit structured section when values are absent: %q", output)
	}
	if strings.Contains(output, "critical-remark=") || strings.Contains(output, "minor-remark=") || strings.Contains(output, "question=") {
		t.Fatalf("output must omit empty structured sections: %q", output)
	}
}

func TestPrintLaunchResultWithStructuredOutput(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:          "completed",
		Summary:         "profile=default git=disabled\nApplied the requested changes.",
		CriticalRemarks: []string{"missing rollback plan", "   "},
		MinorRemarks:    []string{"consider renaming helper"},
		Questions:       []string{"should we add an integration test?"},
	})

	output := stdout.String()
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nprofile=default git=disabled\nApplied the requested changes.\nPROGRESS_SUMMARY\nstructured-output:\n") {
		t.Fatalf("output must separate summary from structured section: %q", output)
	}
	if !strings.Contains(output, "critical-remark=missing rollback plan\n") {
		t.Fatalf("output must include critical remarks: %q", output)
	}
	if !strings.Contains(output, "minor-remark=consider renaming helper\n") {
		t.Fatalf("output must include minor remarks: %q", output)
	}
	if !strings.Contains(output, "question=should we add an integration test?\n") {
		t.Fatalf("output must include questions: %q", output)
	}
	if strings.Contains(output, "critical-remark=   ") {
		t.Fatalf("output must skip blank structured values: %q", output)
	}
}

func TestPrintLaunchResultWithReviewCycleEnvelope(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "profile=default git=disabled\nApplied the requested changes.",
		ReviewCycle: &execution.ReviewCycleEnvelope{
			ProtocolVersion: "review-cycle/v1",
			Mode:            "re-review",
			Summary:         "Re-check after fixes.",
			Remarks: []execution.ReviewCycleRemark{{
				ID:       "remark-1",
				Status:   "resolved",
				Severity: "critical",
				Title:    "Rollback plan",
				Body:     "Confirmed in deploy docs.",
			}},
			Questions: []execution.ReviewCycleQuestion{{
				ID:    "question-1",
				Title: "Integration coverage",
				Body:  "Is the new test enough?",
			}},
			FollowUpActions: []execution.ReviewCycleAction{{
				ID:     "action-1",
				Status: "pending",
				Type:   "test",
				Title:  "Run smoke suite",
			}},
			Changes: []execution.ReviewCycleChange{{Summary: "Updated release checklist."}},
		},
		CriticalRemarks: []string{"Rollback plan: Confirmed in deploy docs."},
	})

	output := stdout.String()
	if !strings.Contains(output, "review-cycle-protocol-version=review-cycle/v1\n") {
		t.Fatalf("output must include review cycle protocol version: %q", output)
	}
	if !strings.Contains(output, "review-cycle-mode=re-review\n") {
		t.Fatalf("output must include review cycle mode: %q", output)
	}
	if !strings.Contains(output, "review-cycle-summary=Re-check after fixes.\n") {
		t.Fatalf("output must include review cycle summary: %q", output)
	}
	if !strings.Contains(output, `review-cycle-remark={"id":"remark-1","status":"resolved","severity":"critical","title":"Rollback plan","body":"Confirmed in deploy docs."}`+"\n") {
		t.Fatalf("output must include serialized review cycle remark: %q", output)
	}
	if !strings.Contains(output, `review-cycle-question={"id":"question-1","title":"Integration coverage","body":"Is the new test enough?"}`+"\n") {
		t.Fatalf("output must include serialized review cycle question: %q", output)
	}
	if !strings.Contains(output, `review-cycle-follow-up-action={"id":"action-1","status":"pending","type":"test","title":"Run smoke suite"}`+"\n") {
		t.Fatalf("output must include serialized follow-up action: %q", output)
	}
	if !strings.Contains(output, `review-cycle-change={"summary":"Updated release checklist."}`+"\n") {
		t.Fatalf("output must include serialized change: %q", output)
	}
	if !strings.Contains(output, "critical-remark=Rollback plan: Confirmed in deploy docs.\n") {
		t.Fatalf("output must keep legacy critical remark view: %q", output)
	}
}

func TestPrintLaunchResultPreservesReviewCycleJSONPayload(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "Applied the requested changes.",
		ReviewCycle: &execution.ReviewCycleEnvelope{
			ProtocolVersion: "review-cycle/v1",
			Remarks: []execution.ReviewCycleRemark{{
				ID:         "remark-1",
				Severity:   "critical",
				Title:      "Spacing",
				Body:       "keep   internal   spacing",
				Reply:      "reply  keeps  spaces",
				FixSummary: "fix  keeps  spaces",
			}},
		},
	})

	output := stdout.String()
	if !strings.Contains(output, `review-cycle-remark={"id":"remark-1","severity":"critical","title":"Spacing","body":"keep   internal   spacing","reply":"reply  keeps  spaces","fix_summary":"fix  keeps  spaces"}`+"\n") {
		t.Fatalf("review-cycle JSON payload must preserve spaces inside string values: %q", output)
	}
}

func TestPrintLaunchResultPreservesMultilineSummaryBoundary(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:          "completed",
		Summary:         "line one\nline two\nline three",
		CriticalRemarks: []string{"missing rollback plan"},
	})

	output := stdout.String()
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nline one\nline two\nline three\nPROGRESS_SUMMARY\nstructured-output:\n") {
		t.Fatalf("multiline summary must stay inside explicit summary block: %q", output)
	}
	if strings.Contains(output, "line three\ncritical-remark=") {
		t.Fatalf("structured lines must not be ambiguous continuation of summary: %q", output)
	}
}

func TestPrintLaunchResultNormalizesMultilineStructuredValues(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:          "completed",
		Summary:         "Applied the requested changes.",
		CriticalRemarks: []string{"missing\nrollback\nplan"},
		MinorRemarks:    []string{" consider\t renaming  helper "},
		Questions:       []string{"should we add\n\nan integration test?"},
	})

	output := stdout.String()
	if !strings.Contains(output, "critical-remark=missing rollback plan\n") {
		t.Fatalf("critical remark must be normalized to one line: %q", output)
	}
	if !strings.Contains(output, "minor-remark=consider renaming helper\n") {
		t.Fatalf("minor remark must be normalized to one line: %q", output)
	}
	if !strings.Contains(output, "question=should we add an integration test?\n") {
		t.Fatalf("question must be normalized to one line: %q", output)
	}
}
