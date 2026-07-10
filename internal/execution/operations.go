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

	if strings.TrimSpace(resultFromExecutionData(state).Status) == "" {
		result := LaunchResult{
			Status:  "completed",
			Summary: fmt.Sprintf("action=%s class=%s operations=completed", state.action.Name, state.action.Class),
		}
		s.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, nil)
	}

	return nil
}

func (e builtinOperationExecutor) Execute(ctx context.Context, state *operationExecution, operation OperationSpec) error {
	name := operationResultName(operation)
	switch operationKind(operation) {
	case OperationKindPrepareData:
		return e.prepareData(ctx, state, operation, name)
	case OperationKindLoadPullRequest:
		return e.loadPullRequest(ctx, state, operation, name)
	case OperationKindLoadReviewRemarks:
		return e.loadReviewRemarks(ctx, state, operation, name, operation.Required)
	case OperationKindResolveProfile:
		return e.resolveProfile(ctx, state, operation, name)
	case OperationKindAllocateResources:
		return e.allocateResources(ctx, state, operation, name)
	case OperationKindPrepareWorkplace:
		return e.prepareWorkplace(ctx, state, operation, name)
	case OperationKindBuildDirective:
		return e.buildDirective(ctx, state, operation, name)
	case OperationKindLaunchSynthesis:
		return e.launchSynthesis(ctx, state, operation, name)
	case OperationKindParseResult:
		return e.parseResult(state, operation, name)
	case OperationKindCommitPush:
		return e.commitPush(ctx, state, operation, name)
	case OperationKindPublishMergeRequest:
		return e.publishMergeRequest(ctx, state, operation, name)
	case OperationKindPublishReviewRemarks:
		return e.publishReviewRemarks(ctx, state, operation, name)
	case OperationKindPublishReviewResponses:
		return e.publishReviewResponses(ctx, state, operation, name)
	case OperationKindFinalize:
		return e.finalize(ctx, state, operation, name)
	default:
		return e.unsupported(ctx, state, operation, name)
	}
}

func (e builtinOperationExecutor) prepareData(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	preparedAssignment := prepareDataInputFromOperation(state, operation)
	preparedInvocation := invocationFromExecutionData(state)
	preparedInvocation.Assignment = preparedAssignment
	if preparedAssignment != nil {
		preparedInvocation.Launch.StructuredInput = preparedAssignment.StructuredInput
	}
	var err error
	preparedInvocation, err = syncPullRequestRefsWithWorkplace(state, preparedInvocation, preparedAssignment)
	if err != nil {
		result := failedStartResult(err)
		state.tracker.fail(name, "Данные задания не согласованы с веткой рабочего места.", err, "pull_request_branch_mismatch", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, preparedInvocation, model.Profile{}, model.Allocation{}, model.Workplace{}, result, err)
		return err
	}

	writePrepareData(state, operation, preparedInvocation)
	state.tracker.completeIO(name, prepareDataInputSummary(preparedAssignment, operation), prepareDataOutputSummary(preparedInvocation, operation), "Данные задания подготовлены для выполнения.")
	return nil
}

func writePrepareData(state *operationExecution, operation OperationSpec, in invocation) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"structured_input": {Ref: "data.structured_input"},
			"workplace":        {Ref: "data.workplace"},
			"invocation":       {Ref: "data.invocation"},
		}
	}
	writeOperationData(state, out, "structured_input", in.Launch.StructuredInput)
	writeOperationData(state, out, "workplace", in.Workplace)
	writeOperationData(state, out, "invocation", in)
}

func prepareDataInputFromOperation(state *operationExecution, operation OperationSpec) *ExecutionAssignment {
	if state == nil {
		return &ExecutionAssignment{}
	}
	source := state.assignment
	if source == nil {
		source = invocationFromExecutionData(state).Assignment
	}
	assignment := cloneAssignment(source)
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
	if state == nil {
		return nil, false
	}
	assignment := state.assignment
	if assignment == nil {
		assignment = invocationFromExecutionData(state).Assignment
	}
	if assignment == nil {
		return nil, false
	}
	ref = strings.TrimSpace(ref)
	switch ref {
	case "in.expected_result", "assignment.expected_result":
		return assignment.ExpectedResult, true
	case "in.constraints", "assignment.constraints":
		return assignment.Constraints, true
	case "in.canonical_task", "assignment.canonical_task":
		return assignment.CanonicalTask, true
	case "in.related_objects", "assignment.related_objects":
		return assignment.RelatedObjects, true
	case "in.reasons", "assignment.reasons":
		return assignment.Reasons, true
	case "in.structured_input", "assignment.structured_input":
		return assignment.StructuredInput, true
	default:
		return nil, false
	}
}

