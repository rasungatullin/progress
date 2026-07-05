package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

type actionFlags struct {
	action              string
	inputFile           string
	task                string
	constraints         []string
	projectContexts     []string
	operationalContexts []string
	previousRunResults  []string
	reviewRemarks       []string
	reviewResponses     []string
	integrationActions  []string
	repository          string
	taskNumber          int
	prNumber            int
	baseRef             string
	headRef             string
	title               string
	body                string
	draft               bool
}

type operationFlags struct {
	actionFlags
	operation string
}

type executionCommandService interface {
	ExecuteAction(context.Context, execution.ActionInvocation) (execution.ExecutionResult, error)
	ExecuteOperation(context.Context, execution.OperationInvocation) (execution.OperationResult, error)
}

type executionServiceFactoryFunc func(*cobra.Command) executionCommandService

type executionServiceFactoryContextKey struct{}

var executionServiceFactory = func(cmd *cobra.Command) executionCommandService {
	return execution.NewService(logging.New(cmd.ErrOrStderr()))
}

func setExecutionServiceFactory(cmd *cobra.Command, factory executionServiceFactoryFunc) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cmd.SetContext(context.WithValue(ctx, executionServiceFactoryContextKey{}, factory))
}

func newExecutionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execution",
		Short: "Контур исполнения",
	}

	cmd.AddCommand(
		newExecutionActionCommand(),
		newExecutionOperationCommand(),
	)

	return cmd
}

func newExecutionActionCommand() *cobra.Command {
	flags := newActionFlags()

	cmd := &cobra.Command{
		Use:   "action",
		Short: "Вызов действия контура исполнения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			request, err := actionInvocationFromFlags(flags)
			if err != nil {
				return err
			}

			result, err := service.ExecuteAction(context.Background(), request)
			if err != nil {
				printExecutionResultOnError(cmd, result)
				return err
			}

			printExecutionResult(cmd, result)
			return nil
		},
	}

	bindActionFlags(cmd, flags)
	return cmd
}

func newExecutionOperationCommand() *cobra.Command {
	flags := newOperationFlags()

	cmd := &cobra.Command{
		Use:   "operation <operation>",
		Short: "Вызов операции контура исполнения",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			request, err := operationInvocationFromFlags(flags)
			if err != nil {
				return err
			}

			result, err := service.ExecuteOperation(context.Background(), request)
			if err != nil {
				printOperationResultOnError(cmd, result)
				return err
			}

			printOperationResult(cmd, result)
			return nil
		},
	}

	bindOperationFlags(cmd, flags)
	cmd.PreRunE = func(_ *cobra.Command, args []string) error {
		flags.operation = strings.TrimSpace(args[0])
		return nil
	}
	return cmd
}

func newExecutionService(cmd *cobra.Command) executionCommandService {
	if factory, ok := cmd.Context().Value(executionServiceFactoryContextKey{}).(executionServiceFactoryFunc); ok && factory != nil {
		return factory(cmd)
	}

	return executionServiceFactory(cmd)
}

func newActionFlags() *actionFlags {
	return &actionFlags{action: execution.ActionClassEngineeringSynthesis}
}

func newOperationFlags() *operationFlags {
	return &operationFlags{actionFlags: *newActionFlags()}
}

func bindActionFlags(cmd *cobra.Command, flags *actionFlags) {
	cmd.Flags().StringVar(&flags.action, "action", flags.action, "Действие контура исполнения")
	bindStructuredInputFlags(cmd, flags)
}

func bindOperationFlags(cmd *cobra.Command, flags *operationFlags) {
	bindActionFlags(cmd, &flags.actionFlags)
}

