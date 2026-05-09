package execution

import (
	"context"
	"log"

	"github.com/rasungatullin/progress/internal/execution/dispatcher"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/execution/profile"
	"github.com/rasungatullin/progress/internal/execution/resources"
	"github.com/rasungatullin/progress/internal/execution/workplace"
)

type Invocation = model.Invocation
type LaunchSpec = model.LaunchSpec
type WorkplaceSpec = model.WorkplaceSpec
type Profile = model.Profile
type Allocation = model.Allocation
type Workplace = model.Workplace
type LaunchResult = model.LaunchResult

type ProfileResolver interface {
	Resolve(context.Context, Invocation) (Profile, error)
}

type ResourceProvider interface {
	Allocate(context.Context, Invocation, Profile) (Allocation, error)
}

type WorkplaceManager interface {
	Prepare(context.Context, Invocation, Profile, Allocation) (Workplace, error)
}

type Launcher interface {
	Launch(context.Context, Invocation, Profile, Allocation, Workplace) (LaunchResult, error)
}

type Service struct {
	logger     *log.Logger
	profiles   ProfileResolver
	resources  ResourceProvider
	workplaces WorkplaceManager
	launcher   Launcher
	dispatcher *dispatcher.Service
}

func NewService(logger *log.Logger) *Service {
	profiles := profile.NewService()
	resources := resources.NewService()
	workplaces := workplace.NewService()
	launcher := launch.NewService()

	return &Service{
		logger:     logger,
		profiles:   profiles,
		resources:  resources,
		workplaces: workplaces,
		launcher:   launcher,
		dispatcher: dispatcher.NewService(logger),
	}
}

func (s *Service) Start(ctx context.Context, in Invocation) (LaunchResult, error) {
	return s.dispatcher.Run(ctx, in, s)
}

func (s *Service) Dispatch(ctx context.Context, in Invocation) []string {
	return s.dispatcher.Plan(ctx, in)
}

func (s *Service) LaunchDirect(ctx context.Context, in Invocation) (LaunchResult, error) {
	profile := Profile{Name: "direct-launch", Mode: "manual", Model: in.Launch.Model, CommitPush: false}
	allocation := Allocation{Resource: "external-launch", Reserved: true}
	workplace := Workplace{Name: in.Launch.Directory, Ready: true}

	return s.Launch(ctx, in, profile, allocation, workplace)
}

func (s *Service) ResolveProfile(ctx context.Context, in Invocation) (Profile, error) {
	profile, err := s.profiles.Resolve(ctx, in)
	if err != nil {
		return Profile{}, err
	}

	s.logger.Printf("Профиль определён: задача=%q профиль=%q режим=%q", in.Task, profile.Name, profile.Mode)
	return profile, nil
}

func (s *Service) AllocateResources(ctx context.Context, in Invocation, profile Profile) (Allocation, error) {
	allocation, err := s.resources.Allocate(ctx, in, profile)
	if err != nil {
		return Allocation{}, err
	}

	s.logger.Printf("Ресурсы подтверждены: задача=%q ресурс=%q резерв=%t", in.Task, allocation.Resource, allocation.Reserved)
	return allocation, nil
}

func (s *Service) PrepareWorkplace(ctx context.Context, in Invocation, profile Profile, allocation Allocation) (Workplace, error) {
	workplace, err := s.workplaces.Prepare(ctx, in, profile, allocation)
	if err != nil {
		return Workplace{}, err
	}

	s.logger.Printf("Рабочее место подготовлено: задача=%q среда=%q готовность=%t", in.Task, workplace.Name, workplace.Ready)
	return workplace, nil
}

func (s *Service) Launch(ctx context.Context, in Invocation, profile Profile, allocation Allocation, workplace Workplace) (LaunchResult, error) {
	if in.Launch.Model == "" {
		in.Launch.Model = profile.Model
	}

	s.logger.Printf("Запуск выполнения начат: каталог=%q runner=%q модель=%q", in.Launch.Directory, in.Launch.Runner, in.Launch.Model)

	result, err := s.launcher.Launch(ctx, in, profile, allocation, workplace)
	if err != nil {
		return LaunchResult{}, err
	}

	s.logger.Printf("Запуск выполнения завершён: каталог=%q состояние=%q", in.Launch.Directory, result.Status)
	return result, nil
}
