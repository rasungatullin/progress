package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "progress",
		Short:         "Комплекс координации вычислительных контуров",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.SetHelpTemplate(helpTemplate)
	cmd.SetUsageTemplate(usageTemplate)
	cmd.SetHelpCommand(newHelpCommand(cmd))
	cmd.CompletionOptions.DisableDefaultCmd = true

	cmd.AddCommand(newDecisionCommand())
	cmd.AddCommand(newIntegrationCommand())
	cmd.AddCommand(newExecutionCommand())
	cmd.AddCommand(newWebCommand())
	cmd.AddCommand(newCompletionCommand(cmd))
	localizeHelp(cmd)

	return cmd
}

const helpTemplate = `{{with (or .Long .Short)}}{{.}}
{{end}}{{if or .Runnable .HasSubCommands}}Порядок вызова:
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}

Доступные команды:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Флаги:{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Наследуемые флаги:{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Дополнительные разделы справки:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Для получения сведений по команде выполните:
  {{.CommandPath}} [команда] --help{{end}}
`

const usageTemplate = `Порядок вызова:
  {{.UseLine}}{{if .HasAvailableSubCommands}}

Доступные команды:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Флаги:{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Наследуемые флаги:{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Дополнительные разделы справки:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Для получения сведений по команде выполните:
  {{.CommandPath}} [команда] --help{{end}}
`

func newHelpCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help [команда]",
		Short: "Справка по командам",
		Long:  "Выводит сведения о команде и её флагах.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target, _, err := root.Find(args)
			if err != nil {
				return err
			}

			return target.Help()
		},
	}

	return cmd
}

func localizeHelp(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	if flag := cmd.Flags().Lookup("help"); flag != nil {
		flag.Usage = localizeHelpUsage(flag.Usage, cmd.Name())
	}

	for _, child := range cmd.Commands() {
		localizeHelp(child)
	}
}

func localizeHelpUsage(usage string, name string) string {
	if usage == "help for "+name {
		return "Справка по команде"
	}

	if strings.HasPrefix(usage, "help for ") {
		return fmt.Sprintf("Справка по команде %q", strings.TrimPrefix(usage, "help for "))
	}

	return usage
}
