package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestLaunchCommitPushDisabled(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Done.","commit_message":"Ignored when git is disabled"}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=disabled") {
		t.Fatalf("summary must include disabled git state: %q", result.Summary)
	}
	if result.StructuredOutput == nil || result.StructuredOutput.CommitMessage != "Ignored when git is disabled" {
		t.Fatalf("structured output must still be parsed when git stage is disabled: %#v", result.StructuredOutput)
	}
}

func TestLaunchCommitPushWithChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	statusCalls := 0
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Done.","commit_message":"  Ship release notes  "}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				statusCalls++
				if statusCalls == 1 {
					return " M file.txt\n", nil
				}
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m Ship release notes":
				return "[feature/test abc123] Ship release notes\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "", nil
			case "push -u origin feature/test":
				return "branch 'feature/test' set up to track 'origin/feature/test'.\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=committed+pushed branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
		{"add", "-A"},
		{"status", "--porcelain"},
		{"commit", "-m", "Ship release notes"},
		{"for-each-ref", "--format=%(upstream:short)", "refs/heads/feature/test"},
		{"push", "-u", "origin", "feature/test"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushUsesWorkplaceNameWhenStructuredCommitMessageBlank(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, true)
	invocation.Workplace.Name = "review-fixes"

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Done.","commit_message":"   "}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m review-fixes":
				return "[feature/test abc123] review-fixes\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t)); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

func TestLaunchCommitPushUsesWorktreeDirectoryNameWhenWorkplaceNameMissing(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, true)
	worktreeDir := filepath.Join(t.TempDir(), "structured-contract-v1-worktree")

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"runner output",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Done.","commit_message":"\t"}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m structured-contract-v1-worktree":
				return "[feature/test abc123] structured-contract-v1-worktree\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "Everything up-to-date\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	workplace := model.Workplace{Name: worktreeDir, Ready: true}
	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), workplace); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

func TestLaunchCommitPushSkipsCommitAndPushWhenNoChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, append([]string(nil), args...))
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "\n", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"branch", "--show-current"},
		{"status", "--porcelain"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchPushErrorReturned(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
		},
		runGitOutput: func(_ context.Context, _ string, args ...string) (string, error) {
			switch strings.Join(args, " ") {
			case "rev-parse --is-inside-work-tree":
				return "true\n", nil
			case "branch --show-current":
				return "feature/test\n", nil
			case "status --porcelain":
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m repo":
				return "[feature/test abc123] repo\n", nil
			case "for-each-ref --format=%(upstream:short) refs/heads/feature/test":
				return "origin/feature/test\n", nil
			case "push":
				return "", errors.New("exit status 1\nremote rejected")
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	_, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected push error")
	}
	if !strings.Contains(err.Error(), "git push failed") || !strings.Contains(err.Error(), "remote rejected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLaunchStructuredOutputPresent(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Main result.","commit_message":"Document deploy checklist","remarks":[{"id":"remark-1","severity":"critical","title":"Rollback plan","body":"Document rollback steps."}],"questions":[{"id":"question-1","title":"Integration coverage","body":"Should we add an integration test?"}],"follow_up_actions":[{"id":"action-1","status":"pending","type":"docs","title":"Update release checklist"}],"changes":[{"summary":"Touched deploy docs."}],"commands":[{"name":"open-pr","args":["--draft"]}],"conclusion":{"status":"needs-follow-up","summary":"Ship after docs update"},"extensions":{"custom":{"owner":"release"}}}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "Applied the requested changes.") {
		t.Fatalf("summary must keep plain runner output: %q", result.Summary)
	}
	if strings.Contains(result.Summary, structuredOutputStart) {
		t.Fatalf("summary must not keep structured block markers: %q", result.Summary)
	}
	if result.StructuredOutput == nil {
		t.Fatal("structured output must be parsed")
	}
	if result.StructuredOutput.ProtocolVersion != model.StructuredIOVersion {
		t.Fatalf("unexpected protocol version: %#v", result.StructuredOutput)
	}
	if result.StructuredOutput.Summary != "Main result." {
		t.Fatalf("unexpected structured summary: %#v", result.StructuredOutput)
	}
	if result.StructuredOutput.CommitMessage != "Document deploy checklist" {
		t.Fatalf("unexpected structured commit message: %#v", result.StructuredOutput)
	}
	if len(result.StructuredOutput.Remarks) != 1 || result.StructuredOutput.Remarks[0].Body != "Document rollback steps." {
		t.Fatalf("unexpected remarks: %#v", result.StructuredOutput.Remarks)
	}
	if len(result.StructuredOutput.Commands) != 1 || result.StructuredOutput.Commands[0].Name != "open-pr" {
		t.Fatalf("unexpected commands: %#v", result.StructuredOutput.Commands)
	}
	if result.StructuredOutput.Conclusion == nil || result.StructuredOutput.Conclusion.Status != "needs-follow-up" {
		t.Fatalf("unexpected conclusion: %#v", result.StructuredOutput.Conclusion)
	}
	if string(result.StructuredOutput.Extensions["custom"]) != `{"owner":"release"}` {
		t.Fatalf("unexpected extensions: %#v", result.StructuredOutput.Extensions)
	}
}

