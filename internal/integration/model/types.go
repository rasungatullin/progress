package model

type Request struct {
	System       string
	Resource     string
	Operation    string
	Repository   string
	RepoProvided bool
	Number       int
	Base         string
	Head         string
	Title        string
	Body         string
	Draft        bool
	Query        string
	Limit        int
}

type ProviderRequest struct {
	System       string
	Resource     string
	Operation    string
	Repository   string
	RepoProvided bool
	Number       int
	Base         string
	Head         string
	Title        string
	Body         string
	Draft        bool
	Query        string
	Limit        int
	Route        Route
}

type IntegrationConfigFile struct {
	DefaultSystem string                             `json:"default_system"`
	Systems       map[string]IntegrationSystemConfig `json:"systems"`
}

type IntegrationSystemConfig struct {
	Type        string                                `json:"type"`
	Enabled     *bool                                 `json:"enabled,omitempty"`
	Command     string                                `json:"command,omitempty"`
	Path        string                                `json:"path,omitempty"`
	Timeout     string                                `json:"timeout,omitempty"`
	DefaultRepo string                                `json:"default_repo,omitempty"`
	Operations  map[string]IntegrationOperationConfig `json:"operations,omitempty"`
}

type IntegrationOperationConfig struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	Timeout string `json:"timeout,omitempty"`
	Script  string `json:"script,omitempty"`
}

type Response struct {
	System            string
	Resource          string
	Operation         string
	Route             Route
	AuthStatus        *AuthStatus
	RepositoryStatus  *RepositoryStatus
	IssueStatus       *IssueStatus
	PullRequestStatus *PullRequestStatus
	Issue             *TrackerIssue
	PullRequest       *TrackerPullRequest
	Comments          []TrackerComment
	Reviews           []TrackerReview
	RepositoryRef     *TrackerRepository
	SearchResults     []TrackerSearchResult
	Artifacts         []Artifact
	Metadata          map[string]string
}

type AuthStatus struct {
	System        string
	State         string
	Available     bool
	Authenticated bool
	Command       string
	Path          string
	ExitCode      int
	Message       string
	Diagnostics   []string
	Stdout        string
	Stderr        string
}

type RepositoryStatus struct {
	System      string
	Repository  string
	State       string
	Command     string
	Path        string
	ExitCode    int
	Message     string
	Diagnostics []string
	Stdout      string
	Stderr      string
}

type IssueStatus struct {
	System      string
	Repository  string
	Number      int
	State       string
	Command     string
	Path        string
	ExitCode    int
	Message     string
	Diagnostics []string
	Stdout      string
	Stderr      string
}

type PullRequestStatus struct {
	System      string
	Repository  string
	Base        string
	Head        string
	Title       string
	Draft       bool
	Number      int
	State       string
	URL         string
	Command     string
	Path        string
	ExitCode    int
	Message     string
	Diagnostics []string
	Stdout      string
	Stderr      string
}

type Route struct {
	System            string
	Provider          string
	ProviderAvailable bool
	Resource          string
	Operation         string
	ExpectedResult    string
	Diagnostics       []string
}

type TrackerIssue struct {
	System     string
	Repository string
	Number     int
	Title      string
	Body       string
	State      string
	Labels     []string
	Assignees  []TrackerUser
	Author     TrackerUser
	URL        string
	CreatedAt  string
	UpdatedAt  string
}

type TrackerPullRequest struct {
	System         string
	Repository     string
	Number         int
	Title          string
	Body           string
	State          string
	Author         TrackerUser
	ReviewDecision string
	BaseRef        string
	HeadRef        string
	Labels         []string
	URL            string
	CreatedAt      string
	UpdatedAt      string
}

type TrackerComment struct {
	System     string
	Repository string
	Number     int
	Author     TrackerUser
	Body       string
	URL        string
	CreatedAt  string
	UpdatedAt  string
}

type TrackerReview struct {
	System     string
	Repository string
	Number     int
	Author     TrackerUser
	State      string
	Body       string
	URL        string
	CreatedAt  string
	UpdatedAt  string
}

type TrackerRepository struct {
	System        string
	FullName      string
	Owner         string
	Name          string
	Description   string
	DefaultBranch string
	URL           string
}

type TrackerUser struct {
	System   string
	Login    string
	Name     string
	Email    string
	URL      string
	IsBot    bool
	IsActive bool
}

type TrackerSearchResult struct {
	System     string
	Repository string
	Kind       string
	Number     int
	Title      string
	State      string
	URL        string
	UpdatedAt  string
}

type Artifact struct {
	System string
	Kind   string
	Name   string
	URL    string
}
