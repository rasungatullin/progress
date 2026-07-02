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
- получить страницу wiki по идентификатору;
- найти страницы wiki по запросу внешней системы.

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
- `WikiPage` — страница wiki;
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

## 6. Интерфейс контура

Контур публикует универсальный интерфейс диспетчеризации и выполнения канонического запроса.

```go
package integration

import "context"

type Service interface {
    Dispatch(context.Context, Request) (Route, error)
    Execute(context.Context, Request) (Response, error)
}
```

Важно:

- контур сам должен уметь определить, с какой системой работать, по настройкам и селектору;
- поле `System` является явным селектором интегрируемой системы;
- если `System` не задан, диспетчер выбирает систему по `IntegrationType` и настройкам `default_systems`;
- если `SystemProvided=true` и `System` пустой, запрос отклоняется как `invalid-request`;
- вызывающему коду не нужно знать устройство GitHub, Bitbucket, Mattermost, Telegram или другой внешней системы.

## 7. Типовой запрос

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

## 8. Типовой ответ

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
- `not-found`;
- `invalid-request`;
- `temporary-unavailable`;
- `timeout`;
- `unsupported-action`;
- `internal-integration-error`.

## 9. Поведение контура

Контур должен вести себя следующим образом:

- по возможности сам разрешать нужную интеграцию по настройкам;
- возвращать каноничные сущности, а не сырой внешний JSON;
- скрывать source-specific поля вроде `labels` или `custom_fields`, переводя их в `Traits` и `Attributes`;
- поддерживать `dry-run` для операций записи;
- поддерживать `explain` для диагностики выбора интеграции и способа вызова;
- различать полный, частичный и неуспешный результат.

## 10. Границы ответственности

Контур интеграции не должен:

- принимать решение о следующем шаге рабочего цикла;
- определять инженерный смысл задачи за пределами нормализации полей;
- выполнять содержательную работу вместо контура исполнения;
- становиться источником истины для жизненного цикла задачи.

Его задача — дать другим контурам устойчивый и предсказуемый способ читать и менять внешние объекты через каноничную внутреннюю модель.
