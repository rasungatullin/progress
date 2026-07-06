package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/spf13/cobra"
)

func TestBindActionFlagsAndInvocation(t *testing.T) {
	t.Parallel()

	flags := newActionFlags()
	cmd := &cobra.Command{Use: "action"}
	bindActionFlags(cmd, flags)

	if err := cmd.ParseFlags([]string{"--action", "review", "--task", "Провести ревизию"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	request, err := actionInvocationFromFlags(flags)
	if err != nil {
		t.Fatalf("build action invocation: %v", err)
	}
	if request.Assignment == nil || request.Assignment.Action != "review" {
		t.Fatalf("unexpected assignment: %#v", request.Assignment)
	}
	if request.Assignment.StructuredInput == nil || request.Assignment.StructuredInput.Task != "Провести ревизию" {
		t.Fatalf("unexpected structured input: %#v", request.Assignment.StructuredInput)
	}
}

func TestStructuredInputFlagsOverrideInputFile(t *testing.T) {
	t.Parallel()

	inputFile := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(inputFile, []byte(`{"task":"from file","constraints":["keep file"],"project_context":[{"title":"File","body":"Context"}],"extensions":{"custom":{"keep":true}}}`), 0o600); err != nil {
		t.Fatalf("write input file: %v", err)
	}

	flags := newActionFlags()
	cmd := &cobra.Command{Use: "action"}
	bindActionFlags(cmd, flags)

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

	request, err := actionInvocationFromFlags(flags)
	if err != nil {
		t.Fatalf("build invocation: %v", err)
	}
	input := request.Assignment.StructuredInput
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

func TestActionFlagsBuildCanonicalTaskAndPullRequestContext(t *testing.T) {
	t.Parallel()

	flags := newActionFlags()
	cmd := &cobra.Command{Use: "action"}
	bindActionFlags(cmd, flags)

	err := cmd.ParseFlags([]string{
		"--action", "start-implementation-pr",
		"--repository", "owner/name",
		"--task-number", "112",
		"--pr-number", "17",
		"--base", "main",
		"--head", "112",
		"--title", "Поддержать действие",
		"--body", "Описание изменения.",
		"--draft",
	})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	request, err := actionInvocationFromFlags(flags)
	if err != nil {
		t.Fatalf("build invocation: %v", err)
	}
	assignment := request.Assignment
	if assignment == nil || assignment.CanonicalTask == nil {
		t.Fatalf("expected canonical task: %#v", assignment)
	}
	if assignment.CanonicalTask.Number != 112 || assignment.CanonicalTask.Repository != "owner/name" || assignment.CanonicalTask.Title != "Поддержать действие" {
		t.Fatalf("unexpected canonical task: %#v", assignment.CanonicalTask)
	}
	if assignment.CanonicalTask.Attributes["body"] != "Описание изменения." {
		t.Fatalf("task body must be stored as an attribute: %#v", assignment.CanonicalTask.Attributes)
	}
	if len(assignment.RelatedObjects) != 1 {
		t.Fatalf("expected pull request object: %#v", assignment.RelatedObjects)
	}
	pr := assignment.RelatedObjects[0]
	if pr.Type != "merge-request" || pr.Number != 17 || pr.Repository != "owner/name" {
		t.Fatalf("unexpected pull request object: %#v", pr)
	}
	if pr.Attributes["base_ref"] != "main" || pr.Attributes["head_ref"] != "112" || pr.Attributes["draft"] != "true" {
		t.Fatalf("unexpected pull request attributes: %#v", pr.Attributes)
	}
}

func TestExecutionActionAllowsActionOnlyInvocation(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "action"})

	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			executeAction: func(_ context.Context, req execution.ActionInvocation) (execution.ExecutionResult, error) {
				if req.Assignment == nil || req.Assignment.Action != execution.ActionClassEngineeringSynthesis {
					t.Fatalf("unexpected action request: %#v", req)
				}
				if req.Assignment.StructuredInput != nil {
					t.Fatalf("structured input must be absent when no structured flags are passed: %#v", req.Assignment.StructuredInput)
				}
				return execution.ExecutionResult{
					Status: "completed",
					Launch: &execution.LaunchResult{
						Status:  "completed",
						Summary: "action completed",
					},
				}, nil
			},
		}
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute action command: %v", err)
	}
	if !strings.Contains(stdout.String(), "state=completed\n") {
		t.Fatalf("output must include completed state: %q", stdout.String())
	}
}

