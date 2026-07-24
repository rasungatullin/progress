package execution

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/rasungatullin/progress/internal/integration"
)

func (e builtinOperationExecutor) executeIntegration(ctx context.Context, state *operationExecution, operation OperationSpec) error {
	name := operationResultName(operation)
	executor, err := e.integrationExecutor()
	if err != nil {
		return e.failIntegrationOperation(state, operation, name, "Контур интеграции недоступен.", err, "integration_unavailable")
	}

	operationName, err := integrationOperationName(operation.Kind)
	if err != nil {
		return e.failIntegrationOperation(state, operation, name, "Ссылка на интеграционную операцию не распознана.", err, "integration_operation_invalid")
	}

	// Разрешение выполняется через каталог контура интеграции. Старые тестовые
	// исполнители без каталога сохраняют возможность проверить сборку запроса;
	// реальный Service всегда публикует каталог.
	var descriptor integration.OperationDescriptor
	if catalog, ok := executor.(integrationOperationDescriptor); ok {
		var found bool
		descriptor, found = catalog.OperationDescriptor(ctx, operationName, "")
		if !found {
			err := fmt.Errorf("integration operation %q is not available", operationName)
			return e.failIntegrationOperation(state, operation, name, "Интеграционная операция не разрешена.", err, "integration_operation_unavailable")
		}
	} else if catalog, ok := executor.(integrationOperationCatalog); ok {
		descriptors := catalog.Operations(ctx, integration.OperationFilter{Name: operationName})
		if len(descriptors) == 0 {
			err := fmt.Errorf("integration operation %q is not available", operationName)
			return e.failIntegrationOperation(state, operation, name, "Интеграционная операция не разрешена.", err, "integration_operation_unavailable")
		}
		descriptor = descriptors[0]
	}

	request := integration.Request{
		IntegrationType: descriptor.IntegrationType,
		System:          descriptor.System,
		SystemProvided:  descriptor.System != "",
		Resource:        descriptor.ObjectType,
		ObjectType:      descriptor.ObjectType,
		Operation:       descriptor.Operation,
	}
	if request.IntegrationType == "" || request.Operation == "" {
		request.IntegrationType, request.ObjectType, request.Operation = integrationOperationParts(operationName)
		request.Resource = request.ObjectType
	}
	if err := fillIntegrationRequest(state, operation, descriptor, &request); err != nil {
		return e.failIntegrationOperation(state, operation, name, "Вход интеграционной операции не прошёл проверку.", err, "integration_input_invalid")
	}

	response, err := executor.Execute(ctx, request)
	if err != nil && !integrationResponseAlreadyAvailable(operation, response) {
		return e.failIntegrationOperation(state, operation, name, "Интеграционная операция завершилась отказом.", err, "integration_operation_failed")
	}
	writeIntegrationResponse(state, operation, response)
	state.tracker.completeIO(name, operationIOSummary(operation.In, nil), operationIOSummary(operation.Out, nil), "Интеграционная операция выполнена через контур интеграции.")
	return nil
}

func integrationOperationName(kind OperationKind) (string, error) {
	parts := strings.Split(strings.TrimSpace(string(kind)), ".")
	if len(parts) < 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[len(parts)-1]) == "" {
		return "", fmt.Errorf("invalid integration operation kind %q", kind)
	}
	if parts[0] == "repository" {
		parts[0] = "repo"
	}
	return strings.Join(parts, "."), nil
}

func integrationOperationParts(name string) (string, string, string) {
	parts := strings.Split(name, ".")
	if len(parts) < 3 {
		return "", "", ""
	}
	return parts[0], strings.Join(parts[1:len(parts)-1], "."), parts[len(parts)-1]
}

