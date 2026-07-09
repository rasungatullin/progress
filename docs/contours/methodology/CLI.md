# CLI контура методик

## Назначение

Команды `progress methodology` управляют каталогом методик: показывают объединённый каталог, выбирают маршрут, сохраняют каталог в слой и добавляют отдельные экземпляры сущностей.

Все команды используют единый сервис `internal/methodology.Service`.

## Слои каталога

Каталог читается из двух мест:

- глобальный слой: `<progress-config-home>/methodology/`;
- локальный слой: `.progress/methodology/`.

`<progress-config-home>` по умолчанию равен `${HOME}/.config/progress`. Значение можно переопределить переменной окружения `PROGRESS_CONFIG_HOME` или флагом `--config-home`.

Локальный слой определяется относительно корня репозитория. Для явного выбора корня используется `--repo-root`.

Локальный слой имеет приоритет над глобальным при совпадении имён маршрутов, действий и инструкций. Для расширяемых сущностей приоритет применяется по ключу `kind/name`.

## Формат каталога

Минимальный корневой файл `catalog.json`:

```json
{
  "default_route": "task-processing"
}
```

Объекты каталога хранятся в отдельных файлах:

```text
methodology/
  catalog.json
  routes/task-processing.json
  actions/start-implementation-pr.json
  instructions/start-implementation-pr-directive.json
  entities/decision-rule--description-assessment.json
```

Пример `routes/task-processing.json`:

```json
{
  "name": "task-processing",
  "title": "Реализация задачи и саморевизия результата",
  "checks": [
    {
      "name": "task-processing-start",
      "action": "start-implementation-pr",
      "missing_labels": ["Ожидает экспертизы"],
      "reason_code": "task_processing_not_started",
      "reason_message": "Требуется начать выполнение."
    }
  ]
}
```

Пример `actions/start-implementation-pr.json`:

```json
{
  "name": "start-implementation-pr",
  "class": "engineering-synthesis",
  "profile": "coder"
}
```

Пример `instructions/start-implementation-pr-directive.json`:

```json
{
  "name": "start-implementation-pr-directive",
  "profile": "coder",
  "action": "start-implementation-pr",
  "body": "Выполнить задачу и открыть запрос на слияние."
}
```

Пример `entities/decision-rule--description-assessment.json`:

```json
{
  "kind": "decision-rule",
  "name": "description-assessment",
  "target_contour": "decision",
  "payload": {
    "has_label": "description-assessment"
  }
}
```

Имя файла должно совпадать с ключом объекта. Для `routes`, `actions` и `instructions` ключом является `name`. Для `entities` ключ задаётся как `<kind>--<name>`.

Монолитный файл `catalog.json` со старыми массивами остаётся входным миграционным форматом. После сохранения слой записывается в файловые реестры.

## Просмотр

Список всех сущностей:

```bash
progress methodology list
```

Фильтр по типовой сущности:

```bash
progress methodology list --kind route
progress methodology list --kind action
progress methodology list --kind instruction
```

Фильтр по расширяемой сущности для контура принятия решения:

```bash
progress methodology list --kind decision-rule --target-contour decision
```

Просмотр одной сущности:

```bash
progress methodology show --kind route --name task-processing
progress methodology show --kind decision-rule --name description-assessment
```

Для машинной обработки используется `--json`.

## Выбор методики

Выбор маршрута, действия и инструкции из объединённого каталога:

```bash
progress methodology select --route task-processing
```

Переопределение действия или профиля:

```bash
progress methodology select --route task-processing --action start-implementation-pr --profile coder
```

Вывод содержит выбранные сущности, источники `global` или `local`, пути каталогов и диагностические строки выбора.

## Сохранение каталога

Сохранить полный файл каталога в локальный слой и разнести объекты по файловым реестрам:

```bash
progress methodology save --file ./catalog.json --scope local
```

Сохранить полный файл каталога в глобальный слой и разнести объекты по файловым реестрам:

```bash
progress methodology save --file ./catalog.json --scope global
```

Команда заменяет содержимое выбранного слоя после проверки структуры каталога. Старый монолитный файл можно передать этой же команде для миграции.

## Добавление экземпляров

Добавить или обновить действие:

```bash
progress methodology add action \
  --name start-implementation-pr \
  --class engineering-synthesis \
  --profile coder \
  --operation prepare-data \
  --operation launch-synthesis
```

Добавить или обновить маршрут:

```bash
progress methodology add route \
  --name manual-action \
  --title "Ручной запуск действия" \
  --action start-implementation-pr \
  --profile coder \
  --reason-code issue_context_ready \
  --reason-message "Контекст задачи готов к передаче в контур исполнения."
```

Маршрут может возвращать исход без запуска действия. Такой маршрут используется, когда контур принятия решения должен явно зафиксировать отсутствие следующей операции:

```bash
progress methodology add route \
  --name task-processing-completed \
  --title "Экспертиза пройдена" \
  --outcome completed \
  --has-label "Экспертиза пройдена" \
  --reason-code review_already_passed \
  --reason-message "У задачи зафиксирован признак пройденной экспертизы."
```

Добавить или обновить инструкцию:

```bash
progress methodology add instruction \
  --name start-implementation-pr-directive \
  --profile coder \
  --action start-implementation-pr \
  --body-file ./instruction.md
```

Добавить расширяемую сущность для контура принятия решения:

```bash
progress methodology add entity \
  --kind decision-rule \
  --name description-assessment \
  --target-contour decision \
  --payload '{"has_label":"description-assessment","missing_label":"description-assessed"}'
```

По умолчанию команды `add` записывают локальный слой. Для записи в глобальный слой используется `--scope global`. Команда обновляет JSON-файл соответствующего объекта; если выбранный слой ещё хранится монолитным `catalog.json`, первый запуск переносит все объекты слоя в новый файловый формат.
