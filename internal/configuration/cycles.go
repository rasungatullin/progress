package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const executionCycleLocalFilePath = ".progress/execution/cycles.json"

func LoadExecutionCycleConfig(repoRoot string, readFile ReadFileFunc) (model.CycleConfig, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}

	configPath := filepath.Join(repoRoot, executionCycleLocalFilePath)
	content, err := readFile(configPath)
	if err != nil {
		if isNotExistErr(err) {
			return model.CycleConfig{}, fmt.Errorf("execution cycle config not found: %s", configPath)
		}
		return model.CycleConfig{}, fmt.Errorf("read execution cycle config %s: %w", configPath, err)
	}

	var config model.CycleConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return model.CycleConfig{}, fmt.Errorf("parse execution cycle config %s: %w", configPath, err)
	}
	if err := validateCycleConfig(config); err != nil {
		return model.CycleConfig{}, fmt.Errorf("invalid execution cycle config %s: %w", configPath, err)
	}

	return config, nil
}

func validateCycleConfig(config model.CycleConfig) error {
	if len(config.Cycles) == 0 {
		return fmt.Errorf("cycles must define at least one cycle")
	}

	for cycleName, definition := range config.Cycles {
		cycleName = strings.TrimSpace(cycleName)
		if cycleName == "" {
			return fmt.Errorf("cycle name must be non-empty")
		}
		if err := validateCycleDefinition(cycleName, definition); err != nil {
			return fmt.Errorf("invalid cycle %q: %w", cycleName, err)
		}
	}

	return nil
}

func validateCycleDefinition(cycleName string, definition model.CycleDefinition) error {
	startStep := strings.TrimSpace(definition.StartStep)
	if startStep == "" {
		return fmt.Errorf("start_step must be non-empty")
	}

	if len(definition.Steps) == 0 {
		return fmt.Errorf("cycle must contain at least one step")
	}

	if definition.Limits.MaxExecutions < 0 {
		return fmt.Errorf("limits.max_executions must be non-negative")
	}

	stepMap := make(map[string]model.CycleStep, len(definition.Steps))
	for _, step := range definition.Steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			return fmt.Errorf("step name must be non-empty")
		}
		if _, ok := stepMap[name]; ok {
			return fmt.Errorf("duplicate step name %q", name)
		}
		if strings.TrimSpace(step.Profile) == "" {
			return fmt.Errorf("step %q profile must be non-empty", name)
		}
		stepMap[name] = step

		if len(step.Transitions) == 0 {
			continue
		}
		for _, transition := range step.Transitions {
			to := strings.TrimSpace(transition.To)
			finish := strings.TrimSpace(transition.Finish)
			hasTo := to != ""
			hasFinish := finish != ""
			if hasTo == hasFinish {
				return fmt.Errorf("step %q transition must define exactly one target action: to or finish", name)
			}
			hasIn := len(transition.In) != 0
			hasNotIn := len(transition.NotIn) != 0
			if !hasIn && !hasNotIn && !transition.Missing {
				return fmt.Errorf("step %q transition must define at least one matcher", name)
			}
		}
	}

	if _, ok := stepMap[startStep]; !ok {
		return fmt.Errorf("start_step %q not found", startStep)
	}

	for _, step := range definition.Steps {
		for _, transition := range step.Transitions {
			to := strings.TrimSpace(transition.To)
			if to == "" {
				continue
			}
			if _, ok := stepMap[to]; !ok {
				return fmt.Errorf("step %q has transition to unknown step %q", step.Name, to)
			}
		}
	}

	return nil
}
