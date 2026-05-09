package model

type LaunchSpec struct {
	Directory     string
	Runner        string
	Model         string
	Prompt        string
	CommitPush    bool
	CommitMessage string
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
	CriticalRemarks []string
	MinorRemarks    []string
	Questions       []string
}
