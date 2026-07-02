# CLI контура интеграции

## 1. Назначение документа

Настоящий документ определяет практический срез контура интеграции с внешними системами. Контур поддерживает GitHub как трекер и репозиторий, Bitbucket как репозиторий, Mattermost и Telegram как мессенджеры.

Документ фиксирует:

- модель работы интеграционного слоя;
- границы ответственности между CLI, сервисом интеграции, `gh` и HTTP API внешних систем;
- состав пользовательских команд;
- порядок настройки встроенных адаптеров.

## 2. Принцип встроенных интеграций

GitHub-адаптер запускает `gh` как внешний инструмент и получает структурированный результат. Bitbucket, Mattermost и Telegram используют HTTP API и нормализуют ответы в те же канонические структуры.

Базовая схема вызова:

1. CLI принимает входной запрос пользователя или другого контура;
2. сервис интеграции формирует внутренний `IntegrationRequest`;
3. диспетчер выбирает адаптер по типу интеграции и имени системы;
4. адаптер выполняет вызов `gh` или HTTP API;
5. адаптер считывает структурированный ответ;
6. JSON-ответ преобразуется во внутреннюю нормализованную структуру;
7. CLI печатает результат в диагностическом или структурированном виде.

```mermaid
flowchart LR
    A[progress integration ...] --> B[CLI integration]
    B --> C[Service]
    C --> D[Integration adapter]
    D --> E[gh или HTTP API]
    E --> F[External source]
    F --> E
    E --> D
    D --> C
    C --> B
```

## 3. Причины выбора `gh`

На первом этапе такой подход предпочтителен по следующим причинам:

- не требуется отдельно реализовывать OAuth, PAT и хранение секретов;
- можно опереться на уже существующую авторизацию пользователя в `gh auth login`;
- `gh` уже покрывает типовые GitHub-сущности: `issue`, `pull request`, `comments`, `search`, `repo`, `api`;
- интеграция может начать работать как тонкий адаптер без преждевременного усложнения кодовой базы.

Ограничения такого подхода:

- требуется установленный бинарь `gh`;
- часть ошибок приходит как текст из `stderr` и должна быть нормализована;
- доступные операции ограничены возможностями установленной версии `gh`.

## 4. Внутренние модули реализации

Минимальный состав кода для первого этапа:

- `internal/integration/service.go` — фасад контура интеграции;
- `internal/integration/model/types.go` — типы запросов и нормализованных ответов;
- `internal/integration/github/service.go` — адаптер GitHub;
- `internal/integration/github/runner.go` — безопасный запуск `gh`;
- `internal/integration/bitbucket/service.go` — адаптер Bitbucket;
- `internal/integration/mattermost/service.go` — адаптер Mattermost;
- `internal/integration/telegram/service.go` — адаптер Telegram;
- `internal/cli/integration.go` — ветка CLI-команд `progress integration ...`.

Минимальные внутренние интерфейсы:

```go
type Query struct {
    System string
    Kind   string
    Repo   string
    ID     string
    Search string
}

type Provider interface {
    Execute(context.Context, Query) (Result, error)
}
```

Диспетчер выбирает `Provider` по имени интегрируемой системы. Если имя системы не указано, используется `default_systems` для типа интеграции.

## 5. Нормализованные сущности

Чтобы не распространять специфику внешних систем по другим контурам, вводятся канонические сущности:

- `CanonicalTask`;
- `TaskComment`;
- `Repository`;
- `MergeRequest`;
- `ReviewRemark`;
- `MessageThread`;
- `Message`;
- `MessageReaction`;
- `Failure`;
- `OperationResult`.

Для перехода соседних контуров сохраняется заполнение прежних структур, когда результат может быть однозначно преобразован:

- `TrackerIssue`;
- `TrackerPullRequest`;
- `TrackerComment`;
- `TrackerReview`;
- `TrackerRepository`;
- `TrackerUser`;
- `TrackerSearchResult`.

Минимальные поля `TrackerIssue`:

- `System`;
- `Repository`;
- `Number`;
- `Title`;
- `Body`;
- `State`;
- `Labels`;
- `Assignees`;
- `Author`;
- `URL`;
- `CreatedAt`;
- `UpdatedAt`.

Минимальные поля `TrackerPullRequest`:

- `System`;
- `Repository`;
- `Number`;
- `Title`;
- `Body`;
- `State`;
- `Author`;
- `ReviewDecision`;
- `BaseRef`;
- `HeadRef`;
- `Labels`;
- `URL`;
- `CreatedAt`;
- `UpdatedAt`.

## 6. Состав CLI-команд

В текущей конфигурации поддерживаются следующие команды:

- `progress integration dispatcher`;
- `progress integration dispatch`;
- `progress integration github auth status`;
- `progress integration github repo get`;
- `progress integration github issue get`;
- `progress integration github issue comments`;
- `progress integration github issue comment create`;
- `progress integration github pr get`;
- `progress integration github pr list`;
- `progress integration github pr search`;
- `progress integration github pr create`;
- `progress integration github pr comments`;
- `progress integration github pr comment create`;
- `progress integration github pr comment resolve`;
- `progress integration bitbucket auth status`;
- `progress integration bitbucket repo get`;
- `progress integration bitbucket pr get`;
- `progress integration bitbucket pr list`;
- `progress integration bitbucket pr search`;
- `progress integration bitbucket pr create`;
- `progress integration bitbucket pr comments`;
- `progress integration bitbucket pr comment create`;
- `progress integration bitbucket pr comment resolve`;
- `progress integration mattermost auth status`;
- `progress integration mattermost thread get`;
- `progress integration mattermost message create`;
- `progress integration telegram auth status`;
- `progress integration telegram thread get`;
- `progress integration telegram message create`.

## 7. Назначение команд

### 7.1 `progress integration dispatcher`

Команда вызывает диспетчер интеграционного контура как отдельный модуль. Она нужна для диагностики маршрута: какой адаптер будет выбран, какие требования к доступу предъявляются и какой тип нормализованного объекта ожидается на выходе.

### 7.2 `progress integration github auth status`

Команда проверяет, доступен ли `gh` и авторизован ли пользователь.

Базовый системный вызов:

```bash
gh auth status
```

Назначение команды:

1. проверить наличие `gh` в `PATH`;
2. убедиться, что для GitHub выполнена авторизация;
3. вернуть диагностируемую ошибку, если дальнейшие вызовы невозможны.

### 7.3 `progress integration github repo get`

Команда получает сведения о репозитории по `owner/name`.

Предпочтительный вызов:

```bash
gh repo view owner/name --json name,owner,description,defaultBranchRef,url
```

Возвращаемые сведения используются как базовый контекст для последующих вызовов задач и запросов на слияние.

### 7.4 `progress integration github issue get`

Команда получает одну карточку issue по номеру и репозиторию.

Предпочтительный вызов:

```bash
gh issue view 123 --repo owner/name --json number,title,body,state,labels,assignees,author,url,createdAt,updatedAt
```

Назначение команды:

1. извлечь нормализованное представление задачи;
2. дать контуру принятия решения исходную карточку задачи;
3. поддержать прямую диагностику интеграции через CLI.

### 7.5 `progress integration github issue comments`

Команда получает комментарии issue.

Вариант через `gh api`:

```bash
gh api --paginate --slurp repos/owner/name/issues/123/comments
```

Команда принимает `--repo` и обязательный `--number`. Если `--repo` не передан, адаптер может использовать `default_repo` из `.progress/integration/systems.json` или глобального `$PROGRESS_CONFIG_HOME/integration/systems.json`; если пользователь явно передал пустой `--repo=`, резервный выбор не применяется и запрос отклоняется как `invalid-request`.

Адаптер преобразует paginated REST-ответ GitHub в массив `TrackerComment` с полями `System`, `Repository`, `Number`, `Author`, `Body`, `URL`, `CreatedAt` и `UpdatedAt`. В текстовом выводе команда печатает общий `comment_count`, затем поля каждого комментария и тело построчно.

