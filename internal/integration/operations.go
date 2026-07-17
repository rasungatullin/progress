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

// OperationDescriptor возвращает описание операции для явно выбранной
// системы либо для системы по умолчанию соответствующего типа.
func (s *Service) OperationDescriptor(ctx context.Context, name, system string) (OperationDescriptor, bool) {
	filter := OperationFilter{Name: strings.TrimSpace(strings.ToLower(name)), System: strings.TrimSpace(strings.ToLower(system))}
	if filter.System == "" {
		integrationType, _, _ := parseOperationName(filter.Name)
		if integrationType == "" {
			return OperationDescriptor{}, false
		}
		defaultSystem := s.defaultSystemForType(integrationType)
		if defaultSystem == "" {
			return OperationDescriptor{}, false
		}
		filter.System = defaultSystem
	}
	descriptors := s.Operations(ctx, filter)
	if len(descriptors) == 0 {
		return OperationDescriptor{}, false
	}
	return descriptors[0], true
}

// OperationRoute возвращает маршрут каталога без выполнения операции. Он
// используется диагностическими командами, которым нужно представить тот
// же нормализованный маршрут при отказе до вызова провайдера.
func (s *Service) OperationRoute(ctx context.Context, name, system string) (Route, bool) {
	descriptor, ok := s.OperationDescriptor(ctx, name, system)
	if !ok {
		return Route{}, false
	}
	objectType, operation := descriptor.ObjectType, descriptor.Operation
	switch descriptor.Name {
	case "issue.issue.comment.list":
		objectType, operation = "issue", "comments"
	case "issue.issue.comment.create":
		objectType = "comment"
	case "issue.issue.label.add", "issue.issue.label.remove":
		objectType = "label"
	case "repo.merge-request.comment.list", "repo.merge-request.comment.create":
		objectType = "merge-request-comment"
	case "repo.review-remark.create":
		objectType, operation = "review-remark", "create"
	}
	route, err := s.resolveRoute(Request{
		IntegrationType: descriptor.IntegrationType,
		System:          descriptor.System,
		SystemProvided:  true,
		Resource:        objectType,
		ObjectType:      objectType,
		Operation:       operation,
	})
	if err != nil {
		return route, false
	}
	return route, true
}

// IsSystemConfigured сообщает, существует ли система в загруженной
// конфигурации или среди зарегистрированных провайдеров.
func (s *Service) IsSystemConfigured(system string) bool {
	_, ok := s.systems[normalizeSystem(system)]
	return ok
}

// DefaultSystemForType возвращает систему по умолчанию для предметного типа.
func (s *Service) DefaultSystemForType(integrationType string) string {
	return s.defaultSystemForType(integrationType)
}