func TestLaunchStructuredOutputInvalidPreservesFreeText(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","remarks":[{}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput != nil {
		t.Fatalf("invalid structured output must not populate fields: %#v", result.StructuredOutput)
	}
	if !strings.Contains(result.Summary, `{"protocol_version":"review-cycle/v1","remarks":[{}]}`) {
		t.Fatalf("summary must preserve invalid block for diagnostics: %q", result.Summary)
	}
}

func TestLaunchStructuredOutputRequiredMissingFails(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "Applied the requested changes.", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.StructuredOutputRequired = true

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected required structured output error")
	}
	if !strings.Contains(err.Error(), "structured output is required") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("unexpected result status: %#v", result)
	}
	if !strings.Contains(result.Summary, "Applied the requested changes.") {
		t.Fatalf("summary must preserve plain runner output: %q", result.Summary)
	}
}

func TestLaunchStructuredOutputRequiredInvalidFails(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		payload    string
		expectPart string
	}{
		{
			name:       "empty summary",
			payload:    `{"protocol_version":"review-cycle/v1","summary":"ok","summary":""}`,
			expectPart: "structured output must include a non-empty summary",
		},
		{
			name:       "unknown field",
			payload:    `{"protocol_version":"review-cycle/v1","summary":"Done.","unknown":true}`,
			expectPart: `unknown field "unknown"`,
		},
		{
			name:       "meaningless remark object",
			payload:    `{"protocol_version":"review-cycle/v1","summary":"Done.","remarks":[{}]}`,
			expectPart: "structured output remarks[0] must include at least one non-empty field",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service := &Service{
				runRunner: func(context.Context, model.Invocation) (string, error) {
					return strings.Join([]string{
						"Applied the requested changes.",
						structuredOutputStart,
						tc.payload,
						structuredOutputEnd,
					}, "\n"), nil
				},
				runGitOutput: func(context.Context, string, ...string) (string, error) {
					t.Fatal("git must not be called when commit-push is disabled")
					return "", nil
				},
			}

			invocation := validInvocation(t, false)
			invocation.Launch.StructuredOutputRequired = true

			result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
			if err == nil {
				t.Fatal("expected required structured output error")
			}
			if !strings.Contains(err.Error(), "structured output is required") || !strings.Contains(err.Error(), tc.expectPart) {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "failed" {
				t.Fatalf("unexpected result status: %#v", result)
			}
			if !strings.Contains(result.Summary, tc.payload) {
				t.Fatalf("summary must preserve invalid structured payload: %q", result.Summary)
			}
		})
	}
}

func TestStructuredInputCanonicalValidatorSharedAcrossPromptAndProgrammaticPaths(t *testing.T) {
	t.Parallel()

	rawPayload := `{"protocol_version":"review-cycle/v1"}`
	_, promptErr := parseStructuredInputPayload(rawPayload)
	if promptErr == nil {
		t.Fatal("expected prompt structured input validation error")
	}

	_, programmaticErr := buildRunnerPrompt(model.LaunchSpec{
		Prompt: "Apply the fixes.",
		StructuredInput: &model.StructuredInput{
			ProtocolVersion: model.StructuredIOVersion,
		},
	})
	if programmaticErr == nil {
		t.Fatal("expected programmatic structured input validation error")
	}

	expectPart := "structured input must include at least one non-empty field besides protocol_version"
	if !strings.Contains(promptErr.Error(), expectPart) {
		t.Fatalf("unexpected prompt validation error: %v", promptErr)
	}
	if !strings.Contains(programmaticErr.Error(), expectPart) {
		t.Fatalf("unexpected programmatic validation error: %v", programmaticErr)
	}
}

