package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/integration"
)

type operationExecution struct {
	in            invocation
	inputs        map[string]any
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
	callStack     []string
}

type builtinOperationExecutor struct {
	service *Service
}

const rebaseAbortTimeout = 30 * time.Second

type commitPusher interface {
	CommitAndPush(context.Context, model.CommitPushInput) (string, error)
}

func (s *Service) runActionOperations(ctx context.Context, state *operationExecution) error {
	executor := builtinOperationExecutor{service: s}
	for _, operation := range state.action.Operations {
		if err := executor.Execute(ctx, state, operation); err != nil {
			return err
		}
	}

	data := executionDataFromState(state)
	if strings.TrimSpace(data.result.Status) == "" {
		result := LaunchResult{
			Status:  "completed",
			Summary: fmt.Sprintf("action=%s class=%s operations=completed", state.action.Name, state.action.Class),
		}
		s.updateStartHistory(ctx, state.historyRoot, state.historyHandle, data.invocation, data.profile, data.allocation, data.workplace, result, nil)
	}

	return nil
}

func (e builtinOperationExecutor) Execute(ctx context.Context, state *operationExecution, operation OperationSpec) error {
	if err := validateRequiredOperationInput(state, operation); err != nil {
		state.tracker.fail(operationResultName(operation), "Обязательное поле входного контракта операции не разрешено.", err, "operation_required_input_missing", false, true)
		e.service.recordOperationHistory(ctx, state, operation, err)
		return err
	}
	var err error
	switch string(operation.Type) {
	case "", OperationTypeBuiltin:
		err = e.execute(ctx, state, operation)
	case OperationTypeAction:
		err = e.executeAction(ctx, state, operation)
	default:
		err = fmt.Errorf("operation %q has unsupported type %q", operationResultName(operation), operation.Type)
		state.tracker.fail(operationResultName(operation), "Тип обработчика операции не поддержан.", err, "operation_type_unsupported", false, true)
	}
	e.service.recordOperationHistory(ctx, state, operation, err)
	return err
}

const maxActionOperationDepth = 16

func (e builtinOperationExecutor) executeAction(ctx context.Context, state *operationExecution, operation OperationSpec) error {
	name := operationResultName(operation)
	actionName := strings.TrimSpace(string(operation.Kind))
	if len(state.callStack) >= maxActionOperationDepth {
		err := fmt.Errorf("action operation depth exceeds %d", maxActionOperationDepth)
		state.tracker.fail(name, "Превышена допустимая глубина вызова действий.", err, "action_operation_depth_exceeded", false, true)
		return err
	}
	for _, current := range state.callStack {
		if current == actionName {
			err := fmt.Errorf("action operation cycle: %s", strings.Join(append(append([]string(nil), state.callStack...), actionName), " -> "))
			state.tracker.fail(name, "Обнаружен циклический вызов действия.", err, "action_operation_cycle", false, true)
			return err
		}
	}

	inputs := make(map[string]any, len(operation.In))
	for field, mapping := range operation.In {
		value, ok := operationMappingRawValue(state, mapping)
		if ok {
			inputs[field] = value
		}
	}
	childInvocation := invocation{Action: actionName, Assignment: &ExecutionAssignment{Action: actionName}}
	childAction, err := e.service.resolveAction(ctx, childInvocation)
	if err != nil {
		state.tracker.fail(name, "Действие-обработчик операции не разрешено.", err, "action_operation_not_resolved", false, true)
		return err
	}
	childState := &operationExecution{
		in:            childInvocation,
		inputs:        inputs,
		assignment:    childInvocation.Assignment,
		action:        childAction,
		data:          map[string]any{},
		historyRoot:   state.historyRoot,
		historyHandle: state.historyHandle,
		tracker:       newOperationTracker(childAction),
		callStack:     append(append([]string(nil), state.callStack...), actionName),
	}
	err = e.service.runActionOperations(ctx, childState)
	children := childState.tracker.snapshot()
	state.tracker.setOperations(name, children)
	if err != nil {
		state.tracker.fail(name, "Действие-обработчик операции завершилось с отказом.", err, "action_operation_failed", false, true)
		state.tracker.setOperations(name, children)
		return err
	}
	for _, field := range childAction.RequiredOut {
		if _, ok := childState.data[field]; !ok {
			err := fmt.Errorf("action %q required output %q is not resolved", childAction.Name, field)
			state.tracker.fail(name, "Обязательный выход действия-обработчика не сформирован.", err, "action_required_output_missing", false, true)
			state.tracker.setOperations(name, children)
			return err
		}
	}
	for _, field := range childAction.OutputFields {
		if value, ok := childState.data[field]; ok {
			writeOperationData(state, operation.Out, field, value)
		}
	}
	state.tracker.completeIO(name, operationIOSummary(operation.In, nil), operationIOSummary(operation.Out, nil), fmt.Sprintf("Действие %q выполнено.", childAction.Name))
	state.tracker.setOperations(name, children)
	return nil
}

func validateRequiredOperationInput(state *operationExecution, operation OperationSpec) error {
	for _, field := range operation.RequiredIn {
		mapping, ok := operation.In[field]
		if !ok || !operationMappingResolved(state, mapping) {
			return fmt.Errorf("operation %q required input %q is not resolved", operationResultName(operation), field)
		}
	}
	return nil
}

func operationMappingResolved(state *operationExecution, mapping model.OperationMapping) bool {
	_, ok := operationMappingRawValue(state, mapping)
	return ok
}

func operationMappingValue[T any](state *operationExecution, mapping model.OperationMapping) (T, bool) {
	var zero T
	raw, ok := operationMappingRawValue(state, mapping)
	if !ok {
		return zero, false
	}
	if value, ok := raw.(T); ok {
		return value, true
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return zero, false
	}
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return zero, false
	}
	return value, true
}

func operationMappingRawValue(state *operationExecution, mapping model.OperationMapping) (any, bool) {
	if len(mapping.Value) != 0 {
		if !json.Valid(mapping.Value) || string(mapping.Value) == "null" {
			return nil, false
		}
		var value any
		if err := json.Unmarshal(mapping.Value, &value); err != nil {
			return nil, false
		}
		return value, true
	}
	if state == nil {
		return nil, false
	}
	ref := strings.TrimSpace(mapping.Ref)
	parts := strings.Split(ref, ".")
	if len(parts) < 2 {
		return nil, false
	}
	switch parts[0] {
	case "action":
		return reflectedPathValue(reflect.ValueOf(state.action), parts[1:])
	case "data":
		if state.data == nil {
			return nil, false
		}
		value, ok := state.data[parts[1]]
		if !ok {
			return nil, false
		}
		return reflectedPathValue(reflect.ValueOf(value), parts[2:])
	case "in":
		if value, ok := operationInputValue(state.inputs, parts[1:]); ok {
			return value, true
		}
		return invocationInputValue(state.in, parts[1:])
	default:
		return nil, false
	}
}

func operationInputValue(inputs map[string]any, path []string) (any, bool) {
	if len(path) == 0 || inputs == nil {
		return nil, false
	}
	value, ok := inputs[path[0]]
	if !ok {
		return nil, false
	}
	return reflectedPathValue(reflect.ValueOf(value), path[1:])
}

