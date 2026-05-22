package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunReviewCycleRepeatsUntilReviewApproves(t *testing.T) {
	t.Parallel()

	starter := &reviewCycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "execution attempt 1"},
			{
				Status:  "completed",
				Summary: "review attempt 1",
				StructuredOutput: &StructuredOutput{
					ProtocolVersion: StructuredIOVersion,
					Summary:         "Needs fixes.",
					Remarks: []StructuredRemark{{
						ID:       "remark-1",
						Status:   "open",
						Severity: "blocking",
						Title:    "Missing limit handling",
						Body:     "Validate the configured limit.",
					}},
					Conclusion: &StructuredConclusion{Status: "changes-requested", Summary: "Fix blocking remarks."},
				},
			},
			{Status: "completed", Summary: "execution attempt 2"},
			{
				Status:  "completed",
				Summary: "review attempt 2",
				StructuredOutput: &StructuredOutput{
					ProtocolVersion: StructuredIOVersion,
					Summary:         "Approved.",
					Conclusion:      &StructuredConclusion{Status: "ok", Summary: "Ready."},
				},
			},
		},
	}

	result, err := RunReviewCycle(context.Background(), starter, Invocation{
		Profile: "coder",
		Launch:  LaunchSpec{Prompt: "Ship the cycle."},
	}, "review", 5)
	if err != nil {
		t.Fatalf("run review cycle: %v", err)
	}

	if result.Status != "completed" {
		t.Fatalf("unexpected result status: %#v", result)
	}
	if !strings.Contains(result.Summary, "attempts=2") || !strings.Contains(result.Summary, "attempt=1 execution=completed review=completed conclusion=changes-requested") || !strings.Contains(result.Summary, "attempt=2 execution=completed review=completed conclusion=ok") {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if len(starter.calls) != 4 {
		t.Fatalf("expected two execution/review pairs, got %d calls", len(starter.calls))
	}
	if starter.calls[0].Profile != "coder" || starter.calls[1].Profile != "review" || starter.calls[2].Profile != "coder" || starter.calls[3].Profile != "review" {
		t.Fatalf("unexpected profile sequence: %#v", []string{starter.calls[0].Profile, starter.calls[1].Profile, starter.calls[2].Profile, starter.calls[3].Profile})
	}

	reworkInput := starter.calls[2].Launch.StructuredInput
	if reworkInput == nil {
		t.Fatal("rework attempt must receive structured input")
	}
	if len(reworkInput.ReviewRemarks) != 1 || reworkInput.ReviewRemarks[0].ID != "remark-1" {
		t.Fatalf("rework input must include review remarks: %#v", reworkInput.ReviewRemarks)
	}
	if len(reworkInput.PreviousRunResults) < 2 {
		t.Fatalf("rework input must include previous execution and review results: %#v", reworkInput.PreviousRunResults)
	}

	reviewInput := starter.calls[3].Launch.StructuredInput
	if reviewInput == nil || len(reviewInput.PreviousRunResults) == 0 || reviewInput.PreviousRunResults[len(reviewInput.PreviousRunResults)-1].Body != "execution attempt 2" {
		t.Fatalf("review input must include latest execution result: %#v", reviewInput)
	}
}

func TestRunReviewCycleStopsAtExecutionLimit(t *testing.T) {
	t.Parallel()

	starter := &reviewCycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "execution attempt 1"},
			{
				Status: "completed",
				StructuredOutput: &StructuredOutput{
					ProtocolVersion: StructuredIOVersion,
					Summary:         "Needs fixes.",
					Conclusion:      &StructuredConclusion{Status: "needs-work"},
				},
			},
			{Status: "completed", Summary: "execution attempt 2"},
			{
				Status: "completed",
				StructuredOutput: &StructuredOutput{
					ProtocolVersion: StructuredIOVersion,
					Summary:         "Still needs fixes.",
					Conclusion:      &StructuredConclusion{Status: "needs-work"},
				},
			},
		},
	}

	result, err := RunReviewCycle(context.Background(), starter, Invocation{Profile: "coder", Launch: LaunchSpec{Prompt: "Ship it."}}, "review", 2)
	if err != nil {
		t.Fatalf("run review cycle: %v", err)
	}
	if result.Status != "limit-reached" {
		t.Fatalf("expected limit-reached status, got %#v", result)
	}
	if len(starter.calls) != 4 {
		t.Fatalf("expected exactly two execution attempts and reviews, got %d calls", len(starter.calls))
	}
}

