package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestLaunchCommitPushDisabled(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "runner output", nil
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
}

func TestLaunchCommitPushEnabledByProfile(t *testing.T) {
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
				return "", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfileWithCommitPush(true), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{{"rev-parse", "--is-inside-work-tree"}, {"branch", "--show-current"}, {"status", "--porcelain"}}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushNoChanges(t *testing.T) {
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
				return "", nil
			default:
				return "", fmt.Errorf("unexpected git command: %v", args)
			}
		},
	}

	invocation := validInvocation(t, true)
	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "git=no-changes branch=feature/test") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}

	expectedCalls := [][]string{{"rev-parse", "--is-inside-work-tree"}, {"branch", "--show-current"}, {"status", "--porcelain"}}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchCommitPushWithChanges(t *testing.T) {
	t.Parallel()

	var calls [][]string
	statusCalls := 0
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
				statusCalls++
				if statusCalls == 1 {
					return " M file.txt\n", nil
				}
				return "M  file.txt\n", nil
			case "add -A":
				return "", nil
			case "commit -m Apply task result":
				return "[feature/test abc123] Apply task result\n", nil
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
		{"commit", "-m", "Apply task result"},
		{"for-each-ref", "--format=%(upstream:short)", "refs/heads/feature/test"},
		{"push", "-u", "origin", "feature/test"},
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected git calls: %#v", calls)
	}
}

func TestLaunchRunnerErrorSkipsGit(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return "", errors.New("launch runner failed: exit status 1")
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not run after runner error")
			return "", nil
		},
	}

	_, err := service.Launch(context.Background(), validInvocation(t, true), validProfile(), validAllocation(), validWorkplace(t))
	if err == nil {
		t.Fatal("expected launch error")
	}
	if !strings.Contains(err.Error(), "launch runner failed") {
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
				`{"critical_remarks":["missing rollback plan"],"minor_remarks":["consider renaming helper"],"questions":["should we add an integration test?"]}`,
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
	if !slices.Equal(result.CriticalRemarks, []string{"missing rollback plan"}) {
		t.Fatalf("unexpected critical remarks: %#v", result.CriticalRemarks)
	}
	if !slices.Equal(result.MinorRemarks, []string{"consider renaming helper"}) {
		t.Fatalf("unexpected minor remarks: %#v", result.MinorRemarks)
	}
	if !slices.Equal(result.Questions, []string{"should we add an integration test?"}) {
		t.Fatalf("unexpected questions: %#v", result.Questions)
	}
	if result.ReviewCycle == nil {
		t.Fatal("legacy structured output must be normalized into review cycle envelope")
	}
	if result.ReviewCycle.ProtocolVersion != model.ReviewCycleProtocolVersion {
		t.Fatalf("unexpected review cycle protocol version: %#v", result.ReviewCycle)
	}
	if len(result.ReviewCycle.Remarks) != 2 || result.ReviewCycle.Remarks[0].Body != "missing rollback plan" || result.ReviewCycle.Remarks[1].Body != "consider renaming helper" {
		t.Fatalf("unexpected review cycle remarks: %#v", result.ReviewCycle.Remarks)
	}
	if len(result.ReviewCycle.Questions) != 1 || result.ReviewCycle.Questions[0].Body != "should we add an integration test?" {
		t.Fatalf("unexpected review cycle questions: %#v", result.ReviewCycle.Questions)
	}
}