func syncPullRequestRefsWithWorkplace(state *operationExecution, in invocation, assignment *ExecutionAssignment) (invocation, error) {
	if state == nil {
		return in, nil
	}

	ref := pullRequestRefFromAssignment(assignment)
	if base := strings.TrimSpace(ref.Base); base != "" && strings.TrimSpace(in.Workplace.BaseRef) == "" {
		in.Workplace.BaseRef = base
	}
	explicitHead := explicitPullRequestHeadFromAssignment(assignment)
	if explicitHead == "" {
		return in, nil
	}
	if strings.TrimSpace(in.Workplace.HeadRef) == "" {
		in.Workplace.HeadRef = explicitHead
	}
	if state.action.Name != ActionStartImplementationPR {
		return in, nil
	}

	workplaceName := strings.TrimSpace(in.Workplace.Name)
	if workplaceName != "" && explicitHead != workplaceName {
		return in, fmt.Errorf("head branch %q does not match workplace branch %q for %s", explicitHead, workplaceName, ActionStartImplementationPR)
	}
	return in, nil
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
	profileInput, profileName := resolveProfileInputFromOperation(state, operation)
	profileInput.Profile = profileName
	profile, err := e.service.resolveProfile(ctx, profileInput)
	if err != nil {
		result := failedStartResult(err)
		writeResolveProfileData(state, operation, model.Profile{}, result)
		state.tracker.fail(name, "Исполнительный профиль не определён.", err, "profile_not_found", false, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, profileInput, model.Profile{}, model.Allocation{}, model.Workplace{}, result, err)
		return err
	}

	writeResolveProfileData(state, operation, profile, LaunchResult{})
	state.tracker.completeIO(name, resolveProfileInputSummary(profileName, operation), resolveProfileOutputSummary(profile, operation), fmt.Sprintf("profile=%s mode=%s", profile.Name, profile.Mode))
	return nil
}

func writeResolveProfileData(state *operationExecution, operation OperationSpec, profile profile, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"profile": {Ref: "data.profile"},
			"result":  {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "profile", profile)
	if strings.TrimSpace(result.Status) != "" || strings.TrimSpace(result.Summary) != "" || result.StructuredOutput != nil {
		writeOperationData(state, out, "result", result)
	}
}

func resolveProfileInputFromOperation(state *operationExecution, operation OperationSpec) (invocation, string) {
	profileInput := invocationFromExecutionData(state)
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromResolveProfileMapping(state, mapping); ok {
			profileInput = value
		}
	}
	return profileInput, resolveProfileInputName(state, operation, profileInput)
}

func resolveProfileInputName(state *operationExecution, operation OperationSpec, profileInput invocation) string {
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
			if strings.TrimSpace(profileInput.Profile) != "" {
				return strings.TrimSpace(profileInput.Profile)
			}
		}
	}
	if strings.TrimSpace(profileInput.Profile) != "" {
		return strings.TrimSpace(profileInput.Profile)
	}
	return "default"
}

