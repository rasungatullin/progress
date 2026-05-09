package cli

import "github.com/spf13/cobra"

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Генерация сценариев автодополнения",
		Long: "Генерирует сценарий автодополнения для выбранной оболочки. " +
			"Подробности подключения выводятся в справке конкретной подкоманды.",
	}

	cmd.AddCommand(
		newBashCompletionCommand(root),
		newFishCompletionCommand(root),
		newPowerShellCompletionCommand(root),
		newZshCompletionCommand(root),
	)

	return cmd
}

func newBashCompletionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "bash",
		Short: "Генерация автодополнения для bash",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return root.GenBashCompletion(cmd.OutOrStdout())
		},
	}
}

func newFishCompletionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "fish",
		Short: "Генерация автодополнения для fish",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		},
	}
}

func newPowerShellCompletionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "powershell",
		Short: "Генерация автодополнения для powershell",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		},
	}
}

func newZshCompletionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "zsh",
		Short: "Генерация автодополнения для zsh",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return root.GenZshCompletion(cmd.OutOrStdout())
		},
	}
}
