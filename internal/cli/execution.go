package cli

import (
	"context"

	"github.com/rasungatullin/progress/internal/execution"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

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
	return &cobra.Command{
		Use:   "start",
		Short: "Полный запуск контура исполнения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			result, err := service.Start(context.Background(), execution.Invocation{})
			if err != nil {
				return err
			}

			cmd.Printf("state=%s\nsummary=%s\n", result.Status, result.Summary)
			return nil
		},
	}
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
	return &cobra.Command{
		Use:   "profile",
		Short: "Выбор исполнительного профиля",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newExecutionService(cmd)
			profile, err := service.ResolveProfile(context.Background(), execution.Invocation{})
			if err != nil {
				return err
			}

			cmd.Printf("profile=%s\nmode=%s\n", profile.Name, profile.Mode)
			return nil
		},
	}
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
	return &cobra.Command{
		Use:   "workplace",
		Short: "Подготовка исполнительного рабочего места",
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

			workplace, err := service.PrepareWorkplace(context.Background(), in, profile, allocation)
			if err != nil {
				return err
			}

			cmd.Printf("workplace=%s\nready=%t\n", workplace.Name, workplace.Ready)
			return nil
		},
	}
}

func newExecutionLaunchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "launch",
		Short: "Пуск задачи после завершения аллокаций",
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

			workplace, err := service.PrepareWorkplace(context.Background(), in, profile, allocation)
			if err != nil {
				return err
			}

			result, err := service.Launch(context.Background(), in, profile, allocation, workplace)
			if err != nil {
				return err
			}

			cmd.Printf("state=%s\nsummary=%s\n", result.Status, result.Summary)
			return nil
		},
	}
}

func newExecutionService(cmd *cobra.Command) *execution.Service {
	return execution.NewService(logging.New(cmd.ErrOrStderr()))
}
