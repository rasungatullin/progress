package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/integration"
	"github.com/rasungatullin/progress/internal/integration/secrets"
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
	input           string
	inputFile       string
	externalID      string
	draft           bool
	line            int
	limit           int
	labels          []string
	excludeLabels   []string
	fields          []string
}

// Оставлено только для сборочной совместимости старых тестовых и внутренних
// потребителей. Хранилище приватных значений не публикуется в дереве CLI.
type integrationPrivateResult struct {
	Status   string `json:"status"`
	Name     string `json:"name,omitempty"`
	Store    string `json:"store"`
	Location string `json:"location,omitempty"`
}

var integrationPrivateStoreFactory = func(cmd *cobra.Command) (secrets.Store, secrets.Descriptor, error) {
	repoRoot := ""
	if output, err := os.Getwd(); err == nil {
		repoRoot = output
	}
	loaded, err := configuration.LoadIntegrationPrivateStoreConfig(repoRoot, os.ReadFile)
	if err != nil {
		return nil, secrets.Descriptor{}, err
	}
	return secrets.NewStore(loaded.Config, loaded.ConfigHome)
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

	cmd.AddCommand(newIntegrationOperationsCommand())
	cmd.AddCommand(newIntegrationStatusCommand())
	cmd.AddCommand(newIntegrationInvokeCommand())
	cmd.AddCommand(newIntegrationIssueCommand())
	cmd.AddCommand(newIntegrationRepoCommand())
	cmd.AddCommand(newIntegrationMessengerCommand())
	cmd.AddCommand(newIntegrationWikiCommand())
	return cmd
}

// Типо-ориентированные команды ниже используют тот же реестр, что и issue.
// Система выбирается по --system либо по default_systems.
func newIntegrationRepoCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Операции с объектами типа repo"}
	cmd.AddCommand(newTypeOrientedRepoGetCommand())
	merge := &cobra.Command{Use: "merge-request", Short: "Операции с запросами на слияние"}
	merge.AddCommand(newTypeOrientedMergeRequestGetCommand(), newTypeOrientedMergeRequestSearchCommand(), newTypeOrientedMergeRequestCreateCommand())
	comment := &cobra.Command{Use: "comment", Short: "Комментарии запроса на слияние"}
	comment.AddCommand(newTypeOrientedMergeRequestCommentListCommand(), newTypeOrientedMergeRequestCommentCreateCommand())
	remark := &cobra.Command{Use: "review-remark", Short: "Замечания ревизии запроса на слияние"}
	remark.AddCommand(newTypeOrientedReviewRemarkListCommand(), newTypeOrientedReviewRemarkCreateCommand(), newTypeOrientedReviewRemarkReplyCommand(), newTypeOrientedReviewRemarkResolveCommand(), newTypeOrientedReviewRemarkUnresolveCommand())
	merge.AddCommand(comment, remark)
	cmd.AddCommand(merge)
	return cmd
}

func newIntegrationMessengerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "messenger", Short: "Операции с объектами типа messenger"}
	thread := &cobra.Command{Use: "thread", Short: "Цепочки обсуждения"}
	thread.AddCommand(newTypeOrientedMessengerThreadGetCommand())
	cmd.AddCommand(thread)
	message := &cobra.Command{Use: "message", Short: "Сообщения"}
	message.AddCommand(newTypeOrientedMessengerMessageCreateCommand())
	cmd.AddCommand(message)
	return cmd
}

func newIntegrationWikiCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "wiki", Short: "Операции с объектами типа wiki"}
	page := &cobra.Command{Use: "page", Short: "Страницы документации"}
	page.AddCommand(newTypeOrientedWikiPageGetCommand(), newTypeOrientedWikiPageSearchCommand())
	cmd.AddCommand(page)
	return cmd
}

func bindTypeSystem(cmd *cobra.Command, flags *integrationFlags) {
	cmd.Flags().StringVar(&flags.system, "system", "", "Имя системы из конфигурации")
}

func executeTypeRequest(cmd *cobra.Command, flags *integrationFlags, request integration.Request, printer func(*cobra.Command, integration.Response)) error {
	format, err := integrationOutputFormat(cmd)
	if err != nil {
		return err
	}
	request.System = flags.system
	request.SystemProvided = cmd.Flags().Changed("system")
	response, executeErr := newIntegrationService(cmd).Execute(cmd.Context(), request)
	if printErr := printIntegrationResponseOrJSON(cmd, response, format, printer); printErr != nil {
		return printErr
	}
	return executeErr
}

func newTypeOrientedRepoGetCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "get", Short: "Получение репозитория", RunE: func(cmd *cobra.Command, _ []string) error {
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: "repo", ObjectType: "repository", Operation: "get", Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo")}, printIntegrationRepository)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	return cmd
}

func newTypeOrientedMergeRequestGetCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "get", Short: "Получение запроса на слияние", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("number") {
			return fmt.Errorf("--number is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: "merge-request", ObjectType: "merge-request", Operation: "get", Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), MergeRequestNumber: flags.number}, printIntegrationMergeRequest)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер запроса на слияние")
	return cmd
}

func newTypeOrientedMergeRequestSearchCommand() *cobra.Command {
	flags := &integrationFlags{state: "closed", scope: "all", limit: 30}
	cmd := &cobra.Command{Use: "search", Aliases: []string{"list"}, Short: "Поиск запросов на слияние", RunE: func(cmd *cobra.Command, _ []string) error {
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: "merge-request", ObjectType: "merge-request", Operation: "search", Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), Query: flags.query, State: flags.state, Scope: flags.scope, Limit: flags.limit}, printIntegrationMergeRequests)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.query, "query", "", "Строка поиска")
	cmd.Flags().StringVar(&flags.state, "state", flags.state, "Состояние запросов на слияние: open, closed или all")
	cmd.Flags().StringVar(&flags.scope, "scope", flags.scope, "Область отбора: all, authored или reviewer")
	cmd.Flags().IntVar(&flags.limit, "limit", flags.limit, "Предельное число результатов")
	return cmd
}

func newTypeOrientedMergeRequestCreateCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "create", Short: "Создание запроса на слияние", RunE: func(cmd *cobra.Command, _ []string) error {
		for _, name := range []string{"repo", "base", "head", "title"} {
			if !cmd.Flags().Changed(name) || strings.TrimSpace(cmd.Flag(name).Value.String()) == "" {
				return fmt.Errorf("--%s is required", name)
			}
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: "merge-request", ObjectType: "merge-request", Operation: "create", Repository: flags.repo, RepoProvided: true, Base: flags.base, Head: flags.head, Title: flags.title, Body: flags.body, Draft: flags.draft}, printIntegrationOperationResult)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.base, "base", "", "Базовая ветка запроса на слияние")
	cmd.Flags().StringVar(&flags.head, "head", "", "Ветка с изменениями")
	cmd.Flags().StringVar(&flags.title, "title", "", "Заголовок запроса на слияние")
	cmd.Flags().StringVar(&flags.body, "body", "", "Описание запроса на слияние")
	cmd.Flags().BoolVar(&flags.draft, "draft", false, "Создать запрос как черновик")
	return cmd
}

func newTypeOrientedMergeRequestCommentListCommand() *cobra.Command {
	return newTypeOrientedMergeRequestListLikeCommand("list", "list", "merge-request-comment", printIntegrationReviewRemarks)
}
func newTypeOrientedReviewRemarkListCommand() *cobra.Command {
	return newTypeOrientedMergeRequestListLikeCommand("list", "list", "review-remark", printIntegrationReviewRemarks)
}
func newTypeOrientedMergeRequestListLikeCommand(use, operation, object string, printer func(*cobra.Command, integration.Response)) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: use, Short: "Получение элементов запроса на слияние", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("number") {
			return fmt.Errorf("--number is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: object, ObjectType: object, Operation: operation, Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), MergeRequestNumber: flags.number}, printer)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер запроса на слияние")
	return cmd
}

func newTypeOrientedMergeRequestCommentCreateCommand() *cobra.Command {
	return newTypeOrientedReviewRemarkCreateCommandWithUse("create", false)
}
func newTypeOrientedReviewRemarkCreateCommand() *cobra.Command {
	return newTypeOrientedReviewRemarkCreateCommandWithUse("create", true)
}
func newTypeOrientedReviewRemarkCreateCommandWithUse(use string, inline bool) *cobra.Command {
	flags := &integrationFlags{side: "RIGHT"}
	cmd := &cobra.Command{Use: use, Short: "Создание комментария запроса на слияние", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("number") {
			return fmt.Errorf("--number is required")
		}
		if strings.TrimSpace(flags.body) == "" {
			return fmt.Errorf("--body is required")
		}
		if inline && (strings.TrimSpace(flags.path) == "" || flags.line <= 0) {
			return fmt.Errorf("--path and --line are required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: "comment", ObjectType: "merge-request-comment", Operation: "create", Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), MergeRequestNumber: flags.number, Body: flags.body, Path: flags.path, Line: flags.line, Side: flags.side}, printIntegrationOperationResult)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер запроса на слияние")
	cmd.Flags().StringVar(&flags.body, "body", "", "Текст комментария")
	if inline {
		cmd.Flags().StringVar(&flags.path, "path", "", "Путь файла для inline-замечания")
		cmd.Flags().IntVar(&flags.line, "line", 0, "Номер строки для inline-замечания")
		cmd.Flags().StringVar(&flags.side, "side", flags.side, "Сторона diff: LEFT или RIGHT")
	}
	return cmd
}

func newTypeOrientedReviewRemarkReplyCommand() *cobra.Command {
	return newTypeOrientedReviewRemarkActionCommand("reply", "Ответ на замечание ревизии", true)
}
func newTypeOrientedReviewRemarkResolveCommand() *cobra.Command {
	return newTypeOrientedReviewRemarkActionCommand("resolve", "Разрешение замечания ревизии", false)
}
func newTypeOrientedReviewRemarkUnresolveCommand() *cobra.Command {
	return newTypeOrientedReviewRemarkActionCommand("unresolve", "Отмена разрешения замечания ревизии", false)
}
func newTypeOrientedReviewRemarkActionCommand(operation, short string, bodyRequired bool) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: operation, Short: short, RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.threadID) == "" {
			return fmt.Errorf("--thread is required")
		}
		if bodyRequired && strings.TrimSpace(flags.body) == "" {
			return fmt.Errorf("--body is required")
		}
		object, resource := "comment", "comment"
		if operation == "unresolve" {
			object, resource = "review-remark", "review-remark"
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "repo", Resource: resource, ObjectType: object, Operation: operation, ThreadID: flags.threadID, Body: flags.body}, printIntegrationOperationResult)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.threadID, "thread", "", "Идентификатор цепочки замечания")
	if bodyRequired {
		cmd.Flags().StringVar(&flags.body, "body", "", "Текст ответа")
	}
	return cmd
}

func newTypeOrientedMessengerThreadGetCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "get", Short: "Получение цепочки обсуждения", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.threadID) == "" {
			return fmt.Errorf("--thread is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "messenger", Resource: "thread", ObjectType: "thread", Operation: "get", ThreadID: flags.threadID}, printIntegrationThread)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.threadID, "thread", "", "Идентификатор цепочки обсуждения")
	return cmd
}
func newTypeOrientedMessengerMessageCreateCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "create", Short: "Создание сообщения", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.text) == "" {
			return fmt.Errorf("--text is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "messenger", Resource: "message", ObjectType: "message", Operation: "create", ChannelID: flags.channelID, ThreadID: flags.threadID, MessageID: flags.messageID, Text: flags.text}, printIntegrationMessage)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.channelID, "channel", "", "Идентификатор канала")
	cmd.Flags().StringVar(&flags.threadID, "thread", "", "Идентификатор цепочки обсуждения")
	cmd.Flags().StringVar(&flags.messageID, "message", "", "Идентификатор сообщения для ответа")
	cmd.Flags().StringVar(&flags.text, "text", "", "Текст сообщения")
	return cmd
}

func newTypeOrientedWikiPageGetCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "get", Short: "Получение страницы документации", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.externalID) == "" {
			return fmt.Errorf("--id is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "wiki", Resource: "page", ObjectType: "page", Operation: "get", ExternalID: flags.externalID}, printIntegrationWikiPage)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Идентификатор страницы")
	return cmd
}
func newTypeOrientedWikiPageSearchCommand() *cobra.Command {
	flags := &integrationFlags{limit: 10}
	cmd := &cobra.Command{Use: "search", Short: "Поиск страниц документации", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.query) == "" {
			return fmt.Errorf("--query is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "wiki", Resource: "page", ObjectType: "page", Operation: "search", Query: flags.query, Limit: flags.limit}, printIntegrationWikiPages)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.query, "query", "", "Строка поиска")
	cmd.Flags().IntVar(&flags.limit, "limit", flags.limit, "Предельное число страниц")
	return cmd
}

// newIntegrationIssueCommand предоставляет типо-ориентированный контракт
// issue.
func newIntegrationIssueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Операции с объектами типа issue",
	}
	cmd.AddCommand(newIntegrationIssueGetCommand())
	cmd.AddCommand(newIntegrationIssueSearchCommand())
	cmd.AddCommand(newTypeOrientedIssueCreateCommand(), newTypeOrientedIssueUpdateCommand())
	comment := &cobra.Command{Use: "comment", Short: "Комментарии задачи"}
	comment.AddCommand(newTypeOrientedIssueCommentListCommand(), newTypeOrientedIssueCommentCreateCommand())
	label := &cobra.Command{Use: "label", Short: "Метки задачи"}
	label.AddCommand(newTypeOrientedIssueLabelCommand("add"), newTypeOrientedIssueLabelCommand("remove"))
	cmd.AddCommand(comment, label)
	return cmd
}

func newIntegrationIssueGetCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение объекта issue по непрозрачному идентификатору",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("id") || strings.TrimSpace(flags.externalID) == "" {
				return fmt.Errorf("--id is required")
			}
			return executeTypeOrientedIssueCommand(cmd, flags, "get")
		},
	}
	cmd.Flags().StringVar(&flags.system, "system", "", "Имя системы из конфигурации")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий в формате owner/name")
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Непрозрачный идентификатор объекта")
	cmd.Flags().StringArrayVar(&flags.fields, "fields", nil, "Поля объекта")
	return cmd
}

func newIntegrationIssueSearchCommand() *cobra.Command {
	flags := &integrationFlags{state: "open"}
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Поиск объектов типа issue",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeTypeOrientedIssueCommand(cmd, flags, "search")
		},
	}
	cmd.Flags().StringVar(&flags.system, "system", "", "Имя системы из конфигурации")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий в формате owner/name")
	cmd.Flags().StringVar(&flags.query, "query", "", "Строка поиска")
	cmd.Flags().StringVar(&flags.state, "state", "open", "Состояние объектов: open, closed или all")
	cmd.Flags().IntVar(&flags.limit, "limit", 30, "Предельное число объектов")
	cmd.Flags().StringArrayVar(&flags.labels, "labels", nil, "Метки задачи")
	cmd.Flags().StringArrayVar(&flags.excludeLabels, "exclude_labels", nil, "Метки, исключаемые из поиска")
	return cmd
}

func newTypeOrientedIssueCreateCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "create", Short: "Создание задачи", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.title) == "" {
			return fmt.Errorf("--title is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "issue", Resource: "issue", ObjectType: "issue", Operation: "create", Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), Title: flags.title, Body: flags.body, State: flags.state, ExternalID: flags.externalID, ID: flags.externalID, Labels: flags.labels}, printTypeOrientedIssueResponse)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.title, "title", "", "Заголовок задачи")
	cmd.Flags().StringVar(&flags.body, "body", "", "Описание задачи")
	cmd.Flags().StringVar(&flags.state, "state", "open", "Состояние задачи")
	cmd.Flags().StringVar(&flags.externalID, "external_id", "", "Внешний идентификатор задачи")
	cmd.Flags().StringArrayVar(&flags.labels, "labels", nil, "Метки задачи")
	return cmd
}

func newTypeOrientedIssueUpdateCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "update", Short: "Обновление задачи", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("id") || strings.TrimSpace(flags.externalID) == "" {
			return fmt.Errorf("--id is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "issue", Resource: "issue", ObjectType: "issue", Operation: "update", ID: flags.externalID, ExternalID: flags.externalID, Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), Title: flags.title, Body: flags.body, State: flags.state, Labels: flags.labels}, printTypeOrientedIssueResponse)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Непрозрачный идентификатор задачи")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.title, "title", "", "Заголовок задачи")
	cmd.Flags().StringVar(&flags.body, "body", "", "Описание задачи")
	cmd.Flags().StringVar(&flags.state, "state", "", "Состояние задачи")
	cmd.Flags().StringArrayVar(&flags.labels, "labels", nil, "Метки задачи")
	return cmd
}

func newTypeOrientedIssueCommentListCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "list", Short: "Получение комментариев задачи", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("id") || strings.TrimSpace(flags.externalID) == "" {
			return fmt.Errorf("--id is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "issue", Resource: "issue", ObjectType: "issue", Operation: "comments", ID: flags.externalID, ExternalID: flags.externalID, Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo")}, printTypeOrientedIssueResponse)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Непрозрачный идентификатор задачи")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	return cmd
}
func newTypeOrientedIssueCommentCreateCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "create", Short: "Создание комментария задачи", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("id") || strings.TrimSpace(flags.externalID) == "" {
			return fmt.Errorf("--id is required")
		}
		if strings.TrimSpace(flags.body) == "" {
			return fmt.Errorf("--body is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "issue", Resource: "comment", ObjectType: "comment", Operation: "create", ID: flags.externalID, ExternalID: flags.externalID, Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), Body: flags.body}, printIntegrationOperationResult)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Непрозрачный идентификатор задачи")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringVar(&flags.body, "body", "", "Текст комментария")
	return cmd
}
func newTypeOrientedIssueLabelCommand(operation string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: operation, Short: "Изменение меток задачи", RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("id") || strings.TrimSpace(flags.externalID) == "" {
			return fmt.Errorf("--id is required")
		}
		if len(flags.labels) == 0 {
			return fmt.Errorf("--labels is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{IntegrationType: "issue", Resource: "label", ObjectType: "label", Operation: operation, ID: flags.externalID, ExternalID: flags.externalID, Repository: flags.repo, RepoProvided: cmd.Flags().Changed("repo"), Labels: flags.labels}, printIntegrationOperationResult)
	}}
	bindTypeSystem(cmd, flags)
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Непрозрачный идентификатор задачи")
	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий внешней системы")
	cmd.Flags().StringArrayVar(&flags.labels, "labels", nil, "Метки задачи")
	return cmd
}

