# Контур настроек и ресурсов

## 1. Назначение

Контур настроек и ресурсов предназначен для описания доступных возможностей комплекса в конкретной среде: интегрируемых систем, окружений исполнения, инструментов исполнения, ресурсов исполнения, привязок ресурсов, локальных и глобальных переопределений.

К настройкам интеграции относится и выбор хранилища приватных значений, из которого адаптеры получают токены и ключи авторизации по ссылке из конфигурации.

Его цель состоит в том, чтобы отделить переносимую методику от фактической доступности ресурсов.

## 2. Краткое описание

Что получает на вход:

- путь к рабочему проекту;
- путь к глобальной конфигурации;
- запрос на чтение нужных разделов настроек.

Что отдаёт на выход:

- сводку настроек интеграции;
- сводку окружений исполнения, инструментов исполнения и ресурсов исполнения;
- диагностируемые отказные состояния по отсутствующим или некорректным слоям.

Как работает:

- читает глобальные и локальные слои конфигурации;
- объединяет слои по правилам контура;
- проверяет ссылки между ресурсами;
- возвращает нормализованный снимок настроек.

Чего не делает:

- не выбирает маршрут обработки;
- не запускает исполнительный модуль;
- не выполняет внешнюю операцию записи.

## 3. Основной состав

В состав контура входят:

- загрузчик настроек интеграции;
- загрузчик настройки хранилища приватных значений;
- загрузчик настроек исполнительных ресурсов;
- загрузчик окружений исполнения;
- загрузчик инструментов исполнения;
- загрузчик ресурсов исполнения;
- правила объединения глобального и локального слоя;
- проверка связности настроек;
- диагностическая сводка.

## 4. Практический каркас

Начальный каркас расположен в пакете `internal/configuration`.

Пакет уже содержит загрузчики `LoadIntegrationConfig` и `LoadExecutionResourceConfig`. В рамках контура добавлен сервис `Service.Snapshot`, который собирает нормализованный снимок доступных настроек и ресурсов.

Файл `resources.json` поддерживает два слоя:

- глобальный: `<progress-config-home>/execution/resources.json`, где `<progress-config-home>` по умолчанию равен `~/.config/progress` и переопределяется `PROGRESS_CONFIG_HOME`;
- локальный: `.progress/execution/resources.json` в репозитории.

Новый формат слоя описывает:

- `environments` — окружения исполнения. В текущей реализации поддержаны типы `local` и `worktree`;
- `tools` — инструменты исполнения. Базовый тип `agentic-system` является временным literal-значением для инструментальной подсистемы;
- `resources` — ресурсы исполнения. Сейчас ресурсом типа `model` является имя модели;
- `bindings` — привязки ресурсов, связывающие инструмент, ресурс и опциональное окружение;
- `git` — опциональные параметры git-операций контура исполнения: идентичность коммита, подпись коммита и SSH-ключ отправки ветки.

Пример:

```json
{
  "defaults": {
    "model-binding": "default",
    "environment": "worktree"
  },
  "environments": {
    "local": {"type": "local", "enabled": true},
    "worktree": {"type": "worktree", "enabled": true}
  },
  "tools": {
    "opencode": {"type": "agentic-system", "enabled": true},
    "codex": {"type": "agentic-system", "enabled": true}
  },
  "resources": {
    "qwen": {"type": "model", "enabled": true, "tools": ["opencode"]},
    "gpt-5.5": {"type": "model", "enabled": true, "tools": ["codex", "opencode"]}
  },
  "bindings": {
    "default": {"tool": "opencode", "resource": "qwen", "environment": "worktree"},
    "review": {"tool": "codex", "resource": "gpt-5.5"}
  },
  "git": {
    "identity": {
      "author-name": "Progress Execution",
      "author-email": "progress@example.com",
      "committer-name": "Progress Execution",
      "committer-email": "progress@example.com"
    },
    "signing": {
      "enabled": true,
      "format": "ssh",
      "signing-key": "/Users/example/.ssh/progress_signing_key.pub"
    },
    "push": {
      "ssh-identity-file": "/Users/example/.ssh/progress_push_key",
      "known-hosts-file": "/Users/example/.ssh/known_hosts",
      "identities-only": true
    }
  }
}
```

Для совместимости загрузчик принимает старые поля `runners`, `models` и `bindings` с полями `runner` и `model`. При нормализации они преобразуются в инструменты исполнения, ресурсы исполнения и привязки ресурсов.

Локальный слой переопределяет глобальный блок `git` целиком. Неполная идентичность коммита отклоняется: поля `author-name`, `author-email`, `committer-name` и `committer-email` должны задаваться вместе. CLI-снимок показывает только наличие идентичности, подписи и ключа отправки, не раскрывая приватный материал.

## 5. CLI

Команды просмотра и изменения настроек расположены в ветке `progress configuration resources`.

Основные вызовы:

```sh
progress configuration resources list
progress configuration resources tool set opencode --type agentic-system
progress configuration resources resource set qwen --tool opencode --disabled
progress configuration resources binding set default --tool opencode --resource qwen --environment worktree
progress configuration resources defaults set --model-binding default --environment worktree
```

По умолчанию команды записи изменяют локальный слой. Для записи в глобальный слой используется `--scope global`; для явного выбора путей доступны `--repo-root` и `--config-home`.

## 6. Дополнительная документация

- `docs/contours/settings-resources/TARGET_ARCHITECTURE.md` — целевое архитектурное состояние контура настроек и ресурсов.
