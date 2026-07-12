# CLI контура интеграции

## 1. Назначение документа

Настоящий документ определяет практический срез контура интеграции с внешними системами. Контур поддерживает GitHub как трекер и репозиторий, Bitbucket как репозиторий, Mattermost и Telegram как мессенджеры, Confluence как тип интеграции `wiki`.

Документ фиксирует:

- модель работы интеграционного слоя;
- границы ответственности между CLI, сервисом интеграции, `gh` и HTTP API внешних систем;
- состав пользовательских команд;
- порядок настройки встроенных адаптеров.

## 2. Принцип встроенных интеграций

GitHub-адаптер поддерживает два способа обращения к внешней системе: режим `cli` через `gh` и режим `api` через GitHub REST API и GraphQL API. Если способ не указан, сохраняется совместимый режим `cli`. Bitbucket, Mattermost, Telegram и Confluence используют HTTP API и нормализуют ответы в те же канонические структуры.

Базовая схема вызова:

1. CLI принимает входной запрос пользователя или другого контура;
2. сервис интеграции формирует внутренний `IntegrationRequest`;
3. реестр выбирает адаптер по типу интеграции и имени системы;
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

## 3. Режимы GitHub-адаптера

Режим `cli` запускает `gh` как внешний инструмент. На первом этапе такой подход был предпочтителен по следующим причинам:

- для GitHub не требуется отдельно реализовывать OAuth, PAT и хранение приватных значений;
- можно опереться на уже существующую авторизацию пользователя в `gh auth login`;
- `gh` уже покрывает типовые GitHub-сущности: `issue`, `pull request`, `comments`, `search`, `repo`, `api`;
- интеграция может начать работать как тонкий адаптер без преждевременного усложнения кодовой базы.

Ограничения такого подхода:

- требуется установленный бинарь `gh`;
- часть ошибок приходит как текст из `stderr` и должна быть нормализована;
- доступные операции ограничены возможностями установленной версии `gh`.

Режим `api` выполняет прямые HTTP-вызовы к GitHub REST API и GraphQL API. Он использует `token`, `token_env`, `token_private` или данные GitHub App из описания системы, по умолчанию обращается к `https://api.github.com`, а для GitHub Enterprise Server может получать базовый адрес через `base_url`. CLI-команды контура при этом не меняются: наружу возвращаются те же канонические объекты `Response`, `CanonicalTask`, `Repository`, `MergeRequest`, `ReviewRemark`, `OperationResult` и `Failure`.

### 3.1 Авторизация GitHub App

В режиме `api` GitHub-адаптер может работать через установку GitHub App. В этом случае в настройке интегрируемой системы задаются данные приложения и установки, а контур интеграции выпускает установочный токен внутри процесса.

Минимальная настройка:

```json
{
  "default_systems": {
    "issue": "github-app",
    "repo": "github-app"
  },
  "systems": {
    "github-app": {
      "type": "github",
      "transport": "api",
      "default_repo": "rasungatullin/progress",
      "github_app_id": "12345",
      "github_app_installation_id": "144549701",
      "github_app_private_key_path": "~/.progress/integration_data/progress-synthesis.2026-07-05.private-key.pem"
    }
  }
}
```

Вместо `github_app_id` можно задать `github_app_client_id`; если заполнены оба поля, для `iss` JWT используется `github_app_id`. PEM-ключ можно передать через `github_app_private_key_private`; тогда значение читается из выбранного хранилища приватных значений и не записывается в проектную конфигурацию.

Дополнительное поле `github_app_token_refresh_before` задаёт упреждающее обновление установочного токена. Если поле не задано, применяется запас `5m`. Значение должно быть положительной длительностью Go, например `10m`.

Порядок работы:

1. адаптер формирует JWT приложения с `iat`, `exp`, `iss` и подписью `RS256`;
2. выполняет `POST /app/installations/{github_app_installation_id}/access_tokens`;
3. сохраняет установочный токен только в памяти процесса;
4. использует его как bearer-токен для REST API и GraphQL API;
5. переиздаёт токен до `expires_at` с заданным запасом.

JWT, установочный токен и содержимое PEM не включаются в CLI-вывод, структурированный вывод, журналы или `CommandResult.Stdout`. Для живой проверки после заполнения `github_app_id` или `github_app_client_id` можно выполнить:

