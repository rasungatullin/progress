package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	configcontour "github.com/rasungatullin/progress/internal/configuration"
	"github.com/rasungatullin/progress/internal/execution/model"
	"github.com/spf13/cobra"
)

type configurationResourceFlags struct {
	format     string
	scope      string
	repoRoot   string
	configHome string

	typ          string
	enabled      bool
	disabled     bool
	config       []string
	tools        []string
	traits       []string
	tool         string
	runner       string
	resource     string
	model        string
	environment  string
	modelBinding string
}

const (
	configurationOutputText = "text"
	configurationOutputJSON = "json"
)

func newConfigurationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configuration",
		Short: "Контур настроек и ресурсов",
	}

	cmd.AddCommand(newConfigurationResourcesCommand())
	cmd.AddCommand(newConfigurationPrivateCommand())
	return cmd
}

func newConfigurationPrivateCommand() *cobra.Command {
	cmd := newIntegrationPrivateCommand()
	cmd.Use = "private"
	cmd.Short = "Управление хранилищем приватных значений"
	cmd.PersistentFlags().String("format", configurationOutputText, "Формат вывода: text (по умолчанию) или json")
	return cmd
}

func newConfigurationResourcesCommand() *cobra.Command {
	flags := &configurationResourceFlags{format: configurationOutputText, scope: string(configcontour.ConfigFileSourceLocal)}
	cmd := &cobra.Command{
		Use:   "resources",
		Short: "Настройки окружений, инструментов и ресурсов исполнения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigurationResourcesList(cmd, flags)
		},
	}

	bindConfigurationResourceCommonFlags(cmd, flags)
	cmd.AddCommand(newConfigurationResourcesListCommand(flags))
	cmd.AddCommand(newConfigurationEnvironmentCommand(flags))
	cmd.AddCommand(newConfigurationToolCommand(flags))
	cmd.AddCommand(newConfigurationResourceCommand(flags))
	cmd.AddCommand(newConfigurationBindingCommand(flags))
	cmd.AddCommand(newConfigurationDefaultsCommand(flags))
	return cmd
}

func newConfigurationResourcesListCommand(flags *configurationResourceFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Вывод настроек окружений, инструментов и ресурсов",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigurationResourcesList(cmd, flags)
		},
	}
}

func newConfigurationEnvironmentCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "environment",
		Short: "Настройка окружений исполнения",
	}
	cmd.AddCommand(newConfigurationEnvironmentSetCommand(parentFlags))
	return cmd
}

func newConfigurationEnvironmentSetCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	flags := *parentFlags
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Добавление или изменение окружения исполнения",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags = effectiveConfigurationResourceFlags(parentFlags, &flags)
			parentConfig, hasParentConfig, err := loadExecutionResourceParentLayerForWrite(&flags)
			if err != nil {
				return err
			}
			path, err := mutateExecutionResourceLayer(cmd, &flags, func(config *model.ResourceConfigFile) error {
				name := strings.TrimSpace(args[0])
				if name == "" {
					return fmt.Errorf("environment name must not be empty")
				}
				exists := mapHasKey(config.Environments, name)
				environment := config.Environments[name]
				if !exists && hasParentConfig {
					if parentEnvironment, ok := parentConfig.Environments[name]; ok {
						environment = parentEnvironment
					}
				}
				if cmd.Flags().Changed("type") {
					environment.Type = strings.TrimSpace(flags.typ)
				}
				if strings.TrimSpace(environment.Type) == "" {
					environment.Type = configurationEnvironmentTypeFromName(name)
				}
				if strings.TrimSpace(environment.Type) == "" {
					return fmt.Errorf("environment type is required for custom environment %q", name)
				}
				enabled, changed, err := configurationEnabledFromFlags(cmd, &flags)
				if err != nil {
					return err
				}
				if changed || !exists {
					environment.Enabled = enabled
				}
				values, err := parseConfigurationKeyValues(flags.config)
				if err != nil {
					return err
				}
				if len(values) > 0 {
					environment.Config = mergeStringMap(environment.Config, values)
				}
				config.Environments[name] = environment
				return nil
			})
			if err != nil {
				return err
			}
			cmd.Printf("status=stored\nscope=%s\npath=%s\n", normalizedConfigurationScope(flags.scope), path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.typ, "type", "", "Тип окружения: local или worktree")
	cmd.Flags().BoolVar(&flags.enabled, "enabled", true, "Включить окружение")
	cmd.Flags().BoolVar(&flags.disabled, "disabled", false, "Отключить окружение")
	cmd.Flags().StringArrayVar(&flags.config, "config", nil, "Параметр окружения key=value, флаг можно повторять")
	return cmd
}

func newConfigurationToolCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool",
		Short: "Настройка инструментов исполнения",
	}
	cmd.AddCommand(newConfigurationToolSetCommand(parentFlags))
	return cmd
}

func newConfigurationToolSetCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	flags := *parentFlags
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Добавление или изменение инструмента исполнения",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags = effectiveConfigurationResourceFlags(parentFlags, &flags)
			parentConfig, hasParentConfig, err := loadExecutionResourceParentLayerForWrite(&flags)
			if err != nil {
				return err
			}
			path, err := mutateExecutionResourceLayer(cmd, &flags, func(config *model.ResourceConfigFile) error {
				name := strings.TrimSpace(args[0])
				if name == "" {
					return fmt.Errorf("tool name must not be empty")
				}
				exists := mapHasKey(config.Tools, name)
				tool := config.Tools[name]
				if !exists && hasParentConfig {
					if parentTool, ok := parentConfig.Tools[name]; ok {
						tool = parentTool
					}
				}
				if cmd.Flags().Changed("type") {
					tool.Type = strings.TrimSpace(flags.typ)
				}
				if strings.TrimSpace(tool.Type) == "" {
					tool.Type = configcontour.ToolTypeAgenticSystem
				}
				enabled, changed, err := configurationEnabledFromFlags(cmd, &flags)
				if err != nil {
					return err
				}
				if changed || !exists {
					tool.Enabled = enabled
				}
				values, err := parseConfigurationKeyValues(flags.config)
				if err != nil {
					return err
				}
				if len(values) > 0 {
					tool.Config = mergeStringMap(tool.Config, values)
				}
				config.Tools[name] = tool
				return nil
			})
			if err != nil {
				return err
			}
			cmd.Printf("status=stored\nscope=%s\npath=%s\n", normalizedConfigurationScope(flags.scope), path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.typ, "type", configcontour.ToolTypeAgenticSystem, "Тип инструмента")
	cmd.Flags().BoolVar(&flags.enabled, "enabled", true, "Включить инструмент")
	cmd.Flags().BoolVar(&flags.disabled, "disabled", false, "Отключить инструмент")
	cmd.Flags().StringArrayVar(&flags.config, "config", nil, "Параметр инструмента key=value, флаг можно повторять")
	return cmd
}

func newConfigurationResourceCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "Настройка ресурсов исполнения",
	}
	cmd.AddCommand(newConfigurationResourceSetCommand(parentFlags))
	return cmd
}

func newConfigurationResourceSetCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	flags := *parentFlags
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Добавление или изменение ресурса исполнения",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags = effectiveConfigurationResourceFlags(parentFlags, &flags)
			parentConfig, hasParentConfig, err := loadExecutionResourceParentLayerForWrite(&flags)
			if err != nil {
				return err
			}
			path, err := mutateExecutionResourceLayer(cmd, &flags, func(config *model.ResourceConfigFile) error {
				name := strings.TrimSpace(args[0])
				if name == "" {
					return fmt.Errorf("resource name must not be empty")
				}
				exists := mapHasKey(config.Resources, name)
				resource := config.Resources[name]
				if !exists && hasParentConfig {
					if parentResource, ok := parentConfig.Resources[name]; ok {
						resource = parentResource
					}
				}
				if cmd.Flags().Changed("type") {
					resource.Type = strings.TrimSpace(flags.typ)
				}
				if strings.TrimSpace(resource.Type) == "" {
					resource.Type = configcontour.ResourceTypeModel
				}
				enabled, changed, err := configurationEnabledFromFlags(cmd, &flags)
				if err != nil {
					return err
				}
				if changed || !exists {
					resource.Enabled = enabled
				}
				if cmd.Flags().Changed("tool") {
					resource.Tools = normalizedConfigurationList(flags.tools)
				}
				if cmd.Flags().Changed("trait") {
					resource.Traits = normalizedConfigurationList(flags.traits)
				}
				values, err := parseConfigurationKeyValues(flags.config)
				if err != nil {
					return err
				}
				if len(values) > 0 {
					resource.Config = mergeStringMap(resource.Config, values)
				}
				config.Resources[name] = resource
				return nil
			})
			if err != nil {
				return err
			}
			cmd.Printf("status=stored\nscope=%s\npath=%s\n", normalizedConfigurationScope(flags.scope), path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.typ, "type", configcontour.ResourceTypeModel, "Тип ресурса")
	cmd.Flags().BoolVar(&flags.enabled, "enabled", true, "Включить ресурс")
	cmd.Flags().BoolVar(&flags.disabled, "disabled", false, "Отключить ресурс")
	cmd.Flags().StringArrayVar(&flags.tools, "tool", nil, "Допустимый инструмент ресурса, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.traits, "trait", nil, "Признак ресурса, флаг можно повторять")
	cmd.Flags().StringArrayVar(&flags.config, "config", nil, "Параметр ресурса key=value, флаг можно повторять")
	return cmd
}

func newConfigurationBindingCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "binding",
		Short: "Настройка привязок ресурсов",
	}
	cmd.AddCommand(newConfigurationBindingSetCommand(parentFlags))
	return cmd
}

func newConfigurationBindingSetCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	flags := *parentFlags
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Добавление или изменение привязки ресурсов",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags = effectiveConfigurationResourceFlags(parentFlags, &flags)
			path, err := mutateExecutionResourceLayer(cmd, &flags, func(config *model.ResourceConfigFile) error {
				name := strings.TrimSpace(args[0])
				if name == "" {
					return fmt.Errorf("binding name must not be empty")
				}
				binding := config.Bindings[name]
				tool := firstNonEmpty(flags.tool, flags.runner)
				resource := firstNonEmpty(flags.resource, flags.model)
				if tool != "" {
					binding.Tool = tool
					binding.Runner = tool
				}
				if resource != "" {
					binding.Resource = resource
					binding.Model = resource
				}
				if cmd.Flags().Changed("environment") {
					binding.Environment = strings.TrimSpace(flags.environment)
				}
				if strings.TrimSpace(binding.Tool) == "" {
					return fmt.Errorf("binding tool must not be empty")
				}
				if strings.TrimSpace(binding.Resource) == "" {
					return fmt.Errorf("binding resource must not be empty")
				}
				config.Bindings[name] = binding
				return nil
			})
			if err != nil {
				return err
			}
			cmd.Printf("status=stored\nscope=%s\npath=%s\n", normalizedConfigurationScope(flags.scope), path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.tool, "tool", "", "Имя инструмента")
	cmd.Flags().StringVar(&flags.runner, "runner", "", "Совместимое имя инструмента для старого формата")
	cmd.Flags().StringVar(&flags.resource, "resource", "", "Имя ресурса")
	cmd.Flags().StringVar(&flags.model, "model", "", "Совместимое имя модели для старого формата")
	cmd.Flags().StringVar(&flags.environment, "environment", "", "Имя окружения")
	return cmd
}

func newConfigurationDefaultsCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "defaults",
		Short: "Настройка значений по умолчанию",
	}
	cmd.AddCommand(newConfigurationDefaultsSetCommand(parentFlags))
	return cmd
}

