package decision

import (
	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
)

const (
	SignalSourceTask = "task"
	SignalKindTask   = "task-number"

	DecisionTypeExecute = "execute"
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

type DecisionType string

type DecisionReason struct {
	Code    string
	Message string
}

type ExecutionPlan struct {
	TaskNumber int
	TaskTitle  string
	Profile    string
	Prompt     string
}

type Decision struct {
	Type          DecisionType
	Reasons       []DecisionReason
	ExecutionPlan *ExecutionPlan
}

type StartResult struct {
	Context   DecisionContext
	Ready     bool
	Decision  *Decision
	Execution *execution.LaunchResult
}