func bindStructuredInputFlags(cmd *cobra.Command, flags *actionFlags) {
	cmd.Flags().StringVar(&flags.inputFile, "input-file", "", "Путь к JSON-файлу структурированного ввода")
	cmd.Flags().StringVar(&flags.task, "task", "", "Текстовая постановка структурированного ввода")
	cmd.Flags().StringArrayVar(&flags.constraints, "constraint", nil, "Ограничение структурированного ввода, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.projectContexts, "project-context", nil, "JSON-объект для project_context, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.operationalContexts, "operational-context", nil, "JSON-объект для operational_context, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.previousRunResults, "previous-run-result", nil, "JSON-объект для previous_run_results, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.reviewRemarks, "review-remark", nil, "JSON-объект для review_remarks, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.reviewResponses, "review-response", nil, "JSON-объект для review_responses, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.integrationActions, "integration-action", nil, "JSON-объект для integration_actions, флаг можно повторять")
	cmd.Flags().StringVar(&flags.repository, "repository", "", "Репозиторий внешней системы в форме owner/name")
	cmd.Flags().IntVar(&flags.taskNumber, "task-number", 0, "Номер задачи для имени ветки и рабочего места")
	cmd.Flags().IntVar(&flags.prNumber, "pr-number", 0, "Номер запроса на слияние")
	cmd.Flags().StringVar(&flags.baseRef, "base", "", "Базовая ветка запроса на слияние")
	cmd.Flags().StringVar(&flags.headRef, "head", "", "Ветка с изменениями для запроса на слияние")
	cmd.Flags().StringVar(&flags.title, "title", "", "Заголовок задачи или запроса на слияние")
	cmd.Flags().StringVar(&flags.body, "body", "", "Описание задачи или запроса на слияние")
	cmd.Flags().BoolVar(&flags.draft, "draft", false, "Открыть запрос на слияние как черновик")
}

func actionInvocationFromFlags(flags *actionFlags) (execution.ActionInvocation, error) {
	input, err := structuredInputFromFlags(flags)
	if err != nil {
		return execution.ActionInvocation{}, err
	}
	if input != nil {
		input, err = launch.NormalizeStructuredInput(input)
		if err != nil {
			return execution.ActionInvocation{}, err
		}
	}

	return execution.ActionInvocation{
		Assignment: &execution.ExecutionAssignment{
			Action:          strings.TrimSpace(flags.action),
			CanonicalTask:   canonicalTaskFromFlags(flags),
			RelatedObjects:  relatedObjectsFromFlags(flags),
			StructuredInput: input,
		},
	}, nil
}

func canonicalTaskFromFlags(flags *actionFlags) *execution.ObjectRef {
	if flags == nil {
		return nil
	}
	if flags.taskNumber <= 0 && strings.TrimSpace(flags.repository) == "" && strings.TrimSpace(flags.title) == "" && strings.TrimSpace(flags.body) == "" {
		return nil
	}

	task := &execution.ObjectRef{
		Type:       "task",
		Repository: strings.TrimSpace(flags.repository),
		Number:     flags.taskNumber,
		Title:      strings.TrimSpace(flags.title),
		Attributes: map[string]string{},
	}
	if body := strings.TrimSpace(flags.body); body != "" {
		task.Attributes["body"] = body
	}
	if len(task.Attributes) == 0 {
		task.Attributes = nil
	}
	return task
}

func relatedObjectsFromFlags(flags *actionFlags) []execution.ObjectRef {
	if flags == nil {
		return nil
	}
	if flags.prNumber <= 0 && strings.TrimSpace(flags.baseRef) == "" && strings.TrimSpace(flags.headRef) == "" && !flags.draft {
		return nil
	}

	attributes := map[string]string{}
	if value := strings.TrimSpace(flags.baseRef); value != "" {
		attributes["base_ref"] = value
	}
	if value := strings.TrimSpace(flags.headRef); value != "" {
		attributes["head_ref"] = value
	}
	if value := strings.TrimSpace(flags.body); value != "" {
		attributes["body"] = value
	}
	if flags.draft {
		attributes["draft"] = "true"
	}
	if len(attributes) == 0 {
		attributes = nil
	}
	return []execution.ObjectRef{{
		Type:       "merge-request",
		Repository: strings.TrimSpace(flags.repository),
		Number:     flags.prNumber,
		Title:      strings.TrimSpace(flags.title),
		Attributes: attributes,
	}}
}

func operationInvocationFromFlags(flags *operationFlags) (execution.OperationInvocation, error) {
	actionInvocation, err := actionInvocationFromFlags(&flags.actionFlags)
	if err != nil {
		return execution.OperationInvocation{}, err
	}

	return execution.OperationInvocation{
		Operation:  strings.TrimSpace(flags.operation),
		Assignment: actionInvocation.Assignment,
	}, nil
}

