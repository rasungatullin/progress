package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/methodology"
	"github.com/spf13/cobra"
)

type methodologyFlags struct {
	repoRoot        string
	configHome      string
	scope           string
	kind            string
	name            string
	entityKind      string
	targetContour   string
	file            string
	jsonOutput      bool
	title           string
	description     string
	action          string
	outcome         string
	profile         string
	class           string
	step            string
	expectedResult  string
	reasonCode      string
	reasonMessage   string
	body            string
	bodyFile        string
	payload         string
	payloadFile     string
	checks          []string
	operations      []string
	constraints     []string
	hasFeatures     []string
	missingFeatures []string
	hasLabels       []string
	missingLabels   []string
}

func newMethodologyCommand() *cobra.Command {
	flags := &methodologyFlags{}

	cmd := &cobra.Command{
		Use:   "methodology",
		Short: "Контур методик",
	}
	cmd.PersistentFlags().StringVar(&flags.repoRoot, "repo-root", "", "Корень репозитория для локального каталога")
	cmd.PersistentFlags().StringVar(&flags.configHome, "config-home", "", "Каталог глобальных настроек progress")

	cmd.AddCommand(
		newMethodologyListCommand(flags),
		newMethodologyShowCommand(flags),
		newMethodologySelectCommand(flags),
		newMethodologySaveCommand(flags),
		newMethodologyAddCommand(flags),
	)
	return cmd
}

func newMethodologyListCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Список сущностей каталога методик",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome

			elements, err := methodology.NewService(nil).List(context.Background(), methodology.ElementRequest{
				RepoRoot:      flags.repoRoot,
				ConfigHome:    flags.configHome,
				Kind:          flags.kind,
				EntityKind:    flags.entityKind,
				TargetContour: flags.targetContour,
			})
			if err != nil {
				return err
			}
			if flags.jsonOutput {
				return printMethodologyJSON(cmd, elements)
			}

			printMethodologyElementsTable(cmd, elements)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.kind, "kind", "", "Фильтр вида сущности: route, action, instruction, entity или вид расширяемой сущности")
	cmd.Flags().StringVar(&flags.entityKind, "entity-kind", "", "Фильтр вида расширяемой сущности")
	cmd.Flags().StringVar(&flags.targetContour, "target-contour", "", "Фильтр целевого контура")
	cmd.Flags().BoolVar(&flags.jsonOutput, "json", false, "Вывести результат в JSON")
	return cmd
}

func newMethodologyShowCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Просмотр сущности каталога методик",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome

			element, err := methodology.NewService(nil).Get(context.Background(), methodology.ElementRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Kind:       flags.kind,
				Name:       flags.name,
				EntityKind: flags.entityKind,
			})
			if err != nil {
				return err
			}
			if flags.jsonOutput {
				return printMethodologyJSON(cmd, element)
			}

			printMethodologyElement(cmd, element)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.kind, "kind", "", "Вид сущности: route, action, instruction, entity или вид расширяемой сущности")
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя сущности")
	cmd.Flags().StringVar(&flags.entityKind, "entity-kind", "", "Вид расширяемой сущности при kind=entity")
	cmd.Flags().BoolVar(&flags.jsonOutput, "json", false, "Вывести результат в JSON")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newMethodologySelectCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "select",
		Short: "Выбор маршрута, действия и инструкции",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome

			result, err := methodology.NewService(nil).Resolve(context.Background(), methodology.SelectionRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Route:      flags.name,
				Action:     flags.action,
				Profile:    flags.profile,
			})
			if err != nil {
				return err
			}
			if flags.jsonOutput {
				return printMethodologyJSON(cmd, result)
			}

			printMethodologySelection(cmd, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.name, "route", "", "Имя маршрута")
	cmd.Flags().StringVar(&flags.action, "action", "", "Явное имя действия")
	cmd.Flags().StringVar(&flags.profile, "profile", "", "Явный исполнительный профиль")
	cmd.Flags().BoolVar(&flags.jsonOutput, "json", false, "Вывести результат в JSON")
	return cmd
}

func newMethodologySaveCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Сохранение каталога методик в выбранный слой",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome

			catalog, err := readMethodologyCatalogFile(flags.file)
			if err != nil {
				return err
			}
			scope, err := parseMethodologyScope(flags.scope)
			if err != nil {
				return err
			}

			result, err := methodology.NewService(nil).Save(context.Background(), methodology.CatalogWriteRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Scope:      scope,
				Catalog:    &catalog,
			})
			if err != nil {
				return err
			}
			printMethodologyWriteResult(cmd, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.file, "file", "", "Путь к JSON-файлу каталога методик")
	cmd.Flags().StringVar(&flags.scope, "scope", string(methodology.CatalogWriteScopeLocal), "Слой сохранения: local или global")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newMethodologyAddCommand(parent *methodologyFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Добавление или обновление экземпляра сущности методики",
	}
	cmd.AddCommand(
		newMethodologyAddRouteCommand(parent),
		newMethodologyAddActionCommand(parent),
		newMethodologyAddInstructionCommand(parent),
		newMethodologyAddEntityCommand(parent),
	)
	return cmd
}

func newMethodologyAddRouteCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "route",
		Short: "Добавление маршрута обработки",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome
			scope, err := parseMethodologyScope(flags.scope)
			if err != nil {
				return err
			}

			result, err := methodology.NewService(nil).Upsert(context.Background(), methodology.CatalogWriteRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Scope:      scope,
				Element: methodology.ElementUpsert{Route: &methodology.Route{
					Name:            flags.name,
					Title:           flags.title,
					Action:          flags.action,
					Outcome:         flags.outcome,
					Profile:         flags.profile,
					Description:     flags.description,
					Checks:          methodologyRouteChecksFromNames(flags.checks),
					Step:            flags.step,
					HasFeatures:     flags.hasFeatures,
					MissingFeatures: flags.missingFeatures,
					HasLabels:       flags.hasLabels,
					MissingLabels:   flags.missingLabels,
					ExpectedResult:  flags.expectedResult,
					Constraints:     flags.constraints,
					ReasonCode:      flags.reasonCode,
					ReasonMessage:   flags.reasonMessage,
				}},
			})
			if err != nil {
				return err
			}
			printMethodologyWriteResult(cmd, result)
			return nil
		},
	}
	bindMethodologyWriteFlags(cmd, &flags)
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя маршрута")
	cmd.Flags().StringVar(&flags.title, "title", "", "Документационное название маршрута")
	cmd.Flags().StringVar(&flags.action, "action", "", "Имя действия маршрута")
	cmd.Flags().StringVar(&flags.outcome, "outcome", "", "Исход маршрута без запуска действия")
	cmd.Flags().StringVar(&flags.profile, "profile", "", "Рекомендуемый исполнительный профиль")
	cmd.Flags().StringVar(&flags.description, "description", "", "Описание маршрута")
	cmd.Flags().StringArrayVar(&flags.checks, "check", nil, "Имя проверки маршрута, флаг можно повторять")
	cmd.Flags().StringVar(&flags.step, "step", "", "Шаг для контура принятия решения")
	cmd.Flags().StringArrayVar(&flags.hasFeatures, "has-feature", nil, "Обязательный признак задачи, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.missingFeatures, "missing-feature", nil, "Запрещающий признак задачи, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.hasLabels, "has-label", nil, "Обязательная метка задачи, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.missingLabels, "missing-label", nil, "Запрещающая метка задачи, флаг можно повторять")
	cmd.Flags().StringVar(&flags.expectedResult, "expected-result", "", "Ожидаемый результат действия")
	cmd.Flags().StringArrayVar(&flags.constraints, "constraint", nil, "Ограничение маршрута, флаг можно повторять")
	cmd.Flags().StringVar(&flags.reasonCode, "reason-code", "", "Код основания выбора")
	cmd.Flags().StringVar(&flags.reasonMessage, "reason-message", "", "Описание основания выбора")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func methodologyRouteChecksFromNames(names []string) []methodology.RouteCheck {
	checks := make([]methodology.RouteCheck, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		checks = append(checks, methodology.RouteCheck{Name: name})
	}
	if len(checks) == 0 {
		return nil
	}
	return checks
}

func newMethodologyAddActionCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "action",
		Short: "Добавление действия",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome
			scope, err := parseMethodologyScope(flags.scope)
			if err != nil {
				return err
			}

			ctx := context.Background()
			service := methodology.NewService(nil)
			action := methodologyActionFromFlags(ctx, cmd, service, flags, scope)
			result, err := service.Upsert(ctx, methodology.CatalogWriteRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Scope:      scope,
				Element:    methodology.ElementUpsert{Action: &action},
			})
			if err != nil {
				return err
			}
			printMethodologyWriteResult(cmd, result)
			return nil
		},
	}
	bindMethodologyWriteFlags(cmd, &flags)
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя действия")
	cmd.Flags().StringVar(&flags.class, "class", "", "Класс действия")
	cmd.Flags().StringVar(&flags.profile, "profile", "", "Рекомендуемый исполнительный профиль")
	cmd.Flags().StringArrayVar(&flags.operations, "operation", nil, "Операция действия, флаг можно повторять")
	cmd.Flags().StringVar(&flags.description, "description", "", "Описание действия")
	cmd.Flags().StringVar(&flags.expectedResult, "expected-result", "", "Ожидаемый результат действия")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newMethodologyAddInstructionCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "instruction",
		Short: "Добавление инструкции для языковой модели",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome
			scope, err := parseMethodologyScope(flags.scope)
			if err != nil {
				return err
			}
			body, err := readMethodologyBody(flags.body, flags.bodyFile)
			if err != nil {
				return err
			}

			result, err := methodology.NewService(nil).Upsert(context.Background(), methodology.CatalogWriteRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Scope:      scope,
				Element: methodology.ElementUpsert{Instruction: &methodology.Instruction{
					Name:          flags.name,
					Profile:       flags.profile,
					Action:        flags.action,
					TargetContour: flags.targetContour,
					Title:         flags.title,
					Description:   flags.description,
					Body:          body,
				}},
			})
			if err != nil {
				return err
			}
			printMethodologyWriteResult(cmd, result)
			return nil
		},
	}
	bindMethodologyWriteFlags(cmd, &flags)
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя инструкции")
	cmd.Flags().StringVar(&flags.profile, "profile", "", "Совместимый исполнительный профиль")
	cmd.Flags().StringVar(&flags.action, "action", "", "Совместимое действие")
	cmd.Flags().StringVar(&flags.targetContour, "target-contour", "", "Целевой контур")
	cmd.Flags().StringVar(&flags.title, "title", "", "Документационное название инструкции")
	cmd.Flags().StringVar(&flags.description, "description", "", "Описание инструкции")
	cmd.Flags().StringVar(&flags.body, "body", "", "Текст инструкции")
	cmd.Flags().StringVar(&flags.bodyFile, "body-file", "", "Путь к файлу с текстом инструкции")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newMethodologyAddEntityCommand(parent *methodologyFlags) *cobra.Command {
	flags := *parent
	cmd := &cobra.Command{
		Use:   "entity",
		Short: "Добавление расширяемой сущности",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.repoRoot = parent.repoRoot
			flags.configHome = parent.configHome
			scope, err := parseMethodologyScope(flags.scope)
			if err != nil {
				return err
			}
			payload, err := readMethodologyPayload(flags.payload, flags.payloadFile)
			if err != nil {
				return err
			}

			result, err := methodology.NewService(nil).Upsert(context.Background(), methodology.CatalogWriteRequest{
				RepoRoot:   flags.repoRoot,
				ConfigHome: flags.configHome,
				Scope:      scope,
				Element: methodology.ElementUpsert{Entity: &methodology.Entity{
					Kind:          flags.entityKind,
					Name:          flags.name,
					TargetContour: flags.targetContour,
					Title:         flags.title,
					Description:   flags.description,
					Payload:       payload,
				}},
			})
			if err != nil {
				return err
			}
			printMethodologyWriteResult(cmd, result)
			return nil
		},
	}
	bindMethodologyWriteFlags(cmd, &flags)
	cmd.Flags().StringVar(&flags.entityKind, "kind", "", "Вид расширяемой сущности")
	cmd.Flags().StringVar(&flags.name, "name", "", "Имя экземпляра")
	cmd.Flags().StringVar(&flags.targetContour, "target-contour", "", "Целевой контур")
	cmd.Flags().StringVar(&flags.title, "title", "", "Документационное название")
	cmd.Flags().StringVar(&flags.description, "description", "", "Описание")
	cmd.Flags().StringVar(&flags.payload, "payload", "", "JSON-содержимое расширяемой сущности")
	cmd.Flags().StringVar(&flags.payloadFile, "payload-file", "", "Путь к JSON-файлу расширяемой сущности")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func bindMethodologyWriteFlags(cmd *cobra.Command, flags *methodologyFlags) {
	cmd.Flags().StringVar(&flags.scope, "scope", string(methodology.CatalogWriteScopeLocal), "Слой сохранения: local или global")
}

