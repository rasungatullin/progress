package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

type integrationFlags struct {
	system    string
	resource  string
	operation string
	repo      string
	number    int
	base      string
	head      string
	title     string
	body      string
	draft     bool
}

const (
	integrationOutputText = "text"
	integrationOutputJSON = "json"
)

var integrationServiceFactory = func(cmd *cobra.Command) *integration.Service {
	return integration.NewService(logging.New(cmd.ErrOrStderr()))
}

func newIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Контур интеграции с внешними системами",
	}
	cmd.PersistentFlags().String("format", integrationOutputText, "Формат вывода: text (по умолчанию) или json")

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
	cmd.AddCommand(newIntegrationGitHubIssueCommand())
	cmd.AddCommand(newIntegrationGitHubPRCommand())
	return cmd
}

func newIntegrationGitHubPRCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Операции с pull request GitHub",
	}

	cmd.AddCommand(newIntegrationGitHubPRGetCommand())
	cmd.AddCommand(newIntegrationGitHubPRCreateCommand())
	return cmd
}

func newIntegrationGitHubIssueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Операции с задачами GitHub",
	}

	cmd.AddCommand(newIntegrationGitHubIssueGetCommand())
	cmd.AddCommand(newIntegrationGitHubIssueCommentsCommand())
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
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:    "github",
				Resource:  "auth",
				Operation: "status",
			})
			if err := printIntegrationResponseOrJSON(cmd, response, format, printGitHubAuthStatus); err != nil {
				return err
			}
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
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:       "github",
				Resource:     "repo",
				Operation:    "get",
				Repository:   flags.repo,
				RepoProvided: cmd.Flags().Changed("repo"),
			})
			if err := printIntegrationResponseOrJSON(cmd, response, format, printGitHubRepository); err != nil {
				return err
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	return cmd
}

func newIntegrationGitHubIssueGetCommand() *cobra.Command {
	flags := &integrationFlags{}

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение задачи GitHub по номеру",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--number is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:       "github",
				Resource:     "issue",
				Operation:    "get",
				Repository:   flags.repo,
				RepoProvided: cmd.Flags().Changed("repo"),
				Number:       flags.number,
			})
			if err := printIntegrationResponseOrJSON(cmd, response, format, printGitHubIssue); err != nil {
				return err
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер задачи GitHub")
	return cmd
}

func newIntegrationGitHubIssueCommentsCommand() *cobra.Command {
	flags := &integrationFlags{}

	cmd := &cobra.Command{
		Use:   "comments",
		Short: "Получение комментариев задачи GitHub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--number is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:       "github",
				Resource:     "issue",
				Operation:    "comments",
				Repository:   flags.repo,
				RepoProvided: cmd.Flags().Changed("repo"),
				Number:       flags.number,
			})
			if err := printIntegrationResponseOrJSON(cmd, response, format, printGitHubIssueComments); err != nil {
				return err
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер задачи GitHub")
	return cmd
}

func newIntegrationGitHubPRCreateCommand() *cobra.Command {
	flags := &integrationFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Создание pull request GitHub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("repo") || strings.TrimSpace(flags.repo) == "" {
				return fmt.Errorf("--repo is required")
			}
			if !cmd.Flags().Changed("base") || strings.TrimSpace(flags.base) == "" {
				return fmt.Errorf("--base is required")
			}
			if !cmd.Flags().Changed("head") || strings.TrimSpace(flags.head) == "" {
				return fmt.Errorf("--head is required")
			}
			if !cmd.Flags().Changed("title") || strings.TrimSpace(flags.title) == "" {
				return fmt.Errorf("--title is required")
			}
			if err := validateSingleLineFlagValue("title", flags.title); err != nil {
				return err
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:     "github",
				Resource:   "pr",
				Operation:  "create",
				Repository: flags.repo,
				Base:       flags.base,
				Head:       flags.head,
				Title:      flags.title,
				Body:       flags.body,
				Draft:      flags.draft,
			})
			if err := printIntegrationResponseOrJSON(cmd, response, format, printGitHubPullRequestStatus); err != nil {
				return err
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().StringVar(&flags.base, "base", "", "Базовая ветка pull request")
	cmd.Flags().StringVar(&flags.head, "head", "", "Ветка с изменениями")
	cmd.Flags().StringVar(&flags.title, "title", "", "Заголовок pull request")
	cmd.Flags().StringVar(&flags.body, "body", "", "Описание pull request")
	cmd.Flags().BoolVar(&flags.draft, "draft", false, "Создать pull request как draft")
	return cmd
}

