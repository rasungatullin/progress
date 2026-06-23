package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

const defaultLaunchModel = "openai/gpt-5.4"

type launchFlags struct {
	directory                string
	name                     string
	repo                     string
	profile                  string
	executionProfile         string
	reviewProfile            string
	maxExecutions            int
	inputFile                string
	task                     string
	constraints              []string
	projectContexts          []string
	operationalContexts      []string
	previousRunResults       []string
	reviewRemarks            []string
	reviewResponses          []string
	integrationActions       []string
	runner                   string
	model                    string
	modelBinding             string
	prompt                   string
	structuredOutput         bool
	structuredOutputRequired bool
	commitPush               bool
}

type executionRunsFlags struct {
	jsonOutput bool
	limit      int
	name       string
	status     string
}

type resumeFlags struct {
	run                      string
	message                  string
	messageFile              string
	name                     string
	profile                  string
	structuredOutput         bool
	structuredOutputRequired bool
	dryRun                   bool
}

type executionCommandService interface {
	Start(context.Context, execution.Invocation) (execution.LaunchResult, error)
	Dispatch(context.Context, execution.Invocation) []string
	ResolveProfile(context.Context, execution.Invocation) (execution.Profile, error)
	AllocateResources(context.Context, execution.Invocation, execution.Profile) (execution.Allocation, error)
	PrepareWorkplace(context.Context, execution.Invocation, execution.Profile, execution.Allocation) (execution.Workplace, error)
	LaunchDirect(context.Context, execution.Invocation) (execution.LaunchResult, error)
	Resume(context.Context, execution.ResumeRequest) (execution.LaunchResult, error)
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
		newExecutionStartCommand(),
		newExecutionReviewCycleCommand(),
		newExecutionResumeCommand(),
		newExecutionDispatcherCommand(),
		newExecutionProfileCommand(),
		newExecutionResourcesCommand(),
		newExecutionWorkplaceCommand(),
		newExecutionLaunchCommand(),
		newExecutionRunsCommand(),
	)

	return cmd
}

func newExecutionRunsCommand() *cobra.Command {
	flags := executionRunsFlags{limit: 20}

	cmd := &cobra.Command{
		Use:   "runs",
		Short: "История запусков execution",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			runs, err := history.List(context.Background(), cwd, history.ListFilter{Limit: flags.limit, Name: flags.name, Status: flags.status})
			if err != nil {
				return err
			}

			if flags.jsonOutput {
				payload, err := json.Marshal(runs)
				if err != nil {
					return err
				}
				cmd.Println(string(payload))
				return nil
			}

			printExecutionRunsTable(cmd, runs)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flags.jsonOutput, "json", false, "Вывести историю в JSON")
	cmd.Flags().IntVar(&flags.limit, "limit", flags.limit, "Максимальное число записей")
	cmd.Flags().StringVar(&flags.name, "name", "", "Фильтр по имени запуска")
	cmd.Flags().StringVar(&flags.status, "status", "", "Фильтр по статусу запуска")
	return cmd
}

func newExecutionStartCommand() *cobra.Command {
	flags := newStartFlags()

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Полный запуск контура исполнения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			in, err := invocationFromStructuredFlags(flags)
			if err != nil {
				return err
			}

			result, err := service.Start(context.Background(), in)
			if err != nil {
				printLaunchResultOnError(cmd, result)
				return err
			}

			printLaunchResult(cmd, result)
			return nil
		},
	}

	bindStartFlags(cmd, flags)
	return cmd
}

func newExecutionReviewCycleCommand() *cobra.Command {
	flags := newReviewCycleFlags()

	cmd := &cobra.Command{
		Use:   "review-cycle",
		Short: "Цикл исполнения с автоматическим ревью",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			in, err := invocationFromReviewCycleFlags(flags)
			if err != nil {
				return err
			}

			result, err := execution.RunReviewCycle(context.Background(), service, in, flags.reviewProfile, flags.maxExecutions)
			if err != nil {
				printLaunchResultOnError(cmd, result)
				return err
			}

			printLaunchResult(cmd, result)
			return nil
		},
	}

	bindReviewCycleFlags(cmd, flags)
	return cmd
}

func newExecutionResumeCommand() *cobra.Command {
	flags := &resumeFlags{}

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Возобновление сеанса исполнительного модуля",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			request, err := resumeRequestFromFlags(flags)
			if err != nil {
				return err
			}

			result, err := service.Resume(context.Background(), request)
			if err != nil {
				printLaunchResultOnError(cmd, result)
				return err
			}

			printLaunchResult(cmd, result)
			return nil
		},
	}

	bindResumeFlags(cmd, flags)
	return cmd
}

func newExecutionDispatcherCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dispatcher",
		Short: "Диагностика маршрута диспетчера исполнения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			stages := service.Dispatch(context.Background(), execution.Invocation{})
			for _, stage := range stages {
				cmd.Println(stage)
			}
			return nil
		},
	}
}

