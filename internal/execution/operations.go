package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/integration"
)

type operationExecution struct {
	in            invocation
	assignment    *ExecutionAssignment
	action        Action
	data          map[string]any
	profile       profile
	allocation    allocation
	workplace     workplace
	result        LaunchResult
	pullRequest   *integration.MergeRequest
	reviewRemarks []integration.ReviewRemark
	historyRoot   string
	historyHandle history.Handle
	tracker       *operationTracker
}

type builtinOperationExecutor struct {
	service *Service
}

type commitPusher interface {
	CommitAndPush(context.Context, model.Invocation, model.Allocation, model.Workplace, *model.StructuredOutput) (string, error)
}

func (s *Service) runActionOperations(ctx context.Context, state *operationExecution) error {
	executor := builtinOperationExecutor{service: s}
	for _, operation := range state.action.Operations {
		if err := executor.Execute(ctx, state, operation); err != nil {
			return err
		}
	}

	if strings.TrimSpace(state.result.Status) == "" {
		state.result = LaunchResult{
			Status:  "completed",
			Summary: fmt.Sprintf("action=%s class=%s operations=completed", state.action.Name, state.action.Class),
		}
		s.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
	}

	return nil
}

func (e builtinOperationExecutor) Execute(ctx context.Context, state *operationExecution, operation OperationSpec) error {
	name := operationResultName(operation)
	switch operationKind(operation) {
	case OperationKindPrepareData:
		return e.prepareData(ctx, state, name)
	case OperationKindLoadPullRequest:
		return e.loadPullRequest(ctx, state, name)
	case OperationKindLoadReviewRemarks:
		return e.loadReviewRemarks(ctx, state, name, operation.Required)
	case OperationKindResolveProfile:
		return e.resolveProfile(ctx, state, operation, name)
	case OperationKindAllocateResources:
		return e.allocateResources(ctx, state, name)
	case OperationKindPrepareWorkplace:
		return e.prepareWorkplace(ctx, state, name)
	case OperationKindBuildDirective:
		return e.buildDirective(ctx, state, name)
	case OperationKindLaunchSynthesis:
		return e.launchSynthesis(ctx, state, name)
	case OperationKindParseResult:
		return e.parseResult(state, name)
	case OperationKindCommitPush:
		return e.commitPush(ctx, state, name)
	case OperationKindPublishMergeRequest:
		return e.publishMergeRequest(ctx, state, name)
	case OperationKindPublishReviewRemarks:
		return e.publishReviewRemarks(ctx, state, name)
	case OperationKindPublishReviewResponses:
		return e.publishReviewResponses(ctx, state, name)
	case OperationKindFinalize:
		return e.finalize(ctx, state, name)
	default:
		return e.unsupported(ctx, state, operation, name)
	}
}

func (e builtinOperationExecutor) prepareData(ctx context.Context, state *operationExecution, name string) error {
	if err := syncPullRequestRefsWithWorkplace(state); err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Данные задания не согласованы с веткой рабочего места.", err, "pull_request_branch_mismatch", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, model.Profile{}, model.Allocation{}, model.Workplace{}, state.result, err)
		return err
	}

	state.tracker.completeIO(name, assignmentSummary(state.assignment), structuredInputSummary(state.assignment.StructuredInput), "Данные задания подготовлены для выполнения.")
	return nil
}

func syncPullRequestRefsWithWorkplace(state *operationExecution) error {
	if state == nil {
		return nil
	}

	ref := pullRequestRefFromAssignment(state.assignment)
	if base := strings.TrimSpace(ref.Base); base != "" && strings.TrimSpace(state.in.Workplace.BaseRef) == "" {
		state.in.Workplace.BaseRef = base
	}
	explicitHead := explicitPullRequestHeadFromAssignment(state.assignment)
	if explicitHead == "" {
		return nil
	}
	if strings.TrimSpace(state.in.Workplace.HeadRef) == "" {
		state.in.Workplace.HeadRef = explicitHead
	}
	if state.action.Name != ActionStartImplementationPR {
		return nil
	}

	workplaceName := strings.TrimSpace(state.in.Workplace.Name)
	if workplaceName != "" && explicitHead != workplaceName {
		return fmt.Errorf("head branch %q does not match workplace branch %q for %s", explicitHead, workplaceName, ActionStartImplementationPR)
	}
	return nil
}

