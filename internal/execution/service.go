package execution

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/execution/model"
	profilepkg "github.com/rasungatullin/progress/internal/execution/profile"
	"github.com/rasungatullin/progress/internal/execution/resources"
	workplacepkg "github.com/rasungatullin/progress/internal/execution/workplace"
	"github.com/rasungatullin/progress/internal/integration"
)

type invocation = model.Invocation
type launchSpec = model.LaunchSpec
type repositorySpec = model.RepositorySpec
type workplaceSpec = model.WorkplaceSpec
type profile = model.Profile
type allocation = model.Allocation
type workplace = model.Workplace
type AssignmentReason = model.AssignmentReason
type ObjectRef = model.ObjectRef
type ExecutionAssignment = model.ExecutionAssignment
type ActionInvocation = model.ActionInvocation
type OperationInvocation = model.OperationInvocation
type Action = model.Action
type ActionClass = model.ActionClass
type OperationKind = model.OperationKind
type OperationSpec = model.OperationSpec
type OperationType = model.OperationType
type OperationStatus = model.OperationStatus
type OperationResult = model.OperationResult
type Artifact = model.Artifact
type DiagnosticLink = model.DiagnosticLink
type Failure = model.Failure
type LaunchResult = model.LaunchResult
type ExecutionResult = model.ExecutionResult
type MergeRequest = model.MergeRequest
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

type profileResolver interface {
	Resolve(context.Context, invocation) (profile, error)
}

type resourceProvider interface {
	Allocate(context.Context, invocation, profile) (allocation, error)
}

type workplaceManager interface {
	Prepare(context.Context, invocation, profile, allocation) (workplace, error)
}

type launcher interface {
	Launch(context.Context, invocation, profile, allocation, workplace) (LaunchResult, error)
}

type integrationExecutor interface {
	Execute(context.Context, integration.Request) (integration.Response, error)
}

type integrationOperationCatalog interface {
	Operations(context.Context, integration.OperationFilter) []integration.OperationDescriptor
}

type integrationOperationDescriptor interface {
	OperationDescriptor(context.Context, string, string) (integration.OperationDescriptor, bool)
}

type Service struct {
	logger       *log.Logger
	actions      actionResolver
	profiles     profileResolver
	resources    resourceProvider
	workplaces   workplaceManager
	launcher     launcher
	integrations integrationExecutor
	runGitOutput func(context.Context, string, ...string) (string, error)
}

func NewService(logger *log.Logger) *Service {
	actions := newMethodologyActionResolver()
	profiles := profilepkg.NewService()
	resources := resources.NewService()
	workplaces := workplacepkg.NewService()
	launcher := launch.NewService()
	integrations := integration.NewConfiguredService(logger)

	return &Service{
		logger:       logger,
		actions:      actions,
		profiles:     profiles,
		resources:    resources,
		workplaces:   workplaces,
		launcher:     launcher,
		integrations: integrations,
		runGitOutput: runGitOutput,
	}
}

func (s *Service) ExecuteAction(ctx context.Context, request ActionInvocation) (ExecutionResult, error) {
	return s.execute(ctx, invocationFromActionInvocation(request))
}

