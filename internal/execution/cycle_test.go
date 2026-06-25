package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rasungatullin/progress/internal/execution/model"
)

func TestRunExecutionCycleTransfersContextBetweenSteps(t *testing.T) {
	t.Parallel()

	starter := &cycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "Сборка выполнена."},
			{
				Status:  "completed",
				Summary: "Ревизия выявила замечания.",
				StructuredOutput: &StructuredOutput{
					Summary: "Changes needed.",
					Conclusion: &StructuredConclusion{
						Status: "needs-work",
					},
					Remarks: []StructuredRemark{{ID: "r1", Status: "open", Severity: "blocking", Title: "Проверка", Body: "Доработать код."}},
				},
			},
			{Status: "completed", Summary: "Доработка выполнена."},
			{
				Status:  "completed",
				Summary: "Ревью выполнено.",
				StructuredOutput: &StructuredOutput{
					Conclusion: &StructuredConclusion{Status: "ok"},
				},
			},
		},
	}

	config := model.CycleConfig{Cycles: map[string]model.CycleDefinition{
		"implementation-review": {
			StartStep: "execution",
			Limits:    model.CycleLimits{MaxExecutions: 10},
			Steps: []model.CycleStep{
				{
					Name:    "execution",
					Profile: "coder",
					Transitions: []model.CycleTransition{
						{To: "review", NotIn: []string{"ok", "approve", "approved"}},
						{To: "review", Missing: true},
					},
				},
				{
					Name:    "review",
					Profile: "review",
					Transitions: []model.CycleTransition{
						{Finish: "completed", In: []string{"ok", "approve", "approved"}},
						{To: "implementation", NotIn: []string{"ok", "approve", "approved"}},
						{To: "implementation", Missing: true},
					},
				},
				{
					Name:    "implementation",
					Profile: "coder",
					InputTransform: model.CycleInputTransform{
						TaskOnRepeat: "Повторно проверить после доработки.",
					},
					Transitions: []model.CycleTransition{
						{To: "review", NotIn: []string{"ok", "approve", "approved"}},
						{To: "review", Missing: true},
					},
				},
			},
		},
	}}

	result, err := RunExecutionCycle(context.Background(), starter, config, "implementation-review", Invocation{
		Workplace: WorkplaceSpec{Name: "task-123"},
		Launch: LaunchSpec{
			Directory: "/tmp/workspace",
			StructuredInput: &StructuredInput{
				Task: "Реализовать цикл.",
			},
		},
	})
	if err != nil {
		t.Fatalf("run execution cycle: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("unexpected status: %#v", result)
	}
	if len(starter.calls) != 4 {
		t.Fatalf("expected 4 step calls, got %d", len(starter.calls))
	}
	if starter.calls[0].Profile != "coder" || starter.calls[1].Profile != "review" || starter.calls[2].Profile != "coder" || starter.calls[3].Profile != "review" {
		t.Fatalf("unexpected profile sequence: %#v", []string{starter.calls[0].Profile, starter.calls[1].Profile, starter.calls[2].Profile, starter.calls[3].Profile})
	}

	reviewInput := starter.calls[2].Launch.StructuredInput
	if reviewInput == nil {
		t.Fatal("expected review input on implementation step")
	}
	if !containsStructuredContext(reviewInput.ProjectContext, "Исходная задача", "Реализовать цикл.") {
		t.Fatalf("implementation input must preserve original task in context: %#v", reviewInput.ProjectContext)
	}
	if reviewInput.Task != "Повторно проверить после доработки." {
		t.Fatalf("implementation input must use task_on_repeat: %q", reviewInput.Task)
	}
	if len(reviewInput.ReviewRemarks) != 1 || reviewInput.ReviewRemarks[0].ID != "r1" {
		t.Fatalf("implementation input must receive review remarks: %#v", reviewInput.ReviewRemarks)
	}
	if len(reviewInput.PreviousRunResults) == 0 || !strings.Contains(reviewInput.PreviousRunResults[len(reviewInput.PreviousRunResults)-1].Body, "Ревизия выявила замечания.") {
		t.Fatalf("implementation input must include previous run result: %#v", reviewInput.PreviousRunResults)
	}
}