func explicitPullRequestHeadFromAssignment(assignment *ExecutionAssignment) string {
	if assignment == nil {
		return ""
	}
	for _, object := range assignment.RelatedObjects {
		if !isPullRequestObject(object.Type) || object.Attributes == nil {
			continue
		}
		if head := firstNonEmptyTrimmed(object.Attributes["head_ref"], object.Attributes["head"]); head != "" {
			return head
		}
	}
	return ""
}

func (e builtinOperationExecutor) resolveProfile(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	profileName := resolveProfileInputName(state, operation)
	profileInput := state.in
	profileInput.Profile = profileName
	profile, err := e.service.resolveProfile(ctx, profileInput)
	if err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Исполнительный профиль не определён.", err, "profile_not_found", false, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, model.Profile{}, model.Allocation{}, model.Workplace{}, state.result, err)
		return err
	}

	writeOperationData(state, operation.Out, "profile", profile)
	state.profile = profile
	state.tracker.completeIO(name, resolveProfileInputSummary(profileName, operation), resolveProfileOutputSummary(profile, operation), fmt.Sprintf("profile=%s mode=%s", profile.Name, profile.Mode))
	return nil
}

func resolveProfileInputName(state *operationExecution, operation OperationSpec) string {
	if mapping, ok := operation.In["profile_name"]; ok {
		if value := stringValueFromOperationMapping(mapping); value != "" {
			return value
		}
		switch strings.TrimSpace(mapping.Ref) {
		case "action.profile":
			if state != nil && strings.TrimSpace(state.action.Profile) != "" {
				return strings.TrimSpace(state.action.Profile)
			}
		case "in.profile", "invocation.profile":
			if state != nil && strings.TrimSpace(state.in.Profile) != "" {
				return strings.TrimSpace(state.in.Profile)
			}
		}
	}
	if state != nil && strings.TrimSpace(state.in.Profile) != "" {
		return strings.TrimSpace(state.in.Profile)
	}
	return "default"
}

func stringValueFromOperationMapping(mapping model.OperationMapping) string {
	if len(mapping.Value) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(mapping.Value, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func writeOperationData(state *operationExecution, mappings model.OperationMap, field string, value any) {
	if state == nil {
		return
	}
	mapping, ok := mappings[field]
	if !ok {
		return
	}
	ref := strings.TrimSpace(mapping.Ref)
	if !strings.HasPrefix(ref, "data.") {
		return
	}
	name := strings.TrimSpace(strings.TrimPrefix(ref, "data."))
	if name == "" {
		return
	}
	if state.data == nil {
		state.data = map[string]any{}
	}
	state.data[name] = value
}

func resolveProfileInputSummary(profileName string, operation OperationSpec) string {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = "default"
	}
	return operationIOSummary(operation.In, map[string]string{
		"profile_name": profileName,
	})
}

func resolveProfileOutputSummary(profile profile, operation OperationSpec) string {
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		profileJSON = []byte(fmt.Sprintf(`{"name":%q}`, profile.Name))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"profile": string(profileJSON),
	})
}

func operationIOSummary(mappings model.OperationMap, values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := values[name]
		ref := ""
		if mapping, ok := mappings[name]; ok {
			ref = strings.TrimSpace(mapping.Ref)
		}
		if ref != "" {
			parts = append(parts, fmt.Sprintf("%s[%s]=%s", name, ref, value))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", name, value))
	}
	return strings.Join(parts, " ")
}

