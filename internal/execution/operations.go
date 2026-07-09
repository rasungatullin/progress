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
		return e.prepareData(ctx, state, operation, name)
	case OperationKindLoadPullRequest:
		return e.loadPullRequest(ctx, state, name)
	case OperationKindLoadReviewRemarks:
		return e.loadReviewRemarks(ctx, state, name, operation.Required)
	case OperationKindResolveProfile:
		return e.resolveProfile(ctx, state, operation, name)
	case OperationKindAllocateResources:
		return e.allocateResources(ctx, state, operation, name)
	case OperationKindPrepareWorkplace:
		return e.prepareWorkplace(ctx, state, operation, name)
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

func (e builtinOperationExecutor) prepareData(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	preparedAssignment := prepareDataInputFromOperation(state, operation)
	state.assignment = preparedAssignment
	state.in.Assignment = preparedAssignment
	if preparedAssignment != nil {
		state.in.Launch.StructuredInput = preparedAssignment.StructuredInput
	}
	if err := syncPullRequestRefsWithWorkplace(state); err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Данные задания не согласованы с веткой рабочего места.", err, "pull_request_branch_mismatch", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, model.Profile{}, model.Allocation{}, model.Workplace{}, state.result, err)
		return err
	}

	writeOperationData(state, operation.Out, "structured_input", state.in.Launch.StructuredInput)
	writeOperationData(state, operation.Out, "workplace", state.in.Workplace)
	state.tracker.completeIO(name, prepareDataInputSummary(preparedAssignment, operation), prepareDataOutputSummary(state, operation), "Данные задания подготовлены для выполнения.")
	return nil
}

func prepareDataInputFromOperation(state *operationExecution, operation OperationSpec) *ExecutionAssignment {
	if state == nil {
		return &ExecutionAssignment{}
	}
	assignment := cloneAssignment(state.assignment)
	if assignment == nil {
		assignment = &ExecutionAssignment{}
	}
	if len(operation.In) == 0 {
		return assignment
	}

	if mapping, ok := operation.In["expected_result"]; ok {
		assignment.ExpectedResult = stringValueFromPrepareDataMapping(state, mapping)
	}
	if mapping, ok := operation.In["constraints"]; ok {
		if constraints, ok := valueFromPrepareDataMapping[[]string](state, mapping); ok {
			assignment.Constraints = constraints
		}
	}
	if mapping, ok := operation.In["canonical_task"]; ok {
		if canonicalTask, ok := valueFromPrepareDataMapping[*ObjectRef](state, mapping); ok {
			assignment.CanonicalTask = canonicalTask
		} else if canonicalTask, ok := valueFromPrepareDataMapping[ObjectRef](state, mapping); ok {
			assignment.CanonicalTask = &canonicalTask
		}
	}
	if mapping, ok := operation.In["related_objects"]; ok {
		if relatedObjects, ok := valueFromPrepareDataMapping[[]ObjectRef](state, mapping); ok {
			assignment.RelatedObjects = relatedObjects
		}
	}
	if mapping, ok := operation.In["reasons"]; ok {
		if reasons, ok := valueFromPrepareDataMapping[[]AssignmentReason](state, mapping); ok {
			assignment.Reasons = reasons
		}
	}
	if mapping, ok := operation.In["structured_input"]; ok {
		if structuredInput, ok := valueFromPrepareDataMapping[*StructuredInput](state, mapping); ok {
			assignment.StructuredInput = structuredInput
		} else if structuredInput, ok := valueFromPrepareDataMapping[StructuredInput](state, mapping); ok {
			assignment.StructuredInput = &structuredInput
		}
	}
	return assignment
}

func stringValueFromPrepareDataMapping(state *operationExecution, mapping model.OperationMapping) string {
	if value, ok := valueFromPrepareDataMapping[string](state, mapping); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func valueFromPrepareDataMapping[T any](state *operationExecution, mapping model.OperationMapping) (T, bool) {
	var zero T
	if len(mapping.Value) != 0 {
		var value T
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return zero, false
	}
	raw, ok := prepareDataRefValue(state, mapping.Ref)
	if !ok {
		return zero, false
	}
	value, ok := raw.(T)
	if ok {
		return value, true
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return zero, false
	}
	var decoded T
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return zero, false
	}
	return decoded, true
}

