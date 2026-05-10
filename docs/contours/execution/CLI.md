# CLI контура исполнения

## 1. Назначение документа

Настоящий документ определяет начальную схему CLI-вызовов для контура исполнения. Команды предназначены как для полного запуска контура, так и для изолированного вызова его внутренних модулей.

CLI рассматривается как основной ручной интерфейс первичной реализации на Go с использованием `cobra`.

## 2. Состав команд

В минимальной конфигурации предусматриваются следующие команды:

- `progress execution start`;
- `progress execution dispatcher`;
- `progress execution profile`;
- `progress execution resources`;
- `progress execution workplace`;
- `progress execution launch`.

## 3. Назначение команд

### 3.1 `progress execution start`

Команда выполняет полный запуск контура исполнения. Внутренне она инициирует последовательный проход по исполнительным стадиям и возвращает итоговый результат полного каскада.

### 3.2 `progress execution dispatcher`

Команда вызывает диспетчер исполнения как отдельный модуль. Данный режим предназначен для диагностики маршрута исполнения, наблюдения за порядком стадий и последующей отладки правил диспетчеризации.

### 3.3 `progress execution profile`

Команда отдельно вызывает модуль выбора исполнительного профиля. Результатом является решение о том, какой профиль должен применяться к данному заданию и какие ограничения он накладывает.

Профили исполнения загружаются из репозиторного файла `.progress/execution/profiles.json`. Файл является частью проекта и хранит как общие `defaults`, так и набор именованных профилей.

Минимальная схема файла:

```json
{
  "defaults": {
    "mode": "manual",
    "model": "openai/gpt-5.4",
    "commit-push": false
  },
  "profiles": {
    "default": {
      "description": "Базовый профиль исполнения через облачную модель по умолчанию"
    },
    "local": {
      "description": "Локальный профиль исполнения через локальную модель",
      "model": "ollama/qwen3.5:2b"
    }
  }
}
```

Правила разрешения профиля:

1. если профиль не указан, используется `default`;
2. профиль наследует незаданные поля из блока `defaults`;
3. `model` может быть определена в `defaults` и переопределена в конкретном профиле;
4. `description` задаётся на уровне конкретного профиля и используется для CLI-диагностики;
5. если конфиг отсутствует, повреждён или не содержит нужного профиля, команда возвращает диагностируемую ошибку.

В resolved profile команда явно возвращает `description`, `mode`, `model` и `commit-push`. Значение `commit-push` по умолчанию безопасное и равно `false`, но может использоваться последующей стадией `launch` как унаследованный признак автокоммита и автопуша.

### 3.4 `progress execution resources`

Команда отдельно вызывает подсистему ресурсного снабжения. Она предназначена для проверки доступности ресурсов и имитации резервирования без обязательного запуска всего исполнительного контура.

### 3.5 `progress execution workplace`

Команда отдельно вызывает модуль подготовки исполнительного рабочего места. Она используется для проверки правил подготовки среды и воспроизводимости исполнительной конфигурации.

В текущей реализации команда принимает `--name` и подготавливает локальное рабочее место в `.progress/workplaces/<name>`. Если каталог ещё не существует, команда создаёт `git worktree` с локальной веткой `<name>` от актуальной default branch remote `origin`. Если каталог уже существует, команда не пересоздаёт его, а проверяет, что это валидный `git worktree` и что в нём активна ветка `<name>`.

### 3.6 `progress execution launch`

Команда вызывает модуль пуска задачи. Она соответствует последней стадии работы диспетчера после завершения аллокаций и подготовки рабочего места.

Назначение команды состоит в изолированной проверке непосредственного запуска задачи без повторного прохождения стадий выбора профиля и резервирования ресурсов.

В текущей реализации команда принимает `--dir`, `--runner`, `--model`, `--prompt`, а также опциональные флаги `--structured-output`, `--structured-protocol`, `--structured-mode`, `--structured-output-required`, `--commit-push` и `--commit-message`.

Эффективный `commit-push` для стадии запуска определяется по простому правилу: git-стадия включается, если `--commit-push` передан явно либо если включён одноимённый флаг у выбранного execution profile.

Если `--commit-push` не задан и профиль также не включает `commit-push`, команда выполняет только runner и возвращает его итоговый summary.

Если `--structured-output` не указан, executor не добавляет в prompt никакой новой служебной structured-инструкции и поведение остаётся максимально близким к текущему.

