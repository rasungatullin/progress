package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const RunnerOpenCode = "opencode"

const DefaultCommitMessage = "Apply task result"

const structuredOutputStart = "<progress-structured-output>"

const structuredOutputEnd = "</progress-structured-output>"

type Service struct {
	runRunner    func(context.Context, model.Invocation) (string, error)
	runGitOutput func(context.Context, string, ...string) (string, error)
}

func NewService() *Service {
	return &Service{
		runRunner:    runRunner,
		runGitOutput: runGitOutput,
	}
}

func (s *Service) Launch(ctx context.Context, in model.Invocation, profile model.Profile, allocation model.Allocation, workplace model.Workplace) (model.LaunchResult, error) {
	if err := validateLaunch(in, workplace); err != nil {
		return model.LaunchResult{}, err
	}

	runnerOutput, err := s.runRunner(ctx, in)
	if err != nil {
		return model.LaunchResult{}, err
	}

	plainRunnerOutput, structuredOutput, hasStructuredOutput := parseStructuredOutput(runnerOutput)

	commitPush := in.Launch.CommitPush || profile.CommitPush

	gitSummary := "git=disabled"
	if commitPush {
		result, err := s.commitAndPush(ctx, in)
		if err != nil {
			return model.LaunchResult{}, err
		}

		gitSummary = result.summary()
	}

	summary := fmt.Sprintf(
		"profile=%s resource=%s workplace=%s runner=%s model=%s %s",
		profile.Name,
		allocation.Resource,
		workplace.Name,
		in.Launch.Runner,
		in.Launch.Model,
		gitSummary,
	)

	result := model.LaunchResult{Status: "completed", Summary: joinSummary(summary, plainRunnerOutput)}
	if hasStructuredOutput {
		result.CriticalRemarks = structuredOutput.CriticalRemarks
		result.MinorRemarks = structuredOutput.MinorRemarks
		result.Questions = structuredOutput.Questions
	}

	return result, nil
}

type structuredOutput struct {
	CriticalRemarks []string `json:"critical_remarks"`
	MinorRemarks    []string `json:"minor_remarks"`
	Questions       []string `json:"questions"`
}

func parseStructuredOutput(output string) (string, structuredOutput, bool) {
	start := strings.Index(output, structuredOutputStart)
	if start == -1 {
		return output, structuredOutput{}, false
	}

	end := strings.Index(output[start+len(structuredOutputStart):], structuredOutputEnd)
	if end == -1 {
		return output, structuredOutput{}, false
	}

	end += start + len(structuredOutputStart)
	rawPayload := strings.TrimSpace(output[start+len(structuredOutputStart) : end])
	if rawPayload == "" {
		return output, structuredOutput{}, false
	}

	var parsed structuredOutput
	if err := json.Unmarshal([]byte(rawPayload), &parsed); err != nil {
		return output, structuredOutput{}, false
	}

	plainOutput := strings.TrimSpace(output[:start] + output[end+len(structuredOutputEnd):])
	return plainOutput, parsed, true
}

func joinSummary(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		filtered = append(filtered, part)
	}

	return strings.Join(filtered, "\n")
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

type gitResult struct {
	status string
	branch string
}

func (r gitResult) summary() string {
	if r.branch == "" {
		return fmt.Sprintf("git=%s", r.status)
	}

	return fmt.Sprintf("git=%s branch=%s", r.status, r.branch)
}

func (s *Service) commitAndPush(ctx context.Context, in model.Invocation) (gitResult, error) {
	if !s.isGitRepository(ctx, in.Launch.Directory) {
		return gitResult{}, fmt.Errorf("launch directory is not a git repository")
	}

	branch, err := s.currentBranch(ctx, in.Launch.Directory)
	if err != nil {
		return gitResult{}, err
	}

	hasChanges, err := s.hasChanges(ctx, in.Launch.Directory)
	if err != nil {
		return gitResult{}, err
	}

	if !hasChanges {
		return gitResult{status: "no-changes", branch: branch}, nil
	}

	if _, err := s.runGitOutput(ctx, in.Launch.Directory, "add", "-A"); err != nil {
		return gitResult{}, fmt.Errorf("git add failed: %w", err)
	}

	hasChanges, err = s.hasChanges(ctx, in.Launch.Directory)
	if err != nil {
		return gitResult{}, err
	}

	if !hasChanges {
		return gitResult{status: "no-changes", branch: branch}, nil
	}

	commitMessage := strings.TrimSpace(in.Launch.CommitMessage)
	if commitMessage == "" {
		commitMessage = DefaultCommitMessage
	}

	if _, err := s.runGitOutput(ctx, in.Launch.Directory, "commit", "-m", commitMessage); err != nil {
		if isNoChangesAfterAddError(err) {
			return gitResult{status: "no-changes", branch: branch}, nil
		}

		return gitResult{}, fmt.Errorf("git commit failed: %w", err)
	}

	hasUpstream, err := s.hasUpstream(ctx, in.Launch.Directory, branch)
	if err != nil {
		return gitResult{}, err
	}

	pushArgs := []string{"push"}
	if !hasUpstream {
		pushArgs = append(pushArgs, "-u", "origin", branch)
	}

	if _, err := s.runGitOutput(ctx, in.Launch.Directory, pushArgs...); err != nil {
		return gitResult{}, fmt.Errorf("git push failed: %w", err)
	}

	return gitResult{status: "committed+pushed", branch: branch}, nil
}

func (s *Service) isGitRepository(ctx context.Context, dir string) bool {
	output, err := s.runGitOutput(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}

	return strings.TrimSpace(output) == "true"
}

func (s *Service) currentBranch(ctx context.Context, dir string) (string, error) {
	output, err := s.runGitOutput(ctx, dir, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("resolve current git branch: %w", err)
	}

	branch := strings.TrimSpace(output)
	if branch == "" {
		return "", fmt.Errorf("resolve current git branch: branch is empty")
	}

	return branch, nil
}

func (s *Service) hasChanges(ctx context.Context, dir string) (bool, error) {
	output, err := s.runGitOutput(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("inspect git changes: %w", err)
	}

	return strings.TrimSpace(output) != "", nil
}

func (s *Service) hasUpstream(ctx context.Context, dir, branch string) (bool, error) {
	output, err := s.runGitOutput(ctx, dir, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch)
	if err != nil {
		return false, fmt.Errorf("resolve git upstream: %w", err)
	}

	return strings.TrimSpace(output) != "", nil
}

func isNoChangesAfterAddError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "nothing to commit") || strings.Contains(message, "no changes added to commit")
}

func runRunner(ctx context.Context, in model.Invocation) (string, error) {
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
		return "", fmt.Errorf("launch runner failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}

func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
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