func parseMethodologyScope(value string) (configuration.ConfigFileSource, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", string(methodology.CatalogWriteScopeLocal):
		return methodology.CatalogWriteScopeLocal, nil
	case string(methodology.CatalogWriteScopeGlobal):
		return methodology.CatalogWriteScopeGlobal, nil
	default:
		return "", fmt.Errorf("unknown methodology scope %q", value)
	}
}

func readMethodologyCatalogFile(path string) (methodology.Catalog, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return methodology.Catalog{}, fmt.Errorf("read methodology catalog file %s: %w", path, err)
	}

	var catalog methodology.Catalog
	if err := decodeStrictJSON(content, &catalog); err != nil {
		return methodology.Catalog{}, fmt.Errorf("parse methodology catalog file %s: %w", path, err)
	}
	return catalog, nil
}

func readMethodologyBody(body string, bodyFile string) (string, error) {
	if strings.TrimSpace(body) != "" && strings.TrimSpace(bodyFile) != "" {
		return "", fmt.Errorf("body and body-file are mutually exclusive")
	}
	if strings.TrimSpace(bodyFile) == "" {
		return strings.TrimSpace(body), nil
	}

	content, err := os.ReadFile(bodyFile)
	if err != nil {
		return "", fmt.Errorf("read instruction body file %s: %w", bodyFile, err)
	}
	return strings.TrimSpace(string(content)), nil
}

