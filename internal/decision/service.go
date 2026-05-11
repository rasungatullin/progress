package decision

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/rasungatullin/progress/internal/integration"
)

type integrationExecutor interface {
	Execute(context.Context, integration.Request) (integration.Response, error)
}

type Service struct {
	logger      *log.Logger
	integration integrationExecutor
}

func NewService(logger *log.Logger) *Service {
	return &Service{
		logger:      ensureLogger(logger),
		integration: integration.NewService(ensureLogger(logger)),
	}
}

func (s *Service) Start(ctx context.Context, input StartInput) (StartResult, error) {
	if input.TaskNumber <= 0 {
		return StartResult{}, fmt.Errorf("task number must be greater than zero")
	}

	signal := Signal{
		Source:     SignalSourceTask,
		Kind:       SignalKindTask,
		TaskNumber: input.TaskNumber,
	}

	response, err := s.integration.Execute(ctx, integration.Request{
		System:    "github",
		Resource:  "issue",
		Operation: "get",
		Number:    input.TaskNumber,
	})
	if err != nil {
		return StartResult{}, err
	}
	if response.Issue == nil {
		return StartResult{}, fmt.Errorf("integration did not return issue for task %d", input.TaskNumber)
	}

	result := StartResult{
		Context: DecisionContext{
			Signal: signal,
			Issue:  response.Issue,
		},
		Ready: true,
	}

	s.logger.Printf("Контекст решения собран: задача=%d готовность=%t", input.TaskNumber, result.Ready)
	return result, nil
}

func ensureLogger(logger *log.Logger) *log.Logger {
	if logger != nil {
		return logger
	}

	return log.New(io.Discard, "", 0)
}
