# Контракты контура интеграции

## 1. Назначение документа

Настоящий документ фиксирует практический контракт контура интеграции с фокусом на его основную задачу: по каноничному внутреннему запросу получить или изменить внешний объект и вернуть результат в нормализованной форме.

Документ сознательно не описывает все возможные расширения. Он задаёт минимальный рабочий срез, достаточный для первого цикла по задаче разработки.

## 2. Основная задача контура

Контур интеграции нужен для того, чтобы другие контуры могли сказать:

- дай детали по задаче;
- дай запрос на слияние и комментарии к нему;
- создай запрос на слияние;
- оставь комментарий;
- измени внешний статус.

При этом вызывающий контур не должен знать:

- какой именно внешний API используется;
- как устроена авторизация;
- где хранится приватное значение авторизации;
- в каких полях конкретной системы лежат нужные признаки;
- как различаются GitHub, Jira и другие системы.

## 3. Базовые возможности

Для текущего рабочего этапа поддерживаются следующие возможности:

- проверить доступность интеграции и авторизацию;
- получить репозиторий;
- получить задачу по идентификатору или номеру;
- получить комментарии задачи;
- создать комментарий задачи;
- добавить метки задачи по каноническим названиям;
- снять метки задачи по каноническим названиям;
- получить запрос на слияние по идентификатору или номеру;
- найти запросы на слияние в репозитории, включая отбор по текущему автору или текущему ревьюеру;
- получить комментарии запроса на слияние, включая замечания ревизии;
- создать комментарий запроса на слияние, включая inline-комментарий к строке diff;
- получить состояние замечания ревизии как `resolved` или `unresolved`;
- разрешить замечание ревизии, если внешняя система поддерживает такую операцию;
- создать запрос на слияние;
- получить цепочку обсуждения мессенджера, если внешняя система поддерживает такую операцию;
- создать сообщение в мессенджере;
- получить страницу документации по идентификатору;
- найти страницы документации по запросу внешней системы.

Это не исчерпывающий список. Контракт должен позволять расширение, но первый потребительский слой должен оставаться небольшим и понятным.

## 4. Каноничные сущности

Контур оперирует следующими каноническими сущностями:

- `CanonicalTask` — каноническая задача трекера;
- `TaskComment` — комментарий задачи;
- `Repository` — репозиторий;
- `MergeRequest` — запрос на слияние;
- `ReviewRemark` — замечание ревизии;
- `MessageThread` — цепочка обсуждения;
- `Message` — сообщение;
- `MessageReaction` — реакция сообщения;
- `WikiPage` — страница документации;
- `Failure` — отказное состояние;
- `OperationResult` — диагностируемый результат операции изменения.

Каноничность здесь означает, что внутренние поля выражают инженерный смысл, а не копируют один в один внешний формат конкретной системы.

Пример:

- в GitHub признак может приходить как `label`;
- в Jira тот же смысл может находиться в `custom field`;
- внутри контура оба случая должны приводиться к одному и тому же каноничному признаку.

Для этого в каноничных сущностях допустимы два механизма:

- `Traits []string` — короткие нормализованные признаки;
- `Attributes map[string]string` — каноничные дополнительные поля key-value.

Для задач внешние метки проходят через сопоставление меток задачи `task_label_mapping` в настройках интегрируемой системы. Ключом правила является внешняя метка, значением — каноническое название. Если правило отсутствует, применяется соответствие один к одному. Если значение правила пустое, внешняя метка игнорируется и не попадает в `Traits` или `Labels` нормализованной задачи.

Операции добавления и снятия меток принимают только канонические названия в поле `Labels`. Перед обращением к адаптеру контур интеграции переводит их во внешние имена по обратному сопоставлению. Если обратного правила нет, используется то же название.

Данные авторизации HTTP-адаптеров могут задаваться ссылкой `token_private` в настройках интегрируемой системы. Перед созданием адаптера контур интеграции читает приватное значение из выбранного хранилища и передаёт адаптеру только разрешённое значение в памяти процесса. Канонический запрос `Request` не содержит токенов и не переносит их через переменные окружения.

