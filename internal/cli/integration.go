package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

type integrationFlags struct {
	system    string
	resource  string
	operation string
	repo      string
}

var integrationServiceFactory = func(cmd *cobra.Command) *integration.Service {
	return integration.NewService(logging.New(cmd.ErrOrStderr()))
}

func newIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Контур интеграции с внешними системами",
	}

	cmd.AddCommand(newIntegrationDispatcherCommand())
	cmd.AddCommand(newIntegrationGitHubCommand())
	return cmd
}

func newIntegrationGitHubCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Интеграция с GitHub через gh",
	}

	cmd.AddCommand(newIntegrationGitHubAuthCommand())
	cmd.AddCommand(newIntegrationGitHubRepoCommand())
	return cmd
}

func newIntegrationGitHubRepoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Операции с репозиториями GitHub",
	}

	cmd.AddCommand(newIntegrationGitHubRepoGetCommand())
	return cmd
}

func newIntegrationGitHubAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Диагностика авторизации GitHub",
	}

	cmd.AddCommand(newIntegrationGitHubAuthStatusCommand())
	return cmd
}

func newIntegrationGitHubAuthStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Проверка доступности gh и состояния авторизации",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:    "github",
				Resource:  "auth",
				Operation: "status",
			})
			printGitHubAuthStatus(cmd, response)
			if err != nil {
				return err
			}

			return nil
		},
	}
}

func newIntegrationGitHubRepoGetCommand() *cobra.Command {
	flags := &integrationFlags{}

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение сведений о репозитории GitHub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(flags.repo) == "" {
				return fmt.Errorf("--repo is required")
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:     "github",
				Resource:   "repo",
				Operation:  "get",
				Repository: flags.repo,
			})
			printGitHubRepository(cmd, response)
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
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
	return integrationServiceFactory(cmd)
}

func printIntegrationRoute(cmd *cobra.Command, route integration.Route) {
	cmd.Printf("system=%s\nprovider=%s\nprovider-available=%t\nresource=%s\noperation=%s\nexpected-result=%s\n", route.System, route.Provider, route.ProviderAvailable, route.Resource, route.Operation, route.ExpectedResult)
	for _, diagnostic := range route.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
}

func printGitHubAuthStatus(cmd *cobra.Command, response integration.Response) {
	status := response.AuthStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nstate=unknown\nmessage=GitHub auth status did not return a normalized result\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nstate=%s\navailable=%t\nauthenticated=%t\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.State, status.Available, status.Authenticated, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printGitHubRepository(cmd *cobra.Command, response integration.Response) {
	repository := response.RepositoryRef
	if repository != nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nfull_name=%s\nowner=%s\nname=%s\ndefault_branch=%s\nurl=%s\n", repository.System, response.Resource, response.Operation, repository.FullName, repository.Owner, repository.Name, repository.DefaultBranch, repository.URL)
		printMultilineField(cmd, "description", repository.Description)
		return
	}

	status := response.RepositoryStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nmessage=GitHub repo get did not return a normalized repository\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.State, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printMultilineField(cmd *cobra.Command, key string, value string) {
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		cmd.Printf("%s=%s\n", key, line)
	}
}
