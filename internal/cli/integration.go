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
	integrationType string
	system          string
	resource        string
	object          string
	operation       string
	repo            string
	number          int
	base            string
	head            string
	title           string
	body            string
	text            string
	query           string
	state           string
	scope           string
	path            string
	side            string
	channelID       string
	threadID        string
	messageID       string
	draft           bool
	line            int
	limit           int
}

const (
	integrationOutputText = "text"
	integrationOutputJSON = "json"
)

var integrationServiceFactory = func(cmd *cobra.Command) *integration.Service {
	return integration.NewConfiguredService(logging.New(cmd.ErrOrStderr()))
}

func newIntegrationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Контур интеграции с внешними системами",
	}
	cmd.PersistentFlags().String("format", integrationOutputText, "Формат вывода: text (по умолчанию) или json")

	cmd.AddCommand(newIntegrationDispatcherCommand())
	cmd.AddCommand(newIntegrationGitHubCommand())
	cmd.AddCommand(newIntegrationBitbucketCommand())
	cmd.AddCommand(newIntegrationMattermostCommand())
	cmd.AddCommand(newIntegrationTelegramCommand())
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

func newIntegrationBitbucketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bitbucket",
		Short: "Интеграция с Bitbucket как репозиторием",
	}
	cmd.AddCommand(newIntegrationSystemAuthCommand("bitbucket", "Bitbucket"))
	cmd.AddCommand(newIntegrationBitbucketRepoCommand())
	cmd.AddCommand(newIntegrationBitbucketPRCommand())
	return cmd
}

func newIntegrationBitbucketRepoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Операции с репозиториями Bitbucket",
	}
	cmd.AddCommand(newIntegrationRepositoryGetCommand("bitbucket", "Bitbucket"))
	return cmd
}

func newIntegrationBitbucketPRCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Операции с запросами на слияние Bitbucket",
	}
	cmd.AddCommand(newIntegrationMergeRequestGetCommand("bitbucket", "Bitbucket"))
	cmd.AddCommand(newIntegrationMergeRequestListCommand("bitbucket", "Bitbucket"))
	cmd.AddCommand(newIntegrationMergeRequestCreateCommand("bitbucket", "Bitbucket"))
	cmd.AddCommand(newIntegrationMergeRequestCommentsCommand("bitbucket", "Bitbucket"))
	cmd.AddCommand(newIntegrationMergeRequestCommentCommand("bitbucket", "Bitbucket"))
	return cmd
}

func newIntegrationMattermostCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mattermost",
		Short: "Интеграция с Mattermost как мессенджером",
	}
	cmd.AddCommand(newIntegrationSystemAuthCommand("mattermost", "Mattermost"))
	cmd.AddCommand(newIntegrationMattermostThreadCommand())
	cmd.AddCommand(newIntegrationMattermostMessageCommand())
	return cmd
}

func newIntegrationMattermostThreadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread",
		Short: "Операции с цепочками обсуждения Mattermost",
	}
	cmd.AddCommand(newIntegrationThreadGetCommand("mattermost", "Mattermost"))
	return cmd
}

func newIntegrationMattermostMessageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Операции с сообщениями Mattermost",
	}
	cmd.AddCommand(newIntegrationMessageCreateCommand("mattermost", "Mattermost"))
	return cmd
}

func newIntegrationTelegramCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Интеграция с Telegram как мессенджером",
	}
	cmd.AddCommand(newIntegrationSystemAuthCommand("telegram", "Telegram"))
	cmd.AddCommand(newIntegrationTelegramThreadCommand())
	cmd.AddCommand(newIntegrationTelegramMessageCommand())
	return cmd
}

func newIntegrationTelegramThreadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread",
		Short: "Операции с цепочками обсуждения Telegram",
	}
	cmd.AddCommand(newIntegrationThreadGetCommand("telegram", "Telegram"))
	return cmd
}

func newIntegrationTelegramMessageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Операции с сообщениями Telegram",
	}
	cmd.AddCommand(newIntegrationMessageCreateCommand("telegram", "Telegram"))
	return cmd
}

func newIntegrationGitHubPRCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Операции с pull request GitHub",
	}

	cmd.AddCommand(newIntegrationGitHubPRGetCommand())
	cmd.AddCommand(newIntegrationMergeRequestListCommand("github", "GitHub"))
	cmd.AddCommand(newIntegrationGitHubPRCreateCommand())
	cmd.AddCommand(newIntegrationMergeRequestCommentsCommand("github", "GitHub"))
	cmd.AddCommand(newIntegrationMergeRequestCommentCommand("github", "GitHub"))
	return cmd
}

func newIntegrationGitHubIssueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Операции с задачами GitHub",
	}

	cmd.AddCommand(newIntegrationGitHubIssueGetCommand())
	cmd.AddCommand(newIntegrationGitHubIssueCommentsCommand())
	cmd.AddCommand(newIntegrationGitHubIssueCommentCommand())
	return cmd
}

func newIntegrationGitHubIssueCommentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Операции с комментариями задачи GitHub",
	}
	cmd.AddCommand(newIntegrationGitHubIssueCommentCreateCommand())
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

func newIntegrationSystemAuthCommand(system string, label string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Диагностика авторизации " + label,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Проверка доступности авторизации " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				System:    system,
				Resource:  "auth",
				Operation: "status",
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationAuthStatus); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	})
	return cmd
}

func newIntegrationRepositoryGetCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение сведений о репозитории " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "repository",
				System:          system,
				Resource:        "repository",
				ObjectType:      "repository",
				Operation:       "get",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationRepository); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	return cmd
}

func newIntegrationMergeRequestGetCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение запроса на слияние " + label,
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
				IntegrationType: "repository",
				System:          system,
				Resource:        "merge-request",
				ObjectType:      "merge-request",
				Operation:       "get",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				Number:          flags.number,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationMergeRequest); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер запроса на слияние")
	return cmd
}

func newIntegrationMergeRequestCreateCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Создание запроса на слияние " + label,
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
				IntegrationType: "repository",
				System:          system,
				Resource:        "merge-request",
				ObjectType:      "merge-request",
				Operation:       "create",
				Repository:      flags.repo,
				RepoProvided:    true,
				Base:            flags.base,
				Head:            flags.head,
				Title:           flags.title,
				Body:            flags.body,
				Draft:           flags.draft,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationOperationResult); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.base, "base", "", "Базовая ветка запроса на слияние")
	cmd.Flags().StringVar(&flags.head, "head", "", "Ветка с изменениями")
	cmd.Flags().StringVar(&flags.title, "title", "", "Заголовок запроса на слияние")
	cmd.Flags().StringVar(&flags.body, "body", "", "Описание запроса на слияние")
	cmd.Flags().BoolVar(&flags.draft, "draft", false, "Создать запрос на слияние как draft, если система поддерживает режим")
	return cmd
}

func newIntegrationMergeRequestListCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{state: "closed", scope: "all", limit: 30}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"search"},
		Short:   "Поиск запросов на слияние " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "repository",
				System:          system,
				Resource:        "merge-request",
				ObjectType:      "merge-request",
				Operation:       "search",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				Query:           flags.query,
				State:           flags.state,
				Scope:           flags.scope,
				Limit:           flags.limit,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationMergeRequests); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.state, "state", flags.state, "Состояние запросов на слияние: open, closed или all")
	cmd.Flags().StringVar(&flags.scope, "scope", flags.scope, "Область отбора: all, authored или reviewer")
	cmd.Flags().StringVar(&flags.query, "query", "", "Строка поиска внешней системы")
	cmd.Flags().IntVar(&flags.limit, "limit", flags.limit, "Максимальное количество результатов")
	return cmd
}

func newIntegrationMergeRequestCommentsCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "comments",
		Short: "Получение замечаний ревизии запроса на слияние " + label,
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
				IntegrationType: "repository",
				System:          system,
				Resource:        "merge-request",
				ObjectType:      "merge-request",
				Operation:       "comments",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				Number:          flags.number,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationReviewRemarks); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер запроса на слияние")
	return cmd
}

func newIntegrationMergeRequestCommentCommand(system string, label string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Операции с комментариями запроса на слияние " + label,
	}
	cmd.AddCommand(newIntegrationMergeRequestCommentCreateCommand(system, label))
	cmd.AddCommand(newIntegrationMergeRequestCommentResolveCommand(system, label))
	return cmd
}

func newIntegrationMergeRequestCommentCreateCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{side: "RIGHT"}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Создание комментария запроса на слияние " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--number is required")
			}
			if !cmd.Flags().Changed("body") || strings.TrimSpace(flags.body) == "" {
				return fmt.Errorf("--body is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "repository",
				System:          system,
				Resource:        "comment",
				ObjectType:      "comment",
				Operation:       "create",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				Number:          flags.number,
				Body:            flags.body,
				Path:            flags.path,
				Line:            flags.line,
				Side:            flags.side,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationReviewRemarkOperation); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер запроса на слияние")
	cmd.Flags().StringVar(&flags.body, "body", "", "Текст комментария")
	cmd.Flags().StringVar(&flags.path, "path", "", "Путь файла для inline-комментария")
	cmd.Flags().IntVar(&flags.line, "line", 0, "Номер строки для inline-комментария")
	cmd.Flags().StringVar(&flags.side, "side", flags.side, "Сторона diff для inline-комментария: LEFT или RIGHT")
	return cmd
}

func newIntegrationMergeRequestCommentResolveCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Разрешение замечания ревизии запроса на слияние " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("thread") || strings.TrimSpace(flags.threadID) == "" {
				return fmt.Errorf("--thread is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "repository",
				System:          system,
				Resource:        "comment",
				ObjectType:      "comment",
				Operation:       "resolve",
				ThreadID:        flags.threadID,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationReviewRemarkOperation); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.threadID, "thread", "", "Идентификатор review thread")
	return cmd
}

func newIntegrationThreadGetCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение цепочки обсуждения " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("thread") || strings.TrimSpace(flags.threadID) == "" {
				return fmt.Errorf("--thread is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "messenger",
				System:          system,
				Resource:        "thread",
				ObjectType:      "thread",
				Operation:       "get",
				ThreadID:        flags.threadID,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationThread); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.threadID, "thread", "", "Идентификатор цепочки обсуждения")
	return cmd
}

func newIntegrationMessageCreateCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Создание сообщения " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("text") || strings.TrimSpace(flags.text) == "" {
				return fmt.Errorf("--text is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "messenger",
				System:          system,
				Resource:        "message",
				ObjectType:      "message",
				Operation:       "create",
				ChannelID:       flags.channelID,
				ThreadID:        flags.threadID,
				MessageID:       flags.messageID,
				Text:            flags.text,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationMessage); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.channelID, "channel", "", "Идентификатор канала или пространства сообщений")
	cmd.Flags().StringVar(&flags.threadID, "thread", "", "Идентификатор цепочки обсуждения")
	cmd.Flags().StringVar(&flags.messageID, "message", "", "Идентификатор сообщения для ответа")
	cmd.Flags().StringVar(&flags.text, "text", "", "Текст сообщения")
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

