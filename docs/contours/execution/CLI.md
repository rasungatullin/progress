# CLI контура исполнения

## 1. Назначение документа

Настоящий документ определяет начальную схему CLI-вызовов для контура исполнения. Команды предназначены как для полного запуска контура, так и для изолированного вызова его внутренних модулей.

CLI рассматривается как основной ручной интерфейс первичной реализации на Go с использованием `cobra`.

Он должен поддерживать как полный запуск контура, так и изолированное воспроизведение его внутренних стадий, имитируя вызов из внешнего источника или из контура принятия решения.

## 2. Состав команд

В минимальной конфигурации предусматриваются следующие команды:

- `progress execution start`;
- `progress execution review-cycle`;
- `progress execution dispatcher`;
- `progress execution profile`;
- `progress execution resources`;
- `progress execution workplace`;
- `progress execution launch`.

Сквозные флаги для ветки `execution`:

- `--dry-run` — разрешает пройти подготовительные стадии и построить итоговую директиву, но запрещает внешние действия записи и необратимые завершающие операции;
- `--explain` — печатает основания выбора профиля, модели, движка, рабочего места и git-стадии.

## 3. Назначение команд

### 3.1 `progress execution start`

Команда выполняет полный запуск контура исполнения. Внутренне она инициирует последовательный проход по исполнительным стадиям и возвращает итоговый результат полного каскада.

Команда должна уметь принять структурированную задачу, подобрать профиль, определить модель и движок, подготовить рабочее место, выполнить задачу и вернуть структурированный результат. Конкретный прикладной сценарий, например кодирование, ревью, доработка замечаний или синтез артефакта, должен определяться входом и профилем, а не отдельным жёстко зашитым режимом команды.

В текущей реализации команда дополнительно поддерживает `--repo`. Если флаг не передан, подготовка рабочего места идёт относительно текущего git-репозитория процесса. Если `--repo` передан, контур нормализует GitHub shorthand `owner/name` или clone URL, materialize-ит локальный cache в `.progress/repositories/<repo-key>` текущего проекта, затем создаёт worktree в `.progress/workplaces/<repo-key>/<name>` и продолжает обычный flow `origin/HEAD -> git fetch -> git worktree add` уже для целевого репозитория.

Постановка задачи и контекст полного маршрута задаются через structured input, а не через сырой prompt. Поддерживаются `--input-file`, `--task`, повторяемый `--constraint` и повторяемые JSON-object флаги для объектных секций: `--project-context`, `--operational-context`, `--previous-run-result`, `--review-remark`, `--review-response`, `--integration-action`. Если передан `--input-file`, значения из файла загружаются первыми; затем scalar-флаги перезаписывают соответствующие поля, а repeated-флаги добавляют элементы к массивам.

### 3.2 `progress execution review-cycle`

Команда выполняет цикл над обычным полным запуском контура исполнения. Она находится над `progress execution start`: каждый шаг исполнения и каждый шаг ревью внутри цикла вызывают тот же маршрут полного запуска, но решение о повторении, остановке и передаче замечаний принимает внешний координатор `review-cycle`.

Вход команды повторяет прикладной вход `start`: `--dir`, `--name`, `--repo`, `--runner`, `--model`, `--model-binding`, structured-input флаги, `--structured-output` и `--structured-output-required`. Вместо одного `--profile` команда принимает два явных профиля:

- `--execution-profile` — профиль исполнения;
- `--review-profile` — профиль ревью.

`--max-executions` ограничивает количество запусков исполнения. Значение по умолчанию равно `5`.

`review-cycle` не вводит отдельный путь подготовки среды. Сначала запускается исполнение, затем ревью. Если structured output ревью содержит `conclusion.status=ok`, `approve` или `approved`, цикл завершается успешно. Любой другой статус считается требованием доработки, и следующий запуск исполнения получает результаты предыдущего исполнения и замечания ревью через `StructuredInput.review_remarks` и `StructuredInput.previous_run_results`. Если лимит исполнений исчерпан без approving conclusion, итоговый статус команды становится `limit-reached`.

Пример:

```bash
progress execution review-cycle \
  --execution-profile coder \
  --review-profile review \
  --max-executions 5 \
  --name review-cycle-task \
  --task "Исполнить задачу и провести саморевью результата"
```