func invocationValueFromResolveProfileMapping(state *operationExecution, mapping model.OperationMapping) (invocation, bool) {
	if len(mapping.Value) != 0 {
		var value invocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return invocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.invocation":
		if state == nil {
			return invocation{}, false
		}
		value, ok := state.data["invocation"].(invocation)
		return value, ok
	case "in", "invocation":
		if state == nil {
			return invocation{}, false
		}
		return state.in, true
	default:
		return invocation{}, false
	}
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

func profileFromExecutionData(state *operationExecution) profile {
	if state == nil {
		return profile{}
	}
	if value, ok := state.data["profile"].(profile); ok {
		return value
	}
	return state.profile
}

func firstResolvedProfile(values ...profile) profile {
	for _, value := range values {
		if strings.TrimSpace(value.Name) != "" || strings.TrimSpace(value.ModelBinding) != "" {
			return value
		}
	}
	return profile{}
}

func allocationFromExecutionData(state *operationExecution) allocation {
	if state == nil {
		return allocation{}
	}
	if value, ok := state.data["allocation"].(allocation); ok {
		return value
	}
	return state.allocation
}

func workplaceFromExecutionData(state *operationExecution) workplace {
	if state == nil {
		return workplace{}
	}
	if value, ok := state.data["workplace"].(workplace); ok {
		return value
	}
	return state.workplace
}

func directiveFromExecutionData(state *operationExecution) launchSpec {
	if state == nil {
		return launchSpec{}
	}
	if value, ok := state.data["directive"].(launchSpec); ok {
		return value
	}
	return state.in.Launch
}

func resultFromExecutionData(state *operationExecution) LaunchResult {
	if state == nil {
		return LaunchResult{}
	}
	if value, ok := state.data["result"].(LaunchResult); ok {
		return value
	}
	return state.result
}

func invocationFromExecutionData(state *operationExecution) invocation {
	if state == nil {
		return invocation{}
	}
	if value, ok := state.data["invocation"].(invocation); ok {
		return value
	}
	return state.in
}

func structuredOutputFromExecutionData(state *operationExecution) *StructuredOutput {
	if state == nil {
		return nil
	}
	if value, ok := state.data["structured_output"].(*StructuredOutput); ok {
		return value
	}
	return resultFromExecutionData(state).StructuredOutput
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

func prepareDataOutputSummary(in invocation, operation OperationSpec) string {
	return operationIOSummary(operation.Out, map[string]string{
		"structured_input": structuredInputSummary(in.Launch.StructuredInput),
		"workplace":        workplaceSpecSummary(in.Workplace),
		"invocation":       invocationSummary(in),
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
		allocation := allocation{Resource: "not-required", Source: "action-without-synthesis"}
		writeAllocateResourcesData(state, operation, allocation)
		state.tracker.skipIO(name, allocateResourcesInputSummary(input, operation), allocateResourcesOutputSummary(allocation, operation), "Ресурсное снабжение не требуется для действия без синтеза.")
		return nil
	}

	allocation, err := e.service.allocateResources(ctx, input.invocation, input.profile)
	if err != nil {
		result := failedStartResult(err)
		state.tracker.fail(name, "Ресурсы недоступны.", err, "resources_unavailable", true, false)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, model.Allocation{}, model.Workplace{}, result, err)
		return err
	}

	writeAllocateResourcesData(state, operation, allocation)
	state.tracker.completeIO(name, allocateResourcesInputSummary(input, operation), allocateResourcesOutputSummary(allocation, operation), fmt.Sprintf("resource=%s runner=%s model=%s", allocation.Resource, allocation.Runner, allocation.Model))
	return nil
}

func writeAllocateResourcesData(state *operationExecution, operation OperationSpec, allocation allocation) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"allocation": {Ref: "data.allocation"}}
	}
	writeOperationData(state, out, "allocation", allocation)
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
		input.invocation = invocationFromExecutionData(state)
		input.profile = profileFromExecutionData(state)
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
	case "data.invocation":
		if state == nil {
			return invocation{}, false
		}
		value, ok := state.data["invocation"].(invocation)
		return value, ok
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
		workplace := workplace{Name: strings.TrimSpace(input.invocation.Launch.Directory), Ready: true}
		writePrepareWorkplaceData(state, operation, workplace, input.invocation)
		state.tracker.skipIO(name, prepareWorkplaceInputSummary(input, operation), prepareWorkplaceOutputSummary(workplace, operation), "Рабочее место не требуется для разрешённого действия.")
		return nil
	}

	workplace, err := e.service.prepareWorkplace(ctx, input.invocation, input.profile, input.allocation)
	if err != nil {
		result := failedStartResult(err)
		state.tracker.fail(name, "Исполнительное рабочее место не подготовлено.", err, "workplace_not_prepared", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, input.allocation, model.Workplace{}, result, err)
		return err
	}

	preparedInvocation := input.invocation
	if strings.TrimSpace(preparedInvocation.Launch.Directory) == "" {
		preparedInvocation.Launch.Directory = workplace.Name
	}
	writePrepareWorkplaceData(state, operation, workplace, preparedInvocation)
	state.tracker.completeIO(name, prepareWorkplaceInputSummary(input, operation), prepareWorkplaceOutputSummary(workplace, operation), fmt.Sprintf("workplace=%s ready=%t", workplace.Name, workplace.Ready))
	return nil
}