## 5. Каноничные Go-типы

Актуальная реализация находится в `internal/integration/model/types.go`. Основной вход — `Request`, внутренний вход адаптера — `ProviderRequest`, выход — `Response`.

Ключевые поля `Request`:

```go
type Request struct {
    IntegrationType string
    System          string
    SystemProvided  bool
    Resource        string
    ObjectType      string
    Operation       string
    Repository      string
    Number          int
    ExternalID      string
    Base            string
    Head            string
    Title           string
    Body            string
    Text            string
    Query           string
    State           string
    Scope           string
    Limit           int
    Path            string
    Line            int
    Side            string
    ChannelID       string
    ThreadID        string
    MessageID       string
    Labels          []string
}
```

Ключевые поля `Response`:

```go
type Response struct {
    IntegrationType string
    System          string
    Resource        string
    ObjectType      string
    Operation       string
    Status          string
    Partial         bool
    Failure         *Failure
    Route           Route
    Task            *CanonicalTask
    TaskComments    []TaskComment
    Repository      *Repository
    MergeRequest    *MergeRequest
    MergeRequests   []MergeRequest
    ReviewRemarks   []ReviewRemark
    Conversation    *MessageThread
    Messages        []Message
    Message         *Message
    WikiPage        *WikiPage
    WikiPages       []WikiPage
    OperationResult *OperationResult
}
```

Для перехода соседних контуров также заполняются прежние структуры `TrackerIssue`, `TrackerRepository`, `TrackerPullRequest` и `TrackerComment`, если результат может быть однозначно преобразован.

Предыдущий пример первого GitHub-среза оставлен ниже как историческая форма контракта, но новые вызовы должны использовать `Request` и `Response`.

```go
package integration

import "time"

type System string

const (
    SystemGitHub System = "github"
)

type RequestOptions struct {
    DryRun  bool
    Explain bool
}

type Repository struct {
    System        System
    ID            string
    Name          string
    Owner         string
    DefaultBranch string
    URL           string
}

type Issue struct {
    System     System
    Repository string
    Number     int
    Title      string
    Body       string
    State      string
    Traits     []string
    Attributes map[string]string
    Assignees  []string
    Author     string
    URL        string
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type IssueComment struct {
    System      System
    ID          string
    IssueNumber int
    Author      string
    Body        string
    URL         string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type PullRequest struct {
    System         System
    Repository     string
    Number         int
    Title          string
    Body           string
    State          string
    Traits         []string
    Attributes     map[string]string
    BaseRef        string
    HeadRef        string
    Author         string
    ReviewDecision string
    URL            string
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type PullRequestCommentKind string

const (
    PullRequestCommentDiscussion PullRequestCommentKind = "discussion"
    PullRequestCommentReview     PullRequestCommentKind = "review"
)

type PullRequestComment struct {
    System            System
    ID                string
    PullRequestNumber int
    Kind              PullRequestCommentKind
    Author            string
    Body              string
    Path              string
    Line              int
    Side              string
    ReplyToID         string
    URL               string
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

В текущем срезе среды исполнения GitHub issue comments нормализуются в общий `TrackerComment`: `Repository` и `Number` задают исходную issue, `Author` хранит нормализованного пользователя, `URL` берётся из `html_url`, а `CreatedAt`/`UpdatedAt` сохраняются как строки из ответа GitHub.

## 6. Транспорт внешнего обращения

Выбор способа обращения к внешней системе относится к настройке интегрируемой системы, а не к публичному запросу вызывающего контура. Для GitHub-системы поле `IntegrationSystemConfig.Transport` принимает значения:

- `cli` — вызов через `gh`;
- `api` — прямой вызов GitHub REST API и GraphQL API.

Если поле не задано, используется `cli`, чтобы сохранить совместимость существующих установок. В режиме `api` GitHub-адаптер использует `token` или `token_env`, а `base_url` задаёт базовый адрес API. Если `base_url` не указан, применяется `https://api.github.com`.

