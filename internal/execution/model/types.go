package model

type LaunchSpec struct {
	Directory       string
	Runner          string
	Model           string
	Prompt          string
	StructuredInput *ReviewCycleEnvelope
	CommitPush      bool
	CommitMessage   string
}

const ReviewCycleProtocolVersion = "review-cycle/v1"

const (
	ReviewCycleModeReview   = "review"
	ReviewCycleModeReply    = "reply"
	ReviewCycleModeFix      = "fix"
	ReviewCycleModeReReview = "re-review"
)

type ReviewCycleEnvelope struct {
	ProtocolVersion string                `json:"protocol_version,omitempty"`
	Mode            string                `json:"mode,omitempty"`
	Summary         string                `json:"summary,omitempty"`
	Remarks         []ReviewCycleRemark   `json:"remarks,omitempty"`
	Questions       []ReviewCycleQuestion `json:"questions,omitempty"`
	FollowUpActions []ReviewCycleAction   `json:"follow_up_actions,omitempty"`
	Changes         []ReviewCycleChange   `json:"changes,omitempty"`
}

type ReviewCycleRemark struct {
	ID             string `json:"id,omitempty"`
	Status         string `json:"status,omitempty"`
	ResponseStatus string `json:"response_status,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Type           string `json:"type,omitempty"`
	Title          string `json:"title,omitempty"`
	Body           string `json:"body,omitempty"`
	Reply          string `json:"reply,omitempty"`
	FixSummary     string `json:"fix_summary,omitempty"`
}

type ReviewCycleQuestion struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
	Reply  string `json:"reply,omitempty"`
}

type ReviewCycleAction struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
	Type   string `json:"type,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body,omitempty"`
}

type ReviewCycleChange struct {
	Summary string `json:"summary,omitempty"`
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
	Status          string
	Summary         string
	ReviewCycle     *ReviewCycleEnvelope
	CriticalRemarks []string
	MinorRemarks    []string
	Questions       []string
}