func (s *Service) ExecuteOperation(ctx context.Context, request OperationInvocation) (OperationResult, error) {
	operationName := strings.TrimSpace(request.Operation)
	if operationName == "" {
		return OperationResult{}, fmt.Errorf("operation is required")
	}

	in := invocationFromActionInvocation(ActionInvocation{Assignment: request.Assignment})
	assignment := assignmentFromInvocation(in)
	in.Assignment = assignment
	if in.Launch.StructuredInput == nil && assignment.StructuredInput != nil {
		in.Launch.StructuredInput = assignment.StructuredInput
	}
	if strings.TrimSpace(in.Action) == "" {
		in.Action = assignment.Action
	}

	historyRoot := executionHistoryRoot(in, workplace{})
	historyHandle := s.beginStartHistory(ctx, historyRoot, in)

	action, err := s.resolveAction(ctx, in)
	if err != nil {
		result := failedStartResult(err)
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile{}, allocation{}, workplace{}, result, err)
		return OperationResult{}, err
	}
	if !actionContainsOperation(action, operationName) {
		err := fmt.Errorf("operation %q is not part of action %q", operationName, action.Name)
		result := OperationResult{
			Name:   operationName,
			Type:   model.OperationType(OperationTypeBuiltin),
			Kind:   model.OperationKind(operationName),
			Status: model.OperationStatus(OperationStatusFailed),
			Failure: &model.Failure{
				Code:               "operation_not_in_action",
				Message:            err.Error(),
				Retryable:          false,
				ManualIntervention: true,
			},
		}
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile{}, allocation{}, workplace{}, failedStartResult(err), err)
		return result, err
	}
	if strings.TrimSpace(action.Profile) != "" {
		in.Profile = strings.TrimSpace(action.Profile)
	}
	assignment = assignmentFromInvocation(in)

	state := &operationExecution{
		in:            in,
		assignment:    assignment,
		action:        action,
		historyRoot:   historyRoot,
		historyHandle: historyHandle,
		tracker:       newOperationTracker(action),
		callStack:     []string{action.Name},
	}
	executor := builtinOperationExecutor{service: s}
	for _, operation := range action.Operations {
		err := executor.Execute(ctx, state, operation)
		if operationResultName(operation) == operationName {
			if result := findOperationResult(state.tracker.snapshot(), operationName); result != nil {
				s.finishOperationHistory(ctx, state, *result, err)
				return *result, err
			}
			s.finishOperationHistory(ctx, state, OperationResult{}, err)
			return OperationResult{}, err
		}
		if err != nil {
			if result := firstFailedOperationResult(state.tracker.snapshot()); result != nil {
				s.finishOperationHistory(ctx, state, *result, err)
				return *result, err
			}
			s.finishOperationHistory(ctx, state, OperationResult{}, err)
			return OperationResult{}, err
		}
	}

	err = fmt.Errorf("operation %q did not produce result", operationName)
	result := OperationResult{
		Name:   operationName,
		Type:   model.OperationType(OperationTypeBuiltin),
		Kind:   model.OperationKind(operationName),
		Status: model.OperationStatus(OperationStatusFailed),
		Failure: &model.Failure{
			Code:               "operation_result_missing",
			Message:            err.Error(),
			Retryable:          false,
			ManualIntervention: true,
		},
	}
	s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile{}, allocation{}, workplace{}, failedStartResult(err), err)
	return result, err
}

func (s *Service) execute(ctx context.Context, in invocation) (ExecutionResult, error) {
	assignment := assignmentFromInvocation(in)
	in.Assignment = assignment
	if in.Launch.StructuredInput == nil && assignment.StructuredInput != nil {
		in.Launch.StructuredInput = assignment.StructuredInput
	}
	if strings.TrimSpace(in.Action) == "" {
		in.Action = assignment.Action
	}
	historyRoot := executionHistoryRoot(in, workplace{})
	historyHandle := s.beginStartHistory(ctx, historyRoot, in)

	action, err := s.resolveAction(ctx, in)
	if err != nil {
		result := failedStartResult(err)
		s.updateStartHistory(ctx, historyRoot, historyHandle, in, profile{}, allocation{}, workplace{}, result, err)
		return executionResultFromLaunch(assignment, Action{Name: actionNameFromInvocation(in)}, nil, nil, result, err), err
	}
	if strings.TrimSpace(action.Profile) != "" {
		in.Profile = strings.TrimSpace(action.Profile)
	}
	assignment = assignmentFromInvocation(in)

	s.logger.Printf("Контур исполнения принят к пуску: задача=%q", in.Task)

	state := &operationExecution{
		in:            in,
		assignment:    assignment,
		action:        action,
		historyRoot:   historyRoot,
		historyHandle: historyHandle,
		tracker:       newOperationTracker(action),
		callStack:     []string{action.Name},
	}
	err = s.runActionOperations(ctx, state)
	data := executionDataFromState(state)
	return executionResultFromLaunch(assignment, action, state.tracker.snapshot(), mergeRequestFromExecutionData(state), data.result, err), err
}