func invocationInputValue(in invocation, path []string) (any, bool) {
	if len(path) == 1 {
		switch path[0] {
		case "invocation":
			return in, true
		case "repository_url":
			return in.Repository.URL, true
		case "workplace_name":
			return in.Workplace.Name, true
		case "environment":
			return in.Workplace.Environment, true
		case "base_ref":
			return in.Workplace.BaseRef, true
		case "head_ref":
			return in.Workplace.HeadRef, true
		case "directory":
			return in.Launch.Directory, true
		case "runner":
			return in.Launch.Runner, true
		case "model":
			return in.Launch.Model, true
		case "model_binding":
			return in.Launch.ModelBinding, true
		case "structured_input":
			if assignment := assignmentFromInvocation(in); assignment != nil && assignment.StructuredInput != nil {
				return assignment.StructuredInput, true
			}
			return in.Launch.StructuredInput, in.Launch.StructuredInput != nil
		case "number":
			if assignment := assignmentFromInvocation(in); assignment != nil && assignment.CanonicalTask != nil {
				return assignment.CanonicalTask.Number, true
			}
		case "pull_request_base_ref":
			return pullRequestRefFromAssignment(assignmentFromInvocation(in)).Base, true
		case "pull_request_head_ref":
			return explicitPullRequestHeadFromAssignment(assignmentFromInvocation(in)), true
		}
	}
	if value, ok := reflectedPathValue(reflect.ValueOf(in), path); ok {
		return value, true
	}
	return reflectedPathValue(reflect.ValueOf(assignmentFromInvocation(in)), path)
}

func reflectedPathValue(value reflect.Value, path []string) (any, bool) {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil, false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil, false
	}
	if len(path) == 0 {
		return value.Interface(), true
	}
	if value.Kind() != reflect.Struct {
		return nil, false
	}
	for index := 0; index < value.NumField(); index++ {
		fieldType := value.Type().Field(index)
		jsonName := strings.Split(fieldType.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = fieldType.Name
		}
		if jsonName == path[0] {
			return reflectedPathValue(value.Field(index), path[1:])
		}
	}
	return nil, false
}

func reflectedPathResolved(value reflect.Value, path []string) bool {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return false
	}
	if len(path) == 0 {
		return true
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	name := path[0]
	for index := 0; index < value.NumField(); index++ {
		fieldType := value.Type().Field(index)
		jsonName := strings.Split(fieldType.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = fieldType.Name
		}
		if jsonName != name {
			continue
		}
		return reflectedPathResolved(value.Field(index), path[1:])
	}
	return false
}

func (e builtinOperationExecutor) execute(ctx context.Context, state *operationExecution, operation OperationSpec) error {
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
	case OperationKindBuildPrompt:
		return e.buildPrompt(state, operation, name)
	case OperationKindLaunchSynthesis:
		return e.launchSynthesis(ctx, state, operation, name)
	case OperationKindParseResult:
		return e.parseResult(state, operation, name)
	case OperationKindCommitPush:
		return e.commitPush(ctx, state, operation, name)
	case OperationKindRebase:
		return e.rebase(ctx, state, operation, name)
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
	input := prepareDataInputFromOperation(state, operation)
	preparedAssignment := input.assignment
	preparedInvocation := input.invocation
	preparedInvocation.Assignment = preparedAssignment
	if preparedAssignment != nil {
		preparedInvocation.Launch.StructuredInput = preparedAssignment.StructuredInput
	}
	var err error
	preparedInvocation, err = syncPullRequestRefsWithWorkplace(preparedInvocation, preparedAssignment)
	if err != nil {
		state.tracker.fail(name, "Данные задания не согласованы с веткой рабочего места.", err, "pull_request_branch_mismatch", true, true)
		return err
	}

	writePrepareData(state, operation, preparedInvocation)
	state.tracker.completeIO(name, prepareDataInputSummary(preparedAssignment, operation), prepareDataOutputSummary(preparedInvocation, operation), "Данные задания подготовлены для выполнения.")
	return nil
}