func writePrepareWorkplaceData(state *operationExecution, operation OperationSpec, workplace workplace, in invocation) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"workplace":  {Ref: "data.workplace"},
			"invocation": {Ref: "data.invocation"},
		}
	}
	writeOperationData(state, out, "workplace", workplace)
	writeOperationData(state, out, "invocation", in)
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
		input.invocation = invocationFromExecutionData(state)
		input.profile = profileFromExecutionData(state)
		input.allocation = allocationFromExecutionData(state)
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
	case "data.invocation":
		if state == nil {
			return invocation{}, false
		}
		value, ok := state.data["invocation"].(invocation)
		return value, ok
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

func (e builtinOperationExecutor) buildDirective(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := buildDirectiveInputFromOperation(state, operation)
	if !input.requiresSynthesis {
		directive := input.invocation.Launch
		writeBuildDirectiveData(state, operation, directive)
		state.tracker.skipIO(name, buildDirectiveInputSummary(input, operation), buildDirectiveOutputSummary(directive, operation), "Исполнительная директива не требуется для действия без синтеза.")
		return nil
	}

	directiveInvocation := input.invocation
	directiveInvocation.Launch.Runner = input.allocation.Runner
	directiveInvocation.Launch.Model = input.allocation.Model
	if strings.TrimSpace(directiveInvocation.Launch.ModelBinding) == "" {
		directiveInvocation.Launch.ModelBinding = input.allocation.ModelBinding
	}
	writeBuildDirectiveData(state, operation, directiveInvocation.Launch)
	state.tracker.completeIO(name, buildDirectiveInputSummary(input, operation), buildDirectiveOutputSummary(directiveInvocation.Launch, operation), "Исполнительная директива подготовлена к запуску.")
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, directiveInvocation, profileFromExecutionData(state), input.allocation, workplaceFromExecutionData(state), LaunchResult{Status: "running"}, nil)
	return nil
}

func writeBuildDirectiveData(state *operationExecution, operation OperationSpec, directive launchSpec) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"directive": {Ref: "data.directive"}}
	}
	writeOperationData(state, out, "directive", directive)
}

type buildDirectiveInput struct {
	requiresSynthesis bool
	invocation        invocation
	allocation        allocation
}

func buildDirectiveInputFromOperation(state *operationExecution, operation OperationSpec) buildDirectiveInput {
	input := buildDirectiveInput{}
	if state != nil {
		input.requiresSynthesis = state.action.RequiresSynthesis
		input.invocation = invocationFromExecutionData(state)
		input.allocation = allocationFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_synthesis"]; ok {
		if value, ok := boolValueFromBuildDirectiveMapping(state, mapping); ok {
			input.requiresSynthesis = value
		}
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromBuildDirectiveMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromBuildDirectiveMapping(state, mapping); ok {
			input.allocation = value
		}
	}
	return input
}

func boolValueFromBuildDirectiveMapping(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
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

func invocationValueFromBuildDirectiveMapping(state *operationExecution, mapping model.OperationMapping) (invocation, bool) {
	if len(mapping.Value) != 0 {
		var value invocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return invocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.invocation":
		if state == nil {
			return invocation{}, false
		}
		value, ok := state.data["invocation"].(invocation)
		return value, ok
	default:
		return invocation{}, false
	}
}

func allocationValueFromBuildDirectiveMapping(state *operationExecution, mapping model.OperationMapping) (allocation, bool) {
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
	default:
		return allocation{}, false
	}
}

func buildDirectiveInputSummary(input buildDirectiveInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"allocation":         allocationSummary(input.allocation),
		"invocation":         invocationSummary(input.invocation),
		"requires_synthesis": fmt.Sprintf("%t", input.requiresSynthesis),
	})
}

func buildDirectiveOutputSummary(directive launchSpec, operation OperationSpec) string {
	directiveJSON, err := json.Marshal(directive)
	if err != nil {
		directiveJSON = []byte(fmt.Sprintf(`{"runner":%q,"model":%q}`, directive.Runner, directive.Model))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"directive": string(directiveJSON),
	})
}

