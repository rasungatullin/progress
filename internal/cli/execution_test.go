package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/execution/history"
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

	err := cmd.ParseFlags([]string{"--name", "task-49", "--repo", "https://github.com/owner/name", "--task", "ship it"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation, err := invocationFromStructuredFlags(flags)
	if err != nil {
		t.Fatalf("build invocation: %v", err)
	}
	if invocation.Workplace.Name != "task-49" {
		t.Fatalf("unexpected workplace: %q", invocation.Workplace.Name)
	}
	if invocation.Repository.URL != "https://github.com/owner/name" {
		t.Fatalf("unexpected repository: %q", invocation.Repository.URL)
	}
	if invocation.Launch.StructuredInput == nil || invocation.Launch.StructuredInput.Task != "ship it" {
		t.Fatalf("unexpected structured input: %#v", invocation.Launch.StructuredInput)
	}
}

func TestStructuredInputFlagsOverrideInputFile(t *testing.T) {
	t.Parallel()

	inputFile := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(inputFile, []byte(`{"task":"from file","constraints":["keep file"],"project_context":[{"title":"File","body":"Context"}],"extensions":{"custom":{"keep":true}}}`), 0o600); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	flags := newStartFlags()
	cmd := &cobra.Command{Use: "start"}
	bindStartFlags(cmd, flags)

	err := cmd.ParseFlags([]string{
		"--input-file", inputFile,
		"--task", "from flag",
		"--constraint", "keep flag",
		"--project-context", `{"title":"Flag","body":"Context"}`,
		"--review-remark", `{"id":"r1","severity":"blocking","body":"Fix it"}`,
	})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	invocation, err := invocationFromStructuredFlags(flags)
	if err != nil {
		t.Fatalf("build invocation: %v", err)
	}
	input := invocation.Launch.StructuredInput
	if input == nil {
		t.Fatal("expected structured input")
	}
	if input.Task != "from flag" {
		t.Fatalf("task flag must override file task: %#v", input)
	}
	if len(input.Constraints) != 2 || input.Constraints[0] != "keep file" || input.Constraints[1] != "keep flag" {
		t.Fatalf("constraints must append to file values: %#v", input.Constraints)
	}
	if len(input.ProjectContext) != 2 || input.ProjectContext[1].Title != "Flag" {
		t.Fatalf("project contexts must append to file values: %#v", input.ProjectContext)
	}
	if len(input.ReviewRemarks) != 1 || input.ReviewRemarks[0].ID != "r1" {
		t.Fatalf("review remarks must be parsed from JSON object flags: %#v", input.ReviewRemarks)
	}
	if string(input.Extensions["custom"]) != `{"keep":true}` {
		t.Fatalf("extensions must be preserved from file: %#v", input.Extensions)
	}
}

func TestExecutionStartRejectsEmptyStructuredInputBeforeServiceStart(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "start", "--dir", "/tmp/work", "--profile", "default"})

	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			start: func(context.Context, execution.Invocation) (execution.LaunchResult, error) {
				t.Fatal("service start must not be called for empty structured input")
				return execution.LaunchResult{}, nil
			},
		}
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected empty structured input error")
	}
	if !strings.Contains(err.Error(), "structured input must include at least one non-empty field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutionReviewCycleCommandRunsCycleAboveStart(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{
		"execution", "review-cycle",
		"--execution-profile", "coder",
		"--review-profile", "review",
		"--max-executions", "3",
		"--dir", "/tmp/work",
		"--task", "ship it",
	})

	var calls []execution.Invocation
	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			start: func(_ context.Context, in execution.Invocation) (execution.LaunchResult, error) {
				calls = append(calls, in)
				switch len(calls) {
				case 1:
					return execution.LaunchResult{Status: "completed", Summary: "execution done"}, nil
				case 2:
					return execution.LaunchResult{
						Status:  "completed",
						Summary: "review done",
						StructuredOutput: &execution.StructuredOutput{
							Summary:    "Approved.",
							Conclusion: &execution.StructuredConclusion{Status: "ok"},
						},
					}, nil
				default:
					return execution.LaunchResult{}, errors.New("unexpected extra start call")
				}
			},
		}
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute review-cycle command: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected execution and review start calls, got %d", len(calls))
	}
	if calls[0].Profile != "coder" || calls[1].Profile != "review" {
		t.Fatalf("unexpected profile sequence: %#v", []string{calls[0].Profile, calls[1].Profile})
	}
	if calls[1].Launch.StructuredInput == nil {
		t.Fatal("review run must receive structured input")
	}

	output := stdout.String()
	if !strings.Contains(output, "state=completed\n") {
		t.Fatalf("output must include completed state: %q", output)
	}
	if !strings.Contains(output, "review-cycle execution-profile=coder review-profile=review max-executions=3 attempts=1") {
		t.Fatalf("output must include review cycle summary: %q", output)
	}
	if !strings.Contains(output, "conclusion={\"status\":\"ok\"}\n") {
		t.Fatalf("output must include final review structured output: %q", output)
	}
}

