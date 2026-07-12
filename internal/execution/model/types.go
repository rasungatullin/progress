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
	Timeout                  string           `json:"timeout,omitempty"`
	NoOutputTimeout          string           `json:"no_output_timeout,omitempty"`
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
	ReviewResponses []StructuredResponse  `json:"review_responses,omitempty"`
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
	ExternalID string `json:"external_id,omitempty"`
	ThreadID   string `json:"thread_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Type       string `json:"type,omitempty"`
	Title      string `json:"title,omitempty"`
	Body       string `json:"body,omitempty"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Side       string `json:"side,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

type StructuredResponse struct {
	ID       string `json:"id,omitempty"`
	RemarkID string `json:"remark_id,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	Type     string `json:"type,omitempty"`
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
	Name        string `json:"name,omitempty"`
	Environment string `json:"environment,omitempty"`
	BaseRef     string `json:"base_ref,omitempty"`
	HeadRef     string `json:"head_ref,omitempty"`
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
	ExpectedResult  string             `json:"expected_result,omitempty"`
	Constraints     []string           `json:"constraints,omitempty"`
	CanonicalTask   *ObjectRef         `json:"canonical_task,omitempty"`
	RelatedObjects  []ObjectRef        `json:"related_objects,omitempty"`
	Reasons         []AssignmentReason `json:"reasons,omitempty"`
	StructuredInput *StructuredInput   `json:"structured_input,omitempty"`
}

type ActionInvocation struct {
	Assignment *ExecutionAssignment `json:"assignment,omitempty"`
}

type OperationInvocation struct {
	Operation  string               `json:"operation,omitempty"`
	Assignment *ExecutionAssignment `json:"assignment,omitempty"`
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
	Timeout                  string   `json:"timeout,omitempty"`
	NoOutputTimeout          string   `json:"no_output_timeout,omitempty"`
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
	Timeout                  string    `json:"timeout"`
	NoOutputTimeout          string    `json:"no-output-timeout"`
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
	Timeout                  string    `json:"timeout"`
	NoOutputTimeout          string    `json:"no-output-timeout"`
}

type Allocation struct {
	Resource         string                     `json:"resource,omitempty"`
	Reserved         bool                       `json:"reserved,omitempty"`
	Runner           string                     `json:"runner,omitempty"`
	Model            string                     `json:"model,omitempty"`
	ModelBinding     string                     `json:"model_binding,omitempty"`
	Environment      string                     `json:"environment,omitempty"`
	EnvironmentType  string                     `json:"environment_type,omitempty"`
	BindingSource    string                     `json:"binding_source,omitempty"`
	Source           string                     `json:"source,omitempty"`
	FallbackUsed     bool                       `json:"fallback_used,omitempty"`
	GlobalConfigPath string                     `json:"global_config_path,omitempty"`
	LocalConfigPath  string                     `json:"local_config_path,omitempty"`
	ConfigHome       string                     `json:"-"`
	PrivateStore     ResourcePrivateStoreConfig `json:"-"`
	Git              *GitConfig                 `json:"-"`
}

type ResourceConfigFile struct {
	Defaults     ResourceDefaultsConfig           `json:"defaults"`
	PrivateStore ResourcePrivateStoreConfig       `json:"private_store,omitempty"`
	Runners      []string                         `json:"runners,omitempty"`
	Models       []string                         `json:"models,omitempty"`
	Bindings     map[string]ResourceBindingConfig `json:"bindings,omitempty"`
	Git          *GitConfig                       `json:"git,omitempty"`

	Environments map[string]EnvironmentConfig `json:"environments,omitempty"`
	Tools        map[string]ToolConfig        `json:"tools,omitempty"`
	Resources    map[string]ResourceConfig    `json:"resources,omitempty"`
}

type ResourcePrivateStoreConfig struct {
	Type    string `json:"type,omitempty"`
	Service string `json:"service,omitempty"`
	Path    string `json:"path,omitempty"`
}

type GitConfig struct {
	Identity *GitIdentityConfig `json:"identity,omitempty"`
	Signing  *GitSigningConfig  `json:"signing,omitempty"`
	Push     *GitPushConfig     `json:"push,omitempty"`
}

type GitIdentityConfig struct {
	AuthorName     string `json:"author-name,omitempty"`
	AuthorEmail    string `json:"author-email,omitempty"`
	CommitterName  string `json:"committer-name,omitempty"`
	CommitterEmail string `json:"committer-email,omitempty"`
}

type GitSigningConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	Format     string `json:"format,omitempty"`
	SigningKey string `json:"signing-key,omitempty"`
	Program    string `json:"program,omitempty"`
}

type GitPushConfig struct {
	SSHIdentityFile         string `json:"ssh-identity-file,omitempty"`
	SSHIdentityPrivate      string `json:"ssh-identity-private,omitempty"`
	SSHIdentityPrivateValue string `json:"-"`
	KnownHostsFile          string `json:"known-hosts-file,omitempty"`
	IdentitiesOnly          bool   `json:"identities-only,omitempty"`
}