func invocationFromActionInvocation(request ActionInvocation) invocation {
	assignment := cloneAssignment(request.Assignment)
	if assignment == nil {
		assignment = &ExecutionAssignment{}
	}
	if strings.TrimSpace(assignment.Action) == "" {
		assignment.Action = defaultActionName()
	}

	in := invocation{
		Task:       taskNameFromAssignment(assignment),
		Action:     strings.TrimSpace(assignment.Action),
		Assignment: assignment,
		Repository: repositorySpec{URL: repositoryFromAssignment(assignment)},
		Workplace:  workplaceSpec{Name: workplaceNameFromAssignment(assignment)},
		Launch: launchSpec{
			StructuredInput: assignment.StructuredInput,
		},
	}
	return in
}

func taskNameFromAssignment(assignment *ExecutionAssignment) string {
	if assignment == nil || assignment.CanonicalTask == nil || assignment.CanonicalTask.Number == 0 {
		return ""
	}
	return fmt.Sprintf("task-%d", assignment.CanonicalTask.Number)
}

func repositoryFromAssignment(assignment *ExecutionAssignment) string {
	if assignment == nil || assignment.CanonicalTask == nil {
		return ""
	}
	return strings.TrimSpace(assignment.CanonicalTask.Repository)
}

func workplaceNameFromAssignment(assignment *ExecutionAssignment) string {
	if assignment != nil && assignment.CanonicalTask != nil && assignment.CanonicalTask.Number > 0 {
		return fmt.Sprintf("%d", assignment.CanonicalTask.Number)
	}
	if assignment != nil {
		if name := stableIdentifier(strings.TrimSpace(assignment.Action)); name != "" {
			return name
		}
	}
	return stableIdentifier(defaultActionName())
}

func stableIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}

func actionContainsOperation(action Action, name string) bool {
	return findOperationSpec(action, name) != nil
}

func findOperationSpec(action Action, name string) *OperationSpec {
	name = strings.TrimSpace(name)
	for _, operation := range action.Operations {
		if operationResultName(operation) != name {
			continue
		}
		copyOfOperation := operation
		return &copyOfOperation
	}
	return nil
}

func findOperationResult(operations []OperationResult, name string) *OperationResult {
	name = strings.TrimSpace(name)
	for _, operation := range operations {
		if strings.TrimSpace(operation.Name) != name {
			continue
		}
		copyOfOperation := operation
		return &copyOfOperation
	}
	return nil
}

func firstFailedOperationResult(operations []OperationResult) *OperationResult {
	for _, operation := range operations {
		if operation.Status != model.OperationStatus(OperationStatusFailed) {
			continue
		}
		copyOfOperation := operation
		return &copyOfOperation
	}
	return nil
}

func (s *Service) resolveAction(ctx context.Context, in invocation) (Action, error) {
	resolver := s.actions
	if resolver == nil {
		resolver = newMethodologyActionResolver()
	}

	action, err := resolver.ResolveAction(ctx, in)
	if err != nil {
		s.logger.Printf("Действие не разрешено: задача=%q ошибка=%v", in.Task, err)
		return Action{}, err
	}

	s.logger.Printf("Действие разрешено: задача=%q действие=%q класс=%q рабочее-место=%t", in.Task, action.Name, action.Class, action.RequiresWorkplace)
	return action, nil
}

func (s *Service) resolveProfile(ctx context.Context, in invocation) (profile, error) {
	profile, err := s.profiles.Resolve(ctx, in)
	if err != nil {
		return model.Profile{}, err
	}

	s.logger.Printf("Профиль определён: задача=%q профиль=%q режим=%q", in.Task, profile.Name, profile.Mode)
	return profile, nil
}

