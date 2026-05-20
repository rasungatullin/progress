package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/spf13/cobra"
)

func TestBindLaunchFlagsAndInvocation(t *testing.T) {
	t.Parallel()

	flags := newLaunchFlags()
	cmd := &cobra.Command{Use: "launch"}
	bindLaunchFlags(cmd, flags)

	err := cmd.ParseFlags([]string{
		"--dir", "/tmp/work",
		"--runner", "codex",
		"--model", "openai/gpt-5.4",
		"--prompt", "ship it",
		"--structured-output",
		"--structured-output-required",
		"--commit-push",
	})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation := invocationFromLaunchFlags(flags)
	if invocation.Launch.Directory != "/tmp/work" {
		t.Fatalf("unexpected directory: %q", invocation.Launch.Directory)
	}
	if invocation.Launch.Runner != "codex" {
		t.Fatalf("unexpected runner: %q", invocation.Launch.Runner)
	}
	if !invocation.Launch.CommitPush {
		t.Fatal("expected commit-push to be enabled")
	}
	if invocation.Launch.Prompt != "ship it" {
		t.Fatalf("unexpected prompt: %q", invocation.Launch.Prompt)
	}
	if !invocation.Launch.StructuredOutput {
		t.Fatal("expected structured-output to be enabled")
	}
	if !invocation.Launch.StructuredOutputRequired {
		t.Fatal("expected structured-output-required to be enabled")
	}
}

func TestBindStartFlagsAndInvocationIncludesRepo(t *testing.T) {
	t.Parallel()

	flags := newStartFlags()
	cmd := &cobra.Command{Use: "start"}
	bindStartFlags(cmd, flags)

	err := cmd.ParseFlags([]string{"--name", "task-49", "--repo", "https://github.com/owner/name", "--prompt", "ship it"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation := invocationFromLaunchFlags(flags)
	if invocation.Workplace.Name != "task-49" {
		t.Fatalf("unexpected workplace: %q", invocation.Workplace.Name)
	}
	if invocation.Repository.URL != "https://github.com/owner/name" {
		t.Fatalf("unexpected repository: %q", invocation.Repository.URL)
	}
}

func TestBindWorkplaceFlagsAndInvocationIncludesRepo(t *testing.T) {
	t.Parallel()

	flags := &launchFlags{}
	cmd := &cobra.Command{Use: "workplace"}
	bindWorkplaceFlags(cmd, flags)

	err := cmd.ParseFlags([]string{"--name", "task-49", "--repo", "git@github.com:owner/name.git"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation := invocationFromWorkplaceFlags(flags)
	if invocation.Workplace.Name != "task-49" {
		t.Fatalf("unexpected workplace: %q", invocation.Workplace.Name)
	}
	if invocation.Repository.URL != "git@github.com:owner/name.git" {
		t.Fatalf("unexpected repository: %q", invocation.Repository.URL)
	}
}

func TestExecutionWorkplaceCommandPrintsRepositoryDiagnostics(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "workplace", "--name", "task-49", "--repo", "owner/name"})

	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			prepareWorkplace: func(context.Context, execution.Invocation, execution.Profile, execution.Allocation) (execution.Workplace, error) {
				return execution.Workplace{
					Name:           "/tmp/workplaces/github-owner-name/task-49",
					RepositoryURL:  "https://github.com/owner/name.git",
					RepositoryRoot: "/tmp/repositories/github-owner-name",
					Ready:          true,
				}, nil
			},
		}
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute workplace command: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "repository=https://github.com/owner/name.git\n") {
		t.Fatalf("output must include repository: %q", output)
	}
	if !strings.Contains(output, "repository-root=/tmp/repositories/github-owner-name\n") {
		t.Fatalf("output must include repository root: %q", output)
	}
	if !strings.Contains(output, "workplace=/tmp/workplaces/github-owner-name/task-49\nready=true\n") {
		t.Fatalf("output must include workplace details: %q", output)
	}
}

