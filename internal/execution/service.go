package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

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
type AssignmentReason = model.AssignmentReason
type ObjectRef = model.ObjectRef
type ExecutionAssignment = model.ExecutionAssignment
type Action = model.Action
type ActionClass = model.ActionClass
type OperationKind = model.OperationKind
type OperationSpec = model.OperationSpec
type OperationStatus = model.OperationStatus
type OperationResult = model.OperationResult
type Artifact = model.Artifact
type DiagnosticLink = model.DiagnosticLink
type Failure = model.Failure
type LaunchResult = model.LaunchResult
type ExecutionResult = model.ExecutionResult
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

type ResumeRequest struct {
	Run                      string
	Name                     string
	Message                  string
	MessageSource            string
	Profile                  string
	StructuredOutput         bool
	StructuredOutputRequired bool
	DryRun                   bool
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
	actions    ActionResolver
	profiles   ProfileResolver
	resources  ResourceProvider
	workplaces WorkplaceManager
	launcher   Launcher
}

func NewService(logger *log.Logger) *Service {
	actions := NewActionCatalog()
	profiles := profile.NewService()
	resources := resources.NewService()
	workplaces := workplace.NewService()
	launcher := launch.NewService()

	return &Service{
		logger:     logger,
		actions:    actions,
		profiles:   profiles,
		resources:  resources,
		workplaces: workplaces,
		launcher:   launcher,
	}
}

func (s *Service) Start(ctx context.Context, in Invocation) (LaunchResult, error) {
	result, err := s.Execute(ctx, in)
	if result.Launch == nil {
		return LaunchResult{}, err
	}

	return *result.Launch, err
}

func (s *Service) Execute(ctx context.Context, in Invocation) (ExecutionResult, error) {
	assignment := assignmentFromInvocation(in)
	in.Assignment = assignment
	if in.Launch.StructuredInput == nil && assignment.StructuredInput != nil {
		in.Launch.StructuredInput = assignment.StructuredInput
	}
	if strings.TrimSpace(in.Action) == "" {
		in.Action = assignment.Action
	}
	if strings.TrimSpace(in.Profile) == "" {
		in.Profile = assignment.Profile
	}
	historyRoot := executionHistoryRoot(in, Workplace{})
	historyHandle := s.beginStartHistory(ctx, historyRoot, in)

	action, err := s.ResolveAction(ctx, in)
	if err != nil {
		result := failedStartResult(err)
		operations := actionResolutionFailureOperations(err)
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, Profile{}, Allocation{}, Workplace{}, result, err)
		return executionResultFromLaunch(assignment, Action{Name: actionNameFromInvocation(in)}, operations, result, err), err
	}
	if strings.TrimSpace(in.Profile) == "" && strings.TrimSpace(action.Profile) != "" {
		in.Profile = strings.TrimSpace(action.Profile)
		assignment.Profile = in.Profile
	}

	s.logger.Printf("Контур исполнения принят к пуску: задача=%q", in.Task)

	state := &operationExecution{
		in:            in,
		assignment:    assignment,
		action:        action,
		historyRoot:   historyRoot,
		historyHandle: historyHandle,
		tracker:       newOperationTracker(action),
	}
	err = s.runActionOperations(ctx, state)
	return executionResultFromLaunch(assignment, action, state.tracker.snapshot(), state.result, err), err
}

func (s *Service) Dispatch(ctx context.Context, in Invocation) []string {
	action, err := s.ResolveAction(ctx, in)
	if err != nil {
		return []string{OperationKindResolveAction}
	}
	stages := make([]string, 0, len(action.Operations))
	for _, operation := range action.Operations {
		stages = append(stages, operation.Name)
	}
	s.logger.Printf("Диспетчер сформировал действие: задача=%q действие=%q операции=%s", in.Task, action.Name, strings.Join(stages, " -> "))
	return stages
}

func (s *Service) Resume(ctx context.Context, req ResumeRequest) (LaunchResult, error) {
	invocation, parent, profile, allocation, workplace, historyRoot, err := s.prepareResume(ctx, req)
	if err != nil {
		return LaunchResult{}, err
	}

	if req.DryRun {
		return LaunchResult{Status: "dry-run", Summary: formatResumeDryRunSummary(invocation, parent, profile)}, nil
	}

	historyHandle := s.beginResumeHistory(ctx, historyRoot, invocation, parent)
	s.updateResumeHistory(ctx, historyRoot, historyHandle, invocation, profile, allocation, workplace, LaunchResult{Status: "running"}, nil)

	launchCtx := launch.WithHistoryHandle(ctx, historyHandle)
	result, err := s.Launch(launchCtx, invocation, profile, allocation, workplace)
	s.updateResumeHistory(ctx, historyRoot, historyHandle, invocation, profile, allocation, workplace, result, err)
	return result, err
}