func structuredInputFromFlags(flags *actionFlags) (*execution.StructuredInput, error) {
	input := execution.StructuredInput{}
	if !hasStructuredInputFlags(flags) {
		return nil, nil
	}

	if strings.TrimSpace(flags.inputFile) != "" {
		loaded, err := readStructuredInputFile(flags.inputFile)
		if err != nil {
			return nil, err
		}
		input = loaded
	}

	if strings.TrimSpace(flags.task) != "" {
		input.Task = strings.TrimSpace(flags.task)
	}
	for _, constraint := range flags.constraints {
		constraint = strings.TrimSpace(constraint)
		if constraint == "" {
			return nil, fmt.Errorf("constraint must be non-empty")
		}
		input.Constraints = append(input.Constraints, constraint)
	}

	if err := appendStructuredJSONObjects(flags.projectContexts, "project-context", &input.ProjectContext); err != nil {
		return nil, err
	}
	if err := appendStructuredJSONObjects(flags.operationalContexts, "operational-context", &input.OperationalContext); err != nil {
		return nil, err
	}
	if err := appendStructuredJSONObjects(flags.previousRunResults, "previous-run-result", &input.PreviousRunResults); err != nil {
		return nil, err
	}
	if err := appendStructuredJSONObjects(flags.reviewRemarks, "review-remark", &input.ReviewRemarks); err != nil {
		return nil, err
	}
	if err := appendStructuredJSONObjects(flags.reviewResponses, "review-response", &input.ReviewResponses); err != nil {
		return nil, err
	}
	if err := appendStructuredJSONObjects(flags.integrationActions, "integration-action", &input.IntegrationActions); err != nil {
		return nil, err
	}

	return &input, nil
}

func hasStructuredInputFlags(flags *actionFlags) bool {
	if flags == nil {
		return false
	}
	return strings.TrimSpace(flags.inputFile) != "" ||
		strings.TrimSpace(flags.task) != "" ||
		len(flags.constraints) != 0 ||
		len(flags.projectContexts) != 0 ||
		len(flags.operationalContexts) != 0 ||
		len(flags.previousRunResults) != 0 ||
		len(flags.reviewRemarks) != 0 ||
		len(flags.reviewResponses) != 0 ||
		len(flags.integrationActions) != 0
}

func readStructuredInputFile(path string) (execution.StructuredInput, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return execution.StructuredInput{}, fmt.Errorf("read structured input file %s: %w", path, err)
	}

	var input execution.StructuredInput
	if err := decodeStrictJSON(content, &input); err != nil {
		return execution.StructuredInput{}, fmt.Errorf("parse structured input file %s: %w", path, err)
	}

	return input, nil
}

func appendStructuredJSONObjects[T any](values []string, flagName string, target *[]T) error {
	for index, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("%s[%d] must be non-empty JSON object", flagName, index)
		}

		var decoded T
		if err := decodeStrictJSON([]byte(value), &decoded); err != nil {
			return fmt.Errorf("parse %s[%d]: %w", flagName, index, err)
		}
		*target = append(*target, decoded)
	}

	return nil
}

func decodeStrictJSON(content []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON tokens")
		}
		return err
	}

	return nil
}

func printExecutionResult(cmd *cobra.Command, result execution.ExecutionResult) {
	if result.Launch != nil {
		printLaunchResult(cmd, *result.Launch)
		return
	}

	printLaunchResult(cmd, execution.LaunchResult{Status: result.Status, Summary: result.Summary})
}

func printExecutionResultOnError(cmd *cobra.Command, result execution.ExecutionResult) {
	if result.Launch != nil {
		printLaunchResultOnError(cmd, *result.Launch)
		return
	}
	if strings.TrimSpace(result.Status) == "" && strings.TrimSpace(result.Summary) == "" {
		return
	}

	printExecutionResult(cmd, result)
}

func printOperationResult(cmd *cobra.Command, result execution.OperationResult) {
	cmd.Printf("operation=%s\n", result.Name)
	cmd.Printf("status=%s\n", result.Status)
	if strings.TrimSpace(result.Summary) != "" {
		cmd.Printf("summary=%s\n", normalizeStructuredValue(result.Summary))
	}
	if strings.TrimSpace(result.Input) != "" {
		cmd.Printf("input=%s\n", normalizeStructuredValue(result.Input))
	}
	if strings.TrimSpace(result.Output) != "" {
		cmd.Printf("output=%s\n", normalizeStructuredValue(result.Output))
	}
	if result.Failure != nil {
		cmd.Printf("failure-code=%s\n", result.Failure.Code)
		cmd.Printf("failure-message=%s\n", normalizeStructuredValue(result.Failure.Message))
	}
}