type CommitPushInput struct {
	Directory     string                     `json:"directory,omitempty"`
	CommitMessage string                     `json:"commit_message,omitempty"`
	FallbackName  string                     `json:"fallback_name,omitempty"`
	Git           *GitConfig                 `json:"-"`
	PrivateStore  ResourcePrivateStoreConfig `json:"-"`
	ConfigHome    string                     `json:"-"`
}

type ResourceBindingConfig struct {
	Runner      string `json:"runner,omitempty"`
	Model       string `json:"model,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Resource    string `json:"resource,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type EnvironmentConfig struct {
	Type    string            `json:"type,omitempty"`
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config,omitempty"`
}

type ToolConfig struct {
	Type    string            `json:"type,omitempty"`
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config,omitempty"`
}

type ResourceConfig struct {
	Type    string            `json:"type,omitempty"`
	Enabled bool              `json:"enabled"`
	Tools   []string          `json:"tools,omitempty"`
	Traits  []string          `json:"traits,omitempty"`
	Config  map[string]string `json:"config,omitempty"`
}

type ResourceDefaultsConfig struct {
	ModelBinding string `json:"model-binding"`
	Environment  string `json:"environment,omitempty"`
}

type Workplace struct {
	Name            string `json:"name,omitempty"`
	BaseRef         string `json:"base_ref,omitempty"`
	HeadRef         string `json:"head_ref,omitempty"`
	Environment     string `json:"environment,omitempty"`
	EnvironmentType string `json:"environment_type,omitempty"`
	RepositoryURL   string `json:"repository_url,omitempty"`
	RepositoryRoot  string `json:"repository_root,omitempty"`
	Ready           bool   `json:"ready,omitempty"`
}

type ActionClass string

type OperationKind string

type OperationType string

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
	Operations        []OperationSpec `json:"operations,omitempty"`
	OutputFields      []string        `json:"output_fields,omitempty"`
	RequiredOut       []string        `json:"required_out,omitempty"`
}

type OperationSpec struct {
	Name       string        `json:"name,omitempty"`
	Type       OperationType `json:"type,omitempty"`
	Kind       OperationKind `json:"kind,omitempty"`
	Title      string        `json:"title,omitempty"`
	In         OperationMap  `json:"in,omitempty"`
	Out        OperationMap  `json:"out,omitempty"`
	Required   bool          `json:"required,omitempty"`
	RequiredIn []string      `json:"required_in,omitempty"`
}

type OperationMap map[string]OperationMapping

type OperationMapping struct {
	Ref   string          `json:"ref,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type OperationResult struct {
	Name       string            `json:"name,omitempty"`
	Type       OperationType     `json:"type,omitempty"`
	Kind       OperationKind     `json:"kind,omitempty"`
	Title      string            `json:"title,omitempty"`
	Required   bool              `json:"required,omitempty"`
	Input      string            `json:"input,omitempty"`
	Output     string            `json:"output,omitempty"`
	Status     OperationStatus   `json:"status,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Failure    *Failure          `json:"failure,omitempty"`
	Operations []OperationResult `json:"operations,omitempty"`
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
	Status              string              `json:"status,omitempty"`
	Summary             string              `json:"summary,omitempty"`
	RawOutput           string              `json:"raw_output,omitempty"`
	RawOutputPath       string              `json:"raw_output_path,omitempty"`
	RawStructuredOutput string              `json:"raw_structured_output,omitempty"`
	StructuredOutput    *StructuredOutput   `json:"structured_output,omitempty"`
	RunnerSessionID     string              `json:"runner_session_id,omitempty"`
	RunRecordPath       string              `json:"run_record_path,omitempty"`
	WorktreeDiagnostic  *WorktreeDiagnostic `json:"worktree_diagnostic,omitempty"`
}

type WorktreeDiagnostic struct {
	DirtyWorktree  bool     `json:"dirty_worktree"`
	ChangedPaths   []string `json:"changed_paths,omitempty"`
	Path           string   `json:"path,omitempty"`
	Branch         string   `json:"branch,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type ExecutionResult struct {
	Status          string               `json:"status,omitempty"`
	Summary         string               `json:"summary,omitempty"`
	Assignment      *ExecutionAssignment `json:"assignment,omitempty"`
	Action          Action               `json:"action,omitempty"`
	MergeRequest    *MergeRequest        `json:"merge_request,omitempty"`
	Operations      []OperationResult    `json:"operations,omitempty"`
	Artifacts       []Artifact           `json:"artifacts,omitempty"`
	DiagnosticLinks []DiagnosticLink     `json:"diagnostic_links,omitempty"`
	Launch          *LaunchResult        `json:"launch,omitempty"`
	Failure         *Failure             `json:"failure,omitempty"`
}

type MergeRequest struct {
	System     string `json:"system,omitempty"`
	Repository string `json:"repository,omitempty"`
	Number     int    `json:"number,omitempty"`
	Title      string `json:"title,omitempty"`
	Body       string `json:"body,omitempty"`
	State      string `json:"state,omitempty"`
	BaseRef    string `json:"base_ref,omitempty"`
	HeadRef    string `json:"head_ref,omitempty"`
	URL        string `json:"url,omitempty"`
}