func newExecutionProfileCommand() *cobra.Command {
	flags := &launchFlags{}

	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Выбор исполнительного профиля",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			profile, err := service.ResolveProfile(context.Background(), invocationFromProfileFlags(flags))
			if err != nil {
				return err
			}

			cmd.Printf("profile=%s\ndescription=%s\nmode=%s\nmodel-binding=%s\nallow-model-fallback=%t\nprompt-additions=%s\nstructured-output=%t\nstructured-output-required=%t\nstructured-output-fields=%s\ncommit-push=%t\n", profile.Name, profile.Description, profile.Mode, profile.ModelBinding, profile.AllowModelFallback, strings.Join(profile.PromptAdditions, " | "), profile.StructuredOutput, profile.StructuredOutputRequired, strings.Join(profile.StructuredOutputFields, ","), profile.CommitPush)
			return nil
		},
	}

	bindProfileFlags(cmd, flags)
	return cmd
}

func newExecutionResourcesCommand() *cobra.Command {
	flags := newStartFlags()

	cmd := &cobra.Command{
		Use:   "resources",
		Short: "Проверка и резервирование ресурсов",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			in := invocationFromProfileFlags(flags)
			in.Launch.Runner = flags.runner
			in.Launch.Model = flags.model
			in.Launch.ModelBinding = flags.modelBinding

			profile, err := service.ResolveProfile(context.Background(), in)
			if err != nil {
				return err
			}

			allocation, err := service.AllocateResources(context.Background(), in, profile)
			if err != nil {
				return err
			}

			cmd.Printf("resource=%s\nreserved=%t\nrunner=%s\nmodel=%s\nmodel-binding=%s\nsource=%s\n", allocation.Resource, allocation.Reserved, allocation.Runner, allocation.Model, allocation.ModelBinding, allocation.Source)
			cmd.Printf("binding-source=%s\nfallback-used=%t\n", allocation.BindingSource, allocation.FallbackUsed)
			cmd.Printf("global-config=%s\nlocal-config=%s\n", allocation.GlobalConfigPath, allocation.LocalConfigPath)
			return nil
		},
	}

	bindProfileFlags(cmd, flags)
	cmd.Flags().StringVar(&flags.runner, "runner", "", "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", "", "Идентификатор модели")
	cmd.Flags().StringVar(&flags.modelBinding, "model-binding", "", "Семантический binding runner+model")
	return cmd
}

func newExecutionWorkplaceCommand() *cobra.Command {
	flags := &launchFlags{}

	cmd := &cobra.Command{
		Use:   "workplace",
		Short: "Подготовка исполнительного рабочего места",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			in := invocationFromWorkplaceFlags(flags)

			workplace, err := service.PrepareWorkplace(context.Background(), in, execution.Profile{}, execution.Allocation{})
			if err != nil {
				return err
			}

			cmd.Println(formatExecutionWorkplaceDiagnostics(workplace))
			return nil
		},
	}

	bindWorkplaceFlags(cmd, flags)
	return cmd
}

func newExecutionLaunchCommand() *cobra.Command {
	flags := newLaunchFlags()

	cmd := &cobra.Command{
		Use:   "launch",
		Short: "Пуск задачи после завершения аллокаций",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			in := invocationFromLaunchFlags(flags)

			result, err := service.LaunchDirect(context.Background(), in)
			if err != nil {
				printLaunchResultOnError(cmd, result)
				return err
			}

			printLaunchResult(cmd, result)
			return nil
		},
	}

	bindLaunchFlags(cmd, flags)
	return cmd
}

func newExecutionService(cmd *cobra.Command) executionCommandService {
	if factory, ok := cmd.Context().Value(executionServiceFactoryContextKey{}).(executionServiceFactoryFunc); ok && factory != nil {
		return factory(cmd)
	}

	return executionServiceFactory(cmd)
}

func newLaunchFlags() *launchFlags {
	return &launchFlags{
		runner: launch.RunnerOpenCode,
		model:  defaultLaunchModel,
	}
}

func newStartFlags() *launchFlags {
	return &launchFlags{
		profile: "default",
	}
}

func newReviewCycleFlags() *launchFlags {
	return &launchFlags{
		executionProfile: "default",
		maxExecutions:    execution.DefaultReviewCycleMaxExecutions,
	}
}

func bindLaunchFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Рабочий каталог для запуска runner")
	cmd.Flags().StringVar(&flags.runner, "runner", flags.runner, "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", flags.model, "Идентификатор модели")
	cmd.Flags().StringVar(&flags.modelBinding, "model-binding", flags.modelBinding, "Семантический binding runner+model")
	cmd.Flags().StringVar(&flags.prompt, "prompt", "", "Промпт для запуска runner")
	cmd.Flags().BoolVar(&flags.structuredOutput, "structured-output", false, "Автоматически добавить инструкцию на structured output")
	cmd.Flags().BoolVar(&flags.structuredOutputRequired, "structured-output-required", false, "Считать отсутствие или невалидность structured output ошибкой")
	cmd.Flags().BoolVar(&flags.commitPush, "commit-push", false, "После успешного запуска выполнить git commit и git push")
	_ = cmd.MarkFlagRequired("dir")
	_ = cmd.MarkFlagRequired("prompt")
}

func bindStartFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Рабочий каталог для запуска runner")
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя нового рабочего места в .progress/workplaces")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub для подготовки рабочего места: owner/name или clone URL")
	cmd.Flags().StringVar(&flags.profile, "profile", flags.profile, "Тип исполнительного профиля")
	cmd.Flags().StringVar(&flags.runner, "runner", flags.runner, "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", flags.model, "Идентификатор модели")
	cmd.Flags().StringVar(&flags.modelBinding, "model-binding", flags.modelBinding, "Семантический binding runner+model")
	bindStructuredInputFlags(cmd, flags)
	cmd.Flags().BoolVar(&flags.structuredOutput, "structured-output", false, "Автоматически добавить инструкцию на structured output")
	cmd.Flags().BoolVar(&flags.structuredOutputRequired, "structured-output-required", false, "Считать отсутствие или невалидность structured output ошибкой")
}

func bindReviewCycleFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Рабочий каталог для запуска runner")
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя нового рабочего места в .progress/workplaces")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub для подготовки рабочего места: owner/name или clone URL")
	cmd.Flags().StringVar(&flags.executionProfile, "execution-profile", flags.executionProfile, "Профиль исполнения")
	cmd.Flags().StringVar(&flags.reviewProfile, "review-profile", "", "Профиль ревью")
	cmd.Flags().IntVar(&flags.maxExecutions, "max-executions", flags.maxExecutions, "Максимальное число запусков исполнения")
	cmd.Flags().StringVar(&flags.runner, "runner", flags.runner, "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", flags.model, "Идентификатор модели")
	cmd.Flags().StringVar(&flags.modelBinding, "model-binding", flags.modelBinding, "Семантический binding runner+model")
	bindStructuredInputFlags(cmd, flags)
	cmd.Flags().BoolVar(&flags.structuredOutput, "structured-output", false, "Автоматически добавить инструкцию на structured output")
	cmd.Flags().BoolVar(&flags.structuredOutputRequired, "structured-output-required", false, "Считать отсутствие или невалидность structured output ошибкой")
	_ = cmd.MarkFlagRequired("review-profile")
}

func bindResumeFlags(cmd *cobra.Command, flags *resumeFlags) {
	cmd.Flags().StringVar(&flags.run, "run", "", "Исходный запуск: числовой id или latest")
	cmd.Flags().StringVar(&flags.message, "message", "", "Дополнительное сообщение для возобновления")
	cmd.Flags().StringVar(&flags.messageFile, "message-file", "", "Путь к файлу с дополнительным сообщением")
	cmd.Flags().StringVar(&flags.name, "name", "", "Фильтр имени запуска для выбора latest")
	cmd.Flags().StringVar(&flags.profile, "profile", "", "Опциональное переопределение исполнительного профиля")
	cmd.Flags().BoolVar(&flags.structuredOutput, "structured-output", false, "Автоматически добавить инструкцию на structured output")
	cmd.Flags().BoolVar(&flags.structuredOutputRequired, "structured-output-required", false, "Считать отсутствие или невалидность structured output ошибкой")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "Показать задание на возобновление без запуска исполнительного модуля")
	_ = cmd.MarkFlagRequired("run")
}

func bindStructuredInputFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.inputFile, "input-file", "", "Путь к JSON-файлу structured input")
	cmd.Flags().StringVar(&flags.task, "task", "", "Текстовая постановка structured input")
	cmd.Flags().StringArrayVar(&flags.constraints, "constraint", nil, "Ограничение structured input, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.projectContexts, "project-context", nil, "JSON object для project_context, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.operationalContexts, "operational-context", nil, "JSON object для operational_context, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.previousRunResults, "previous-run-result", nil, "JSON object для previous_run_results, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.reviewRemarks, "review-remark", nil, "JSON object для review_remarks, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.reviewResponses, "review-response", nil, "JSON object для review_responses, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.integrationActions, "integration-action", nil, "JSON object для integration_actions, флаг можно повторять")
}

func bindProfileFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.profile, "profile", "default", "Тип исполнительного профиля")
}

func bindWorkplaceFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Существующий рабочий каталог")
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя нового рабочего места в .progress/workplaces")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub для подготовки рабочего места: owner/name или clone URL")
}