func (e builtinOperationExecutor) launchSynthesis(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := launchSynthesisInputFromOperation(state, operation)
	if !input.requiresSynthesis {
		result := LaunchResult{Status: "skipped", Summary: "synthesis=not-required"}
		writeLaunchSynthesisData(state, operation, result)
		state.tracker.skipIO(name, launchSynthesisInputSummary(input, operation), launchSynthesisOutputSummary(result, operation), "Запуск синтеза не требуется для разрешённого действия.")
		return nil
	}

	launchCtx := launch.WithHistoryHandle(ctx, state.historyHandle)
	launchInvocation := input.invocation
	launchInvocation.Launch = input.directive
	launchInvocation.Launch.CommitPush = false
	result, err := e.service.launch(launchCtx, launchInvocation, input.profile, input.allocation, input.workplace)
	writeLaunchSynthesisData(state, operation, result)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, launchInvocation, firstResolvedProfile(input.profile, profileFromExecutionData(state)), input.allocation, input.workplace, result, err)
	if err != nil {
		if result.StructuredOutput != nil {
			state.tracker.completeIO(name, launchSynthesisInputSummary(input, operation), launchSynthesisOutputSummary(result, operation), fmt.Sprintf("status=%s", result.Status))
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

	state.tracker.completeIO(name, launchSynthesisInputSummary(input, operation), launchSynthesisOutputSummary(result, operation), fmt.Sprintf("status=%s", result.Status))
	return nil
}

func writeLaunchSynthesisData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"result": {Ref: "data.result"}}
	}
	writeOperationData(state, out, "result", result)
}

type launchSynthesisInput struct {
	requiresSynthesis bool
	invocation        invocation
	directive         launchSpec
	profile           profile
	allocation        allocation
	workplace         workplace
}

func launchSynthesisInputFromOperation(state *operationExecution, operation OperationSpec) launchSynthesisInput {
	input := launchSynthesisInput{}
	if state != nil {
		input.requiresSynthesis = state.action.RequiresSynthesis
		input.invocation = invocationFromExecutionData(state)
		input.directive = directiveFromExecutionData(state)
		input.profile = profileFromExecutionData(state)
		input.allocation = allocationFromExecutionData(state)
		input.workplace = workplaceFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_synthesis"]; ok {
		if value, ok := boolValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.requiresSynthesis = value
		}
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["directive"]; ok {
		if value, ok := directiveValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.directive = value
		}
	}
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.allocation = value
		}
	}
	if mapping, ok := operation.In["workplace"]; ok {
		if value, ok := workplaceValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.workplace = value
		}
	}
	return input
}

func boolValueFromLaunchSynthesisMapping(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
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

func invocationValueFromLaunchSynthesisMapping(state *operationExecution, mapping model.OperationMapping) (invocation, bool) {
	if len(mapping.Value) != 0 {
		var value invocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return invocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.invocation":
		if state == nil {
			return invocation{}, false
		}
		value, ok := state.data["invocation"].(invocation)
		return value, ok
	default:
		return invocation{}, false
	}
}

func directiveValueFromLaunchSynthesisMapping(state *operationExecution, mapping model.OperationMapping) (launchSpec, bool) {
	if len(mapping.Value) != 0 {
		var value launchSpec
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return launchSpec{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.directive":
		if state == nil {
			return launchSpec{}, false
		}
		value, ok := state.data["directive"].(launchSpec)
		return value, ok
	default:
		return launchSpec{}, false
	}
}

func profileValueFromLaunchSynthesisMapping(state *operationExecution, mapping model.OperationMapping) (profile, bool) {
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
	default:
		return profile{}, false
	}
}

func allocationValueFromLaunchSynthesisMapping(state *operationExecution, mapping model.OperationMapping) (allocation, bool) {
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
	default:
		return allocation{}, false
	}
}

