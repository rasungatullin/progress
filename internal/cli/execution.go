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
	directory     string
	name          string
	profile       string
	runner        string
	model         string
	prompt        string
	commitPush    bool
	commitMessage string
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

			cmd.Printf("profile=%s\ndescription=%s\nmode=%s\nmodel=%s\ncommit-push=%t\n", profile.Name, profile.Description, profile.Mode, profile.Model, profile.CommitPush)
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
				return err
			}

			printLaunchResult(cmd, result)
			return nil
		},
	}

	bindLaunchFlags(cmd, flags)
	return cmd
}

func newExecutionService(cmd *cobra.Command) *execution.Service {
	return execution.NewService(logging.New(cmd.ErrOrStderr()))
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
		runner:  launch.RunnerOpenCode,
	}
}

func bindLaunchFlags(cmd *cobra.Command, flags *launchFlags) {
	cmd.Flags().StringVar(&flags.directory, "dir", "", "Рабочий каталог для запуска runner")
	cmd.Flags().StringVar(&flags.runner, "runner", flags.runner, "Исполнительный runner")
	cmd.Flags().StringVar(&flags.model, "model", flags.model, "Идентификатор модели")
	cmd.Flags().StringVar(&flags.prompt, "prompt", "", "Промпт для запуска runner")
	cmd.Flags().BoolVar(&flags.commitPush, "commit-push", false, "После успешного запуска выполнить git commit и git push")
	cmd.Flags().StringVar(&flags.commitMessage, "commit-message", launch.DefaultCommitMessage, "Текст git commit при использовании --commit-push")
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
			Directory:     flags.directory,
			Runner:        flags.runner,
			Model:         flags.model,
			Prompt:        flags.prompt,
			CommitPush:    flags.commitPush,
			CommitMessage: flags.commitMessage,
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
	printLaunchStructuredOutput(cmd, result)
}

func printLaunchSummary(cmd *cobra.Command, summary string) {
	cmd.Printf("summary<<%s\n%s\n%s\n", launchSummaryDelimiter, summary, launchSummaryDelimiter)
}

func printLaunchStructuredOutput(cmd *cobra.Command, result execution.LaunchResult) {
	if !hasStructuredValues(result.CriticalRemarks, result.MinorRemarks, result.Questions) && result.ReviewCycle == nil {
		return
	}

	cmd.Println("structured-output:")
	printReviewCycleStructuredOutput(cmd, result.ReviewCycle)
	printLaunchResultSection(cmd, "critical-remark", result.CriticalRemarks)
	printLaunchResultSection(cmd, "minor-remark", result.MinorRemarks)
	printLaunchResultSection(cmd, "question", result.Questions)
}

func printReviewCycleStructuredOutput(cmd *cobra.Command, reviewCycle *execution.ReviewCycleEnvelope) {
	if reviewCycle == nil {
		return
	}

	printLaunchResultSection(cmd, "review-cycle-protocol-version", []string{reviewCycle.ProtocolVersion})
	printLaunchResultSection(cmd, "review-cycle-mode", []string{reviewCycle.Mode})
	printLaunchResultSection(cmd, "review-cycle-summary", []string{reviewCycle.Summary})
	printStructuredJSONSection(cmd, "review-cycle-remark", reviewCycle.Remarks)
	printStructuredJSONSection(cmd, "review-cycle-question", reviewCycle.Questions)
	printStructuredJSONSection(cmd, "review-cycle-follow-up-action", reviewCycle.FollowUpActions)
	printStructuredJSONSection(cmd, "review-cycle-change", reviewCycle.Changes)
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

const launchSummaryDelimiter = "PROGRESS_SUMMARY"

func normalizeStructuredValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func hasStructuredValues(sections ...[]string) bool {
	for _, values := range sections {
		for _, value := range values {
			if normalizeStructuredValue(value) != "" {
				return true
			}
		}
	}

	return false
}