### 3.3 `progress execution dispatcher`

Команда вызывает диспетчер исполнения как отдельный модуль. Данный режим предназначен для диагностики маршрута исполнения, наблюдения за порядком стадий и последующей отладки правил диспетчеризации.

### 3.4 `progress execution profile`

Команда отдельно вызывает модуль выбора исполнительного профиля. Результатом является решение о том, какой профиль должен применяться к данному заданию и какие ограничения он накладывает.

Профили исполнения загружаются из репозиторного файла `.progress/execution/profiles.json`. Файл является частью проекта и хранит как общие `defaults`, так и набор именованных профилей.

Минимальная схема файла `.progress/execution/profiles.json`:

```json
{
  "defaults": {
    "mode": "manual",
    "model-binding": "default",
    "allow-model-fallback": false,
    "prompt-additions": [],
    "structured-output": false,
    "structured-output-required": false,
    "commit-push": false
  },
  "profiles": {
    "default": {
      "description": "Базовый профиль исполнения через облачную модель по умолчанию"
    },
    "coder": {
      "description": "Профиль кодера с обязательным structured output для каскадной обработки",
      "model-binding": "coder",
      "structured-output": true,
      "structured-output-fields": ["commit_message", "changes", "remarks", "commands"]
    },
    "review": {
      "description": "Профиль ревью без автоматического commit и push",
      "model-binding": "review",
      "prompt-additions": [
        "Ты выполняешь code review. Не изменяй код и не коммить изменения.",
        "Сначала собери контекст PR, issue, diff и предыдущих review comments, если они указаны во входе.",
        "Приоритизируй bugs, behavioral regressions, missing tests, strict contract violations, security/privacy risks и risky side effects.",
        "Если blocking issues не найдены, явно верни conclusion status=ok/approve.",
        "Верни structured output с remarks, questions, follow_up_actions и conclusion."
      ],
      "structured-output": true,
      "structured-output-required": true,
      "structured-output-fields": ["summary", "remarks", "questions", "follow_up_actions", "conclusion"],
      "commit-push": false
    }
  }
}
```

Отдельный файл `.progress/execution/resources.json` хранит список доступных `runners`, `models`, `bindings` и опциональный `defaults.model-binding`. Поля `runners` и `models` задаются массивами строк, например `["opencode", "codex"]` и `["openai/gpt-5.4", "gpt-5.3-codex"]`. Семантические bindings вроде `default`, `coder` и `review` указывают на конкретную пару `runner/model`.

Правила разрешения профиля:

1. если профиль не указан, используется `default`;
2. профиль наследует незаданные поля из блока `defaults`;
3. `model-binding` наследуется из `defaults` и может быть переопределён в конкретном профиле;
4. `allow-model-fallback` наследуется из `defaults` и разрешает fallback на `resources.defaults.model-binding`, если профиль ссылается на неизвестный binding или не задаёт `model-binding`; если fallback запрещён, default binding использовать нельзя;
5. `structured-output` и `structured-output-required` наследуются из `defaults`, а затем объединяются с настройками конкретного запуска по OR-семантике;
6. `prompt-additions` наследуется из `defaults` и объединяется с additions конкретного профиля; additions из `defaults` идут первыми, profile additions идут после них;
7. `structured-output-fields` наследуется из `defaults` или целиком переопределяется профилем, поддерживает `summary` и остальные канонические top-level поля, но влияет только на те дополнительные секции, которые executor отдельно перечисляет в prompt;
8. `description` задаётся на уровне конкретного профиля и используется для CLI-диагностики;
9. если конфиг отсутствует, повреждён или не содержит нужного профиля, команда возвращает диагностируемую ошибку.

В resolved profile команда явно возвращает `description`, `mode`, `model-binding`, `allow-model-fallback`, `prompt-additions`, `structured-output`, `structured-output-required`, `structured-output-fields` и `commit-push`. Значение `commit-push` по умолчанию безопасное и равно `false`, но может использоваться последующей стадией `launch` как унаследованный признак автокоммита и автопуша.

### 3.5 `progress execution resources`