func prepareDataRefValue(state *operationExecution, ref string) (any, bool) {
	if state == nil || state.assignment == nil {
		return nil, false
	}
	ref = strings.TrimSpace(ref)
	switch ref {
	case "in.expected_result", "assignment.expected_result":
		return state.assignment.ExpectedResult, true
	case "in.constraints", "assignment.constraints":
		return state.assignment.Constraints, true
	case "in.canonical_task", "assignment.canonical_task":
		return state.assignment.CanonicalTask, true
	case "in.related_objects", "assignment.related_objects":
		return state.assignment.RelatedObjects, true
	case "in.reasons", "assignment.reasons":
		return state.assignment.Reasons, true
	case "in.structured_input", "assignment.structured_input":
		return state.assignment.StructuredInput, true
	default:
		return nil, false
	}
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

func prepareDataInputSummary(assignment *ExecutionAssignment, operation OperationSpec) string {
	if assignment == nil {
		return ""
	}
	return operationIOSummary(operation.In, map[string]string{
		"canonical_task":   objectPresenceSummary(assignment.CanonicalTask),
		"constraints":      formatInt(len(assignment.Constraints)),
		"expected_result":  presenceSummary(assignment.ExpectedResult),
		"reasons":          formatInt(len(assignment.Reasons)),
		"related_objects":  formatInt(len(assignment.RelatedObjects)),
		"structured_input": structuredInputSummary(assignment.StructuredInput),
	})
}

func prepareDataOutputSummary(state *operationExecution, operation OperationSpec) string {
	if state == nil {
		return ""
	}
	return operationIOSummary(operation.Out, map[string]string{
		"structured_input": structuredInputSummary(state.in.Launch.StructuredInput),
		"workplace":        workplaceSpecSummary(state.in.Workplace),
	})
}

func objectPresenceSummary(object *ObjectRef) string {
	if object == nil {
		return "absent"
	}
	parts := []string{"present"}
	if strings.TrimSpace(object.Type) != "" {
		parts = append(parts, "type="+strings.TrimSpace(object.Type))
	}
	if object.Number != 0 {
		parts = append(parts, "number="+formatInt(object.Number))
	}
	if repository := strings.TrimSpace(object.Repository); repository != "" {
		parts = append(parts, "repository="+repository)
	}
	return strings.Join(parts, ",")
}

func presenceSummary(value string) string {
	if strings.TrimSpace(value) == "" {
		return "absent"
	}
	return "present"
}

func workplaceSpecSummary(workplace workplaceSpec) string {
	parts := []string{}
	if name := strings.TrimSpace(workplace.Name); name != "" {
		parts = append(parts, "name="+name)
	}
	if baseRef := strings.TrimSpace(workplace.BaseRef); baseRef != "" {
		parts = append(parts, "base="+baseRef)
	}
	if headRef := strings.TrimSpace(workplace.HeadRef); headRef != "" {
		parts = append(parts, "head="+headRef)
	}
	if environment := strings.TrimSpace(workplace.Environment); environment != "" {
		parts = append(parts, "environment="+environment)
	}
	if len(parts) == 0 {
		return "absent"
	}
	return strings.Join(parts, ",")
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

func (e builtinOperationExecutor) allocateResources(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := allocateResourcesInputFromOperation(state, operation)
	if !input.requiresSynthesis {
		state.allocation = allocation{Resource: "not-required", Source: "action-without-synthesis"}
		writeOperationData(state, operation.Out, "allocation", state.allocation)
		state.tracker.skipIO(name, allocateResourcesInputSummary(input, operation), allocateResourcesOutputSummary(state.allocation, operation), "Ресурсное снабжение не требуется для действия без синтеза.")
		return nil
	}

	allocation, err := e.service.allocateResources(ctx, input.invocation, input.profile)
	if err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Ресурсы недоступны.", err, "resources_unavailable", true, false)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, model.Allocation{}, model.Workplace{}, state.result, err)
		return err
	}

	state.allocation = allocation
	writeOperationData(state, operation.Out, "allocation", allocation)
	state.tracker.completeIO(name, allocateResourcesInputSummary(input, operation), allocateResourcesOutputSummary(allocation, operation), fmt.Sprintf("resource=%s runner=%s model=%s", allocation.Resource, allocation.Runner, allocation.Model))
	return nil
}

type allocateResourcesInput struct {
	requiresSynthesis bool
	invocation        invocation
	profile           profile
}

func allocateResourcesInputFromOperation(state *operationExecution, operation OperationSpec) allocateResourcesInput {
	input := allocateResourcesInput{}
	if state != nil {
		input.requiresSynthesis = state.action.RequiresSynthesis
		input.invocation = state.in
		input.profile = state.profile
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_synthesis"]; ok {
		if value, ok := boolValueFromAllocateResourcesMapping(state, mapping); ok {
			input.requiresSynthesis = value
		}
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromAllocateResourcesMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromAllocateResourcesMapping(state, mapping); ok {
			input.profile = value
		}
	}
	return input
}

func boolValueFromAllocateResourcesMapping(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
	if len(mapping.Value) != 0 {
		var value bool
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return false, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "action.requires_synthesis":
		if state == nil {
			return false, false
		}
		return state.action.RequiresSynthesis, true
	default:
		return false, false
	}
}

func invocationValueFromAllocateResourcesMapping(state *operationExecution, mapping model.OperationMapping) (invocation, bool) {
	if len(mapping.Value) != 0 {
		var value invocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return invocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "in", "invocation":
		if state == nil {
			return invocation{}, false
		}
		return state.in, true
	default:
		return invocation{}, false
	}
}

func profileValueFromAllocateResourcesMapping(state *operationExecution, mapping model.OperationMapping) (profile, bool) {
	if len(mapping.Value) != 0 {
		var value profile
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return profile{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.profile":
		if state == nil {
			return profile{}, false
		}
		value, ok := state.data["profile"].(profile)
		return value, ok
	case "state.profile":
		if state == nil {
			return profile{}, false
		}
		return state.profile, true
	default:
		return profile{}, false
	}
}

func allocateResourcesInputSummary(input allocateResourcesInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"invocation":         invocationSummary(input.invocation),
		"profile":            profileSummary(input.profile),
		"requires_synthesis": fmt.Sprintf("%t", input.requiresSynthesis),
	})
}