```bash
progress integration github auth status --system github-app --format json
progress integration github repo get --system github-app --repo rasungatullin/progress --format json
```

## 4. Внутренние модули реализации

Минимальный состав кода для первого этапа:

- `internal/integration/service.go` — фасад контура интеграции;
- `internal/integration/model/types.go` — типы запросов и нормализованных ответов;
- `internal/integration/github/service.go` — адаптер GitHub;
- `internal/integration/github/runner.go` — безопасный запуск `gh`;
- `internal/integration/github/api_runner.go` — прямой вызов GitHub REST API и GraphQL API;
- `internal/integration/bitbucket/service.go` — адаптер Bitbucket;
- `internal/integration/mattermost/service.go` — адаптер Mattermost;
- `internal/integration/telegram/service.go` — адаптер Telegram;
- `internal/integration/confluence/service.go` — адаптер Confluence;
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

Реестр выбирает `Provider` по имени интегрируемой системы. Если имя системы не указано, используется `default_systems` для типа интеграции.

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
- `WikiPage`;
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
- `ID`;
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

В текущей конфигурации поддерживаются следующие команды. Команды с именем
внешней системы сохранены только как переходная форма: при запуске они выводят
предупреждение и должны постепенно заменяться типо-ориентированными командами
с флагом `--system`.

- `progress integration operations`;
- `progress integration issue get --id 123`;
- `progress integration issue search --query "текст"`;
- `progress integration repo get --system github`;
- `progress integration github auth status`;
- `progress integration github repo get`;
- `progress integration github issue get`;
- `progress integration github issue comments`;
- `progress integration github issue comment create`;
- `progress integration github issue label add`;
- `progress integration github issue label remove`;
- `progress integration github pr get`;
- `progress integration github pr list`;
- `progress integration github pr search`;
- `progress integration github pr create`;
- `progress integration github pr comments`;
- `progress integration github pr comment create`;
- `progress integration github pr comment reply`;
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
- `progress integration telegram message create`;
- `progress integration confluence auth status`;
- `progress integration confluence page get`;
- `progress integration confluence page search`.

## 7. Назначение команд

### 7.0 Типо-ориентированные команды

Новые команды выбирают предметный тип объекта, а не название внешней системы:

```text
progress integration issue get --id ABC-123
progress integration issue search --query "готово к реализации" --system jira-main
```

Флаг `--id` принимает непрозрачную строку: как числовое значение `123`, так и значение внешней системы `ABC-123`. Если `--system` не задан, контур выбирает систему по умолчанию для типа `issue`; явное значение выбирается по имени записи в конфигурации. Старые команды вида `integration github issue ...` сохраняются как совместимый переход.

### 7.1 `progress integration operations`

Команда публикует каталог интеграционных операций, доступных по текущей конфигурации систем. Каталог нужен контуру исполнения и диагностическим проверкам: он показывает каноническое имя операции, интегрируемую систему, тип адаптера, контракт входных полей, тип результата, признак побочного действия и возможные отказные состояния.

Базовые вызовы:

```bash
progress integration operations
progress integration operations --format json
progress integration operations --type issue
progress integration operations --system github
progress integration operations --name issue.issue.get
```

Каноническое имя операции строится по схеме:

```text
<тип-интеграции>.<объект>.<операция>
```

Примеры:

- `issue.issue.get`;
- `issue.issue.comment.create`;
- `repo.merge-request.create`;
- `repo.comment.reply`;
- `repo.comment.resolve`;
- `messenger.message.create`;
- `wiki.page.search`.

Текстовый вывод предназначен для ручной диагностики. JSON-вывод возвращает массив `OperationDescriptor` и пригоден для машинного чтения.

Отключённая система может присутствовать в каталоге только с `available=false`. Сценарные системы типа `script` публикуют операции из `systems.<name>.operations`; доступными считаются только операции с заданным `script`, `command` или `path`. Контракт входных полей сохраняется из конфигурации.

Хранилище приватных значений не входит в публичное пространство имён интеграции и настраивается только через контур настроек и ресурсов.

### 7.4 `progress integration github auth status`

Команда проверяет доступность GitHub-интеграции. В режиме `cli` она проверяет, доступен ли `gh` и авторизован ли пользователь. В режиме `api` она проверяет, что токен задан и GitHub API принимает запрос.

Базовый системный вызов:

```bash
gh auth status
```

Назначение команды:

1. проверить наличие `gh` в `PATH`;
2. убедиться, что для GitHub выполнена авторизация;
3. вернуть диагностируемую ошибку, если дальнейшие вызовы невозможны.

Для режима `api` с готовым токеном вместо системного вызова выполняется `GET /user` с заголовком `Authorization: Bearer <token>`. Для режима GitHub App команда выпускает установочный токен через `POST /app/installations/{installation_id}/access_tokens` и считает интеграцию готовой, если выпуск завершён успешно.

### 7.5 `progress integration github repo get`

Команда получает сведения о репозитории по `owner/name`.

Предпочтительный вызов:

```bash
gh repo view owner/name --json name,owner,description,defaultBranchRef,url
```

Возвращаемые сведения используются как базовый контекст для последующих вызовов задач и запросов на слияние.

### 7.6 `progress integration github issue get`

Команда получает одну карточку issue по номеру и репозиторию.

Предпочтительный вызов:

```bash
gh issue view 123 --repo owner/name --json number,title,body,state,labels,assignees,author,url,createdAt,updatedAt
```

Назначение команды:

1. извлечь нормализованное представление задачи;
2. дать контуру принятия решения исходную карточку задачи;
3. поддержать прямую диагностику интеграции через CLI.

### 7.7 `progress integration github issue comments`

Команда получает комментарии issue.

Вариант через `gh api`:

```bash
gh api --paginate --slurp repos/owner/name/issues/123/comments
```

Команда принимает `--repo` и обязательный `--number`. Если `--repo` не передан, адаптер может использовать `default_repo` из `.progress/integration/systems.json` или глобального `$PROGRESS_CONFIG_HOME/integration/systems.json`; если пользователь явно передал пустой `--repo=`, резервный выбор не применяется и запрос отклоняется как `invalid-request`.

Адаптер преобразует paginated REST-ответ GitHub в массив `TrackerComment` с полями `System`, `Repository`, `TaskID`, `Author`, `Body`, `URL`, `CreatedAt` и `UpdatedAt`. В текстовом выводе команда печатает общий `comment_count`, затем поля каждого комментария и тело построчно.

### 7.8 `progress integration github issue label add`

Команда добавляет к задаче одну или несколько меток по каноническим названиям.

```bash
progress integration github issue label add --repo owner/name --number 123 --label bug --label backend
```

Поле `--label` принимает каноническое название метки задачи. Перед вызовом GitHub контур интеграции переводит каноническое название во внешнее имя по `task_label_mapping`. Если сопоставление не задано, используется то же название.

Вариант системного вызова:

```bash
gh issue edit 123 --repo owner/name --add-label bug --add-label backend
```

### 7.9 `progress integration github issue label remove`

Команда снимает с задачи одну или несколько меток по каноническим названиям.

```bash
progress integration github issue label remove --repo owner/name --number 123 --label bug
```

Вариант системного вызова:

```bash
gh issue edit 123 --repo owner/name --remove-label bug
```

### 7.10 `progress integration github issue search`

Команда получает список задач GitHub с фильтрами по состоянию, строке поиска и меткам.

Вариант вызова:

```bash
progress integration github issue search \
  --repo owner/name \
  --label "Готово к реализации" \
  --exclude-label "Требует проработки" \
  --state open
```

Вариант системного вызова:

```bash
gh issue list --repo owner/name --state open --limit 30 --json number,title,state,labels,assignees,author,url,createdAt,updatedAt --search 'label:"Готово к реализации" -label:"Требует проработки"'
```

Команда поддерживает:

- `--repo` — репозиторий GitHub в формате `owner/name`; если флаг не передан, адаптер может использовать `default_repo` из конфигурации; если пользователь явно передал пустой `--repo=`, резервный выбор не применяется и запрос отклоняется как `invalid-request`;
- `--state` — состояние задач: `open`, `closed`, `all`; по умолчанию `open`;
- `--label` — включающая метка, флаг можно повторять; несколько меток задают пересечение;
- `--exclude-label` — исключающая метка, флаг можно повторять; несколько меток исключают задачи, у которых есть любая из этих меток;
- `--query` — дополнительная строка поиска GitHub, добавляется к сформированным фильтрам по меткам;
- `--limit` — предельное число задач;
- `--format json` — машинно-читаемый вывод без потери меток.

