package model

const (
	IntegrationTypeTracker    = "tracker"
	IntegrationTypeRepository = "repository"
	IntegrationTypeMessenger  = "messenger"
	IntegrationTypeWiki       = "wiki"

	ResponseStatusOK      = "ok"
	ResponseStatusPartial = "partial"
	ResponseStatusFailed  = "failed"

	FailureKindNotConfigured        = "not-configured"
	FailureKindAuthRequired         = "auth-required"
	FailureKindPermissionDenied     = "permission-denied"
	FailureKindNotFound             = "not-found"
	FailureKindTemporaryUnavailable = "temporary-unavailable"
	FailureKindRateLimited          = "rate-limited"
	FailureKindUnsupportedOperation = "unsupported-operation"
	FailureKindPartialResponse      = "partial-response"
	FailureKindStateConflict        = "state-conflict"
	FailureKindInvalidRequest       = "invalid-request"
	FailureKindExternalFailure      = "external-failure"
)

type Request struct {
	IntegrationType string
	System          string
	SystemProvided  bool
	Resource        string
	ObjectType      string
	Operation       string
	Repository      string
	RepoProvided    bool
	Number          int
	ExternalID      string
	Base            string
	Head            string
	Title           string
	Body            string
	Text            string
	Draft           bool
	Query           string
	State           string
	Scope           string
	Limit           int
	Path            string
	Line            int
	Side            string
	ChannelID       string
	ThreadID        string
	MessageID       string
	Reaction        string
	Fields          []string
	Labels          []string
}

type ProviderRequest struct {
	IntegrationType string
	System          string
	SystemProvided  bool
	Resource        string
	ObjectType      string
	Operation       string
	Repository      string
	RepoProvided    bool
	Number          int
	ExternalID      string
	Base            string
	Head            string
	Title           string
	Body            string
	Text            string
	Draft           bool
	Query           string
	State           string
	Scope           string
	Limit           int
	Path            string
	Line            int
	Side            string
	ChannelID       string
	ThreadID        string
	MessageID       string
	Reaction        string
	Fields          []string
	Labels          []string
	Route           Route
}

type IntegrationConfigFile struct {
	DefaultSystem  string                             `json:"default_system"`
	DefaultSystems map[string]string                  `json:"default_systems,omitempty"`
	Systems        map[string]IntegrationSystemConfig `json:"systems"`
}

type IntegrationSystemConfig struct {
	Type             string                                `json:"type"`
	IntegrationType  string                                `json:"integration_type,omitempty"`
	IntegrationTypes []string                              `json:"integration_types,omitempty"`
	Default          bool                                  `json:"default,omitempty"`
	Enabled          *bool                                 `json:"enabled,omitempty"`
	Command          string                                `json:"command,omitempty"`
	Path             string                                `json:"path,omitempty"`
	Timeout          string                                `json:"timeout,omitempty"`
	BaseURL          string                                `json:"base_url,omitempty"`
	APIVariant       string                                `json:"api_variant,omitempty"`
	Token            string                                `json:"token,omitempty"`
	TokenEnv         string                                `json:"token_env,omitempty"`
	Username         string                                `json:"username,omitempty"`
	Repository       string                                `json:"repository,omitempty"`
	Workspace        string                                `json:"workspace,omitempty"`
	Project          string                                `json:"project,omitempty"`
	DefaultRepo      string                                `json:"default_repo,omitempty"`
	ChannelID        string                                `json:"channel_id,omitempty"`
	ChatID           string                                `json:"chat_id,omitempty"`
	TaskLabelMapping map[string]string                     `json:"task_label_mapping,omitempty"`
	Operations       map[string]IntegrationOperationConfig `json:"operations,omitempty"`
}

type IntegrationOperationConfig struct {
	Type     string            `json:"type,omitempty"`
	Command  string            `json:"command,omitempty"`
	Path     string            `json:"path,omitempty"`
	Timeout  string            `json:"timeout,omitempty"`
	Script   string            `json:"script,omitempty"`
	Required []string          `json:"required,omitempty"`
	Optional []string          `json:"optional,omitempty"`
	Defaults map[string]string `json:"defaults,omitempty"`
}