func newConfigurationDefaultsSetCommand(parentFlags *configurationResourceFlags) *cobra.Command {
	flags := *parentFlags
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Изменение значений по умолчанию",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags = effectiveConfigurationResourceFlags(parentFlags, &flags)
			path, err := mutateExecutionResourceLayer(cmd, &flags, func(config *model.ResourceConfigFile) error {
				if cmd.Flags().Changed("model-binding") {
					config.Defaults.ModelBinding = strings.TrimSpace(flags.modelBinding)
				}
				if cmd.Flags().Changed("environment") {
					config.Defaults.Environment = strings.TrimSpace(flags.environment)
				}
				return nil
			})
			if err != nil {
				return err
			}
			cmd.Printf("status=stored\nscope=%s\npath=%s\n", normalizedConfigurationScope(flags.scope), path)
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.modelBinding, "model-binding", "", "Привязка ресурсов по умолчанию")
	cmd.Flags().StringVar(&flags.environment, "environment", "", "Окружение по умолчанию")
	return cmd
}

func bindConfigurationResourceCommonFlags(cmd *cobra.Command, flags *configurationResourceFlags) {
	cmd.PersistentFlags().StringVar(&flags.format, "format", flags.format, "Формат вывода: text или json")
	cmd.PersistentFlags().StringVar(&flags.scope, "scope", flags.scope, "Слой записи: local или global")
	cmd.PersistentFlags().StringVar(&flags.repoRoot, "repo-root", "", "Корень репозитория для локального слоя")
	cmd.PersistentFlags().StringVar(&flags.configHome, "config-home", "", "Каталог глобальной конфигурации")
}

func effectiveConfigurationResourceFlags(parentFlags *configurationResourceFlags, localFlags *configurationResourceFlags) configurationResourceFlags {
	effective := *localFlags
	effective.format = parentFlags.format
	effective.scope = parentFlags.scope
	effective.repoRoot = parentFlags.repoRoot
	effective.configHome = parentFlags.configHome
	return effective
}

func runConfigurationResourcesList(cmd *cobra.Command, flags *configurationResourceFlags) error {
	repoRoot, err := configurationResourcesRepoRoot(flags)
	if err != nil {
		return err
	}
	loaded, err := configcontour.LoadExecutionResourceConfigWithHome(repoRoot, flags.configHome, os.ReadFile)
	if err != nil {
		return err
	}

	format, err := configurationOutputFormat(flags.format)
	if err != nil {
		return err
	}
	if format == configurationOutputJSON {
		return printConfigurationJSON(cmd, struct {
			Config             model.ResourceConfigFile                  `json:"config"`
			EnvironmentSources map[string]configcontour.ConfigFileSource `json:"environment_sources,omitempty"`
			ToolSources        map[string]configcontour.ConfigFileSource `json:"tool_sources,omitempty"`
			ResourceSources    map[string]configcontour.ConfigFileSource `json:"resource_sources,omitempty"`
			BindingSources     map[string]configcontour.ConfigFileSource `json:"binding_sources,omitempty"`
		}{
			Config:             loaded.Config,
			EnvironmentSources: loaded.EnvironmentSources,
			ToolSources:        loaded.ToolSources,
			ResourceSources:    loaded.ResourceSources,
			BindingSources:     loaded.BindingSources,
		})
	}

	printConfigurationResourcesText(cmd, loaded)
	return nil
}

func mutateExecutionResourceLayer(cmd *cobra.Command, flags *configurationResourceFlags, mutate func(*model.ResourceConfigFile) error) (string, error) {
	repoRoot, err := configurationResourcesRepoRoot(flags)
	if err != nil {
		return "", err
	}
	source, err := configurationConfigSource(flags.scope)
	if err != nil {
		return "", err
	}
	path, err := configcontour.ExecutionResourceConfigPath(repoRoot, flags.configHome, source)
	if err != nil {
		return "", err
	}

	config, preserveEmptyGit, err := loadExecutionResourceLayerForWrite(path)
	if err != nil {
		return "", err
	}
	if err := mutate(&config); err != nil {
		return "", err
	}
	config = configcontour.NormalizeExecutionResourceLayerConfig(config)
	if preserveEmptyGit && config.Git == nil {
		config.Git = &model.GitConfig{}
	}
	if err := writeExecutionResourceLayer(path, config); err != nil {
		return "", err
	}

	return path, nil
}

