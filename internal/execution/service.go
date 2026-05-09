package execution

import (
	"context"
	"fmt"
	"log"
	"strings"
)

type Invocation struct {
	Task string
}

type Profile struct {
	Name string
	Mode string
}

type Allocation struct {
	Resource string
	Reserved bool
}

type Workplace struct {
	Name  string
	Ready bool
}

type LaunchResult struct {
	Status  string
	Summary string
}

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
}

func NewService(logger *log.Logger) *Service {
	return &Service{
		logger:     logger,
		profiles:   stubProfileResolver{},
		resources:  stubResourceProvider{},
		workplaces: stubWorkplaceManager{},
		launcher:   stubLauncher{},
	}
}

func (s *Service) Start(ctx context.Context, in Invocation) (LaunchResult, error) {
	s.logger.Printf("Контур исполнения принят к пуску: задача=%q", in.Task)

	profile, err := s.ResolveProfile(ctx, in)
	if err != nil {
		return LaunchResult{}, err
	}

	allocation, err := s.AllocateResources(ctx, in, profile)
	if err != nil {
		return LaunchResult{}, err
	}

	workplace, err := s.PrepareWorkplace(ctx, in, profile, allocation)
	if err != nil {
		return LaunchResult{}, err
	}

	return s.Launch(ctx, in, profile, allocation, workplace)
}

func (s *Service) Dispatch(_ context.Context, in Invocation) []string {
	stages := []string{"profile", "resources", "workplace", "launch"}
	s.logger.Printf("Диспетчер сформировал маршрут: задача=%q стадии=%s", in.Task, strings.Join(stages, " -> "))
	return stages
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
	result, err := s.launcher.Launch(ctx, in, profile, allocation, workplace)
	if err != nil {
		return LaunchResult{}, err
	}

	s.logger.Printf("Пуск задачи завершён: задача=%q состояние=%q", in.Task, result.Status)
	return result, nil
}

type stubProfileResolver struct{}

func (stubProfileResolver) Resolve(_ context.Context, in Invocation) (Profile, error) {
	return Profile{Name: "local-default", Mode: "manual"}, nil
}

type stubResourceProvider struct{}

func (stubResourceProvider) Allocate(_ context.Context, _ Invocation, profile Profile) (Allocation, error) {
	return Allocation{Resource: "local-slot:" + profile.Name, Reserved: true}, nil
}

type stubWorkplaceManager struct{}

func (stubWorkplaceManager) Prepare(_ context.Context, in Invocation, profile Profile, _ Allocation) (Workplace, error) {
	name := fmt.Sprintf("workspace/%s/%s", profile.Name, sanitizeName(in.Task))
	return Workplace{Name: name, Ready: true}, nil
}

type stubLauncher struct{}

func (stubLauncher) Launch(_ context.Context, in Invocation, profile Profile, allocation Allocation, workplace Workplace) (LaunchResult, error) {
	summary := fmt.Sprintf("profile=%s resource=%s workplace=%s", profile.Name, allocation.Resource, workplace.Name)
	return LaunchResult{Status: "completed", Summary: summary}, nil
}

func sanitizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	if value == "" {
		return "unnamed"
	}

	return value
}
