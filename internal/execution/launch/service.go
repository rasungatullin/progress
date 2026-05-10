package launch

import (
	"context"
	"encoding/json"
	"fmt"
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
	in = prepareInvocation(in)

	if err := validateLaunch(in, workplace); err != nil {
		return model.LaunchResult{}, err
	}

	runnerOutput, err := s.runRunner(ctx, in)
	if err != nil {
		return model.LaunchResult{}, err
	}

	plainRunnerOutput, structuredOutput, structuredOutputState := parseStructuredOutput(runnerOutput)
	if err := validateStructuredOutputRequirement(in.Launch, structuredOutputState); err != nil {
		return model.LaunchResult{}, err
	}

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
	if structuredOutputState == trailingStructuredBlockValid {
		result.ReviewCycle = structuredOutput.ReviewCycle
		result.CriticalRemarks = structuredOutput.CriticalRemarks
		result.MinorRemarks = structuredOutput.MinorRemarks
		result.Questions = structuredOutput.Questions
	}

	return result, nil
}

type structuredOutput struct {
	ReviewCycle     *model.ReviewCycleEnvelope
	CriticalRemarks []string `json:"critical_remarks"`
	MinorRemarks    []string `json:"minor_remarks"`
	Questions       []string `json:"questions"`
}

func parseStructuredOutput(output string) (string, structuredOutput, trailingStructuredBlockState) {
	plainOutput, rawPayload, state := extractTrailingStructuredBlock(output, structuredOutputStart, structuredOutputEnd, func(rawPayload string) bool {
		_, err := parseStructuredPayload(rawPayload)
		return err == nil
	})
	if state != trailingStructuredBlockValid {
		return output, structuredOutput{}, state
	}

	parsed, err := parseStructuredPayload(rawPayload)
	if err != nil {
		return output, structuredOutput{}, trailingStructuredBlockInvalid
	}

	return plainOutput, parsed, trailingStructuredBlockValid
}

func parseStructuredInput(prompt string) (string, *model.ReviewCycleEnvelope, bool) {
	plainPrompt, rawPayload, state := extractTrailingStructuredBlock(prompt, structuredInputStart, structuredInputEnd, func(rawPayload string) bool {
		_, err := parseStructuredPayload(rawPayload)
		return err == nil
	})
	if state != trailingStructuredBlockValid {
		return prompt, nil, false
	}

	parsed, err := parseStructuredPayload(rawPayload)
	if err != nil {
		return prompt, nil, false
	}

	envelope := parsed.ReviewCycle
	if envelope == nil {
		return prompt, nil, false
	}

	return plainPrompt, envelope, true
}

func extractTrailingStructuredBlock(text, startTag, endTag string, validatePayload func(string) bool) (string, string, trailingStructuredBlockState) {
	trimmedText := strings.TrimRightFunc(text, unicode.IsSpace)
	if !strings.HasSuffix(trimmedText, endTag) {
		return text, "", trailingStructuredBlockAbsent
	}

	end := len(trimmedText) - len(endTag)
	searchEnd := end
	foundCandidate := false
	for {
		start := strings.LastIndex(trimmedText[:searchEnd], startTag)
		if start == -1 {
			if foundCandidate {
				return text, "", trailingStructuredBlockInvalid
			}

			return text, "", trailingStructuredBlockAbsent
		}

		foundCandidate = true
		rawPayload := strings.TrimSpace(trimmedText[start+len(startTag) : end])
		if rawPayload != "" && validatePayload(rawPayload) {
			plainText := strings.TrimSpace(trimmedText[:start])
			return plainText, rawPayload, trailingStructuredBlockValid
		}

		searchEnd = start
	}
}

type structuredPayload struct {
	ProtocolVersion string                    `json:"protocol_version"`
	Mode            string                    `json:"mode"`
	Summary         string                    `json:"summary"`
	Remarks         []model.ReviewCycleRemark `json:"remarks"`
	Questions       json.RawMessage           `json:"questions"`
	FollowUpActions []model.ReviewCycleAction `json:"follow_up_actions"`
	Changes         []model.ReviewCycleChange `json:"changes"`
	CriticalRemarks []string                  `json:"critical_remarks"`
	MinorRemarks    []string                  `json:"minor_remarks"`
}

func parseStructuredPayload(rawPayload string) (structuredOutput, error) {
	var payload structuredPayload
	if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {
		return structuredOutput{}, err
	}

	envelope, err := canonicalReviewCycleEnvelope(payload)
	if err != nil {
		return structuredOutput{}, err
	}

	result := structuredOutput{ReviewCycle: envelope}
	if envelope != nil {
		result.CriticalRemarks, result.MinorRemarks, result.Questions = reviewCycleLegacyView(*envelope)
	}

	return dedupeStructuredOutput(result), nil
}