func (s *Service) operationDescriptorsForSystem(state systemState) []OperationDescriptor {
	var result []OperationDescriptor
	for _, template := range builtinOperationTemplates(state.Type) {
		if !systemSupportsIntegrationType(state, template.IntegrationType) {
			continue
		}
		descriptor := OperationDescriptor{
			Name:            canonicalConfiguredOperationName(template.Name),
			IntegrationType: template.IntegrationType,
			System:          state.Name,
			AdapterType:     state.Type,
			ObjectType:      operationObjectPath(template.Name),
			Operation:       operationAction(template.Name),
			Enabled:         state.Enabled,
			Available:       state.Enabled && state.Registered,
			SideEffect:      template.SideEffect,
			DryRunSupported: template.DryRunSupported,
			Input:           template.Input,
			Output:          template.Output,
			FailureKinds:    append([]string(nil), template.FailureKinds...),
		}
		if bitbucketServerState(state) && (descriptor.Name == "repo.merge-request.comment.create" || descriptor.Name == "repo.review-remark.create") {
			descriptor.Available = false
		}
		descriptor.Diagnostics = operationDiagnostics(state, descriptor.Available)
		if bitbucketServerState(state) && (descriptor.Name == "repo.merge-request.comment.create" || descriptor.Name == "repo.review-remark.create") {
			descriptor.Diagnostics = append(descriptor.Diagnostics, "Bitbucket Server does not support pull request comment or inline remark creation")
		}
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

func bitbucketServerState(state systemState) bool {
	if state.Type != "bitbucket" {
		return false
	}
	switch normalizeSystem(state.APIVariant) {
	case "server", "bitbucket-server", "data-center", "datacenter", "stash":
		return true
	}
	base := strings.TrimRight(strings.ToLower(strings.TrimSpace(state.BaseURL)), "/")
	return strings.Contains(base, "/rest/api/") || strings.Contains(base, "://stash.") || strings.Contains(base, ".stash.")
}

func builtinOperationTemplates(adapterType string) []operationTemplate {
	switch normalizeSystem(adapterType) {
	case "github":
		return []operationTemplate{
			trackerTaskGetOperation(),
			trackerTaskSearchOperation(true),
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
			reviewRemarkCreateOperation(),
			reviewRemarkListOperation(),
			reviewRemarkReplyOperation(),
			reviewRemarkResolveOperation(),
			reviewRemarkUnresolveOperation(),
		}
	case "bitbucket":
		return []operationTemplate{
			repositoryGetOperation(),
			mergeRequestGetOperation(),
			mergeRequestSearchOperation(),
			mergeRequestCreateOperation(),
			mergeRequestCommentListOperation(),
			mergeRequestCommentCreateOperation(),
			reviewRemarkCreateOperation(),
			reviewRemarkListOperation(),
		}
	case "mattermost":
		return []operationTemplate{
			messengerThreadGetOperation(),
			messengerMessageListOperation(),
			messengerMessageCreateOperation(),
		}
	case "telegram":
		return []operationTemplate{
			messengerMessageListOperation(),
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
			trackerTaskSearchOperation(false),
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
	canonicalName := canonicalConfiguredOperationName(name)

	required := operationFields(operation.Required, operation.Defaults)
	optional := operationFields(operation.Optional, operation.Defaults)
	if integrationType == model.IntegrationTypeIssue {
		required = normalizeIssueOperationFields(required)
		optional = normalizeIssueOperationFields(optional)
	}

	available := state.Enabled && state.Registered
	unsupportedIntegrationType := !systemSupportsIntegrationType(state, integrationType)
	if unsupportedIntegrationType {
		available = false
	}
	missingScriptExecutable := state.Type == "script" && !scriptOperationHasExecutable(operation)
	if missingScriptExecutable {
		available = false
	}
	descriptor := OperationDescriptor{
		Name:            canonicalName,
		IntegrationType: integrationType,
		System:          state.Name,
		AdapterType:     state.Type,
		ObjectType:      operationObjectPath(canonicalName),
		Operation:       operationAction(canonicalName),
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
	if unsupportedIntegrationType {
		descriptor.Diagnostics = append(descriptor.Diagnostics, "system does not support integration type="+integrationType)
	}
	if operation.Script != "" {
		descriptor.Diagnostics = append(descriptor.Diagnostics, "script="+strings.TrimSpace(operation.Script))
	}
	return descriptor
}

func normalizeIssueOperationFields(fields []model.OperationField) []model.OperationField {
	for i := range fields {
		if fields[i].Name == "number" {
			fields[i].Name = "id"
			fields[i].Type = "string"
		}
	}
	return fields
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
	objectType := strings.Join(parts[1:len(parts)-1], ".")
	objectType = normalizeObjectType(objectType)
	// Каноническое имя операции допускает вложенные объектные пространства,
	// тогда как реестр сопоставляет их с единым объектом адаптера.
	switch objectType {
	case "issue.comment", "issue-comment":
		objectType = "comment"
	case "issue.label", "issue-label":
		objectType = "label"
	case "merge-request.comment", "merge-request-comment":
		objectType = "comment"
	}
	if integrationType == model.IntegrationTypeIssue && objectType == "task" {
		objectType = "issue"
	}
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
		if isRepeatedOperationField(name) {
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
	case "labels", "exclude_labels", "fields":
		return "string[]"
	default:
		return "string"
	}
}

func operationOutputShape(integrationType string, objectType string, operation string) string {
	objectType = canonicalObjectType(objectType)
	switch normalizeIntegrationType(integrationType) {
	case model.IntegrationTypeTracker:
		switch objectType {
		case "issue":
			if operation == "search" || operation == "list" {
				return "TrackerSearchResult[]"
			}
			return "CanonicalTask"
		case "issue-comment", "comment":
			if operation == "create" {
				return "OperationResult"
			}
			return "TaskComment[]"
		case "issue-label", "label":
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
		case "merge-request-comment", "review-remark", "comment":
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

func canonicalConfiguredOperationName(name string) string {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(name)), ".")
	if len(parts) < 3 {
		return ""
	}
	parts[0] = normalizeIntegrationType(parts[0])
	if parts[0] == model.IntegrationTypeIssue && parts[1] == "task" {
		parts[1] = "issue"
	}
	parts[len(parts)-1] = normalizeOperation(parts[len(parts)-1])
	if parts[len(parts)-1] == "comments" {
		parts[len(parts)-1] = "list"
	}
	return strings.Join(parts, ".")
}

func operationObjectPath(name string) string {
	parts := strings.Split(canonicalConfiguredOperationName(name), ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[1:len(parts)-1], ".")
}

func operationAction(name string) string {
	parts := strings.Split(canonicalConfiguredOperationName(name), ".")
	if len(parts) < 3 {
		return ""
	}
	return parts[len(parts)-1]
}

func canonicalObjectType(objectType string) string {
	objectType = normalizeObjectType(objectType)
	switch objectType {
	case "task":
		return "issue"
	case "issue-comment", "merge-request-comment", "review-remark":
		return "comment"
	default:
		return objectType
	}
}

func normalizeOperationFilter(filter OperationFilter) OperationFilter {
	filter.System = normalizeSystem(filter.System)
	filter.IntegrationType = normalizeIntegrationType(filter.IntegrationType)
	filter.Name = strings.TrimSpace(strings.ToLower(filter.Name))
	filter.Name = strings.Replace(filter.Name, "tracker.", "issue.", 1)
	filter.Name = strings.Replace(filter.Name, "repository.", "repo.", 1)
	filter.Name = strings.Replace(filter.Name, "issue.task.", "issue.issue.", 1)
	filter.Name = canonicalConfiguredOperationName(filter.Name)
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
		Name:            "issue.issue.get",
		IntegrationType: model.IntegrationTypeIssue,
		ObjectType:      "issue",
		Operation:       "get",
		Input:           input(requiredField("id", "string"), optionalFields("repository", "fields")...),
		Output:          output("task", "CanonicalTask"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "issue.issue.create",
		IntegrationType: model.IntegrationTypeIssue,
		ObjectType:      "issue",
		Operation:       "create",
		SideEffect:      true,
		Input:           input(requiredField("title", "string"), optionalFields("body", "state", "external_id", "labels")...),
		Output:          output("task", "CanonicalTask"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskSearchOperation(includeExcludeLabels bool) operationTemplate {
	optional := optionalFields("query", "state", "labels", "limit")
	if includeExcludeLabels {
		optional = append(optional, optionalFields("exclude_labels")...)
	}
	return operationTemplate{
		Name:            "issue.issue.search",
		IntegrationType: model.IntegrationTypeIssue,
		ObjectType:      "issue",
		Operation:       "search",
		Input:           model.OperationInputContract{Optional: optional},
		Output:          output("tracker-search-result", "TrackerSearchResult[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskUpdateOperation() operationTemplate {
	return operationTemplate{
		Name:            "issue.issue.update",
		IntegrationType: model.IntegrationTypeIssue,
		ObjectType:      "issue",
		Operation:       "update",
		SideEffect:      true,
		Input:           input(requiredField("id", "string"), optionalFields("title", "body", "state", "labels")...),
		Output:          output("task", "CanonicalTask"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskCommentListOperation() operationTemplate {
	return operationTemplate{
		Name:            "issue.issue.comment.list",
		IntegrationType: model.IntegrationTypeIssue,
		ObjectType:      "issue",
		Operation:       "comments",
		Input:           input(requiredField("id", "string"), optionalFields("repository")...),
		Output:          output("task-comment", "TaskComment[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskCommentCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "issue.issue.comment.create",
		IntegrationType: model.IntegrationTypeIssue,
		ObjectType:      "comment",
		Operation:       "create",
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("id", "string"), requiredField("body", "string")}, optionalFields("repository")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func trackerTaskLabelAddOperation() operationTemplate {
	return trackerTaskLabelOperation("issue.issue.label.add", "add")
}

func trackerTaskLabelRemoveOperation() operationTemplate {
	return trackerTaskLabelOperation("issue.issue.label.remove", "remove")
}

func trackerTaskLabelOperation(name string, operation string) operationTemplate {
	return operationTemplate{
		Name:            name,
		IntegrationType: model.IntegrationTypeTracker,
		ObjectType:      "task-label",
		Operation:       operation,
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("id", "string"), requiredRepeatedField("labels", "string[]")}, optionalFields("repository")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func repositoryGetOperation() operationTemplate {
	return operationTemplate{
		Name:            "repo.repo.get",
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
		Name:            "repo.merge-request.get",
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
		Name:            "repo.merge-request.search",
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
		Name:            "repo.merge-request.create",
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
		Name:            "repo.merge-request.comment.list",
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
		Name:            "repo.merge-request.comment.create",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "merge-request-comment",
		Operation:       "create",
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("number", "integer"), requiredField("body", "string")}, optionalFields("repository")...),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func reviewRemarkListOperation() operationTemplate {
	return operationTemplate{
		Name:            "repo.review-remark.list",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "review-remark",
		Operation:       "list",
		Input:           input(requiredField("number", "integer"), optionalField("repository", "string")),
		Output:          output("review-remark", "ReviewRemark[]"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func reviewRemarkCreateOperation() operationTemplate {
	return operationTemplate{
		Name:            "repo.review-remark.create",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "review-remark",
		Operation:       "create",
		SideEffect:      true,
		Input: inputMany([]model.OperationField{
			requiredField("number", "integer"),
			requiredField("body", "string"),
			requiredField("path", "string"),
			requiredField("line", "integer"),
		}, optionalFields("repository", "side")...),
		Output:       output("operation-result", "OperationResult"),
		FailureKinds: defaultFailureKinds(),
	}
}

func reviewRemarkReplyOperation() operationTemplate {
	return operationTemplate{
		Name:            "repo.comment.reply",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "comment",
		Operation:       "reply",
		SideEffect:      true,
		Input:           inputMany([]model.OperationField{requiredField("thread", "string"), requiredField("body", "string")}),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func reviewRemarkResolveOperation() operationTemplate {
	return operationTemplate{
		Name:            "repo.comment.resolve",
		IntegrationType: model.IntegrationTypeRepository,
		ObjectType:      "comment",
		Operation:       "resolve",
		SideEffect:      true,
		Input:           input(requiredField("thread", "string")),
		Output:          output("operation-result", "OperationResult"),
		FailureKinds:    defaultFailureKinds(),
	}
}

func reviewRemarkUnresolveOperation() operationTemplate {
	return operationTemplate{
		Name: "repo.review-remark.unresolve", IntegrationType: model.IntegrationTypeRepository, ObjectType: "review-remark", Operation: "unresolve", SideEffect: true,
		Input: input(requiredField("thread", "string")), Output: output("operation-result", "OperationResult"), FailureKinds: defaultFailureKinds(),
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

func messengerMessageListOperation() operationTemplate {
	return operationTemplate{
		Name: "messenger.message.list", IntegrationType: model.IntegrationTypeMessenger, ObjectType: "message", Operation: "list",
		Input:  input(optionalField("channel", "string"), optionalField("limit", "integer"), optionalField("cursor", "string"), optionalField("direction", "string"), optionalField("order", "string"), optionalField("from", "string"), optionalField("to", "string"), optionalField("include-replies", "boolean")),
		Output: output("messages", "[]Message"), FailureKinds: defaultFailureKinds(),
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
		fields = append(fields, model.OperationField{Name: name, Type: operationFieldType(name), Repeated: isRepeatedOperationField(name)})
	}
	return fields
}

func isRepeatedOperationField(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "fields", "labels", "exclude_labels":
		return true
	default:
		return false
	}
}

func output(resource string, shape string) model.OperationOutputContract {
	return model.OperationOutputContract{Resource: resource, Shape: shape}
}

func isSideEffectOperation(operation string) bool {
	switch normalizeOperation(operation) {
	case "create", "update", "add", "remove", "reply", "resolve", "delete":
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