func TestLaunchStructuredOutputReviewCycleEnvelopePresent(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","mode":"review","summary":"Main review findings.","remarks":[{"id":"remark-1","status":"open","response_status":"needs-reply","severity":"critical","type":"bug","title":"Missing rollback plan","body":"Document rollback steps."}],"questions":[{"id":"question-1","title":"Integration coverage","body":"Should we add an integration test?"}],"follow_up_actions":[{"id":"action-1","status":"pending","type":"docs","title":"Update release checklist","body":"Add rollback item."}],"changes":[{"summary":"Touched deploy docs."}]}`,
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

	if result.ReviewCycle == nil {
		t.Fatal("review cycle envelope must be populated")
	}
	if result.ReviewCycle.ProtocolVersion != model.ReviewCycleProtocolVersion {
		t.Fatalf("unexpected protocol version: %#v", result.ReviewCycle)
	}
	if result.ReviewCycle.Mode != model.ReviewCycleModeReview {
		t.Fatalf("unexpected mode: %#v", result.ReviewCycle)
	}
	if result.ReviewCycle.Summary != "Main review findings." {
		t.Fatalf("unexpected structured summary: %#v", result.ReviewCycle)
	}
	if len(result.ReviewCycle.Remarks) != 1 || result.ReviewCycle.Remarks[0].ID != "remark-1" {
		t.Fatalf("unexpected remarks: %#v", result.ReviewCycle.Remarks)
	}
	if len(result.ReviewCycle.Questions) != 1 || result.ReviewCycle.Questions[0].ID != "question-1" {
		t.Fatalf("unexpected questions: %#v", result.ReviewCycle.Questions)
	}
	if len(result.ReviewCycle.FollowUpActions) != 1 || result.ReviewCycle.FollowUpActions[0].ID != "action-1" {
		t.Fatalf("unexpected follow-up actions: %#v", result.ReviewCycle.FollowUpActions)
	}
	if len(result.ReviewCycle.Changes) != 1 || result.ReviewCycle.Changes[0].Summary != "Touched deploy docs." {
		t.Fatalf("unexpected changes: %#v", result.ReviewCycle.Changes)
	}
	if !slices.Equal(result.CriticalRemarks, []string{"Missing rollback plan: Document rollback steps."}) {
		t.Fatalf("unexpected critical remarks: %#v", result.CriticalRemarks)
	}
	if len(result.MinorRemarks) != 0 {
		t.Fatalf("unexpected minor remarks: %#v", result.MinorRemarks)
	}
	if !slices.Equal(result.Questions, []string{"Integration coverage: Should we add an integration test?"}) {
		t.Fatalf("unexpected legacy questions view: %#v", result.Questions)
	}
}

func TestLaunchStructuredOutputMixedPayloadMergesIntoCanonicalEnvelope(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","mode":"review","summary":"Main review findings.","remarks":[{"id":"remark-1","status":"open","response_status":"needs-reply","severity":"critical","type":"bug","title":"Missing rollback plan","body":"Document rollback steps.","reply":"Will update docs.","fix_summary":"Added rollback section."}],"questions":[{"id":"question-1","title":"Integration coverage","body":"Should we add an integration test?","reply":"Added one integration scenario."}],"critical_remarks":["legacy critical context"],"minor_remarks":["legacy naming note"]}`,
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

	if result.ReviewCycle == nil {
		t.Fatal("review cycle envelope must be populated")
	}
	if len(result.ReviewCycle.Remarks) != 3 {
		t.Fatalf("unexpected canonical remarks: %#v", result.ReviewCycle.Remarks)
	}
	if result.ReviewCycle.Remarks[1].Body != "legacy critical context" || result.ReviewCycle.Remarks[1].Severity != "critical" {
		t.Fatalf("legacy critical remark must be merged into envelope: %#v", result.ReviewCycle.Remarks)
	}
	if result.ReviewCycle.Remarks[2].Body != "legacy naming note" || result.ReviewCycle.Remarks[2].Severity != "minor" {
		t.Fatalf("legacy minor remark must be merged into envelope: %#v", result.ReviewCycle.Remarks)
	}
	if !slices.Equal(result.CriticalRemarks, []string{"Missing rollback plan: Document rollback steps.; reply=Will update docs.; fix_summary=Added rollback section.", "legacy critical context"}) {
		t.Fatalf("unexpected critical remarks projection: %#v", result.CriticalRemarks)
	}
	if !slices.Equal(result.MinorRemarks, []string{"legacy naming note"}) {
		t.Fatalf("unexpected minor remarks projection: %#v", result.MinorRemarks)
	}
	if !slices.Equal(result.Questions, []string{"Integration coverage: Should we add an integration test?; reply=Added one integration scenario."}) {
		t.Fatalf("unexpected question projection: %#v", result.Questions)
	}
}