func TestLaunchProgrammaticStructuredInputRejectsProtocolOnlyPayload(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			t.Fatal("runner must not be called when structured input is invalid")
			return "", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.StructuredInput = &model.StructuredInput{ProtocolVersion: model.StructuredIOVersion}

	_, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected invalid structured input error")
	}
	if !strings.Contains(err.Error(), "structured input must include at least one non-empty field besides protocol_version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLaunchPromptStructuredInputRejectsProtocolOnlyPayload(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			t.Fatal("runner must not be called when prompt structured input is invalid")
			return "", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = strings.Join([]string{
		"Reply to the latest review.",
		structuredInputStart,
		`{"protocol_version":"review-cycle/v1"}`,
		structuredInputEnd,
	}, "\n")

	_, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected invalid structured input error")
	}
	if !strings.Contains(err.Error(), "structured input must include at least one non-empty field besides protocol_version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLaunchParsesStructuredInputFromPromptBlock(t *testing.T) {
	t.Parallel()

	structuredInput := model.StructuredInput{
		ProtocolVersion: model.StructuredIOVersion,
		Task:            "Answer review remarks.",
		Constraints:     []string{"Do not change public API."},
		ReviewRemarks: []model.StructuredRemark{{
			ID:       "remark-1",
			Severity: "critical",
			Title:    "Rollback plan",
			Body:     "Please add rollback steps.",
		}},
		ReviewResponses: []model.StructuredResponse{{
			RemarkID: "remark-1",
			Summary:  "Will update docs.",
		}},
	}

	payload, err := buildStructuredJSON(structuredInput)
	if err != nil {
		t.Fatalf("marshal structured input: %v", err)
	}

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			if in.Launch.Prompt != "Reply to the latest review." {
				t.Fatalf("runner must receive plain prompt without structured block: %q", in.Launch.Prompt)
			}
			if !reflect.DeepEqual(in.Launch.StructuredInput, &structuredInput) {
				t.Fatalf("unexpected structured input: %#v", in.Launch.StructuredInput)
			}
			return "done", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = strings.Join([]string{
		"Reply to the latest review.",
		structuredInputStart,
		payload,
		structuredInputEnd,
	}, "\n")

	if _, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t)); err != nil {
		t.Fatalf("launch: %v", err)
	}
}

