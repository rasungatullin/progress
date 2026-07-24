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
	task  int
	route string
}

type decisionServiceFactoryFunc func(*cobra.Command) decisionStarter

type decisionServiceFactoryContextKey struct{}

var decisionServiceFactory = func(cmd *cobra.Command) decisionStarter {
	return decision.NewService(logging.New(cmd.ErrOrStderr()))
}

func setDecisionServiceFactory(cmd *cobra.Command, factory decisionServiceFactoryFunc) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cmd.SetContext(context.WithValue(ctx, decisionServiceFactoryContextKey{}, factory))
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
			result, err := service.Start(cmd.Context(), decision.StartInput{TaskNumber: flags.task, Route: flags.route})
			if err != nil {
				printDecisionStartResultOnError(cmd, result)
				return err
			}

			printDecisionStartResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().IntVar(&flags.task, "task", 0, "Номер задачи для запуска контура решения")
	cmd.Flags().StringVar(&flags.route, "route", "", "Имя маршрута обработки")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func newDecisionService(cmd *cobra.Command) decisionStarter {
	if factory, ok := cmd.Context().Value(decisionServiceFactoryContextKey{}).(decisionServiceFactoryFunc); ok && factory != nil {
		return factory(cmd)
	}

	return decisionServiceFactory(cmd)
}

func printDecisionStartResultOnError(cmd *cobra.Command, result decision.StartResult) {
	if result.Context.Signal.TaskNumber == 0 && result.Context.Signal.Source == "" && result.Context.Signal.Kind == "" && !result.Ready && result.Decision == nil && result.Execution == nil && result.Context.Task.ID == "" {
		return
	}

	printDecisionStartResult(cmd, result)
}

func printDecisionStartResult(cmd *cobra.Command, result decision.StartResult) {
	task := result.Context.Task
	cmd.Printf("task=%d\nsignal-source=%s\nsignal-kind=%s\ncontext-ready=%t\n", result.Context.Signal.TaskNumber, result.Context.Signal.Source, result.Context.Signal.Kind, result.Ready)
	if result.Consideration != nil {
		if result.Consideration.Status != "" {
			cmd.Printf("consideration-status=%s\n", result.Consideration.Status)
		}
		if result.Consideration.Route.Name != "" {
			cmd.Printf("processing-route=%s\n", result.Consideration.Route.Name)
		}
		for _, check := range result.Consideration.Checks {
			if check.Name == "" && check.Status == "" {
				continue
			}
			cmd.Printf("route-check=%s:%s\n", check.Name, check.Status)
		}
	}
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
	}
	if result.ExecutionResult != nil {
		if result.ExecutionResult.Status != "" {
			cmd.Printf("execution-result-status=%s\n", result.ExecutionResult.Status)
		}
		if result.ExecutionResult.Action.Name != "" {
			cmd.Printf("execution-action=%s\n", result.ExecutionResult.Action.Name)
		}
		for _, operation := range result.ExecutionResult.Operations {
			if operation.Name == "" && operation.Status == "" {
				continue
			}
			cmd.Printf("execution-operation=%s:%s\n", operation.Name, operation.Status)
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
	if task.ID == "" && task.Title == "" && task.State == "" && task.URL == "" {
		return
	}

	cmd.Printf("issue-id=%s\nissue-title=%s\nissue-state=%s\n", task.ID, task.Title, task.State)
	if task.URL != "" {
		cmd.Printf("issue-url=%s\n", task.URL)
	}
}