### 7.6 `progress integration github issue search`

Команда выполняет поиск issue в репозитории или во всём доступном GitHub-пространстве.

Вариант вызова:

```bash
gh issue list --repo owner/name --search "is:open label:bug" --json number,title,state,labels,assignees,url,updatedAt
```

Команда должна поддерживать:

- фильтр по репозиторию;
- строку поиска GitHub;
- ограничение количества результатов;
- режим краткого и подробного вывода.

### 7.7 `progress integration github pr get`

Команда получает одну карточку запроса на слияние.

Предпочтительный вызов:

```bash
gh pr view 456 --repo owner/name --json number,title,body,state,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt
```

Результат должен использоваться как нормализованная карточка запроса на слияние.

### 7.8 `progress integration github pr comments`

Команда получает комментарии запроса на слияние, включая замечания ревизии.

Адаптер GitHub читает два источника:

1. обычные комментарии обсуждения PR через issue-часть запроса на слияние;
2. inline-замечания ревизии через review threads GraphQL API.

Предпочтительные вызовы:

```bash
gh api --paginate --slurp repos/owner/name/issues/456/comments
gh api graphql -f query='<review threads query>' -f owner=owner -f name=name -F number=456
```

Адаптер приводит оба вида комментариев к `ReviewRemark`. Для обычного комментария поля `Path`, `Line` и `ReplyToID` пустые. Для inline-замечания `ReplyToID` содержит идентификатор review thread, а `State` отражает состояние `resolved` или `unresolved`.

### 7.9 `progress integration github pr search`

Команда выполняет поиск запросов на слияние по фильтрам GitHub.

Вариант вызова:

```bash
gh pr list --repo owner/name --state closed --limit 30 --json number,title,body,state,author,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt
```

Команда поддерживает:

- `--state` со значениями `open`, `closed`, `merged` и `all`; по умолчанию используется `closed`;
- `--scope` со значениями `all`, `authored` и `reviewer`; по умолчанию используется `all`;
- `--query` для дополнительной строки поиска GitHub;
- `--limit` для ограничения количества результатов.

Для `--scope authored` адаптер добавляет `--author @me`. Для `--scope reviewer` адаптер добавляет поисковый фильтр `reviewed-by:@me`.

Если `--repo` не передан, GitHub-адаптер сначала использует `default_repo` из конфигурации, а при его отсутствии вызывает `gh pr list` без `--repo`, чтобы `gh` выбрал текущий репозиторий рабочей директории.

### 7.10 `progress integration github pr comment create`

Команда создаёт комментарий к запросу на слияние.

Обычный комментарий обсуждения:

```bash
progress integration github pr comment create --repo owner/name --number 456 --body "Проверил, замечание принято"
```

Inline-замечание ревизии:

```bash
progress integration github pr comment create --repo owner/name --number 456 --body "Нужно обработать пустой ответ" --path internal/service.go --line 42
```

Если `--path` и `--line` не переданы, адаптер создаёт обычный комментарий обсуждения через issue-часть PR. Если они переданы, адаптер создаёт review thread через GraphQL mutation `addPullRequestReviewThread`. Флаг `--side` задаёт сторону diff и по умолчанию равен `RIGHT`.

### 7.11 `progress integration github pr comment resolve`

Команда разрешает inline-замечание ревизии по идентификатору review thread.

```bash
progress integration github pr comment resolve --thread PRRT_kw...
```

Команда использует GraphQL mutation `resolveReviewThread`. Идентификатор thread можно получить из поля `remark_thread_id` команды `progress integration github pr comments`.

### 7.12 `progress integration bitbucket pr search`

Команда выполняет поиск запросов на слияние в Bitbucket.

Для Bitbucket Cloud закрытое состояние разворачивается в состояния API `MERGED` и `DECLINED`, потому что единого внешнего значения `closed` у Cloud API нет. По умолчанию команда ищет закрытые запросы на слияние.

Команда поддерживает:

- `--state` со значениями `open`, `closed`, `merged`, `declined` и `all`;
- `--scope` со значениями `all`, `authored` и `reviewer` для Bitbucket Cloud;
- `--query` для выражения фильтра Bitbucket Cloud;
- `--limit` для ограничения количества результатов.

### 7.13 `progress integration bitbucket pr comment create`

Команда создаёт комментарий к запросу на слияние Bitbucket Cloud. Для inline-комментария используются `--path`, `--line` и `--side`.

`progress integration bitbucket pr comment resolve` присутствует в CLI как единая операция контура, но текущий Bitbucket-адаптер возвращает `unsupported-operation`, потому что механизм разрешения замечаний различается между Bitbucket Cloud и Server/Data Center и требует отдельного контракта.

### 7.14 `progress integration github api`

Команда даёт управляемый escape hatch для редких операций, которые ещё не вынесены в отдельный подкомандный интерфейс.

Пример вызова:

```bash
progress integration github api repos/owner/name/issues/123/events
```

Ограничение команды состоит в том, что она не должна становиться основным пользовательским интерфейсом контура. Её задача - ускорить расширение адаптера без немедленного разрастания CLI-дерева.

## 8. Схема дерева команд

```mermaid
flowchart TD
    A[progress] --> B[integration]
    B --> C[dispatcher]
    B --> D[github]
    D --> E[auth status]
    D --> F[repo get]
    D --> G[issue get]
    D --> H[issue comments]
    D --> I[issue search]
    D --> J[pr get]
    D --> K[pr comments]
    D --> L[pr search]
    D --> M[api]
```

## 9. Общие флаги и правила вызова

Для унификации вызовов целесообразно ввести общие флаги:

- `--repo` — репозиторий `owner/name`;
- `--number` — номер issue или PR;
- `--query` — поисковая строка;
- `--limit` — ограничение числа результатов;
- `--format` — `text` или `json`;
- `--jq` или внутренний фильтр, если позже потребуется пользовательская фильтрация результата.

Базовые правила:

1. если операция требует репозиторий, `--repo` обязателен, кроме `github repo get`, `github issue get` и `github pr get`, где можно опустить `--repo` и использовать `default_repo` из конфигурации;
2. если операция адресует сущность по номеру, `--number` обязателен;
3. для машинного использования предпочтителен `--format json`;
4. текстовый вывод нужен для ручной диагностики и первичного освоения CLI.

## 10. Обработка ошибок

GitHub-адаптер должен различать как минимум следующие классы ошибок:

- `gh` не установлен;
- `gh` не авторизован;
- репозиторий не найден;
- issue или PR не найден;
- доступ запрещён;
- превышен rate limit;
- формат ответа неожиданно изменился;
- внешний вызов завершился по таймауту.

Для других контуров ошибка должна возвращаться не как сырая строка `stderr`, а как структурированное состояние с признаком:

- `temporary-unavailable`;
- `auth-required`;
- `not-found`;
- `invalid-request`;
- `internal-integration-error`.

## 11. Конфигурация

Контур интеграции использует двухслойную конфигурацию систем:

1. глобальный слой `$PROGRESS_CONFIG_HOME/integration/systems.json` или `~/.config/progress/integration/systems.json`;
2. локальный слой `.progress/integration/systems.json`.

Локальный слой имеет приоритет над глобальным:

1. `default_system` полностью заменяет глобальное значение;
2. `default_systems.<type>` заменяет систему по умолчанию для конкретного типа интеграции;
3. `systems.<name>` дополняет или переопределяет одноимённую систему из глобального слоя;
4. простые поля системы, например `command`, `path`, `timeout`, `base_url`, `token_env`, `repository`, `project`, `channel_id`, `chat_id` и `default_repo`, заменяются локальными значениями;
5. `operations` сливается по ключу операции;
6. `enabled=false` в локальном слое выключает систему целиком.

Минимальная конфигурация встроенных адаптеров может выглядеть так:

```json
{
  "default_systems": {
    "tracker": "github",
    "repository": "github",
    "messenger": "mattermost"
  },
  "systems": {
    "github": {
      "type": "github",
      "integration_types": ["tracker", "repository"],
      "enabled": true,
      "command": "gh",
      "timeout": "30s",
      "repository": "owner/name",
      "default_repo": "owner/name"
    },
    "bitbucket": {
      "type": "bitbucket",
      "integration_type": "repository",
      "enabled": true,
      "base_url": "https://api.bitbucket.org/2.0",
      "api_variant": "cloud",
      "token_env": "BITBUCKET_TOKEN",
      "workspace": "workspace",
      "repository": "workspace/repository"
    },
    "mattermost": {
      "type": "mattermost",
      "integration_type": "messenger",
      "enabled": true,
      "base_url": "https://mattermost.example",
      "token_env": "MATTERMOST_TOKEN",
      "channel_id": "channel-id"
    },
    "telegram": {
      "type": "telegram",
      "integration_type": "messenger",
      "enabled": true,
      "token_env": "TELEGRAM_BOT_TOKEN",
      "chat_id": "chat-id"
    }
  }
}
```

Назначение общих полей системы:

1. `type` фиксирует тип встроенного адаптера: `github`, `bitbucket`, `mattermost`, `telegram` или `script`;
2. `integration_type` задаёт один тип интеграции, а `integration_types` — несколько типов для одной системы;
3. `enabled` включает или выключает систему без удаления её описания;
4. `default=true` делает систему системой по умолчанию для её типов, если `default_systems` не задаёт явное значение;
5. `command` и `path` позволяют переопределить исполняемый файл `gh`;
6. `timeout` ограничивает внешний вызов;
7. `base_url` задаёт базовый адрес HTTP API для Bitbucket, Mattermost или Telegram;
8. `api_variant` задаёт вариант Bitbucket API: `cloud` для `api.bitbucket.org/2.0` или `server` для Bitbucket Server/Data Center через `rest/api/1.0`;
9. `token` или `token_env` задают данные авторизации либо ссылку на переменную окружения;
10. `repository` задаёт резервный репозиторий для репозиторных операций;
11. `workspace` задаёт рабочее пространство Bitbucket Cloud, если `--repo` передан без префикса;
12. `project` задаёт ключ проекта Bitbucket Server/Data Center, если `--repo` передан без префикса;
13. `channel_id` задаёт резервный канал Mattermost;
14. `chat_id` задаёт резервный чат Telegram;
15. `operations` резервирует пространство для пооперационной настройки.

Правило приоритета `--repo`, `repository` и `default_repo`:

1. если `--repo` не передан, сервис использует `repository`, а при его отсутствии может использовать `default_repo`;
2. если `--repo` передан с непустым значением, используется именно это значение;
3. если `--repo` передан явно пустым (`--repo=`), запрос отклоняется как `invalid-request`, резервный выбор через `repository` и `default_repo` не применяется.

Для переходного периода сохраняется совместимость с локальным файлом `.progress/integration/github.json`, если новый `systems.json` отсутствует. Новый конфигурационный слой считается приоритетным и должен использоваться как основной.

## 12. Текущее состояние реализации

Реализованный рабочий срез включает:

1. диспетчер `Dispatch`, который выбирает интегрируемую систему по `IntegrationType`, `System` и `default_systems`;
2. единый вызов `Execute`, который возвращает `Response` с каноническим объектом, маршрутом и отказным состоянием;
3. GitHub-адаптер через `gh` для задач, комментариев задач, репозиториев и запросов на слияние;
4. Bitbucket-адаптер через HTTP API для репозиториев и запросов на слияние;
5. Mattermost-адаптер через HTTP API для цепочек обсуждения и сообщений;
6. Telegram-адаптер через Bot API для отправки сообщений;
7. нормализованные отказные состояния для отсутствия авторизации, недоступности, неподдерживаемой операции, неполного ответа и ошибок внешнего источника.

Принцип проектирования остаётся прежним: другие контуры не должны знать синтаксис `gh`, HTTP-маршруты Bitbucket, Mattermost или Telegram, формат токенов и поля внешних ответов. Эти сведения остаются внутри адаптеров, а наружу выходит канонический ответ контура интеграции.