func readMethodologyPayload(payload string, payloadFile string) (json.RawMessage, error) {
	if strings.TrimSpace(payload) != "" && strings.TrimSpace(payloadFile) != "" {
		return nil, fmt.Errorf("payload and payload-file are mutually exclusive")
	}
	if strings.TrimSpace(payloadFile) != "" {
		content, err := os.ReadFile(payloadFile)
		if err != nil {
			return nil, fmt.Errorf("read entity payload file %s: %w", payloadFile, err)
		}
		payload = string(content)
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, nil
	}
	if !json.Valid([]byte(payload)) {
		return nil, fmt.Errorf("entity payload must be valid JSON")
	}
	return json.RawMessage(payload), nil
}

func printMethodologyJSON(cmd *cobra.Command, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	cmd.Println(string(payload))
	return nil
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func printMethodologyElementsTable(cmd *cobra.Command, elements []methodology.ListedElement) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "KIND\tNAME\tSOURCE\tPROFILE\tACTION\tOUTCOME\tENTITY_KIND\tTARGET_CONTOUR\tTITLE")
	for _, element := range elements {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", element.Kind, element.Name, element.Source, element.Profile, element.Action, element.Outcome, element.EntityKind, element.TargetContour, singleLine(element.Title))
	}
	_ = writer.Flush()
}

func printMethodologyElement(cmd *cobra.Command, element methodology.ListedElement) {
	cmd.Printf("kind=%s\nname=%s\nsource=%s\n", element.Kind, element.Name, element.Source)
	if element.EntityKind != "" {
		cmd.Printf("entity-kind=%s\n", element.EntityKind)
	}
	if element.TargetContour != "" {
		cmd.Printf("target-contour=%s\n", element.TargetContour)
	}
	if element.Profile != "" {
		cmd.Printf("profile=%s\n", element.Profile)
	}
	if element.Action != "" {
		cmd.Printf("action=%s\n", element.Action)
	}
	if element.Outcome != "" {
		cmd.Printf("outcome=%s\n", element.Outcome)
	}
	if element.Class != "" {
		cmd.Printf("class=%s\n", element.Class)
	}
	if element.Title != "" {
		cmd.Printf("title=%s\n", element.Title)
	}
	if element.Description != "" {
		cmd.Printf("description=%s\n", singleLine(element.Description))
	}
	payload := methodologyElementPayload(element)
	if payload == nil {
		return
	}
	cmd.Printf("json<<%s\n%s\n%s\n", methodologyJSONDelimiter, string(payload), methodologyJSONDelimiter)
}

