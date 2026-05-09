package resources

import (
	"context"

	"github.com/rasungatullin/progress/internal/execution/model"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Allocate(_ context.Context, _ model.Invocation, profile model.Profile) (model.Allocation, error) {
	return model.Allocation{Resource: "local-slot:" + profile.Name, Reserved: true}, nil
}
