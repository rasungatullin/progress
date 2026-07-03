package analytics

import (
	"context"
	"time"
)

type RunSample struct {
	Route    string
	Action   string
	Status   string
	Duration time.Duration
	Cost     float64
	Repeats  int
}

type Measurement struct {
	Name   string
	Value  float64
	Unit   string
	Labels map[string]string
}

type Report struct {
	Samples      int
	Measurements []Measurement
}

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Calculate(ctx context.Context, samples []RunSample) Report {
	_ = ctx
	_ = s

	if len(samples) == 0 {
		return Report{}
	}

	var completed int
	var totalDuration time.Duration
	var totalCost float64
	var totalRepeats int
	for _, sample := range samples {
		if sample.Status == "completed" || sample.Status == "ok" || sample.Status == "success" {
			completed++
		}
		totalDuration += sample.Duration
		totalCost += sample.Cost
		totalRepeats += sample.Repeats
	}

	count := float64(len(samples))
	return Report{
		Samples: len(samples),
		Measurements: []Measurement{
			{Name: "runs_total", Value: count, Unit: "count"},
			{Name: "success_rate", Value: float64(completed) / count, Unit: "ratio"},
			{Name: "duration_average_seconds", Value: totalDuration.Seconds() / count, Unit: "seconds"},
			{Name: "cost_total", Value: totalCost, Unit: "currency"},
			{Name: "repeats_total", Value: float64(totalRepeats), Unit: "count"},
		},
	}
}
