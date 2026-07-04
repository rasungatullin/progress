package model

import "encoding/json"

type LaunchSpec struct {
	Directory                string           `json:"directory,omitempty"`
	Runner                   string           `json:"runner,omitempty"`
	Model                    string           `json:"model,omitempty"`
	ModelBinding             string           `json:"model_binding,omitempty"`
	Resume                   *ResumeSpec      `json:"resume,omitempty"`
	Prompt                   string           `json:"prompt,omitempty"`
	PromptAdditions          []string         `json:"prompt_additions,omitempty"`
	StructuredInput          *StructuredInput `json:"structured_input,omitempty"`
	StructuredOutput         bool             `json:"structured_output,omitempty"`
	StructuredOutputRequired bool             `json:"structured_output_required,omitempty"`
	StructuredOutputFields   []string         `json:"structured_output_fields,omitempty"`
	CommitPush               bool             `json:"commit_push,omitempty"`
}

type StructuredExtensions map[string]json.RawMessage

type StructuredInput struct {
	Task               string               `json:"task,omitempty"`
	Constraints        []string             `json:"constraints,omitempty"`
	ProjectContext     []StructuredContext  `json:"project_context,omitempty"`
	OperationalContext []StructuredContext  `json:"operational_context,omitempty"`
	PreviousRunResults []StructuredResult   `json:"previous_run_results,omitempty"`
	ReviewRemarks      []StructuredRemark   `json:"review_remarks,omitempty"`
	ReviewResponses    []StructuredResponse `json:"review_responses,omitempty"`
	IntegrationActions []StructuredAction   `json:"integration_actions,omitempty"`
	Extensions         StructuredExtensions `json:"extensions,omitempty"`
}

type StructuredOutput struct {
	Summary         string                `json:"summary,omitempty"`
	CommitMessage   string                `json:"commit_message,omitempty"`
	Remarks         []StructuredRemark    `json:"remarks,omitempty"`
	Questions       []StructuredQuestion  `json:"questions,omitempty"`
	FollowUpActions []StructuredAction    `json:"follow_up_actions,omitempty"`
	Changes         []StructuredChange    `json:"changes,omitempty"`
	Commands        []StructuredCommand   `json:"commands,omitempty"`
	Conclusion      *StructuredConclusion `json:"conclusion,omitempty"`
	Extensions      StructuredExtensions  `json:"extensions,omitempty"`
}

type StructuredContext struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type StructuredResult struct {
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body,omitempty"`
}

