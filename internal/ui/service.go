package ui

import (
	"context"
	"fmt"
	"strings"
)

const (
	SectionTasks       = "tasks"
	SectionRoutes      = "routes"
	SectionRuns        = "runs"
	SectionSettings    = "settings"
	SectionDiagnostics = "diagnostics"
)

type ViewRequest struct {
	Section   string
	Workspace string
}

type ViewModel struct {
	Section string
	Title   string
	Panels  []Panel
	Actions []Action
}

type Panel struct {
	Name  string
	Title string
}

type Action struct {
	Name  string
	Title string
}

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) BuildView(ctx context.Context, request ViewRequest) (ViewModel, error) {
	_ = ctx
	_ = s

	section := strings.TrimSpace(request.Section)
	if section == "" {
		section = SectionRuns
	}

	title, ok := sectionTitles()[section]
	if !ok {
		return ViewModel{}, fmt.Errorf("раздел пользовательского интерфейса %q не поддерживается", section)
	}

	view := ViewModel{
		Section: section,
		Title:   title,
		Panels: []Panel{
			{Name: "summary", Title: "Сводка"},
			{Name: section, Title: title},
		},
		Actions: []Action{
			{Name: "refresh", Title: "Обновить сведения"},
		},
	}
	if strings.TrimSpace(request.Workspace) != "" {
		view.Panels = append(view.Panels, Panel{Name: "workspace", Title: "Рабочая область"})
	}

	return view, nil
}

func sectionTitles() map[string]string {
	return map[string]string{
		SectionTasks:       "Задачи",
		SectionRoutes:      "Маршруты обработки",
		SectionRuns:        "Запуски",
		SectionSettings:    "Настройки и ресурсы",
		SectionDiagnostics: "Диагностика",
	}
}
