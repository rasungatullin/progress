package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/rasungatullin/progress/internal/execution/model"
)

const RunnerOpenCode = "opencode"

const RunnerCodex = "codex"

const DefaultCommitMessage = "Apply task result"

const structuredOutputStart = "<progress-structured-output>"

const structuredOutputEnd = "</progress-structured-output>"

const structuredInputStart = "<progress-structured-input>"

const structuredInputEnd = "</progress-structured-input>"

const runnerOutputExcludePathspec = ":(exclude).progress/runner-output"

type trailingStructuredBlockState int

const (
	trailingStructuredBlockAbsent trailingStructuredBlockState = iota
	trailingStructuredBlockValid
	trailingStructuredBlockInvalid
)

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
	var err error
	in, err = prepareInvocation(in)
	if err != nil {
		return model.LaunchResult{}, err
	}
	in.Launch = applyProfileStructuredOutput(in.Launch, profile)

	if err := validateLaunch(in, workplace); err != nil {
		return model.LaunchResult{}, err
	}

	runnerOutput, err := s.runRunner(ctx, in)
	if err != nil {
		return model.LaunchResult{}, err
	}
	rawOutputPath := persistRunnerOutput(workplace.Name, runnerOutput)

	plainRunnerOutput, rawStructuredOutput, structuredOutput, structuredOutputState, structuredOutputErr := parseStructuredOutput(runnerOutput)
	if err := validateStructuredOutputRequirement(in.Launch, rawStructuredOutput, structuredOutputState, structuredOutputErr); err != nil {
		return model.LaunchResult{Status: "failed", Summary: strings.TrimSpace(plainRunnerOutput), RawOutputPath: rawOutputPath}, err
	}

	commitPush := in.Launch.CommitPush || profile.CommitPush

	gitSummary := "git=disabled"
	if commitPush {
		result, err := s.commitAndPush(ctx, in, workplace, structuredOutput)
		if err != nil {
			launchResult := model.LaunchResult{
				Status:        "failed",
				Summary:       strings.TrimSpace(plainRunnerOutput),
				RawOutputPath: rawOutputPath,
			}
			if structuredOutputState == trailingStructuredBlockValid {
				launchResult.StructuredOutput = structuredOutput
			}
			return launchResult, err
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

	result := model.LaunchResult{Status: "completed", Summary: buildLaunchSummary(summary, plainRunnerOutput, structuredOutputState, structuredOutput), RawOutputPath: rawOutputPath}
	if structuredOutputState == trailingStructuredBlockValid {
		result.StructuredOutput = structuredOutput
	}

	return result, nil
}

func parseStructuredOutput(output string) (string, string, *model.StructuredOutput, trailingStructuredBlockState, error) {
	plainOutput, rawPayload, state, err := extractTrailingStructuredBlock(output, structuredOutputStart, structuredOutputEnd, func(rawPayload string) error {
		_, err := parseStructuredOutputPayload(rawPayload)
		return err
	})
	if state != trailingStructuredBlockValid {
		return output, rawPayload, nil, state, err
	}

	parsed, err := parseStructuredOutputPayload(rawPayload)
	if err != nil {
		return output, rawPayload, nil, trailingStructuredBlockInvalid, err
	}

	return plainOutput, rawPayload, parsed, trailingStructuredBlockValid, nil
}

func parseStructuredInput(prompt string) (string, *model.StructuredInput, trailingStructuredBlockState, error) {
	plainPrompt, rawPayload, state, err := extractTrailingStructuredBlock(prompt, structuredInputStart, structuredInputEnd, func(rawPayload string) error {
		_, err := parseStructuredInputPayload(rawPayload)
		return err
	})
	if state != trailingStructuredBlockValid {
		if err == nil && strings.TrimSpace(rawPayload) != "" {
			_, err = parseStructuredInputPayload(rawPayload)
		}
		return prompt, nil, state, err
	}

	parsed, err := parseStructuredInputPayload(rawPayload)
	if err != nil {
		return prompt, nil, trailingStructuredBlockInvalid, err
	}
	if parsed == nil {
		return prompt, nil, trailingStructuredBlockInvalid, fmt.Errorf("payload is empty")
	}

	return plainPrompt, parsed, trailingStructuredBlockValid, nil
}

func extractTrailingStructuredBlock(text, startTag, endTag string, validatePayload func(string) error) (string, string, trailingStructuredBlockState, error) {
	trimmedText := strings.TrimRightFunc(text, unicode.IsSpace)
	if !strings.HasSuffix(trimmedText, endTag) {
		return text, "", trailingStructuredBlockAbsent, nil
	}

	end := len(trimmedText) - len(endTag)
	searchEnd := end
	foundCandidate := false
	firstInvalidPayload := ""
	var firstInvalidErr error
	for {
		start := strings.LastIndex(trimmedText[:searchEnd], startTag)
		if start == -1 {
			if foundCandidate {
				return text, firstInvalidPayload, trailingStructuredBlockInvalid, firstInvalidErr
			}

			return text, "", trailingStructuredBlockAbsent, nil
		}

		foundCandidate = true
		rawPayload := strings.TrimSpace(trimmedText[start+len(startTag) : end])
		if rawPayload != "" {
			if err := validatePayload(rawPayload); err == nil {
				plainText := strings.TrimSpace(trimmedText[:start])
				return plainText, rawPayload, trailingStructuredBlockValid, nil
			} else if firstInvalidErr == nil {
				firstInvalidPayload = rawPayload
				firstInvalidErr = err
			}
		} else if firstInvalidErr == nil {
			firstInvalidErr = fmt.Errorf("payload is empty")
		}

		searchEnd = start
	}
}

func parseStructuredInputPayload(rawPayload string) (*model.StructuredInput, error) {
	var payload model.StructuredInput
	if err := decodeJSONStrict(rawPayload, &payload); err != nil {
		return nil, err
	}

	return canonicalizeStructuredInput(payload)
}

func parseStructuredOutputPayload(rawPayload string) (*model.StructuredOutput, error) {
	var payload model.StructuredOutput
	if err := decodeJSONStrict(rawPayload, &payload); err != nil {
		return nil, err
	}

	if err := validateStructuredOutputPayload(payload); err != nil {
		return nil, err
	}

	return &payload, nil
}

func normalizeStructuredInput(payload model.StructuredInput) model.StructuredInput {
	if payload.ProtocolVersion == "" && hasStructuredInputContent(payload) {
		payload.ProtocolVersion = model.StructuredIOVersion
	}

	return payload
}

func hasStructuredInput(payload model.StructuredInput) bool {
	return payload.ProtocolVersion != "" || payload.Task != "" || len(payload.Constraints) != 0 || len(payload.ProjectContext) != 0 || len(payload.OperationalContext) != 0 || len(payload.PreviousRunResults) != 0 || len(payload.ReviewRemarks) != 0 || len(payload.ReviewResponses) != 0 || len(payload.IntegrationActions) != 0 || len(payload.Extensions) != 0
}

func hasStructuredInputContent(payload model.StructuredInput) bool {
	return payload.Task != "" || len(payload.Constraints) != 0 || len(payload.ProjectContext) != 0 || len(payload.OperationalContext) != 0 || len(payload.PreviousRunResults) != 0 || len(payload.ReviewRemarks) != 0 || len(payload.ReviewResponses) != 0 || len(payload.IntegrationActions) != 0 || len(payload.Extensions) != 0
}

func canonicalizeStructuredInput(payload model.StructuredInput) (*model.StructuredInput, error) {
	payload = normalizeStructuredInput(payload)
	if err := validateStructuredInputPayload(payload); err != nil {
		return nil, err
	}

	return &payload, nil
}

func validateStructuredInputPayload(payload model.StructuredInput) error {
	if payload.ProtocolVersion != "" && payload.ProtocolVersion != model.StructuredIOVersion {
		return fmt.Errorf("structured input must set protocol_version=%q", model.StructuredIOVersion)
	}
	if !hasStructuredInputContent(payload) {
		return fmt.Errorf("structured input must include at least one non-empty field besides protocol_version")
	}
	for index, value := range payload.Constraints {
		if strings.TrimSpace(value) != "" {
			continue
		}

		return fmt.Errorf("structured input constraints[%d] must be non-empty", index)
	}
	for index, value := range payload.ProjectContext {
		if hasNonEmptyStructuredField(value.Title, value.Body) {
			continue
		}

		return fmt.Errorf("structured input project_context[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.OperationalContext {
		if hasNonEmptyStructuredField(value.Title, value.Body) {
			continue
		}

		return fmt.Errorf("structured input operational_context[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.PreviousRunResults {
		if hasNonEmptyStructuredField(value.Summary, value.Body) {
			continue
		}

		return fmt.Errorf("structured input previous_run_results[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.ReviewRemarks {
		if hasNonEmptyStructuredField(value.ID, value.Status, value.Severity, value.Type, value.Title, value.Body, value.Answer, value.Resolution) {
			continue
		}

		return fmt.Errorf("structured input review_remarks[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.ReviewResponses {
		if hasNonEmptyStructuredField(value.ID, value.RemarkID, value.Status, value.Summary, value.Body) {
			continue
		}

		return fmt.Errorf("structured input review_responses[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.IntegrationActions {
		if hasNonEmptyStructuredField(value.ID, value.Status, value.Type, value.Title, value.Body) {
			continue
		}

		return fmt.Errorf("structured input integration_actions[%d] must include at least one non-empty field", index)
	}

	return nil
}

func validateStructuredOutputPayload(payload model.StructuredOutput) error {
	if payload.ProtocolVersion != model.StructuredIOVersion {
		return fmt.Errorf("structured output must set protocol_version=%q", model.StructuredIOVersion)
	}
	if strings.TrimSpace(payload.Summary) == "" {
		return fmt.Errorf("structured output must include a non-empty summary")
	}
	for index, value := range payload.Remarks {
		if hasNonEmptyStructuredField(value.ID, value.Status, value.Severity, value.Type, value.Title, value.Body, value.Answer, value.Resolution) {
			continue
		}

		return fmt.Errorf("structured output remarks[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.Questions {
		if hasNonEmptyStructuredField(value.ID, value.Status, value.Title, value.Body, value.Answer) {
			continue
		}

		return fmt.Errorf("structured output questions[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.FollowUpActions {
		if hasNonEmptyStructuredField(value.ID, value.Status, value.Type, value.Title, value.Body) {
			continue
		}

		return fmt.Errorf("structured output follow_up_actions[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.Changes {
		if hasNonEmptyStructuredField(value.Summary) {
			continue
		}

		return fmt.Errorf("structured output changes[%d] must include a non-empty summary", index)
	}
	for index, value := range payload.Commands {
		if hasNonEmptyStructuredField(value.Name, value.Title, value.Body) || len(value.Args) != 0 {
			continue
		}

		return fmt.Errorf("structured output commands[%d] must include at least one non-empty field", index)
	}
	if payload.Conclusion != nil && !hasNonEmptyStructuredField(payload.Conclusion.Status, payload.Conclusion.Summary, payload.Conclusion.Body) {
		return fmt.Errorf("structured output conclusion must include at least one non-empty field")
	}

	return nil
}

func prepareInvocation(in model.Invocation) (model.Invocation, error) {
	plainPrompt, structuredInput, err := NormalizeStructuredInput(in.Launch.Prompt, in.Launch.StructuredInput)
	if err != nil {
		return model.Invocation{}, err
	}

	in.Launch.Prompt = plainPrompt
	in.Launch.StructuredInput = structuredInput
	return in, nil
}

func NormalizeStructuredInput(prompt string, input *model.StructuredInput) (string, *model.StructuredInput, error) {
	plainPrompt, structuredInput, structuredInputState, structuredInputErr := parseStructuredInput(prompt)
	switch structuredInputState {
	case trailingStructuredBlockValid:
		prompt = plainPrompt
		if input == nil {
			input = structuredInput
		}
	case trailingStructuredBlockInvalid:
		if structuredInputErr != nil {
			return "", nil, fmt.Errorf("invalid structured input: %w", structuredInputErr)
		}
		return "", nil, fmt.Errorf("invalid structured input: trailing %s block is invalid", structuredInputStart)
	default:
		if input == nil {
			return prompt, nil, nil
		}
	}

	if input == nil {
		return prompt, nil, nil
	}

	canonical, err := canonicalizeStructuredInput(*input)
	if err != nil {
		return "", nil, fmt.Errorf("invalid structured input: %w", err)
	}

	return prompt, canonical, nil
}

func validateStructuredOutputRequirement(spec model.LaunchSpec, rawPayload string, state trailingStructuredBlockState, parseErr error) error {
	if !spec.StructuredOutputRequired {
		return nil
	}

	switch state {
	case trailingStructuredBlockValid:
		if err := validateRequiredStructuredPayload(spec, rawPayload); err != nil {
			return fmt.Errorf("structured output is required but %w", err)
		}
		return nil
	case trailingStructuredBlockInvalid:
		if parseErr == nil && strings.TrimSpace(rawPayload) != "" {
			_, parseErr = parseStructuredOutputPayload(rawPayload)
		}
		if parseErr != nil {
			return fmt.Errorf("structured output is required but payload does not match structured output schema: %w", parseErr)
		}
		return fmt.Errorf("structured output is required but trailing %s block is invalid", structuredOutputStart)
	default:
		return fmt.Errorf("structured output is required but trailing %s block is missing", structuredOutputStart)
	}
}

func validateRequiredStructuredPayload(_ model.LaunchSpec, rawPayload string) error {
	_, err := parseStructuredOutputPayload(rawPayload)
	if err != nil {
		return fmt.Errorf("payload does not match structured output schema: %w", err)
	}

	return nil
}

func hasNonEmptyStructuredField(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}

	return false
}

func decodeJSONStrict(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return formatStrictJSONError(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON tokens")
		}
		return formatStrictJSONError(err)
	}

	return nil
}

func formatStrictJSONError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := strings.TrimSpace(typeErr.Field)
		if field == "" {
			field = "(root)"
		}
		if expectedShape, ok := strictExpectedShapeForField(field); ok {
			return fmt.Errorf("type mismatch at %s: expected %s but got %s", field, expectedShape, typeErr.Value)
		}
		return fmt.Errorf("type mismatch at %s: expected %s but got %s", field, typeErr.Type, typeErr.Value)
	}

	return err
}

func strictExpectedShapeForField(field string) (string, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return "", false
	}

	segment := field
	if dot := strings.Index(segment, "."); dot >= 0 {
		segment = segment[:dot]
	}
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "", false
	}

	switch strings.ToLower(segment) {
	case "remarks":
		return "array of objects with id/status/severity/type/title/body/answer/resolution", true
	case "questions":
		return "array of objects with id/status/title/body/answer", true
	case "follow_up_actions":
		return "array of objects with id/status/type/title/body", true
	case "changes":
		return "array of objects with summary", true
	case "commands":
		return "array of objects with name/args/title/body", true
	case "conclusion":
		return "object with status/summary/body", true
	default:
		return "", false
	}
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

func buildLaunchSummary(baseSummary, plainRunnerOutput string, state trailingStructuredBlockState, structuredOutput *model.StructuredOutput) string {
	if state == trailingStructuredBlockValid && structuredOutput != nil {
		return joinSummary(baseSummary, "result="+normalizeSummaryValue(structuredOutput.Summary))
	}

	return joinSummary(baseSummary, plainRunnerOutput)
}

func normalizeSummaryValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func validateLaunch(in model.Invocation, workplace model.Workplace) error {
	if !workplace.Ready {
		return fmt.Errorf("workplace is not ready")
	}

	if strings.TrimSpace(in.Launch.Directory) == "" {
		return fmt.Errorf("launch directory is required")
	}

	if strings.TrimSpace(in.Launch.Prompt) == "" && in.Launch.StructuredInput == nil {
		return fmt.Errorf("launch prompt is required")
	}

	if !isSupportedRunner(in.Launch.Runner) {
		return fmt.Errorf("unsupported runner: %s", in.Launch.Runner)
	}

	if strings.TrimSpace(in.Launch.Model) == "" {
		return fmt.Errorf("launch model is required")
	}

	if err := validateStructuredOutputSettings(in.Launch); err != nil {
		return err
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

func isSupportedRunner(runner string) bool {
	switch strings.TrimSpace(runner) {
	case RunnerOpenCode, RunnerCodex:
		return true
	default:
		return false
	}
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

func (s *Service) commitAndPush(ctx context.Context, in model.Invocation, workplace model.Workplace, output *model.StructuredOutput) (gitResult, error) {
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

	if _, err := s.runGitOutput(ctx, in.Launch.Directory, "add", "-A", "--", ".", runnerOutputExcludePathspec); err != nil {
		return gitResult{}, fmt.Errorf("git add failed: %w", err)
	}

	hasChanges, err = s.hasChanges(ctx, in.Launch.Directory)
	if err != nil {
		return gitResult{}, err
	}

	if !hasChanges {
		return gitResult{status: "no-changes", branch: branch}, nil
	}

	commitMessage := resolveCommitMessage(in, workplace, output)

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

	for _, line := range strings.Split(output, "\n") {
		if statusLineHasUserChanges(line) {
			return true, nil
		}
	}

	return false, nil
}

func statusLineHasUserChanges(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	if isRunnerOutputStatusLine(line) {
		return false
	}

	return true
}

func isRunnerOutputStatusLine(line string) bool {
	if len(line) < 4 {
		return false
	}

	pathValue := strings.TrimSpace(line[3:])
	if pathValue == "" {
		return false
	}

	if strings.Contains(pathValue, " -> ") {
		for _, part := range strings.Split(pathValue, " -> ") {
			if !isRunnerOutputPath(part) {
				return false
			}
		}

		return true
	}

	return isRunnerOutputPath(pathValue)
}

func isRunnerOutputPath(pathValue string) bool {
	pathValue = strings.Trim(pathValue, "\"")
	return pathValue == ".progress/runner-output" || strings.HasPrefix(pathValue, ".progress/runner-output/")
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

func resolveCommitMessage(in model.Invocation, workplace model.Workplace, output *model.StructuredOutput) string {
	if output != nil {
		if message := normalizeCommitMessage(output.CommitMessage); message != "" {
			return message
		}
	}

	if name := normalizeCommitMessage(in.Workplace.Name); name != "" {
		return name
	}

	if name := worktreeDirectoryName(workplace.Name); name != "" {
		return name
	}

	return DefaultCommitMessage
}

func normalizeCommitMessage(value string) string {
	return strings.TrimSpace(value)
}

func worktreeDirectoryName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}

	return normalizeCommitMessage(name)
}

func runRunner(ctx context.Context, in model.Invocation) (string, error) {
	prompt, err := buildRunnerPrompt(in.Launch)
	if err != nil {
		return "", err
	}

	cmd, err := buildRunnerCommand(ctx, in.Launch, prompt)
	if err != nil {
		return "", err
	}
	cmd.Dir = in.Launch.Directory
	cmd.Env = sanitizedEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("launch runner failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	return string(output), nil
}

func persistRunnerOutput(workplaceDir, output string) string {
	workplaceDir = strings.TrimSpace(workplaceDir)
	if workplaceDir == "" || strings.TrimSpace(output) == "" {
		return ""
	}

	rawDir := filepath.Join(workplaceDir, ".progress", "runner-output")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return ""
	}

	file, err := os.CreateTemp(rawDir, "execution-*.log")
	if err != nil {
		return ""
	}
	defer file.Close()

	if _, err := io.WriteString(file, output); err != nil {
		return ""
	}

	return file.Name()
}

func buildRunnerCommand(ctx context.Context, spec model.LaunchSpec, prompt string) (*exec.Cmd, error) {
	runner := strings.TrimSpace(spec.Runner)
	var args []string

	switch runner {
	case RunnerOpenCode:
		args = []string{"run", "--dir", spec.Directory, "--model", spec.Model, prompt}
	case RunnerCodex:
		args = []string{"exec", "-C", spec.Directory, "-m", spec.Model, prompt}
	default:
		return nil, fmt.Errorf("unsupported runner: %s", spec.Runner)
	}

	return exec.CommandContext(ctx, runner, args...), nil
}

func buildRunnerPrompt(spec model.LaunchSpec) (string, error) {
	parts := make([]string, 0, 6)
	prompt := strings.TrimSpace(spec.Prompt)
	if prompt != "" {
		parts = append(parts, prompt)
	}
	parts = append(parts, normalizePromptAdditions(spec.PromptAdditions)...)

	if spec.StructuredInput == nil {
		if structuredOutputEnabled(spec) {
			parts = append(parts, buildStructuredOutputInstruction(spec.StructuredOutputFields))
		}
		return joinSummary(parts...), nil
	}
	if structuredOutputEnabled(spec) {
		parts = append(parts, buildStructuredOutputInstruction(spec.StructuredOutputFields))
	}

	canonical, err := canonicalizeStructuredInput(*spec.StructuredInput)
	if err != nil {
		return "", fmt.Errorf("invalid structured input: %w", err)
	}

	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal structured input: %w", err)
	}

	parts = append(parts,
		"Use every field from the structured input block below as execution context.",
		structuredInputStart,
		string(payload),
		structuredInputEnd,
	)

	return joinSummary(parts...), nil
}

func validateStructuredOutputSettings(spec model.LaunchSpec) error {
	if _, err := normalizeStructuredOutputInstructionFields(spec.StructuredOutputFields); err != nil {
		return fmt.Errorf("invalid structured output fields: %w", err)
	}

	return nil
}

func buildStructuredOutputInstruction(fields []string) string {
	parts := []string{
		"Return your normal answer, then append a trailing <progress-structured-output>...</progress-structured-output> JSON block.",
		fmt.Sprintf("Use a JSON object with protocol_version=%q and a summary field.", model.StructuredIOVersion),
	}

	optionalFields := structuredOutputInstructionFields(fields)
	if len(optionalFields) != 0 {
		parts = append(parts, fmt.Sprintf("Include %s when they are applicable.", strings.Join(optionalFields, ", ")))
	}
	if forms := selectedStructuredObjectForms(optionalFields); len(forms) != 0 {
		parts = append(parts, "Object forms: "+strings.Join(forms, ", ")+".")
	}
	parts = append(parts, "Canonical compact JSON example: "+buildStructuredOutputCanonicalExample(optionalFields)+".")

	return strings.Join(parts, " ")
}

func selectedStructuredObjectForms(fields []string) []string {
	fieldForms := []struct {
		field string
		form  string
	}{
		{field: "remarks", form: "remarks[{id,status,severity,type,title,body,answer,resolution}]"},
		{field: "questions", form: "questions[{id,status,title,body,answer}]"},
		{field: "follow_up_actions", form: "follow_up_actions[{id,status,type,title,body}]"},
		{field: "changes", form: "changes[{summary}]"},
		{field: "commands", form: "commands[{name,args,title,body}]"},
		{field: "conclusion", form: "conclusion{status,summary,body}"},
	}

	lookup := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		lookup[field] = struct{}{}
	}

	forms := make([]string, 0, len(fieldForms))
	for _, item := range fieldForms {
		if _, ok := lookup[item.field]; ok {
			forms = append(forms, item.form)
		}
	}

	return forms
}

func buildStructuredOutputCanonicalExample(fields []string) string {
	parts := []string{
		`{"protocol_version":"review-cycle/v1","summary":"Implemented changes."`,
	}
	for _, field := range fields {
		switch field {
		case "commit_message":
			parts = append(parts, `,"commit_message":"Apply requested review fixes"`)
		case "remarks":
			parts = append(parts, `,"remarks":[{"id":"remark-1","title":"Rollback plan"}]`)
		case "questions":
			parts = append(parts, `,"questions":[{"id":"question-1","title":"Need extra test?"}]`)
		case "follow_up_actions":
			parts = append(parts, `,"follow_up_actions":[{"id":"action-1","status":"pending","title":"Update checklist"}]`)
		case "changes":
			parts = append(parts, `,"changes":[{"summary":"Updated structured output instruction"}]`)
		case "commands":
			parts = append(parts, `,"commands":[{"name":"go test","args":["./..."]}]`)
		case "conclusion":
			parts = append(parts, `,"conclusion":{"status":"ok","summary":"Ready for review"}`)
		case "extensions":
			parts = append(parts, `,"extensions":{"custom":{"owner":"review-cycle"}}`)
		}
	}
	parts = append(parts, "}")

	return strings.Join(parts, "")
}

func applyProfileStructuredOutput(spec model.LaunchSpec, profile model.Profile) model.LaunchSpec {
	if len(spec.PromptAdditions) == 0 && len(profile.PromptAdditions) != 0 {
		spec.PromptAdditions = append([]string(nil), profile.PromptAdditions...)
	}
	if profile.StructuredOutput || profile.StructuredOutputRequired {
		spec.StructuredOutput = spec.StructuredOutput || profile.StructuredOutput || profile.StructuredOutputRequired
	}
	spec.StructuredOutputRequired = spec.StructuredOutputRequired || profile.StructuredOutputRequired
	if spec.StructuredOutputFields == nil && profile.StructuredOutputFields != nil {
		spec.StructuredOutputFields = append([]string(nil), profile.StructuredOutputFields...)
	}

	return spec
}

func normalizePromptAdditions(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func structuredOutputEnabled(spec model.LaunchSpec) bool {
	return spec.StructuredOutput || spec.StructuredOutputRequired
}

func structuredOutputInstructionFields(fields []string) []string {
	if fields == nil {
		return []string{"commit_message", "remarks", "questions", "follow_up_actions", "changes", "commands", "conclusion", "extensions"}
	}
	if normalized, err := normalizeStructuredOutputInstructionFields(fields); err == nil {
		return normalized
	}

	return fields
}

func normalizeStructuredOutputInstructionFields(fields []string) ([]string, error) {
	if fields == nil {
		return nil, nil
	}

	allowed := map[string]struct{}{
		"summary":           {},
		"commit_message":    {},
		"remarks":           {},
		"questions":         {},
		"follow_up_actions": {},
		"changes":           {},
		"commands":          {},
		"conclusion":        {},
		"extensions":        {},
	}

	seen := make(map[string]struct{}, len(fields))
	normalized := make([]string, 0, len(fields))
	for index, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("field at index %d must be non-empty", index)
		}
		if _, ok := allowed[field]; !ok {
			return nil, fmt.Errorf("unsupported field %q", field)
		}
		if _, ok := seen[field]; ok {
			continue
		}

		seen[field] = struct{}{}
		if field == "summary" {
			continue
		}

		normalized = append(normalized, field)
	}

	return normalized, nil
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
