package workplace

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Prepare(_ context.Context, in model.Invocation, profile model.Profile, _ model.Allocation) (model.Workplace, error) {
	if in.Launch.Directory != "" {
		info, err := os.Stat(in.Launch.Directory)
		if err != nil {
			return model.Workplace{}, err
		}

		if !info.IsDir() {
			return model.Workplace{}, fmt.Errorf("execution directory is not a folder: %s", in.Launch.Directory)
		}

		return model.Workplace{Name: in.Launch.Directory, Ready: true}, nil
	}

	name := fmt.Sprintf("workspace/%s/%s", profile.Name, sanitizeName(in.Task))
	return model.Workplace{Name: name, Ready: true}, nil
}

func sanitizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	if value == "" {
		return "unnamed"
	}

	return value
}