func TestNewLaunchFlagsDefaults(t *testing.T) {
	t.Parallel()

	flags := newLaunchFlags()
	if flags.commitPush {
		t.Fatal("commit-push must be disabled by default")
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
		"--structured-output-required",
		"Автоматически добавить инструкцию на structured output",
	} {
		if !strings.Contains(help, fragment) {
			t.Fatalf("launch help must include %q, got %q", fragment, help)
		}
	}
	for _, fragment := range []string{"--structured-protocol", "--structured-mode"} {
		if strings.Contains(help, fragment) {
			t.Fatalf("launch help must not include removed flag %q, got %q", fragment, help)
		}
	}
	if strings.Contains(help, "--commit-message") {
		t.Fatalf("launch help must not include removed flag %q, got %q", "--commit-message", help)
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
	if !strings.Contains(output, "runner=opencode\n") {
		t.Fatalf("profile output must include resolved runner, got %q", output)
	}
	if !strings.Contains(output, "model=openai/gpt-5.4\n") {
		t.Fatalf("profile output must include resolved model, got %q", output)
	}
	if !strings.Contains(output, "prompt-additions=\n") {
		t.Fatalf("profile output must include prompt-additions field, got %q", output)
	}
	if !strings.Contains(output, "structured-output=true\n") {
		t.Fatalf("profile output must include structured-output flag, got %q", output)
	}
	if !strings.Contains(output, "structured-output-required=true\n") {
		t.Fatalf("profile output must include structured-output-required flag, got %q", output)
	}
	if !strings.Contains(output, "structured-output-fields=summary,commit_message,remarks,questions,follow_up_actions,changes,commands,conclusion,extensions\n") {
		t.Fatalf("profile output must include structured-output-fields, got %q", output)
	}
	if !strings.Contains(output, "commit-push=true\n") {
		t.Fatalf("profile output must include commit-push flag, got %q", output)
	}
}

func TestExecutionLaunchCommandPrintsSummaryOnStructuredOutputError(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "launch", "--dir", "/tmp/work", "--prompt", "ship it"})

	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			launchDirect: func(context.Context, execution.Invocation) (execution.LaunchResult, error) {
				return execution.LaunchResult{
					Status:  "failed",
					Summary: "Applied the requested changes.\n<progress-structured-output>\n{\"protocol_version\":\"review-cycle/v1\",\"remarks\":[{}]}\n</progress-structured-output>",
				}, errors.New("structured output is required but payload does not match structured output schema: structured output remarks[0] must include at least one non-empty field")
			},
		}
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected launch error")
	}
	if !strings.Contains(err.Error(), "structured output is required") {
		t.Fatalf("unexpected command error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "state=failed\n") {
		t.Fatalf("output must include failed state: %q", output)
	}
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nApplied the requested changes.\n<progress-structured-output>") {
		t.Fatalf("output must include summary even on error: %q", output)
	}
	if strings.Contains(output, "structured-output:\n") {
		t.Fatalf("output must not print structured section for invalid payload: %q", output)
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
}

func TestPrintLaunchResultWithStructuredOutput(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "profile=default git=disabled\nApplied the requested changes.",
		StructuredOutput: &execution.StructuredOutput{
			ProtocolVersion: execution.StructuredIOVersion,
			Summary:         "Re-check after fixes.",
			CommitMessage:   "Ship review fixes",
			Remarks: []execution.StructuredRemark{{
				ID:       "remark-1",
				Status:   "resolved",
				Severity: "critical",
				Title:    "Rollback plan",
				Body:     "Confirmed in deploy docs.",
			}},
			Questions: []execution.StructuredQuestion{{
				ID:    "question-1",
				Title: "Integration coverage",
				Body:  "Is the new test enough?",
			}},
			FollowUpActions: []execution.StructuredAction{{
				ID:     "action-1",
				Status: "pending",
				Type:   "test",
				Title:  "Run smoke suite",
			}},
			Changes: []execution.StructuredChange{{Summary: "Updated release checklist."}},
			Commands: []execution.StructuredCommand{{
				Name: "open-pr",
				Args: []string{"--draft"},
			}},
			Conclusion: &execution.StructuredConclusion{Status: "ok", Summary: "Ready for merge"},
		},
	})

	output := stdout.String()
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nprofile=default git=disabled\nApplied the requested changes.\nPROGRESS_SUMMARY\nstructured-output:\n") {
		t.Fatalf("output must separate summary from structured section: %q", output)
	}
	if !strings.Contains(output, "protocol-version=review-cycle/v1\n") {
		t.Fatalf("output must include protocol version: %q", output)
	}
	if !strings.Contains(output, "summary-field=Re-check after fixes.\n") {
		t.Fatalf("output must include structured summary: %q", output)
	}
	if !strings.Contains(output, "commit-message=Ship review fixes\n") {
		t.Fatalf("output must include structured commit message: %q", output)
	}
	if !strings.Contains(output, `remark={"id":"remark-1","status":"resolved","severity":"critical","title":"Rollback plan","body":"Confirmed in deploy docs."}`+"\n") {
		t.Fatalf("output must include serialized remark: %q", output)
	}
	if !strings.Contains(output, `question={"id":"question-1","title":"Integration coverage","body":"Is the new test enough?"}`+"\n") {
		t.Fatalf("output must include serialized question: %q", output)
	}
	if !strings.Contains(output, `follow-up-action={"id":"action-1","status":"pending","type":"test","title":"Run smoke suite"}`+"\n") {
		t.Fatalf("output must include follow-up action: %q", output)
	}
	if !strings.Contains(output, `change={"summary":"Updated release checklist."}`+"\n") {
		t.Fatalf("output must include change: %q", output)
	}
	if !strings.Contains(output, `command={"name":"open-pr","args":["--draft"]}`+"\n") {
		t.Fatalf("output must include command: %q", output)
	}
	if !strings.Contains(output, `conclusion={"status":"ok","summary":"Ready for merge"}`+"\n") {
		t.Fatalf("output must include conclusion: %q", output)
	}
}

