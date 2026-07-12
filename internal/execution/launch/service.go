package launch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/rasungatullin/progress/internal/execution/history"
	"github.com/rasungatullin/progress/internal/execution/model"
	integrationmodel "github.com/rasungatullin/progress/internal/integration/model"
	"github.com/rasungatullin/progress/internal/integration/secrets"
)

const RunnerOpenCode = "opencode"

const RunnerCodex = "codex"

const DefaultCommitMessage = "Apply task result"

const structuredOutputStart = "<progress-structured-output>"

const structuredOutputEnd = "</progress-structured-output>"

const runnerMetadataStart = "<progress-runner-metadata>"

const runnerMetadataEnd = "</progress-runner-metadata>"

const runRecordFilePrefix = "execution-"

type trailingStructuredBlockState int

const (
	trailingStructuredBlockAbsent trailingStructuredBlockState = iota
	trailingStructuredBlockValid
	trailingStructuredBlockInvalid
)

type Service struct {
	runRunner        func(context.Context, model.Invocation) (string, error)
	extractSessionID func(model.Invocation, string) string
	runGitOutput     func(context.Context, string, ...string) (string, error)
	runGitOutputEnv  func(context.Context, string, []string, ...string) (string, error)
}

type runnerMetadata struct {
	RunnerSessionID string `json:"runner_session_id,omitempty"`
}

var errResumeUnsupported = errors.New("resume is unsupported")

type historyHandleContextKey struct{}

func WithHistoryHandle(ctx context.Context, handle history.Handle) context.Context {
	return context.WithValue(ctx, historyHandleContextKey{}, handle)
}

func historyHandleFromContext(ctx context.Context) history.Handle {
	handle, _ := ctx.Value(historyHandleContextKey{}).(history.Handle)
	return handle
}

func NewService() *Service {
	return &Service{
		runRunner:        runRunner,
		extractSessionID: extractRunnerSessionID,
		runGitOutput:     runGitOutput,
		runGitOutputEnv:  runGitOutputEnv,
	}
}

func BuildPrompt(spec model.LaunchSpec) (string, error) {
	return buildRunnerPrompt(spec)
}

func ParseOutput(output string) (string, string, *model.StructuredOutput, error) {
	plain, raw, structured, state, err := parseStructuredOutput(output)
	if state == trailingStructuredBlockInvalid {
		return plain, raw, nil, err
	}
	return plain, raw, structured, nil
}

