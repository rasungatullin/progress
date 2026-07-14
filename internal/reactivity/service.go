package reactivity

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/rasungatullin/progress/internal/decision"
	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/integration"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
)

const (
	StatusAccepted = "accepted"
	StatusIgnored  = "ignored"
)

type Event struct {
	Source     string
	Kind       string
	ObjectType string
	ObjectID   string
	OccurredAt time.Time
	Metadata   map[string]string
}

type Process struct {
	Name            string
	EventSource     string
	EventKind       string
	SignalKind      string
	IntegrationType string
	ObjectType      string
	Disabled        bool
}

type Signal struct {
	Source          string
	Kind            string
	Process         string
	IntegrationType string
	ObjectType      string
	ObjectID        string
	OccurredAt      time.Time
	Metadata        map[string]string
}

type Reason struct {
	Code    string
	Message string
}

type Result struct {
	Status  string
	Signal  *Signal
	Reasons []Reason
}

type Service struct {
	logger                       *log.Logger
	now                          func() time.Time
	integration                  integrationExecutor
	decision                     decisionConsiderer
	execution                    executionStarter
	fingerprintMu                sync.Mutex
	resolvedConflictFingerprints map[int]string
}

var resolvedConflictAttempts = struct {
	sync.Mutex
	values map[int]string
}{values: make(map[int]string)}

func NewService(logger *log.Logger) *Service {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	return &Service{
		logger:                       logger,
		now:                          time.Now,
		integration:                  integration.NewConfiguredService(logger),
		decision:                     decision.NewService(logger),
		execution:                    execution.NewService(logger),
		resolvedConflictFingerprints: make(map[int]string),
	}
}

type integrationExecutor interface {
	Execute(context.Context, integration.Request) (integration.Response, error)
}

type decisionConsiderer interface {
	Consider(context.Context, decision.ConsiderationInput) (decision.ConsiderationResult, error)
}

type executionStarter interface {
	ExecuteAction(context.Context, execution.ActionInvocation) (execution.ExecutionResult, error)
}

const (
	LabelAwaitingReview = "Ожидает экспертизы"
	LabelNeedsRework    = "Требует доработки"
	LabelReviewPassed   = "Экспертиза пройдена"

	defaultMaxProcessingCycles = 20

	integrationTypeTracker    = integrationmodel.IntegrationTypeTracker
	integrationTypeRepository = integrationmodel.IntegrationTypeRepository
)

func (s *Service) Normalize(ctx context.Context, event Event, process Process) (Result, error) {
	_ = ctx

	if s == nil {
		s = NewService(nil)
	}
	if s.now == nil {
		s.now = time.Now
	}

	process.Name = strings.TrimSpace(process.Name)
	if process.Name == "" {
		return Result{}, fmt.Errorf("имя процесса реакции должно быть непустым")
	}
	if process.Disabled {
		return ignored("process_disabled", "Процесс реакции отключён."), nil
	}

	event.Source = strings.TrimSpace(event.Source)
	event.Kind = strings.TrimSpace(event.Kind)
	event.ObjectType = strings.TrimSpace(event.ObjectType)
	event.ObjectID = strings.TrimSpace(event.ObjectID)
	if expected := strings.TrimSpace(process.EventSource); expected != "" && expected != event.Source {
		return ignored("source_mismatch", "Источник события не соответствует процессу реакции."), nil
	}
	if expected := strings.TrimSpace(process.EventKind); expected != "" && expected != event.Kind {
		return ignored("kind_mismatch", "Вид события не соответствует процессу реакции."), nil
	}
	if event.Source == "" {
		return Result{}, fmt.Errorf("источник события должен быть непустым")
	}
	if event.Kind == "" {
		return Result{}, fmt.Errorf("вид события должен быть непустым")
	}
	if event.ObjectID == "" {
		return Result{}, fmt.Errorf("идентификатор объекта события должен быть непустым")
	}

	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}

	objectType := firstNonEmpty(process.ObjectType, event.ObjectType)
	signalKind := firstNonEmpty(process.SignalKind, event.Kind)
	signal := Signal{
		Source:          event.Source,
		Kind:            signalKind,
		Process:         process.Name,
		IntegrationType: strings.TrimSpace(process.IntegrationType),
		ObjectType:      strings.TrimSpace(objectType),
		ObjectID:        event.ObjectID,
		OccurredAt:      occurredAt,
		Metadata:        cloneMap(event.Metadata),
	}

	s.logger.Printf("Контур реакции нормализовал сигнал: процесс=%q источник=%q объект=%q", signal.Process, signal.Source, signal.ObjectID)
	return Result{
		Status: StatusAccepted,
		Signal: &signal,
		Reasons: []Reason{{
			Code:    "signal_normalized",
			Message: "Внешнее событие приведено к нормализованному сигналу.",
		}},
	}, nil
}

func ignored(code string, message string) Result {
	return Result{
		Status: StatusIgnored,
		Reasons: []Reason{{
			Code:    code,
			Message: message,
		}},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