type Response struct {
	IntegrationType   string
	System            string
	Resource          string
	ObjectType        string
	Operation         string
	Status            string
	Partial           bool
	Failure           *Failure
	Route             Route
	Task              *CanonicalTask
	TaskComments      []TaskComment
	Repository        *Repository
	MergeRequest      *MergeRequest
	MergeRequests     []MergeRequest
	ReviewRemarks     []ReviewRemark
	Conversation      *MessageThread
	Messages          []Message
	Message           *Message
	WikiPage          *WikiPage
	WikiPages         []WikiPage
	OperationResult   *OperationResult
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

type Failure struct {
	Kind        string
	Retryable   bool
	Message     string
	Diagnostics []string
}

type OperationResult struct {
	System       string
	ObjectType   string
	Operation    string
	Status       string
	ExternalID   string
	URL          string
	HTTPStatus   int
	Method       string
	Endpoint     string
	Idempotent   bool
	Message      string
	Diagnostics  []string
	Failure      *Failure
	ResponseBody string
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
	IntegrationType   string
	System            string
	Provider          string
	ProviderType      string
	ProviderAvailable bool
	Resource          string
	ObjectType        string
	Operation         string
	ExpectedResult    string
	Diagnostics       []string
}

type OperationFilter struct {
	System          string
	IntegrationType string
	Name            string
}

type OperationDescriptor struct {
	Name            string                  `json:"name"`
	IntegrationType string                  `json:"integration_type"`
	System          string                  `json:"system"`
	AdapterType     string                  `json:"adapter_type"`
	ObjectType      string                  `json:"object_type"`
	Operation       string                  `json:"operation"`
	Enabled         bool                    `json:"enabled"`
	Available       bool                    `json:"available"`
	SideEffect      bool                    `json:"side_effect"`
	DryRunSupported bool                    `json:"dry_run_supported"`
	Input           OperationInputContract  `json:"input"`
	Output          OperationOutputContract `json:"output"`
	FailureKinds    []string                `json:"failure_kinds,omitempty"`
	Diagnostics     []string                `json:"diagnostics,omitempty"`
}

type OperationInputContract struct {
	Required []OperationField `json:"required,omitempty"`
	Optional []OperationField `json:"optional,omitempty"`
}

type OperationField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
	Repeated    bool   `json:"repeated,omitempty"`
}

type OperationOutputContract struct {
	Resource string `json:"resource"`
	Shape    string `json:"shape"`
}

type CanonicalTask struct {
	System     string
	Repository string
	Number     int
	ExternalID string
	Title      string
	Body       string
	State      string
	Traits     []string
	Attributes map[string]string
	Assignees  []User
	Author     User
	URL        string
	CreatedAt  string
	UpdatedAt  string
	Links      []ObjectLink
}

type TaskComment struct {
	System     string
	Repository string
	TaskNumber int
	ExternalID string
	Author     User
	Body       string
	URL        string
	CreatedAt  string
	UpdatedAt  string
}

type Repository struct {
	System        string
	ExternalID    string
	FullName      string
	Owner         string
	Name          string
	Description   string
	DefaultBranch string
	URL           string
	Traits        []string
	Attributes    map[string]string
}

type MergeRequest struct {
	System         string
	Repository     string
	Number         int
	ExternalID     string
	Title          string
	Body           string
	State          string
	Traits         []string
	Attributes     map[string]string
	BaseRef        string
	HeadRef        string
	Author         User
	ReviewDecision string
	URL            string
	CreatedAt      string
	UpdatedAt      string
}

type ReviewRemark struct {
	System             string
	Repository         string
	MergeRequestNumber int
	ExternalID         string
	Author             User
	State              string
	Body               string
	Path               string
	Line               int
	Side               string
	ReplyToID          string
	URL                string
	CreatedAt          string
	UpdatedAt          string
}

type MessageThread struct {
	System     string
	SpaceID    string
	ThreadID   string
	RootID     string
	URL        string
	Messages   []Message
	Reactions  []MessageReaction
	Attributes map[string]string
}

type Message struct {
	System     string
	SpaceID    string
	ThreadID   string
	MessageID  string
	Author     User
	Body       string
	URL        string
	CreatedAt  string
	UpdatedAt  string
	Reactions  []MessageReaction
	Attributes map[string]string
}

type MessageReaction struct {
	System    string
	MessageID string
	User      User
	Name      string
	CreatedAt string
}

type WikiPage struct {
	System     string
	Space      string
	ExternalID string
	Title      string
	Body       string
	BodyFormat string
	Version    int
	URL        string
	CreatedAt  string
	UpdatedAt  string
	UpdatedBy  User
	Traits     []string
	Attributes map[string]string
	Links      []ObjectLink
}

type User struct {
	System   string
	Login    string
	Name     string
	Email    string
	URL      string
	IsBot    bool
	IsActive bool
}

type ObjectLink struct {
	System     string
	ObjectType string
	ExternalID string
	URL        string
	Relation   string
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