func TestLaunchStructuredOutputAbsent(t *testing.T) {
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

	result, err := service.Launch(context.Background(), validInvocation(t, false), validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}

	if len(result.CriticalRemarks) != 0 || len(result.MinorRemarks) != 0 || len(result.Questions) != 0 {
		t.Fatalf("structured output must stay empty when absent: %#v", result)
	}
	if !strings.Contains(result.Summary, "Applied the requested changes.") {
		t.Fatalf("summary must keep plain runner output: %q", result.Summary)
	}
}

func TestLaunchMalformedStructuredOutputFallsBackToRawSummary(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"critical_remarks":"missing rollback plan"}`,
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

	if len(result.CriticalRemarks) != 0 || len(result.MinorRemarks) != 0 || len(result.Questions) != 0 {
		t.Fatalf("malformed structured output must not populate fields: %#v", result)
	}
	if !strings.Contains(result.Summary, structuredOutputStart) {
		t.Fatalf("summary must preserve raw output on malformed structured block: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, `{"critical_remarks":"missing rollback plan"}`) {
		t.Fatalf("summary must preserve malformed payload for diagnostics: %q", result.Summary)
	}
}

func TestLaunchStructuredOutputOnlyFromTrailingBlock(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Example in prose:",
				structuredOutputStart,
				`{"critical_remarks":["example only"]}`,
				structuredOutputEnd,
				"Applied the requested changes.",
				structuredOutputStart,
				`{"critical_remarks":["missing rollback plan"],"minor_remarks":["consider renaming helper"],"questions":["should we add an integration test?"]}`,
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

	if strings.Contains(result.Summary, "Applied the requested changes.\n"+structuredOutputStart) {
		t.Fatalf("summary must strip trailing structured block: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "Example in prose:") || !strings.Contains(result.Summary, `{"critical_remarks":["example only"]}`) {
		t.Fatalf("summary must preserve non-trailing example block: %q", result.Summary)
	}
	if !slices.Equal(result.CriticalRemarks, []string{"missing rollback plan"}) {
		t.Fatalf("unexpected critical remarks: %#v", result.CriticalRemarks)
	}
	if !slices.Equal(result.MinorRemarks, []string{"consider renaming helper"}) {
		t.Fatalf("unexpected minor remarks: %#v", result.MinorRemarks)
	}
	if !slices.Equal(result.Questions, []string{"should we add an integration test?"}) {
		t.Fatalf("unexpected questions: %#v", result.Questions)
	}
}

func TestLaunchTrailingTextAfterStructuredBlockDisablesExtraction(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"critical_remarks":["missing rollback plan"]}`,
				structuredOutputEnd,
				"example continuation after block",
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

	if len(result.CriticalRemarks) != 0 || len(result.MinorRemarks) != 0 || len(result.Questions) != 0 {
		t.Fatalf("structured output must stay empty when block is not trailing: %#v", result)
	}
	if !strings.Contains(result.Summary, "example continuation after block") {
		t.Fatalf("summary must preserve trailing prose after non-trailing block: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, structuredOutputStart) {
		t.Fatalf("summary must keep non-trailing structured markers verbatim: %q", result.Summary)
	}
}

func TestLaunchParsesStructuredInputBlock(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = strings.Join([]string{
		"Reply to the latest review.",
		structuredInputStart,
		`{"protocol_version":"review-cycle/v1","mode":"reply","summary":"Need to answer review remarks.","remarks":[{"id":"remark-1","status":"open","response_status":"needs-reply","severity":"critical","title":"Missing rollback plan","body":"Please add rollback steps."}]}`,
		structuredInputEnd,
	}, "\n")

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			if in.Launch.Prompt != "Reply to the latest review." {
				t.Fatalf("runner must receive plain prompt without protocol block: %q", in.Launch.Prompt)
			}
			if in.Launch.StructuredInput == nil {
				t.Fatal("runner invocation must include parsed structured input")
			}
			if in.Launch.StructuredInput.Mode != model.ReviewCycleModeReply {
				t.Fatalf("unexpected structured input mode: %#v", in.Launch.StructuredInput)
			}
			if len(in.Launch.StructuredInput.Remarks) != 1 || in.Launch.StructuredInput.Remarks[0].ID != "remark-1" {
				t.Fatalf("unexpected structured input remarks: %#v", in.Launch.StructuredInput.Remarks)
			}
			return "runner output", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "runner output") {
		t.Fatalf("summary must preserve runner output: %q", result.Summary)
	}
}