func (s *Service) allocateResources(ctx context.Context, in invocation, profile profile) (allocation, error) {
	allocation, err := s.resources.Allocate(ctx, in, profile)
	if err != nil {
		return model.Allocation{}, err
	}

	s.logger.Printf("Ресурсы подтверждены: задача=%q ресурс=%q резерв=%t runner=%q model=%q binding=%q source=%q binding-source=%q fallback=%t global-config=%q local-config=%q", in.Task, allocation.Resource, allocation.Reserved, allocation.Runner, allocation.Model, allocation.ModelBinding, allocation.Source, allocation.BindingSource, allocation.FallbackUsed, allocation.GlobalConfigPath, allocation.LocalConfigPath)
	return allocation, nil
}

func (s *Service) prepareWorkplace(ctx context.Context, in invocation, profile profile, allocation allocation) (workplace, error) {
	workplace, err := s.workplaces.Prepare(ctx, in, profile, allocation)
	if err != nil {
		return model.Workplace{}, err
	}

	s.logger.Printf("Рабочее место подготовлено: задача=%q среда=%q готовность=%t", in.Task, workplace.Name, workplace.Ready)
	return workplace, nil
}

func (s *Service) launch(ctx context.Context, in invocation, profile profile, allocation allocation, workplace workplace) (LaunchResult, error) {
	in.Launch.Runner = allocation.Runner
	in.Launch.Model = allocation.Model
	in.Launch.ReasoningEffort = model.NormalizeReasoningEffort(in.Launch.ReasoningEffort)
	if strings.TrimSpace(allocation.ReasoningEffort) != "" {
		in.Launch.ReasoningEffort = model.NormalizeReasoningEffort(allocation.ReasoningEffort)
	}
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

func (s *Service) finishOperationHistory(ctx context.Context, state *operationExecution, result OperationResult, operationErr error) {
	if state == nil {
		return
	}

	data := executionDataFromState(state)
	launchResult := data.result
	if strings.TrimSpace(launchResult.Status) == "" {
		launchResult.Status = "completed"
		if operationErr != nil {
			launchResult.Status = "failed"
		}
	}
	if strings.TrimSpace(launchResult.Summary) == "" {
		launchResult.Summary = strings.TrimSpace(result.Summary)
	}
	if strings.TrimSpace(launchResult.Summary) == "" && operationErr != nil {
		launchResult.Summary = strings.TrimSpace(operationErr.Error())
	}

	s.updateStartHistory(ctx, state.historyRoot, state.historyHandle, data.invocation, data.profile, data.allocation, data.workplace, launchResult, operationErr)
}

func (s *Service) recordOperationHistory(ctx context.Context, state *operationExecution, operation OperationSpec, operationErr error) {
	if state == nil {
		return
	}

	data := executionDataFromState(state)
	if data.invocation.Assignment == nil {
		data.invocation = state.in
	}
	result := data.result
	if operationErr != nil {
		result.Status = "failed"
		if strings.TrimSpace(result.Summary) == "" {
			result.Summary = strings.TrimSpace(operationErr.Error())
		}
	} else if strings.TrimSpace(result.Status) == "" {
		result.Status = "running"
		if operationResult := findOperationResult(state.tracker.snapshot(), operationResultName(operation)); operationResult != nil {
			result.Summary = strings.TrimSpace(operationResult.Summary)
		}
	}

	s.updateStartHistory(ctx, state.historyRoot, state.historyHandle, data.invocation, data.profile, data.allocation, data.workplace, result, operationErr)
}

func (s *Service) beginStartHistory(ctx context.Context, root string, in invocation) history.Handle {
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

func (s *Service) updateStartHistory(ctx context.Context, root string, handle history.Handle, in invocation, profile profile, allocation allocation, workplace workplace, result LaunchResult, launchErr error) {
	if root == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
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
	if strings.TrimSpace(profileName) == "" {
		profileName = strings.TrimSpace(in.Launch.ModelBinding)
	}
	if strings.TrimSpace(profileName) == "" {
		profileName = strings.TrimSpace(allocation.ModelBinding)
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

func executionHistoryRoot(in invocation, workplace workplace) string {
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

func fallbackLaunchDirectory(in invocation, root string) string {
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