Поля `--label` и `--exclude-label` принимают канонические названия меток задачи. Перед вызовом GitHub контур интеграции переводит их во внешние имена по `task_label_mapping`. Если сопоставление не задано, используется то же название.

Текстовый вывод содержит количество найденных задач и основные поля каждой задачи: номер, заголовок, состояние, метки, автор, назначенные исполнители, URL и время изменения.

### 7.11 `progress integration github pr get`

Команда получает одну карточку запроса на слияние.

Предпочтительный вызов:

```bash
gh pr view 456 --repo owner/name --json number,title,body,state,author,labels,reviewDecision,baseRefName,headRefName,url,createdAt,updatedAt
```

Результат должен использоваться как нормализованная карточка запроса на слияние.

### 7.12 `progress integration github pr comments`

Команда получает комментарии запроса на слияние, включая замечания ревизии.

Адаптер GitHub читает два источника:

1. обычные комментарии обсуждения PR через issue-часть запроса на слияние;
2. inline-замечания ревизии через review threads GraphQL API.

Предпочтительные вызовы:

```bash
gh api --paginate --slurp repos/owner/name/issues/456/comments
gh api graphql -f query='<review threads query>' -f owner=owner -f name=name -F number=456
```

Адаптер приводит оба вида комментариев к `ReviewRemark`. Поле `Type` принимает значение `comment` для обычного комментария и `inline` для замечания ревизии в цепочке. Для обычного комментария поля `Path`, `Line` и `ReplyToID` пустые. Для inline-замечания `ReplyToID` содержит идентификатор review thread, а `State` отражает состояние `resolved` или `unresolved`.

### 7.13 `progress integration github pr search`

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

В режиме `api` базовый список запросов на слияние поддерживает `--state`, `--limit` и явный или резервный репозиторий. Расширенные фильтры `--query`, `--scope authored` и `--scope reviewer` временно возвращают отказ `unsupported-operation`, пока сопоставление с поисковым API GitHub не закреплено в адаптере.

### 7.14 `progress integration github pr comment create`

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

### 7.15 `progress integration github pr comment reply`

Команда создаёт ответ в существующей цепочке inline-замечания ревизии.

```bash
progress integration github pr comment reply --thread PRRT_kw... --body "Исправлено в новом коммите"
```

Команда использует GraphQL mutation `addPullRequestReviewThreadReply`. Идентификатор thread можно получить из поля `remark_thread_id` команды `progress integration github pr comments` или из ответа команды создания inline-замечания.

### 7.16 `progress integration github pr comment resolve`

Команда разрешает inline-замечание ревизии по идентификатору review thread.

```bash
progress integration github pr comment resolve --thread PRRT_kw...
```

Команда использует GraphQL mutation `resolveReviewThread`. Идентификатор thread можно получить из поля `remark_thread_id` команды `progress integration github pr comments`.

### 7.17 `progress integration bitbucket pr search`

Команда выполняет поиск запросов на слияние в Bitbucket.

Для Bitbucket Cloud закрытое состояние разворачивается в состояния API `MERGED` и `DECLINED`, потому что единого внешнего значения `closed` у Cloud API нет. По умолчанию команда ищет закрытые запросы на слияние.

Команда поддерживает:

- `--state` со значениями `open`, `closed`, `merged`, `declined` и `all`;
- `--scope` со значениями `all`, `authored` и `reviewer` для Bitbucket Cloud;
- `--query` для выражения фильтра Bitbucket Cloud;
- `--limit` для ограничения количества результатов.

### 7.18 `progress integration bitbucket pr comment create`

Команда создаёт комментарий к запросу на слияние Bitbucket Cloud. Для inline-комментария используются `--path`, `--line` и `--side`.

`progress integration bitbucket pr comment resolve` присутствует в CLI как единая операция контура, но текущий Bitbucket-адаптер возвращает `unsupported-operation`, потому что механизм разрешения замечаний различается между Bitbucket Cloud и Server/Data Center и требует отдельного контракта.

### 7.19 `progress integration github api`

Команда даёт управляемый резервный путь для редких операций, которые ещё не вынесены в отдельный подкомандный интерфейс.

Пример вызова:

```bash
progress integration github api repos/owner/name/issues/123/events
```

Ограничение команды состоит в том, что она не должна становиться основным пользовательским интерфейсом контура. Её задача — ускорить расширение адаптера без немедленного разрастания CLI-дерева.

### 7.19 `progress integration confluence auth status`