func TestLaunchParsesStructuredOutputBlockWhenPayloadContainsLiteralTag(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Applied the requested changes.",
				structuredOutputStart,
				`{"protocol_version":"review-cycle/v1","mode":"fix","summary":"Done.","remarks":[{"id":"remark-1","severity":"critical","title":"Literal tag","body":"Keep literal <progress-structured-output> inside payload."}]}`,
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

	if result.ReviewCycle == nil || len(result.ReviewCycle.Remarks) != 1 {
		t.Fatalf("structured output with literal tag inside payload must still parse: %#v", result.ReviewCycle)
	}
	if result.ReviewCycle.Remarks[0].Body != "Keep literal <progress-structured-output> inside payload." {
		t.Fatalf("unexpected remark body: %#v", result.ReviewCycle.Remarks)
	}
}

func TestLaunchParsesStructuredInputBlockWhenPayloadContainsLiteralTag(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = strings.Join([]string{
		"Reply to the latest review.",
		structuredInputStart,
		`{"protocol_version":"review-cycle/v1","mode":"reply","summary":"Need to answer review remarks.","remarks":[{"id":"remark-1","status":"open","response_status":"needs-reply","severity":"critical","title":"Literal tag","body":"Please keep literal <progress-structured-input> in the quoted example."}]}`,
		structuredInputEnd,
	}, "\n")

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			if in.Launch.Prompt != "Reply to the latest review." {
				t.Fatalf("runner must receive plain prompt without protocol block: %q", in.Launch.Prompt)
			}
			if in.Launch.StructuredInput == nil || len(in.Launch.StructuredInput.Remarks) != 1 {
				t.Fatalf("runner invocation must include parsed structured input: %#v", in.Launch.StructuredInput)
			}
			if in.Launch.StructuredInput.Remarks[0].Body != "Please keep literal <progress-structured-input> in the quoted example." {
				t.Fatalf("unexpected structured input remark: %#v", in.Launch.StructuredInput.Remarks)
			}
			return "runner output", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "runner output") {
		t.Fatalf("summary must preserve runner output: %q", result.Summary)
	}
}

func TestLaunchParsesStructuredInputMixedPayloadIntoCanonicalEnvelope(t *testing.T) {
	t.Parallel()

	invocation := validInvocation(t, false)
	invocation.Launch.Prompt = strings.Join([]string{
		"Reply to the latest review.",
		structuredInputStart,
		`{"protocol_version":"review-cycle/v1","mode":"reply","summary":"Need to answer review remarks.","remarks":[{"id":"remark-1","status":"open","response_status":"needs-reply","severity":"critical","title":"Missing rollback plan","body":"Please add rollback steps.","reply":"Added rollback section.","fix_summary":"Updated deploy docs."}],"critical_remarks":["legacy critical context"],"questions":["legacy question"]}`,
		structuredInputEnd,
	}, "\n")

	service := &Service{
		runRunner: func(_ context.Context, in model.Invocation) (string, error) {
			if in.Launch.Prompt != "Reply to the latest review." {
				t.Fatalf("runner must receive plain prompt without protocol block: %q", in.Launch.Prompt)
			}
			if in.Launch.StructuredInput == nil {
				t.Fatal("runner invocation must include parsed structured input")
			}
			if in.Launch.StructuredInput.Mode != model.ReviewCycleModeReply {
				t.Fatalf("unexpected structured input mode: %#v", in.Launch.StructuredInput)
			}
			if len(in.Launch.StructuredInput.Remarks) != 2 {
				t.Fatalf("mixed payload must merge legacy remarks into canonical envelope: %#v", in.Launch.StructuredInput.Remarks)
			}
			if in.Launch.StructuredInput.Remarks[1].Body != "legacy critical context" || in.Launch.StructuredInput.Remarks[1].Severity != "critical" {
				t.Fatalf("unexpected merged legacy remark: %#v", in.Launch.StructuredInput.Remarks)
			}
			if len(in.Launch.StructuredInput.Questions) != 1 || in.Launch.StructuredInput.Questions[0].Body != "legacy question" {
				t.Fatalf("legacy questions must be merged into canonical envelope: %#v", in.Launch.StructuredInput.Questions)
			}
			return "runner output", nil
		},
		runGitOutput: func(context.Context, string, ...string) (string, error) {
			t.Fatal("git must not be called when commit-push is disabled")
			return "", nil
		},
	}

	result, err := service.Launch(context.Background(), invocation, validProfile(), validAllocation(), validWorkplace(t))
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !strings.Contains(result.Summary, "runner output") {
		t.Fatalf("summary must preserve runner output: %q", result.Summary)
	}
}