func (s *Service) Launch(ctx context.Context, in model.Invocation, profile model.Profile, allocation model.Allocation, workplace model.Workplace) (model.LaunchResult, error) {
	historyHandle := historyHandleFromContext(ctx)
	if historyHandle.RunID == 0 {
		historyHandle = beginExecutionHistoryRun(ctx, workplace.Name, in, profile, "running", "")
	}

	var err error
	in, err = prepareInvocation(in)
	if err != nil {
		result := failedLaunchResult(err)
		result.RunRecordPath = persistExecutionRunRecord(historyHandle, workplace.Name, in, profile, allocation, workplace, result, "", nil, nil, err)
		return result, err
	}
	in.Launch = applyProfileStructuredOutput(in.Launch, profile)

	if err := validateLaunch(in, workplace); err != nil {
		result := failedLaunchResult(err)
		result.RunRecordPath = persistExecutionRunRecord(historyHandle, workplace.Name, in, profile, allocation, workplace, result, "", nil, nil, err)
		return result, err
	}

	runnerOutput, err := s.runRunner(ctx, in)
	if err != nil {
		status := "failed"
		if errors.Is(err, errResumeUnsupported) {
			status = "resume-unsupported"
		} else if launchInterrupted(ctx, err) {
			status = "interrupted"
		}
		result := model.LaunchResult{
			Status:  status,
			Summary: strings.TrimSpace(err.Error()),
		}
		result.RunRecordPath = persistExecutionRunRecord(historyHandle, workplace.Name, in, profile, allocation, workplace, result, "", nil, nil, err)
		return result, err
	}
	rawRunnerOutput := runnerOutput
	runnerSessionID := ""
	if s.extractSessionID != nil {
		runnerSessionID = strings.TrimSpace(s.extractSessionID(in, rawRunnerOutput))
	}
	runnerOutput, _ = stripTrailingRunnerMetadata(rawRunnerOutput)
	rawOutputPath := persistRunnerOutput(workplace.Name, runnerOutput)

	plainRunnerOutput, rawStructuredOutput, structuredOutput, structuredOutputState, structuredOutputErr := parseStructuredOutput(runnerOutput)
	result := model.LaunchResult{
		Status:              "failed",
		Summary:             strings.TrimSpace(plainRunnerOutput),
		RawOutput:           runnerOutput,
		RawOutputPath:       rawOutputPath,
		RawStructuredOutput: rawStructuredOutput,
		RunnerSessionID:     runnerSessionID,
	}
	if structuredOutputState == trailingStructuredBlockValid {
		result.StructuredOutput = structuredOutput
	}
	if err := validateStructuredOutputRequirement(in.Launch, rawStructuredOutput, structuredOutputState, structuredOutputErr); err != nil {
		if structuredOutputState != trailingStructuredBlockValid {
			result.StructuredOutput = nil
		}
		result.RunRecordPath = persistExecutionRunRecord(historyHandle, workplace.Name, in, profile, allocation, workplace, result, rawStructuredOutput, structuredOutput, err, err)
		return result, err
	}

	gitSummary := "git=disabled"
	if in.Launch.CommitPush {
		result, err := s.commitAndPush(ctx, commitPushInputFromLaunch(in, allocation, workplace, structuredOutput))
		if err != nil {
			launchResult := model.LaunchResult{
				Status:              "failed",
				Summary:             strings.TrimSpace(plainRunnerOutput),
				RawOutput:           runnerOutput,
				RawOutputPath:       rawOutputPath,
				RawStructuredOutput: rawStructuredOutput,
				StructuredOutput:    structuredOutput,
				RunnerSessionID:     runnerSessionID,
				RunRecordPath:       "",
			}

			if structuredOutputState != trailingStructuredBlockValid {
				launchResult.StructuredOutput = nil
			}
			launchResult.RunRecordPath = persistExecutionRunRecord(historyHandle, workplace.Name, in, profile, allocation, workplace, launchResult, rawStructuredOutput, structuredOutput, structuredOutputErr, err)
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

	result = model.LaunchResult{Status: "completed", Summary: buildLaunchSummary(summary, plainRunnerOutput, structuredOutputState, structuredOutput), RawOutput: runnerOutput, RawOutputPath: rawOutputPath, RawStructuredOutput: rawStructuredOutput}
	result.RunnerSessionID = runnerSessionID
	if structuredOutputState == trailingStructuredBlockValid {
		result.StructuredOutput = structuredOutput
	}
	result.RunRecordPath = persistExecutionRunRecord(historyHandle, workplace.Name, in, profile, allocation, workplace, result, rawStructuredOutput, structuredOutput, structuredOutputErr, nil)

	return result, nil
}

func failedLaunchResult(err error) model.LaunchResult {
	return model.LaunchResult{Status: "failed", Summary: strings.TrimSpace(err.Error())}
}

func launchInterrupted(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
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

func appendTrailingRunnerMetadata(output string, metadata runnerMetadata) string {
	if strings.TrimSpace(metadata.RunnerSessionID) == "" {
		return output
	}

	payload, err := json.Marshal(metadata)
	if err != nil {
		return output
	}

	trimmedOutput := strings.TrimRightFunc(output, unicode.IsSpace)
	if trimmedOutput == "" {
		return runnerMetadataStart + "\n" + string(payload) + "\n" + runnerMetadataEnd
	}

	return trimmedOutput + "\n" + runnerMetadataStart + "\n" + string(payload) + "\n" + runnerMetadataEnd
}

func stripTrailingRunnerMetadata(output string) (string, *runnerMetadata) {
	plainOutput, rawPayload, state, err := extractTrailingStructuredBlock(output, runnerMetadataStart, runnerMetadataEnd, func(rawPayload string) error {
		var payload runnerMetadata
		return decodeJSONStrict(rawPayload, &payload)
	})
	if err != nil || state != trailingStructuredBlockValid {
		return output, nil
	}

	var payload runnerMetadata
	if err := decodeJSONStrict(rawPayload, &payload); err != nil {
		return output, nil
	}

	payload.RunnerSessionID = strings.TrimSpace(payload.RunnerSessionID)
	return plainOutput, &payload
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

func hasStructuredInputContent(payload model.StructuredInput) bool {
	return payload.Task != "" || len(payload.Constraints) != 0 || len(payload.ProjectContext) != 0 || len(payload.OperationalContext) != 0 || len(payload.PreviousRunResults) != 0 || len(payload.ReviewRemarks) != 0 || len(payload.ReviewResponses) != 0 || len(payload.IntegrationActions) != 0 || len(payload.Extensions) != 0
}

func canonicalizeStructuredInput(payload model.StructuredInput) (*model.StructuredInput, error) {
	if err := validateStructuredInputPayload(payload); err != nil {
		return nil, err
	}

	return &payload, nil
}

func validateStructuredInputPayload(payload model.StructuredInput) error {
	if !hasStructuredInputContent(payload) {
		return fmt.Errorf("structured input must include at least one non-empty field")
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
		if hasNonEmptyStructuredRemark(value) {
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
	if strings.TrimSpace(payload.Summary) == "" {
		return fmt.Errorf("structured output must include a non-empty summary")
	}
	for index, value := range payload.Remarks {
		if hasNonEmptyStructuredRemark(value) {
			continue
		}

		return fmt.Errorf("structured output remarks[%d] must include at least one non-empty field", index)
	}
	for index, value := range payload.ReviewResponses {
		if hasNonEmptyStructuredField(value.ID, value.RemarkID, value.Status, value.Summary, value.Body) {
			continue
		}

		return fmt.Errorf("structured output review_responses[%d] must include at least one non-empty field", index)
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
	if in.Launch.StructuredInput != nil {
		structuredInput, err := NormalizeStructuredInput(in.Launch.StructuredInput)
		if err != nil {
			return in, err
		}
		in.Launch.StructuredInput = structuredInput
	}
	return in, nil
}

func NormalizeStructuredInput(input *model.StructuredInput) (*model.StructuredInput, error) {
	if input == nil {
		return nil, nil
	}

	canonical, err := canonicalizeStructuredInput(*input)
	if err != nil {
		return nil, fmt.Errorf("invalid structured input: %w", err)
	}

	return canonical, nil
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

func hasNonEmptyStructuredRemark(value model.StructuredRemark) bool {
	return hasNonEmptyStructuredField(value.ID, value.Status, value.Severity, value.Type, value.Title, value.Body, value.Answer, value.Resolution)
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
		return "array of objects with id/status/severity/type/title/body/path/line/side/answer/resolution", true
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

	if !isSupportedRunner(in.Launch.Runner) && in.Launch.Resume == nil {
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

type runRecord struct {
	CreatedAt           string                  `json:"created_at"`
	Invocation          model.Invocation        `json:"invocation"`
	Profile             model.Profile           `json:"profile"`
	Allocation          model.Allocation        `json:"allocation"`
	Workplace           model.Workplace         `json:"workplace"`
	StructuredInput     *model.StructuredInput  `json:"structured_input,omitempty"`
	RunnerSessionID     string                  `json:"runner_session_id,omitempty"`
	RawStructuredOutput string                  `json:"raw_structured_output"`
	StructuredOutput    *model.StructuredOutput `json:"structured_output,omitempty"`
	StructuredOutputErr string                  `json:"structured_output_error,omitempty"`
	Error               string                  `json:"error,omitempty"`
	Result              runRecordResult         `json:"result"`
}

type runRecordResult struct {
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	RawOutputPath string `json:"raw_output_path"`
}

func (r gitResult) summary() string {
	if r.branch == "" {
		return fmt.Sprintf("git=%s", r.status)
	}

	return fmt.Sprintf("git=%s branch=%s", r.status, r.branch)
}

func beginExecutionHistoryRun(ctx context.Context, workplaceDir string, in model.Invocation, profile model.Profile, status string, summary string) history.Handle {
	workplaceDir = strings.TrimSpace(workplaceDir)
	if workplaceDir == "" {
		return history.Handle{}
	}
	if strings.TrimSpace(status) == "" {
		status = "running"
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)
	handle, err := history.Begin(ctx, workplaceDir, history.Run{
		CreatedAt:          createdAt,
		Status:             status,
		Summary:            summary,
		Name:               in.Workplace.Name,
		ProfileName:        fallbackHistoryValue(profile.Name),
		Runner:             fallbackHistoryValue(in.Launch.Runner),
		Model:              fallbackHistoryValue(in.Launch.Model),
		LaunchDirectory:    in.Launch.Directory,
		RawStructuredInput: history.StructuredInputJSON(in.Launch.StructuredInput),
	})
	if err != nil {
		return history.Handle{}
	}
	return handle
}

func persistExecutionRunRecord(historyHandle history.Handle, workplaceDir string, in model.Invocation, profile model.Profile, allocation model.Allocation, workplace model.Workplace, launchResult model.LaunchResult, rawStructuredOutput string, structuredOutput *model.StructuredOutput, structuredOutputErr, launchErr error) string {
	workplaceDir = strings.TrimSpace(workplaceDir)
	recordPath := ""

	var structuredInput *model.StructuredInput
	if in.Launch.StructuredInput != nil {
		copyOfStructuredInput := *in.Launch.StructuredInput
		structuredInput = &copyOfStructuredInput
	}

	errText := ""
	if structuredOutputErr != nil {
		errText = structuredOutputErr.Error()
	}
	launchErrText := ""
	if launchErr != nil {
		launchErrText = launchErr.Error()
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)
	record := runRecord{
		CreatedAt:           createdAt,
		Invocation:          in,
		Profile:             profile,
		Allocation:          allocation,
		Workplace:           workplace,
		StructuredInput:     structuredInput,
		RunnerSessionID:     launchResult.RunnerSessionID,
		RawStructuredOutput: rawStructuredOutput,
		StructuredOutput:    structuredOutput,
		StructuredOutputErr: errText,
		Error:               launchErrText,
		Result: runRecordResult{
			Status:        launchResult.Status,
			Summary:       launchResult.Summary,
			RawOutputPath: launchResult.RawOutputPath,
		},
	}

	if workplaceDir != "" && existingDirectory(workplaceDir) {
		recordDir := filepath.Join(workplaceDir, ".progress", "execution-runs")
		if err := os.MkdirAll(recordDir, 0o755); err == nil {
			if recordFile, err := os.CreateTemp(recordDir, runRecordFilePrefix+"*.json"); err == nil {
				payload, marshalErr := json.MarshalIndent(record, "", "  ")
				if marshalErr == nil {
					if _, writeErr := io.WriteString(recordFile, string(payload)); writeErr == nil {
						recordPath = recordFile.Name()
					}
				}
				_ = recordFile.Close()
			}
		}
	}

	_ = history.Update(context.Background(), historyHandle, history.Run{
		CreatedAt:           createdAt,
		Status:              launchResult.Status,
		Summary:             launchResult.Summary,
		Name:                in.Workplace.Name,
		ProfileName:         fallbackHistoryValue(profile.Name),
		Runner:              fallbackHistoryValue(in.Launch.Runner),
		RunnerSessionID:     launchResult.RunnerSessionID,
		ParentRunID:         resumeParentRunID(in),
		ResumeMessage:       resumeMessage(in),
		ResumeMessageSource: resumeMessageSource(in),
		Model:               fallbackHistoryValue(in.Launch.Model),
		LaunchDirectory:     in.Launch.Directory,
		RawStructuredInput:  history.StructuredInputJSON(structuredInput),
		RawOutputPath:       launchResult.RawOutputPath,
		RawStructuredOutput: history.StructuredOutputJSON(structuredOutput, rawStructuredOutput),
		RunRecordPath:       recordPath,
		Error:               launchErrText,
	})

	return recordPath
}

func existingDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fallbackHistoryValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func (s *Service) commitAndPush(ctx context.Context, input model.CommitPushInput) (gitResult, error) {
	if !s.isGitRepository(ctx, input.Directory) {
		return gitResult{}, fmt.Errorf("launch directory is not a git repository")
	}

	gitRoot, err := s.gitRepositoryRoot(ctx, input.Directory)
	if err != nil {
		return gitResult{}, err
	}

	branch, err := s.currentBranch(ctx, input.Directory)
	if err != nil {
		return gitResult{}, err
	}

	changedPaths, trackedDeletionPaths, err := s.changedPathsForCommit(ctx, gitRoot)
	if err != nil {
		return gitResult{}, err
	}

	if len(changedPaths) == 0 && len(trackedDeletionPaths) == 0 {
		return gitResult{status: "no-changes", branch: branch}, nil
	}

	pushEnv, cleanupPushKey, err := gitPushEnv(ctx, input.Git, input.PrivateStore, input.ConfigHome)
	if err != nil {
		return gitResult{}, err
	}
	defer cleanupPushKey()

	if len(trackedDeletionPaths) > 0 {
		addArgs := append([]string{"add", "-u", "--"}, trackedDeletionPaths...)
		if _, err := s.runGitOutput(ctx, gitRoot, addArgs...); err != nil {
			return gitResult{}, fmt.Errorf("git add failed: %w", err)
		}
	}
	if len(changedPaths) > 0 {
		addArgs := append([]string{"add", "-A", "--"}, changedPaths...)
		if _, err := s.runGitOutput(ctx, gitRoot, addArgs...); err != nil {
			return gitResult{}, fmt.Errorf("git add failed: %w", err)
		}
	}

	changedPaths, trackedDeletionPaths, err = s.changedPathsForCommit(ctx, gitRoot)
	if err != nil {
		return gitResult{}, err
	}

	if len(changedPaths) == 0 && len(trackedDeletionPaths) == 0 {
		return gitResult{status: "no-changes", branch: branch}, nil
	}

	commitMessage := resolveCommitMessage(input)
	commitArgs, commitEnv := gitCommitInvocation(input.Git, commitMessage)

	if _, err := s.runGitOutputWithEnv(ctx, input.Directory, commitEnv, commitArgs...); err != nil {
		if isNoChangesAfterAddError(err) {
			return gitResult{status: "no-changes", branch: branch}, nil
		}

		return gitResult{}, fmt.Errorf("git commit failed: %w", err)
	}

	upstream, err := s.upstreamBranch(ctx, input.Directory, branch)
	if err != nil {
		return gitResult{}, err
	}

	pushArgs := []string{"push"}
	if upstream != "origin/"+branch {
		pushArgs = append(pushArgs, "-u", "origin", branch)
	}

	if _, err := s.runGitOutputWithEnv(ctx, input.Directory, pushEnv, pushArgs...); err != nil {
		return gitResult{}, fmt.Errorf("git push failed: %w", err)
	}

	return gitResult{status: "committed+pushed", branch: branch}, nil
}

func commitPushInputFromLaunch(in model.Invocation, allocation model.Allocation, workplace model.Workplace, output *model.StructuredOutput) model.CommitPushInput {
	input := model.CommitPushInput{
		Directory:    in.Launch.Directory,
		FallbackName: firstNonEmpty(in.Workplace.Name, worktreeDirectoryName(workplace.Name)),
		Git:          allocation.Git,
		PrivateStore: allocation.PrivateStore,
		ConfigHome:   allocation.ConfigHome,
	}
	if output != nil {
		input.CommitMessage = output.CommitMessage
	}
	return input
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Service) CommitAndPush(ctx context.Context, input model.CommitPushInput) (string, error) {
	result, err := s.commitAndPush(ctx, input)
	if err != nil {
		return "", err
	}
	return result.summary(), nil
}

func (s *Service) runGitOutputWithEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	if len(env) == 0 || s.runGitOutputEnv == nil {
		return s.runGitOutput(ctx, dir, args...)
	}
	return s.runGitOutputEnv(ctx, dir, env, args...)
}

func gitCommitInvocation(config *model.GitConfig, message string) ([]string, []string) {
	args := []string{"commit", "-m", message}
	var env []string
	if config == nil {
		return args, env
	}
	if identity := config.Identity; identity != nil && strings.TrimSpace(identity.AuthorName) != "" {
		env = append(env,
			"GIT_AUTHOR_NAME="+identity.AuthorName,
			"GIT_AUTHOR_EMAIL="+identity.AuthorEmail,
			"GIT_COMMITTER_NAME="+identity.CommitterName,
			"GIT_COMMITTER_EMAIL="+identity.CommitterEmail,
		)
	}
	if signing := config.Signing; signing != nil && signing.Enabled {
		prefix := []string{"-c", "commit.gpgsign=true", "-c", "gpg.format=" + signing.Format, "-c", "user.signingkey=" + signing.SigningKey}
		if strings.TrimSpace(signing.Program) != "" {
			prefix = append(prefix, "-c", "gpg."+signing.Format+".program="+signing.Program)
		}
		args = append(prefix, args...)
	}
	return args, env
}

func gitPushEnv(ctx context.Context, config *model.GitConfig, privateStore model.ResourcePrivateStoreConfig, configHome string) ([]string, func(), error) {
	cleanup := func() {}
	if config == nil || config.Push == nil {
		return nil, cleanup, nil
	}
	if err := resolvePrivateGitPushConfig(ctx, config, privateStore, configHome); err != nil {
		return nil, cleanup, err
	}
	identityFile := strings.TrimSpace(config.Push.SSHIdentityFile)
	if identityFile == "" && strings.TrimSpace(config.Push.SSHIdentityPrivateValue) != "" {
		path, err := writeTemporaryPrivateKey(config.Push.SSHIdentityPrivateValue)
		if err != nil {
			return nil, cleanup, err
		}
		identityFile = path
		cleanup = func() { _ = os.Remove(path) }
	}
	if identityFile == "" && strings.TrimSpace(config.Push.SSHIdentityPrivate) != "" {
		return nil, cleanup, fmt.Errorf("git.push.ssh-identity-private is configured but private value is unavailable")
	}
	if identityFile == "" && (strings.TrimSpace(config.Push.KnownHostsFile) != "" || config.Push.IdentitiesOnly) {
		return nil, cleanup, fmt.Errorf("git.push must define ssh-identity-file or ssh-identity-private when known-hosts-file or identities-only is set")
	}
	if identityFile == "" {
		return nil, cleanup, nil
	}
	parts := []string{"ssh", "-i", shellQuote(identityFile)}
	parts = append(parts, "-o", "IdentitiesOnly=yes")
	if strings.TrimSpace(config.Push.KnownHostsFile) != "" {
		parts = append(parts, "-o", "UserKnownHostsFile="+shellQuote(config.Push.KnownHostsFile))
	}
	return []string{"GIT_SSH_COMMAND=" + strings.Join(parts, " ")}, cleanup, nil
}

func resolvePrivateGitPushConfig(ctx context.Context, config *model.GitConfig, privateStore model.ResourcePrivateStoreConfig, configHome string) error {
	if config == nil || config.Push == nil || strings.TrimSpace(config.Push.SSHIdentityPrivate) == "" || strings.TrimSpace(config.Push.SSHIdentityPrivateValue) != "" {
		return nil
	}
	store, _, err := secrets.NewStore(integrationmodel.IntegrationPrivateStoreConfig{
		Type:    privateStore.Type,
		Service: privateStore.Service,
		Path:    privateStore.Path,
	}, configHome)
	if err != nil {
		return fmt.Errorf("git.push requires private store for ssh-identity-private %q: %w", config.Push.SSHIdentityPrivate, err)
	}
	value, err := store.Get(ctx, config.Push.SSHIdentityPrivate)
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return fmt.Errorf("git.push references missing private value %q", config.Push.SSHIdentityPrivate)
		}
		return fmt.Errorf("git.push cannot read private value %q: %w", config.Push.SSHIdentityPrivate, err)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("git.push references empty private value %q", config.Push.SSHIdentityPrivate)
	}
	config.Push.SSHIdentityPrivateValue = value
	return nil
}

func writeTemporaryPrivateKey(value string) (string, error) {
	file, err := os.CreateTemp("", "progress-git-ssh-key-*")
	if err != nil {
		return "", fmt.Errorf("create temporary git ssh identity file: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("restrict temporary git ssh identity file: %w", err)
	}
	if _, err := file.WriteString(value); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write temporary git ssh identity file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temporary git ssh identity file: %w", err)
	}
	return path, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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

func (s *Service) gitRepositoryRoot(ctx context.Context, dir string) (string, error) {
	output, err := s.runGitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve git repository root: %w", err)
	}

	root := strings.TrimSpace(output)
	if root == "" {
		return "", fmt.Errorf("resolve git repository root: root is empty")
	}

	return root, nil
}

func (s *Service) hasChanges(ctx context.Context, dir string) (bool, error) {
	paths, err := s.changedUserPaths(ctx, dir)
	if err != nil {
		return false, err
	}

	return len(paths) > 0, nil
}

func (s *Service) changedUserPaths(ctx context.Context, dir string) ([]string, error) {
	paths, _, err := s.changedPathsForCommit(ctx, dir)
	return paths, err
}

func (s *Service) changedPathsForCommit(ctx context.Context, dir string) ([]string, []string, error) {
	output, err := s.runGitOutput(ctx, dir, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return nil, nil, fmt.Errorf("inspect git changes: %w", err)
	}

	paths, trackedDeletionPaths := userChangedPathsForCommitFromPorcelain(output)
	return paths, trackedDeletionPaths, nil
}

func userChangedPathsFromPorcelain(output string) []string {
	paths, _ := userChangedPathsForCommitFromPorcelain(output)
	return paths
}

func userChangedPathsForCommitFromPorcelain(output string) ([]string, []string) {
	if output == "" {
		return nil, nil
	}

	paths := make([]string, 0)
	trackedDeletionPaths := make([]string, 0)
	seen := make(map[string]struct{})
	seenTrackedDeletion := make(map[string]struct{})
	entries := strings.Split(output, "\x00")
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}

		pathValues := []string{strings.TrimSpace(entry[3:])}
		if isRenameOrCopyStatus(entry) && index+1 < len(entries) {
			index++
			pathValues = append(pathValues, strings.TrimSpace(entries[index]))
		}

		for _, pathValue := range pathValues {
			if pathValue == "" {
				continue
			}
			if isProgressRuntimePath(pathValue) {
				if isTrackedDeletion(entry) {
					if _, ok := seenTrackedDeletion[pathValue]; ok {
						continue
					}
					seenTrackedDeletion[pathValue] = struct{}{}
					trackedDeletionPaths = append(trackedDeletionPaths, pathValue)
				}
				continue
			}
			if _, ok := seen[pathValue]; ok {
				continue
			}
			seen[pathValue] = struct{}{}
			paths = append(paths, pathValue)
		}
	}

	return paths, trackedDeletionPaths
}

func isTrackedDeletion(entry string) bool {
	return len(entry) >= 2 && (entry[0] == 'D' || entry[1] == 'D')
}

func isRenameOrCopyStatus(entry string) bool {
	return entry[0] == 'R' || entry[0] == 'C'
}

func isProgressRuntimePath(pathValue string) bool {
	pathValue = strings.Trim(pathValue, "\"")
	parts := strings.Split(pathValue, "/")
	for index, part := range parts {
		if part != ".progress" {
			continue
		}

		suffix := strings.Trim(strings.Join(parts[index+1:], "/"), "/")
		return suffix == "" ||
			suffix == "runner-output" || strings.HasPrefix(suffix, "runner-output/") ||
			suffix == "execution-runs" || strings.HasPrefix(suffix, "execution-runs/")
	}

	return false
}

func (s *Service) upstreamBranch(ctx context.Context, dir, branch string) (string, error) {
	output, err := s.runGitOutput(ctx, dir, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("resolve git upstream: %w", err)
	}

	return strings.TrimSpace(output), nil
}

func isNoChangesAfterAddError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "nothing to commit") || strings.Contains(message, "no changes added to commit")
}

func resolveCommitMessage(input model.CommitPushInput) string {
	if message := normalizeCommitMessage(input.CommitMessage); message != "" {
		return message
	}

	if name := normalizeCommitMessage(input.FallbackName); name != "" {
		return name
	}

	if name := worktreeDirectoryName(input.Directory); name != "" {
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

	runner := strings.TrimSpace(in.Launch.Runner)
	if runner == RunnerCodex {
		return runCodexRunner(ctx, in.Launch, prompt)
	}

	cmd, err := buildRunnerCommand(ctx, in.Launch, prompt)
	if err != nil {
		return "", err
	}
	metadata := runnerMetadata{}
	opencodeTitle := ""
	if runner == RunnerOpenCode && in.Launch.Resume == nil {
		opencodeTitle = openCodeExecutionTitle()
		cmd.Args = insertOpenCodeTitle(cmd.Args, opencodeTitle)
	}
	cmd.Dir = in.Launch.Directory
	cmd.Env = sanitizedEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("launch runner failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	if opencodeTitle != "" {
		metadata.RunnerSessionID = lookupOpenCodeSessionID(ctx, opencodeTitle)
	}
	if strings.TrimSpace(metadata.RunnerSessionID) == "" && in.Launch.Resume != nil {
		metadata.RunnerSessionID = strings.TrimSpace(in.Launch.Resume.RunnerSessionID)
	}

	return appendTrailingRunnerMetadata(string(output), metadata), nil
}

func runCodexRunner(ctx context.Context, spec model.LaunchSpec, prompt string) (string, error) {
	cmd, err := buildRunnerCommand(ctx, spec, prompt)
	if err != nil {
		return "", err
	}
	cmd.Args = insertCodexJSONFlag(cmd.Args)
	cmd.Dir = spec.Directory
	cmd.Env = sanitizedEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("launch runner failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	plainOutput, sessionID := normalizeCodexJSONOutput(string(output))
	if strings.TrimSpace(sessionID) == "" && spec.Resume != nil {
		sessionID = strings.TrimSpace(spec.Resume.RunnerSessionID)
	}

	return appendTrailingRunnerMetadata(plainOutput, runnerMetadata{RunnerSessionID: sessionID}), nil
}

func insertCodexJSONFlag(args []string) []string {
	if len(args) < 2 || args[1] != "exec" {
		return args
	}
	withJSON := make([]string, 0, len(args)+1)
	withJSON = append(withJSON, args[0], args[1], "--json")
	withJSON = append(withJSON, args[2:]...)
	return withJSON
}

func normalizeCodexJSONOutput(output string) (string, string) {
	var sessionID string
	plainLines := make([]string, 0)
	messageParts := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil || strings.TrimSpace(event.Type) == "" {
			plainLines = append(plainLines, line)
			continue
		}

		if event.Type == "thread.started" && strings.TrimSpace(event.ThreadID) != "" {
			sessionID = strings.TrimSpace(event.ThreadID)
			continue
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" && strings.TrimSpace(event.Item.Text) != "" {
			messageParts = append(messageParts, strings.TrimSpace(event.Item.Text))
		}
	}

	lines := append(plainLines, messageParts...)
	if len(lines) == 0 {
		return strings.TrimSpace(output), sessionID
	}

	return strings.TrimSpace(strings.Join(lines, "\n")), sessionID
}

func insertOpenCodeTitle(args []string, title string) []string {
	title = strings.TrimSpace(title)
	if title == "" || len(args) < 2 || args[1] != "run" {
		return args
	}
	if len(args) == 2 {
		return append(args, "--title", title)
	}
	withTitle := make([]string, 0, len(args)+2)
	withTitle = append(withTitle, args[:len(args)-1]...)
	withTitle = append(withTitle, "--title", title, args[len(args)-1])
	return withTitle
}

func openCodeExecutionTitle() string {
	return fmt.Sprintf("progress-execution-%d", time.Now().UTC().UnixNano())
}

func lookupOpenCodeSessionID(ctx context.Context, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}

	for attempt := 0; attempt < 3; attempt++ {
		cmd := exec.CommandContext(ctx, RunnerOpenCode, "session", "list")
		cmd.Env = sanitizedEnv()
		output, err := cmd.CombinedOutput()
		if err == nil {
			if sessionID := parseOpenCodeSessionList(string(output), title); sessionID != "" {
				return sessionID
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return ""
}

func parseOpenCodeSessionList(output, title string) string {
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "ses_") {
			continue
		}
		if fields[1] == title {
			return strings.TrimSpace(fields[0])
		}
	}

	return ""
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
	resume := spec.Resume != nil
	sessionID := ""
	if spec.Resume != nil {
		sessionID = strings.TrimSpace(spec.Resume.RunnerSessionID)
	}

	switch runner {
	case RunnerOpenCode:
		if resume {
			if sessionID == "" {
				return nil, fmt.Errorf("%w: runner %s requires runner session id", errResumeUnsupported, runner)
			}
			args = []string{"run", "--dir", spec.Directory, "--model", spec.Model, "--session", sessionID, prompt}
		} else {
			args = []string{"run", "--dir", spec.Directory, "--model", spec.Model, prompt}
		}
	case RunnerCodex:
		if resume {
			if sessionID == "" {
				return nil, fmt.Errorf("%w: runner %s requires runner session id", errResumeUnsupported, runner)
			}
			args = []string{"exec", "resume", sessionID, prompt}
		} else {
			args = []string{"exec", "-C", spec.Directory, "-m", codexModelName(spec.Model), prompt}
		}
	default:
		if resume {
			return nil, fmt.Errorf("%w: runner %s does not support saved sessions", errResumeUnsupported, spec.Runner)
		}
		return nil, fmt.Errorf("unsupported runner: %s", spec.Runner)
	}

	return exec.CommandContext(ctx, runner, args...), nil
}

func extractRunnerSessionID(in model.Invocation, output string) string {
	_, metadata := stripTrailingRunnerMetadata(output)
	if metadata != nil && strings.TrimSpace(metadata.RunnerSessionID) != "" {
		return strings.TrimSpace(metadata.RunnerSessionID)
	}

	if in.Launch.Resume != nil {
		return strings.TrimSpace(in.Launch.Resume.RunnerSessionID)
	}

	return ""
}

func resumeParentRunID(in model.Invocation) int64 {
	if in.Launch.Resume == nil {
		return 0
	}

	return in.Launch.Resume.ParentRunID
}

func resumeMessageSource(in model.Invocation) string {
	if in.Launch.Resume == nil {
		return ""
	}

	return strings.TrimSpace(in.Launch.Resume.MessageSource)
}

func resumeMessage(in model.Invocation) string {
	if in.Launch.StructuredInput == nil {
		return ""
	}
	for _, item := range in.Launch.StructuredInput.OperationalContext {
		if strings.TrimSpace(item.Title) == "Дополнительное сообщение для возобновления" {
			return strings.TrimSpace(item.Body)
		}
	}

	return ""
}

func codexModelName(modelName string) string {
	return strings.TrimPrefix(modelName, "openai/")
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
		"Use every field from the structured input JSON below as execution context.",
		string(payload),
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
		"Use a JSON object with a non-empty summary field.",
	}

	optionalFields := structuredOutputInstructionFields(fields)
	if len(optionalFields) != 0 {
		parts = append(parts, fmt.Sprintf("Include %s when they are applicable.", strings.Join(optionalFields, ", ")))
	}
	if forms := selectedStructuredObjectForms(optionalFields); len(forms) != 0 {
		parts = append(parts, "Object forms: "+strings.Join(forms, ", ")+".")
	}
	if structuredOutputIncludesField(optionalFields, "remarks") {
		parts = append(parts, "For remarks, path, line and side are optional inline location metadata; line must be a diff line on the selected side, not an arbitrary file line. Omit path or line when no diff location is available.")
	}
	parts = append(parts, "Canonical compact JSON example: "+buildStructuredOutputCanonicalExample(optionalFields)+".")

	return strings.Join(parts, " ")
}

func selectedStructuredObjectForms(fields []string) []string {
	fieldForms := []struct {
		field string
		form  string
	}{
		{field: "remarks", form: "remarks[{id,status,severity,type,title,body,path,line,side,answer,resolution}]"},
		{field: "review_responses", form: "review_responses[{id,remark_id,thread_id,status,summary,body}]"},
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

func structuredOutputIncludesField(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}

	return false
}

func buildStructuredOutputCanonicalExample(fields []string) string {
	parts := []string{
		`{"summary":"Implemented changes."`,
	}
	for _, field := range fields {
		switch field {
		case "commit_message":
			parts = append(parts, `,"commit_message":"Apply requested review fixes"`)
		case "remarks":
			parts = append(parts, `,"remarks":[{"id":"remark-1","title":"Rollback plan","path":"internal/service.go","line":42,"side":"RIGHT"}]`)
		case "review_responses":
			parts = append(parts, `,"review_responses":[{"remark_id":"remark-1","thread_id":"thread-1","status":"resolved","summary":"Fixed"}]`)
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
			parts = append(parts, `,"extensions":{"custom":{"owner":"structured-output-test"}}`)
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
		"review_responses":  {},
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

func runGitOutputEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
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