Команда проверяет доступность Confluence через HTTP API. Для локально размещённой версии Confluence Server/Data Center используется базовый адрес из `base_url` и путь `/rest/api/user/current`.

Настройка поддерживает два режима авторизации:

- `username` + `token` или `token_env` — HTTP Basic;
- только `token` или `token_env` — заголовок `Authorization: Bearer`.

### 7.20 `progress integration confluence page get`

Команда получает страницу документации по идентификатору Confluence.

```bash
progress integration confluence page get --id 12345
```

Вариант HTTP-вызова для локально размещённой версии Confluence:

```bash
GET /rest/api/content/12345?expand=space,body.storage,version,history
```

Адаптер возвращает `WikiPage` — страницу документации с идентификатором, пространством, заголовком, телом в формате `storage`, номером версии, временем обновления, пользователем обновления и ссылкой на страницу.

### 7.21 `progress integration confluence page search`

Команда ищет страницы документации по CQL-запросу Confluence.

```bash
progress integration confluence page search --query 'type=page and text ~ "integration"' --limit 10
```

Вариант HTTP-вызова:

```bash
GET /rest/api/content/search?cql=type%3Dpage&limit=10&expand=space,version
```

Команда возвращает массив `WikiPage` без обязательного тела страницы. Для получения полного тела нужно выполнить `page get` по идентификатору.

## 8. Схема дерева команд

```mermaid
flowchart TD
    A[progress] --> B[integration]
    B --> C[реестр типов, систем и операций]
    B --> D[github]
    D --> E[auth status]
    D --> F[repo get]
    D --> G[issue get]
    D --> H[issue comments]
    D --> I[issue comment create]
    D --> J[issue label add]
    D --> K[issue label remove]
    D --> L[pr get]
    D --> M[pr list]
    D --> N[pr comments]
    D --> O[pr comment create]
    D --> P[pr comment reply]
    D --> AA[pr comment resolve]
    B --> Q[bitbucket]
    Q --> R[auth status]
    Q --> S[repo get]
    Q --> T[pr get]
    Q --> U[pr list]
    Q --> V[pr comments]
    Q --> W[pr comment create]
    B --> X[mattermost]
    X --> Y[auth status]
    X --> Z[thread get]
    X --> AA[message create]
    B --> AB[telegram]
    AB --> AC[auth status]
    AB --> AD[thread get]
    AB --> AE[message create]
    B --> AF[confluence]
    AF --> AG[auth status]
    AF --> AH[page get]
    AF --> AI[page search]
```

## 9. Общие флаги и правила вызова

Для унификации вызовов целесообразно ввести общие флаги:

- `--repo` — репозиторий `owner/name`;
- `--number` — номер issue или PR;
- `--label` — каноническое название метки задачи;
- `--id` — внешний идентификатор объекта, если источник не использует числовой номер;
- `--query` — поисковая строка;
- `--limit` — ограничение числа результатов;
- `--format` — `text` или `json`;
- `--jq` или внутренний фильтр, если позже потребуется пользовательская фильтрация результата.

Базовые правила:

1. если операция требует репозиторий, `--repo` обязателен, кроме `github repo get`, `github issue get` и `github pr get`, где можно опустить `--repo` и использовать `default_repo` из конфигурации;
2. если операция адресует сущность по номеру, `--number` обязателен;
3. если операция изменяет метки задачи, `--label` задаётся каноническим названием и может повторяться;
4. если операция адресует страницу документации, `--id` содержит внешний идентификатор страницы;
5. для машинного использования предпочтителен `--format json`;
6. текстовый вывод нужен для ручной диагностики и первичного освоения CLI.

## 10. Обработка ошибок

GitHub-адаптер должен различать как минимум следующие классы ошибок:

- `gh` не установлен;
- `gh` не авторизован;
- токен API не задан или отклонён внешней системой;
- репозиторий не найден;
- issue или PR не найден;
- доступ запрещён;
- превышен rate limit;
- формат ответа неожиданно изменился;
- внешний вызов завершился по таймауту.

Для других контуров ошибка должна возвращаться не как сырая строка `stderr`, а как структурированное состояние с признаком:

- `temporary-unavailable`;
- `auth-required`;
- `permission-denied`;
- `not-found`;
- `invalid-request`;
- `unsupported-operation`;
- `internal-integration-error`.

## 11. Конфигурация