func executeTypeOrientedIssueCommand(cmd *cobra.Command, flags *integrationFlags, operation string) error {
	format, err := integrationOutputFormat(cmd)
	if err != nil {
		return err
	}
	request := integration.Request{
		IntegrationType: "issue",
		System:          flags.system,
		SystemProvided:  cmd.Flags().Changed("system"),
		Resource:        "issue",
		ObjectType:      "issue",
		Operation:       operation,
		Repository:      flags.repo,
		RepoProvided:    cmd.Flags().Changed("repo"),
		ID:              strings.TrimSpace(flags.externalID),
		ExternalID:      strings.TrimSpace(flags.externalID),
		Query:           flags.query,
		State:           flags.state,
		Limit:           flags.limit,
		Fields:          flags.fields,
		Labels:          append([]string(nil), flags.labels...),
		ExcludeLabels:   append([]string(nil), flags.excludeLabels...),
	}
	response, err := newIntegrationService(cmd).Execute(cmd.Context(), request)
	if printErr := printIntegrationResponseOrJSON(cmd, response, format, printTypeOrientedIssueResponse); printErr != nil {
		return printErr
	}
	return err
}

func printTypeOrientedIssueResponse(cmd *cobra.Command, response integration.Response) {
	cmd.Printf("system=%s\nresource=issue\nobject=issue\noperation=%s\nstatus=%s\n", response.System, response.Operation, response.Status)
	if response.SearchResults != nil {
		cmd.Printf("issue_count=%d\n", len(response.SearchResults))
		for _, issue := range response.SearchResults {
			cmd.Printf("id=%s\ntitle=%s\nstate=%s\nurl=%s\n", issue.ID, issue.Title, issue.State, issue.URL)
		}
	}
	if response.TaskComments != nil {
		cmd.Printf("comment_count=%d\n", len(response.TaskComments))
		for _, comment := range response.TaskComments {
			cmd.Printf("comment_id=%s\ncomment_body=%s\n", firstNonEmpty(comment.ExternalID, comment.TaskID), comment.Body)
		}
	}
	if response.Task != nil {
		cmd.Printf("id=%s\n", firstNonEmpty(response.Task.ExternalID, response.Task.ID))
		cmd.Printf("title=%s\nstate=%s\n", response.Task.Title, response.Task.State)
	}
	if response.Failure != nil {
		cmd.Printf("failure=%s\nmessage=%s\n", response.Failure.Kind, response.Failure.Message)
	}
}

func newIntegrationGitHubCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:        "github",
		Short:      "Интеграция с GitHub через gh",
		Deprecated: "используйте типо-ориентированные команды integration issue и integration repo; выбор системы задаётся флагом --system",
	}

	cmd.AddCommand(newIntegrationGitHubAuthCommand())
	cmd.AddCommand(newIntegrationGitHubRepoCommand())
	cmd.AddCommand(newIntegrationGitHubIssueCommand())
	cmd.AddCommand(newIntegrationGitHubPRCommand())
	return cmd
}

func newIntegrationBitbucketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:        "bitbucket",
		Short:      "Интеграция с Bitbucket как репозиторием",
		Deprecated: "используйте типо-ориентированные команды integration repo с флагом --system",
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
		Use:        "mattermost",
		Short:      "Интеграция с Mattermost как мессенджером",
		Deprecated: "используйте типо-ориентированные команды integration messenger с флагом --system",
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
		Use:        "telegram",
		Short:      "Интеграция с Telegram как мессенджером",
		Deprecated: "используйте типо-ориентированные команды integration messenger с флагом --system",
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
	cmd.AddCommand(newIntegrationGitHubIssueSearchCommand())
	cmd.AddCommand(newIntegrationGitHubIssueCommentsCommand())
	cmd.AddCommand(newIntegrationGitHubIssueCommentCommand())
	cmd.AddCommand(newIntegrationGitHubIssueLabelCommand())
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

func newIntegrationGitHubIssueLabelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label",
		Short: "Операции с метками задачи GitHub",
	}
	cmd.AddCommand(newIntegrationGitHubIssueLabelChangeCommand("add"))
	cmd.AddCommand(newIntegrationGitHubIssueLabelChangeCommand("remove"))
	return cmd
}

func newIntegrationConfluenceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:        "confluence",
		Short:      "Интеграция с Confluence как wiki",
		Deprecated: "используйте типо-ориентированные команды integration wiki с флагом --system",
	}
	cmd.AddCommand(newIntegrationSystemAuthCommand("confluence", "Confluence"))
	cmd.AddCommand(newIntegrationConfluencePageCommand())
	return cmd
}

func newIntegrationConfluencePageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "page",
		Short: "Операции со страницами wiki Confluence",
	}
	cmd.AddCommand(newIntegrationConfluencePageGetCommand())
	cmd.AddCommand(newIntegrationConfluencePageSearchCommand())
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType:    "repository",
				System:             system,
				Resource:           "merge-request",
				ObjectType:         "merge-request",
				Operation:          "get",
				Repository:         flags.repo,
				RepoProvided:       cmd.Flags().Changed("repo"),
				MergeRequestNumber: flags.number,
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType:    "repo",
				System:             system,
				Resource:           "review-remark",
				ObjectType:         "review-remark",
				Operation:          "list",
				Repository:         flags.repo,
				RepoProvided:       cmd.Flags().Changed("repo"),
				MergeRequestNumber: flags.number,
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
	cmd.AddCommand(newIntegrationMergeRequestCommentReplyCommand(system, label))
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType:    "repository",
				System:             system,
				Resource:           "comment",
				ObjectType:         "comment",
				Operation:          "create",
				Repository:         flags.repo,
				RepoProvided:       cmd.Flags().Changed("repo"),
				MergeRequestNumber: flags.number,
				Body:               flags.body,
				Path:               flags.path,
				Line:               flags.line,
				Side:               flags.side,
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

func newIntegrationMergeRequestCommentReplyCommand(system string, label string) *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "reply",
		Short: "Ответ на замечание ревизии запроса на слияние " + label,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("thread") || strings.TrimSpace(flags.threadID) == "" {
				return fmt.Errorf("--thread is required")
			}
			if !cmd.Flags().Changed("body") || strings.TrimSpace(flags.body) == "" {
				return fmt.Errorf("--body is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType: "repository",
				System:          system,
				Resource:        "comment",
				ObjectType:      "comment",
				Operation:       "reply",
				ThreadID:        flags.threadID,
				Body:            flags.body,
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
	cmd.Flags().StringVar(&flags.body, "body", "", "Текст ответа")
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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

func newIntegrationConfluencePageGetCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Получение страницы wiki Confluence",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("id") || strings.TrimSpace(flags.externalID) == "" {
				return fmt.Errorf("--id is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType: "wiki",
				System:          "confluence",
				Resource:        "page",
				ObjectType:      "page",
				Operation:       "get",
				ExternalID:      flags.externalID,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationWikiPage); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.externalID, "id", "", "Идентификатор страницы Confluence")
	return cmd
}

