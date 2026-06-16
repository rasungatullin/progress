package execution

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/dispatcher"
	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/execution/profile"
	"github.com/rasungatullin/progress/internal/execution/resources"
	"github.com/rasungatullin/progress/internal/execution/workplace"
)

type Invocation = model.Invocation
type LaunchSpec = model.LaunchSpec
type RepositorySpec = model.RepositorySpec
type WorkplaceSpec = model.WorkplaceSpec
type Profile = model.Profile
type Allocation = model.Allocation
type Workplace = model.Workplace
type LaunchResult = model.LaunchResult
type StructuredInput = model.StructuredInput
type StructuredOutput = model.StructuredOutput
type StructuredExtensions = model.StructuredExtensions
type StructuredContext = model.StructuredContext
type StructuredResult = model.StructuredResult
type StructuredRemark = model.StructuredRemark
type StructuredResponse = model.StructuredResponse
type StructuredQuestion = model.StructuredQuestion
type StructuredAction = model.StructuredAction
type StructuredChange = model.StructuredChange
type StructuredCommand = model.StructuredCommand
type StructuredConclusion = model.StructuredConclusion

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
	s.logger.Printf("Контур исполнения принят к пуску: задача=%q", in.Task)
	historyRoot := executionHistoryRoot(in, Workplace{})
	historyHandle := s.beginStartHistory(ctx, historyRoot, in)

	profile, err := s.ResolveProfile(ctx, in)
	if err != nil {
		result := failedStartResult(err)
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, Profile{}, Allocation{}, Workplace{}, result, err)
		return result, err
	}

	allocation, err := s.AllocateResources(ctx, in, profile)
	if err != nil {
		result := failedStartResult(err)
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile, Allocation{}, Workplace{}, result, err)
		return result, err
	}

	workplace, err := s.PrepareWorkplace(ctx, in, profile, allocation)
	if err != nil {
		result := failedStartResult(err)
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile, allocation, Workplace{}, result, err)
		return result, err
	}

	if strings.TrimSpace(in.Launch.Directory) == "" {
		in.Launch.Directory = workplace.Name
	}
	in.Launch.Runner = allocation.Runner
	in.Launch.Model = allocation.Model
	if strings.TrimSpace(in.Launch.ModelBinding) == "" {
		in.Launch.ModelBinding = allocation.ModelBinding
	}
	s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile, allocation, workplace, LaunchResult{Status: "running"}, nil)

	launchCtx := launch.WithHistoryHandle(ctx, historyHandle)
	result, err := s.Launch(launchCtx, in, profile, allocation, workplace)
	s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile, allocation, workplace, result, err)
	return result, err
}

func (s *Service) Dispatch(ctx context.Context, in Invocation) []string {
	return s.dispatcher.Plan(ctx, in)
}

func (s *Service) LaunchDirect(ctx context.Context, in Invocation) (LaunchResult, error) {
	profile := Profile{Name: "direct-launch", Mode: "manual", CommitPush: false}
	allocation := Allocation{
		Resource:     "external-launch",
		Reserved:     true,
		Runner:       in.Launch.Runner,
		Model:        in.Launch.Model,
		ModelBinding: in.Launch.ModelBinding,
		Source:       "direct-launch",
	}
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

	s.logger.Printf("Ресурсы подтверждены: задача=%q ресурс=%q резерв=%t runner=%q model=%q binding=%q source=%q fallback=%t", in.Task, allocation.Resource, allocation.Reserved, allocation.Runner, allocation.Model, allocation.ModelBinding, allocation.Source, allocation.FallbackUsed)
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
	in.Launch.Runner = allocation.Runner
	in.Launch.Model = allocation.Model
	if strings.TrimSpace(in.Launch.ModelBinding) == "" {
		in.Launch.ModelBinding = allocation.ModelBinding
	}

	s.logger.Printf("Запуск выполнения начат: каталог=%q runner=%q модель=%q", in.Launch.Directory, in.Launch.Runner, in.Launch.Model)

	result, err := s.launcher.Launch(ctx, in, profile, allocation, workplace)
	if err != nil {
		return result, err
	}

	s.logger.Printf("Запуск выполнения завершён: каталог=%q состояние=%q", in.Launch.Directory, result.Status)
	return result, nil
}

func failedStartResult(err error) LaunchResult {
	return LaunchResult{Status: "failed", Summary: strings.TrimSpace(err.Error())}
}

func (s *Service) beginStartHistory(ctx context.Context, root string, in Invocation) history.Handle {
	if root == "" {
		return history.Handle{}
	}

	handle, err := history.Begin(ctx, root, history.Run{
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		Status:             "running",
		Summary:            "",
		Name:               in.Workplace.Name,
		ProfileName:        fallbackExecutionHistoryValue(strings.TrimSpace(in.Profile)),
		Runner:             fallbackExecutionHistoryValue(in.Launch.Runner),
		Model:              fallbackExecutionHistoryValue(in.Launch.Model),
		LaunchDirectory:    fallbackLaunchDirectory(in, root),
		RawStructuredInput: history.StructuredInputJSON(in.Launch.StructuredInput),
	})
	if err != nil {
		return history.Handle{}
	}
	return handle
}

func (s *Service) updateStartHistory(ctx context.Context, root string, handle history.Handle, in Invocation, profile Profile, allocation Allocation, workplace Workplace, result LaunchResult, launchErr error) {
	if root == "" {
		return
	}
	errorText := ""
	if launchErr != nil {
		errorText = strings.TrimSpace(launchErr.Error())
	}

	runner := firstNonEmptyTrimmed(in.Launch.Runner, allocation.Runner)
	modelName := firstNonEmptyTrimmed(in.Launch.Model, allocation.Model)
	profileName := profile.Name
	if strings.TrimSpace(profileName) == "" {
		profileName = strings.TrimSpace(in.Profile)
	}

	_ = history.Update(ctx, handle, history.Run{
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		Status:              result.Status,
		Summary:             result.Summary,
		Name:                in.Workplace.Name,
		ProfileName:         fallbackExecutionHistoryValue(profileName),
		Runner:              fallbackExecutionHistoryValue(runner),
		Model:               fallbackExecutionHistoryValue(modelName),
		LaunchDirectory:     fallbackLaunchDirectory(in, root),
		RawStructuredInput:  history.StructuredInputJSON(in.Launch.StructuredInput),
		RawOutputPath:       result.RawOutputPath,
		RawStructuredOutput: history.StructuredOutputJSON(result.StructuredOutput, ""),
		RunRecordPath:       result.RunRecordPath,
		Error:               errorText,
	})
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func executionHistoryRoot(in Invocation, workplace Workplace) string {
	if strings.TrimSpace(workplace.Name) != "" {
		return workplace.Name
	}
	if strings.TrimSpace(in.Launch.Directory) != "" {
		return in.Launch.Directory
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func fallbackLaunchDirectory(in Invocation, root string) string {
	if strings.TrimSpace(in.Launch.Directory) != "" {
		return in.Launch.Directory
	}
	return root
}

func fallbackExecutionHistoryValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
