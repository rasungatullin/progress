package reactivity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/integration"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
	"github.com/rasungatullin/progress/internal/methodology"
)

const (
	ReactionProcessKind          = "reactivity-process"
	ReactionProcessTargetContour = "reactivity"
	ReactionProcessSourceTracker = "tracker-task-search"

	ProcessStopReasonDisabled        = "process-disabled"
	ProcessStopReasonSingleCycle     = "single-cycle"
	ProcessStopReasonContextCanceled = "context-canceled"
)

type ProcessRunInput struct {
	Name     string
	Once     bool
	WaitOnce bool
}

type ReactionProcess struct {
	Kind          string                 `json:"kind"`
	Name          string                 `json:"name"`
	TargetContour string                 `json:"target_contour"`
	Title         string                 `json:"title,omitempty"`
	Description   string                 `json:"description,omitempty"`
	Payload       ReactionProcessPayload `json:"payload"`
}

type ReactionProcessPayload struct {
	Enabled        bool                     `json:"enabled"`
	Source         ProcessTaskSource        `json:"source"`
	TaskProcessing ProcessTaskProcessing    `json:"task_processing"`
	Cycle          ReactionProcessCycleSpec `json:"cycle"`
}

type ProcessTaskSource struct {
	Type          string   `json:"type"`
	System        string   `json:"system,omitempty"`
	State         string   `json:"state,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	ExcludeLabels []string `json:"exclude_labels,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

type ProcessTaskProcessing struct {
	Route     string `json:"route,omitempty"`
	Once      bool   `json:"once,omitempty"`
	MaxCycles int    `json:"max_cycles,omitempty"`
}

type ReactionProcessCycleSpec struct {
	MinDuration string `json:"min_duration,omitempty"`
}

type ProcessRunResult struct {
	ProcessName string
	Title       string
	Completed   bool
	StopReason  string
	Cycles      []ProcessRunCycleResult
}

type ProcessRunCycleResult struct {
	Index          int
	FoundTasks     []int
	ProcessedTasks []TaskProcessingResult
	StartedAt      time.Time
	Duration       time.Duration
	WaitDuration   time.Duration
	Error          string
}

func (s *Service) RunProcess(ctx context.Context, input ProcessRunInput) (ProcessRunResult, error) {
	if s == nil {
		s = NewService(nil)
	}
	s.ensureProcessDependencies()

	process, err := s.loadReactionProcess(ctx, input.Name)
	if err != nil {
		return ProcessRunResult{}, err
	}
	result := ProcessRunResult{ProcessName: process.Name, Title: process.Title}
	if !process.Payload.Enabled {
		result.Completed = true
		result.StopReason = ProcessStopReasonDisabled
		return result, nil
	}
	if err := validateReactionProcess(process); err != nil {
		return result, err
	}

	for cycleIndex := 1; ; cycleIndex++ {
		cycle, err := s.runProcessCycle(ctx, process, cycleIndex, input)
		result.Cycles = append(result.Cycles, cycle)
		if err != nil {
			return result, err
		}
		if input.Once {
			result.Completed = true
			result.StopReason = ProcessStopReasonSingleCycle
			return result, nil
		}
		if err := ctx.Err(); err != nil {
			result.Completed = true
			result.StopReason = ProcessStopReasonContextCanceled
			return result, err
		}
	}
}

func (s *Service) runProcessCycle(ctx context.Context, process ReactionProcess, index int, input ProcessRunInput) (ProcessRunCycleResult, error) {
	startedAt := s.now()
	cycle := ProcessRunCycleResult{Index: index, StartedAt: startedAt}

	tasks, err := s.searchProcessTasks(ctx, process.Payload.Source)
	if err != nil {
		cycle.Duration = s.now().Sub(startedAt)
		cycle.Error = err.Error()
		return cycle, err
	}
	cycle.FoundTasks = tasks

	processing := process.Payload.TaskProcessing
	for _, taskNumber := range tasks {
		processed, err := s.ProcessTask(ctx, TaskProcessingInput{
			TaskNumber: taskNumber,
			Route:      processing.Route,
			Once:       processing.Once,
			MaxCycles:  processing.MaxCycles,
		})
		cycle.ProcessedTasks = append(cycle.ProcessedTasks, processed)
		if err != nil {
			cycle.Duration = s.now().Sub(startedAt)
			cycle.Error = err.Error()
			return cycle, err
		}
	}

	cycle.Duration = s.now().Sub(startedAt)
	if !input.Once || input.WaitOnce {
		waitDuration, err := s.waitProcessCycleRemainder(ctx, process, startedAt)
		cycle.WaitDuration = waitDuration
		cycle.Duration = s.now().Sub(startedAt)
		if err != nil {
			cycle.Error = err.Error()
			return cycle, err
		}
	}
	return cycle, nil
}

func (s *Service) loadReactionProcess(ctx context.Context, name string) (ReactionProcess, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ReactionProcess{}, fmt.Errorf("имя процесса реакции должно быть задано")
	}
	element, err := s.methodology.Get(ctx, methodology.ElementRequest{Kind: methodology.ElementKindEntity, EntityKind: ReactionProcessKind, Name: name})
	if err != nil {
		return ReactionProcess{}, fmt.Errorf("загрузить процесс реакции %q из каталога методик: %w", name, err)
	}
	if element.Entity == nil {
		return ReactionProcess{}, fmt.Errorf("сущность методики %q не является процессом реакции", name)
	}

	process := ReactionProcess{
		Kind:          element.Entity.Kind,
		Name:          element.Entity.Name,
		TargetContour: element.Entity.TargetContour,
		Title:         element.Entity.Title,
		Description:   element.Entity.Description,
	}
	if len(element.Entity.Payload) == 0 {
		return ReactionProcess{}, fmt.Errorf("процесс реакции %q не содержит payload", name)
	}
	if err := json.Unmarshal(element.Entity.Payload, &process.Payload); err != nil {
		return ReactionProcess{}, fmt.Errorf("разобрать payload процесса реакции %q: %w", name, err)
	}
	return process, nil
}

