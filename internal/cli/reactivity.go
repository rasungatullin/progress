package cli

import (
	"context"
	"strings"

	"github.com/rasungatullin/progress/internal/logging"
	"github.com/rasungatullin/progress/internal/reactivity"
	"github.com/spf13/cobra"
)

type reactivityFlags struct {
	task      int
	route     string
	action    string
	once      bool
	maxCycles int
}

type reactivityCommandService interface {
	ProcessTask(context.Context, reactivity.TaskProcessingInput) (reactivity.TaskProcessingResult, error)
	RunTaskAction(context.Context, reactivity.TaskActionInput) (reactivity.TaskProcessingResult, error)
}

type reactivityServiceFactoryFunc func(*cobra.Command) reactivityCommandService

type reactivityServiceFactoryContextKey struct{}

var reactivityServiceFactory = func(cmd *cobra.Command) reactivityCommandService {
	return reactivity.NewService(logging.New(cmd.ErrOrStderr()))
}

func setReactivityServiceFactory(cmd *cobra.Command, factory reactivityServiceFactoryFunc) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cmd.SetContext(context.WithValue(ctx, reactivityServiceFactoryContextKey{}, factory))
}

func newReactivityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reactivity",
		Short: "Контур реакции на внешние события",
	}

	cmd.AddCommand(
		newReactivityProcessCommand(),
		newReactivityActionCommand(),
	)
	return cmd
}

func newReactivityProcessCommand() *cobra.Command {
	flags := &reactivityFlags{}

	cmd := &cobra.Command{
		Use:   "process",
		Short: "Обработка указанной задачи",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newReactivityService(cmd)
			result, err := service.ProcessTask(cmd.Context(), reactivity.TaskProcessingInput{
				TaskNumber: flags.task,
				Route:      flags.route,
				Once:       flags.once,
				MaxCycles:  flags.maxCycles,
			})
			if err != nil {
				printReactivityResultOnError(cmd, result)
				return err
			}
			printReactivityResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().IntVar(&flags.task, "task", 0, "Номер задачи для обработки")
	cmd.Flags().StringVar(&flags.route, "route", "", "Имя маршрута обработки")
	cmd.Flags().BoolVar(&flags.once, "once", false, "Выполнить только один цикл обработки")
	cmd.Flags().IntVar(&flags.maxCycles, "max-cycles", 0, "Максимальное число циклов обработки")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func newReactivityActionCommand() *cobra.Command {
	flags := &reactivityFlags{}

	cmd := &cobra.Command{
		Use:   "action",
		Short: "Запуск указанного действия для задачи",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service := newReactivityService(cmd)
			result, err := service.RunTaskAction(cmd.Context(), reactivity.TaskActionInput{
				TaskNumber: flags.task,
				Action:     flags.action,
			})
			if err != nil {
				printReactivityResultOnError(cmd, result)
				return err
			}
			printReactivityResult(cmd, result)
			return nil
		},
	}

	cmd.Flags().IntVar(&flags.task, "task", 0, "Номер задачи для запуска действия")
	cmd.Flags().StringVar(&flags.action, "action", "", "Действие контура исполнения")
	_ = cmd.MarkFlagRequired("task")
	_ = cmd.MarkFlagRequired("action")
	return cmd
}

func newReactivityService(cmd *cobra.Command) reactivityCommandService {
	if factory, ok := cmd.Context().Value(reactivityServiceFactoryContextKey{}).(reactivityServiceFactoryFunc); ok && factory != nil {
		return factory(cmd)
	}

	return reactivityServiceFactory(cmd)
}

func printReactivityResultOnError(cmd *cobra.Command, result reactivity.TaskProcessingResult) {
	if result.TaskNumber == 0 && len(result.Cycles) == 0 && result.StopReason == "" && result.FinalIssue == nil {
		return
	}
	printReactivityResult(cmd, result)
}

func printReactivityResult(cmd *cobra.Command, result reactivity.TaskProcessingResult) {
	cmd.Printf("task=%d\ncompleted=%t\n", result.TaskNumber, result.Completed)
	if result.StopReason != "" {
		cmd.Printf("stop-reason=%s\n", result.StopReason)
	}
	for _, cycle := range result.Cycles {
		cmd.Printf("cycle=%d\n", cycle.Index)
		if cycle.Issue != nil {
			cmd.Printf("cycle-issue-number=%d\ncycle-issue-title=%s\ncycle-issue-state=%s\n", cycle.Issue.Number, cycle.Issue.Title, cycle.Issue.State)
			if len(cycle.Issue.Labels) != 0 {
				cmd.Printf("cycle-issue-labels=%s\n", strings.Join(cycle.Issue.Labels, ","))
			}
		}
		if cycle.MergeRequest != nil {
			cmd.Printf("cycle-merge-request=%d\ncycle-merge-request-head=%s\n", cycle.MergeRequest.Number, cycle.MergeRequest.HeadRef)
		}
		if cycle.Consideration != nil {
			cmd.Printf("cycle-consideration-status=%s\n", cycle.Consideration.Status)
			if cycle.Consideration.Route.Name != "" {
				cmd.Printf("cycle-processing-route=%s\n", cycle.Consideration.Route.Name)
			}
		}
		if cycle.Action != "" {
			cmd.Printf("cycle-action=%s\n", cycle.Action)
		}
		if cycle.ExecutionResult != nil {
			cmd.Printf("cycle-execution-result-status=%s\n", cycle.ExecutionResult.Status)
			if cycle.ExecutionResult.Action.Name != "" {
				cmd.Printf("cycle-execution-action=%s\n", cycle.ExecutionResult.Action.Name)
			}
		}
		if cycle.Execution != nil && cycle.Execution.Status != "" {
			cmd.Printf("cycle-execution-status=%s\n", cycle.Execution.Status)
		}
		if cycle.ReviewPassed != nil {
			cmd.Printf("cycle-review-passed=%t\n", *cycle.ReviewPassed)
		}
		for _, change := range cycle.LabelChanges {
			if len(change.Labels) == 0 {
				continue
			}
			cmd.Printf("cycle-label-%s=%s\n", change.Operation, strings.Join(change.Labels, ","))
		}
	}
	if result.FinalIssue != nil {
		cmd.Printf("final-issue-number=%d\n", result.FinalIssue.Number)
		if len(result.FinalIssue.Labels) != 0 {
			cmd.Printf("final-issue-labels=%s\n", strings.Join(result.FinalIssue.Labels, ","))
		}
	}
}