func newIntegrationConfluencePageSearchCommand() *cobra.Command {
	flags := &integrationFlags{limit: 10}
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Поиск страниц wiki Confluence по CQL",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("query") || strings.TrimSpace(flags.query) == "" {
				return fmt.Errorf("--query is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}
			service := newIntegrationService(cmd)
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType: "wiki",
				System:          "confluence",
				Resource:        "page",
				ObjectType:      "page",
				Operation:       "search",
				Query:           flags.query,
				Limit:           flags.limit,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printIntegrationWikiPages); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.query, "query", "", "CQL-запрос Confluence")
	cmd.Flags().IntVar(&flags.limit, "limit", flags.limit, "Предельное число страниц")
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				System:       "github",
				Resource:     "issue",
				Operation:    "get",
				Repository:   flags.repo,
				RepoProvided: cmd.Flags().Changed("repo"),
				ID:           strconv.Itoa(flags.number),
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

func newIntegrationGitHubIssueSearchCommand() *cobra.Command {
	flags := &integrationFlags{state: "open"}

	cmd := &cobra.Command{
		Use:   "search",
		Short: "Поиск задач GitHub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType: "tracker",
				System:          "github",
				Resource:        "issue",
				ObjectType:      "task",
				Operation:       "search",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				Query:           flags.query,
				State:           flags.state,
				Limit:           flags.limit,
				Labels:          flags.labels,
				ExcludeLabels:   flags.excludeLabels,
			})
			if printErr := printIntegrationResponseOrJSON(cmd, response, format, printGitHubIssueSearchResults); printErr != nil {
				return printErr
			}
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().StringVar(&flags.state, "state", "open", "Состояние задач: open, closed или all")
	cmd.Flags().StringArrayVar(&flags.labels, "label", nil, "Каноническое название включающей метки задачи")
	cmd.Flags().StringArrayVar(&flags.excludeLabels, "exclude-label", nil, "Каноническое название исключающей метки задачи")
	cmd.Flags().StringVar(&flags.query, "query", "", "Дополнительная строка поиска GitHub")
	cmd.Flags().IntVar(&flags.limit, "limit", 30, "Предельное число задач")
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				System:       "github",
				Resource:     "issue",
				Operation:    "comments",
				Repository:   flags.repo,
				RepoProvided: cmd.Flags().Changed("repo"),
				ID:           strconv.Itoa(flags.number),
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType: "tracker",
				System:          "github",
				Resource:        "comment",
				ObjectType:      "comment",
				Operation:       "create",
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				ID:              strconv.Itoa(flags.number),
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