func TestRunReviewCyclePreservesTrailingStructuredInputFromPrompt(t *testing.T) {
	t.Parallel()

	starter := &reviewCycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "execution attempt 1"},
			{
				Status: "completed",
				StructuredOutput: &StructuredOutput{
					ProtocolVersion: StructuredIOVersion,
					Summary:         "Approved.",
					Conclusion:      &StructuredConclusion{Status: "approve"},
				},
			},
		},
	}

	prompt := strings.Join([]string{
		"Ship the cycle.",
		"<progress-structured-input>",
		`{"protocol_version":"review-cycle/v1","task":"Original structured task","constraints":["Keep structured task"],"project_context":[{"title":"Area","body":"Execution"}]}`,
		"</progress-structured-input>",
	}, "\n")

	if _, err := RunReviewCycle(context.Background(), starter, Invocation{Profile: "coder", Launch: LaunchSpec{Prompt: prompt}}, "review", 5); err != nil {
		t.Fatalf("run review cycle: %v", err)
	}
	if len(starter.calls) != 2 {
		t.Fatalf("expected execution and review calls, got %d", len(starter.calls))
	}

	executionInput := starter.calls[0].Launch.StructuredInput
	if executionInput == nil || executionInput.Task != "Original structured task" {
		t.Fatalf("execution call must receive normalized structured input from prompt: %#v", executionInput)
	}
	if starter.calls[0].Launch.Prompt != "Ship the cycle." {
		t.Fatalf("execution prompt must be stripped to plain text: %q", starter.calls[0].Launch.Prompt)
	}

	reviewInput := starter.calls[1].Launch.StructuredInput
	if reviewInput == nil {
		t.Fatal("review call must receive structured input")
	}
	if !containsString(reviewInput.Constraints, "Keep structured task") {
		t.Fatalf("review input must preserve original constraints: %#v", reviewInput.Constraints)
	}
	if !containsStructuredContext(reviewInput.ProjectContext, "Исходная структурированная задача", "Original structured task") {
		t.Fatalf("review input must preserve original task as context: %#v", reviewInput.ProjectContext)
	}
}

func TestRunReviewCycleReturnsAccumulatedResultOnReviewError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("review failed")
	starter := &reviewCycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "execution attempt 1"},
			{Status: "failed", Summary: "review failed"},
		},
		errAt: 2,
		err:   expectedErr,
	}

	result, err := RunReviewCycle(context.Background(), starter, Invocation{Profile: "coder", Launch: LaunchSpec{Prompt: "Ship it."}}, "review", 5)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected review error, got %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("unexpected status: %#v", result)
	}
	if !strings.Contains(result.Summary, "attempt=1 execution=completed review=failed conclusion=missing") {
		t.Fatalf("result must include failed attempt diagnostics: %q", result.Summary)
	}
}

type reviewCycleStarterStub struct {
	calls   []Invocation
	results []LaunchResult
	errAt   int
	err     error
}

func (s *reviewCycleStarterStub) Start(_ context.Context, in Invocation) (LaunchResult, error) {
	s.calls = append(s.calls, in)
	index := len(s.calls)
	if index > len(s.results) {
		return LaunchResult{}, errors.New("unexpected Start call")
	}
	result := s.results[index-1]
	if s.errAt == index {
		return result, s.err
	}

	return result, nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}

	return false
}

func containsStructuredContext(values []StructuredContext, title, body string) bool {
	for _, value := range values {
		if value.Title == title && value.Body == body {
			return true
		}
	}

	return false
}