func TestExecutionActionCommandCallsService(t *testing.T) {
	t.Parallel()

	type contextKey struct{}

	cmd := NewRootCommand()
	cmd.SetContext(context.WithValue(context.Background(), contextKey{}, "command-context"))
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "action", "--action", "review", "--task", "Провести ревизию"})

	var captured execution.ActionInvocation
	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			executeAction: func(ctx context.Context, req execution.ActionInvocation) (execution.ExecutionResult, error) {
				if got := ctx.Value(contextKey{}); got != "command-context" {
					t.Fatalf("execution action must receive command context, got %#v", got)
				}
				captured = req
				return execution.ExecutionResult{
					Status: "completed",
					Launch: &execution.LaunchResult{
						Status:  "completed",
						Summary: "action=review completed",
					},
				}, nil
			},
		}
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute action command: %v", err)
	}
	if captured.Assignment == nil || captured.Assignment.Action != "review" {
		t.Fatalf("unexpected action request: %#v", captured)
	}
	if strings.Contains(stdout.String(), "profile=") {
		t.Fatalf("action output must not include profile override diagnostics: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "state=completed\n") {
		t.Fatalf("output must include completed state: %q", stdout.String())
	}
}

func TestExecutionActionHelpDoesNotIncludeRemovedFlags(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "action", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute action help: %v", err)
	}

	help := stdout.String()
	if !strings.Contains(help, "--action") {
		t.Fatalf("action help must include action flag, got %q", help)
	}
	for _, fragment := range []string{"--profile", "--name", "--dir", "--runner", "--model", "--model-binding", "--prompt", "--structured-output", "--structured-output-required"} {
		if strings.Contains(help, fragment) {
			t.Fatalf("action help must not include removed flag %q, got %q", fragment, help)
		}
	}
}

func TestExecutionActionRejectsRemovedDirFlag(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"execution", "action", "--dir", "/tmp/work", "--task", "Ship it"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown dir flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutionOperationCommandCallsService(t *testing.T) {
	t.Parallel()

	type contextKey struct{}

	cmd := NewRootCommand()
	cmd.SetContext(context.WithValue(context.Background(), contextKey{}, "command-context"))
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"execution", "operation", "resolve-action", "--action", "review"})

	var captured execution.OperationInvocation
	setExecutionServiceFactory(cmd, func(*cobra.Command) executionCommandService {
		return executionCommandServiceStub{
			executeOperation: func(ctx context.Context, req execution.OperationInvocation) (execution.OperationResult, error) {
				if got := ctx.Value(contextKey{}); got != "command-context" {
					t.Fatalf("execution operation must receive command context, got %#v", got)
				}
				captured = req
				return execution.OperationResult{
					Name:    "resolve-action",
					Status:  "completed",
					Summary: "action=review class=review",
				}, nil
			},
		}
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute operation command: %v", err)
	}
	if captured.Operation != "resolve-action" || captured.Assignment == nil || captured.Assignment.Action != "review" {
		t.Fatalf("unexpected operation request: %#v", captured)
	}
	if captured.Assignment.StructuredInput != nil {
		t.Fatalf("operation request must not require structured input: %#v", captured.Assignment.StructuredInput)
	}
	output := stdout.String()
	for _, fragment := range []string{"operation=resolve-action\n", "status=completed\n", "summary=action=review class=review\n"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("operation output must include %q, got %q", fragment, output)
		}
	}
}

