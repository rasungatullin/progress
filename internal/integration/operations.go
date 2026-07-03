package integration

import (
	"context"
	"sort"
	"strings"

	"github.com/rasungatullin/progress/internal/integration/model"
)

type operationTemplate struct {
	Name            string
	IntegrationType string
	ObjectType      string
	Operation       string
	SideEffect      bool
	DryRunSupported bool
	Input           model.OperationInputContract
	Output          model.OperationOutputContract
	FailureKinds    []string
}

func (s *Service) Operations(_ context.Context, filter OperationFilter) []OperationDescriptor {
	filter = normalizeOperationFilter(filter)
	var result []OperationDescriptor

	for _, system := range sortedSystemNames(s.systems) {
		state := s.systems[system]
		if filter.System != "" && state.Name != filter.System {
			continue
		}

		descriptors := s.operationDescriptorsForSystem(state)
		for _, descriptor := range descriptors {
			if filter.IntegrationType != "" && descriptor.IntegrationType != filter.IntegrationType {
				continue
			}
			if filter.Name != "" && descriptor.Name != filter.Name {
				continue
			}
			result = append(result, descriptor)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].System == result[j].System {
			return result[i].Name < result[j].Name
		}
		return result[i].System < result[j].System
	})
	return result
}

func (s *Service) operationDescriptorsForSystem(state systemState) []OperationDescriptor {
	var result []OperationDescriptor
	for _, template := range builtinOperationTemplates(state.Type) {
		if !systemSupportsIntegrationType(state, template.IntegrationType) {
			continue
		}
		descriptor := OperationDescriptor{
			Name:            template.Name,
			IntegrationType: template.IntegrationType,
			System:          state.Name,
			AdapterType:     state.Type,
			ObjectType:      template.ObjectType,
			Operation:       template.Operation,
			Enabled:         state.Enabled,
			Available:       state.Enabled && state.Registered,
			SideEffect:      template.SideEffect,
			DryRunSupported: template.DryRunSupported,
			Input:           template.Input,
			Output:          template.Output,
			FailureKinds:    append([]string(nil), template.FailureKinds...),
		}
		descriptor.Diagnostics = operationDiagnostics(state, descriptor.Available)
		result = append(result, descriptor)
	}

	for _, name := range sortedOperationConfigNames(state.Operations) {
		descriptor := operationDescriptorFromConfig(state, name, state.Operations[name])
		if descriptor.Name == "" {
			continue
		}
		result = append(result, descriptor)
	}

	return result
}

func builtinOperationTemplates(adapterType string) []operationTemplate {
	switch normalizeSystem(adapterType) {
	case "github":
		return []operationTemplate{
			trackerTaskGetOperation(),
			trackerTaskCommentListOperation(),
			trackerTaskCommentCreateOperation(),
			trackerTaskLabelAddOperation(),
			trackerTaskLabelRemoveOperation(),
			repositoryGetOperation(),
			mergeRequestGetOperation(),
			mergeRequestSearchOperation(),
			mergeRequestCreateOperation(),
			mergeRequestCommentListOperation(),
			mergeRequestCommentCreateOperation(),
			reviewRemarkListOperation(),
			reviewRemarkResolveOperation(),
		}
	case "bitbucket":
		return []operationTemplate{
			repositoryGetOperation(),
			mergeRequestGetOperation(),
			mergeRequestSearchOperation(),
			mergeRequestCreateOperation(),
			mergeRequestCommentListOperation(),
			mergeRequestCommentCreateOperation(),
		}
	case "mattermost":
		return []operationTemplate{
			messengerThreadGetOperation(),
			messengerMessageCreateOperation(),
		}
	case "telegram":
		return []operationTemplate{
			messengerMessageCreateOperation(),
		}
	case "confluence":
		return []operationTemplate{
			wikiPageGetOperation(),
			wikiPageSearchOperation(),
		}
	case "local-tracker":
		return []operationTemplate{
			trackerTaskCreateOperation(),
			trackerTaskGetOperation(),
			trackerTaskSearchOperation(),
			trackerTaskUpdateOperation(),
			trackerTaskCommentListOperation(),
			trackerTaskCommentCreateOperation(),
			trackerTaskLabelAddOperation(),
			trackerTaskLabelRemoveOperation(),
		}
	default:
		return nil
	}
}

