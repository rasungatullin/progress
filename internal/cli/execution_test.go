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
	if !strings.Contains(output, "summary=profile=default git=disabled\nApplied the requested changes.\n") {
		t.Fatalf("output must include summary: %q", output)
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