Контур интеграции использует двухслойную конфигурацию систем:

1. глобальный слой `$PROGRESS_CONFIG_HOME/integration/systems.json` или `~/.config/progress/integration/systems.json`;
2. локальный слой `.progress/integration/systems.json`.

Локальный слой имеет приоритет над глобальным:

1. `default_system` полностью заменяет глобальное значение;
2. `default_systems.<type>` заменяет систему по умолчанию для конкретного типа интеграции;
3. `private_store` задаёт реализацию хранилища приватных значений и сливается по простым полям;
4. `systems.<name>` дополняет или переопределяет одноимённую систему из глобального слоя;
5. простые поля системы, например `transport`, `command`, `path`, `timeout`, `base_url`, `token_private`, `token_env`, `github_app_id`, `github_app_client_id`, `github_app_installation_id`, `github_app_private_key_path`, `github_app_private_key_private`, `github_app_token_refresh_before`, `repository`, `project`, `channel_id`, `chat_id` и `default_repo`, заменяются локальными значениями;
6. `database` дополняется по полям `driver`, `path` и `dsn`;
7. `settings` сливается по имени настройки;
8. `task_label_mapping` сливается по внешней метке;
9. `operations` сливается по ключу операции;
10. `enabled=false` в локальном слое выключает систему целиком.

Минимальная конфигурация встроенных адаптеров может выглядеть так:

```json
{
  "private_store": {
    "type": "keychain",
    "service": "progress"
  },
  "default_systems": {
    "issue": "github",
    "repo": "github",
    "messenger": "mattermost",
    "wiki": "confluence"
  },
  "systems": {
    "github": {
      "type": "github",
      "integration_types": ["issue", "repo"],
      "enabled": true,
      "transport": "cli",
      "command": "gh",
      "timeout": "30s",
      "repository": "owner/name",
      "default_repo": "owner/name",
      "task_label_mapping": {
        "bug": "bug",
        "type:backend": "backend",
        "external-only": ""
      }
    },
    "bitbucket": {
      "type": "bitbucket",
      "integration_type": "repo",
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
      "token_private": "mt_auth_token",
      "channel_id": "channel-id"
    },
    "telegram": {
      "type": "telegram",
      "integration_type": "messenger",
      "enabled": true,
      "token_env": "TELEGRAM_BOT_TOKEN",
      "chat_id": "chat-id"
    },
    "confluence": {
      "type": "confluence",
      "integration_type": "wiki",
      "enabled": true,
      "base_url": "https://confluence.example/confluence",
      "username": "service-user",
      "token_env": "CONFLUENCE_TOKEN"
    },
    "local": {
      "type": "local-tracker",
      "integration_type": "issue",
      "enabled": true,
      "database": {
        "driver": "sqlite",
        "path": ".progress/local-tracker/tasks.sqlite"
      }
    },
    "work-tracker": {
      "type": "script",
      "integration_type": "issue",
      "enabled": true,
      "timeout": "30s",
      "project": "ABC",
      "settings": {
        "tracker_url": "https://tracker.example"
      },
      "operations": {
        "tracker.task.get": {
          "script": ".progress/integration/work-tracker/task-get.sh",
          "required": ["number"],
          "optional": ["project", "tracker_url"],
          "defaults": {
            "project": "${system.project}",
            "tracker_url": "${system.settings.tracker_url}"
          }
        },
        "tracker.task.comment.create": {
          "script": ".progress/integration/work-tracker/task-comment-create.sh",
          "required": ["number", "body"]
        }
      }
    }
  }
}
```

Назначение общих полей системы:

