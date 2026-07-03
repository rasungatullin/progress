package observability

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const StatusRecorded = "recorded"

type Event struct {
	ID        string
	Time      time.Time
	Contour   string
	Module    string
	Operation string
	Status    string
	Message   string
	Metadata  map[string]string
}

type RecordInput struct {
	Contour   string
	Module    string
	Operation string
	Status    string
	Message   string
	Metadata  map[string]string
}

type Filter struct {
	Contour string
	Status  string
	Limit   int
}

type Service struct {
	mu      sync.Mutex
	events  []Event
	nextID  int
	nowFunc func() time.Time
}

func NewService() *Service {
	return &Service{nowFunc: time.Now}
}

func (s *Service) Record(ctx context.Context, input RecordInput) (Event, error) {
	_ = ctx

	if s == nil {
		s = NewService()
	}
	if s.nowFunc == nil {
		s.nowFunc = time.Now
	}

	input.Contour = strings.TrimSpace(input.Contour)
	input.Operation = strings.TrimSpace(input.Operation)
	if input.Contour == "" {
		return Event{}, fmt.Errorf("контур должен быть непустым")
	}
	if input.Operation == "" {
		return Event{}, fmt.Errorf("операция должна быть непустой")
	}

	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = StatusRecorded
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	event := Event{
		ID:        fmt.Sprintf("%d", s.nextID),
		Time:      s.nowFunc(),
		Contour:   input.Contour,
		Module:    strings.TrimSpace(input.Module),
		Operation: input.Operation,
		Status:    status,
		Message:   strings.TrimSpace(input.Message),
		Metadata:  cloneMap(input.Metadata),
	}
	s.events = append(s.events, event)
	return event, nil
}

func (s *Service) List(ctx context.Context, filter Filter) []Event {
	_ = ctx

	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Event
	for _, event := range s.events {
		if filter.Contour != "" && event.Contour != filter.Contour {
			continue
		}
		if filter.Status != "" && event.Status != filter.Status {
			continue
		}
		out = append(out, cloneEvent(event))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

func cloneEvent(event Event) Event {
	event.Metadata = cloneMap(event.Metadata)
	return event
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
