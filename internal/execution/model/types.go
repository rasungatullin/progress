package model

import "encoding/json"

type LaunchSpec struct {
	Directory                string           `json:"directory,omitempty"`
	Runner                   string           `json:"runner,omitempty"`
	Model                    string           `json:"model,omitempty"`
	ModelBinding             string           `json:"model_binding,omitempty"`
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

type WorkplaceSpec struct {
	Name string `json:"name,omitempty"`
}

type RepositorySpec struct {
	URL string `json:"url,omitempty"`
}

type Invocation struct {
	Task       string         `json:"task,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Repository RepositorySpec `json:"repository,omitempty"`
	Workplace  WorkplaceSpec  `json:"workplace,omitempty"`
	Launch     LaunchSpec     `json:"launch,omitempty"`
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
	CommitPush               bool     `json:"commit_push,omitempty"`
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
	CommitPush               *bool     `json:"commit-push"`
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
	CommitPush               *bool     `json:"commit-push"`
}

type Allocation struct {
	Resource     string `json:"resource,omitempty"`
	Reserved     bool   `json:"reserved,omitempty"`
	Runner       string `json:"runner,omitempty"`
	Model        string `json:"model,omitempty"`
	ModelBinding string `json:"model_binding,omitempty"`
	Source       string `json:"source,omitempty"`
	FallbackUsed bool   `json:"fallback_used,omitempty"`
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

type LaunchResult struct {
	Status           string            `json:"status,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	RawOutputPath    string            `json:"raw_output_path,omitempty"`
	StructuredOutput *StructuredOutput `json:"structured_output,omitempty"`
	RunRecordPath    string            `json:"run_record_path,omitempty"`
}