func workplaceValueFromLaunchSynthesisMapping(state *operationExecution, mapping model.OperationMapping) (workplace, bool) {
	if len(mapping.Value) != 0 {
		var value workplace
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return workplace{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.workplace":
		if state == nil {
			return workplace{}, false
		}
		value, ok := state.data["workplace"].(workplace)
		return value, ok
	default:
		return workplace{}, false
	}
}

func launchSynthesisInputSummary(input launchSynthesisInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"allocation":         allocationSummary(input.allocation),
		"directive":          directiveSummary(input.directive),
		"invocation":         invocationSummary(input.invocation),
		"profile":            profileSummary(input.profile),
		"requires_synthesis": fmt.Sprintf("%t", input.requiresSynthesis),
		"workplace":          workplaceSummary(input.workplace),
	})
}

func launchSynthesisOutputSummary(result LaunchResult, operation OperationSpec) string {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"status":%q}`, result.Status))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"result": string(resultJSON),
	})
}

func directiveSummary(directive launchSpec) string {
	if strings.TrimSpace(directive.Runner) == "" && strings.TrimSpace(directive.Model) == "" {
		return "absent"
	}
	parts := []string{}
	if runner := strings.TrimSpace(directive.Runner); runner != "" {
		parts = append(parts, "runner="+runner)
	}
	if model := strings.TrimSpace(directive.Model); model != "" {
		parts = append(parts, "model="+model)
	}
	if binding := strings.TrimSpace(directive.ModelBinding); binding != "" {
		parts = append(parts, "binding="+binding)
	}
	if directory := strings.TrimSpace(directive.Directory); directory != "" {
		parts = append(parts, "directory="+directory)
	}
	return strings.Join(parts, ",")
}

func workplaceSummary(workplace workplace) string {
	if strings.TrimSpace(workplace.Name) == "" && strings.TrimSpace(workplace.RepositoryRoot) == "" {
		return "absent"
	}
	parts := []string{}
	if name := strings.TrimSpace(workplace.Name); name != "" {
		parts = append(parts, "name="+name)
	}
	if root := strings.TrimSpace(workplace.RepositoryRoot); root != "" {
		parts = append(parts, "repository-root="+root)
	}
	parts = append(parts, fmt.Sprintf("ready=%t", workplace.Ready))
	return strings.Join(parts, ",")
}

func (e builtinOperationExecutor) parseResult(state *operationExecution, operation OperationSpec, name string) error {
	input := parseResultInputFromOperation(state, operation)
	if !input.requiresSynthesis {
		writeOperationData(state, operation.Out, "structured_output", (*StructuredOutput)(nil))
		state.tracker.skipIO(name, parseResultInputSummary(input, operation), parseResultOutputSummary(nil, operation), "Разбор результата синтеза не требуется.")
		return nil
	}

	writeOperationData(state, operation.Out, "structured_output", input.result.StructuredOutput)
	state.tracker.completeIO(name, parseResultInputSummary(input, operation), parseResultOutputSummary(input.result.StructuredOutput, operation), "Результат выполнения нормализован.")
	return nil
}

type parseResultInput struct {
	requiresSynthesis bool
	result            LaunchResult
}

func parseResultInputFromOperation(state *operationExecution, operation OperationSpec) parseResultInput {
	input := parseResultInput{}
	if state != nil {
		input.requiresSynthesis = state.action.RequiresSynthesis
		input.result = resultFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_synthesis"]; ok {
		if value, ok := boolValueFromParseResultMapping(state, mapping); ok {
			input.requiresSynthesis = value
		}
	}
	if mapping, ok := operation.In["result"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.result = value
		}
	}
	return input
}

func boolValueFromParseResultMapping(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
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

func resultValueFromParseResultMapping(state *operationExecution, mapping model.OperationMapping) (LaunchResult, bool) {
	if len(mapping.Value) != 0 {
		var value LaunchResult
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return LaunchResult{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.result":
		if state == nil {
			return LaunchResult{}, false
		}
		value, ok := state.data["result"].(LaunchResult)
		return value, ok
	default:
		return LaunchResult{}, false
	}
}

func parseResultInputSummary(input parseResultInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"requires_synthesis": fmt.Sprintf("%t", input.requiresSynthesis),
		"result":             resultSummary(input.result),
	})
}

func parseResultOutputSummary(output *StructuredOutput, operation OperationSpec) string {
	outputJSON, err := json.Marshal(output)
	if err != nil {
		outputJSON = []byte("null")
	}
	return operationIOSummary(operation.Out, map[string]string{
		"structured_output": string(outputJSON),
	})
}

func (e builtinOperationExecutor) commitPush(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := commitPushInputFromOperation(state, operation)
	if !input.requiresSynthesis {
		writeCommitPushData(state, operation, "", input.result)
		state.tracker.skipIO(name, commitPushInputSummary(input, operation), commitPushOutputSummary("", input.result, operation), "Создание коммита не требуется для действия без синтеза.")
		return nil
	}

	pusher, ok := e.service.launcher.(commitPusher)
	if !ok {
		err := fmt.Errorf("commit-push operation is unsupported by launcher")
		input.result.Status = "failed"
		if strings.TrimSpace(input.result.Summary) == "" {
			input.result.Summary = err.Error()
		}
		writeCommitPushData(state, operation, "", input.result)
		state.tracker.fail(name, "Операция commit-push не поддержана модулем запуска.", err, "commit_push_unsupported", false, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, input.allocation, input.workplace, input.result, err)
		return err
	}

	summary, err := pusher.CommitAndPush(ctx, input.invocation, input.allocation, input.workplace, input.structuredOutput)
	if err != nil {
		input.result.Status = "failed"
		if strings.TrimSpace(input.result.Summary) == "" {
			input.result.Summary = strings.TrimSpace(err.Error())
		}
		writeCommitPushData(state, operation, "", input.result)
		state.tracker.fail(name, "Создание коммита или отправка ветки не выполнены.", err, "commit_push_failed", true, true)
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, input.allocation, input.workplace, input.result, err)
		return err
	}

	input.result.Summary = joinExecutionSummaries(input.result.Summary, summary)
	writeCommitPushData(state, operation, summary, input.result)
	state.tracker.completeIO(name, commitPushInputSummary(input, operation), commitPushOutputSummary(summary, input.result, operation), summary)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, input.invocation, input.profile, input.allocation, input.workplace, input.result, nil)
	return nil
}

func writeCommitPushData(state *operationExecution, operation OperationSpec, summary string, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"commit_summary": {Ref: "data.commit_summary"},
			"result":         {Ref: "data.result"},
		}
	}
	writeOperationData(state, out, "commit_summary", summary)
	writeOperationData(state, out, "result", result)
}

type commitPushInput struct {
	requiresSynthesis bool
	invocation        invocation
	profile           profile
	allocation        allocation
	workplace         workplace
	result            LaunchResult
	structuredOutput  *StructuredOutput
}

func commitPushInputFromOperation(state *operationExecution, operation OperationSpec) commitPushInput {
	input := commitPushInput{}
	if state != nil {
		input.requiresSynthesis = state.action.RequiresSynthesis
		input.invocation = invocationFromExecutionData(state)
		input.profile = profileFromExecutionData(state)
		input.allocation = allocationFromExecutionData(state)
		input.workplace = workplaceFromExecutionData(state)
		input.result = resultFromExecutionData(state)
		input.structuredOutput = structuredOutputFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_synthesis"]; ok {
		if value, ok := boolValueFromParseResultMapping(state, mapping); ok {
			input.requiresSynthesis = value
		}
	}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["allocation"]; ok {
		if value, ok := allocationValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.allocation = value
		}
	}
	if mapping, ok := operation.In["workplace"]; ok {
		if value, ok := workplaceValueFromLaunchSynthesisMapping(state, mapping); ok {
			input.workplace = value
		}
	}
	if mapping, ok := operation.In["result"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.result = value
			input.structuredOutput = value.StructuredOutput
		}
	}
	if mapping, ok := operation.In["structured_output"]; ok {
		if value, ok := structuredOutputValueFromCommitPushMapping(state, mapping); ok {
			input.structuredOutput = value
		}
	}
	return input
}

func structuredOutputValueFromCommitPushMapping(state *operationExecution, mapping model.OperationMapping) (*StructuredOutput, bool) {
	if len(mapping.Value) != 0 {
		var value StructuredOutput
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return &value, true
		}
		return nil, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.structured_output":
		if state == nil {
			return nil, false
		}
		value, ok := state.data["structured_output"].(*StructuredOutput)
		return value, ok
	case "state.result.structured_output":
		if state == nil {
			return nil, false
		}
		return state.result.StructuredOutput, true
	default:
		return nil, false
	}
}

func commitPushInputSummary(input commitPushInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"allocation":         allocationSummary(input.allocation),
		"invocation":         invocationSummary(input.invocation),
		"profile":            profileSummary(input.profile),
		"requires_synthesis": fmt.Sprintf("%t", input.requiresSynthesis),
		"result":             resultSummary(input.result),
		"structured_output":  structuredOutputSummary(input.structuredOutput),
		"workplace":          workplaceSummary(input.workplace),
	})
}

func commitPushOutputSummary(commitSummary string, result LaunchResult, operation OperationSpec) string {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"status":%q}`, result.Status))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"commit_summary": commitSummary,
		"result":         string(resultJSON),
	})
}