func loadExecutionResourceLayerForWrite(path string) (model.ResourceConfigFile, bool, error) {
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		if errors.Is(readErr, fs.ErrNotExist) {
			return configcontour.NewExecutionResourceConfigFile(), false, nil
		}
		return model.ResourceConfigFile{}, false, readErr
	}

	preserveEmptyGit := jsonObjectHasKey(content, "git")
	config, err := configcontour.LoadExecutionResourceConfigFile(path, func(string) ([]byte, error) { return content, nil })
	if err == nil {
		return config, preserveEmptyGit, nil
	}
	return model.ResourceConfigFile{}, false, err
}

func jsonObjectHasKey(content []byte, key string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return false
	}
	_, ok := raw[key]
	return ok
}

func loadExecutionResourceParentLayerForWrite(flags *configurationResourceFlags) (model.ResourceConfigFile, bool, error) {
	source, err := configurationConfigSource(flags.scope)
	if err != nil {
		return model.ResourceConfigFile{}, false, err
	}
	if source != configcontour.ConfigFileSourceLocal {
		return model.ResourceConfigFile{}, false, nil
	}

	repoRoot, err := configurationResourcesRepoRoot(flags)
	if err != nil {
		return model.ResourceConfigFile{}, false, err
	}
	path, err := configcontour.ExecutionResourceConfigPath(repoRoot, flags.configHome, configcontour.ConfigFileSourceGlobal)
	if err != nil {
		return model.ResourceConfigFile{}, false, nil
	}

	config, err := configcontour.LoadExecutionResourceConfigFile(path, os.ReadFile)
	if err == nil {
		return config, true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return model.ResourceConfigFile{}, false, nil
	}
	return model.ResourceConfigFile{}, false, err
}

func writeExecutionResourceLayer(path string, config model.ResourceConfigFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create execution resource config directory: %w", err)
	}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(path, content, 0o600)
}

func printConfigurationResourcesText(cmd *cobra.Command, loaded configcontour.ExecutionResourceConfig) {
	cmd.Printf("defaults.model-binding=%s\n", loaded.Config.Defaults.ModelBinding)
	cmd.Printf("defaults.environment=%s\n", loaded.Config.Defaults.Environment)
	printConfigurationGitSummary(cmd, loaded)
	printConfigurationEnvironmentTable(cmd, loaded)
	printConfigurationToolTable(cmd, loaded)
	printConfigurationResourceTable(cmd, loaded)
	printConfigurationBindingTable(cmd, loaded)
}

func printConfigurationGitSummary(cmd *cobra.Command, loaded configcontour.ExecutionResourceConfig) {
	git := loaded.Config.Git
	identity := false
	signing := false
	push := false
	if git != nil {
		identity = git.Identity != nil && strings.TrimSpace(git.Identity.AuthorName) != ""
		signing = git.Signing != nil && git.Signing.Enabled
		push = git.Push != nil && (strings.TrimSpace(git.Push.SSHIdentityFile) != "" || strings.TrimSpace(git.Push.SSHIdentityPrivate) != "")
	}
	cmd.Printf("git.identity=%t\n", identity)
	cmd.Printf("git.signing=%t\n", signing)
	cmd.Printf("git.push-key=%t\n", push)
}

func printConfigurationEnvironmentTable(cmd *cobra.Command, loaded configcontour.ExecutionResourceConfig) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ENVIRONMENT\tTYPE\tENABLED\tSOURCE")
	for _, name := range sortedConfigurationKeys(loaded.Config.Environments) {
		environment := loaded.Config.Environments[name]
		fmt.Fprintf(writer, "%s\t%s\t%t\t%s\n", name, environment.Type, environment.Enabled, loaded.EnvironmentSources[name])
	}
	_ = writer.Flush()
}