func (e builtinOperationExecutor) allocateResources(ctx context.Context, state *operationExecution, name string) error {
	if !state.action.RequiresSynthesis {
		state.allocation = allocation{Resource: "not-required", Source: "action-without-synthesis"}
		state.tracker.skip(name, "Ресурсное снабжение не требуется для действия без синтеза.")
		return nil
	}

	allocation, err := e.service.allocateResources(ctx, state.in, state.profile)
	if err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Ресурсы недоступны.", err, "resources_unavailable", true, false)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, model.Allocation{}, model.Workplace{}, state.result, err)
		return err
	}

	state.allocation = allocation
	state.tracker.complete(name, fmt.Sprintf("resource=%s runner=%s model=%s", allocation.Resource, allocation.Runner, allocation.Model))
	return nil
}

func (e builtinOperationExecutor) prepareWorkplace(ctx context.Context, state *operationExecution, name string) error {
	if !state.action.RequiresWorkplace {
		state.workplace = workplace{Name: strings.TrimSpace(state.in.Launch.Directory), Ready: true}
		state.tracker.skip(name, "Рабочее место не требуется для разрешённого действия.")
		return nil
	}

	workplace, err := e.service.prepareWorkplace(ctx, state.in, state.profile, state.allocation)
	if err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Исполнительное рабочее место не подготовлено.", err, "workplace_not_prepared", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, model.Workplace{}, state.result, err)
		return err
	}

	state.workplace = workplace
	if strings.TrimSpace(state.in.Launch.Directory) == "" {
		state.in.Launch.Directory = workplace.Name
	}
	state.tracker.complete(name, fmt.Sprintf("workplace=%s ready=%t", workplace.Name, workplace.Ready))
	return nil
}

func (e builtinOperationExecutor) buildDirective(ctx context.Context, state *operationExecution, name string) error {
	if !state.action.RequiresSynthesis {
		state.tracker.skip(name, "Исполнительная директива не требуется для действия без синтеза.")
		return nil
	}

	state.in.Launch.Runner = state.allocation.Runner
	state.in.Launch.Model = state.allocation.Model
	if strings.TrimSpace(state.in.Launch.ModelBinding) == "" {
		state.in.Launch.ModelBinding = state.allocation.ModelBinding
	}
	state.tracker.completeIO(name, structuredInputSummary(state.in.Launch.StructuredInput), fmt.Sprintf("runner=%s model=%s", state.in.Launch.Runner, state.in.Launch.Model), "Исполнительная директива подготовлена к запуску.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, LaunchResult{Status: "running"}, nil)
	return nil
}

func (e builtinOperationExecutor) launchSynthesis(ctx context.Context, state *operationExecution, name string) error {
	if !state.action.RequiresSynthesis {
		state.tracker.skip(name, "Запуск синтеза не требуется для разрешённого действия.")
		return nil
	}

	launchCtx := launch.WithHistoryHandle(ctx, state.historyHandle)
	launchInvocation := state.in
	launchInvocation.Launch.CommitPush = false
	launchProfile := state.profile
	result, err := e.service.launch(launchCtx, launchInvocation, launchProfile, state.allocation, state.workplace)
	state.result = result
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, result, err)
	if err != nil {
		if result.StructuredOutput != nil {
			state.tracker.complete(name, fmt.Sprintf("status=%s", result.Status))
			if parseResultName, ok := actionOperationNameByKind(state.action, OperationKindParseResult); ok {
				state.tracker.completeIO(parseResultName, resultSummary(result), structuredOutputSummary(result.StructuredOutput), "Результат синтеза получен и нормализован.")
			}
			if finalizeName, ok := actionOperationNameByKind(state.action, OperationKindFinalize); ok {
				state.tracker.fail(finalizeName, "Завершающая операция после синтеза не выполнена.", err, "final_operation_failed", true, true)
			} else {
				state.tracker.fail(name, "Запуск синтеза завершился отказом после получения результата.", err, "synthesis_failed", true, true)
			}
			return err
		}

		state.tracker.fail(name, "Запуск синтеза завершился отказом.", err, "synthesis_failed", true, true)
		if parseResultName, ok := actionOperationNameByKind(state.action, OperationKindParseResult); ok {
			state.tracker.fail(parseResultName, "Результат выполнения не приведён к нормализованной форме.", err, "result_not_parsed", false, true)
		}
		return err
	}

	state.tracker.complete(name, fmt.Sprintf("status=%s", result.Status))
	return nil
}