func (e builtinOperationExecutor) finalize(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := finalizeInputFromOperation(state, operation)
	if !input.requiresSynthesis {
		result := LaunchResult{
			Status:  "completed",
			Summary: fmt.Sprintf("action=%s class=%s synthesis=not-required", input.actionName, input.actionClass),
		}
		writeFinalizeData(state, operation, result)
		state.tracker.completeIO(name, finalizeInputSummary(input, operation), finalizeOutputSummary(result, operation), finalizeSummary(result))
		e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, state.in, profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, nil)
		return nil
	}

	writeFinalizeData(state, operation, input.result)
	state.tracker.completeIO(name, finalizeInputSummary(input, operation), finalizeOutputSummary(input.result, operation), finalizeSummary(input.result))
	return nil
}

func writeFinalizeData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"result": {Ref: "data.result"}}
	}
	writeOperationData(state, out, "result", result)
}

type finalizeInput struct {
	requiresSynthesis bool
	actionName        string
	actionClass       string
	result            LaunchResult
}

func finalizeInputFromOperation(state *operationExecution, operation OperationSpec) finalizeInput {
	input := finalizeInput{}
	if state != nil {
		input.requiresSynthesis = state.action.RequiresSynthesis
		input.actionName = state.action.Name
		input.actionClass = string(state.action.Class)
		input.result = resultFromExecutionData(state)
	}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["requires_synthesis"]; ok {
		if value, ok := boolValueFromParseResultMapping(state, mapping); ok {
			input.requiresSynthesis = value
		}
	}
	if mapping, ok := operation.In["action_name"]; ok {
		if value, ok := actionStringValueFromFinalizeMapping(state, mapping); ok {
			input.actionName = value
		}
	}
	if mapping, ok := operation.In["action_class"]; ok {
		if value, ok := actionStringValueFromFinalizeMapping(state, mapping); ok {
			input.actionClass = value
		}
	}
	if mapping, ok := operation.In["result"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.result = value
		}
	}
	return input
}