func printConfigurationToolTable(cmd *cobra.Command, loaded configcontour.ExecutionResourceConfig) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TOOL\tTYPE\tENABLED\tSOURCE")
	for _, name := range sortedConfigurationKeys(loaded.Config.Tools) {
		tool := loaded.Config.Tools[name]
		fmt.Fprintf(writer, "%s\t%s\t%t\t%s\n", name, tool.Type, tool.Enabled, loaded.ToolSources[name])
	}
	_ = writer.Flush()
}

func printConfigurationResourceTable(cmd *cobra.Command, loaded configcontour.ExecutionResourceConfig) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "RESOURCE\tTYPE\tENABLED\tTOOLS\tTRAITS\tSOURCE")
	for _, name := range sortedConfigurationKeys(loaded.Config.Resources) {
		resource := loaded.Config.Resources[name]
		fmt.Fprintf(writer, "%s\t%s\t%t\t%s\t%s\t%s\n", name, resource.Type, resource.Enabled, strings.Join(resource.Tools, ","), strings.Join(resource.Traits, ","), loaded.ResourceSources[name])
	}
	_ = writer.Flush()
}

func printConfigurationBindingTable(cmd *cobra.Command, loaded configcontour.ExecutionResourceConfig) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "BINDING\tTOOL\tRESOURCE\tENVIRONMENT\tSOURCE")
	for _, name := range sortedConfigurationKeys(loaded.Config.Bindings) {
		binding := loaded.Config.Bindings[name]
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", name, firstNonEmpty(binding.Tool, binding.Runner), firstNonEmpty(binding.Resource, binding.Model), binding.Environment, loaded.BindingSources[name])
	}
	_ = writer.Flush()
}

func configurationResourcesRepoRoot(flags *configurationResourceFlags) (string, error) {
	if root := strings.TrimSpace(flags.repoRoot); root != "" {
		return root, nil
	}
	output, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(output)), nil
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return "", fmt.Errorf("resolve git repository root for configuration resources: %w", err)
	}
	return cwd, nil
}

func configurationConfigSource(value string) (configcontour.ConfigFileSource, error) {
	switch normalizedConfigurationScope(value) {
	case string(configcontour.ConfigFileSourceLocal):
		return configcontour.ConfigFileSourceLocal, nil
	case string(configcontour.ConfigFileSourceGlobal):
		return configcontour.ConfigFileSourceGlobal, nil
	default:
		return "", fmt.Errorf("--scope supports only local or global")
	}
}

func normalizedConfigurationScope(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return string(configcontour.ConfigFileSourceLocal)
	}
	return value
}

func configurationEnvironmentTypeFromName(name string) string {
	switch strings.TrimSpace(name) {
	case configcontour.EnvironmentTypeLocal:
		return configcontour.EnvironmentTypeLocal
	case configcontour.EnvironmentTypeWorktree:
		return configcontour.EnvironmentTypeWorktree
	default:
		return ""
	}
}

func configurationOutputFormat(value string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(value))
	if format == "" {
		format = configurationOutputText
	}
	if format != configurationOutputText && format != configurationOutputJSON {
		return "", fmt.Errorf("--format supports only text or json")
	}
	return format, nil
}

func configurationEnabledFromFlags(cmd *cobra.Command, flags *configurationResourceFlags) (bool, bool, error) {
	enabledChanged := cmd.Flags().Changed("enabled")
	if enabledChanged && flags.disabled {
		return false, false, fmt.Errorf("--enabled and --disabled are mutually exclusive")
	}
	if flags.disabled {
		return false, true, nil
	}
	if enabledChanged {
		return flags.enabled, true, nil
	}
	return true, false, nil
}

func parseConfigurationKeyValues(values []string) (map[string]string, error) {
	result := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("config value must use key=value form")
		}
		result[key] = strings.TrimSpace(val)
	}
	return result, nil
}

func mergeStringMap(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 {
		base = map[string]string{}
	}
	for key, value := range override {
		base[key] = value
	}
	return base
}

func normalizedConfigurationList(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sortedConfigurationKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mapHasKey[T any](values map[string]T, key string) bool {
	_, ok := values[key]
	return ok
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func printConfigurationJSON(cmd *cobra.Command, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	cmd.Println(string(encoded))
	return nil
}