func newIntegrationGitHubIssueLabelChangeCommand(operation string) *cobra.Command {
	flags := &integrationFlags{}
	title := "Добавление меток задачи GitHub"
	if operation == "remove" {
		title = "Снятие меток задачи GitHub"
	}

	cmd := &cobra.Command{
		Use:   operation,
		Short: title,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("number") {
				return fmt.Errorf("--number is required")
			}
			if len(flags.labels) == 0 {
				return fmt.Errorf("--label is required")
			}
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			response, err := service.Execute(cmd.Context(), integration.Request{
				IntegrationType: "tracker",
				System:          "github",
				Resource:        "label",
				ObjectType:      "label",
				Operation:       operation,
				Repository:      flags.repo,
				RepoProvided:    cmd.Flags().Changed("repo"),
				ID:              strconv.Itoa(flags.number),
				Labels:          flags.labels,
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

	cmd.Flags().StringVar(&flags.repo, "repo", "", "Репозиторий GitHub в формате owner/name")
	cmd.Flags().IntVar(&flags.number, "number", 0, "Номер задачи GitHub")
	cmd.Flags().StringArrayVar(&flags.labels, "label", nil, "Каноническое название метки задачи")
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
			response, err := service.Execute(cmd.Context(), integration.Request{
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
			response, err := service.Execute(cmd.Context(), integration.Request{
				System:             "github",
				Resource:           "pr",
				Operation:          "get",
				Repository:         flags.repo,
				RepoProvided:       cmd.Flags().Changed("repo"),
				MergeRequestNumber: flags.number,
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

func newIntegrationOperationsCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{
		Use:   "operations",
		Short: "Каталог доступных операций контура интеграции",
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := integrationOutputFormat(cmd)
			if err != nil {
				return err
			}

			service := newIntegrationService(cmd)
			operations := service.Operations(cmd.Context(), integration.OperationFilter{
				System:          flags.system,
				IntegrationType: flags.integrationType,
				Name:            flags.operation,
			})
			if format == integrationOutputJSON {
				return printIntegrationJSON(cmd, operations)
			}
			printIntegrationOperations(cmd, operations)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.integrationType, "type", "", "Тип интеграции")
	cmd.Flags().StringVar(&flags.system, "system", "", "Имя внешней системы")
	cmd.Flags().StringVar(&flags.operation, "name", "", "Каноническое имя операции")
	return cmd
}

func newIntegrationStatusCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "status", Short: "Проверка состояния системы", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.system) == "" {
			return fmt.Errorf("--system is required")
		}
		return executeTypeRequest(cmd, flags, integration.Request{Resource: "auth", ObjectType: "auth", Operation: "status"}, printIntegrationAuthStatus)
	}}
	cmd.Flags().StringVar(&flags.system, "system", "", "Имя системы из конфигурации")
	return cmd
}

func newIntegrationInvokeCommand() *cobra.Command {
	flags := &integrationFlags{}
	cmd := &cobra.Command{Use: "invoke", Short: "Диагностический вызов канонической операции", RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(flags.operation) == "" {
			return fmt.Errorf("--name is required")
		}
		if (strings.TrimSpace(flags.input) == "") == (strings.TrimSpace(flags.inputFile) == "") {
			return fmt.Errorf("exactly one of --input or --input-file is required")
		}
		service := newIntegrationService(cmd)
		data := []byte(flags.input)
		if flags.inputFile != "" {
			var err error
			data, err = os.ReadFile(flags.inputFile)
			if err != nil {
				return printInvokeInputFailure(cmd, flags, service, fmt.Errorf("read input file: %w", err))
			}
		}
		var values map[string]any
		if err := json.Unmarshal(data, &values); err != nil {
			return printInvokeInputFailure(cmd, flags, service, fmt.Errorf("parse input JSON: %w", err))
		}
		if values == nil {
			return printInvokeInputFailure(cmd, flags, service, fmt.Errorf("input JSON must be an object"))
		}
		descriptor, found := service.OperationDescriptor(cmd.Context(), flags.operation, flags.system)
		if !found {
			kind := "unsupported-operation"
			integrationType := strings.SplitN(flags.operation, ".", 2)[0]
			defaultSystem := service.DefaultSystemForType(integrationType)
			if strings.TrimSpace(flags.system) == "" && defaultSystem == "" {
				kind = "not-configured"
			}
			if strings.TrimSpace(flags.system) != "" && !service.IsSystemConfigured(flags.system) {
				kind = "not-configured"
			}
			if strings.TrimSpace(flags.system) == "" {
				flags.system = defaultSystem
			}
			return printInvokeAvailabilityFailure(cmd, flags, service, "operation is not available: "+flags.operation, kind)
		}
		if strings.TrimSpace(flags.system) == "" {
			flags.system = descriptor.System
		}
		if !descriptor.Available {
			kind := "unsupported-operation"
			if !descriptor.Enabled {
				kind = "not-configured"
			}
			return printInvokeAvailabilityFailure(cmd, flags, service, "operation is not available: "+flags.operation, kind)
		}
		for _, field := range append(append([]integration.OperationField{}, descriptor.Input.Required...), descriptor.Input.Optional...) {
			value, ok := values[field.Name]
			if !ok && field.Default != "" {
				value, ok = invokeDefaultValue(field)
				if ok {
					values[field.Name] = value
				}
			}
			if !ok {
				continue
			}
			if err := validateInvokeField(field, value); err != nil {
				return printInvokeInputFailure(cmd, flags, service, err)
			}
		}
		for _, field := range descriptor.Input.Required {
			value, ok := values[field.Name]
			if !ok {
				if field.Default != "" {
					continue
				}
				return printInvokeInputFailure(cmd, flags, service, fmt.Errorf("input field is required: %s", field.Name))
			}
			if field.Type == "string" && strings.TrimSpace(value.(string)) == "" && field.Default == "" {
				return printInvokeInputFailure(cmd, flags, service, fmt.Errorf("input field is required: %s", field.Name))
			}
		}
		request := requestFromInvokeInput(descriptor, values)
		request.System = flags.system
		request.SystemProvided = cmd.Flags().Changed("system")
		return executeTypeRequestWithService(cmd, flags, service, request, printIntegrationResponse)
	}}
	cmd.Flags().StringVar(&flags.operation, "name", "", "Каноническое имя операции")
	cmd.Flags().StringVar(&flags.system, "system", "", "Имя системы из конфигурации")
	cmd.Flags().StringVar(&flags.input, "input", "", "JSON структурированного ввода")
	cmd.Flags().StringVar(&flags.inputFile, "input-file", "", "Путь к JSON-файлу структурированного ввода")
	return cmd
}

func invokeDefaultValue(field integration.OperationField) (any, bool) {
	// Значения ${system.*} разрешаются сценарным адаптером, у которого есть
	// доступ к настройкам выбранной системы. Не подменяем их заглушками: это
	// одновременно сохраняет типовую проверку и позволяет адаптеру применить
	// динамическое значение по умолчанию.
	if strings.HasPrefix(strings.TrimSpace(field.Default), "${") && strings.HasSuffix(strings.TrimSpace(field.Default), "}") {
		return nil, false
	}
	switch field.Type {
	case "integer":
		value, err := strconv.ParseInt(strings.TrimSpace(field.Default), 10, 64)
		return float64(value), err == nil
	case "boolean":
		value, err := strconv.ParseBool(strings.TrimSpace(field.Default))
		return value, err == nil
	case "string[]":
		var values []string
		if err := json.Unmarshal([]byte(field.Default), &values); err == nil {
			items := make([]any, len(values))
			for index, value := range values {
				items[index] = value
			}
			return items, true
		}
	}
	defaultValue := strings.TrimSpace(field.Default)
	if strings.HasPrefix(defaultValue, "${") && strings.HasSuffix(defaultValue, "}") {
		switch field.Type {
		case "string":
			return "", true
		case "integer":
			return float64(0), true
		case "boolean":
			return false, true
		}
	}
	return field.Default, true
}

func printInvokeInputFailure(cmd *cobra.Command, flags *integrationFlags, service *integration.Service, err error) error {
	return printInvokeAvailabilityFailure(cmd, flags, service, err.Error(), "invalid-request")
}

func printInvokeAvailabilityFailure(cmd *cobra.Command, flags *integrationFlags, service *integration.Service, message, kind string) error {
	format, err := integrationOutputFormat(cmd)
	if err != nil {
		return err
	}
	response := integration.Response{
		IntegrationType: strings.SplitN(flags.operation, ".", 2)[0],
		System:          flags.system,
		Operation:       flags.operation,
		Status:          "failed",
		Failure:         &integration.Failure{Kind: kind, Message: message},
	}
	if service != nil {
		if route, ok := service.OperationRoute(cmd.Context(), flags.operation, flags.system); ok {
			response.Route = route
			response.Resource = route.Resource
			response.ObjectType = route.ObjectType
			response.Operation = route.Operation
		}
	}
	if response.Route.Resource == "" {
		if objectType, operation := invokeRouteObjectType(flags.operation); objectType != "" {
			response.Resource = objectType
			response.ObjectType = objectType
			response.Operation = operation
			response.Route = integration.Route{IntegrationType: response.IntegrationType, System: response.System, Provider: response.System, Resource: response.Resource, ObjectType: response.ObjectType, Operation: response.Operation}
		}
	}
	if format == integrationOutputJSON {
		if err := printIntegrationJSON(cmd, response); err != nil {
			return err
		}
	} else {
		printFailure(cmd, response)
	}
	return fmt.Errorf("%s", message)
}

func invokeRouteObjectType(name string) (string, string) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(name)), ".")
	if len(parts) < 3 {
		return "", ""
	}
	operation := parts[len(parts)-1]
	object := strings.Join(parts[1:len(parts)-1], ".")
	switch strings.Join(parts, ".") {
	case "issue.issue.comment.list":
		object, operation = "issue", "comments"
	case "issue.issue.comment.create":
		object = "comment"
	case "issue.issue.label.add", "issue.issue.label.remove":
		object = "label"
	case "repo.merge-request.comment.list", "repo.merge-request.comment.create":
		object = "merge-request-comment"
	case "repo.review-remark.list":
		object = "review-remark"
	case "repo.review-remark.unresolve":
		object = "review-remark"
	}
	return object, operation
}

