package cli

import (
	"context"

	"github.com/rasungatullin/progress/internal/decision"
	"github.com/rasungatullin/progress/internal/logging"
	"github.com/spf13/cobra"
)

type decisionStarter interface {
	Start(context.Context, decision.StartInput) (decision.StartResult, error)
}

type decisionFlags struct {
	task int
}

var decisionServiceFactory = func(cmd *cobra.Command) decisionStarter {
	return decision.NewService(logging.New(cmd.ErrOrStderr()))
}

func newDecisionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Контур принятия решения",
	}

	cmd.AddCommand(newDecisionStartCommand())
	return cmd
}

func newDecisionStartCommand() *cobra.Command {
	flags := &decisionFlags{}

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Полный запуск контура принятия решения",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newDecisionService(cmd)
			result, err := service.Start(context.Background(), decision.StartInput{TaskNumber: flags.task})
			if err != nil {
				return err
			}

			printDecisionStartResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().IntVar(&flags.task, "task", 0, "Номер задачи для запуска контура решения")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func newDecisionService(cmd *cobra.Command) decisionStarter {
	return decisionServiceFactory(cmd)
}

func printDecisionStartResult(cmd *cobra.Command, result decision.StartResult) {
	issue := result.Context.Issue
	cmd.Printf("task=%d\nsignal-source=%s\nsignal-kind=%s\ncontext-ready=%t\n", result.Context.Signal.TaskNumber, result.Context.Signal.Source, result.Context.Signal.Kind, result.Ready)
	if issue == nil {
		return
	}

	cmd.Printf("issue-number=%d\nissue-title=%s\nissue-state=%s\n", issue.Number, issue.Title, issue.State)
	if issue.URL != "" {
		cmd.Printf("issue-url=%s\n", issue.URL)
	}
}