func TestBuildRunnerPromptAppendsProgrammaticStructuredInput(t *testing.T) {
	t.Parallel()

	structuredInput := &model.ReviewCycleEnvelope{
		ProtocolVersion: model.ReviewCycleProtocolVersion,
		Mode:            model.ReviewCycleModeFix,
		Summary:         "Apply the accepted fixes.",
		Remarks: []model.ReviewCycleRemark{{
			ID:       "remark-1",
			Severity: "critical",
			Title:    "Rollback plan",
			Body:     "Please add rollback steps.",
		}},
	}

	prompt, err := buildRunnerPrompt("Apply the latest review fixes.", structuredInput)
	if err != nil {
		t.Fatalf("buildRunnerPrompt: %v", err)
	}

	plainPrompt, parsedStructuredInput, ok := parseStructuredInput(prompt)
	if !ok {
		t.Fatalf("programmatic structured input must be encoded into runner prompt: %q", prompt)
	}
	if plainPrompt != "Apply the latest review fixes." {
		t.Fatalf("unexpected plain prompt after round-trip: %q", plainPrompt)
	}
	if !reflect.DeepEqual(parsedStructuredInput, structuredInput) {
		t.Fatalf("unexpected structured input after round-trip: %#v", parsedStructuredInput)
	}
}

func TestLaunchBrokenBlockBeforeValidTrailingBlock(t *testing.T) {
	t.Parallel()

	service := &Service{
		runRunner: func(context.Context, model.Invocation) (string, error) {
			return strings.Join([]string{
				"Broken example in prose:",
				structuredOutputStart,
				`{"critical_remarks":`,
				"Applied the requested changes.",
				structuredOutputStart,
				`{"critical_remarks":["missing rollback plan"]}`,
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

	if !slices.Equal(result.CriticalRemarks, []string{"missing rollback plan"}) {
		t.Fatalf("unexpected critical remarks: %#v", result.CriticalRemarks)
	}
	if !strings.Contains(result.Summary, `{"critical_remarks":`) {
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
				`{"critical_remarks":["earlier valid block"]}`,
				structuredOutputEnd,
				structuredOutputStart,
				`{"critical_remarks":"broken trailing block"}`,
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

	if len(result.CriticalRemarks) != 0 || len(result.MinorRemarks) != 0 || len(result.Questions) != 0 {
		t.Fatalf("invalid trailing block must suppress structured extraction: %#v", result)
	}
	if !strings.Contains(result.Summary, `{"critical_remarks":["earlier valid block"]}`) {
		t.Fatalf("summary must preserve earlier valid block when trailing block is invalid: %q", result.Summary)
	}
	if !strings.Contains(result.Summary, `{"critical_remarks":"broken trailing block"}`) {
		t.Fatalf("summary must preserve invalid trailing block for diagnostics: %q", result.Summary)
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
			case "commit -m Apply task result":
				return "[feature/test abc123] Apply task result\n", nil
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
	return validProfileWithCommitPush(false)
}

func validProfileWithCommitPush(commitPush bool) model.Profile {
	return model.Profile{Name: "default", Model: "openai/gpt-5.4", CommitPush: commitPush}
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
