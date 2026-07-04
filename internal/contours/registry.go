package contours

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	Reactivity        = "reactivity"
	Integration       = "integration"
	Decision          = "decision"
	Execution         = "execution"
	Methodology       = "methodology"
	Configuration     = "configuration"
	Observability     = "observability"
	UserInterface     = "user-interface"
	Authorization     = "authorization"
	ExecutionQueue    = "execution-queue"
	Analytics         = "analytics"
	TaskBank          = "task-bank"
	Research          = "research"
	ContractKindRead  = "read"
	ContractKindWrite = "write"
	ContractKindEvent = "event"
)

type Contour struct {
	Name          string
	Title         string
	Kind          string
	Package       string
	Documentation string
}

type Contract struct {
	ID         string
	Source     string
	Target     string
	Kind       string
	Object     string
	Summary    string
	Invariants []string
}

type Registry struct {
	Contours  []Contour
	Contracts []Contract
}

func DefaultRegistry() Registry {
	return Registry{
		Contours: []Contour{
			{Name: Reactivity, Title: "контур реакции на внешние события", Kind: "основной", Package: "internal/reactivity", Documentation: "docs/contours/reactivity/README.md"},
			{Name: Integration, Title: "контур интеграции", Kind: "основной", Package: "internal/integration", Documentation: "docs/contours/integration/README.md"},
			{Name: Decision, Title: "контур принятия решения", Kind: "основной", Package: "internal/decision", Documentation: "docs/contours/decision/README.md"},
			{Name: Execution, Title: "контур исполнения", Kind: "основной", Package: "internal/execution", Documentation: "docs/contours/execution/README.md"},
			{Name: Methodology, Title: "контур методик", Kind: "обеспечивающий", Package: "internal/methodology", Documentation: "docs/contours/methodology/README.md"},
			{Name: Configuration, Title: "контур настроек и ресурсов", Kind: "обеспечивающий", Package: "internal/configuration", Documentation: "docs/contours/settings-resources/README.md"},
			{Name: Observability, Title: "контур журналирования и наблюдаемости", Kind: "обеспечивающий", Package: "internal/observability", Documentation: "docs/contours/observability/README.md"},
			{Name: UserInterface, Title: "контур пользовательского интерфейса", Kind: "обеспечивающий", Package: "internal/ui", Documentation: "docs/contours/user-interface/README.md"},
			{Name: Authorization, Title: "контур авторизации", Kind: "обеспечивающий", Package: "internal/authorization", Documentation: "docs/contours/authorization/README.md"},
			{Name: ExecutionQueue, Title: "очередь исполнения задач", Kind: "обеспечивающий", Package: "internal/executionqueue", Documentation: "docs/contours/execution-queue/README.md"},
			{Name: Analytics, Title: "контур аналитики и статистики", Kind: "исследовательский", Package: "internal/analytics", Documentation: "docs/contours/analytics/README.md"},
			{Name: TaskBank, Title: "банк задач", Kind: "исследовательский", Package: "internal/taskbank", Documentation: "docs/contours/task-bank/README.md"},
			{Name: Research, Title: "контур исследований", Kind: "исследовательский", Package: "internal/research", Documentation: "docs/contours/research/README.md"},
		},
		Contracts: []Contract{
			{
				ID:      "reactivity-to-integration-restore-request",
				Source:  Reactivity,
				Target:  Integration,
				Kind:    ContractKindRead,
				Object:  "параметры восстановления данных",
				Summary: "Контур реакции передаёт параметры, по которым контур интеграции восстанавливает канонические данные внешнего объекта.",
				Invariants: []string{
					"контур реакции не передаёт полное состояние задачи",
					"контур интеграции не выбирает следующий инженерный шаг",
				},
			},
			{
				ID:      "integration-to-decision-canonical-context",
				Source:  Integration,
				Target:  Decision,
				Kind:    ContractKindRead,
				Object:  "каноническая задача и связанные объекты",
				Summary: "Контур интеграции передаёт контуру принятия решения восстановленные канонические данные без внешних форматов.",
				Invariants: []string{
					"контур принятия решения не зависит от устройства внешней системы",
					"неполные данные должны быть выражены через отказное состояние или признак неполноты",
				},
			},
			{
				ID:      "decision-to-execution-assignment",
				Source:  Decision,
				Target:  Execution,
				Kind:    ContractKindWrite,
				Object:  "задание на выполнение",
				Summary: "Контур принятия решения передаёт контуру исполнения согласованный шаг с действием, канонической задачей и основаниями выбора.",
				Invariants: []string{
					"контур исполнения не пересматривает маршрут обработки",
					"задание должно содержать действие или диагностируемый отказ формирования",
				},
			},
			{
				ID:      "decision-to-execution-queue-item",
				Source:  Decision,
				Target:  ExecutionQueue,
				Kind:    ContractKindWrite,
				Object:  "элемент очереди",
				Summary: "Контур принятия решения может поставить согласованное задание в очередь, если запуск нужно отложить или приоритизировать.",
				Invariants: []string{
					"очередь не принимает решение о необходимости выполнения",
					"элемент очереди должен ссылаться на уже сформированное задание",
				},
			},
			{
				ID:      "execution-queue-to-execution-assignment",
				Source:  ExecutionQueue,
				Target:  Execution,
				Kind:    ContractKindWrite,
				Object:  "допуск к запуску задания",
				Summary: "Очередь исполнения задач выдаёт следующий готовый элемент контуру исполнения без изменения самого задания.",
				Invariants: []string{
					"очередь управляет приоритетом и попытками, но не меняет действие",
					"при исчерпании попыток элемент переводится в ручное вмешательство",
				},
			},
			{
				ID:      "execution-to-decision-result",
				Source:  Execution,
				Target:  Decision,
				Kind:    ContractKindEvent,
				Object:  "результат выполнения",
				Summary: "Контур исполнения возвращает нормализованный итог действия и диагностические сведения для следующего рассмотрения.",
				Invariants: []string{
					"следующий шаг выбирается только новым рассмотрением",
					"результат выполнения не является текущим состоянием внешней задачи",
				},
			},
			{
				ID:      "execution-to-integration-operation",
				Source:  Execution,
				Target:  Integration,
				Kind:    ContractKindWrite,
				Object:  "интеграционная операция",
				Summary: "Контур исполнения передаёт операции чтения или записи во внешние источники через контур интеграции.",
				Invariants: []string{
					"контур исполнения не знает устройство конкретной внешней системы",
					"контур интеграции возвращает нормализованный результат операции",
				},
			},
			{
				ID:      "methodology-to-decision-route",
				Source:  Methodology,
				Target:  Decision,
				Kind:    ContractKindRead,
				Object:  "маршрут обработки, действие и расширяемые сущности методики",
				Summary: "Контур методик предоставляет контуру принятия решения переносимые маршруты, проверки, действия и дополнительные методические элементы.",
				Invariants: []string{
					"проектные настройки могут ограничить применимость методики",
					"выбор методики не выполняет внешних изменений",
				},
			},
			{
				ID:      "methodology-to-execution-action",
				Source:  Methodology,
				Target:  Execution,
				Kind:    ContractKindRead,
				Object:  "шаблон действия и инструкция",
				Summary: "Контур методик предоставляет контуру исполнения шаблоны действий и инструкции для языковой модели.",
				Invariants: []string{
					"инструкция не заменяет задание на выполнение",
					"применение методики учитывает настройки и доступные ресурсы",
				},
			},
			{
				ID:      "configuration-to-integration-settings",
				Source:  Configuration,
				Target:  Integration,
				Kind:    ContractKindRead,
				Object:  "настройки интеграции",
				Summary: "Контур настроек и ресурсов задаёт доступные интегрируемые системы, адаптеры и ограничения интеграции.",
				Invariants: []string{
					"контур интеграции не становится хранилищем проектных настроек",
					"отсутствие настройки должно быть диагностируемым отказным состоянием",
				},
			},
			{
				ID:      "configuration-to-decision-rules",
				Source:  Configuration,
				Target:  Decision,
				Kind:    ContractKindRead,
				Object:  "проектные ограничения маршрутов",
				Summary: "Контур настроек и ресурсов задаёт включённые маршруты, разрешённые действия и локальные переопределения для рассмотрения.",
				Invariants: []string{
					"настройки ограничивают методику, но не выполняют проверку маршрута вместо контура принятия решения",
				},
			},
			{
				ID:      "configuration-to-execution-resources",
				Source:  Configuration,
				Target:  Execution,
				Kind:    ContractKindRead,
				Object:  "исполнительные ресурсы",
				Summary: "Контур настроек и ресурсов задаёт исполнительные модули, модели, привязки ресурсов и локальные переопределения.",
				Invariants: []string{
					"ресурсная конфигурация не выбирает инженерный шаг",
					"неизвестная привязка ресурсов должна быть отказным состоянием",
				},
			},
			{
				ID:      "authorization-to-user-interface-decision",
				Source:  Authorization,
				Target:  UserInterface,
				Kind:    ContractKindRead,
				Object:  "решение авторизации",
				Summary: "Контур авторизации сообщает пользовательскому интерфейсу, какие сведения и действия доступны субъекту доступа.",
				Invariants: []string{
					"интерфейс не должен обходить решение авторизации для операций изменения",
				},
			},
			{
				ID:      "user-interface-to-contours-operation",
				Source:  UserInterface,
				Target:  Authorization,
				Kind:    ContractKindWrite,
				Object:  "операция доступа",
				Summary: "Контур пользовательского интерфейса передаёт запрошенную эксплуатационную операцию на проверку допуска.",
				Invariants: []string{
					"интерфейс не содержит собственную логику принятия решения по задаче",
					"операция должна указывать контур и действие",
				},
			},
			{
				ID:      "reactivity-to-observability-events",
				Source:  Reactivity,
				Target:  Observability,
				Kind:    ContractKindEvent,
				Object:  "диагностическое событие",
				Summary: "Контур реакции передаёт сведения о принятых, отклонённых и отброшенных внешних событиях.",
				Invariants: []string{
					"журналирование не является источником текущего состояния рабочей задачи",
					"диагностическое событие не меняет предметный результат обработки",
				},
			},
			{
				ID:      "integration-to-observability-events",
				Source:  Integration,
				Target:  Observability,
				Kind:    ContractKindEvent,
				Object:  "диагностическое событие",
				Summary: "Контур интеграции передаёт сведения о вызовах, выбранных системах, полноте результата и отказных состояниях.",
				Invariants: []string{
					"журналирование не является источником текущего состояния рабочей задачи",
					"диагностическое событие не меняет предметный результат обработки",
				},
			},
			{
				ID:      "decision-to-observability-events",
				Source:  Decision,
				Target:  Observability,
				Kind:    ContractKindEvent,
				Object:  "диагностическое событие",
				Summary: "Контур принятия решения передаёт сведения о рассмотрениях, проверках, основаниях и сформированных заданиях.",
				Invariants: []string{
					"журналирование не является источником текущего состояния рабочей задачи",
					"диагностическое событие не меняет предметный результат обработки",
				},
			},
			{
				ID:      "execution-to-observability-events",
				Source:  Execution,
				Target:  Observability,
				Kind:    ContractKindEvent,
				Object:  "диагностическое событие",
				Summary: "Контур исполнения передаёт сведения о задании, действии, операциях, ресурсах, рабочем месте, результате и отказных состояниях.",
				Invariants: []string{
					"журналирование не является источником текущего состояния рабочей задачи",
					"диагностическое событие не меняет предметный результат обработки",
				},
			},
			{
				ID:      "observability-to-user-interface-events",
				Source:  Observability,
				Target:  UserInterface,
				Kind:    ContractKindRead,
				Object:  "диагностические записи",
				Summary: "Контур журналирования и наблюдаемости передаёт пользовательскому интерфейсу сведения для просмотра запусков и отказов.",
				Invariants: []string{
					"пользовательский интерфейс только отображает диагностические записи",
				},
			},
			{
				ID:      "observability-to-analytics-samples",
				Source:  Observability,
				Target:  Analytics,
				Kind:    ContractKindRead,
				Object:  "выборка запусков и событий",
				Summary: "Контур журналирования и наблюдаемости предоставляет данные для расчёта показателей.",
				Invariants: []string{
					"контур аналитики не изменяет исходные диагностические записи",
				},
			},
			{
				ID:      "task-bank-to-research-cases",
				Source:  TaskBank,
				Target:  Research,
				Kind:    ContractKindRead,
				Object:  "задачи банка",
				Summary: "Банк задач предоставляет контуру исследований воспроизводимые проверочные наборы.",
				Invariants: []string{
					"банк задач не является источником текущего состояния рабочих задач",
				},
			},
			{
				ID:      "research-to-analytics-experiment-results",
				Source:  Research,
				Target:  Analytics,
				Kind:    ContractKindWrite,
				Object:  "результаты эксперимента",
				Summary: "Контур исследований передаёт результаты сравнительных прогонов для расчёта показателей.",
				Invariants: []string{
					"контур исследований не рассчитывает итоговые показатели вместо аналитики",
				},
			},
			{
				ID:      "analytics-to-methodology-report",
				Source:  Analytics,
				Target:  Methodology,
				Kind:    ContractKindEvent,
				Object:  "отчёт показателей",
				Summary: "Контур аналитики и статистики передаёт измеримые сведения, которые могут использоваться при изменении методик.",
				Invariants: []string{
					"отчёт показателей не изменяет методику автоматически",
				},
			},
		},
	}
}