func (e builtinOperationExecutor) parseResult(state *operationExecution, name string) error {
	if !state.action.RequiresSynthesis {
		state.tracker.skip(name, "Разбор результата синтеза не требуется.")
		return nil
	}

	state.tracker.completeIO(name, resultSummary(state.result), structuredOutputSummary(state.result.StructuredOutput), "Результат выполнения нормализован.")
	return nil
}

func (e builtinOperationExecutor) commitPush(ctx context.Context, state *operationExecution, name string) error {
	if !state.action.RequiresSynthesis {
		state.tracker.skip(name, "Создание коммита не требуется для действия без синтеза.")
		return nil
	}

	pusher, ok := e.service.launcher.(commitPusher)
	if !ok {
		err := fmt.Errorf("commit-push operation is unsupported by launcher")
		state.result.Status = "failed"
		if strings.TrimSpace(state.result.Summary) == "" {
			state.result.Summary = err.Error()
		}
		state.tracker.fail(name, "Операция commit-push не поддержана модулем запуска.", err, "commit_push_unsupported", false, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, err)
		return err
	}

	summary, err := pusher.CommitAndPush(ctx, state.in, state.allocation, state.workplace, state.result.StructuredOutput)
	if err != nil {
		state.result.Status = "failed"
		if strings.TrimSpace(state.result.Summary) == "" {
			state.result.Summary = strings.TrimSpace(err.Error())
		}
		state.tracker.fail(name, "Создание коммита или отправка ветки не выполнены.", err, "commit_push_failed", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, err)
		return err
	}

	state.result.Summary = joinExecutionSummaries(state.result.Summary, summary)
	state.tracker.complete(name, summary)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
	return nil
}

func (e builtinOperationExecutor) finalize(ctx context.Context, state *operationExecution, name string) error {
	if !state.action.RequiresSynthesis {
		state.result = LaunchResult{
			Status:  "completed",
			Summary: fmt.Sprintf("action=%s class=%s synthesis=not-required", state.action.Name, state.action.Class),
		}
		state.tracker.complete(name, finalizeSummary(state.result))
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, nil)
		return nil
	}

	state.tracker.complete(name, finalizeSummary(state.result))
	return nil
}

func actionOperationNameByKind(action Action, kind string) (string, bool) {
	for _, operation := range action.Operations {
		if operationKind(operation) != model.OperationKind(kind) {
			continue
		}
		return operationResultName(operation), true
	}
	return "", false
}

func joinExecutionSummaries(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

func (e builtinOperationExecutor) unsupported(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	if !operation.Required {
		state.tracker.skip(name, "Операция не поддержана текущей реализацией и не является обязательной.")
		return nil
	}

	err := fmt.Errorf("operation %q is unsupported", name)
	state.result = failedStartResult(err)
	state.tracker.fail(name, "Операция не поддержана текущей реализацией.", err, "operation_unsupported", false, true)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, state.profile, state.allocation, state.workplace, state.result, err)
	return err
}

func operationKind(operation OperationSpec) OperationKind {
	if operation.Kind != "" {
		return operation.Kind
	}
	return model.OperationKind(operationResultName(operation))
}

func operationResultName(operation OperationSpec) string {
	if strings.TrimSpace(operation.Name) != "" {
		return strings.TrimSpace(operation.Name)
	}
	return strings.TrimSpace(string(operation.Kind))
}