func TestPrintLaunchResultWithStructuredOutput(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "action"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:  "completed",
		Summary: "Applied the requested changes.",
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
			Questions:       []execution.StructuredQuestion{{ID: "question-1", Title: "Integration coverage", Body: "Is the new test enough?"}},
			FollowUpActions: []execution.StructuredAction{{ID: "action-1", Status: "pending", Type: "test", Title: "Run smoke suite"}},
			Changes:         []execution.StructuredChange{{Summary: "Updated release checklist."}},
			Commands:        []execution.StructuredCommand{{Name: "open-pr", Args: []string{"--draft"}}},
			Conclusion:      &execution.StructuredConclusion{Status: "ok", Summary: "Ready for merge"},
		},
	})

	output := stdout.String()
	for _, fragment := range []string{
		"summary<<PROGRESS_SUMMARY\nApplied the requested changes.\nPROGRESS_SUMMARY\nstructured-output:\n",
		"summary-field=Re-check after fixes.\n",
		"commit-message=Ship review fixes\n",
		`remark={"id":"remark-1","status":"resolved","severity":"critical","title":"Rollback plan","body":"Confirmed in deploy docs."}` + "\n",
		`question={"id":"question-1","title":"Integration coverage","body":"Is the new test enough?"}` + "\n",
		`follow-up-action={"id":"action-1","status":"pending","type":"test","title":"Run smoke suite"}` + "\n",
		`change={"summary":"Updated release checklist."}` + "\n",
		`command={"name":"open-pr","args":["--draft"]}` + "\n",
		`conclusion={"status":"ok","summary":"Ready for merge"}` + "\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output must include %q, got %q", fragment, output)
		}
	}
}

func TestPrintLaunchResultPreservesExtensionPayload(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "action"}
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

	cmd := &cobra.Command{Use: "action"}
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

func TestPrintLaunchResultWithoutStructuredOutput(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "action"}
	stdout := &bytes.Buffer{}
	cmd.SetOut(stdout)

	printLaunchResult(cmd, execution.LaunchResult{
		Status:        "completed",
		Summary:       "Compact summary.",
		RawOutputPath: "/tmp/progress/raw.log",
		RunRecordPath: "/tmp/progress/execution.json",
	})

	output := stdout.String()
	for _, fragment := range []string{
		"state=completed\n",
		"summary<<PROGRESS_SUMMARY\nCompact summary.\nPROGRESS_SUMMARY\n",
		"raw-output-path=/tmp/progress/raw.log\n",
		"run-record-path=/tmp/progress/execution.json\n",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output must include %q, got %q", fragment, output)
		}
	}
	if strings.Contains(output, "structured-output:\n") {
		t.Fatalf("output must omit structured section when values are absent: %q", output)
	}
}

func TestDecodeStrictJSONRejectsTrailingTokens(t *testing.T) {
	t.Parallel()

	var payload map[string]string
	err := decodeStrictJSON([]byte(`{"a":"b"} {"c":"d"}`), &payload)
	if err == nil {
		t.Fatal("expected trailing token error")
	}
}

func TestStructuredExtensionEntriesSkipEmptyPayloads(t *testing.T) {
	t.Parallel()

	entries := extensionsAsEntries(execution.StructuredExtensions{
		"":      json.RawMessage(`{"skip":true}`),
		"empty": nil,
		"keep":  json.RawMessage(`{"ok":true}`),
	})
	if len(entries) != 1 || entries[0].Name != "keep" || string(entries[0].Value) != `{"ok":true}` {
		t.Fatalf("unexpected extension entries: %#v", entries)
	}
}

type executionCommandServiceStub struct {
	executeAction    func(context.Context, execution.ActionInvocation) (execution.ExecutionResult, error)
	executeOperation func(context.Context, execution.OperationInvocation) (execution.OperationResult, error)
}

func (s executionCommandServiceStub) ExecuteAction(ctx context.Context, req execution.ActionInvocation) (execution.ExecutionResult, error) {
	if s.executeAction == nil {
		return execution.ExecutionResult{}, errors.New("unexpected ExecuteAction call")
	}
	return s.executeAction(ctx, req)
}

func (s executionCommandServiceStub) ExecuteOperation(ctx context.Context, req execution.OperationInvocation) (execution.OperationResult, error) {
	if s.executeOperation == nil {
		return execution.OperationResult{}, errors.New("unexpected ExecuteOperation call")
	}
	return s.executeOperation(ctx, req)
}