1. `type` фиксирует тип встроенного адаптера: `github`, `bitbucket`, `mattermost`, `telegram`, `confluence`, `local-tracker` или `script`;
2. `integration_type` задаёт один тип интеграции, а `integration_types` — несколько типов для одной системы;
3. `enabled` включает или выключает систему без удаления её описания;
4. `default=true` делает систему системой по умолчанию для её типов, если `default_systems` не задаёт явное значение;
5. `command` и `path` позволяют переопределить исполняемый файл `gh`;
6. `timeout` ограничивает внешний вызов;
7. `transport` для GitHub выбирает способ обращения: `cli` через `gh` или `api` через GitHub REST API и GraphQL API; если поле не задано, используется `cli`;
8. `base_url` задаёт базовый адрес HTTP API для GitHub в режиме `api`, Bitbucket, Mattermost, Telegram или Confluence;
9. `api_variant` задаёт вариант Bitbucket API: `cloud` для `api.bitbucket.org/2.0` или `server` для Bitbucket Server/Data Center через `rest/api/1.0`;
10. `token`, `token_private` или `token_env` задают данные авторизации, ссылку на приватное значение или ссылку на переменную окружения;
11. `repository` задаёт резервный репозиторий для репозиторных операций;
12. `workspace` задаёт рабочее пространство Bitbucket Cloud, если `--repo` передан без префикса;
13. `project` задаёт ключ проекта Bitbucket Server/Data Center, если `--repo` передан без префикса;
14. `channel_id` задаёт резервный канал Mattermost;
15. `chat_id` задаёт резервный чат Telegram;
16. `database` задаёт хранилище локального трекера; в текущем срезе поддержан `driver=sqlite`;
17. `settings` задаёт несекретные настройки сценарных систем и других расширяемых адаптеров;
18. `task_label_mapping` задаёт сопоставление меток задачи: внешняя метка в ключе, каноническое название в значении, пустое значение для игнорирования внешней метки;
19. `operations` задаёт пооперационную настройку сценариев и их входных контрактов.

Пример GitHub-системы в режиме `api`:

```json
{
  "systems": {
    "github": {
      "type": "github",
    "integration_types": ["issue", "repo"],
      "enabled": true,
      "transport": "api",
      "base_url": "https://api.github.com",
      "token_env": "GITHUB_TOKEN",
      "repository": "owner/name",
      "timeout": "30s"
    }
  }
}
```

Если `base_url` не задан для GitHub в режиме `api`, используется `https://api.github.com`. Для локально размещённого GitHub Enterprise Server указывается базовый адрес REST API, например `https://github.example/api/v3`; GraphQL-вызовы будут направлены в соответствующий путь `/api/graphql`.

### 11.1 Локальный трекер задач

Система `type=local-tracker` подключает локальное хранилище задач как обычную интегрируемую систему типа `tracker`. Если `database` не задан, используется SQLite-файл `.progress/local-tracker/tasks.sqlite` относительно корня репозитория.

Минимальная настройка:

```json
{
  "default_systems": {
    "issue": "local"
  },
  "systems": {
    "local": {
      "type": "local-tracker",
      "integration_type": "issue",
      "enabled": true
    }
  }
}
```

Локальный трекер поддерживает операции:

- `issue.issue.create`;
- `issue.issue.get`;
- `issue.issue.search`;
- `issue.issue.update`;
- `issue.issue.comment.list`;
- `issue.issue.comment.create`;
- `issue.issue.label.add`;
- `issue.issue.label.remove`.

Хранилище создаёт схему при первом обращении. Задачи и комментарии возвращаются как `CanonicalTask`, `TaskComment`, `OperationResult` и совместимые структуры `TrackerIssue`/`TrackerComment`.

### 11.2 Сценарная система `script`

Система `type=script` позволяет подключить внешний трекер или служебную систему через локальный исполняемый файл. Сценарий выбирается по каноническому имени операции из `operations`.

Перед запуском адаптер:

1. выбирает операцию по `IntegrationType`, `ObjectType` и `Operation`;
2. применяет значения по умолчанию из `defaults`;
3. проверяет обязательные поля `required`;
4. записывает входной JSON во временный файл;
5. запускает сценарий из корня репозитория или из директории `path`, если она задана;
6. читает JSON-ответ из stdout и нормализует его в `Response`.

Сценарий получает переменные окружения:

```text
PROGRESS_INTEGRATION_SYSTEM
PROGRESS_INTEGRATION_TYPE
PROGRESS_INTEGRATION_OPERATION
PROGRESS_INTEGRATION_REQUEST_FILE
PROGRESS_INTEGRATION_TIMEOUT
```

Файл `PROGRESS_INTEGRATION_REQUEST_FILE` содержит `system`, `integration_type`, `operation_name`, `object_type`, `operation`, `request` и `settings`. Для `tracker.task.search` поле `request` может содержать `query`, `state`, `labels`, `exclude_labels` и `limit`. Поле `settings` содержит только явно настроенные несекретные значения. `token` и значение из `token_env` в JSON-файл не записываются.

Успешный ответ сценария:

```json
{
  "status": "ok",
  "task": {
    "system": "work-tracker",
    "number": 123,
    "title": "Задача",
    "body": "Описание",
    "state": "open",
    "traits": ["backend"]
  }
}
```

Ответ операции изменения:

```json
{
  "status": "ok",
  "operation_result": {
    "status": "ok",
    "message": "Комментарий создан",
    "url": "https://tracker.example/task/ABC-123#comment-1"
  }
}
```

Отказ сценария:

```json
{
  "status": "failed",
  "failure": {
    "kind": "not-found",
    "message": "Задача не найдена"
  }
}
```

Ошибки запуска, ненулевой код выхода, таймаут, невалидный JSON и неподдержанная операция переводятся в канонические отказные состояния контура интеграции.

Настройка `private_store` выбирает реализацию хранилища приватных значений. Если `type` не задан, на macOS в сборке с `cgo` используется `keychain` с сервисом `progress`. В остальных средах используется файловая реализация `file` в `$PROGRESS_CONFIG_HOME/integration/private-values.json` или `~/.config/progress/integration/private-values.json` с правами доступа `0600`. Явный `keychain` отклоняется при запуске сборки, где macOS Keychain недоступен.

Поддержанные поля `private_store`:

1. `type` — `keychain` или `file`;
2. `service` — имя сервиса macOS Keychain для `keychain`;
3. `path` — путь файла для реализации `file`; относительный путь считается от каталога конфигурации комплекса.

Правило приоритета авторизации:

1. если задан `token`, используется прямое значение из конфигурации;
2. если `token` не задан и указан `token_private`, значение читается из хранилища приватных значений по имени;
3. если `token` и `token_private` не заданы, сохраняется совместимость с `token_env`.

При слиянии слоёв `token`, `token_private` и `token_env` считаются взаимоисключающими источниками токена. Если более приоритетный слой задаёт один из этих ключей, ранее унаследованные альтернативы очищаются.

Пример записи приватного значения для Mattermost:

```bash
private_store.type=keychain
```

После записи локальная конфигурация может ссылаться на это значение:

```json
{
  "systems": {
    "mattermost": {
      "type": "mattermost",
      "integration_type": "messenger",
      "base_url": "https://mattermost.example",
      "token_private": "mt_auth_token",
      "channel_id": "channel-id"
    }
  }
}
```

При создании HTTP-запроса адаптер получает уже разрешённое значение токена в памяти процесса. Значение не передаётся через переменные окружения и не выводится командой записи.

Правило приоритета `--repo`, `repository` и `default_repo`:

1. если `--repo` не передан, сервис использует `repository`, а при его отсутствии может использовать `default_repo`;
2. если `--repo` передан с непустым значением, используется именно это значение;
3. если `--repo` передан явно пустым (`--repo=`), запрос отклоняется как `invalid-request`, резервный выбор через `repository` и `default_repo` не применяется.

Для переходного периода сохраняется совместимость с локальным файлом `.progress/integration/github.json`, если новый `systems.json` отсутствует. Новый конфигурационный слой считается приоритетным и должен использоваться как основной.

## 12. Текущее состояние реализации

Реализованный рабочий срез включает:

1. реестр типов, систем и операций, который выбирает интегрируемую систему по `IntegrationType`, `System` и `default_systems`;
2. единый вызов `Execute`, который возвращает `Response` с каноническим объектом, маршрутом и отказным состоянием;
3. GitHub-адаптер через `gh` или прямой GitHub API для задач, комментариев задач, репозиториев и запросов на слияние;
4. Bitbucket-адаптер через HTTP API для репозиториев и запросов на слияние;
5. Mattermost-адаптер через HTTP API для цепочек обсуждения и сообщений;
6. Telegram-адаптер через Bot API для отправки сообщений;
7. локальный трекер задач с SQLite-хранилищем по умолчанию;
8. сценарный адаптер `script` для операций трекера, настроенных через `systems.<name>.operations`;
9. нормализованные отказные состояния для отсутствия авторизации, недоступности, неподдерживаемой операции, неполного ответа, таймаута и ошибок внешнего источника.

Принцип проектирования остаётся прежним: другие контуры не должны знать синтаксис `gh`, HTTP-маршруты GitHub, Bitbucket, Mattermost или Telegram, формат токенов и поля внешних ответов. Эти сведения остаются внутри адаптеров, а наружу выходит канонический ответ контура интеграции.