func validateInvokeField(field integration.OperationField, value any) error {
	valid := false
	switch field.Type {
	case "string":
		_, valid = value.(string)
	case "integer":
		number, ok := value.(float64)
		valid = ok && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && number >= float64(-1<<63) && number < float64(1<<63)
	case "boolean":
		_, valid = value.(bool)
	case "string[]":
		items, ok := value.([]any)
		valid = ok
		if valid {
			for _, item := range items {
				if _, ok := item.(string); !ok {
					valid = false
					break
				}
			}
		}
	default:
		valid = true
	}
	if !valid {
		return fmt.Errorf("input field %s must have type %s", field.Name, field.Type)
	}
	return nil
}

func requestFromInvokeInput(descriptor integration.OperationDescriptor, values map[string]any) integration.Request {
	objectType := descriptor.ObjectType
	operation := descriptor.Operation
	requestValues := cloneInvokeValues(values)
	switch descriptor.Name {
	case "issue.issue.comment.list":
		objectType, operation = "issue", "comments"
	case "issue.issue.comment.create":
		objectType = "comment"
	case "issue.issue.label.add", "issue.issue.label.remove":
		objectType = "label"
	case "repo.merge-request.comment.list":
		objectType, operation = "merge-request-comment", "list"
	case "repo.merge-request.comment.create":
		objectType = "merge-request-comment"
	case "repo.review-remark.create":
		objectType, operation = "review-remark", "create"

	case "repo.review-remark.list":
		objectType, operation = "review-remark", "list"
	case "repo.review-remark.unresolve":
		objectType = "review-remark"
	}
	request := integration.Request{IntegrationType: descriptor.IntegrationType, Resource: objectType, ObjectType: objectType, Operation: operation, Extra: requestValues}
	stringValue := func(name string) string {
		if value, ok := values[name].(string); ok {
			return value
		}
		return ""
	}
	request.Repository = stringValue("repository")
	_, request.RepoProvided = values["repository"]
	request.ID = stringValue("id")
	request.ExternalID = firstNonEmpty(request.ID, stringValue("external_id"))
	request.Base = stringValue("base")
	request.Head = stringValue("head")
	request.Title = stringValue("title")
	request.Body = stringValue("body")
	request.Text = stringValue("text")
	request.Query = stringValue("query")
	request.State = stringValue("state")
	request.Scope = stringValue("scope")
	// Координаты diff остаются в Extra для сценарных операций, если они
	// опубликованы их входным контрактом, но встроенный обычный комментарий
	// всегда выполняется без координат и не превращается в inline-замечание.
	if descriptor.Name != "repo.merge-request.comment.create" {
		request.Path = stringValueFrom(requestValues, "path")
		request.Side = stringValueFrom(requestValues, "side")
	}
	request.ChannelID = stringValueFrom(requestValues, "channel")
	request.ThreadID = stringValueFrom(requestValues, "thread")
	request.MessageID = stringValueFrom(requestValues, "message")
	if value, ok := requestValues["number"].(float64); ok {
		request.MergeRequestNumber = int(value)
	}
	if descriptor.Name != "repo.merge-request.comment.create" {
		if value, ok := requestValues["line"].(float64); ok {
			request.Line = int(value)
		}
	}
	if value, ok := requestValues["limit"].(float64); ok {
		request.Limit = int(value)
	}
	if value, ok := requestValues["draft"].(bool); ok {
		request.Draft = value
	}
	if labels, ok := requestValues["labels"].([]any); ok {
		for _, label := range labels {
			if value, ok := label.(string); ok {
				request.Labels = append(request.Labels, value)
			}
		}
	}
	if excludeLabels, ok := requestValues["exclude_labels"].([]any); ok {
		for _, label := range excludeLabels {
			if value, ok := label.(string); ok {
				request.ExcludeLabels = append(request.ExcludeLabels, value)
			}
		}
	}
	if fields, ok := requestValues["fields"].([]any); ok {
		for _, field := range fields {
			if value, ok := field.(string); ok {
				request.Fields = append(request.Fields, value)
			}
		}
	}
	return request
}

func stringValueFrom(values map[string]any, name string) string {
	if value, ok := values[name].(string); ok {
		return value
	}
	return ""
}

