package launch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const RunnerOpenCode = "opencode"

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Launch(ctx context.Context, in model.Invocation, profile model.Profile, allocation model.Allocation, workplace model.Workplace) (model.LaunchResult, error) {
	if err := validateLaunch(in, workplace); err != nil {
		return model.LaunchResult{}, err
	}

	args := []string{
		"run",
		"--dir", in.Launch.Directory,
		"--model", in.Launch.Model,
		in.Launch.Prompt,
	}

	cmd := exec.CommandContext(ctx, in.Launch.Runner, args...)
	cmd.Dir = in.Launch.Directory
	cmd.Env = sanitizedEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return model.LaunchResult{}, fmt.Errorf("launch runner failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	summary := fmt.Sprintf(
		"profile=%s resource=%s workplace=%s runner=%s model=%s",
		profile.Name,
		allocation.Resource,
		workplace.Name,
		in.Launch.Runner,
		in.Launch.Model,
	)

	return model.LaunchResult{Status: "completed", Summary: summary + "\n" + strings.TrimSpace(string(output))}, nil
}

func validateLaunch(in model.Invocation, workplace model.Workplace) error {
	if !workplace.Ready {
		return fmt.Errorf("workplace is not ready")
	}

	if strings.TrimSpace(in.Launch.Directory) == "" {
		return fmt.Errorf("launch directory is required")
	}

	if strings.TrimSpace(in.Launch.Prompt) == "" {
		return fmt.Errorf("launch prompt is required")
	}

	if strings.TrimSpace(in.Launch.Runner) != RunnerOpenCode {
		return fmt.Errorf("unsupported runner: %s", in.Launch.Runner)
	}

	if strings.TrimSpace(in.Launch.Model) == "" {
		return fmt.Errorf("launch model is required")
	}

	info, err := os.Stat(in.Launch.Directory)
	if err != nil {
		return fmt.Errorf("launch directory is unavailable: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("launch directory is not a folder: %s", filepath.Clean(in.Launch.Directory))
	}

	return nil
}

func sanitizedEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))

	for _, entry := range env {
		if shouldDropEnv(entry) {
			continue
		}

		filtered = append(filtered, entry)
	}

	return filtered
}

func shouldDropEnv(entry string) bool {
	prefixes := []string{
		"OPENCODE_",
		"OPENCHAMBER_",
		"AGENT=",
		"OPENCODE=",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}

	return false
}