func (r Registry) Validate() error {
	var messages []string
	contours := make(map[string]Contour, len(r.Contours))
	for index, contour := range r.Contours {
		name := strings.TrimSpace(contour.Name)
		if name == "" {
			messages = append(messages, fmt.Sprintf("контур %d не содержит имя", index))
			continue
		}
		if _, exists := contours[name]; exists {
			messages = append(messages, fmt.Sprintf("контур %q описан повторно", name))
			continue
		}
		contours[name] = contour
		if strings.TrimSpace(contour.Title) == "" {
			messages = append(messages, fmt.Sprintf("контур %q не содержит документационное имя", name))
		}
		if strings.TrimSpace(contour.Kind) == "" {
			messages = append(messages, fmt.Sprintf("контур %q не содержит класс", name))
		}
		if strings.TrimSpace(contour.Package) == "" {
			messages = append(messages, fmt.Sprintf("контур %q не содержит пакет реализации", name))
		}
		if strings.TrimSpace(contour.Documentation) == "" {
			messages = append(messages, fmt.Sprintf("контур %q не содержит документ", name))
		}
	}

	contractIDs := make(map[string]struct{}, len(r.Contracts))
	exchangeKeys := make(map[string]string, len(r.Contracts))
	for index, contract := range r.Contracts {
		id := strings.TrimSpace(contract.ID)
		if id == "" {
			messages = append(messages, fmt.Sprintf("контракт %d не содержит идентификатор", index))
			continue
		}
		if _, exists := contractIDs[id]; exists {
			messages = append(messages, fmt.Sprintf("контракт %q описан повторно", id))
		}
		contractIDs[id] = struct{}{}
		if _, exists := contours[contract.Source]; !exists {
			messages = append(messages, fmt.Sprintf("контракт %q ссылается на неизвестный исходный контур %q", id, contract.Source))
		}
		if _, exists := contours[contract.Target]; !exists {
			messages = append(messages, fmt.Sprintf("контракт %q ссылается на неизвестный целевой контур %q", id, contract.Target))
		}
		if contract.Source == contract.Target {
			messages = append(messages, fmt.Sprintf("контракт %q связывает контур сам с собой", id))
		}
		if !validContractKind(contract.Kind) {
			messages = append(messages, fmt.Sprintf("контракт %q не содержит тип обмена", id))
		}
		if strings.TrimSpace(contract.Object) == "" {
			messages = append(messages, fmt.Sprintf("контракт %q не содержит объект обмена", id))
		}
		if strings.TrimSpace(contract.Summary) == "" {
			messages = append(messages, fmt.Sprintf("контракт %q не содержит описание", id))
		}
		if len(contract.Invariants) == 0 {
			messages = append(messages, fmt.Sprintf("контракт %q не содержит инварианты", id))
		}
		for invariantIndex, invariant := range contract.Invariants {
			if strings.TrimSpace(invariant) == "" {
				messages = append(messages, fmt.Sprintf("контракт %q содержит пустой инвариант %d", id, invariantIndex))
			}
		}

		exchangeKey := strings.Join([]string{contract.Source, contract.Target, contract.Object}, "\x00")
		if existingID, exists := exchangeKeys[exchangeKey]; exists {
			messages = append(messages, fmt.Sprintf("контракты %q и %q описывают один и тот же обмен", existingID, id))
		}
		exchangeKeys[exchangeKey] = id
	}

	if err := validateRequiredContours(contours); err != nil {
		messages = append(messages, err.Error())
	}
	if err := validateRequiredContracts(contractIDs); err != nil {
		messages = append(messages, err.Error())
	}
	if len(messages) > 0 {
		sort.Strings(messages)
		return errors.New(strings.Join(messages, "; "))
	}
	return nil
}

func validContractKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case ContractKindRead, ContractKindWrite, ContractKindEvent:
		return true
	default:
		return false
	}
}

func validateRequiredContours(contours map[string]Contour) error {
	required := []string{
		Reactivity,
		Integration,
		Decision,
		Execution,
		Methodology,
		Configuration,
		Observability,
		UserInterface,
		Authorization,
		ExecutionQueue,
		Analytics,
		TaskBank,
		Research,
	}
	var missing []string
	for _, name := range required {
		if _, ok := contours[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("не описаны обязательные контуры: %s", strings.Join(missing, ", "))
	}
	return nil
}

func validateRequiredContracts(contractIDs map[string]struct{}) error {
	required := []string{
		"reactivity-to-integration-restore-request",
		"integration-to-decision-canonical-context",
		"decision-to-execution-assignment",
		"execution-to-decision-result",
		"execution-to-integration-operation",
		"methodology-to-decision-route",
		"methodology-to-execution-action",
		"configuration-to-integration-settings",
		"configuration-to-decision-rules",
		"configuration-to-execution-resources",
		"decision-to-execution-queue-item",
		"execution-queue-to-execution-assignment",
		"reactivity-to-observability-events",
		"integration-to-observability-events",
		"decision-to-observability-events",
		"execution-to-observability-events",
		"observability-to-analytics-samples",
		"task-bank-to-research-cases",
		"research-to-analytics-experiment-results",
	}
	var missing []string
	for _, id := range required {
		if _, ok := contractIDs[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("не описаны обязательные контракты: %s", strings.Join(missing, ", "))
	}
	return nil
}