func TestPrintLaunchResultPreservesExtensionPayload(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "Applied the requested changes.",
		StructuredOutput: &execution.StructuredOutput{
			ProtocolVersion: execution.StructuredIOVersion,
			Summary:         "Done.",
			Extensions: execution.StructuredExtensions{
				"custom": []byte(`{"owner":"release","preserve":"keep   spaces"}`),
			},
		},
	})

	output := stdout.String()
	if !strings.Contains(output, `extension={"name":"custom","value":{"owner":"release","preserve":"keep   spaces"}}`+"\n") {
		t.Fatalf("extension payload must stay lossless: %q", output)
	}
}

func TestPrintLaunchResultPreservesMultilineSummaryBoundary(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "line one\nline two\nline three",
		StructuredOutput: &execution.StructuredOutput{
			ProtocolVersion: execution.StructuredIOVersion,
			Summary:         "Done.",
		},
	})

	output := stdout.String()
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nline one\nline two\nline three\nPROGRESS_SUMMARY\nstructured-output:\n") {
		t.Fatalf("multiline summary must stay inside explicit summary block: %q", output)
	}
	if strings.Contains(output, "line three\nprotocol-version=") {
		t.Fatalf("structured lines must not be ambiguous continuation of summary: %q", output)
	}
}

func TestPrintLaunchResultIncludesRawOutputPath(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "launch"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:        "completed",
		Summary:       "Compact summary.",
		RawOutputPath: "/tmp/progress/.progress/runner-output/execution-123.log",
	})

	output := stdout.String()
	if !strings.Contains(output, "raw-output-path=/tmp/progress/.progress/runner-output/execution-123.log\n") {
		t.Fatalf("output must include raw output path: %q", output)
	}
}

type executionCommandServiceStub struct {
	start            func(context.Context, execution.Invocation) (execution.LaunchResult, error)
	dispatch         func(context.Context, execution.Invocation) []string
	resolveProfile   func(context.Context, execution.Invocation) (execution.Profile, error)
	allocateResource func(context.Context, execution.Invocation, execution.Profile) (execution.Allocation, error)
	prepareWorkplace func(context.Context, execution.Invocation, execution.Profile, execution.Allocation) (execution.Workplace, error)
	launchDirect     func(context.Context, execution.Invocation) (execution.LaunchResult, error)
}

func (s executionCommandServiceStub) Start(ctx context.Context, in execution.Invocation) (execution.LaunchResult, error) {
	if s.start == nil {
		return execution.LaunchResult{}, errors.New("unexpected Start call")
	}
	return s.start(ctx, in)
}

func (s executionCommandServiceStub) Dispatch(ctx context.Context, in execution.Invocation) []string {
	if s.dispatch == nil {
		return nil
	}
	return s.dispatch(ctx, in)
}

func (s executionCommandServiceStub) ResolveProfile(ctx context.Context, in execution.Invocation) (execution.Profile, error) {
	if s.resolveProfile == nil {
		return execution.Profile{}, errors.New("unexpected ResolveProfile call")
	}
	return s.resolveProfile(ctx, in)
}

func (s executionCommandServiceStub) AllocateResources(ctx context.Context, in execution.Invocation, profile execution.Profile) (execution.Allocation, error) {
	if s.allocateResource == nil {
		return execution.Allocation{}, errors.New("unexpected AllocateResources call")
	}
	return s.allocateResource(ctx, in, profile)
}

func (s executionCommandServiceStub) PrepareWorkplace(ctx context.Context, in execution.Invocation, profile execution.Profile, allocation execution.Allocation) (execution.Workplace, error) {
	if s.prepareWorkplace == nil {
		return execution.Workplace{}, errors.New("unexpected PrepareWorkplace call")
	}
	return s.prepareWorkplace(ctx, in, profile, allocation)
}

func (s executionCommandServiceStub) LaunchDirect(ctx context.Context, in execution.Invocation) (execution.LaunchResult, error) {
	if s.launchDirect == nil {
		return execution.LaunchResult{}, errors.New("unexpected LaunchDirect call")
	}
	return s.launchDirect(ctx, in)
}