func allocateResourcesOutputSummary(allocation allocation, operation OperationSpec) string {
	allocationJSON, err := json.Marshal(allocation)
	if err != nil {
		allocationJSON = []byte(fmt.Sprintf(`{"resource":%q}`, allocation.Resource))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"allocation": string(allocationJSON),
	})
}

func invocationSummary(in invocation) string {
	parts := []string{}
	if task := strings.TrimSpace(in.Task); task != "" {
		parts = append(parts, "task="+task)
	}
	if action := strings.TrimSpace(in.Action); action != "" {
		parts = append(parts, "action="+action)
	}
	if repository := strings.TrimSpace(in.Repository.URL); repository != "" {
		parts = append(parts, "repository="+repository)
	}
	if workplace := strings.TrimSpace(in.Workplace.Name); workplace != "" {
		parts = append(parts, "workplace="+workplace)
	}
	if len(parts) == 0 {
		return "absent"
	}
	return strings.Join(parts, ",")
}

func profileSummary(profile profile) string {
	if strings.TrimSpace(profile.Name) == "" && strings.TrimSpace(profile.ModelBinding) == "" {
		return "absent"
	}
	parts := []string{}
	if name := strings.TrimSpace(profile.Name); name != "" {
		parts = append(parts, "name="+name)
	}
	if mode := strings.TrimSpace(profile.Mode); mode != "" {
		parts = append(parts, "mode="+mode)
	}
	if binding := strings.TrimSpace(profile.ModelBinding); binding != "" {
		parts = append(parts, "binding="+binding)
	}
	return strings.Join(parts, ",")
}

func (e builtinOperationExecutor) prepareWorkplace(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := prepareWorkplaceInputFromOperation(state, operation)
	if !input.requiresWorkplace {
		state.workplace = workplace{Name: strings.TrimSpace(input.invocation.Launch.Directory), Ready: true}
		writeOperationData(state, operation.Out, "workplace", state.workplace)
		state.tracker.skipIO(name, prepareWorkplaceInputSummary(input, operation), prepareWorkplaceOutputSummary(state.workplace, operation), "Рабочее место не требуется для разрешённого действия.")
		return nil
	}

	workplace, err := e.service.prepareWorkplace(ctx, input.invocation, input.profile, input.allocation)
	if err != nil {
		state.result = failedStartResult(err)
		state.tracker.fail(name, "Исполнительное рабочее место не подготовлено.", err, "workplace_not_prepared", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, input.allocation, model.Workplace{}, state.result, err)
		return err
	}

	state.workplace = workplace
	if strings.TrimSpace(state.in.Launch.Directory) == "" {
		state.in.Launch.Directory = workplace.Name
	}
	writeOperationData(state, operation.Out, "workplace", workplace)
	state.tracker.completeIO(name, prepareWorkplaceInputSummary(input, operation), prepareWorkplaceOutputSummary(workplace, operation), fmt.Sprintf("workplace=%s ready=%t", workplace.Name, workplace.Ready))
	return nil
}