func newIntegrationGitHubPRGetCommand() *cobra.Command {
	flags := &integrationFlags{}

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение pull request GitHub по номеру",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--number is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:       "github",
				Resource:     "pr",
				Operation:    "get",
				Repository:   flags.repo,
				RepoProvided: cmd.Flags().Changed("repo"),
				Number:       flags.number,
			})
			if err := printIntegrationResponseOrJSON(cmd, response, format, printGitHubPullRequest); err != nil {
				return err
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер pull request GitHub")
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
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			route, err := service.Dispatch(context.Background(), integration.Request{
				System:    flags.system,
				Resource:  flags.resource,
				Operation: flags.operation,
			})
			if err := printIntegrationRouteOrJSON(cmd, route, format); err != nil {
				return err
			}
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

func printIntegrationRouteOrJSON(cmd *cobra.Command, route integration.Route, format string) error {
	if format == integrationOutputJSON {
		return printIntegrationJSON(cmd, route)
	}

	printIntegrationRoute(cmd, route)
	return nil
}

func printIntegrationResponseOrJSON(cmd *cobra.Command, response integration.Response, format string, textPrinter func(*cobra.Command, integration.Response)) error {
	if format == integrationOutputJSON {
		return printIntegrationJSON(cmd, response)
	}

	if textPrinter != nil {
		textPrinter(cmd, response)
	}

	return nil
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

func printGitHubIssue(cmd *cobra.Command, response integration.Response) {
	issue := response.Issue
	if issue != nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\ntitle=%s\nstate=%s\nauthor_login=%s\nauthor_name=%s\nauthor_url=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", issue.System, response.Resource, response.Operation, issue.Repository, issue.Number, issue.Title, issue.State, issue.Author.Login, issue.Author.Name, issue.Author.URL, issue.URL, issue.CreatedAt, issue.UpdatedAt)
		for _, label := range issue.Labels {
			cmd.Printf("label=%s\n", label)
		}
		for _, assignee := range issue.Assignees {
			cmd.Printf("assignee_login=%s\n", assignee.Login)
			if assignee.Name != "" {
				cmd.Printf("assignee_name=%s\n", assignee.Name)
			}
			if assignee.URL != "" {
				cmd.Printf("assignee_url=%s\n", assignee.URL)
			}
		}
		printIssueBody(cmd, issue.Body)
		return
	}

	status := response.IssueStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nmessage=GitHub issue get did not return a normalized issue\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.Number, status.State, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printGitHubIssueComments(cmd *cobra.Command, response integration.Response) {
	if response.Comments != nil {
		repository := ""
		number := 0
		if len(response.Comments) > 0 {
			repository = response.Comments[0].Repository
			number = response.Comments[0].Number
		} else if response.Metadata != nil {
			repository = response.Metadata["repository"]
			number, _ = strconv.Atoi(response.Metadata["number"])
		}
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\ncomment_count=%d\n", response.System, response.Resource, response.Operation, repository, number, len(response.Comments))
		for _, comment := range response.Comments {
			cmd.Printf("comment_author_login=%s\ncomment_author_name=%s\ncomment_author_url=%s\ncomment_url=%s\ncomment_created_at=%s\ncomment_updated_at=%s\n", comment.Author.Login, comment.Author.Name, comment.Author.URL, comment.URL, comment.CreatedAt, comment.UpdatedAt)
			printCommentBody(cmd, comment.Body)
		}
		return
	}

	status := response.IssueStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nmessage=GitHub issue comments did not return normalized comments\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.Number, status.State, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printGitHubPullRequestStatus(cmd *cobra.Command, response integration.Response) {
	status := response.PullRequestStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nmessage=GitHub pr create did not return a normalized pull request status\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nbase=%s\nhead=%s\ntitle=%s\ndraft=%t\nstate=%s\nnumber=%d\nurl=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.Base, status.Head, status.Title, status.Draft, status.State, status.Number, status.URL, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printGitHubPullRequest(cmd *cobra.Command, response integration.Response) {
	pr := response.PullRequest
	if pr != nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\ntitle=%s\nstate=%s\nauthor_login=%s\nauthor_name=%s\nauthor_url=%s\nreview_decision=%s\nbase_ref=%s\nhead_ref=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", pr.System, response.Resource, response.Operation, pr.Repository, pr.Number, pr.Title, pr.State, pr.Author.Login, pr.Author.Name, pr.Author.URL, pr.ReviewDecision, pr.BaseRef, pr.HeadRef, pr.URL, pr.CreatedAt, pr.UpdatedAt)
		for _, label := range pr.Labels {
			cmd.Printf("label=%s\n", label)
		}
		printIssueBody(cmd, pr.Body)
		return
	}

	status := response.PullRequestStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nmessage=GitHub pr get did not return a normalized pull request\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.Number, status.State, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printMultilineField(cmd *cobra.Command, key string, value string) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if value == "" {
		return
	}
	cmd.Printf("%s=%s\n", key, value)
}

func printIssueBody(cmd *cobra.Command, value string) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if value == "" {
		return
	}
	for _, line := range strings.Split(value, "\n") {
		cmd.Printf("body=%s\n", line)
	}

	cmd.Printf("body_raw=%s\n", strconv.Quote(strings.ReplaceAll(value, "\r\n", "\n")))
}

func printCommentBody(cmd *cobra.Command, value string) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if value == "" {
		return
	}
	for _, line := range strings.Split(value, "\n") {
		cmd.Printf("comment_body=%s\n", line)
	}

	cmd.Printf("comment_body_raw=%s\n", strconv.Quote(strings.ReplaceAll(value, "\r\n", "\n")))
}

func validateSingleLineFlagValue(name string, value string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("--%s must not contain control characters or line breaks", name)
		}
	}

	return nil
}

func printIntegrationJSON(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	cmd.Println(string(encoded))
	return nil
}

func integrationOutputFormat(cmd *cobra.Command) (string, error) {
	flag := cmd.Flags().Lookup("format")
	if flag == nil {
		flag = cmd.InheritedFlags().Lookup("format")
	}
	if flag == nil {
		return integrationOutputText, nil
	}

	format := strings.ToLower(strings.TrimSpace(flag.Value.String()))
	if format == "" {
		format = integrationOutputText
	}

	if format != integrationOutputText && format != integrationOutputJSON {
		return "", fmt.Errorf("--format supports only text or json")
	}

	return format, nil
}
