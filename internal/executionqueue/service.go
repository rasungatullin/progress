package executionqueue

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	StatusQueued             = "queued"
	StatusRunning            = "running"
	StatusManualIntervention = "manual-intervention"
)

type TaskRef struct {
	System     string
	Repository string
	Number     int
	ExternalID string
	Title      string
}

type EnqueueRequest struct {
	Task         TaskRef
	Priority     int
	NotBefore    time.Time
	MaxAttempts  int
	AssignmentID string
}

type Item struct {
	ID           string
	Task         TaskRef
	Priority     int
	NotBefore    time.Time
	Attempts     int
	MaxAttempts  int
	AssignmentID string
	Status       string
	CreatedAt    time.Time
}

type Service struct {
	mu      sync.Mutex
	items   []Item
	nextID  int
	nowFunc func() time.Time
}

func NewService() *Service {
	return &Service{nowFunc: time.Now}
}

func (s *Service) Enqueue(ctx context.Context, request EnqueueRequest) (Item, error) {
	_ = ctx

	if s == nil {
		s = NewService()
	}
	if s.nowFunc == nil {
		s.nowFunc = time.Now
	}
	if request.Task.Number <= 0 && strings.TrimSpace(request.Task.ExternalID) == "" {
		return Item{}, fmt.Errorf("задача очереди должна задавать номер или внешний идентификатор")
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = 1
	}
	if request.MaxAttempts < 0 {
		return Item{}, fmt.Errorf("предельное число попыток должно быть неотрицательным")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	item := Item{
		ID:           fmt.Sprintf("%d", s.nextID),
		Task:         request.Task,
		Priority:     request.Priority,
		NotBefore:    request.NotBefore,
		MaxAttempts:  request.MaxAttempts,
		AssignmentID: strings.TrimSpace(request.AssignmentID),
		Status:       StatusQueued,
		CreatedAt:    s.nowFunc(),
	}
	s.items = append(s.items, item)
	return item, nil
}

func (s *Service) Next(ctx context.Context, now time.Time) (Item, bool) {
	_ = ctx

	if s == nil {
		return Item{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	best := -1
	for index, item := range s.items {
		if item.Status != StatusQueued {
			continue
		}
		if !item.NotBefore.IsZero() && item.NotBefore.After(now) {
			continue
		}
		if best == -1 || item.Priority > s.items[best].Priority || item.Priority == s.items[best].Priority && item.CreatedAt.Before(s.items[best].CreatedAt) {
			best = index
		}
	}
	if best == -1 {
		return Item{}, false
	}

	s.items[best].Status = StatusRunning
	return s.items[best], true
}

func (s *Service) MarkAttempt(ctx context.Context, id string) (Item, error) {
	_ = ctx

	if s == nil {
		return Item{}, fmt.Errorf("сервис очереди не инициализирован")
	}
	id = strings.TrimSpace(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	for index := range s.items {
		if s.items[index].ID != id {
			continue
		}
		s.items[index].Attempts++
		if s.items[index].MaxAttempts > 0 && s.items[index].Attempts >= s.items[index].MaxAttempts {
			s.items[index].Status = StatusManualIntervention
		} else {
			s.items[index].Status = StatusQueued
		}
		return s.items[index], nil
	}

	return Item{}, fmt.Errorf("элемент очереди %q не найден", id)
}

func (s *Service) List(ctx context.Context) []Item {
	_ = ctx

	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Item(nil), s.items...)
}
