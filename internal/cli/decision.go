package cli

import (
	"context"
	"strings"

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
				printDecisionStartResultOnError(cmd, result)
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

func printDecisionStartResultOnError(cmd *cobra.Command, result decision.StartResult) {
	if result.Context.Signal.TaskNumber == 0 && result.Context.Signal.Source == "" && result.Context.Signal.Kind == "" && !result.Ready && result.Decision == nil && result.Execution == nil && result.Context.Issue == nil {
		return
	}

	printDecisionStartResult(cmd, result)
}

func printDecisionStartResult(cmd *cobra.Command, result decision.StartResult) {
	issue := result.Context.Issue
	cmd.Printf("task=%d\nsignal-source=%s\nsignal-kind=%s\ncontext-ready=%t\n", result.Context.Signal.TaskNumber, result.Context.Signal.Source, result.Context.Signal.Kind, result.Ready)
	if result.Decision != nil {
		cmd.Printf("decision-type=%s\n", result.Decision.Type)
		for _, reason := range result.Decision.Reasons {
			message := strings.TrimSpace(reason.Message)
			if reason.Code != "" && message != "" {
				cmd.Printf("decision-reason=%s:%s\n", reason.Code, message)
				continue
			}
			if reason.Code != "" {
				cmd.Printf("decision-reason=%s\n", reason.Code)
				continue
			}
			if message != "" {
				cmd.Printf("decision-reason=%s\n", message)
			}
		}
		if result.Decision.ExecutionPlan != nil && result.Decision.ExecutionPlan.Profile != "" {
			cmd.Printf("execution-profile=%s\n", result.Decision.ExecutionPlan.Profile)
		}
	}
	if result.Execution != nil {
		if result.Execution.Status != "" {
			cmd.Printf("execution-status=%s\n", result.Execution.Status)
		}
		if summary := strings.TrimSpace(result.Execution.Summary); summary != "" {
			cmd.Printf("execution-summary=%s\n", strings.Join(strings.Fields(summary), " "))
		}
		printLaunchStructuredOutput(cmd, *result.Execution)
	}
	if issue == nil {
		return
	}

	cmd.Printf("issue-number=%d\nissue-title=%s\nissue-state=%s\n", issue.Number, issue.Title, issue.State)
	if issue.URL != "" {
		cmd.Printf("issue-url=%s\n", issue.URL)
	}
}