func cloneInvokeValues(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func executeTypeRequestWithService(cmd *cobra.Command, flags *integrationFlags, service *integration.Service, request integration.Request, printer func(*cobra.Command, integration.Response)) error {
	format, err := integrationOutputFormat(cmd)
	if err != nil {
		return err
	}
	response, executeErr := service.Execute(cmd.Context(), request)
	if format == integrationOutputJSON {
		if printErr := printIntegrationJSON(cmd, response); printErr != nil {
			return printErr
		}
		return executeErr
	}
	if printer != nil {
		printer(cmd, response)
	}
	return executeErr
}

func printIntegrationResponse(cmd *cobra.Command, response integration.Response) {
	switch {
	case response.MergeRequests != nil:
		printIntegrationMergeRequests(cmd, response)
	case response.Task != nil || response.TaskComments != nil || response.SearchResults != nil:
		printTypeOrientedIssueResponse(cmd, response)
	case response.Repository != nil:
		printIntegrationRepository(cmd, response)
	case response.MergeRequest != nil:
		printIntegrationMergeRequest(cmd, response)
	case response.ReviewRemarks != nil:
		printIntegrationReviewRemarks(cmd, response)
	case response.Conversation != nil:
		printIntegrationThread(cmd, response)
	case response.Message != nil:
		printIntegrationMessage(cmd, response)
	case response.WikiPage != nil:
		printIntegrationWikiPage(cmd, response)
	case response.WikiPages != nil:
		printIntegrationWikiPages(cmd, response)
	case response.AuthStatus != nil:
		printIntegrationAuthStatus(cmd, response)
	default:
		printIntegrationOperationResult(cmd, response)
	}
}

func newIntegrationService(cmd *cobra.Command) *integration.Service {
	path := cmd.CommandPath()
	for _, system := range []string{"github", "bitbucket", "mattermost", "telegram", "confluence"} {
		if strings.Contains(path, "integration "+system) {
			cmd.PrintErrf("предупреждение: форма integration %s устарела; используйте типо-ориентированную команду и --system\n", system)
			break
		}
	}
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

func printIntegrationOperations(cmd *cobra.Command, operations []integration.OperationDescriptor) {
	if len(operations) == 0 {
		cmd.Println("operations=<none>")
		return
	}

	for _, operation := range operations {
		cmd.Printf("name=%s\nsystem=%s\ntype=%s\nadapter-type=%s\nobject=%s\noperation=%s\nenabled=%t\navailable=%t\nside-effect=%t\noutput-resource=%s\noutput-shape=%s\n", operation.Name, operation.System, operation.IntegrationType, operation.AdapterType, operation.ObjectType, operation.Operation, operation.Enabled, operation.Available, operation.SideEffect, operation.Output.Resource, operation.Output.Shape)
		for _, field := range operation.Input.Required {
			cmd.Printf("required=%s:%s\n", field.Name, field.Type)
		}
		for _, field := range operation.Input.Optional {
			if field.Default != "" {
				cmd.Printf("optional=%s:%s=%s\n", field.Name, field.Type, field.Default)
				continue
			}
			cmd.Printf("optional=%s:%s\n", field.Name, field.Type)
		}
		for _, failureKind := range operation.FailureKinds {
			cmd.Printf("failure=%s\n", failureKind)
		}
		for _, diagnostic := range operation.Diagnostics {
			cmd.Printf("diagnostic=%s\n", diagnostic)
		}
	}
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
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nid=%s\ntitle=%s\nstate=%s\nauthor_login=%s\nauthor_name=%s\nauthor_url=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", issue.System, response.Resource, response.Operation, issue.Repository, issue.ID, issue.Title, issue.State, issue.Author.Login, issue.Author.Name, issue.Author.URL, issue.URL, issue.CreatedAt, issue.UpdatedAt)
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

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nid=%s\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.ID, status.State, status.Command, status.Path, status.ExitCode, status.Message)
	for _, diagnostic := range status.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
	printMultilineField(cmd, "stdout", status.Stdout)
	printMultilineField(cmd, "stderr", status.Stderr)
}

func printGitHubIssueSearchResults(cmd *cobra.Command, response integration.Response) {
	if response.Failure != nil {
		printFailure(cmd, response)
		return
	}

	if response.SearchResults != nil {
		repository := ""
		if response.Metadata != nil {
			repository = response.Metadata["repository"]
		}
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nissue_count=%d\n", response.System, response.Resource, response.Operation, repository, len(response.SearchResults))
		for _, issue := range response.SearchResults {
			cmd.Printf("id=%s\ntitle=%s\nstate=%s\n", issue.ID, issue.Title, issue.State)
			for _, label := range issue.Labels {
				cmd.Printf("label=%s\n", label)
			}
			if issue.Author.Login != "" || issue.Author.Name != "" || issue.Author.URL != "" {
				cmd.Printf("author_login=%s\nauthor_name=%s\nauthor_url=%s\n", issue.Author.Login, issue.Author.Name, issue.Author.URL)
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
			cmd.Printf("url=%s\ncreated_at=%s\nupdated_at=%s\n", issue.URL, issue.CreatedAt, issue.UpdatedAt)
		}
		return
	}

	status := response.IssueStatus
	if status == nil {
		cmd.Printf("system=%s\nresource=%s\noperation=%s\nmessage=GitHub issue search did not return normalized results\n", response.System, response.Resource, response.Operation)
		return
	}

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.State, status.Command, status.Path, status.ExitCode, status.Message)
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
			number, _ = strconv.Atoi(response.Comments[0].TaskID)
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

	cmd.Printf("system=%s\nresource=%s\noperation=%s\nrepository=%s\nid=%s\nstate=%s\ncommand=%s\npath=%s\nexit-code=%d\nmessage=%s\n", status.System, response.Resource, response.Operation, status.Repository, status.ID, status.State, status.Command, status.Path, status.ExitCode, status.Message)
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
		if response.Failure != nil {
			printFailure(cmd, response)
		} else {
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

func printIntegrationWikiPage(cmd *cobra.Command, response integration.Response) {
	if response.WikiPage == nil {
		printFailure(cmd, response)
		return
	}
	printWikiPage(cmd, *response.WikiPage, response)
}

func printIntegrationWikiPages(cmd *cobra.Command, response integration.Response) {
	if response.Failure != nil {
		printFailure(cmd, response)
		return
	}
	query := ""
	if response.Metadata != nil {
		query = response.Metadata["query"]
	}
	cmd.Printf("system=%s\nresource=%s\noperation=%s\nquery=%s\npage_count=%d\n", response.System, response.Resource, response.Operation, query, len(response.WikiPages))
	for _, page := range response.WikiPages {
		cmd.Printf("page_id=%s\npage_space=%s\npage_title=%s\npage_version=%d\npage_url=%s\npage_updated_at=%s\n", page.ExternalID, page.Space, page.Title, page.Version, page.URL, page.UpdatedAt)
	}
}

func printMessage(cmd *cobra.Command, message integration.Message) {
	cmd.Printf("message_system=%s\nspace_id=%s\nthread_id=%s\nmessage_id=%s\nauthor_login=%s\nauthor_name=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", message.System, message.SpaceID, message.ThreadID, message.MessageID, message.Author.Login, message.Author.Name, message.URL, message.CreatedAt, message.UpdatedAt)
	printMultilineField(cmd, "message_body", message.Body)
}

func printWikiPage(cmd *cobra.Command, page integration.WikiPage, response integration.Response) {
	cmd.Printf("system=%s\nresource=%s\noperation=%s\npage_id=%s\nspace=%s\ntitle=%s\nbody_format=%s\nversion=%d\nupdated_by_login=%s\nupdated_by_name=%s\nurl=%s\ncreated_at=%s\nupdated_at=%s\n", page.System, response.Resource, response.Operation, page.ExternalID, page.Space, page.Title, page.BodyFormat, page.Version, page.UpdatedBy.Login, page.UpdatedBy.Name, page.URL, page.CreatedAt, page.UpdatedAt)
	for _, trait := range page.Traits {
		cmd.Printf("trait=%s\n", trait)
	}
	printMultilineField(cmd, "body", page.Body)
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