Команда отдельно вызывает подсистему ресурсного снабжения. Она предназначена для проверки доступности ресурсов и имитации резервирования без обязательного запуска всего исполнительного контура. Команда поддерживает `--profile`, `--runner`, `--model` и `--model-binding`, а в выводе печатает `runner`, `model`, `model-binding`, `source` и `fallback-used`.

### 3.6 `progress execution workplace`

Команда отдельно вызывает модуль подготовки исполнительного рабочего места. Она используется для проверки правил подготовки среды и воспроизводимости исполнительной конфигурации.

В текущей реализации команда принимает `--name` и опциональный `--repo`.

Если `--repo` не передан, команда сохраняет прежнее поведение и подготавливает локальное рабочее место в `.progress/workplaces/<name>` текущего git-репозитория. Если каталог ещё не существует, команда создаёт `git worktree` с локальной веткой `<name>` от актуальной default branch remote `origin`. Если каталог уже существует, команда не пересоздаёт его, а проверяет, что это валидный `git worktree` и что в нём активна ветка `<name>`.

Если `--repo` передан, поддерживаются форматы `https://github.com/owner/name`, `https://github.com/owner/name.git`, `git@github.com:owner/name.git` и shorthand `owner/name`. После нормализации clone URL materialize-ится cache-копия репозитория в `.progress/repositories/<repo-key>`, затем worktree создаётся в `.progress/workplaces/<repo-key>/<name>`. Диагностический вывод команды дополнительно печатает выбранный `repository=` и локальный `repository-root=`.

### 3.7 `progress execution launch`

Команда вызывает модуль пуска задачи. Она соответствует последней стадии работы диспетчера после завершения аллокаций и подготовки рабочего места.

Назначение команды состоит в изолированной проверке непосредственного запуска задачи без повторного прохождения стадий выбора профиля и резервирования ресурсов.

Подробное описание механизма структурированного ввода и вывода, потока данных и требований к расширяемости вынесено в `docs/contours/execution/STRUCTURED_IO.md`. В настоящем документе фиксируются только CLI-аспекты этого механизма.

В текущей реализации команда принимает `--dir`, `--runner`, `--model`, `--prompt`, а также опциональные флаги `--structured-output`, `--structured-output-required` и `--commit-push`.

Поддерживаются минимум два runner:

1. `opencode` c командой `opencode run --dir <dir> --model <model> <prompt>`;
2. `codex` c командой `codex exec -C <dir> -m <model> <prompt>`.

Для `progress execution launch` git-стадия включается только явным флагом `--commit-push`. Эта команда является прямым изолированным запуском и не подтягивает исполнительный профиль.

На полном маршруте `progress execution start` конкретные `runner/model` берутся не из профиля, а из resolved allocation. Если пользователь передал `--model-binding`, он имеет приоритет над профилем. Если пользователь передал явные `--runner` и `--model`, подсистема ресурсов использует их без binding, но только если оба значения зарегистрированы в `resources.json`. Если fallback действительно нужен, но `resources.defaults.model-binding` не задан, подсистема ресурсов возвращает диагностируемую ошибку вместо молчаливого выбора модели.

Если `--commit-push` не задан, команда выполняет только исполнительный модуль и возвращает его итоговое резюме.

В `--dry-run` команда обязана показать, какой запуск был бы выполнен, какой профиль, модель, движок, рабочий каталог и git-режим были бы выбраны, но не должна выполнять внешние модифицирующие действия.

Если ни `--structured-output`, ни `--structured-output-required`, ни resolved profile не включают structured output, executor не добавляет в prompt никакой новой служебной structured-инструкции и поведение остаётся максимально близким к обычному текстовому запуску.

Если resolved profile содержит `prompt-additions`, executor добавляет их в итоговый prompt после задачи из structured input и до любых structured-инструкций.

Если `--structured-output`, `--structured-output-required` или resolved profile эффективно включают structured output, executor сам добавляет в итоговый prompt самодостаточную каноническую инструкцию, требующую trailing block `<progress-structured-output>...</progress-structured-output>` и канонический JSON-объект structured output. Инструкция всегда требует непустой `summary`, а формы object-секций и canonical JSON example строятся subset-aware: только для выбранных optional полей из `structured-output-fields` (или для полного набора, если поле не задано). В текущей реализации итоговый порядок такой: structured input -> `prompt-additions` профиля -> structured output instruction -> JSON structured input context.