Если `--structured-output` указан, executor сам дописывает в конец prompt короткую детерминированную инструкцию, требующую trailing block `<progress-structured-output>...</progress-structured-output>`. Для этого режима используется прагматичный default `--structured-protocol=review-cycle`. Значение `--structured-mode` по умолчанию не подставляется: если флаг не указан, executor не заставляет runner явно выставлять `mode` в injected-инструкции.

Поддерживаются два минимальных протокола:

1. `legacy` — runner должен вернуть JSON-объект с массивами `critical_remarks`, `minor_remarks`, `questions`;
2. `review-cycle` — runner должен вернуть JSON-объект с `protocol_version="review-cycle/v1"`, `summary` и, при необходимости, массивами `remarks`, `questions`, `follow_up_actions`, `changes`; если явно задан `--structured-mode`, injected-инструкция дополнительно требует выставить соответствующий `mode`.

Флаг `--structured-output-required` включает строгую валидацию результата: отсутствие trailing structured block либо невалидный JSON block считается ошибкой запуска.

Runner может дополнительно вернуть необязательный structured block в формате JSON внутри секции `<progress-structured-output>...</progress-structured-output>`. Legacy-ключи `critical_remarks`, `minor_remarks` и `questions` по-прежнему принимаются, но сразу нормализуются в канонический review-cycle envelope.

Review cycle envelope является единственным каноническим представлением structured данных. Поддерживается следующая схема верхнего уровня: `protocol_version`, `mode`, `summary`, `remarks[]`, `questions[]`, `follow_up_actions[]`, `changes[]`. Для `remarks[]` доступны поля `id`, `status`, `response_status`, `severity`, `type`, `title`, `body`, `reply`, `fix_summary`. Если runner присылает mixed payload, legacy-ключи мержатся в тот же envelope, а плоские `critical_remarks`, `minor_remarks` и `questions` затем уже вычисляются из канонической модели как диагностическая проекция.

Executor разбирает structured output только если в конце runner output присутствует один явный trailing block, допускающий только завершающие пробелы и переводы строки после `</progress-structured-output>`. Теги, встретившиеся внутри обычного текста, примера, промежуточного пояснения или literal JSON string values внутри payload, не должны ломать выделение реального trailing block и остаются частью исходного содержимого. Если trailing block присутствует, но не парсится, executor сохраняет исходный runner output в `summary` без заполнения дополнительных полей, чтобы не терять диагностический контекст.

В prompt можно передавать structured input block `<progress-structured-input>...</progress-structured-input>`. Executor выделяет trailing input block из prompt, нормализует его в тот же канонический review-cycle envelope и передаёт эту структуру дальше в launch flow. Если structured input задан программно, runner получает prompt с тем же trailing block. Это позволяет использовать envelope из предыдущего review/reply/fix/re-review запуска как вход для следующего цикла без отдельного legacy path.

В CLI итог печатается в виде отдельного блока `summary<<PROGRESS_SUMMARY ... PROGRESS_SUMMARY`, после которого при наличии structured данных выводится секция `structured-output:`. Для review cycle envelope CLI дополнительно печатает строки `review-cycle-protocol-version=...`, `review-cycle-mode=...`, `review-cycle-summary=...` и JSON-строки `review-cycle-remark=...`, `review-cycle-question=...`, `review-cycle-follow-up-action=...`, `review-cycle-change=...`. Эти JSON-строки выводятся без дополнительной whitespace-нормализации, чтобы payload оставался lossless. Legacy-строки `critical-remark=...`, `minor-remark=...` и `question=...` сохраняются как производная диагностическая проекция и для них по-прежнему используется однострочная нормализация.

Если effective `commit-push` включён, то после успешного завершения runner команда:

1. проверяет, что рабочий каталог является git-репозиторием;
2. определяет текущую активную ветку;
3. проверяет наличие staged, unstaged и untracked изменений;
4. при наличии изменений выполняет `git add -A`, `git commit -m <message>` и `git push`;
5. если upstream у текущей ветки ещё не настроен, выполняет `git push -u origin <branch>`;
6. если изменений нет, корректно пропускает commit и push.

По умолчанию для `--commit-message` используется нейтральное значение `Apply task result`.

## 4. Схема дерева команд

```mermaid
flowchart TD
    A[progress] --> B[execution]
    B --> C[start]
    B --> D[dispatcher]
    B --> E[profile]
    B --> F[resources]
    B --> G[workplace]
    B --> H[launch]
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
  --commit-push \
  --commit-message "Apply task result"
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