func operationDescriptorFromConfig(state systemState, name string, operation model.IntegrationOperationConfig) OperationDescriptor {
	integrationType, objectType, action := parseOperationName(name)
	if integrationType == "" {
		return OperationDescriptor{}
	}

	required := operationFields(operation.Required, operation.Defaults)
	optional := operationFields(operation.Optional, operation.Defaults)

	available := state.Enabled && state.Registered
	missingScriptExecutable := state.Type == "script" && !scriptOperationHasExecutable(operation)
	if missingScriptExecutable {
		available = false
	}
	descriptor := OperationDescriptor{
		Name:            name,
		IntegrationType: integrationType,
		System:          state.Name,
		AdapterType:     state.Type,
		ObjectType:      objectType,
		Operation:       action,
		Enabled:         state.Enabled,
		Available:       available,
		SideEffect:      isSideEffectOperation(action),
		Input:           model.OperationInputContract{Required: required, Optional: optional},
		Output:          model.OperationOutputContract{Resource: objectType, Shape: operationOutputShape(integrationType, objectType, action)},
		FailureKinds:    defaultFailureKinds(),
		Diagnostics:     operationDiagnostics(state, available),
	}
	if missingScriptExecutable {
		descriptor.Diagnostics = append(descriptor.Diagnostics, "script operation has no script, command or path")
	}
	if operation.Script != "" {
		descriptor.Diagnostics = append(descriptor.Diagnostics, "script="+strings.TrimSpace(operation.Script))
	}
	return descriptor
}

func scriptOperationHasExecutable(operation model.IntegrationOperationConfig) bool {
	return strings.TrimSpace(firstNonEmpty(operation.Script, operation.Command, operation.Path)) != ""
}

func operationDiagnostics(state systemState, available bool) []string {
	if available {
		return []string{"operation available through registered provider"}
	}
	if !state.Enabled {
		return []string{"system disabled by integration configuration"}
	}
	if !state.Registered {
		return []string{"provider not registered in current build"}
	}
	return []string{"operation is not available"}
}

func parseOperationName(name string) (string, string, string) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(name)), ".")
	if len(parts) < 3 {
		return "", "", ""
	}
	integrationType := normalizeIntegrationType(parts[0])
	operation := normalizeOperation(parts[len(parts)-1])
	if operation == "comments" {
		operation = "list"
	}
	objectType := strings.Join(parts[1:len(parts)-1], "-")
	objectType = normalizeObjectType(objectType)
	return integrationType, objectType, operation
}

func operationFields(names []string, defaults map[string]string) []model.OperationField {
	if len(names) == 0 {
		return nil
	}
	result := make([]model.OperationField, 0, len(names))
	for _, name := range trimStrings(names) {
		field := model.OperationField{Name: name, Type: operationFieldType(name)}
		if defaults != nil {
			field.Default = strings.TrimSpace(defaults[name])
		}
		if name == "labels" || name == "fields" {
			field.Repeated = true
		}
		result = append(result, field)
	}
	return result
}

func operationFieldType(name string) string {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "number", "line", "limit":
		return "integer"
	case "draft", "dry_run":
		return "boolean"
	case "labels", "fields":
		return "string[]"
	default:
		return "string"
	}
}

func operationOutputShape(integrationType string, objectType string, operation string) string {
	switch normalizeIntegrationType(integrationType) {
	case model.IntegrationTypeTracker:
		switch objectType {
		case "task":
			if operation == "search" || operation == "list" {
				return "TrackerSearchResult[]"
			}
			return "CanonicalTask"
		case "task-comment", "comment":
			if operation == "create" {
				return "OperationResult"
			}
			return "TaskComment[]"
		case "task-label", "label":
			return "OperationResult"
		}
	case model.IntegrationTypeRepository:
		switch objectType {
		case "repository":
			return "Repository"
		case "merge-request":
			if operation == "search" || operation == "list" {
				return "MergeRequest[]"
			}
			if operation == "create" {
				return "OperationResult"
			}
			return "MergeRequest"
		case "merge-request-comment", "review-remark":
			if isSideEffectOperation(operation) {
				return "OperationResult"
			}
			return "ReviewRemark[]"
		}
	case model.IntegrationTypeMessenger:
		switch objectType {
		case "thread":
			return "MessageThread"
		case "message":
			if operation == "create" {
				return "Message"
			}
			return "Message[]"
		}
	case model.IntegrationTypeWiki:
		if objectType == "page" {
			if operation == "search" || operation == "list" {
				return "WikiPage[]"
			}
			return "WikiPage"
		}
	}
	return "Response"
}