func TestRunExecutionCycleStopsAtLimit(t *testing.T) {
	t.Parallel()

	starter := &cycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "review loop", StructuredOutput: &StructuredOutput{Conclusion: &StructuredConclusion{Status: "needs-work"}}},
			{Status: "completed", Summary: "review loop", StructuredOutput: &StructuredOutput{Conclusion: &StructuredConclusion{Status: "needs-work"}}},
			{Status: "completed", Summary: "review loop", StructuredOutput: &StructuredOutput{Conclusion: &StructuredConclusion{Status: "needs-work"}}},
		},
	}
	config := model.CycleConfig{Cycles: map[string]model.CycleDefinition{
		"ralph-loop": {
			StartStep: "review",
			Limits:    model.CycleLimits{MaxExecutions: 2},
			Steps: []model.CycleStep{
				{
					Name:    "review",
					Profile: "review",
					Transitions: []model.CycleTransition{
						{To: "review", In: []string{"needs-work"}},
					},
				},
			},
		},
	}}

	result, err := RunExecutionCycle(context.Background(), starter, config, "ralph-loop", Invocation{
		Workplace: WorkplaceSpec{Name: "task-124"},
		Launch: LaunchSpec{
			Directory:       "/tmp/workspace",
			StructuredInput: &StructuredInput{Task: "Тест лимита."},
		},
	})
	if err != nil {
		t.Fatalf("run execution cycle: %v", err)
	}
	if result.Status != "limit-reached" {
		t.Fatalf("expected limit-reached status, got %#v", result)
	}
	if len(starter.calls) != 2 {
		t.Fatalf("expected 2 calls limited by max-executions, got %d", len(starter.calls))
	}
}

func TestRunExecutionCycleRejectsUnknownCycleAndTransition(t *testing.T) {
	t.Parallel()

	starter := &cycleStarterStub{
		results: []LaunchResult{
			{Status: "completed", Summary: "done", StructuredOutput: &StructuredOutput{Conclusion: &StructuredConclusion{Status: "needs-work"}}},
		},
	}

	config := model.CycleConfig{Cycles: map[string]model.CycleDefinition{
		"bad-cycle": {
			StartStep: "a",
			Steps:     []model.CycleStep{{Name: "a", Profile: "coder"}},
		},
	}}

	if _, err := RunExecutionCycle(context.Background(), starter, config, "missing", Invocation{Launch: LaunchSpec{Directory: "/tmp", StructuredInput: &StructuredInput{Task: "task"}}}); err == nil {
		t.Fatal("expected unknown cycle error")
	}

	broken := model.CycleConfig{Cycles: map[string]model.CycleDefinition{
		"bad-cycle": {
			StartStep: "a",
			Steps: []model.CycleStep{
				{
					Name:    "a",
					Profile: "coder",
					Transitions: []model.CycleTransition{
						{To: "a", In: []string{"ok"}},
					},
				},
			},
		},
	}}
	_, err := RunExecutionCycle(context.Background(), starter, broken, "bad-cycle", Invocation{Launch: LaunchSpec{Directory: "/tmp", StructuredInput: &StructuredInput{Task: "task"}}})
	if err == nil {
		t.Fatal("expected invalid transition error")
	}
	if !strings.Contains(err.Error(), `step "a" has no valid transition`) {
		t.Fatalf("unexpected transition error: %v", err)
	}
}

func TestRunExecutionCycleImplicitlyFinishesWhenStepHasNoTransitions(t *testing.T) {
	t.Parallel()

	starter := &cycleStarterStub{
		results: []LaunchResult{
			{
				Status:  "completed",
				Summary: "done",
				StructuredOutput: &StructuredOutput{
					Conclusion: &StructuredConclusion{Status: "needs-work"},
				},
			},
		},
	}

	config := model.CycleConfig{Cycles: map[string]model.CycleDefinition{
		"implicit-finish": {
			StartStep: "a",
			Steps: []model.CycleStep{
				{
					Name:    "a",
					Profile: "coder",
				},
			},
		},
	}}

	result, err := RunExecutionCycle(context.Background(), starter, config, "implicit-finish", Invocation{
		Launch: LaunchSpec{
			Directory:       "/tmp",
			StructuredInput: &StructuredInput{Task: "task"},
		},
	})
	if err != nil {
		t.Fatalf("run execution cycle: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed status, got %#v", result)
	}
	if !strings.Contains(result.Summary, "marker=implicit-finish/no-transitions") {
		t.Fatalf("expected implicit finish marker in summary: %q", result.Summary)
	}
	if len(starter.calls) != 1 {
		t.Fatalf("expected single execution call, got %d", len(starter.calls))
	}
}

type cycleStarterStub struct {
	calls   []Invocation
	results []LaunchResult
	errAt   int
	err     error
}

func (s *cycleStarterStub) Start(_ context.Context, in Invocation) (LaunchResult, error) {
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