func TestExecutionStartHelpDoesNotIncludeReviewCycleFlags(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "start", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute start help: %v", err)
	}

	help := stdout.String()
	for _, fragment := range []string{"--review-profile", "--max-executions", "--prompt"} {
		if strings.Contains(help, fragment) {
			t.Fatalf("start help must not include %q, got %q", fragment, help)
		}
	}
}

func TestExecutionReviewCycleHelpIncludesCycleFlags(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "review-cycle", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute review-cycle help: %v", err)
	}

	help := stdout.String()
	for _, fragment := range []string{"--execution-profile", "--review-profile", "--max-executions"} {
		if !strings.Contains(help, fragment) {
			t.Fatalf("review-cycle help must include %q, got %q", fragment, help)
		}
	}
	if strings.Contains(help, "--prompt") {
		t.Fatalf("review-cycle help must not include prompt flag, got %q", help)
	}
}

func TestExecutionRunsCommandPrintsJSONHistory(t *testing.T) {
	root := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir temp root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previousDir)
	})

	if err := history.Store(context.Background(), root, history.Run{
		CreatedAt:       "2026-06-10T10:00:00Z",
		Status:          "failed",
		Summary:         "boom",
		Name:            "task-54",
		ProfileName:     "default",
		Runner:          "opencode",
		Model:           "openai/gpt-5.4",
		LaunchDirectory: root,
		Error:           "boom",
	}); err != nil {
		t.Fatalf("store history: %v", err)
	}

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "runs", "--json", "--status", "failed", "--name", "task-54"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute runs command: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, `"status":"failed"`) || !strings.Contains(output, `"name":"task-54"`) || !strings.Contains(output, `"error":"boom"`) {
		t.Fatalf("unexpected runs json: %q", output)
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
					Summary: "Applied the requested changes.\n<progress-structured-output>\n{\"remarks\":[{}]}\n</progress-structured-output>",
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
		Status:        "completed",
		Summary:       "profile=default git=disabled\nApplied the requested changes.",
		RawOutputPath: "/tmp/progress/raw.log",
		RunRecordPath: "/tmp/progress/execution.json",
	})

	output := stdout.String()
	if !strings.Contains(output, "state=completed\n") {
		t.Fatalf("output must include state: %q", output)
	}
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nprofile=default git=disabled\nApplied the requested changes.\nPROGRESS_SUMMARY\n") {
		t.Fatalf("output must include summary: %q", output)
	}
	if !strings.Contains(output, "raw-output-path=/tmp/progress/raw.log\n") {
		t.Fatalf("output must include raw output path: %q", output)
	}
	if !strings.Contains(output, "run-record-path=/tmp/progress/execution.json\n") {
		t.Fatalf("output must include run record path: %q", output)
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
			Summary:       "Re-check after fixes.",
			CommitMessage: "Ship review fixes",
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
			Summary: "Done.",
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
			Summary: "Done.",
		},
	})

	output := stdout.String()
	if !strings.Contains(output, "summary<<PROGRESS_SUMMARY\nline one\nline two\nline three\nPROGRESS_SUMMARY\nstructured-output:\n") {
		t.Fatalf("multiline summary must stay inside explicit summary block: %q", output)
	}
	if strings.Contains(output, "line three\nsummary-field=") {
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
