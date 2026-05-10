package cli

import (
	"context"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

type integrationFlags struct {
	system    string
	resource  string
	operation string
}

func newIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Контур интеграции с внешними системами",
	}

	cmd.AddCommand(newIntegrationDispatcherCommand())
	return cmd
}

func newIntegrationDispatcherCommand() *cobra.Command {
	flags := &integrationFlags{
		system:    "github",
		resource:  "issue",
		operation: "get",
	}

	cmd := &cobra.Command{
		Use:   "dispatcher",
		Short: "Диагностика маршрута диспетчера интеграции",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newIntegrationService(cmd)
			route, err := service.Dispatch(context.Background(), integration.Request{
				System:    flags.system,
				Resource:  flags.resource,
				Operation: flags.operation,
			})
			printIntegrationRoute(cmd, route)
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.system, "system", flags.system, "Имя внешней системы")
	cmd.Flags().StringVar(&flags.resource, "resource", flags.resource, "Тип внешнего ресурса")
	cmd.Flags().StringVar(&flags.operation, "operation", flags.operation, "Тип операции интеграции")
	return cmd
}

func newIntegrationService(cmd *cobra.Command) *integration.Service {
	return integration.NewService(logging.New(cmd.ErrOrStderr()))
}

func printIntegrationRoute(cmd *cobra.Command, route integration.Route) {
	cmd.Printf("system=%s\nprovider=%s\nprovider-available=%t\nresource=%s\noperation=%s\nexpected-result=%s\n", route.System, route.Provider, route.ProviderAvailable, route.Resource, route.Operation, route.ExpectedResult)
	for _, diagnostic := range route.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
}