func canonicalReviewCycleEnvelope(payload structuredPayload) (*model.ReviewCycleEnvelope, error) {
	questions, _, err := parseStructuredQuestions(payload.Questions)
	if err != nil {
		return nil, err
	}

	envelope := &model.ReviewCycleEnvelope{
		ProtocolVersion: payload.ProtocolVersion,
		Mode:            payload.Mode,
		Summary:         payload.Summary,
		Remarks:         append([]model.ReviewCycleRemark(nil), payload.Remarks...),
		Questions:       questions,
		FollowUpActions: append([]model.ReviewCycleAction(nil), payload.FollowUpActions...),
		Changes:         append([]model.ReviewCycleChange(nil), payload.Changes...),
	}
	appendLegacyRemarks(envelope, payload.CriticalRemarks, "critical")
	appendLegacyRemarks(envelope, payload.MinorRemarks, "minor")
	if envelope.ProtocolVersion == "" && hasReviewCycleEnvelope(*envelope) {
		envelope.ProtocolVersion = model.ReviewCycleProtocolVersion
	}
	if !hasReviewCycleEnvelope(*envelope) {
		return nil, nil
	}

	return envelope, nil
}

func parseStructuredQuestions(raw json.RawMessage) ([]model.ReviewCycleQuestion, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}

	var legacyQuestions []string
	if err := json.Unmarshal(raw, &legacyQuestions); err == nil {
		return stringsToQuestions(legacyQuestions), false, nil
	}

	var questions []model.ReviewCycleQuestion
	if err := json.Unmarshal(raw, &questions); err == nil {
		return questions, true, nil
	}

	return nil, false, fmt.Errorf("parse structured questions")
}

func hasReviewCycleEnvelope(envelope model.ReviewCycleEnvelope) bool {
	return envelope.ProtocolVersion != "" || envelope.Mode != "" || envelope.Summary != "" || len(envelope.Remarks) != 0 || len(envelope.Questions) != 0 || len(envelope.FollowUpActions) != 0 || len(envelope.Changes) != 0
}

func stringsToQuestions(values []string) []model.ReviewCycleQuestion {
	questions := make([]model.ReviewCycleQuestion, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		questions = append(questions, model.ReviewCycleQuestion{Body: value})
	}

	return questions
}

func appendLegacyRemarks(envelope *model.ReviewCycleEnvelope, values []string, severity string) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		envelope.Remarks = append(envelope.Remarks, model.ReviewCycleRemark{Severity: severity, Body: value})
	}
}

func reviewCycleLegacyView(envelope model.ReviewCycleEnvelope) ([]string, []string, []string) {
	criticalRemarks := make([]string, 0, len(envelope.Remarks))
	minorRemarks := make([]string, 0, len(envelope.Remarks))
	for _, remark := range envelope.Remarks {
		text := joinLegacySegments(
			compactStructuredText(remark.Title, remark.Body),
			labelStructuredText("reply", remark.Reply),
			labelStructuredText("fix_summary", remark.FixSummary),
		)
		if text == "" {
			text = compactStructuredText(remark.ID)
		}
		if text == "" {
			continue
		}

		if isCriticalSeverity(remark.Severity) {
			criticalRemarks = append(criticalRemarks, text)
			continue
		}

		minorRemarks = append(minorRemarks, text)
	}

	questions := make([]string, 0, len(envelope.Questions))
	for _, question := range envelope.Questions {
		text := joinLegacySegments(
			compactStructuredText(question.Title, question.Body),
			labelStructuredText("reply", question.Reply),
		)
		if text == "" {
			text = compactStructuredText(question.ID)
		}
		if text == "" {
			continue
		}

		questions = append(questions, text)
	}

	return criticalRemarks, minorRemarks, questions
}

func dedupeStructuredOutput(output structuredOutput) structuredOutput {
	output.CriticalRemarks = dedupeStrings(output.CriticalRemarks)
	output.MinorRemarks = dedupeStrings(output.MinorRemarks)
	output.Questions = dedupeStrings(output.Questions)
	return output
}

func dedupeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

func compactStructuredText(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		filtered = append(filtered, part)
	}

	return strings.Join(filtered, ": ")
}

func labelStructuredText(label, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	return fmt.Sprintf("%s=%s", label, value)
}

func joinLegacySegments(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		filtered = append(filtered, part)
	}

	return strings.Join(filtered, "; ")
}

