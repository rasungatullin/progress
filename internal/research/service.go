package research

import (
	"context"
	"fmt"
	"strings"
)

type Hypothesis struct {
	Name        string
	Description string
}

type Variant struct {
	Name        string
	Methodology string
}

type Experiment struct {
	Name       string
	Hypothesis Hypothesis
	TaskBank   string
	Variants   []Variant
	Metrics    []string
}

type Plan struct {
	Experiment  string
	Steps       []string
	Diagnostics []string
}

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Plan(ctx context.Context, experiment Experiment) (Plan, error) {
	_ = ctx
	_ = s

	experiment.Name = strings.TrimSpace(experiment.Name)
	experiment.Hypothesis.Name = strings.TrimSpace(experiment.Hypothesis.Name)
	if experiment.Name == "" {
		return Plan{}, fmt.Errorf("имя эксперимента должно быть непустым")
	}
	if experiment.Hypothesis.Name == "" {
		return Plan{}, fmt.Errorf("имя гипотезы исследования должно быть непустым")
	}
	if len(experiment.Variants) == 0 {
		return Plan{}, fmt.Errorf("эксперимент должен содержать хотя бы один вариант")
	}

	steps := []string{
		"зафиксировать гипотезу исследования",
		"подготовить банк задач",
		"выполнить сравнительные прогоны вариантов",
		"рассчитать показатели аналитики",
		"сформировать заключение о применимости изменения",
	}
	diagnostics := []string{
		fmt.Sprintf("hypothesis=%s", experiment.Hypothesis.Name),
		fmt.Sprintf("variants=%d", len(experiment.Variants)),
	}
	if strings.TrimSpace(experiment.TaskBank) != "" {
		diagnostics = append(diagnostics, fmt.Sprintf("task-bank=%s", strings.TrimSpace(experiment.TaskBank)))
	}

	return Plan{Experiment: experiment.Name, Steps: steps, Diagnostics: diagnostics}, nil
}