func invocationFromLaunchFlags(flags *launchFlags) execution.Invocation {
	return execution.Invocation{
		Profile:    flags.profile,
		Repository: execution.RepositorySpec{URL: flags.repo},
		Workplace:  execution.WorkplaceSpec{Name: flags.name},
		Launch: execution.LaunchSpec{
			Directory:                flags.directory,
			Runner:                   flags.runner,
			Model:                    flags.model,
			ModelBinding:             flags.modelBinding,
			Prompt:                   flags.prompt,
			StructuredOutput:         flags.structuredOutput,
			StructuredOutputRequired: flags.structuredOutputRequired,
			CommitPush:               flags.commitPush,
		},
	}
}

func invocationFromStructuredFlags(flags *launchFlags) (execution.Invocation, error) {
	input, err := structuredInputFromFlags(flags)
	if err != nil {
		return execution.Invocation{}, err
	}
	input, err = launch.NormalizeStructuredInput(input)
	if err != nil {
		return execution.Invocation{}, err
	}

	invocation := invocationFromLaunchFlags(flags)
	invocation.Launch.Prompt = ""
	invocation.Launch.StructuredInput = input
	return invocation, nil
}

func invocationFromReviewCycleFlags(flags *launchFlags) (execution.Invocation, error) {
	invocation, err := invocationFromStructuredFlags(flags)
	if err != nil {
		return execution.Invocation{}, err
	}
	invocation.Profile = flags.executionProfile
	return invocation, nil
}

func invocationFromProfileFlags(flags *launchFlags) execution.Invocation {
	return execution.Invocation{Profile: flags.profile}
}

func invocationFromWorkplaceFlags(flags *launchFlags) execution.Invocation {
	return execution.Invocation{
		Repository: execution.RepositorySpec{URL: flags.repo},
		Workplace:  execution.WorkplaceSpec{Name: flags.name},
		Launch:     execution.LaunchSpec{Directory: flags.directory},
	}
}

func resumeRequestFromFlags(flags *resumeFlags) (execution.ResumeRequest, error) {
	if strings.TrimSpace(flags.message) != "" && strings.TrimSpace(flags.messageFile) != "" {
		return execution.ResumeRequest{}, fmt.Errorf("message and message-file are mutually exclusive")
	}

	message := strings.TrimSpace(flags.message)
	messageSource := "message"
	if strings.TrimSpace(flags.messageFile) != "" {
		content, err := os.ReadFile(flags.messageFile)
		if err != nil {
			return execution.ResumeRequest{}, fmt.Errorf("read message file %s: %w", flags.messageFile, err)
		}
		message = strings.TrimSpace(string(content))
		messageSource = "message-file"
	}
	if message == "" {
		return execution.ResumeRequest{}, fmt.Errorf("resume message must be non-empty")
	}

	return execution.ResumeRequest{
		Run:                      strings.TrimSpace(flags.run),
		Name:                     strings.TrimSpace(flags.name),
		Message:                  message,
		MessageSource:            messageSource,
		Profile:                  strings.TrimSpace(flags.profile),
		StructuredOutput:         flags.structuredOutput,
		StructuredOutputRequired: flags.structuredOutputRequired,
		DryRun:                   flags.dryRun,
	}, nil
}

func structuredInputFromFlags(flags *launchFlags) (*execution.StructuredInput, error) {
	input := execution.StructuredInput{}
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

func printExecutionRunsTable(cmd *cobra.Command, runs []history.ListedRun) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tCREATED_AT\tSTATUS\tNAME\tPROFILE\tRUNNER\tSUMMARY")
	for _, run := range runs {
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", run.ID, run.CreatedAt, run.Status, run.Name, run.ProfileName, run.Runner, singleLine(run.Summary))
	}
	_ = writer.Flush()
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func formatExecutionWorkplaceDiagnostics(workplace execution.Workplace) string {
	lines := make([]string, 0, 4)
	if strings.TrimSpace(workplace.RepositoryURL) != "" {
		lines = append(lines, fmt.Sprintf("repository=%s", workplace.RepositoryURL))
	}
	if strings.TrimSpace(workplace.RepositoryRoot) != "" {
		lines = append(lines, fmt.Sprintf("repository-root=%s", workplace.RepositoryRoot))
	}
	lines = append(lines, fmt.Sprintf("workplace=%s", workplace.Name))
	lines = append(lines, fmt.Sprintf("ready=%t", workplace.Ready))
	return strings.Join(lines, "\n")
}

func printStructuredOutputBlock(cmd *cobra.Command, output *execution.StructuredOutput) {
	printLaunchResultSection(cmd, "summary-field", []string{output.Summary})
	printLaunchResultSection(cmd, "commit-message", []string{output.CommitMessage})
	printStructuredJSONSection(cmd, "remark", output.Remarks)
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