Профиль `review` служит preset для code review и сам добавляет минимальный boilerplate про сбор контекста PR, запрет на изменение кода, приоритизацию bugs/regressions/missing tests, проверку закрытия предыдущих замечаний и требование вернуть approving conclusion при отсутствии blocking issues. Поэтому короткого вызова вроде `progress execution start --profile review --task "Проведи ревью PR #38 в rasungatullin/progress"` достаточно: profile additions автоматически расширят задачу до полноценной review-директивы.

Поддерживается один канонический structured output:

1. runner должен вернуть JSON-объект с непустым `summary`;
2. при необходимости объект дополняется полем `commit_message` и секциями `remarks`, `questions`, `follow_up_actions`, `changes`, `commands`, `conclusion`, `extensions`;
3. если в профиле задан `structured-output-fields`, executor просит у runner только перечисленные дополнительные поля; `summary` остаётся обязательным всегда, а parser по-прежнему принимает любой валидный канонический payload с дополнительными секциями.

Флаг `--structured-output-required` и одноимённое поле профиля объединяются по OR-семантике и включают строгую валидацию результата: ошибкой запуска считается отсутствие trailing structured block, невалидный JSON block, пустой или бессмысленный payload, неизвестные top-level поля, пустые объектные элементы внутри секций structured output, а также отсутствие обязательного непустого `summary`. Для `json.UnmarshalTypeError` strict parser возвращает более полезную диагностику в формате `type mismatch at <field>: expected <type> but got <json-kind>`. При этом свободный текст ответа не теряется: если trailing block невалиден, он остаётся в `summary`, а strict-режим возвращает диагностичную schema-level ошибку.

Пример mismatch-диагностики для contract shape:

- wrong short form: `{"summary":"Done.","remarks":"fixed"}`
- strict error: `type mismatch at remarks: expected array of objects but got string`

Структурированный ввод нужен для передачи в контур исполнения полноценного контекста задачи. В этом блоке могут находиться:

- постановка и ограничения задачи;
- артефакты предыдущего шага;
- замечания ревью и ответы на них;
- указания о требуемых интеграционных действиях;
- дополнительные поля, введённые расширением конфигурации.

Контур исполнения не обязан передавать этот блок модели в исходном виде. Его задача состоит в том, чтобы на основании структурированного ввода и активного профиля собрать итоговую директиву, пригодную для конкретного исполнительного модуля и модели.

Исполнительный модуль может дополнительно вернуть необязательный структурированный блок в формате JSON внутри секции `<progress-structured-output>...</progress-structured-output>`.

Канонический structured input является единственным внутренним представлением входного контекста. Поддерживается следующая схема верхнего уровня: `task`, `constraints[]`, `project_context[]`, `operational_context[]`, `previous_run_results[]`, `review_remarks[]`, `review_responses[]`, `integration_actions[]`, `extensions`.

Канонический structured output является единственным внутренним представлением структурированного результата. Поддерживается следующая схема верхнего уровня: `summary`, `commit_message`, `remarks[]`, `questions[]`, `follow_up_actions[]`, `changes[]`, `commands[]`, `conclusion`, `extensions`. Поле `commit_message` остаётся необязательным и используется только как кандидат для git commit при включённом `commit-push`. Для `remarks[]` доступны поля `id`, `status`, `severity`, `type`, `title`, `body`, `answer`, `resolution`. Для дальнейшего расширения верхнеуровневый контейнер `extensions` сохраняется отдельной ветвью, а повторяемые секции вынесены в самостоятельные типы.

Wrong vs right JSON example:

- wrong short form: `{"summary":"Done.","remarks":["Fix docs"],"conclusion":"ready"}`
- canonical form: `{"summary":"Done.","remarks":[{"title":"Fix docs"}],"conclusion":{"status":"ok","summary":"Ready for review"}}`

Основное назначение структурированного вывода состоит в том, чтобы результат можно было использовать в следующих каскадах без повторного разбора свободного текста. Например:

- замечания ревью могут быть автоматически прикреплены к коду через интеграции;
- исполнительный каскад кодирования может получить эти замечания как структурированный ввод следующего запуска;
- исполнительный каскад кодирования может вернуть ответ на комментарий в структурированном выводе;
- структурированный вывод может содержать команды и заключения, например рекомендацию открыть запрос на слияние, признак готовности результата или требование доработки;