func printMethodologySelection(cmd *cobra.Command, result methodology.SelectionResult) {
	cmd.Printf("route=%s\nroute-source=%s\naction=%s\naction-source=%s\noutcome=%s\nprofile=%s\n", result.Route.Name, result.RouteSource, result.Action.Name, result.ActionSource, result.Route.Outcome, result.Profile)
	if result.Instruction.Name != "" {
		cmd.Printf("instruction=%s\ninstruction-source=%s\n", result.Instruction.Name, result.InstructionSource)
	}
	if result.GlobalCatalogPath != "" {
		cmd.Printf("global-catalog=%s\n", result.GlobalCatalogPath)
	}
	if result.LocalCatalogPath != "" {
		cmd.Printf("local-catalog=%s\n", result.LocalCatalogPath)
	}
	for _, diagnostic := range result.Diagnostics {
		cmd.Printf("diagnostic=%s\n", diagnostic)
	}
}

func printMethodologyWriteResult(cmd *cobra.Command, result methodology.CatalogWriteResult) {
	cmd.Printf("scope=%s\npath=%s\nroutes=%d\nactions=%d\ninstructions=%d\nentities=%d\n", result.Scope, result.Path, len(result.Catalog.Routes), len(result.Catalog.Actions), len(result.Catalog.Instructions), len(result.Catalog.Entities))
}

func methodologyActionFromFlags(ctx context.Context, cmd *cobra.Command, service *methodology.Service, flags methodologyFlags, scope configuration.ConfigFileSource) methodology.Action {
	action := existingMethodologyAction(ctx, service, flags.repoRoot, flags.configHome, scope, flags.name)
	action.Name = flags.name
	if cmd.Flags().Changed("class") {
		action.Class = flags.class
	}
	if cmd.Flags().Changed("profile") {
		action.Profile = flags.profile
	}
	if cmd.Flags().Changed("operation") {
		action.Operations = methodologyOperationsFromFlags(flags.operations)
	}
	if cmd.Flags().Changed("description") {
		action.Description = flags.description
	}
	if cmd.Flags().Changed("expected-result") {
		action.ExpectedResult = flags.expectedResult
	}
	return action
}

func existingMethodologyAction(ctx context.Context, service *methodology.Service, repoRoot string, configHome string, scope configuration.ConfigFileSource, name string) methodology.Action {
	if service == nil {
		service = methodology.NewService(nil)
	}
	snapshot, err := service.Load(ctx, methodology.CatalogRequest{RepoRoot: repoRoot, ConfigHome: configHome})
	if err != nil {
		return methodology.Action{}
	}
	name = strings.TrimSpace(name)
	for _, layer := range snapshot.Layers {
		if layer.Source != scope {
			continue
		}
		if action, ok := findMethodologyActionByExactName(layer.Catalog.Actions, name); ok {
			return cloneMethodologyAction(action)
		}
	}
	if scope != methodology.CatalogWriteScopeLocal {
		return methodology.Action{}
	}
	if action, ok := findMethodologyActionByExactName(snapshot.Catalog.Actions, name); ok {
		return cloneMethodologyAction(action)
	}
	return methodology.Action{}
}

func findMethodologyActionByExactName(actions []methodology.Action, name string) (methodology.Action, bool) {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Name), name) {
			return action, true
		}
	}
	return methodology.Action{}, false
}

func cloneMethodologyAction(action methodology.Action) methodology.Action {
	cloned := action
	cloned.Aliases = append([]string(nil), action.Aliases...)
	cloned.Operations = append([]methodology.ActionOperation(nil), action.Operations...)
	return cloned
}

func methodologyOperationsFromFlags(names []string) []methodology.ActionOperation {
	operations := make([]methodology.ActionOperation, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		operations = append(operations, methodology.ActionOperation{Name: name, Kind: name})
	}
	return operations
}

func methodologyElementPayload(element methodology.ListedElement) []byte {
	var payload any
	switch {
	case element.Route != nil:
		payload = element.Route
	case element.ActionEntry != nil:
		payload = element.ActionEntry
	case element.Instruction != nil:
		payload = element.Instruction
	case element.Entity != nil:
		payload = element.Entity
	default:
		return nil
	}

	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil
	}
	return encoded
}

const methodologyJSONDelimiter = "PROGRESS_METHODOLOGY_JSON"