Независимо от выбранного транспорта адаптер возвращает тот же `Response`: канонические объекты `CanonicalTask`, `TaskComment`, `Repository`, `MergeRequest`, `ReviewRemark`, `OperationResult` и `Failure` не должны раскрывать вызывающему контуру, был ли внешний вызов выполнен через `gh` или напрямую через HTTP API. Диагностика маршрута может включать транспорт как служебный признак, например `transport=api`.

## 7. Интерфейс контура

Контур публикует универсальный интерфейс диспетчеризации и выполнения канонического запроса.

```go
package integration

import "context"

type Service interface {
    Dispatch(context.Context, Request) (Route, error)
    Execute(context.Context, Request) (Response, error)
    Operations(context.Context, OperationFilter) []OperationDescriptor
}
```

Важно:

- контур сам должен уметь определить, с какой системой работать, по настройкам и селектору;
- поле `System` является явным селектором интегрируемой системы;
- если `System` не задан, диспетчер выбирает систему по `IntegrationType` и настройкам `default_systems`;
- если `SystemProvided=true` и `System` пустой, запрос отклоняется как `invalid-request`;
- вызывающему коду не нужно знать устройство GitHub, Bitbucket, Mattermost, Telegram или другой внешней системы.

## 7. Каталог операций

Контур интеграции публикует каталог операций, чтобы соседние контуры могли заранее понять, какие действия доступны в текущей конфигурации и какие поля нужны для вызова. Каталог не заменяет выполнение операции: он описывает контракт и доступность.

Каноническое имя операции строится по схеме:

```text
<integration-type>.<object>.<operation>
```

Примеры:

- `tracker.task.get`;
- `tracker.task.comment.list`;
- `tracker.task.comment.create`;
- `tracker.task.label.add`;
- `repository.repo.get`;
- `repository.merge-request.create`;
- `repository.review-remark.resolve`;
- `messenger.thread.get`;
- `wiki.page.search`.

Внешние имена команд, например `github issue comments` или `bitbucket pr get`, не являются каноническими именами операций. Они остаются диагностическим способом вызова конкретного адаптера.

Публичная модель каталога:

```go
type OperationDescriptor struct {
    Name            string
    IntegrationType string
    System          string
    AdapterType     string
    ObjectType      string
    Operation       string
    Enabled         bool
    Available       bool
    SideEffect      bool
    DryRunSupported bool
    Input           OperationInputContract
    Output          OperationOutputContract
    FailureKinds    []string
    Diagnostics     []string
}

type OperationInputContract struct {
    Required []OperationField
    Optional []OperationField
}

type OperationField struct {
    Name        string
    Type        string
    Description string
    Default     string
    Repeated    bool
}

type OperationOutputContract struct {
    Resource string
    Shape    string
}
```

Поле `Available` означает, что операция может быть выполнена через зарегистрированный адаптер при текущей конфигурации. Отключённые системы и системы без подключённого адаптера не должны публиковать операции как доступные. При этом каталог может сохранить их описатели с `available=false`, если это полезно для диагностики настройки.

Для сценарных систем типа `script` каталог строится из `systems.<name>.operations`: обязательные и необязательные поля берутся из конфигурации операции, а значения по умолчанию попадают в описатель поля. Это позволяет контуру исполнения увидеть контракт сценарной операции до фактического запуска внешнего сценария.

Для локального трекера `local-tracker` каталог публикует операции трекера: создание, чтение, поиск, обновление задачи, комментарии и изменение меток.

## 8. Локальный трекер задач

Локальный трекер подключается как интегрируемая система типа `tracker`. Для контура принятия решения и контура исполнения он выглядит так же, как внешний трекер: входом остаётся `Request`, выходом остаётся `Response`.

Минимальная конфигурация:

