package taskbank

import (
	"context"
	"fmt"
	"strings"
)

type Case struct {
	ID             string
	Title          string
	Input          map[string]string
	ExpectedResult string
	Tags           []string
}

type Bank struct {
	Name  string
	Cases []Case
}

type Query struct {
	Tags  []string
	Limit int
}

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Select(ctx context.Context, bank Bank, query Query) ([]Case, error) {
	_ = ctx
	_ = s

	if strings.TrimSpace(bank.Name) == "" {
		return nil, fmt.Errorf("имя банка задач должно быть непустым")
	}

	var selected []Case
	for _, taskCase := range bank.Cases {
		if strings.TrimSpace(taskCase.ID) == "" {
			return nil, fmt.Errorf("идентификатор задачи банка должен быть непустым")
		}
		if !hasAllTags(taskCase.Tags, query.Tags) {
			continue
		}
		selected = append(selected, cloneCase(taskCase))
		if query.Limit > 0 && len(selected) >= query.Limit {
			break
		}
	}
	return selected, nil
}

func hasAllTags(values []string, required []string) bool {
	if len(required) == 0 {
		return true
	}

	seen := map[string]struct{}{}
	for _, value := range values {
		seen[strings.TrimSpace(value)] = struct{}{}
	}
	for _, tag := range required {
		if _, ok := seen[strings.TrimSpace(tag)]; !ok {
			return false
		}
	}
	return true
}

func cloneCase(in Case) Case {
	out := in
	out.Tags = append([]string(nil), in.Tags...)
	if len(in.Input) > 0 {
		out.Input = make(map[string]string, len(in.Input))
		for key, value := range in.Input {
			out.Input[key] = value
		}
	}
	return out
}