func normalizeOperationFilter(filter OperationFilter) OperationFilter {
	filter.System = normalizeSystem(filter.System)
	filter.IntegrationType = normalizeIntegrationType(filter.IntegrationType)
	filter.Name = strings.TrimSpace(strings.ToLower(filter.Name))
	return filter
}

func sortedSystemNames(systems map[string]systemState) []string {
	names := make([]string, 0, len(systems))
	for name := range systems {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedOperationConfigNames(operations map[string]model.IntegrationOperationConfig) []string {
	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func trackerTaskGetOperation() operationTemplate {
	return operationTemplate{
		Name:            "tracker.task.get",
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task",
		Operation:       "get",
		Input:           input(requiredField("number", "integer"), optionalFields("repository", "fields")...),
		Output:          output("task", "CanonicalTask"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "tracker.task.create",
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task",
		Operation:       "create",
		SideEffect:      true,
		Input:           input(requiredField("title", "string"), optionalFields("body", "state", "external_id", "labels")...),
		Output:          output("task", "CanonicalTask"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskSearchOperation() operationTemplate {
	return operationTemplate{
		Name:            "tracker.task.search",
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task",
		Operation:       "search",
		Input:           model.OperationInputContract{Optional: optionalFields("query", "state", "labels", "limit")},
		Output:          output("tracker-search-result", "TrackerSearchResult[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskUpdateOperation() operationTemplate {
	return operationTemplate{
		Name:            "tracker.task.update",
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task",
		Operation:       "update",
		SideEffect:      true,
		Input:           input(requiredField("number", "integer"), optionalFields("title", "body", "state", "labels")...),
		Output:          output("task", "CanonicalTask"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskCommentListOperation() operationTemplate {
	return operationTemplate{
		Name:            "tracker.task.comment.list",
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task",
		Operation:       "comments",
		Input:           input(requiredField("number", "integer"), optionalFields("repository")...),
		Output:          output("task-comment", "TaskComment[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskCommentCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "tracker.task.comment.create",
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task-comment",
		Operation:       "create",
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("number", "integer"), requiredField("body", "string")}, optionalFields("repository")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskLabelAddOperation() operationTemplate {
	return trackerTaskLabelOperation("tracker.task.label.add", "add")
}

func trackerTaskLabelRemoveOperation() operationTemplate {
	return trackerTaskLabelOperation("tracker.task.label.remove", "remove")
}

func trackerTaskLabelOperation(name string, operation string) operationTemplate {
	return operationTemplate{
		Name:            name,
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task-label",
		Operation:       operation,
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("number", "integer"), requiredRepeatedField("labels", "string[]")}, optionalFields("repository")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func repositoryGetOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.repo.get",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "repository",
		Operation:       "get",
		Input:           model.OperationInputContract{Optional: optionalFields("repository")},
		Output:          output("repository", "Repository"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func mergeRequestGetOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.merge-request.get",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "merge-request",
		Operation:       "get",
		Input:           input(requiredField("number", "integer"), optionalField("repository", "string")),
		Output:          output("merge-request", "MergeRequest"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func mergeRequestSearchOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.merge-request.search",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "merge-request",
		Operation:       "search",
		Input:           model.OperationInputContract{Optional: optionalFields("repository", "query", "state", "scope", "limit")},
		Output:          output("merge-request", "MergeRequest[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func mergeRequestCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.merge-request.create",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "merge-request",
		Operation:       "create",
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("repository", "string"), requiredField("base", "string"), requiredField("head", "string"), requiredField("title", "string")}, optionalFields("body", "draft")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func mergeRequestCommentListOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.merge-request.comment.list",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "merge-request-comment",
		Operation:       "list",
		Input:           input(requiredField("number", "integer"), optionalField("repository", "string")),
		Output:          output("review-remark", "ReviewRemark[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func mergeRequestCommentCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.merge-request.comment.create",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "merge-request-comment",
		Operation:       "create",
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("number", "integer"), requiredField("body", "string")}, optionalFields("repository", "path", "line", "side")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func reviewRemarkListOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.review-remark.list",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "review-remark",
		Operation:       "list",
		Input:           input(requiredField("number", "integer"), optionalField("repository", "string")),
		Output:          output("review-remark", "ReviewRemark[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func reviewRemarkResolveOperation() operationTemplate {
	return operationTemplate{
		Name:            "repository.review-remark.resolve",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "review-remark",
		Operation:       "resolve",
		SideEffect:      true,
		Input:           input(requiredField("thread", "string")),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func messengerThreadGetOperation() operationTemplate {
	return operationTemplate{
		Name:            "messenger.thread.get",
		IntegrationType: model.IntegrationTypeMessenger,
		ObjectType:      "thread",
		Operation:       "get",
		Input:           input(requiredField("thread", "string")),
		Output:          output("thread", "MessageThread"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func messengerMessageCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "messenger.message.create",
		IntegrationType: model.IntegrationTypeMessenger,
		ObjectType:      "message",
		Operation:       "create",
		SideEffect:      true,
		Input:           input(requiredField("text", "string"), optionalFields("channel", "thread", "message")...),
		Output:          output("message", "Message"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func wikiPageGetOperation() operationTemplate {
	return operationTemplate{
		Name:            "wiki.page.get",
		IntegrationType: model.IntegrationTypeWiki,
		ObjectType:      "page",
		Operation:       "get",
		Input:           input(requiredField("id", "string")),
		Output:          output("page", "WikiPage"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func wikiPageSearchOperation() operationTemplate {
	return operationTemplate{
		Name:            "wiki.page.search",
		IntegrationType: model.IntegrationTypeWiki,
		ObjectType:      "page",
		Operation:       "search",
		Input:           input(requiredField("query", "string"), optionalField("limit", "integer")),
		Output:          output("page", "WikiPage[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func input(required model.OperationField, optional ...model.OperationField) model.OperationInputContract {
	return model.OperationInputContract{Required: []model.OperationField{required}, Optional: optional}
}

func inputMany(required []model.OperationField, optional ...model.OperationField) model.OperationInputContract {
	return model.OperationInputContract{Required: required, Optional: optional}
}

func requiredField(name string, fieldType string) model.OperationField {
	return model.OperationField{Name: name, Type: fieldType}
}

func requiredRepeatedField(name string, fieldType string) model.OperationField {
	return model.OperationField{Name: name, Type: fieldType, Repeated: true}
}

func optionalField(name string, fieldType string) model.OperationField {
	return model.OperationField{Name: name, Type: fieldType}
}

func optionalFields(names ...string) []model.OperationField {
	fields := make([]model.OperationField, 0, len(names))
	for _, name := range names {
		fields = append(fields, model.OperationField{Name: name, Type: operationFieldType(name), Repeated: name == "fields" || name == "labels"})
	}
	return fields
}

func output(resource string, shape string) model.OperationOutputContract {
	return model.OperationOutputContract{Resource: resource, Shape: shape}
}

func isSideEffectOperation(operation string) bool {
	switch normalizeOperation(operation) {
	case "create", "update", "add", "remove", "resolve", "delete":
		return true
	default:
		return false
	}
}

func defaultFailureKinds() []string {
	return []string{
		model.FailureKindInvalidRequest,
		model.FailureKindAuthRequired,
		model.FailureKindPermissionDenied,
		model.FailureKindNotFound,
		model.FailureKindTemporaryUnavailable,
		model.FailureKindRateLimited,
		model.FailureKindTimeout,
		model.FailureKindUnsupportedOperation,
		model.FailureKindInternalIntegration,
		model.FailureKindExternalFailure,
	}
}
