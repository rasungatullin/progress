package launch

import (
	"context"
	"encoding/json"
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

const DefaultCommitMessage = "Apply task result"

const structuredOutputStart = "<progress-structured-output>"

const structuredOutputEnd = "</progress-structured-output>"

const structuredInputStart = "<progress-structured-input>"

const structuredInputEnd = "</progress-structured-input>"

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

	if err := validateLaunch(in, workplace); err != nil {
		return model.LaunchResult{}, err
	}

	runnerOutput, err := s.runRunner(ctx, in)
	if err != nil {
		return model.LaunchResult{}, err
	}

	plainRunnerOutput, rawStructuredOutput, structuredOutput, structuredOutputState, structuredOutputErr := parseStructuredOutput(runnerOutput)
	if err := validateStructuredOutputRequirement(in.Launch, rawStructuredOutput, structuredOutputState, structuredOutputErr); err != nil {
		return model.LaunchResult{Status: "failed", Summary: strings.TrimSpace(plainRunnerOutput)}, err
	}

	commitPush := in.Launch.CommitPush || profile.CommitPush

	gitSummary := "git=disabled"
	if commitPush {
		result, err := s.commitAndPush(ctx, in, workplace, structuredOutput)
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
	plainPrompt, structuredInput, structuredInputState, structuredInputErr := parseStructuredInput(in.Launch.Prompt)
	switch structuredInputState {
	case trailingStructuredBlockValid:
		in.Launch.Prompt = plainPrompt
		if in.Launch.StructuredInput == nil {
			in.Launch.StructuredInput = structuredInput
		}
	case trailingStructuredBlockInvalid:
		if structuredInputErr != nil {
			return model.Invocation{}, fmt.Errorf("invalid structured input: %w", structuredInputErr)
		}
		return model.Invocation{}, fmt.Errorf("invalid structured input: trailing %s block is invalid", structuredInputStart)
	default:
		if in.Launch.StructuredInput == nil {
			return in, nil
		}
	}

	if in.Launch.StructuredInput == nil {
		return in, nil
	}

	canonical, err := canonicalizeStructuredInput(*in.Launch.StructuredInput)
	if err != nil {
		return model.Invocation{}, fmt.Errorf("invalid structured input: %w", err)
	}
	in.Launch.StructuredInput = canonical

	return in, nil
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
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON tokens")
		}
		return err
	}

	return nil
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

	if strings.TrimSpace(in.Launch.Prompt) == "" && in.Launch.StructuredInput == nil {
		return fmt.Errorf("launch prompt is required")
	}

	if strings.TrimSpace(in.Launch.Runner) != RunnerOpenCode {
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

	args := []string{
		"run",
		"--dir", in.Launch.Directory,
		"--model", in.Launch.Model,
		prompt,
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

func buildRunnerPrompt(spec model.LaunchSpec) (string, error) {
	parts := make([]string, 0, 6)
	prompt := strings.TrimSpace(spec.Prompt)
	if prompt != "" {
		parts = append(parts, prompt)
	}

	if spec.StructuredInput == nil {
		if spec.StructuredOutput {
			parts = append(parts, buildStructuredOutputInstruction())
		}
		return joinSummary(parts...), nil
	}
	if spec.StructuredOutput {
		parts = append(parts, buildStructuredOutputInstruction())
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
	return nil
}

func buildStructuredOutputInstruction() string {
	parts := []string{
		"Return your normal answer, then append a trailing <progress-structured-output>...</progress-structured-output> JSON block.",
		fmt.Sprintf("Use a JSON object with protocol_version=%q and a summary field.", model.StructuredIOVersion),
		"Include commit_message, remarks, questions, follow_up_actions, changes, commands, conclusion, and extensions when they are applicable.",
	}

	return strings.Join(parts, " ")
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