func TestBuildRunnerPromptAppendsProgrammaticStructuredInputAndOutputInstruction(t *testing.T) {
	t.Parallel()

	structuredInput := &model.StructuredInput{
		Task:        "Apply the accepted fixes.",
		Constraints: []string{"Do not change the public API."},
		ProjectContext: []model.StructuredContext{{
			Title: "Service",
			Body:  "Execution contour migration.",
		}},
		OperationalContext: []model.StructuredContext{{
			Title: "Branch",
			Body:  "feature/structured-io",
		}},
		PreviousRunResults: []model.StructuredResult{{
			Summary: "Earlier attempt failed validation.",
		}},
		ReviewRemarks: []model.StructuredRemark{{
			ID:       "remark-1",
			Severity: "critical",
			Title:    "Rollback plan",
			Body:     "Please add rollback steps.",
		}},
		ReviewResponses: []model.StructuredResponse{{
			RemarkID: "remark-1",
			Summary:  "Will update docs.",
		}},
		IntegrationActions: []model.StructuredAction{{
			Type:  "github",
			Title: "Open PR after changes",
		}},
	}

	prompt, err := buildRunnerPrompt(model.LaunchSpec{
		Prompt:           "Apply the latest review fixes.",
		StructuredInput:  structuredInput,
		StructuredOutput: true,
	})
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Use every field from the structured input block below as execution context.") {
		t.Fatalf("prompt must mention structured input usage: %q", prompt)
	}
	if !strings.Contains(prompt, `protocol_version="review-cycle/v1"`) {
		t.Fatalf("prompt must mention canonical protocol version: %q", prompt)
	}
	if !strings.Contains(prompt, "Include commit_message") {
		t.Fatalf("prompt must mention optional structured commit message: %q", prompt)
	}

	plainPrompt, parsedStructuredInput, state, err := parseStructuredInput(prompt)
	if err != nil {
		t.Fatalf("parseStructuredInput: %v", err)
	}
	if state != trailingStructuredBlockValid {
		t.Fatalf("programmatic structured input must be encoded into runner prompt: %q", prompt)
	}
	if !strings.Contains(plainPrompt, "Apply the latest review fixes.") {
		t.Fatalf("unexpected plain prompt after round-trip: %q", plainPrompt)
	}
	if !strings.Contains(plainPrompt, `protocol_version="review-cycle/v1"`) {
		t.Fatalf("plain prompt must keep structured output instruction after extracting input block: %q", plainPrompt)
	}
	expected := *structuredInput
	expected.ProtocolVersion = model.StructuredIOVersion
	if !reflect.DeepEqual(parsedStructuredInput, &expected) {
		t.Fatalf("unexpected structured input after round-trip: %#v", parsedStructuredInput)
	}
}

func TestLaunchStructuredOutputWithLiteralTagInsidePayload(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Done.","remarks":[{"id":"remark-1","title":"Literal tag","body":"Keep literal <progress-structured-output> inside payload."}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput == nil || len(result.StructuredOutput.Remarks) != 1 || result.StructuredOutput.Remarks[0].Body != "Keep literal <progress-structured-output> inside payload." {
		t.Fatalf("structured output with literal tag inside payload must still parse: %#v", result.StructuredOutput)
	}
}

func TestLaunchBrokenBlockBeforeValidTrailingBlock(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Broken example in prose:",
				structuredOutputStart,
				`{"protocol_version":`,
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Done.","remarks":[{"title":"Rollback plan","body":"missing rollback plan"}]}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput == nil || len(result.StructuredOutput.Remarks) != 1 {
		t.Fatalf("unexpected structured output: %#v", result.StructuredOutput)
	}
	if !strings.Contains(result.Summary, `{"protocol_version":`) {
		t.Fatalf("summary must preserve earlier broken example: %q", result.Summary)
	}
}

func TestLaunchInvalidTrailingBlockWinsOverEarlierValidBlock(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","summary":"Earlier valid block."}`,
				structuredOutputEnd,
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","remarks":"broken trailing block"}`,
				structuredOutputEnd,
			}, "\n"), nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if result.StructuredOutput != nil {
		t.Fatalf("invalid trailing block must suppress structured extraction: %#v", result.StructuredOutput)
	}
	if !strings.Contains(result.Summary, `{"protocol_version":"review-cycle/v1","summary":"Earlier valid block."}`) {
		t.Fatalf("summary must preserve earlier valid block when trailing block is invalid: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, `{"protocol_version":"review-cycle/v1","remarks":"broken trailing block"}`) {
		t.Fatalf("summary must preserve invalid trailing block for diagnostics: %q", result.Summary)
	}
}

func validInvocation(t *testing.T, commitPush bool) model.Invocation {
	t.Helper()

	return model.Invocation{
		Launch: model.LaunchSpec{
			Directory:     tempDir(t),
			Runner:        RunnerOpenCode,
			Model:         "openai/gpt-5.4",
			Prompt:        "do work",
			CommitPush:    commitPush,
			CommitMessage: DefaultCommitMessage,
		},
	}
}

func validProfile() model.Profile {
	return model.Profile{Name: "default", Model: "openai/gpt-5.4"}
}

func validAllocation() model.Allocation {
	return model.Allocation{Resource: "external-launch", Reserved: true}
}

func validWorkplace(t *testing.T) model.Workplace {
	t.Helper()
	return model.Workplace{Name: tempDir(t), Ready: true}
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir temp repo: %v", err)
	}
	return dir
}

func buildStructuredJSON(value any) (string, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}
