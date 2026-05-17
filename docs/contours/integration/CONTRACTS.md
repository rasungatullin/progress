# Контракты контура интеграции

## 1. Назначение документа

Настоящий документ фиксирует практический контракт контура интеграции с фокусом на его основную задачу: по каноничному внутреннему запросу получить или изменить внешний объект и вернуть результат в нормализованной форме.

Документ сознательно не описывает все возможные расширения. Он задаёт минимальный рабочий срез, достаточный для первого цикла по задаче разработки.

## 2. Основная задача контура

Контур интеграции нужен для того, чтобы другие контуры могли сказать:

- дай детали по задаче;
- дай pull request и комментарии к нему;
- создай pull request;
- оставь комментарий;
- измени внешний статус.

При этом вызывающий контур не должен знать:

- какой именно внешний API используется;
- как устроена авторизация;
- в каких полях конкретной системы лежат нужные признаки;
- как различаются GitHub, Jira и другие системы.

## 3. Базовые возможности

Для первого рабочего этапа достаточно следующих возможностей:

- проверить доступность интеграции и авторизацию;
- получить репозиторий;
- получить задачу по идентификатору или номеру;
- получить комментарии задачи;
- получить pull request по идентификатору или номеру;
- получить комментарии pull request, включая review comments;
- создать pull request;
- создать комментарий к задаче или pull request;
- ответить на комментарий pull request.

Это не исчерпывающий список. Контракт должен позволять расширение, но первый потребительский слой должен оставаться небольшим и понятным.

## 4. Каноничные сущности

Для первого среза контур должен оперировать следующими каноничными сущностями:

- `Repository`;
- `Issue`;
- `IssueComment`;
- `PullRequest`;
- `PullRequestComment`.

Каноничность здесь означает, что внутренние поля выражают инженерный смысл, а не копируют один в один внешний формат конкретной системы.

Пример:

- в GitHub признак может приходить как `label`;
- в Jira тот же смысл может находиться в `custom field`;
- внутри контура оба случая должны приводиться к одному и тому же каноничному признаку.

Для этого в каноничных сущностях допустимы два механизма:

- `Traits []string` — короткие нормализованные признаки;
- `Attributes map[string]string` — каноничные дополнительные поля key-value.

## 5. Каноничные Go-типы

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

## 6. Интерфейс контура

Контур должен публиковать простой Go-интерфейс с предметными методами по основным сущностям первого среза.

```go
package integration

import "context"

type Service interface {
    AuthCheck(context.Context, AuthCheckRequest) (AuthCheckResult, error)

    RepositoryGet(context.Context, RepositoryGetRequest) (RepositoryGetResult, error)

    IssueGet(context.Context, IssueGetRequest) (IssueGetResult, error)
    IssueCommentList(context.Context, IssueCommentListRequest) (IssueCommentListResult, error)

    PullRequestGet(context.Context, PullRequestGetRequest) (PullRequestGetResult, error)
    PullRequestCommentList(context.Context, PullRequestCommentListRequest) (PullRequestCommentListResult, error)
    PullRequestCreate(context.Context, PullRequestCreateRequest) (PullRequestCreateResult, error)
    PullRequestCommentCreate(context.Context, PullRequestCommentCreateRequest) (PullRequestCommentCreateResult, error)
    PullRequestCommentReply(context.Context, PullRequestCommentReplyRequest) (PullRequestCommentReplyResult, error)
}
```

Важно:

- контур сам должен уметь определить, с какой системой работать, по настройкам и селектору;
- в текущем runtime-контракте `integration.Service` поле `System` обязательно и используется как явный селектор провайдера;
- вызывающему коду не нужно заранее знать, GitHub это или иная система, если этого достаточно для настройки маршрутизации.

## 7. Типовой запрос

Основной типовой запрос к контуру звучит так:

`Дай детали по задаче 123`.

Минимальный Go-тип такого запроса:

```go
type IssueGetRequest struct {
    System     System // required in current runtime contract
    Repository string
    Number     int
    Fields     []string
    Options    RequestOptions
}
```

Семантика полей:

- `System` — в текущем runtime-контракте обязателен и указывает конкретную интеграцию;
- `Repository` — опорный контекст, если он нужен для разрешения объекта;
- `Number` — канонический идентификатор задачи внутри контекста источника;
- `Fields` — опциональный список требуемых полей;
- `Options` — служебные режимы вызова.

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

- принимать решение о следующем шаге workflow;
- определять инженерный смысл задачи за пределами нормализации полей;
- выполнять содержательную работу вместо контура исполнения;
- становиться источником истины для жизненного цикла задачи.

Его задача — дать другим контурам устойчивый и предсказуемый способ читать и менять внешние объекты через каноничную внутреннюю модель.
