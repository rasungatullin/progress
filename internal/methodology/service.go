package methodology

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
)

type Catalog struct {
	Routes       []Route
	Actions      []Action
	Instructions []Instruction
}

type Route struct {
	Name        string
	Title       string
	Action      string
	Profile     string
	Description string
	Checks      []string
}

type Action struct {
	Name        string
	Class       string
	Profile     string
	Operations  []string
	Description string
}

type Instruction struct {
	Name    string
	Profile string
	Body    string
}

type SelectionRequest struct {
	Route   string
	Action  string
	Profile string
}

type SelectionResult struct {
	Route       Route
	Action      Action
	Instruction Instruction
	Diagnostics []string
}

type Service struct {
	logger *log.Logger
}

func NewService(logger *log.Logger) *Service {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	return &Service{logger: logger}
}

func (s *Service) Select(ctx context.Context, catalog Catalog, request SelectionRequest) (SelectionResult, error) {
	_ = ctx

	if s == nil {
		s = NewService(nil)
	}

	route, err := selectRoute(catalog.Routes, request.Route)
	if err != nil {
		return SelectionResult{}, err
	}

	actionName := firstNonEmpty(request.Action, route.Action)
	action, err := selectAction(catalog.Actions, actionName)
	if err != nil {
		return SelectionResult{}, err
	}

	profile := firstNonEmpty(request.Profile, action.Profile, route.Profile)
	instruction := selectInstruction(catalog.Instructions, profile)
	result := SelectionResult{
		Route:       route,
		Action:      action,
		Instruction: instruction,
		Diagnostics: []string{
			fmt.Sprintf("route=%s", route.Name),
			fmt.Sprintf("action=%s", action.Name),
			fmt.Sprintf("profile=%s", profile),
		},
	}
	if instruction.Name != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("instruction=%s", instruction.Name))
	} else if profile != "" {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("instruction-missing-for-profile=%s", profile))
	}

	s.logger.Printf("Контур методик выбрал маршрут: маршрут=%q действие=%q профиль=%q", route.Name, action.Name, profile)
	return result, nil
}

func selectRoute(routes []Route, name string) (Route, error) {
	name = strings.TrimSpace(name)
	for _, route := range routes {
		route.Name = strings.TrimSpace(route.Name)
		if route.Name == "" {
			continue
		}
		if name == "" || route.Name == name {
			return route, nil
		}
	}
	if name == "" {
		return Route{}, fmt.Errorf("каталог методик не содержит маршруты")
	}
	return Route{}, fmt.Errorf("маршрут методики %q не найден", name)
}

func selectAction(actions []Action, name string) (Action, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Action{}, fmt.Errorf("действие методики должно быть задано")
	}
	for _, action := range actions {
		action.Name = strings.TrimSpace(action.Name)
		if action.Name == name {
			return action, nil
		}
	}
	return Action{}, fmt.Errorf("действие методики %q не найдено", name)
}

func selectInstruction(instructions []Instruction, profile string) Instruction {
	profile = strings.TrimSpace(profile)
	for _, instruction := range instructions {
		if strings.TrimSpace(instruction.Profile) == profile {
			return instruction
		}
	}
	return Instruction{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
