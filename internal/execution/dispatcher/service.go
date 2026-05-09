package dispatcher

import (
	"context"
	"log"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

type executor interface {
	ResolveProfile(context.Context, model.Invocation) (model.Profile, error)
	AllocateResources(context.Context, model.Invocation, model.Profile) (model.Allocation, error)
	PrepareWorkplace(context.Context, model.Invocation, model.Profile, model.Allocation) (model.Workplace, error)
	Launch(context.Context, model.Invocation, model.Profile, model.Allocation, model.Workplace) (model.LaunchResult, error)
}

type Service struct {
	logger *log.Logger
}

func NewService(logger *log.Logger) *Service {
	return &Service{logger: logger}
}

func (s *Service) Plan(_ context.Context, in model.Invocation) []string {
	stages := []string{"profile", "resources", "workplace", "launch"}
	s.logger.Printf("Диспетчер сформировал маршрут: задача=%q стадии=%s", in.Task, strings.Join(stages, " -> "))
	return stages
}

func (s *Service) Run(ctx context.Context, in model.Invocation, exec executor) (model.LaunchResult, error) {
	s.logger.Printf("Контур исполнения принят к пуску: задача=%q", in.Task)

	profile, err := exec.ResolveProfile(ctx, in)
	if err != nil {
		return model.LaunchResult{}, err
	}

	allocation, err := exec.AllocateResources(ctx, in, profile)
	if err != nil {
		return model.LaunchResult{}, err
	}

	workplace, err := exec.PrepareWorkplace(ctx, in, profile, allocation)
	if err != nil {
		return model.LaunchResult{}, err
	}

	return exec.Launch(ctx, in, profile, allocation, workplace)
}