func validateReactionProcess(process ReactionProcess) error {
	if process.Kind != ReactionProcessKind {
		return fmt.Errorf("процесс реакции %q имеет неподдержанный kind %q", process.Name, process.Kind)
	}
	if process.TargetContour != ReactionProcessTargetContour {
		return fmt.Errorf("процесс реакции %q имеет неподдержанный target_contour %q", process.Name, process.TargetContour)
	}
	if process.Payload.Source.Type != ReactionProcessSourceTracker {
		return fmt.Errorf("процесс реакции %q имеет неподдержанный источник %q", process.Name, process.Payload.Source.Type)
	}
	if countNonEmptyStrings(process.Payload.Source.Labels) == 0 {
		return fmt.Errorf("процесс реакции %q должен задавать хотя бы одну включающую метку источника", process.Name)
	}
	if minDurationText := strings.TrimSpace(process.Payload.Cycle.MinDuration); minDurationText != "" {
		if _, err := time.ParseDuration(minDurationText); err != nil {
			return fmt.Errorf("разобрать cycle.min_duration процесса реакции %q: %w", process.Name, err)
		}
	}
	return nil
}

func countNonEmptyStrings(values []string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}

func (s *Service) searchProcessTasks(ctx context.Context, source ProcessTaskSource) ([]int, error) {
	response, err := s.integration.Execute(ctx, integration.Request{
		IntegrationType: integrationmodel.IntegrationTypeTracker,
		System:          source.System,
		SystemProvided:  strings.TrimSpace(source.System) != "",
		Resource:        "task",
		ObjectType:      "task",
		Operation:       "search",
		State:           source.State,
		Labels:          append([]string(nil), source.Labels...),
		ExcludeLabels:   append([]string(nil), source.ExcludeLabels...),
		Limit:           source.Limit,
	})
	if err != nil {
		return nil, err
	}
	numbers := make([]int, 0, len(response.SearchResults))
	for _, task := range response.SearchResults {
		if task.Number <= 0 {
			continue
		}
		numbers = append(numbers, task.Number)
	}
	return numbers, nil
}

func (s *Service) waitProcessCycleRemainder(ctx context.Context, process ReactionProcess, startedAt time.Time) (time.Duration, error) {
	minDurationText := strings.TrimSpace(process.Payload.Cycle.MinDuration)
	if minDurationText == "" {
		return 0, nil
	}
	minDuration, err := time.ParseDuration(minDurationText)
	if err != nil {
		return 0, fmt.Errorf("разобрать cycle.min_duration процесса реакции %q: %w", process.Name, err)
	}
	elapsed := s.now().Sub(startedAt)
	if elapsed >= minDuration {
		return 0, nil
	}
	waitDuration := minDuration - elapsed
	return waitDuration, s.wait(ctx, waitDuration)
}

func (s *Service) ensureProcessDependencies() {
	s.ensureProcessingDependencies()
	if s.now == nil {
		s.now = time.Now
	}
	if s.wait == nil {
		s.wait = waitContext
	}
	if s.methodology == nil {
		s.methodology = methodology.NewService(s.logger)
	}
}
