package research

import (
	"context"
	"testing"
)

func TestServicePlanBuildsExperimentPlan(t *testing.T) {
	t.Parallel()

	plan, err := NewService().Plan(context.Background(), Experiment{
		Name:       "compare-review-methods",
		Hypothesis: Hypothesis{Name: "short-review"},
		TaskBank:   "review-regression",
		Variants: []Variant{
			{Name: "base", Methodology: "default"},
			{Name: "candidate", Methodology: "strict-review"},
		},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Experiment != "compare-review-methods" {
		t.Fatalf("unexpected experiment: %q", plan.Experiment)
	}
	if len(plan.Steps) != 5 {
		t.Fatalf("unexpected steps: %#v", plan.Steps)
	}
	if len(plan.Diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
}

func TestServicePlanRequiresVariants(t *testing.T) {
	t.Parallel()

	if _, err := NewService().Plan(context.Background(), Experiment{
		Name:       "invalid",
		Hypothesis: Hypothesis{Name: "h"},
	}); err == nil {
		t.Fatal("expected validation error")
	}
}