type prepareWorkplaceInput struct {
	requiresWorkplace bool
	invocation        invocation
	profile           profile
	allocation        allocation
}

func prepareWorkplaceInputFromOperation(state *operationExecution, operation OperationSpec) prepareWorkplaceInput {
	input := prepareWorkplaceInput{}
	if state != nil {
		input.requiresWorkplace = state.action.RequiresWorkplace
		input.invocation = state.in
		input.profile = state.profile
		input.allocation = state.allocation
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_workplace"]; ok {
		if value, ok := boolValueFromPrepareWorkplaceMapping(state, mapping); ok {
			input.requiresWorkplace = value
		}
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromPrepareWorkplaceMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromPrepareWorkplaceMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromPrepareWorkplaceMapping(state, mapping); ok {
			input.allocation = value
		}
	}
	return input
}

func boolValueFromPrepareWorkplaceMapping(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
	if len(mapping.Value) != 0 {
		var value bool
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return false, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "action.requires_workplace":
		if state == nil {
			return false, false
		}
		return state.action.RequiresWorkplace, true
	default:
		return false, false
	}
}

func invocationValueFromPrepareWorkplaceMapping(state *operationExecution, mapping model.OperationMapping) (invocation, bool) {
	if len(mapping.Value) != 0 {
		var value invocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return invocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "in", "invocation":
		if state == nil {
			return invocation{}, false
		}
		return state.in, true
	default:
		return invocation{}, false
	}
}

func profileValueFromPrepareWorkplaceMapping(state *operationExecution, mapping model.OperationMapping) (profile, bool) {
	if len(mapping.Value) != 0 {
		var value profile
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return profile{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.profile":
		if state == nil {
			return profile{}, false
		}
		value, ok := state.data["profile"].(profile)
		return value, ok
	case "state.profile":
		if state == nil {
			return profile{}, false
		}
		return state.profile, true
	default:
		return profile{}, false
	}
}

func allocationValueFromPrepareWorkplaceMapping(state *operationExecution, mapping model.OperationMapping) (allocation, bool) {
	if len(mapping.Value) != 0 {
		var value allocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return allocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.allocation":
		if state == nil {
			return allocation{}, false
		}
		value, ok := state.data["allocation"].(allocation)
		return value, ok
	case "state.allocation":
		if state == nil {
			return allocation{}, false
		}
		return state.allocation, true
	default:
		return allocation{}, false
	}
}

func prepareWorkplaceInputSummary(input prepareWorkplaceInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"allocation":         allocationSummary(input.allocation),
		"invocation":         invocationSummary(input.invocation),
		"profile":            profileSummary(input.profile),
		"requires_workplace": fmt.Sprintf("%t", input.requiresWorkplace),
	})
}

func prepareWorkplaceOutputSummary(workplace workplace, operation OperationSpec) string {
	workplaceJSON, err := json.Marshal(workplace)
	if err != nil {
		workplaceJSON = []byte(fmt.Sprintf(`{"name":%q}`, workplace.Name))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"workplace": string(workplaceJSON),
	})
}

func allocationSummary(allocation allocation) string {
	if strings.TrimSpace(allocation.Resource) == "" && strings.TrimSpace(allocation.Runner) == "" && strings.TrimSpace(allocation.Model) == "" {
		return "absent"
	}
	parts := []string{}
	if resource := strings.TrimSpace(allocation.Resource); resource != "" {
		parts = append(parts, "resource="+resource)
	}
	if runner := strings.TrimSpace(allocation.Runner); runner != "" {
		parts = append(parts, "runner="+runner)
	}
	if model := strings.TrimSpace(allocation.Model); model != "" {
		parts = append(parts, "model="+model)
	}
	if binding := strings.TrimSpace(allocation.ModelBinding); binding != "" {
		parts = append(parts, "binding="+binding)
	}
	return strings.Join(parts, ",")
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
