package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/execution/launch"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

const defaultLaunchModel = "openai/gpt-5.4"

type launchFlags struct {
	directory                string
	name                     string
	profile                  string
	runner                   string
	model                    string
	prompt                   string
	structuredOutput         bool
	structuredOutputRequired bool
	commitPush               bool
}

type executionCommandService interface {
	Start(context.Context, execution.Invocation) (execution.LaunchResult, error)
	Dispatch(context.Context, execution.Invocation) []string
	ResolveProfile(context.Context, execution.Invocation) (execution.Profile, error)
	AllocateResources(context.Context, execution.Invocation, execution.Profile) (execution.Allocation, error)
	PrepareWorkplace(context.Context, execution.Invocation, execution.Profile, execution.Allocation) (execution.Workplace, error)
	LaunchDirect(context.Context, execution.Invocation) (execution.LaunchResult, error)
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
		newExecutionDispatcherCommand(),
		newExecutionProfileCommand(),
		newExecutionResourcesCommand(),
		newExecutionWorkplaceCommand(),
		newExecutionLaunchCommand(),
	)

	return cmd
}

func newExecutionStartCommand() *cobra.Command {
	flags := newStartFlags()

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Полный запуск контура исполнения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			result, err := service.Start(context.Background(), invocationFromLaunchFlags(flags))
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

			cmd.Printf("profile=%s\ndescription=%s\nrunner=%s\nmode=%s\nmodel=%s\nprompt-additions=%s\nstructured-output=%t\nstructured-output-required=%t\nstructured-output-fields=%s\ncommit-push=%t\n", profile.Name, profile.Description, profile.Runner, profile.Mode, profile.Model, strings.Join(profile.PromptAdditions, " | "), profile.StructuredOutput, profile.StructuredOutputRequired, strings.Join(profile.StructuredOutputFields, ","), profile.CommitPush)
			return nil
		},
	}

	bindProfileFlags(cmd, flags)
	return cmd
}

func newExecutionResourcesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "Проверка и резервирование ресурсов",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			in := execution.Invocation{}

			profile, err := service.ResolveProfile(context.Background(), in)
			if err != nil {
				return err
			}

			allocation, err := service.AllocateResources(context.Background(), in, profile)
			if err != nil {
				return err
			}

			cmd.Printf("resource=%s\nreserved=%t\n", allocation.Resource, allocation.Reserved)
			return nil
		},
	}
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

			cmd.Printf("workplace=%s\nready=%t\n", workplace.Name, workplace.Ready)
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

func bindLaunchFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Рабочий каталог для запуска runner")
	cmd.Flags().StringVar(&flags.runner, "runner", flags.runner, "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", flags.model, "Идентификатор модели")
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
	cmd.Flags().StringVar(&flags.profile, "profile", flags.profile, "Тип исполнительного профиля")
	cmd.Flags().StringVar(&flags.runner, "runner", flags.runner, "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", flags.model, "Идентификатор модели")
	cmd.Flags().StringVar(&flags.prompt, "prompt", "", "Промпт для запуска runner")
	cmd.Flags().BoolVar(&flags.structuredOutput, "structured-output", false, "Автоматически добавить инструкцию на structured output")
	cmd.Flags().BoolVar(&flags.structuredOutputRequired, "structured-output-required", false, "Считать отсутствие или невалидность structured output ошибкой")
	_ = cmd.MarkFlagRequired("prompt")
}

func bindProfileFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.profile, "profile", "default", "Тип исполнительного профиля")
}

func bindWorkplaceFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Существующий рабочий каталог")
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя нового рабочего места в .progress/workplaces")
}

func invocationFromLaunchFlags(flags *launchFlags) execution.Invocation {
	return execution.Invocation{
		Profile:   flags.profile,
		Workplace: execution.WorkplaceSpec{Name: flags.name},
		Launch: execution.LaunchSpec{
			Directory:                flags.directory,
			Runner:                   flags.runner,
			Model:                    flags.model,
			Prompt:                   flags.prompt,
			StructuredOutput:         flags.structuredOutput,
			StructuredOutputRequired: flags.structuredOutputRequired,
			CommitPush:               flags.commitPush,
		},
	}
}

func invocationFromProfileFlags(flags *launchFlags) execution.Invocation {
	return execution.Invocation{Profile: flags.profile}
}

func invocationFromWorkplaceFlags(flags *launchFlags) execution.Invocation {
	return execution.Invocation{
		Workplace: execution.WorkplaceSpec{Name: flags.name},
		Launch:    execution.LaunchSpec{Directory: flags.directory},
	}
}

func printLaunchResult(cmd *cobra.Command, result execution.LaunchResult) {
	cmd.Printf("state=%s\n", result.Status)
	printLaunchSummary(cmd, result.Summary)
	printLaunchRawOutputPath(cmd, result.RawOutputPath)
	printLaunchStructuredOutput(cmd, result)
}

func printLaunchResultOnError(cmd *cobra.Command, result execution.LaunchResult) {
	if strings.TrimSpace(result.Status) == "" && strings.TrimSpace(result.Summary) == "" && strings.TrimSpace(result.RawOutputPath) == "" && result.StructuredOutput == nil {
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

func printLaunchStructuredOutput(cmd *cobra.Command, result execution.LaunchResult) {
	if result.StructuredOutput == nil {
		return
	}

	cmd.Println("structured-output:")
	printStructuredOutputBlock(cmd, result.StructuredOutput)
}

func printStructuredOutputBlock(cmd *cobra.Command, output *execution.StructuredOutput) {
	printLaunchResultSection(cmd, "protocol-version", []string{output.ProtocolVersion})
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