func isCriticalSeverity(severity string) bool {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high", "blocker":
		return true
	default:
		return false
	}
}

func prepareInvocation(in model.Invocation) model.Invocation {
	plainPrompt, structuredInput, hasStructuredInput := parseStructuredInput(in.Launch.Prompt)
	if !hasStructuredInput {
		return in
	}

	in.Launch.Prompt = plainPrompt
	if in.Launch.StructuredInput == nil {
		in.Launch.StructuredInput = structuredInput
	}

	return in
}

func validateStructuredOutputRequirement(spec model.LaunchSpec, state trailingStructuredBlockState) error {
	if !spec.StructuredOutputRequired {
		return nil
	}

	switch state {
	case trailingStructuredBlockValid:
		return nil
	case trailingStructuredBlockInvalid:
		return fmt.Errorf("structured output is required but trailing %s block is invalid", structuredOutputStart)
	default:
		return fmt.Errorf("structured output is required but trailing %s block is missing", structuredOutputStart)
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
	parts := make([]string, 0, 4)
	prompt := strings.TrimSpace(spec.Prompt)
	if prompt != "" {
		parts = append(parts, prompt)
	}

	if spec.StructuredOutput {
		parts = append(parts, buildStructuredOutputInstruction(normalizedStructuredProtocol(spec), strings.TrimSpace(spec.StructuredMode)))
	}

	if spec.StructuredInput == nil {
		return joinSummary(parts...), nil
	}

	payload, err := json.Marshal(spec.StructuredInput)
	if err != nil {
		return "", fmt.Errorf("marshal structured input: %w", err)
	}

	parts = append(parts, structuredInputStart, string(payload), structuredInputEnd)
	return joinSummary(parts...), nil
}

func validateStructuredOutputSettings(spec model.LaunchSpec) error {
	if !spec.StructuredOutput {
		if strings.TrimSpace(spec.StructuredProtocol) != "" && strings.TrimSpace(spec.StructuredProtocol) != model.StructuredProtocolReviewCycle {
			return fmt.Errorf("structured protocol requires structured output to be enabled")
		}
		if strings.TrimSpace(spec.StructuredMode) != "" {
			return fmt.Errorf("structured mode requires structured output to be enabled")
		}
		return nil
	}

	if !isSupportedStructuredProtocol(normalizedStructuredProtocol(spec)) {
		return fmt.Errorf("unsupported structured protocol: %s", strings.TrimSpace(spec.StructuredProtocol))
	}

	if mode := strings.TrimSpace(spec.StructuredMode); mode != "" && !isSupportedStructuredMode(mode) {
		return fmt.Errorf("unsupported structured mode: %s", mode)
	}

	return nil
}

func normalizedStructuredProtocol(spec model.LaunchSpec) string {
	protocol := strings.TrimSpace(spec.StructuredProtocol)
	if protocol == "" {
		return model.StructuredProtocolReviewCycle
	}

	return protocol
}

func isSupportedStructuredProtocol(protocol string) bool {
	switch protocol {
	case model.StructuredProtocolLegacy, model.StructuredProtocolReviewCycle:
		return true
	default:
		return false
	}
}

func isSupportedStructuredMode(mode string) bool {
	switch mode {
	case model.ReviewCycleModeReview, model.ReviewCycleModeReply, model.ReviewCycleModeFix, model.ReviewCycleModeReReview:
		return true
	default:
		return false
	}
}

func buildStructuredOutputInstruction(protocol, mode string) string {
	if protocol == model.StructuredProtocolLegacy {
		return buildLegacyStructuredOutputInstruction(mode)
	}

	return buildReviewCycleStructuredOutputInstruction(mode)
}

func buildLegacyStructuredOutputInstruction(mode string) string {
	parts := []string{
		"Return your normal answer, then append a trailing <progress-structured-output>...</progress-structured-output> JSON block.",
		"Use a JSON object with array fields critical_remarks, minor_remarks, questions.",
	}
	if mode != "" {
		parts = append(parts, fmt.Sprintf("Current structured mode: %s.", mode))
	}

	return strings.Join(parts, " ")
}

func buildReviewCycleStructuredOutputInstruction(mode string) string {
	parts := []string{
		"Return your normal answer, then append a trailing <progress-structured-output>...</progress-structured-output> JSON block.",
		fmt.Sprintf("Use a JSON object with protocol_version=%q and a summary field.", model.ReviewCycleProtocolVersion),
		"Include remarks, questions, follow_up_actions, and changes when they are applicable.",
	}
	if mode != "" {
		parts = append(parts, fmt.Sprintf("Set mode to %q.", mode))
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
