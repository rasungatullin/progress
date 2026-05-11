package decision

import "github.com/rasungatullin/progress/internal/integration"

const (
	SignalSourceTask = "task"
	SignalKindTask   = "task-number"
)

type Signal struct {
	Source     string
	Kind       string
	TaskNumber int
}

type StartInput struct {
	TaskNumber int
}

type DecisionContext struct {
	Signal Signal
	Issue  *integration.TrackerIssue
}

type StartResult struct {
	Context DecisionContext
	Ready   bool
}
