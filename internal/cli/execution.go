package cli

import (
	"context"

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

			cmd.Printf("state=%s\nsummary=%s\n", result.Status, result.Summary)
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

			cmd.Printf("profile=%s\nmode=%s\nmodel=%s\ncommit-push=%t\n", profile.Name, profile.Mode, profile.Model, profile.CommitPush)
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

			cmd.Printf("state=%s\nsummary=%s\n", result.Status, result.Summary)
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