func newIntegrationGitHubIssueCommentCreateCommand() *cobra.Command {
	flags := &integrationFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Создание комментария задачи GitHub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--number is required")
			}
			if !cmd.Flags().Changed("body") || strings.TrimSpace(flags.body) == "" {
				return fmt.Errorf("--body is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(context.Background(), integration.Request{
				IntegrationType: "tracker",
				System:          "github",
				Resource:        "comment",
				ObjectType:      "comment",
				Operation:       "create",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				Number:          flags.number,
				Body:            flags.body,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printGitHubIssueComments); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер задачи GitHub")
	cmd.Flags().StringVar(&flags.body, "body", "", "Текст комментария")
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
		Use:     "dispatcher",
		Aliases: []string{"dispatch"},
		Short:   "Диагностика маршрута диспетчера интеграции",
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			route, err := service.Dispatch(context.Background(), integration.Request{
				IntegrationType: flags.integrationType,
				System:          flags.system,
				SystemProvided:  cmd.Flags().Changed("system"),
				Resource:        flags.resource,
				ObjectType:      flags.object,
				Operation:       flags.operation,
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

	cmd.Flags().StringVar(&flags.integrationType, "type", flags.integrationType, "Тип интеграции")
	cmd.Flags().StringVar(&flags.system, "system", flags.system, "Имя внешней системы")
	cmd.Flags().StringVar(&flags.resource, "resource", flags.resource, "Тип внешнего ресурса")
	cmd.Flags().StringVar(&flags.object, "object", flags.object, "Тип канонического объекта")
	cmd.Flags().StringVar(&flags.operation, "operation", flags.operation, "Тип операции интеграции")
	return cmd
}

func newIntegrationService(cmd *cobra.Command) *integration.Service {
	return integrationServiceFactory(cmd)
}

func printIntegrationRoute(cmd *cobra.Command, route integration.Route) {
	cmd.Printf("type=%s\nsystem=%s\nprovider=%s\nprovider-type=%s\nprovider-available=%t\nresource=%s\nobject=%s\noperation=%s\nexpected-result=%s\n", route.IntegrationType, route.System, route.Provider, route.ProviderType, route.ProviderAvailable, route.Resource, route.ObjectType, route.Operation, route.ExpectedResult)
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

func printIntegrationAuthStatus(cmd *cobra.Command, response integration.Response) {
	status := response.AuthStatus
	if status == nil {
		printFailure(cmd, response)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nstate=%s\navailable=%t\nauthenticated=%t\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.State, status.Available, status.Authenticated, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printIntegrationRepository(cmd *cobra.Command, response integration.Response) {
	repository := response.Repository
	if repository == nil && response.RepositoryRef != nil {
		repository = &integration.Repository{
			System:        response.RepositoryRef.System,
			FullName:      response.RepositoryRef.FullName,
			Owner:         response.RepositoryRef.Owner,
			Name:          response.RepositoryRef.Name,
			Description:   response.RepositoryRef.Description,
			DefaultBranch: response.RepositoryRef.DefaultBranch,
			URL:           response.RepositoryRef.URL,
		}
	}
	if repository == nil {
		printFailure(cmd, response)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nfull_name=%s\nowner=%s\nname=%s\ndefault_branch=%s\nurl=%s\n", repository.System, response.Resource, response.Operation, repository.FullName, repository.Owner, repository.Name, repository.DefaultBranch, repository.URL)
	printMultilineField(cmd, "description", repository.Description)
}

func printIntegrationMergeRequest(cmd *cobra.Command, response integration.Response) {
	pr := response.MergeRequest
	if pr == nil && response.PullRequest != nil {
		pr = &integration.MergeRequest{
			System:         response.PullRequest.System,
			Repository:     response.PullRequest.Repository,
			Number:         response.PullRequest.Number,
			Title:          response.PullRequest.Title,
			Body:           response.PullRequest.Body,
			State:          response.PullRequest.State,
			Author:         integration.User{System: response.PullRequest.Author.System, Login: response.PullRequest.Author.Login, Name: response.PullRequest.Author.Name, URL: response.PullRequest.Author.URL},
			ReviewDecision: response.PullRequest.ReviewDecision,
			BaseRef:        response.PullRequest.BaseRef,
			HeadRef:        response.PullRequest.HeadRef,
			URL:            response.PullRequest.URL,
			CreatedAt:      response.PullRequest.CreatedAt,
			UpdatedAt:      response.PullRequest.UpdatedAt,
		}
	}
	if pr == nil {
		printFailure(cmd, response)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nnumber=%d\ntitle=%s\nstate=%s\nauthor_login=%s\nauthor_name=%s\nauthor_url=%s\nreview_decision=%s\nbase_ref=%s\nhead_ref=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", pr.System, response.Resource, response.Operation, pr.Repository, pr.Number, pr.Title, pr.State, pr.Author.Login, pr.Author.Name, pr.Author.URL, pr.ReviewDecision, pr.BaseRef, pr.HeadRef, pr.URL, pr.CreatedAt, pr.UpdatedAt)
	for _, trait := range pr.Traits {
		cmd.Printf("trait=%s\n", trait)
	}
	printIssueBody(cmd, pr.Body)
}

func printIntegrationMergeRequests(cmd *cobra.Command, response integration.Response) {
	if response.Failure != nil {
		printFailure(cmd, response)
		return
	}
	repository := ""
	state := ""
	scope := ""
	if response.Metadata != nil {
		repository = response.Metadata["repository"]
		state = response.Metadata["state"]
		scope = response.Metadata["scope"]
	}
	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nstate=%s\nscope=%s\nmerge_request_count=%d\n", response.System, response.Resource, response.Operation, repository, state, scope, len(response.MergeRequests))
	for _, pr := range response.MergeRequests {
		cmd.Printf("merge_request_number=%d\nmerge_request_title=%s\nmerge_request_state=%s\nmerge_request_author_login=%s\nmerge_request_author_name=%s\nmerge_request_review_decision=%s\nmerge_request_base_ref=%s\nmerge_request_head_ref=%s\nmerge_request_url=%s\nmerge_request_updated_at=%s\n", pr.Number, pr.Title, pr.State, pr.Author.Login, pr.Author.Name, pr.ReviewDecision, pr.BaseRef, pr.HeadRef, pr.URL, pr.UpdatedAt)
	}
}

func printIntegrationOperationResult(cmd *cobra.Command, response integration.Response) {
	if response.MergeRequest != nil {
		printIntegrationMergeRequest(cmd, response)
	}
	result := response.OperationResult
	if result == nil {
		printFailure(cmd, response)
		return
	}
	cmd.Printf("system=%s\nobject=%s\noperation=%s\nstatus=%s\nexternal_id=%s\nurl=%s\nhttp_status=%d\nmethod=%s\nendpoint=%s\nidempotent=%t\nmessage=%s\n", result.System, result.ObjectType, result.Operation, result.Status, result.ExternalID, result.URL, result.HTTPStatus, result.Method, result.Endpoint, result.Idempotent, result.Message)
	for _, diagnostic := range result.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	if result.Failure != nil {
		cmd.Printf("failure_kind=%s\nretryable=%t\nfailure_message=%s\n", result.Failure.Kind, result.Failure.Retryable, result.Failure.Message)
	}
}

func printIntegrationReviewRemarkOperation(cmd *cobra.Command, response integration.Response) {
	if len(response.ReviewRemarks) > 0 {
		printIntegrationReviewRemarks(cmd, response)
	}
	if response.OperationResult != nil {
		printIntegrationOperationResult(cmd, response)
		return
	}
	if len(response.ReviewRemarks) == 0 {
		printFailure(cmd, response)
	}
}

func printIntegrationReviewRemarks(cmd *cobra.Command, response integration.Response) {
	if len(response.ReviewRemarks) == 0 {
		printFailure(cmd, response)
		if response.Failure == nil {
			cmd.Printf("system=%s\nresource=%s\noperation=%s\nremark_count=0\n", response.System, response.Resource, response.Operation)
		}
		return
	}
	cmd.Printf("system=%s\nresource=%s\noperation=%s\nremark_count=%d\n", response.System, response.Resource, response.Operation, len(response.ReviewRemarks))
	for _, remark := range response.ReviewRemarks {
		cmd.Printf("remark_id=%s\nremark_thread_id=%s\nremark_repository=%s\nremark_merge_request_number=%d\nremark_author_login=%s\nremark_author_name=%s\nremark_state=%s\nremark_path=%s\nremark_line=%d\nremark_url=%s\nremark_created_at=%s\nremark_updated_at=%s\n", remark.ExternalID, remark.ReplyToID, remark.Repository, remark.MergeRequestNumber, remark.Author.Login, remark.Author.Name, remark.State, remark.Path, remark.Line, remark.URL, remark.CreatedAt, remark.UpdatedAt)
		printMultilineField(cmd, "remark_body", remark.Body)
	}
}

func printIntegrationThread(cmd *cobra.Command, response integration.Response) {
	thread := response.Conversation
	if thread == nil {
		printFailure(cmd, response)
		return
	}
	cmd.Printf("system=%s\nresource=%s\noperation=%s\nspace_id=%s\nthread_id=%s\nroot_id=%s\nmessage_count=%d\n", thread.System, response.Resource, response.Operation, thread.SpaceID, thread.ThreadID, thread.RootID, len(thread.Messages))
	for _, message := range thread.Messages {
		printMessage(cmd, message)
	}
}

func printIntegrationMessage(cmd *cobra.Command, response integration.Response) {
	if response.Message == nil {
		printFailure(cmd, response)
		return
	}
	printMessage(cmd, *response.Message)
	if response.OperationResult != nil {
		cmd.Printf("operation_status=%s\noperation_message=%s\n", response.OperationResult.Status, response.OperationResult.Message)
	}
}

func printMessage(cmd *cobra.Command, message integration.Message) {
	cmd.Printf("message_system=%s\nspace_id=%s\nthread_id=%s\nmessage_id=%s\nauthor_login=%s\nauthor_name=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", message.System, message.SpaceID, message.ThreadID, message.MessageID, message.Author.Login, message.Author.Name, message.URL, message.CreatedAt, message.UpdatedAt)
	printMultilineField(cmd, "message_body", message.Body)
}

func printFailure(cmd *cobra.Command, response integration.Response) {
	if response.Failure == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nstatus=%s\nmessage=integration response did not return a normalized object\n", response.System, response.Resource, response.Operation, response.Status)
		return
	}
	cmd.Printf("system=%s\nresource=%s\noperation=%s\nstatus=%s\nfailure_kind=%s\nretryable=%t\nfailure_message=%s\n", response.System, response.Resource, response.Operation, response.Status, response.Failure.Kind, response.Failure.Retryable, response.Failure.Message)
	for _, diagnostic := range response.Failure.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
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