func (s *Service) LaunchDirect(ctx context.Context, in Invocation) (LaunchResult, error) {
	profile := Profile{Name: "direct-launch", Mode: "manual"}
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

func (s *Service) prepareResume(ctx context.Context, req ResumeRequest) (Invocation, history.ListedRun, Profile, Allocation, Workplace, string, error) {
	parent, historyRoot, err := resolveResumeParentRun(ctx, req)
	if err != nil {
		return Invocation{}, history.ListedRun{}, Profile{}, Allocation{}, Workplace{}, "", err
	}

	profileName := strings.TrimSpace(req.Profile)
	if profileName == "" || profileName == "unknown" {
		profileName = strings.TrimSpace(parent.ProfileName)
	}
	if profileName == "" || profileName == "unknown" {
		profileName = "default"
	}

	profileInput := Invocation{
		Profile: profileName,
		Launch: LaunchSpec{
			StructuredOutput:         req.StructuredOutput,
			StructuredOutputRequired: req.StructuredOutputRequired,
		},
	}
	profile, err := s.ResolveProfile(ctx, profileInput)
	if err != nil {
		return Invocation{}, history.ListedRun{}, Profile{}, Allocation{}, Workplace{}, "", err
	}

	structuredInput, err := buildResumeStructuredInput(parent, req)
	if err != nil {
		return Invocation{}, history.ListedRun{}, Profile{}, Allocation{}, Workplace{}, "", err
	}

	invocation := Invocation{
		Profile:    profileName,
		Repository: RepositorySpec{},
		Workplace:  WorkplaceSpec{Name: parent.Name},
		Launch: LaunchSpec{
			Directory:                parent.LaunchDirectory,
			Runner:                   parent.Runner,
			Model:                    parent.Model,
			Resume:                   &model.ResumeSpec{ParentRunID: parent.ID, RunnerSessionID: parent.RunnerSessionID, MessageSource: req.MessageSource},
			StructuredInput:          structuredInput,
			StructuredOutput:         req.StructuredOutput,
			StructuredOutputRequired: req.StructuredOutputRequired,
		},
	}

	allocation := Allocation{Resource: "resume-parent-run", Reserved: true, Runner: parent.Runner, Model: parent.Model, Source: "resume-parent-run"}
	workplace := Workplace{Name: parent.LaunchDirectory, Ready: true}
	return invocation, parent, profile, allocation, workplace, historyRoot, nil
}

func (s *Service) ResolveAction(ctx context.Context, in Invocation) (Action, error) {
	resolver := s.actions
	if resolver == nil {
		resolver = NewActionCatalog()
	}

	action, err := resolver.ResolveAction(ctx, in)
	if err != nil {
		s.logger.Printf("Действие не разрешено: задача=%q ошибка=%v", in.Task, err)
		return Action{}, err
	}

	s.logger.Printf("Действие разрешено: задача=%q действие=%q класс=%q рабочее-место=%t синтез=%t", in.Task, action.Name, action.Class, action.RequiresWorkplace, action.RequiresSynthesis)
	return action, nil
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

	s.logger.Printf("Ресурсы подтверждены: задача=%q ресурс=%q резерв=%t runner=%q model=%q binding=%q source=%q binding-source=%q fallback=%t global-config=%q local-config=%q", in.Task, allocation.Resource, allocation.Reserved, allocation.Runner, allocation.Model, allocation.ModelBinding, allocation.Source, allocation.BindingSource, allocation.FallbackUsed, allocation.GlobalConfigPath, allocation.LocalConfigPath)
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

func buildResumeStructuredInput(parent history.ListedRun, req ResumeRequest) (*StructuredInput, error) {
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return nil, fmt.Errorf("resume message must be non-empty")
	}

	resumeExtension, err := json.Marshal(struct {
		ParentRunID        int64  `json:"parent_run_id,omitempty"`
		ParentRunner       string `json:"parent_runner,omitempty"`
		ParentRunnerSessID string `json:"parent_runner_session_id,omitempty"`
		MessageSource      string `json:"message_source,omitempty"`
	}{
		ParentRunID:        parent.ID,
		ParentRunner:       parent.Runner,
		ParentRunnerSessID: parent.RunnerSessionID,
		MessageSource:      strings.TrimSpace(req.MessageSource),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal resume extension: %w", err)
	}

	bodyParts := make([]string, 0, 2)
	if summary := singleLineSummary(parent.Summary); summary != "" {
		bodyParts = append(bodyParts, "summary="+summary)
	}
	if path := strings.TrimSpace(parent.RunRecordPath); path != "" {
		bodyParts = append(bodyParts, "run_record_path="+path)
	}

	input := StructuredInput{
		OperationalContext: []StructuredContext{{
			Title: "Дополнительное сообщение для возобновления",
			Body:  message,
		}},
		PreviousRunResults: []StructuredResult{{
			Summary: fmt.Sprintf("parent run #%d %s", parent.ID, parent.Status),
			Body:    strings.Join(bodyParts, "\n"),
		}},
		Extensions: StructuredExtensions{"resume": resumeExtension},
	}

	normalized, err := launch.NormalizeStructuredInput(&input)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func resolveResumeParentRun(ctx context.Context, req ResumeRequest) (history.ListedRun, string, error) {
	runRef := strings.TrimSpace(req.Run)
	if runRef == "" {
		return history.ListedRun{}, "", fmt.Errorf("resume run reference must be provided")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return history.ListedRun{}, "", err
	}

	if runRef == "latest" {
		runs, err := history.List(ctx, cwd, history.ListFilter{Limit: 1, Name: strings.TrimSpace(req.Name)})
		if err != nil {
			return history.ListedRun{}, "", err
		}
		if len(runs) == 0 {
			return history.ListedRun{}, "", fmt.Errorf("resume parent run not found")
		}
		return runs[0], cwd, nil
	}

	runID, err := strconv.ParseInt(runRef, 10, 64)
	if err != nil || runID <= 0 {
		return history.ListedRun{}, "", fmt.Errorf("resume run reference must be numeric id or latest")
	}

	parent, err := history.Get(ctx, cwd, runID)
	if err != nil {
		return history.ListedRun{}, "", err
	}
	return parent, cwd, nil
}

func formatResumeDryRunSummary(in Invocation, parent history.ListedRun, profile Profile) string {
	payload, err := json.MarshalIndent(in.Launch.StructuredInput, "", "  ")
	if err != nil {
		payload = []byte("{}")
	}

	return strings.Join([]string{
		fmt.Sprintf("resume plan parent-run=%d profile=%s runner=%s model=%s", parent.ID, profile.Name, in.Launch.Runner, in.Launch.Model),
		fmt.Sprintf("parent-session-id=%s", fallbackExecutionHistoryValue(parent.RunnerSessionID)),
		"structured-input:",
		string(payload),
	}, "\n")
}

func singleLineSummary(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (s *Service) beginResumeHistory(ctx context.Context, root string, in Invocation, parent history.ListedRun) history.Handle {
	if root == "" {
		return history.Handle{}
	}

	handle, err := history.Begin(ctx, root, history.Run{
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		Status:              "running",
		Summary:             "",
		Name:                in.Workplace.Name,
		ProfileName:         fallbackExecutionHistoryValue(strings.TrimSpace(in.Profile)),
		Runner:              fallbackExecutionHistoryValue(in.Launch.Runner),
		RunnerSessionID:     parent.RunnerSessionID,
		ParentRunID:         parent.ID,
		ResumeMessage:       firstResumeMessage(in),
		ResumeMessageSource: firstNonEmptyTrimmed(resumeMessageSource(in), parent.ResumeMessageSource),
		Model:               fallbackExecutionHistoryValue(in.Launch.Model),
		LaunchDirectory:     fallbackLaunchDirectory(in, root),
		RawStructuredInput:  history.StructuredInputJSON(in.Launch.StructuredInput),
	})
	if err != nil {
		return history.Handle{}
	}
	return handle
}

func (s *Service) updateResumeHistory(ctx context.Context, root string, handle history.Handle, in Invocation, profile Profile, allocation Allocation, workplace Workplace, result LaunchResult, launchErr error) {
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
		RunnerSessionID:     firstNonEmptyTrimmed(result.RunnerSessionID, parentSessionID(in)),
		ParentRunID:         parentRunID(in),
		ResumeMessage:       firstResumeMessage(in),
		ResumeMessageSource: resumeMessageSource(in),
		Model:               fallbackExecutionHistoryValue(modelName),
		LaunchDirectory:     fallbackLaunchDirectory(in, root),
		RawStructuredInput:  history.StructuredInputJSON(in.Launch.StructuredInput),
		RawOutputPath:       result.RawOutputPath,
		RawStructuredOutput: history.StructuredOutputJSON(result.StructuredOutput, result.RawStructuredOutput),
		RunRecordPath:       result.RunRecordPath,
		Error:               errorText,
	})
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
		RawStructuredOutput: history.StructuredOutputJSON(result.StructuredOutput, result.RawStructuredOutput),
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

func firstResumeMessage(in Invocation) string {
	if in.Launch.StructuredInput == nil {
		return ""
	}
	for _, item := range in.Launch.StructuredInput.OperationalContext {
		if strings.TrimSpace(item.Title) == "Дополнительное сообщение для возобновления" {
			return strings.TrimSpace(item.Body)
		}
	}
	return ""
}

func parentRunID(in Invocation) int64 {
	if in.Launch.Resume == nil {
		return 0
	}
	return in.Launch.Resume.ParentRunID
}

func parentSessionID(in Invocation) string {
	if in.Launch.Resume == nil {
		return ""
	}
	return strings.TrimSpace(in.Launch.Resume.RunnerSessionID)
}

func resumeMessageSource(in Invocation) string {
	if in.Launch.Resume == nil {
		return ""
	}
	return strings.TrimSpace(in.Launch.Resume.MessageSource)
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