Исполнительный контур разбирает структурированный вывод только если в конце вывода исполнительного модуля присутствует один явный завершающий блок, допускающий только завершающие пробелы и переводы строки после `</progress-structured-output>`. Теги, встретившиеся внутри обычного текста, примера, промежуточного пояснения или literal JSON string values внутри payload, не должны ломать выделение реального завершающего блока и остаются частью исходного содержимого. Если завершающий блок присутствует, но не парсится, исполнительный контур сохраняет исходный вывод исполнительного модуля в `summary` без заполнения дополнительных полей, чтобы не терять диагностический контекст.

Structured input для полного маршрута задаётся явно через `--input-file` и structured-input флаги. Передача входного контракта через trailing `<progress-structured-input>...</progress-structured-input>` внутри `--prompt` больше не поддерживается. Низкоуровневый `progress execution launch` сохраняет `--prompt` как ручной запуск runner, но полный маршрут `start` и `review-cycle` строят prompt внутри контура исполнения из structured input.

Целевой механизм должен быть расширяемым через конфигурацию. Это означает, что схема структурированного ввода и вывода, правила включения отдельных секций в prompt и набор допустимых команд или заключений не должны быть жёстко зашиты только в одном исполнительном пути. В текущей реализации исполнительный профиль уже управляет включением `structured output`, strict-режимом и списком optional structured output fields для prompt-инструкции, а структурированный ввод определяется `LaunchSpec.StructuredInput`, собранным из CLI-флагов или `--input-file`.

В CLI итог печатается в виде отдельного блока `summary<<PROGRESS_SUMMARY ... PROGRESS_SUMMARY`. Если сохранены локальные runtime-артефакты, CLI дополнительно печатает `raw-output-path=...` для полного stdout/stderr runner-а и `run-record-path=...` для JSON record запуска в `.progress/execution-runs/`. Run record содержит normalized `Invocation`, resolved profile, allocation, workplace, canonical structured input, raw structured output payload, parsed structured output и итоговый статус запуска. Эти файлы считаются локальной диагностикой и не попадают в auto `commit-push`.

После этого при наличии структурированных данных выводится секция `structured-output:`. Для канонического structured output CLI дополнительно печатает строки `summary-field=...`, `commit-message=...` и JSON-строки `remark=...`, `question=...`, `follow-up-action=...`, `change=...`, `command=...`, `conclusion=...`, `extension=...`. Эти JSON-строки выводятся без дополнительной whitespace-нормализации, чтобы payload оставался lossless.

При включённом `--explain` CLI дополнительно должен печатать:

- выбранный профиль и источник его разрешения;
- выбранную модель и движок;
- способ выбора рабочего места;
- включение или подавление `commit-push`;
- причины перехода к каждой стадии диспетчера.

Если режим `commit-push` эффективно включён, то после успешного завершения исполнительного модуля команда:

1. проверяет, что рабочий каталог является git-репозиторием;
2. определяет текущую активную ветку;
3. проверяет наличие staged, unstaged и untracked изменений;
4. при наличии изменений выполняет `git add -A`, `git commit -m <message>` и `git push`;
5. если upstream у текущей ветки ещё не настроен, выполняет `git push -u origin <branch>`;
6. если изменений нет, корректно пропускает commit и push.

Сообщение коммита выбирается детерминированно и нормализуется через `strings.TrimSpace` в таком порядке:

1. `structured_output.commit_message`;
2. `Invocation.Workplace.Name` как каноническое имя запуска или рабочего места;
3. базовое имя подготовленного `workplace` каталога;
4. нейтральный fallback `Apply task result`, только если все предыдущие источники пустые или состоят из whitespace.

Пустой или состоящий только из пробелов `commit_message` не считается валидным и не блокирует переход к следующему источнику. Если `commit-push` не включён, structured output с `commit_message` по-прежнему парсится и печатается в CLI, но git-стадия не запускается.

## 4. Схема дерева команд

```mermaid
flowchart TD
    A[progress] --> B[execution]
    B --> C[start]
    B --> D[review-cycle]
    B --> E[dispatcher]
    B --> F[profile]
    B --> G[resources]
    B --> H[workplace]
    B --> I[launch]
```

