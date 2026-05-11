package model

import "encoding/json"

type LaunchSpec struct {
	Directory                string
	Runner                   string
	Model                    string
	Prompt                   string
	StructuredInput          *StructuredInput
	StructuredOutput         bool
	StructuredOutputRequired bool
	CommitPush               bool
	CommitMessage            string
}

const StructuredIOVersion = "review-cycle/v1"

type StructuredExtensions map[string]json.RawMessage

type StructuredInput struct {
	ProtocolVersion    string               `json:"protocol_version,omitempty"`
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
	ProtocolVersion string                `json:"protocol_version,omitempty"`
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
	Name string
}

type Invocation struct {
	Task      string
	Profile   string
	Workplace WorkplaceSpec
	Launch    LaunchSpec
}

type Profile struct {
	Name        string
	Description string
	Mode        string
	Model       string
	CommitPush  bool
}

type ProfileConfigFile struct {
	Defaults ProfileOptions           `json:"defaults"`
	Profiles map[string]ProfileConfig `json:"profiles"`
}

type ProfileOptions struct {
	Mode       string `json:"mode"`
	Model      string `json:"model"`
	CommitPush *bool  `json:"commit-push"`
}

type ProfileConfig struct {
	Description string `json:"description"`
	Mode        string `json:"mode"`
	Model       string `json:"model"`
	CommitPush  *bool  `json:"commit-push"`
}

type Allocation struct {
	Resource string
	Reserved bool
}

type Workplace struct {
	Name  string
	Ready bool
}

type LaunchResult struct {
	Status           string
	Summary          string
	StructuredOutput *StructuredOutput
}