type prepareDataInput struct {
	invocation invocation
	assignment *ExecutionAssignment
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

func prepareDataInputFromOperation(state *operationExecution, operation OperationSpec) prepareDataInput {
	input := prepareDataInput{}
	if mapping, ok := operation.In["invocation"]; ok {
		if value, ok := invocationValueFromPrepareDataMapping(state, mapping); ok {
			input.invocation = value
		}
	}
	assignment := cloneAssignment(assignmentFromInvocation(input.invocation))
	if assignment == nil {
		assignment = &ExecutionAssignment{}
	}
	if len(operation.In) == 0 {
		input.assignment = assignment
		return input
	}

	if mapping, ok := operation.In["expected_result"]; ok {
		assignment.ExpectedResult = stringValueFromPrepareDataMapping(assignment, mapping)
	}
	if mapping, ok := operation.In["constraints"]; ok {
		if constraints, ok := valueFromPrepareDataMapping[[]string](assignment, mapping); ok {
			assignment.Constraints = constraints
		}
	}
	if mapping, ok := operation.In["canonical_task"]; ok {
		if canonicalTask, ok := valueFromPrepareDataMapping[*ObjectRef](assignment, mapping); ok {
			assignment.CanonicalTask = canonicalTask
		} else if canonicalTask, ok := valueFromPrepareDataMapping[ObjectRef](assignment, mapping); ok {
			assignment.CanonicalTask = &canonicalTask
		}
	}
	if mapping, ok := operation.In["related_objects"]; ok {
		if relatedObjects, ok := valueFromPrepareDataMapping[[]ObjectRef](assignment, mapping); ok {
			assignment.RelatedObjects = relatedObjects
		}
	}
	if mapping, ok := operation.In["reasons"]; ok {
		if reasons, ok := valueFromPrepareDataMapping[[]AssignmentReason](assignment, mapping); ok {
			assignment.Reasons = reasons
		}
	}
	if mapping, ok := operation.In["structured_input"]; ok {
		if structuredInput, ok := valueFromPrepareDataMapping[*StructuredInput](assignment, mapping); ok {
			assignment.StructuredInput = structuredInput
		} else if structuredInput, ok := valueFromPrepareDataMapping[StructuredInput](assignment, mapping); ok {
			assignment.StructuredInput = &structuredInput
		}
	}
	input.assignment = assignment
	return input
}

func invocationValueFromPrepareDataMapping(state *operationExecution, mapping model.OperationMapping) (invocation, bool) {
	if len(mapping.Value) != 0 {
		var value invocation
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return invocation{}, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "in.invocation", "invocation":
		if state == nil {
			return invocation{}, false
		}
		return state.in, true
	default:
		return invocation{}, false
	}
}

func stringValueFromPrepareDataMapping(assignment *ExecutionAssignment, mapping model.OperationMapping) string {
	if value, ok := valueFromPrepareDataMapping[string](assignment, mapping); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func valueFromPrepareDataMapping[T any](assignment *ExecutionAssignment, mapping model.OperationMapping) (T, bool) {
	var zero T
	if len(mapping.Value) != 0 {
		var value T
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return value, true
		}
		return zero, false
	}
	raw, ok := prepareDataRefValue(assignment, mapping.Ref)
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

func prepareDataRefValue(assignment *ExecutionAssignment, ref string) (any, bool) {
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

func syncPullRequestRefsWithWorkplace(in invocation, assignment *ExecutionAssignment) (invocation, error) {
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
	if strings.TrimSpace(in.Action) != ActionStartImplementationPR {
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
	profileInput := invocation{}
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

type executionDataSnapshot struct {
	invocation       invocation
	profile          profile
	allocation       allocation
	workplace        workplace
	result           LaunchResult
	structuredOutput *StructuredOutput
}

func executionDataFromState(state *operationExecution) executionDataSnapshot {
	return executionDataSnapshot{
		invocation:       invocationFromExecutionData(state),
		profile:          profileFromExecutionData(state),
		allocation:       allocationFromExecutionData(state),
		workplace:        workplaceFromExecutionData(state),
		result:           resultFromExecutionData(state),
		structuredOutput: structuredOutputFromExecutionData(state),
	}
}

func mergeRequestFromExecutionData(state *operationExecution) *model.MergeRequest {
	if state == nil {
		return nil
	}
	value, ok := state.data["merge_request"].(integration.MergeRequest)
	if !ok || value.Number <= 0 {
		return nil
	}
	return &model.MergeRequest{
		System: value.System, Repository: value.Repository, Number: value.Number,
		Title: value.Title, Body: value.Body, State: value.State,
		BaseRef: value.BaseRef, HeadRef: value.HeadRef, URL: value.URL,
	}
}

func profileFromExecutionData(state *operationExecution) profile {
	if state == nil {
		return profile{}
	}
	if value, ok := state.data["profile"].(profile); ok {
		return value
	}
	return profile{}
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
	return allocation{}
}

func workplaceFromExecutionData(state *operationExecution) workplace {
	if state == nil {
		return workplace{}
	}
	if value, ok := state.data["workplace"].(workplace); ok {
		return value
	}
	return workplace{}
}

func directiveFromExecutionData(state *operationExecution) launchSpec {
	if state == nil {
		return launchSpec{}
	}
	if value, ok := state.data["directive"].(launchSpec); ok {
		return value
	}
	return launchSpec{}
}

func resultFromExecutionData(state *operationExecution) LaunchResult {
	if state == nil {
		return LaunchResult{}
	}
	if value, ok := state.data["result"].(LaunchResult); ok {
		return value
	}
	return LaunchResult{}
}

func invocationFromExecutionData(state *operationExecution) invocation {
	if state == nil {
		return invocation{}
	}
	if value, ok := state.data["invocation"].(invocation); ok {
		return value
	}
	return invocation{}
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
	allocation, err := e.service.allocateResources(ctx, input.invocation(), input.resolvedProfile())
	if err != nil {
		state.tracker.fail(name, "Ресурсы недоступны.", err, "resources_unavailable", true, false)
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
	allowModelFallback    bool
	allowModelFallbackSet bool
	modelBinding          string
	runner                string
	model                 string
	environment           string
	workplaceName         string
	repositoryURL         string
	profile               profile
	invocationValue       invocation
}

func (input allocateResourcesInput) invocation() invocation {
	result := input.invocationValue
	result.Repository = model.RepositorySpec{URL: input.repositoryURL}
	result.Workplace = model.WorkplaceSpec{Name: input.workplaceName, Environment: input.environment}
	result.Launch = model.LaunchSpec{ModelBinding: input.modelBinding, Runner: input.runner, Model: input.model}
	return result
}

func allocateResourcesInputFromOperation(state *operationExecution, operation OperationSpec) allocateResourcesInput {
	input := allocateResourcesInput{}
	if len(operation.In) == 0 {
		return input
	}
	if mapping, ok := operation.In["profile"]; ok {
		input.profile, _ = profileValueFromAllocateResourcesMapping(state, mapping)
	}
	if mapping, ok := operation.In["invocation"]; ok {
		input.invocationValue, _ = invocationValueFromAllocateResourcesMapping(state, mapping)
	}
	input.modelBinding = stringValueFromAllocateResourcesMapping(state, operation.In["model_binding"])
	input.runner = stringValueFromAllocateResourcesMapping(state, operation.In["runner"])
	input.model = stringValueFromAllocateResourcesMapping(state, operation.In["model"])
	input.environment = stringValueFromAllocateResourcesMapping(state, operation.In["environment"])
	input.workplaceName = stringValueFromAllocateResourcesMapping(state, operation.In["workplace_name"])
	input.repositoryURL = stringValueFromAllocateResourcesMapping(state, operation.In["repository_url"])
	if mapping, ok := operation.In["allow_model_fallback"]; ok {
		input.allowModelFallback, input.allowModelFallbackSet = boolValueFromProfileMapping(state, mapping)
	}
	return input
}

func (input allocateResourcesInput) resolvedProfile() profile {
	result := input.profile
	if strings.TrimSpace(input.modelBinding) != "" {
		result.ModelBinding = input.modelBinding
	}
	if input.allowModelFallbackSet {
		result.AllowModelFallback = input.allowModelFallback
	}
	return result
}

func boolValueFromProfileMapping(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
	if len(mapping.Value) != 0 {
		var value bool
		if json.Unmarshal(mapping.Value, &value) == nil {
			return value, true
		}
		return false, false
	}
	if state == nil || strings.TrimSpace(mapping.Ref) != "data.profile.allow_model_fallback" {
		return false, false
	}
	value, ok := state.data["profile"].(profile)
	return value.AllowModelFallback, ok
}

func stringValueFromAllocateResourcesMapping(state *operationExecution, mapping model.OperationMapping) string {
	if value, ok := operationMappingValue[string](state, mapping); ok {
		return strings.TrimSpace(value)
	}
	if len(mapping.Value) != 0 {
		var value string
		if json.Unmarshal(mapping.Value, &value) == nil {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if state == nil {
		return ""
	}
	inv, ok := state.data["invocation"].(invocation)
	if !ok {
		return ""
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.invocation.launch.model_binding":
		return strings.TrimSpace(inv.Launch.ModelBinding)
	case "data.invocation.launch.runner":
		return strings.TrimSpace(inv.Launch.Runner)
	case "data.invocation.launch.model":
		return strings.TrimSpace(inv.Launch.Model)
	case "data.invocation.workplace.environment":
		return strings.TrimSpace(inv.Workplace.Environment)
	case "data.invocation.workplace.name":
		return strings.TrimSpace(inv.Workplace.Name)
	case "data.invocation.repository.url":
		return strings.TrimSpace(inv.Repository.URL)
	default:
		return ""
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
		"model_binding":  input.modelBinding,
		"runner":         input.runner,
		"model":          input.model,
		"environment":    input.environment,
		"workplace_name": input.workplaceName,
		"repository_url": input.repositoryURL,
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
	if input.actionName == ActionStartImplementationPR && strings.TrimSpace(input.pullRequestHeadRef) != "" && strings.TrimSpace(input.workplaceName) != "" && strings.TrimSpace(input.pullRequestHeadRef) != strings.TrimSpace(input.workplaceName) {
		err := fmt.Errorf("head branch %q does not match workplace branch %q for %s", input.pullRequestHeadRef, input.workplaceName, ActionStartImplementationPR)
		state.tracker.fail(name, "Данные задания не согласованы с веткой рабочего места.", err, "pull_request_branch_mismatch", true, true)
		return err
	}
	if _, ok := operation.In["invocation"]; !ok {
		input.invocation = input.resolvedInvocation()
		input.allocation = input.resolvedAllocation()
	}
	if !input.requiresWorkplace {
		workplace := workplace{Name: strings.TrimSpace(input.invocation.Launch.Directory), Ready: true}
		writePrepareWorkplaceData(state, operation, workplace, input.invocation, input.resolvedBaseRef(), input.resolvedHeadRef())
		state.tracker.skipIO(name, prepareWorkplaceInputSummary(input, operation), prepareWorkplaceOutputSummary(workplace, operation), "Рабочее место не требуется для разрешённого действия.")
		return nil
	}

	workplace, err := e.service.prepareWorkplace(ctx, input.invocation, input.profile, input.allocation)
	if err != nil {
		state.tracker.fail(name, "Исполнительное рабочее место не подготовлено.", err, "workplace_not_prepared", true, true)
		return err
	}

	preparedInvocation := input.invocation
	if strings.TrimSpace(preparedInvocation.Launch.Directory) == "" {
		preparedInvocation.Launch.Directory = workplace.Name
	}
	writePrepareWorkplaceData(state, operation, workplace, preparedInvocation, firstNonEmptyTrimmed(workplace.BaseRef, input.resolvedBaseRef()), firstNonEmptyTrimmed(workplace.HeadRef, input.resolvedHeadRef()))
	state.tracker.completeIO(name, prepareWorkplaceInputSummary(input, operation), prepareWorkplaceOutputSummary(workplace, operation), fmt.Sprintf("workplace=%s ready=%t", workplace.Name, workplace.Ready))
	return nil
}

func writePrepareWorkplaceData(state *operationExecution, operation OperationSpec, workplace workplace, in invocation, baseRef string, headRef string) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{
			"workplace":  {Ref: "data.workplace"},
			"invocation": {Ref: "data.invocation"},
		}
	}
	writeOperationData(state, out, "workplace", workplace)
	writeOperationData(state, out, "invocation", in)
	writeOperationData(state, out, "base_ref", strings.TrimSpace(baseRef))
	writeOperationData(state, out, "head_ref", strings.TrimSpace(headRef))
}

type prepareWorkplaceInput struct {
	requiresWorkplace        bool
	invocation               invocation
	profile                  profile
	allocation               allocation
	directory                string
	repositoryURL            string
	environment              string
	workplaceName            string
	baseRef                  string
	headRef                  string
	pullRequestBaseRef       string
	pullRequestHeadRef       string
	actionName               string
	allocatedEnvironment     string
	allocatedEnvironmentType string
}

func (input prepareWorkplaceInput) resolvedHeadRef() string {
	return firstNonEmptyTrimmed(input.headRef, input.pullRequestHeadRef, input.workplaceName)
}

func (input prepareWorkplaceInput) resolvedBaseRef() string {
	return firstNonEmptyTrimmed(input.baseRef, input.pullRequestBaseRef)
}

func (input prepareWorkplaceInput) resolvedInvocation() invocation {
	result := input.invocation
	baseRef := input.baseRef
	if strings.TrimSpace(baseRef) == "" {
		baseRef = input.pullRequestBaseRef
	}
	headRef := input.headRef
	if strings.TrimSpace(headRef) == "" {
		headRef = input.pullRequestHeadRef
	}
	result.Repository.URL = input.repositoryURL
	result.Workplace = model.WorkplaceSpec{Name: input.workplaceName, Environment: input.environment, BaseRef: baseRef, HeadRef: headRef}
	result.Launch.Directory = input.directory
	return result
}

func (input prepareWorkplaceInput) resolvedAllocation() allocation {
	result := input.allocation
	result.Environment = input.allocatedEnvironment
	result.EnvironmentType = input.allocatedEnvironmentType
	return result
}

func prepareWorkplaceInputFromOperation(state *operationExecution, operation OperationSpec) prepareWorkplaceInput {
	input := prepareWorkplaceInput{}
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
	input.directory = stringValueFromPrepareWorkplaceMapping(state, operation.In["directory"])
	input.repositoryURL = stringValueFromPrepareWorkplaceMapping(state, operation.In["repository_url"])
	input.environment = stringValueFromPrepareWorkplaceMapping(state, operation.In["environment"])
	input.workplaceName = stringValueFromPrepareWorkplaceMapping(state, operation.In["workplace_name"])
	input.baseRef = stringValueFromPrepareWorkplaceMapping(state, operation.In["base_ref"])
	input.headRef = stringValueFromPrepareWorkplaceMapping(state, operation.In["head_ref"])
	input.pullRequestBaseRef = stringValueFromPrepareWorkplaceMapping(state, operation.In["pull_request_base_ref"])
	input.pullRequestHeadRef = stringValueFromPrepareWorkplaceMapping(state, operation.In["pull_request_head_ref"])
	input.actionName = stringValueFromPrepareWorkplaceMapping(state, operation.In["action_name"])
	input.allocatedEnvironment = stringValueFromPrepareWorkplaceMapping(state, operation.In["allocated_environment"])
	input.allocatedEnvironmentType = stringValueFromPrepareWorkplaceMapping(state, operation.In["allocated_environment_type"])
	if _, ok := operation.In["invocation"]; ok {
		return input
	}
	input.invocation = input.resolvedInvocation()
	input.allocation = input.resolvedAllocation()
	return input
}

func stringValueFromPrepareWorkplaceMapping(state *operationExecution, mapping model.OperationMapping) string {
	if value, ok := operationMappingValue[string](state, mapping); ok {
		return strings.TrimSpace(value)
	}
	if len(mapping.Value) != 0 {
		var value string
		if json.Unmarshal(mapping.Value, &value) == nil {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if state == nil {
		return ""
	}
	value, ok := state.data["invocation"].(invocation)
	if strings.HasPrefix(mapping.Ref, "data.invocation.") && ok {
		switch strings.TrimPrefix(mapping.Ref, "data.invocation.") {
		case "launch.directory":
			return value.Launch.Directory
		case "repository.url":
			return value.Repository.URL
		case "workplace.environment":
			return value.Workplace.Environment
		case "workplace.name":
			return value.Workplace.Name
		case "workplace.base_ref":
			return value.Workplace.BaseRef
		case "workplace.head_ref":
			return value.Workplace.HeadRef
		}
	}
	allocationValue, allocationOK := state.data["allocation"].(allocation)
	switch strings.TrimSpace(mapping.Ref) {
	case "data.allocation.environment":
		if allocationOK {
			return allocationValue.Environment
		}
	case "data.allocation.environment_type":
		if allocationOK {
			return allocationValue.EnvironmentType
		}
	}
	return ""
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
	directiveInvocation := input.invocation
	directiveInvocation.Launch.Runner = input.allocation.Runner
	directiveInvocation.Launch.Model = input.allocation.Model
	if strings.TrimSpace(directiveInvocation.Launch.ModelBinding) == "" {
		directiveInvocation.Launch.ModelBinding = input.allocation.ModelBinding
	}
	writeBuildDirectiveData(state, operation, directiveInvocation.Launch)
	state.tracker.completeIO(name, buildDirectiveInputSummary(input, operation), buildDirectiveOutputSummary(directiveInvocation.Launch, operation), "Исполнительная директива подготовлена к запуску.")
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
	invocation   invocation
	profile      profile
	allocation   allocation
	workplace    workplace
	directory    string
	runner       string
	model        string
	modelBinding string
}

func buildDirectiveInputFromOperation(state *operationExecution, operation OperationSpec) buildDirectiveInput {
	input := buildDirectiveInput{}
	if len(operation.In) == 0 {
		return input
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
	if mapping, ok := operation.In["profile"]; ok {
		if value, ok := profileValueFromBuildDirectiveMapping(state, mapping); ok {
			input.profile = value
		}
	}
	if mapping, ok := operation.In["workplace"]; ok {
		if value, ok := workplaceValueFromBuildDirectiveMapping(state, mapping); ok {
			input.workplace = value
		}
	}
	input.directory = stringValueFromBuildDirectiveMapping(state, operation.In["directory"])
	input.runner = stringValueFromBuildDirectiveMapping(state, operation.In["runner"])
	input.model = stringValueFromBuildDirectiveMapping(state, operation.In["model"])
	input.modelBinding = stringValueFromBuildDirectiveMapping(state, operation.In["model_binding"])
	if _, ok := operation.In["invocation"]; !ok {
		input.invocation = invocation{Launch: model.LaunchSpec{Directory: input.directory}}
		input.allocation = allocation{Runner: input.runner, Model: input.model, ModelBinding: input.modelBinding}
	}
	return input
}

func stringValueFromBuildDirectiveMapping(state *operationExecution, mapping model.OperationMapping) string {
	if value, ok := operationMappingValue[string](state, mapping); ok {
		return strings.TrimSpace(value)
	}
	if len(mapping.Value) != 0 {
		var value string
		if json.Unmarshal(mapping.Value, &value) == nil {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if state == nil {
		return ""
	}
	invocationValue, invocationOK := state.data["invocation"].(invocation)
	allocationValue, allocationOK := state.data["allocation"].(allocation)
	switch strings.TrimSpace(mapping.Ref) {
	case "data.invocation.launch.directory":
		if invocationOK {
			return invocationValue.Launch.Directory
		}
	case "data.allocation.runner":
		if allocationOK {
			return allocationValue.Runner
		}
	case "data.allocation.model":
		if allocationOK {
			return allocationValue.Model
		}
	case "data.allocation.model_binding":
		if allocationOK {
			return allocationValue.ModelBinding
		}
	}
	return ""
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

func profileValueFromBuildDirectiveMapping(state *operationExecution, mapping model.OperationMapping) (profile, bool) {
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

func workplaceValueFromBuildDirectiveMapping(state *operationExecution, mapping model.OperationMapping) (workplace, bool) {
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

func buildDirectiveInputSummary(input buildDirectiveInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"allocation": allocationSummary(input.allocation),
		"invocation": invocationSummary(input.invocation),
		"profile":    profileSummary(input.profile),
		"workplace":  workplaceSummary(input.workplace),
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

func (e builtinOperationExecutor) buildPrompt(state *operationExecution, operation OperationSpec, name string) error {
	spec := launchSpec{}
	spec.Prompt, _ = operationMappingValue[string](state, operation.In["prompt"])
	spec.PromptAdditions, _ = operationMappingValue[[]string](state, operation.In["prompt_additions"])
	spec.StructuredInput, _ = operationMappingValue[*StructuredInput](state, operation.In["structured_input"])
	spec.StructuredOutput, _ = operationMappingValue[bool](state, operation.In["structured_output"])
	spec.StructuredOutputRequired, _ = operationMappingValue[bool](state, operation.In["structured_output_required"])
	spec.StructuredOutputFields, _ = operationMappingValue[[]string](state, operation.In["structured_output_fields"])
	prompt, err := launch.BuildPrompt(spec)
	if err != nil {
		state.tracker.fail(name, "Исполнительная директива не сформирована.", err, "prompt_not_built", false, true)
		return err
	}
	reviewRemarks, _ := operationMappingValue[[]integration.ReviewRemark](state, operation.In["review_remarks"])
	if len(reviewRemarks) != 0 {
		remarksAllowed := spec.StructuredOutputFields == nil
		for _, field := range spec.StructuredOutputFields {
			if strings.TrimSpace(field) == "remarks" {
				remarksAllowed = true
				break
			}
		}
		if !remarksAllowed {
			writeOperationData(state, operation.Out, "prompt", prompt)
			state.tracker.fail(name, "Исполнительная директива не согласована со схемой структурированного вывода.", fmt.Errorf("previous review remarks require structured output field %q", "remarks"), "review_remarks_field_not_allowed", false, true)
			return fmt.Errorf("previous review remarks require structured output field %q", "remarks")
		}
		payload, err := json.Marshal(canonicalReviewRemarks(reviewRemarks))
		if err != nil {
			state.tracker.fail(name, "Замечания ревизии не включены в исполнительную директиву.", err, "review_remarks_not_encoded", false, true)
			return err
		}
		prompt = joinExecutionSummaries(prompt, "Use the canonical review remarks below as execution context. Return new findings with a new id. When a finding continues or reopens an existing remark, preserve its external_id and thread_id in that remarks element. Do not return review_responses unless that field is explicitly allowed by the structured output schema. When review_responses is allowed, preserve ExternalID, ReplyToID and Type as remark_id, thread_id and type. For type inline, provide thread_id; for type comment, publish a new related comment; for type local, do not publish an external response.", string(payload))
	}
	writeOperationData(state, operation.Out, "prompt", prompt)
	state.tracker.completeIO(name, operationIOSummary(operation.In, map[string]string{
		"prompt":           presenceSummary(spec.Prompt),
		"structured_input": presenceSummary(fmt.Sprintf("%v", spec.StructuredInput != nil)),
		"review_remarks":   formatInt(len(reviewRemarks)),
	}), operationIOSummary(operation.Out, map[string]string{"prompt": presenceSummary(prompt)}), "Исполнительная директива сформирована.")
	return nil
}

// canonicalReviewRemarks переводит поля интеграционного замечания в
// словарь структурированного вывода до передачи данных исполнителю.
func canonicalReviewRemarks(reviewRemarks []integration.ReviewRemark) []model.StructuredRemark {
	remarks := make([]model.StructuredRemark, 0, len(reviewRemarks))
	for _, remark := range reviewRemarks {
		remarks = append(remarks, model.StructuredRemark{
			ExternalID: strings.TrimSpace(remark.ExternalID),
			ThreadID:   strings.TrimSpace(remark.ReplyToID),
			Status:     strings.TrimSpace(remark.State),
			Title:      "Предыдущее замечание ревизии",
			Body:       strings.TrimSpace(remark.Body),
			Path:       strings.TrimSpace(remark.Path),
			Line:       remark.Line,
			Side:       strings.TrimSpace(remark.Side),
		})
	}
	return remarks
}

func (e builtinOperationExecutor) launchSynthesis(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := launchSynthesisInputFromOperation(state, operation)
	launchCtx := launch.WithHistoryHandle(ctx, state.historyHandle)
	launchInvocation := invocation{Launch: launchSpec{Prompt: input.prompt, Directory: input.directory, Runner: input.runner, Model: input.model}}
	if input.resumeSessionID != "" {
		launchInvocation.Launch.Resume = &model.ResumeSpec{RunnerSessionID: input.resumeSessionID}
	}
	launchWorkplace := workplace{Name: input.directory, RepositoryRoot: input.directory, Ready: true}
	launchAllocation := allocation{Runner: input.runner, Model: input.model}
	result, err := e.service.launch(launchCtx, launchInvocation, profile{}, launchAllocation, launchWorkplace)
	writeLaunchSynthesisData(state, operation, result)
	if err != nil {
		state.tracker.fail(name, "Запуск синтеза завершился отказом.", err, "synthesis_failed", true, true)
		return err
	}

	state.tracker.completeIO(name, launchSynthesisInputSummary(input, operation), launchSynthesisOutputSummary(result, operation), fmt.Sprintf("status=%s", result.Status))
	return nil
}

func writeLaunchSynthesisData(state *operationExecution, operation OperationSpec, result LaunchResult) {
	writeOperationData(state, operation.Out, "raw_output", rawOutputFromLaunchResult(result))
	writeOperationData(state, operation.Out, "session_id", result.RunnerSessionID)
	writeOperationData(state, operation.Out, "result", result)
}

func rawOutputFromLaunchResult(result LaunchResult) string {
	if result.RawOutput != "" {
		return result.RawOutput
	}
	parts := []string{}
	if strings.TrimSpace(result.Summary) != "" {
		parts = append(parts, strings.TrimSpace(result.Summary))
	}
	if result.StructuredOutput != nil {
		if payload, err := json.Marshal(result.StructuredOutput); err == nil {
			parts = append(parts, "<progress-structured-output>\n"+string(payload)+"\n</progress-structured-output>")
		}
	}
	return strings.Join(parts, "\n")
}

type launchSynthesisInput struct {
	prompt          string
	directory       string
	runner          string
	model           string
	resumeSessionID string
}

func launchSynthesisInputFromOperation(state *operationExecution, operation OperationSpec) launchSynthesisInput {
	input := launchSynthesisInput{}
	if len(operation.In) == 0 {
		return input
	}
	input.prompt, _ = operationMappingValue[string](state, operation.In["prompt"])
	input.directory, _ = operationMappingValue[string](state, operation.In["directory"])
	input.runner, _ = operationMappingValue[string](state, operation.In["runner"])
	input.model, _ = operationMappingValue[string](state, operation.In["model"])
	input.resumeSessionID, _ = operationMappingValue[string](state, operation.In["resume_session_id"])
	if strings.TrimSpace(input.prompt) == "" {
		if directive, ok := directiveValueFromLaunchSynthesisMapping(state, operation.In["directive"]); ok {
			input.prompt, _ = launch.BuildPrompt(directive)
			input.directory = directive.Directory
			input.runner = directive.Runner
			input.model = directive.Model
			if directive.Resume != nil {
				input.resumeSessionID = directive.Resume.RunnerSessionID
			}
		}
	}
	return input
}

func launchStringValue(state *operationExecution, mapping model.OperationMapping) string {
	if value, ok := operationMappingValue[string](state, mapping); ok {
		return strings.TrimSpace(value)
	}
	if len(mapping.Value) != 0 {
		var value string
		if json.Unmarshal(mapping.Value, &value) == nil {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if state == nil {
		return ""
	}
	ref := strings.TrimSpace(mapping.Ref)
	if value, ok := state.data["invocation"].(invocation); ok {
		switch ref {
		case "data.invocation.task":
			return value.Task
		case "data.invocation.action":
			return value.Action
		case "data.invocation.repository.url":
			return value.Repository.URL
		case "data.invocation.launch.directory":
			return value.Launch.Directory
		}
	}
	if value, ok := state.data["profile"].(profile); ok && ref == "data.profile.name" {
		return value.Name
	}
	if value, ok := state.data["allocation"].(allocation); ok {
		switch ref {
		case "data.allocation.runner":
			return value.Runner
		case "data.allocation.model":
			return value.Model
		case "data.allocation.model_binding":
			return value.ModelBinding
		}
	}
	if value, ok := state.data["workplace"].(workplace); ok && ref == "data.workplace.name" {
		return value.Name
	}
	return ""
}

func launchBoolValue(state *operationExecution, mapping model.OperationMapping) (bool, bool) {
	if len(mapping.Value) != 0 {
		var value bool
		err := json.Unmarshal(mapping.Value, &value)
		return value, err == nil
	}
	if state == nil {
		return false, false
	}
	value, ok := state.data["profile"].(profile)
	if !ok {
		return false, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.profile.structured_output":
		return value.StructuredOutput, true
	case "data.profile.structured_output_required":
		return value.StructuredOutputRequired, true
	default:
		return false, false
	}
}

func launchStringSliceValue(state *operationExecution, mapping model.OperationMapping) ([]string, bool) {
	if len(mapping.Value) != 0 {
		var value []string
		err := json.Unmarshal(mapping.Value, &value)
		return value, err == nil
	}
	if state == nil {
		return nil, false
	}
	value, ok := state.data["profile"].(profile)
	if !ok {
		return nil, false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.profile.prompt_additions":
		return append([]string(nil), value.PromptAdditions...), true
	case "data.profile.structured_output_fields":
		return append([]string(nil), value.StructuredOutputFields...), true
	default:
		return nil, false
	}
}

func launchStructuredInputValue(state *operationExecution, mapping model.OperationMapping) (*StructuredInput, bool) {
	if value, ok := operationMappingValue[*StructuredInput](state, mapping); ok {
		return value, true
	}
	if state == nil || strings.TrimSpace(mapping.Ref) != "data.invocation.launch.structured_input" {
		return nil, false
	}
	value, ok := state.data["invocation"].(invocation)
	return value.Launch.StructuredInput, ok
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
	case "in.invocation", "invocation":
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
		"prompt":            presenceSummary(input.prompt),
		"directory":         input.directory,
		"runner":            input.runner,
		"model":             input.model,
		"resume_session_id": presenceSummary(input.resumeSessionID),
	})
}

func launchSynthesisOutputSummary(result LaunchResult, operation OperationSpec) string {
	return operationIOSummary(operation.Out, map[string]string{
		"raw_output": presenceSummary(rawOutputFromLaunchResult(result)),
		"session_id": presenceSummary(result.RunnerSessionID),
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
	if input.legacyResult != nil {
		writeOperationData(state, operation.Out, "structured_output", input.legacyResult.StructuredOutput)
		writeOperationData(state, operation.Out, "result", *input.legacyResult)
		state.tracker.completeIO(name, parseResultInputSummary(input, operation), parseResultOutputSummary(input.legacyResult.StructuredOutput, operation), "Результат выполнения нормализован.")
		return nil
	}
	plain, rawStructured, structured, err := launch.ParseOutput(input.rawOutput)
	if err != nil {
		state.tracker.fail(name, "Результат выполнения не приведён к нормализованной форме.", err, "result_not_parsed", false, true)
		return err
	}
	result := LaunchResult{Status: "completed", Summary: strings.TrimSpace(plain), RawOutput: input.rawOutput, RawStructuredOutput: rawStructured, StructuredOutput: structured, RunnerSessionID: input.sessionID}
	writeOperationData(state, operation.Out, "structured_output", structured)
	writeOperationData(state, operation.Out, "result", result)
	state.tracker.completeIO(name, parseResultInputSummary(input, operation), parseResultOutputSummary(structured, operation), "Результат выполнения нормализован.")
	return nil
}

type parseResultInput struct {
	rawOutput    string
	sessionID    string
	legacyResult *LaunchResult
}

func parseResultInputFromOperation(state *operationExecution, operation OperationSpec) parseResultInput {
	input := parseResultInput{}
	if len(operation.In) == 0 {
		return input
	}
	input.rawOutput, _ = operationMappingValue[string](state, operation.In["raw_output"])
	input.sessionID, _ = operationMappingValue[string](state, operation.In["session_id"])
	if mapping, ok := operation.In["result"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.legacyResult = &value
		}
	}
	return input
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
	case "data.result", "data.result.structured_output":
		if state == nil {
			return LaunchResult{}, false
		}
		value, ok := state.data["result"].(LaunchResult)
		if strings.TrimSpace(mapping.Ref) == "data.result.structured_output" && ok {
			value = LaunchResult{StructuredOutput: value.StructuredOutput}
		}
		return value, ok
	default:
		return LaunchResult{}, false
	}
}

func parseResultInputSummary(input parseResultInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"raw_output": presenceSummary(input.rawOutput),
		"session_id": presenceSummary(input.sessionID),
	})
}

func parseResultOutputSummary(output *StructuredOutput, operation OperationSpec) string {
	outputJSON, err := json.Marshal(output)
	if err != nil {
		outputJSON = []byte("null")
	}
	return operationIOSummary(operation.Out, map[string]string{
		"structured_output": string(outputJSON),
		"result":            structuredOutputSummary(output),
	})
}

func (e builtinOperationExecutor) commitPush(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := commitPushInputFromOperation(state, operation)
	pusher, ok := e.service.launcher.(commitPusher)
	if !ok {
		err := fmt.Errorf("commit-push operation is unsupported by launcher")
		state.tracker.fail(name, "Операция commit-push не поддержана модулем запуска.", err, "commit_push_unsupported", false, true)
		return err
	}

	summary, err := pusher.CommitAndPush(ctx, input.request)
	if err != nil {
		state.tracker.fail(name, "Создание коммита или отправка ветки не выполнены.", err, "commit_push_failed", true, true)
		return err
	}

	writeCommitPushData(state, operation, summary)
	state.tracker.completeIO(name, commitPushInputSummary(input, operation), commitPushOutputSummary(summary, operation), summary)
	return nil
}

func (e builtinOperationExecutor) rebase(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := rebaseInputFromOperation(state, operation)
	if strings.TrimSpace(input.Directory) == "" {
		return e.failRebase(state, operation, name, "Каталог рабочего места не задан.", "rebase_directory_required", fmt.Errorf("rebase directory is required"))
	}
	if strings.TrimSpace(input.BaseRef) == "" {
		return e.failRebase(state, operation, name, "Базовая ссылка для перебазирования не задана.", "rebase_base_ref_required", fmt.Errorf("rebase base ref is required"))
	}
	if strings.HasPrefix(input.BaseRef, "-") {
		return e.failRebase(state, operation, name, "Базовая ссылка имеет недопустимый вид.", "rebase_base_ref_invalid", fmt.Errorf("rebase base ref must not start with '-'"))
	}
	workplaceBranch := strings.TrimSpace(input.HeadRef)
	if workplaceBranch == "" {
		return e.failRebase(state, operation, name, "Рабочая ветка для перебазирования не задана.", "rebase_head_ref_required", fmt.Errorf("rebase head ref is required"))
	}

	gitOutput := e.service.runGitOutput
	if gitOutput == nil {
		gitOutput = runGitOutput
	}
	if _, err := gitOutput(ctx, input.Directory, "rev-parse", "--is-inside-work-tree"); err != nil {
		return e.failRebase(state, operation, name, "Рабочее место не является Git-репозиторием.", "rebase_not_repository", err)
	}
	dirty, err := gitOutput(ctx, input.Directory, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return e.failRebase(state, operation, name, "Состояние рабочего места не получено.", "rebase_status_failed", err)
	}
	if strings.TrimSpace(strings.ReplaceAll(dirty, "\x00", "")) != "" {
		return e.failRebase(state, operation, name, "Перебазирование запрещено при незаписанных изменениях.", "rebase_worktree_dirty", fmt.Errorf("working tree has uncommitted changes"))
	}
	if input.ForceWithLease && !allowForceWithLease(input.Git) {
		return e.failRebase(state, operation, name, "Безопасная принудительная отправка не разрешена настройкой Git.", "rebase_force_with_lease_not_allowed", fmt.Errorf("git.push.allow-force-with-lease is not enabled"))
	}
	if input.ForceWithLease && !input.Push {
		return e.failRebase(state, operation, name, "Политика --force-with-lease требует явного разрешения отправки.", "rebase_push_policy_required", fmt.Errorf("force-with-lease requires push=true"))
	}
	branch, err := gitOutput(ctx, input.Directory, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branch) == "" {
		if err == nil {
			err = fmt.Errorf("current branch is empty")
		}
		return e.failRebase(state, operation, name, "Текущая ветка не определена.", "rebase_branch_failed", err)
	}
	branch = strings.TrimSpace(branch)
	if branch != workplaceBranch {
		return e.failRebase(state, operation, name, "Текущая ветка не совпадает с рабочей веткой.", "rebase_head_ref_mismatch", fmt.Errorf("current branch %q does not match workplace branch %q", branch, input.HeadRef))
	}
	protectedBranch := strings.TrimSpace(input.ProtectedRef)
	if protectedBranch == "" {
		protectedBranch = "main"
	}
	if branch == normalizeRebaseRef(protectedBranch) {
		return e.failRebase(state, operation, name, "Перебазирование основной ветки запрещено.", "rebase_base_branch", fmt.Errorf("current branch %q is the rebase base branch", branch))
	}

	if _, err := gitOutput(ctx, input.Directory, "fetch", "origin", input.BaseRef); err != nil {
		return e.failRebase(state, operation, name, "Получение базовой ссылки завершилось отказом.", "rebase_fetch_failed", err)
	}
	remoteOID := ""
	if input.ForceWithLease {
		remoteOID, err = gitOutput(ctx, input.Directory, "rev-parse", "--verify", "refs/remotes/origin/"+branch)
		if err != nil || strings.TrimSpace(remoteOID) == "" {
			if err == nil {
				err = fmt.Errorf("remote branch origin/%s has no commit", branch)
			}
			return e.failRebase(state, operation, name, "Исходная вершина удалённой ветки не определена.", "rebase_remote_head_failed", err)
		}
		remoteOID = strings.TrimSpace(remoteOID)
	}
	if _, err := gitOutput(ctx, input.Directory, "rebase", "--", "FETCH_HEAD"); err != nil {
		abortErr := error(nil)
		abortCtx, cancelAbort := context.WithTimeout(context.WithoutCancel(ctx), rebaseAbortTimeout)
		_, abortErr = gitOutput(abortCtx, input.Directory, "rebase", "--abort")
		cancelAbort()
		if abortErr != nil {
			err = fmt.Errorf("%w; additionally failed to abort rebase: %v", err, abortErr)
		}
		return e.failRebase(state, operation, name, "Перебазирование завершилось конфликтом и было прервано.", "rebase_conflict", err)
	}

	summary := fmt.Sprintf("rebased branch=%s base=%s", branch, input.BaseRef)
	if input.Push {
		pushArgs := []string{"push"}
		if input.ForceWithLease {
			pushArgs = append(pushArgs, "--force-with-lease=refs/heads/"+branch+":"+remoteOID)
		}
		pushArgs = append(pushArgs, "origin", "HEAD:"+branch)
		if _, err := gitOutput(ctx, input.Directory, pushArgs...); err != nil {
			return e.failRebase(state, operation, name, "Отправка перебазированной ветки завершилась отказом.", "rebase_push_failed", err)
		}
		if input.ForceWithLease {
			summary += " pushed=force-with-lease"
		} else {
			summary += " pushed=normal"
		}
	}

	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"rebase_summary": {Ref: "data.rebase_summary"}}
	}
	writeOperationData(state, out, "rebase_summary", summary)
	state.tracker.completeIO(name, rebaseInputSummary(input, operation), operationIOSummary(operation.Out, map[string]string{"rebase_summary": summary}), summary)
	return nil
}

func (e builtinOperationExecutor) failRebase(state *operationExecution, operation OperationSpec, name, summary, code string, err error) error {
	state.tracker.fail(name, summary, err, code, true, true)
	return err
}

func allowForceWithLease(config *model.GitConfig) bool {
	return config != nil && config.Push != nil && config.Push.AllowForceWithLease
}

func rebaseInputFromOperation(state *operationExecution, operation OperationSpec) model.RebaseInput {
	input := model.RebaseInput{}
	input.Directory, _ = operationMappingValue[string](state, operation.In["directory"])
	input.BaseRef, _ = operationMappingValue[string](state, operation.In["base_ref"])
	input.HeadRef, _ = operationMappingValue[string](state, operation.In["head_ref"])
	input.ProtectedRef, _ = operationMappingValue[string](state, operation.In["protected_ref"])
	input.Push, _ = operationMappingValue[bool](state, operation.In["push"])
	input.ForceWithLease, _ = operationMappingValue[bool](state, operation.In["force_with_lease"])
	if mapping, ok := operation.In["git"]; ok {
		if strings.TrimSpace(mapping.Ref) == "data.allocation.git" && state != nil {
			input.Git, _ = state.data["allocation"].(allocation).Git, true
		} else {
			input.Git, _ = operationMappingValue[*model.GitConfig](state, mapping)
		}
	}
	return input
}

func rebaseInputSummary(input model.RebaseInput, operation OperationSpec) string {
	return operationIOSummary(operation.In, map[string]string{
		"directory":        input.Directory,
		"base_ref":         input.BaseRef,
		"head_ref":         input.HeadRef,
		"protected_ref":    input.ProtectedRef,
		"push":             fmt.Sprintf("%t", input.Push),
		"force_with_lease": fmt.Sprintf("%t", input.ForceWithLease),
	})
}

func normalizeRebaseRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return strings.TrimPrefix(ref, "origin/")
}

func writeCommitPushData(state *operationExecution, operation OperationSpec, summary string) {
	out := operation.Out
	if len(out) == 0 {
		out = model.OperationMap{"commit_summary": {Ref: "data.commit_summary"}}
	}
	writeOperationData(state, out, "commit_summary", summary)
}

type commitPushInput struct {
	request model.CommitPushInput
}

func commitPushInputFromOperation(state *operationExecution, operation OperationSpec) commitPushInput {
	input := commitPushInput{}
	input.request.Directory, _ = stringValueFromCommitPushMapping(state, operation.In["directory"])
	input.request.CommitMessage, _ = stringValueFromCommitPushMapping(state, operation.In["commit_message"])
	input.request.FallbackName, _ = stringValueFromCommitPushMapping(state, operation.In["fallback_name"])
	input.request.Git, _ = gitConfigValueFromCommitPushMapping(state, operation.In["git"])
	input.request.PrivateStore, _ = privateStoreValueFromCommitPushMapping(state, operation.In["private_store"])
	input.request.ConfigHome, _ = stringValueFromCommitPushMapping(state, operation.In["config_home"])
	return input
}

func stringValueFromCommitPushMapping(state *operationExecution, mapping model.OperationMapping) (string, bool) {
	if value, ok := operationMappingValue[string](state, mapping); ok {
		return strings.TrimSpace(value), true
	}
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
	case "data.workplace.name":
		value, ok := state.data["workplace"].(workplace)
		return strings.TrimSpace(value.Name), ok
	case "data.invocation.workplace.name":
		value, ok := state.data["invocation"].(invocation)
		return strings.TrimSpace(value.Workplace.Name), ok
	case "data.structured_output.commit_message":
		value, ok := state.data["structured_output"].(*StructuredOutput)
		if !ok || value == nil {
			return "", false
		}
		return strings.TrimSpace(value.CommitMessage), true
	case "data.allocation.config_home":
		value, ok := state.data["allocation"].(allocation)
		return strings.TrimSpace(value.ConfigHome), ok
	default:
		return "", false
	}
}

func gitConfigValueFromCommitPushMapping(state *operationExecution, mapping model.OperationMapping) (*model.GitConfig, bool) {
	if state == nil || strings.TrimSpace(mapping.Ref) != "data.allocation.git" {
		return nil, false
	}
	value, ok := state.data["allocation"].(allocation)
	return value.Git, ok
}

func privateStoreValueFromCommitPushMapping(state *operationExecution, mapping model.OperationMapping) (model.ResourcePrivateStoreConfig, bool) {
	if state == nil || strings.TrimSpace(mapping.Ref) != "data.allocation.private_store" {
		return model.ResourcePrivateStoreConfig{}, false
	}
	value, ok := state.data["allocation"].(allocation)
	return value.PrivateStore, ok
}

func structuredOutputValueFromCommitPushMapping(state *operationExecution, mapping model.OperationMapping) (*StructuredOutput, bool) {
	if len(mapping.Value) != 0 {
		var value StructuredOutput
		if err := json.Unmarshal(mapping.Value, &value); err == nil {
			return &value, true
		}
		return nil, false
	}
	if state == nil || strings.TrimSpace(mapping.Ref) != "data.structured_output" {
		return nil, false
	}
	value, ok := state.data["structured_output"].(*StructuredOutput)
	return value, ok
}

func commitPushInputSummary(input commitPushInput, operation OperationSpec) string {
	git := ""
	if input.request.Git != nil {
		git = "configured"
	}
	return operationIOSummary(operation.In, map[string]string{
		"directory":      input.request.Directory,
		"commit_message": presenceSummary(input.request.CommitMessage),
		"fallback_name":  input.request.FallbackName,
		"git":            presenceSummary(git),
		"private_store":  presenceSummary(input.request.PrivateStore.Type),
		"config_home":    presenceSummary(input.request.ConfigHome),
	})
}

func commitPushOutputSummary(commitSummary string, operation OperationSpec) string {
	return operationIOSummary(operation.Out, map[string]string{
		"commit_summary": commitSummary,
	})
}

func (e builtinOperationExecutor) finalize(ctx context.Context, state *operationExecution, operation OperationSpec, name string) error {
	input := finalizeInputFromOperation(state, operation)
	if strings.TrimSpace(input.result.Status) == "" {
		input.result = LaunchResult{
			Status:  "completed",
			Summary: fmt.Sprintf("action=%s class=%s operations=completed", input.actionName, input.actionClass),
		}
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
	actionName  string
	actionClass string
	invocation  invocation
	profile     profile
	allocation  allocation
	workplace   workplace
	result      LaunchResult
}

func finalizeInputFromOperation(state *operationExecution, operation OperationSpec) finalizeInput {
	input := finalizeInput{}
	if len(operation.In) == 0 {
		return input
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
	if mapping, ok := operation.In["result_status"]; ok {
		if value, ok := finalizeResultStringValue(state, mapping); ok {
			input.result.Status = value
		}
	}
	if mapping, ok := operation.In["result_summary"]; ok {
		if value, ok := finalizeResultStringValue(state, mapping); ok {
			input.result.Summary = value
		}
	}
	if mapping, ok := operation.In["structured_output"]; ok {
		if value, ok := resultValueFromParseResultMapping(state, mapping); ok {
			input.result.StructuredOutput = value.StructuredOutput
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
	return input
}

func finalizeResultStringValue(state *operationExecution, mapping model.OperationMapping) (string, bool) {
	if len(mapping.Value) != 0 {
		var value string
		if json.Unmarshal(mapping.Value, &value) == nil {
			return strings.TrimSpace(value), true
		}
		return "", false
	}
	if state == nil {
		return "", false
	}
	value, ok := state.data["result"].(LaunchResult)
	if !ok {
		return "", false
	}
	switch strings.TrimSpace(mapping.Ref) {
	case "data.result.status":
		return strings.TrimSpace(value.Status), true
	case "data.result.summary":
		return strings.TrimSpace(value.Summary), true
	default:
		return "", false
	}
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
		"allocation":   allocationSummary(input.allocation),
		"action_name":  strings.TrimSpace(input.actionName),
		"action_class": strings.TrimSpace(input.actionClass),
		"invocation":   invocationSummary(input.invocation),
		"profile":      profileSummary(input.profile),
		"result":       resultSummary(input.result),
		"workplace":    workplaceSummary(input.workplace),
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
	return err
}

type unsupportedInput struct {
	invocation invocation
	profile    profile
	allocation allocation
	workplace  workplace
}

func unsupportedInputFromOperation(state *operationExecution, operation OperationSpec) unsupportedInput {
	input := unsupportedInput{}
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
	return input
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