## 5. Порядок полного вызова

Полный вызов `progress execution start` должен соответствовать следующему внутреннему маршруту:

1. выбор профиля;
2. проверка и резервирование ресурсов;
3. подготовка рабочего места;
4. пуск задачи;
5. фиксация результата.

```mermaid
flowchart LR
    A[progress execution start] --> B[profile]
    B --> C[resources]
    C --> D[workplace]
    D --> E[launch]
    E --> F[result]
```

Для `progress execution review-cycle` внешний маршрут становится циклическим, но каждый отдельный запуск исполнения и ревью по-прежнему проходит тот же внутренний маршрут полного запуска.

```mermaid
flowchart TD
    A[progress execution review-cycle] --> B[execution start route]
    B --> C[review start route]
    C --> D{review conclusion}
    D -- ok/approve --> E[result completed]
    D -- needs changes --> F{execution limit reached}
    F -- no --> B
    F -- yes --> G[result limit-reached]
```

## 6. Порядки изолированного вызова

Изолированные команды предназначены не для полного решения задачи, а для локальной проверки отдельной стадии.

### 6.1 Вызов диспетчера

```mermaid
flowchart LR
    A[progress execution dispatcher] --> B[Диспетчер исполнения]
    B --> C[План стадий]
    B --> D[Состояние маршрута]
```

### 6.2 Вызов выбора профиля

```mermaid
flowchart LR
    A[progress execution profile] --> B[Выбор профиля]
    B --> C[Исполнительный профиль]
```

### 6.3 Вызов ресурсного снабжения

```mermaid
flowchart LR
    A[progress execution resources] --> B[Проверка ресурсов]
    B --> C[Результат резервирования]
```

### 6.4 Вызов подготовки рабочего места

```mermaid
flowchart LR
    A[progress execution workplace] --> B[Подготовка среды]
    B --> C[Готовое рабочее место]
```

Пример вызова:

```bash
progress execution workplace --name feature-brief
```

Ожидаемое поведение:

1. создаётся или переиспользуется каталог `.progress/workplaces/feature-brief`;
2. каталог должен быть зарегистрирован как `git worktree`;
3. активная ветка внутри каталога должна совпадать с именем `feature-brief`.

### 6.5 Вызов модуля пуска

```mermaid
flowchart LR
    A[progress execution launch] --> B[Проверка готовности]
    B --> C[Пуск задачи]
    C --> D{commit-push включён?}
    D -->|нет| E[Состояние запуска]
    D -->|да| F[Git commit и push при наличии изменений]
    F --> E[Состояние запуска]
```

Пример вызова только runner:

```bash
progress execution launch --dir .progress/workplaces/feature-brief --prompt "Подготовь изменения"
```

Пример вызова runner с последующим commit и push:

```bash
progress execution launch \
  --dir .progress/workplaces/feature-brief \
  --prompt "Подготовь изменения" \
  --commit-push
```

Ожидаемое поведение:

1. без `--commit-push` выполняется только runner;
2. с `--commit-push` или с `commit-push=true` у профиля git-операции запускаются только после успешного завершения runner;
3. при ошибке runner git-операции не выполняются;
4. при отсутствии изменений commit и push пропускаются без ошибки;
5. summary содержит компактный результат git-стадии, например `git=no-changes` или `git=committed+pushed`.

## 7. Принцип проектирования команд

Для начального этапа принимаются следующие правила:

1. все команды ветки `execution` относятся к одному контуру и используют общий формат входного задания;
2. команда `start` является пользовательской точкой полного запуска;
3. команды `profile`, `resources`, `workplace` и `launch` являются модульными и предназначены для локальной проверки стадий;
4. команда `dispatcher` отражает координирующую роль диспетчера и не подменяет собой отдельные стадии;
5. `launch` рассматривается как специализированная команда последнего этапа диспетчеризации.

## 8. Ближайшие задачи реализации CLI

На первом кодовом этапе требуется:

1. ввести корневую команду `progress`;
2. ввести ветку `execution`;
3. зарегистрировать перечисленные подкоманды;
4. обеспечить единый формат ручного входа для всех команд ветки;
5. привязать команды к заглушечным внутренним модулям;
6. вывести журналы прохождения стадий в стандартный поток вывода.
