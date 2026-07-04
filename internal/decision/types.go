package decision

import (
	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
)

const (
	SignalSourceTask = "task"
	SignalKindTask   = "task-number"

	DecisionTypeExecute = "execute"

	ConsiderationStatusExecution          = "execution"
	ConsiderationStatusCompleted          = "completed"
	ConsiderationStatusAwaiting           = "awaiting-external-change"
	ConsiderationStatusManualIntervention = "manual-intervention"
	ConsiderationStatusFailed             = "failed"

	RouteCheckStatusPassed  = "passed"
	RouteCheckStatusFailed  = "failed"
	RouteCheckStatusSkipped = "skipped"
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
	Task   integration.CanonicalTask
	Issue  *integration.TrackerIssue
}

type DecisionType string

type DecisionReason struct {
	Code    string
	Message string
}

type ConsiderationStatus string

type RouteCheckStatus string

type ProcessingRoute struct {
	Name        string
	Title       string
	Description string
}

type RouteCheckResult struct {
	Name    string
	Status  RouteCheckStatus
	Reasons []DecisionReason
}

type DecisionFailure struct {
	Code               string
	Message            string
	Retryable          bool
	ManualIntervention bool
}

type ExecutionPlan struct {
	TaskNumber      int
	TaskTitle       string
	Action          string
	ExpectedResult  string
	Constraints     []string
	Route           ProcessingRoute
	Reasons         []DecisionReason
	Assignment      *execution.ExecutionAssignment
	StructuredInput *execution.StructuredInput
}

type Decision struct {
	Type          DecisionType
	Reasons       []DecisionReason
	Route         ProcessingRoute
	Checks        []RouteCheckResult
	Status        ConsiderationStatus
	Failure       *DecisionFailure
	ExecutionPlan *ExecutionPlan
}

type ConsiderationInput struct {
	Context DecisionContext
}

type ConsiderationResult struct {
	Context       DecisionContext
	Status        ConsiderationStatus
	Route         ProcessingRoute
	Checks        []RouteCheckResult
	Reasons       []DecisionReason
	ExecutionPlan *ExecutionPlan
	Failure       *DecisionFailure
}

type StartResult struct {
	Context         DecisionContext
	Ready           bool
	Consideration   *ConsiderationResult
	Decision        *Decision
	ExecutionResult *execution.ExecutionResult
	Execution       *execution.LaunchResult
}