type StructuredRemark struct {
	ID         string `json:"id,omitempty"`
	Status     string `json:"status,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Type       string `json:"type,omitempty"`
	Title      string `json:"title,omitempty"`
	Body       string `json:"body,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

type StructuredResponse struct {
	ID       string `json:"id,omitempty"`
	RemarkID string `json:"remark_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Body     string `json:"body,omitempty"`
}

type StructuredQuestion struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Answer string `json:"answer,omitempty"`
}

type StructuredAction struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
	Type   string `json:"type,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
}

type StructuredChange struct {
	Summary string `json:"summary,omitempty"`
}

type StructuredCommand struct {
	Name  string   `json:"name,omitempty"`
	Args  []string `json:"args,omitempty"`
	Title string   `json:"title,omitempty"`
	Body  string   `json:"body,omitempty"`
}

type StructuredConclusion struct {
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body,omitempty"`
}

type ResumeSpec struct {
	ParentRunID     int64  `json:"parent_run_id,omitempty"`
	RunnerSessionID string `json:"runner_session_id,omitempty"`
	MessageSource   string `json:"message_source,omitempty"`
}

type WorkplaceSpec struct {
	Name string `json:"name,omitempty"`
}

type RepositorySpec struct {
	URL string `json:"url,omitempty"`
}

type AssignmentReason struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type ObjectRef struct {
	Type       string            `json:"type,omitempty"`
	System     string            `json:"system,omitempty"`
	Repository string            `json:"repository,omitempty"`
	Number     int               `json:"number,omitempty"`
	ID         string            `json:"id,omitempty"`
	Title      string            `json:"title,omitempty"`
	URL        string            `json:"url,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type ExecutionAssignment struct {
	Action          string             `json:"action,omitempty"`
	Profile         string             `json:"profile,omitempty"`
	ExpectedResult  string             `json:"expected_result,omitempty"`
	Constraints     []string           `json:"constraints,omitempty"`
	CanonicalTask   *ObjectRef         `json:"canonical_task,omitempty"`
	RelatedObjects  []ObjectRef        `json:"related_objects,omitempty"`
	Reasons         []AssignmentReason `json:"reasons,omitempty"`
	StructuredInput *StructuredInput   `json:"structured_input,omitempty"`
}

type Invocation struct {
	Task       string               `json:"task,omitempty"`
	Action     string               `json:"action,omitempty"`
	Assignment *ExecutionAssignment `json:"assignment,omitempty"`
	Profile    string               `json:"profile,omitempty"`
	Repository RepositorySpec       `json:"repository,omitempty"`
	Workplace  WorkplaceSpec        `json:"workplace,omitempty"`
	Launch     LaunchSpec           `json:"launch,omitempty"`
}

type Profile struct {
	Name                     string   `json:"name,omitempty"`
	Description              string   `json:"description,omitempty"`
	Mode                     string   `json:"mode,omitempty"`
	ModelBinding             string   `json:"model_binding,omitempty"`
	AllowModelFallback       bool     `json:"allow_model_fallback,omitempty"`
	PromptAdditions          []string `json:"prompt_additions,omitempty"`
	StructuredOutput         bool     `json:"structured_output,omitempty"`
	StructuredOutputRequired bool     `json:"structured_output_required,omitempty"`
	StructuredOutputFields   []string `json:"structured_output_fields,omitempty"`
}

type ProfileConfigFile struct {
	Defaults ProfileOptions           `json:"defaults"`
	Profiles map[string]ProfileConfig `json:"profiles"`
}

type ProfileOptions struct {
	Mode                     string    `json:"mode"`
	ModelBinding             string    `json:"model-binding"`
	AllowModelFallback       *bool     `json:"allow-model-fallback"`
	PromptAdditions          *[]string `json:"prompt-additions"`
	StructuredOutput         *bool     `json:"structured-output"`
	StructuredOutputRequired *bool     `json:"structured-output-required"`
	StructuredOutputFields   *[]string `json:"structured-output-fields"`
}

type ProfileConfig struct {
	Description              string    `json:"description"`
	Mode                     string    `json:"mode"`
	ModelBinding             string    `json:"model-binding"`
	AllowModelFallback       *bool     `json:"allow-model-fallback"`
	PromptAdditions          *[]string `json:"prompt-additions"`
	StructuredOutput         *bool     `json:"structured-output"`
	StructuredOutputRequired *bool     `json:"structured-output-required"`
	StructuredOutputFields   *[]string `json:"structured-output-fields"`
}

type Allocation struct {
	Resource         string `json:"resource,omitempty"`
	Reserved         bool   `json:"reserved,omitempty"`
	Runner           string `json:"runner,omitempty"`
	Model            string `json:"model,omitempty"`
	ModelBinding     string `json:"model_binding,omitempty"`
	BindingSource    string `json:"binding_source,omitempty"`
	Source           string `json:"source,omitempty"`
	FallbackUsed     bool   `json:"fallback_used,omitempty"`
	GlobalConfigPath string `json:"global_config_path,omitempty"`
	LocalConfigPath  string `json:"local_config_path,omitempty"`
}

type ResourceConfigFile struct {
	Defaults ResourceDefaultsConfig           `json:"defaults"`
	Runners  []string                         `json:"runners"`
	Models   []string                         `json:"models"`
	Bindings map[string]ResourceBindingConfig `json:"bindings"`
}

type ResourceBindingConfig struct {
	Runner string `json:"runner"`
	Model  string `json:"model"`
}

type ResourceDefaultsConfig struct {
	ModelBinding string `json:"model-binding"`
}

type Workplace struct {
	Name           string `json:"name,omitempty"`
	RepositoryURL  string `json:"repository_url,omitempty"`
	RepositoryRoot string `json:"repository_root,omitempty"`
	Ready          bool   `json:"ready,omitempty"`
}

type ActionClass string

type OperationKind string

type OperationStatus string

type Failure struct {
	Code               string `json:"code,omitempty"`
	Message            string `json:"message,omitempty"`
	Retryable          bool   `json:"retryable,omitempty"`
	ManualIntervention bool   `json:"manual_intervention,omitempty"`
}

type Action struct {
	Name              string          `json:"name,omitempty"`
	Class             ActionClass     `json:"class,omitempty"`
	Profile           string          `json:"profile,omitempty"`
	ExpectedResult    string          `json:"expected_result,omitempty"`
	RequiresWorkplace bool            `json:"requires_workplace,omitempty"`
	RequiresSynthesis bool            `json:"requires_synthesis,omitempty"`
	Operations        []OperationSpec `json:"operations,omitempty"`
}

type OperationSpec struct {
	Name     string        `json:"name,omitempty"`
	Kind     OperationKind `json:"kind,omitempty"`
	Title    string        `json:"title,omitempty"`
	Origin   string        `json:"origin,omitempty"`
	Required bool          `json:"required,omitempty"`
}

type OperationResult struct {
	Name     string          `json:"name,omitempty"`
	Kind     OperationKind   `json:"kind,omitempty"`
	Title    string          `json:"title,omitempty"`
	Origin   string          `json:"origin,omitempty"`
	Required bool            `json:"required,omitempty"`
	Input    string          `json:"input,omitempty"`
	Output   string          `json:"output,omitempty"`
	Status   OperationStatus `json:"status,omitempty"`
	Summary  string          `json:"summary,omitempty"`
	Failure  *Failure        `json:"failure,omitempty"`
}

type Artifact struct {
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

type DiagnosticLink struct {
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

type LaunchResult struct {
	Status              string            `json:"status,omitempty"`
	Summary             string            `json:"summary,omitempty"`
	RawOutputPath       string            `json:"raw_output_path,omitempty"`
	RawStructuredOutput string            `json:"raw_structured_output,omitempty"`
	StructuredOutput    *StructuredOutput `json:"structured_output,omitempty"`
	RunnerSessionID     string            `json:"runner_session_id,omitempty"`
	RunRecordPath       string            `json:"run_record_path,omitempty"`
}

type ExecutionResult struct {
	Status          string               `json:"status,omitempty"`
	Summary         string               `json:"summary,omitempty"`
	Assignment      *ExecutionAssignment `json:"assignment,omitempty"`
	Action          Action               `json:"action,omitempty"`
	Operations      []OperationResult    `json:"operations,omitempty"`
	Artifacts       []Artifact           `json:"artifacts,omitempty"`
	DiagnosticLinks []DiagnosticLink     `json:"diagnostic_links,omitempty"`
	Launch          *LaunchResult        `json:"launch,omitempty"`
	Failure         *Failure             `json:"failure,omitempty"`
}