```json
{
  "default_systems": {
    "tracker": "local"
  },
  "systems": {
    "local": {
      "type": "local-tracker",
      "integration_type": "tracker",
      "enabled": true,
      "database": {
        "driver": "sqlite",
        "path": ".progress/local-tracker/tasks.sqlite"
      }
    }
  }
}
```

Если `database` не задан, используется:

```json
{
  "driver": "sqlite",
  "path": ".progress/local-tracker/tasks.sqlite"
}
```

Относительный путь считается относительно корня репозитория. Неподдержанный драйвер отклоняется как ошибка конфигурации.

Хранилище фиксирует задачи и комментарии в канонической модели:

- задача хранит номер, внешний идентификатор, заголовок, описание, состояние, признаки, атрибуты, автора и время изменения;
- комментарий хранит идентификатор, номер задачи, автора, тело и время изменения;
- признаки задачи возвращаются как `Traits` и совместимые `Labels`.

Поддержанные операции первого среза:

- `tracker.task.create`;
- `tracker.task.get`;
- `tracker.task.search`;
- `tracker.task.update`;
- `tracker.task.comment.list`;
- `tracker.task.comment.create`;
- `tracker.task.label.add`;
- `tracker.task.label.remove`;
- `auth status` для проверки доступности SQLite-хранилища.

## 9. Типовой запрос

Основной типовой запрос к контуру звучит так:

`Дай детали по задаче 123`.

Минимальный Go-тип такого запроса:

```go
type IssueGetRequest struct {
    IntegrationType string // "tracker"
    System          string
    ObjectType      string // "task" или "issue"
    Operation       string // "get"
    Repository      string
    Number          int
}
```

Семантика полей:

- `IntegrationType` — тип интеграции, например `tracker`, `repository` или `messenger`;
- `System` — конкретная интегрируемая система, если нужно переопределить систему по умолчанию;
- `ObjectType` — тип канонического объекта;
- `Operation` — интеграционная операция;
- `Repository` — опорный контекст, если он нужен для разрешения объекта;
- `Number` — канонический идентификатор задачи внутри контекста источника;

Если `Fields` не указан, контур должен вернуть базовый каноничный набор полей сущности.

Если `Fields` указан, контур может:

- вернуть только requested subset;
- вернуть базовый набор плюс requested fields;
- вернуть частичный результат, если часть полей не поддерживается конкретной интеграцией.

## 10. Типовой ответ

```go
type IssueGetResult struct {
    Status  string
    Data    Issue
    Partial bool
    Failure *Failure
}

type Failure struct {
    Kind      string
    Retryable bool
    Message   string
}
```

Типовая семантика ответа:

- `Status=ok` — задача успешно получена;
- `Status=partial` — задача получена, но не все поля удалось вернуть;
- `Status=failed` — задача не получена.

Минимальные `Failure.Kind`:

- `auth-required`;
- `permission-denied`;
- `not-found`;
- `invalid-request`;
- `temporary-unavailable`;
- `rate-limited`;
- `timeout`;
- `unsupported-operation`;
- `internal-integration-error`.

## 11. Поведение контура

Контур должен вести себя следующим образом:

- по возможности сам разрешать нужную интеграцию по настройкам;
- возвращать каноничные сущности, а не сырой внешний JSON;
- скрывать source-specific поля вроде `labels` или `custom_fields`, переводя их в `Traits` и `Attributes`;
- поддерживать `dry-run` для операций записи;
- поддерживать `explain` для диагностики выбора интеграции и способа вызова;
- различать полный, частичный и неуспешный результат.

## 12. Границы ответственности

Контур интеграции не должен:

- принимать решение о следующем шаге рабочего цикла;
- определять инженерный смысл задачи за пределами нормализации полей;
- выполнять содержательную работу вместо контура исполнения;
- становиться источником истины для жизненного цикла задачи.

Его задача — дать другим контурам устойчивый и предсказуемый способ читать и менять внешние объекты через каноничную внутреннюю модель.
