package analytics

import (
	"context"
	"testing"
	"time"
)

func TestServiceCalculateBuildsReport(t *testing.T) {
	t.Parallel()

	report := NewService().Calculate(context.Background(), []RunSample{
		{Status: "completed", Duration: 2 * time.Second, Cost: 1.5, Repeats: 1},
		{Status: "failed", Duration: 4 * time.Second, Cost: 2.5, Repeats: 2},
	})
	if report.Samples != 2 {
		t.Fatalf("unexpected sample count: %d", report.Samples)
	}
	values := map[string]float64{}
	for _, measurement := range report.Measurements {
		values[measurement.Name] = measurement.Value
	}
	if values["success_rate"] != 0.5 {
		t.Fatalf("unexpected success rate: %v", values["success_rate"])
	}
	if values["duration_average_seconds"] != 3 {
		t.Fatalf("unexpected average duration: %v", values["duration_average_seconds"])
	}
	if values["cost_total"] != 4 {
		t.Fatalf("unexpected total cost: %v", values["cost_total"])
	}
}
