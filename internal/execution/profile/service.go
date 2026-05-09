package profile

import (
	"context"
	"fmt"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const (
	ProfileDefault = "default"
	ProfileLocal   = "local"

	defaultModel = "openai/gpt-5.4"
	localModel   = "ollama/qwen3.5:2b"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Resolve(_ context.Context, in model.Invocation) (model.Profile, error) {
	switch strings.TrimSpace(in.Profile) {
	case "", ProfileDefault:
		return model.Profile{Name: ProfileDefault, Mode: "manual", Model: defaultModel, CommitPush: false}, nil
	case ProfileLocal:
		return model.Profile{Name: ProfileLocal, Mode: "manual", Model: localModel, CommitPush: false}, nil
	default:
		return model.Profile{}, fmt.Errorf("unknown execution profile: %s", in.Profile)
	}
}