func fillIntegrationRequest(state *operationExecution, operation OperationSpec, descriptor integration.OperationDescriptor, request *integration.Request) error {
	fields := append(append([]integration.OperationField(nil), descriptor.Input.Required...), descriptor.Input.Optional...)
	for _, field := range fields {
		mapping, ok := integrationInputMapping(operation.In, field.Name)
		if !ok {
			if containsOperationField(descriptor.Input.Required, field.Name) {
				return fmt.Errorf("required integration input %q is not mapped", field.Name)
			}
			continue
		}
		value, resolved := operationMappingRawValue(state, mapping)
		if !resolved && containsOperationField(descriptor.Input.Required, field.Name) {
			return fmt.Errorf("required integration input %q is not resolved", field.Name)
		}
		if resolved && !integrationValueMatchesField(value, field) {
			return fmt.Errorf("integration input %q has incompatible type", field.Name)
		}
		if resolved {
			setIntegrationRequestField(request, field.Name, value)
		}
	}
	for fieldName, mapping := range operation.In {
		if containsOperationField(fields, fieldName) {
			continue
		}
		if value, ok := operationMappingRawValue(state, mapping); ok {
			setIntegrationRequestField(request, fieldName, value)
		}
	}
	return nil
}

func containsOperationField(fields []integration.OperationField, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func integrationInputMapping(inputs model.OperationMap, name string) (model.OperationMapping, bool) {
	if mapping, ok := inputs[name]; ok {
		return mapping, true
	}
	aliases := map[string]string{"base": "base_ref", "head": "head_ref"}
	if alias, ok := aliases[name]; ok {
		mapping, mapped := inputs[alias]
		return mapping, mapped
	}
	return model.OperationMapping{}, false
}

func integrationValueMatchesField(value any, field integration.OperationField) bool {
	if value == nil {
		return !field.Repeated
	}
	if field.Repeated {
		return reflect.ValueOf(value).Kind() == reflect.Slice
	}
	switch field.Type {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		k := reflect.ValueOf(value).Kind()
		if k >= reflect.Int && k <= reflect.Int64 {
			return true
		}
		if k == reflect.Float64 {
			number := reflect.ValueOf(value).Float()
			return number == float64(int(number))
		}
		return false
	case "number":
		k := reflect.ValueOf(value).Kind()
		return k >= reflect.Int && k <= reflect.Float64
	default:
		return true
	}
}

func setIntegrationRequestField(request *integration.Request, name string, value any) {
	if name == "system" {
		request.System, _ = value.(string)
		request.SystemProvided = request.System != ""
		return
	}
	if name == "repository" {
		request.Repository, _ = value.(string)
		request.RepoProvided = request.Repository != ""
		return
	}
	fieldName := requestFieldName(name)
	target := reflect.ValueOf(request).Elem().FieldByName(fieldName)
	if target.IsValid() {
		if valueOf, ok := integrationValueForTarget(value, target.Type()); ok {
			target.Set(valueOf)
			return
		}
	}
	request.Extra = addIntegrationExtra(request.Extra, name, value)
}

func integrationValueForTarget(value any, targetType reflect.Type) (reflect.Value, bool) {
	if value == nil {
		return reflect.Value{}, false
	}
	valueOf := reflect.ValueOf(value)
	if valueOf.Type().AssignableTo(targetType) {
		return valueOf, true
	}
	if targetType.Kind() == reflect.Int && valueOf.Kind() == reflect.Float64 {
		number := valueOf.Float()
		if number == float64(int(number)) {
			return reflect.ValueOf(int(number)).Convert(targetType), true
		}
	}
	if targetType.Kind() == reflect.Slice && valueOf.Kind() == reflect.Slice {
		result := reflect.MakeSlice(targetType, valueOf.Len(), valueOf.Len())
		for index := 0; index < valueOf.Len(); index++ {
			item, ok := integrationValueForTarget(valueOf.Index(index).Interface(), targetType.Elem())
			if !ok {
				return reflect.Value{}, false
			}
			result.Index(index).Set(item)
		}
		return result, true
	}
	return reflect.Value{}, false
}

func requestFieldName(name string) string {
	aliases := map[string]string{"id": "ID", "number": "MergeRequestNumber", "external_id": "ExternalID", "base_ref": "Base", "head_ref": "Head", "merge_request_number": "MergeRequestNumber"}
	if alias, ok := aliases[name]; ok {
		return alias
	}
	result := ""
	for _, part := range strings.Split(name, "_") {
		if part != "" {
			result += strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return result
}

func addIntegrationExtra(extra map[string]any, name string, value any) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	if value != nil {
		extra[name] = value
	}
	return extra
}

func writeIntegrationResponse(state *operationExecution, operation OperationSpec, response integration.Response) {
	values := map[string]any{
		"response":         response,
		"task":             response.Task,
		"tasks":            response.Tasks,
		"task_comments":    response.TaskComments,
		"repository":       response.Repository,
		"merge_request":    response.MergeRequest,
		"merge_requests":   response.MergeRequests,
		"pull_request":     response.MergeRequest,
		"review_remarks":   response.ReviewRemarks,
		"operation_result": response.OperationResult,
		"failure":          response.Failure,
	}
	if integrationCreateProducesMergeRequest(operation, response) {
		mergeRequest := mergeRequestFromPublishResponse(response, pullRequestRefFromOperation(state, operation))
		values["merge_request"] = mergeRequest
		values["pull_request"] = mergeRequest
	} else if mergeRequest, ok := mergeRequestFromIntegrationResponse(response); ok {
		values["merge_request"] = mergeRequest
		values["pull_request"] = mergeRequest
	}
	mergeRequest, ok := values["merge_request"].(integration.MergeRequest)
	if !ok {
		if pointer, pointerOK := values["merge_request"].(*integration.MergeRequest); pointerOK && pointer != nil {
			mergeRequest, ok = *pointer, true
		}
	}
	if ok {
		current, exists := state.data["invocation"].(invocation)
		if !exists {
			if mapping, mapped := operation.In["invocation"]; mapped {
				current, exists = invocationValueFromLaunchSynthesisMapping(state, mapping)
			}
		}
		if !exists {
			current, exists = state.in, state != nil
		}
		if exists {
			values["invocation"] = invocationWithPullRequest(current, mergeRequest)
		}
	}
	for name, value := range response.Data {
		values[name] = value
	}
	if response.OperationResult != nil {
		values["publish_summary"] = fmt.Sprintf("external_id=%s url=%s status=%s", response.OperationResult.ExternalID, response.OperationResult.URL, response.OperationResult.Status)
		result := resultFromExecutionData(state)
		result.Summary = joinExecutionSummaries(result.Summary, values["publish_summary"].(string))
		values["result"] = result
	}
	for field := range operation.Out {
		if value, ok := values[field]; ok && value != nil {
			writeOperationData(state, operation.Out, field, value)
		}
	}
}

func integrationCreateProducesMergeRequest(operation OperationSpec, response integration.Response) bool {
	kind := strings.TrimSpace(string(operation.Kind))
	if !strings.HasSuffix(kind, ".create") {
		return false
	}
	if strings.Contains(kind, ".merge-request.") || strings.EqualFold(strings.TrimSpace(response.ObjectType), "merge-request") || strings.EqualFold(strings.TrimSpace(response.Resource), "merge-request") {
		return true
	}
	return response.OperationResult != nil && strings.EqualFold(strings.TrimSpace(response.OperationResult.ObjectType), "merge-request") ||
		pullRequestStatusAlreadyExists(response)
}

func integrationResponseAlreadyAvailable(operation OperationSpec, response integration.Response) bool {
	if !strings.HasSuffix(strings.TrimSpace(string(operation.Kind)), ".create") {
		return false
	}
	if response.MergeRequest != nil && (response.MergeRequest.Number > 0 || strings.TrimSpace(response.MergeRequest.URL) != "") {
		return true
	}
	if response.OperationResult != nil && strings.TrimSpace(response.OperationResult.URL) != "" {
		return true
	}
	// Старый статус остаётся только резервом для отказа already-exists:
	// адаптер пока не публикует для него канонический объект или OperationResult.
	return pullRequestStatusAlreadyExists(response)
}

func (e builtinOperationExecutor) failIntegrationOperation(state *operationExecution, operation OperationSpec, name, summary string, err error, code string) error {
	if !operation.Required {
		for field, mapping := range operation.Out {
			if field == "review_remarks" {
				writeOperationData(state, model.OperationMap{field: mapping}, field, []integration.ReviewRemark(nil))
			}
		}
		state.tracker.skip(name, joinExecutionSummaries(summary, strings.TrimSpace(err.Error())))
		return nil
	}
	result := resultFromExecutionData(state)
	if strings.TrimSpace(result.Status) == "" {
		result = failedStartResult(err)
	} else {
		result.Status = "failed"
		result.Summary = joinExecutionSummaries(result.Summary, strings.TrimSpace(err.Error()))
	}
	writeOperationData(state, operation.Out, "result", result)
	state.tracker.fail(name, summary, err, code, false, true)
	return err
}