func printOperationResultOnError(cmd *cobra.Command, result execution.OperationResult) {
	if strings.TrimSpace(result.Name) == "" && strings.TrimSpace(string(result.Status)) == "" && result.Failure == nil {
		return
	}

	printOperationResult(cmd, result)
}

func printLaunchResult(cmd *cobra.Command, result execution.LaunchResult) {
	cmd.Printf("state=%s\n", result.Status)
	printLaunchSummary(cmd, result.Summary)
	printLaunchRawOutputPath(cmd, result.RawOutputPath)
	printLaunchRunRecordPath(cmd, result.RunRecordPath)
	printLaunchStructuredOutput(cmd, result)
}

func printLaunchResultOnError(cmd *cobra.Command, result execution.LaunchResult) {
	if strings.TrimSpace(result.Status) == "" && strings.TrimSpace(result.Summary) == "" && strings.TrimSpace(result.RawOutputPath) == "" && strings.TrimSpace(result.RunRecordPath) == "" && result.StructuredOutput == nil {
		return
	}

	printLaunchResult(cmd, result)
}

func printLaunchSummary(cmd *cobra.Command, summary string) {
	cmd.Printf("summary<<%s\n%s\n%s\n", launchSummaryDelimiter, summary, launchSummaryDelimiter)
}

func printLaunchRawOutputPath(cmd *cobra.Command, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}

	cmd.Printf("raw-output-path=%s\n", path)
}

func printLaunchRunRecordPath(cmd *cobra.Command, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}

	cmd.Printf("run-record-path=%s\n", path)
}

func printLaunchStructuredOutput(cmd *cobra.Command, result execution.LaunchResult) {
	if result.StructuredOutput == nil {
		return
	}

	cmd.Println("structured-output:")
	printStructuredOutputBlock(cmd, result.StructuredOutput)
}

func printStructuredOutputBlock(cmd *cobra.Command, output *execution.StructuredOutput) {
	printLaunchResultSection(cmd, "summary-field", []string{output.Summary})
	printLaunchResultSection(cmd, "commit-message", []string{output.CommitMessage})
	printStructuredJSONSection(cmd, "remark", output.Remarks)
	printStructuredJSONSection(cmd, "review-response", output.ReviewResponses)
	printStructuredJSONSection(cmd, "question", output.Questions)
	printStructuredJSONSection(cmd, "follow-up-action", output.FollowUpActions)
	printStructuredJSONSection(cmd, "change", output.Changes)
	printStructuredJSONSection(cmd, "command", output.Commands)
	if output.Conclusion != nil {
		printStructuredJSONSection(cmd, "conclusion", []execution.StructuredConclusion{*output.Conclusion})
	}
	printStructuredJSONSection(cmd, "extension", extensionsAsEntries(output.Extensions))
}

func printLaunchResultSection(cmd *cobra.Command, key string, values []string) {
	for _, value := range values {
		value = normalizeStructuredValue(value)
		if value == "" {
			continue
		}

		cmd.Printf("%s=%s\n", key, value)
	}
}

func printStructuredJSONSection[T any](cmd *cobra.Command, key string, values []T) {
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}

		text := strings.TrimSpace(string(encoded))
		if text == "{}" {
			continue
		}

		cmd.Printf("%s=%s\n", key, text)
	}
}

type structuredExtensionEntry struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

func extensionsAsEntries(extensions execution.StructuredExtensions) []structuredExtensionEntry {
	if len(extensions) == 0 {
		return nil
	}

	entries := make([]structuredExtensionEntry, 0, len(extensions))
	for name, value := range extensions {
		if strings.TrimSpace(name) == "" || len(value) == 0 {
			continue
		}

		entries = append(entries, structuredExtensionEntry{Name: name, Value: value})
	}

	return entries
}

const launchSummaryDelimiter = "PROGRESS_SUMMARY"

func normalizeStructuredValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
