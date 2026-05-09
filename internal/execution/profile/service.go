package profile

import (
	"context"

	"github.com/rasungatullin/progress/internal/execution/model"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Resolve(_ context.Context, _ model.Invocation) (model.Profile, error) {
	return model.Profile{Name: "local-default", Mode: "manual"}, nil
}