func actionStringValueFromFinalizeMapping(state *operationExecution, mapping model.OperationMapping) (string, bool) {
	if len(mapping.Value) != 0 {
		var value string
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return strings.TrimSpace(value), true
		}
		return "", false
	}
	if state == nil {
		return "", false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "action.name":
		return strings.TrimSpace(state.action.Name), true
	case "action.class":
		return strings.TrimSpace(string(state.action.Class)), true
	default:
		return "", false
	}
}

func finalizeInputSummary(input finalizeInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"requires_synthesis": fmt.Sprintf("%t", input.requiresSynthesis),
		"action_name":        strings.TrimSpace(input.actionName),
		"action_class":       strings.TrimSpace(input.actionClass),
		"result":             resultSummary(input.result),
	})
}

func finalizeOutputSummary(result LaunchResult, operation OperationSpec) string {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"status":%q}`, result.Status))
	}
	return operationIOSummary(operation.Out, map[string]string{
		"result": string(resultJSON),
	})
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
	result := failedStartResult(err)
	writeUnsupportedData(state, operation, result)
	state.tracker.fail(name, "Операция не поддержана текущей реализацией.", err, "operation_unsupported", false, true)
	e.service.updateStartHistory(ctx, state.historyRoot, state.historyHandle, invocationFromExecutionData(state), profileFromExecutionData(state), allocationFromExecutionData(state), workplaceFromExecutionData(state), result, err)
	return err
}

func writeUnsupportedData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"result": {Ref: "data.result"}}
	}
	writeOperationData(state, out, "result", result)
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
