package domain

import (
	"encoding/json"
	"time"
)

type Product struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description"`
	Targets      []string                `json:"targets"`
	TargetConfig map[string]TargetConfig `json:"targetConfig"`
	Profiles     []string                `json:"profiles"`
	Versions     []string                `json:"versions"`
	Artifacts    map[string]string       `json:"artifacts"`
	Components   []Component             `json:"components"`
	Workloads    []Workload              `json:"workloads"`
	Config       map[string]any          `json:"config"`
}

type ManifestRevision struct {
	ID        string          `json:"id"`
	ProductID string          `json:"productId"`
	Hash      string          `json:"hash"`
	Summary   string          `json:"summary"`
	Manifest  json.RawMessage `json:"manifest,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

type TargetConfig struct {
	InstallDir  string            `json:"installDir"`
	PackageName string            `json:"packageName"`
	Templates   map[string]string `json:"templates"`
	Config      map[string]any    `json:"config"`
}

type Component struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	Source   ComponentSource `json:"source"`
}

type Workload struct {
	ID        string           `json:"id"`
	Runtime   string           `json:"runtime"`
	Component string           `json:"component"`
	Module    string           `json:"module"`
	Enabled   *bool            `json:"enabled,omitempty"`
	Docker    map[string]any   `json:"docker,omitempty"`
	Systemd   *SystemdWorkload `json:"systemd,omitempty"`
}

type SystemdWorkload struct {
	UnitName         string            `json:"unitName"`
	Description      string            `json:"description"`
	User             string            `json:"user"`
	Group            string            `json:"group"`
	WorkingDirectory string            `json:"workingDirectory"`
	ExecStart        string            `json:"execStart"`
	ExecStop         string            `json:"execStop"`
	Restart          string            `json:"restart"`
	After            []string          `json:"after"`
	WantedBy         string            `json:"wantedBy"`
	Environment      map[string]string `json:"environment"`
	EnvironmentFile  string            `json:"environmentFile"`
}

type ResolvedWorkload struct {
	ID        string           `json:"id"`
	Runtime   string           `json:"runtime"`
	Component string           `json:"component"`
	Module    string           `json:"module"`
	Enabled   bool             `json:"enabled"`
	Docker    map[string]any   `json:"docker,omitempty"`
	Systemd   *SystemdWorkload `json:"systemd,omitempty"`
}

type ComponentSource struct {
	Kind       string   `json:"kind"`
	Repository string   `json:"repository"`
	Path       string   `json:"path"`
	URL        string   `json:"url"`
	UploadID   string   `json:"uploadId"`
	URI        string   `json:"uri"`
	Accept     []string `json:"accept"`
}

type ResolvedComponent struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Version  string          `json:"version"`
	Source   ComponentSource `json:"source"`
	URI      string          `json:"uri"`
	SHA256   string          `json:"sha256"`
	Required bool            `json:"required"`
}

type BuildLock struct {
	BuildID        string              `json:"buildId"`
	ProductID      string              `json:"productId"`
	Target         string              `json:"target"`
	Profile        string              `json:"profile"`
	Customer       string              `json:"customer"`
	Version        string              `json:"version"`
	ReleaseChannel string              `json:"releaseChannel"`
	Config         map[string]any      `json:"config"`
	Components     []ResolvedComponent `json:"components"`
	Workloads      []ResolvedWorkload  `json:"workloads"`
}

type BuildRequest struct {
	ProductID          string                     `json:"productId"`
	Target             string                     `json:"target"`
	Profile            string                     `json:"profile"`
	Customer           string                     `json:"customer"`
	Version            string                     `json:"version"`
	ReleaseChannel     string                     `json:"releaseChannel"`
	Variables          map[string]any             `json:"variables"`
	ConfigOverrides    map[string]any             `json:"configOverrides"`
	ComponentOverrides map[string]ComponentSource `json:"componentOverrides"`
}

type ManifestDryRunRequest struct {
	Manifest           json.RawMessage            `json:"manifest"`
	Target             string                     `json:"target"`
	Profile            string                     `json:"profile"`
	Customer           string                     `json:"customer"`
	Version            string                     `json:"version"`
	ReleaseChannel     string                     `json:"releaseChannel"`
	Variables          map[string]any             `json:"variables"`
	ConfigOverrides    map[string]any             `json:"configOverrides"`
	ComponentOverrides map[string]ComponentSource `json:"componentOverrides"`
}

type ManifestDryRunResult struct {
	Valid      bool                `json:"valid"`
	Errors     []string            `json:"errors"`
	BuildLock  BuildLock           `json:"buildLock,omitempty"`
	Config     map[string]any      `json:"config,omitempty"`
	Components []ResolvedComponent `json:"components,omitempty"`
}

type ReleaseCheckResult struct {
	BuildID       string             `json:"buildId"`
	Stage         string             `json:"stage"`
	PreviewReady  bool               `json:"previewReady"`
	ReleaseReady  bool               `json:"releaseReady"`
	BlockingCount int                `json:"blockingCount"`
	Checks        []ReleaseCheckItem `json:"checks"`
}

type ReleaseCheckSummary struct {
	BuildID            string `json:"buildId"`
	Stage              string `json:"stage"`
	PreviewReady       bool   `json:"previewReady"`
	ReleaseReady       bool   `json:"releaseReady"`
	BlockingCount      int    `json:"blockingCount"`
	FirstBlockedID     string `json:"firstBlockedId,omitempty"`
	FirstBlockedStage  string `json:"firstBlockedStage,omitempty"`
	FirstBlockedTitle  string `json:"firstBlockedTitle,omitempty"`
	FirstBlockedReason string `json:"firstBlockedReason,omitempty"`
}

type ReleaseCheckItem struct {
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type APIErrorDetail struct {
	Code    string `json:"code,omitempty"`
	Field   string `json:"field,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

type APIError struct {
	Code       string           `json:"code"`
	Message    string           `json:"message"`
	Severity   string           `json:"severity"`
	Category   string           `json:"category"`
	Retryable  bool             `json:"retryable"`
	UserAction string           `json:"userAction,omitempty"`
	RequestID  string           `json:"requestId,omitempty"`
	Details    []APIErrorDetail `json:"details,omitempty"`
}

type APIErrorResponse struct {
	Error  APIError `json:"error"`
	Errors []string `json:"errors,omitempty"`
}

type Build struct {
	ID                  string               `json:"id"`
	ProductID           string               `json:"productId"`
	Target              string               `json:"target"`
	Profile             string               `json:"profile"`
	Customer            string               `json:"customer"`
	Version             string               `json:"version"`
	ReleaseChannel      string               `json:"releaseChannel"`
	Status              string               `json:"status"`
	Attempt             int                  `json:"attempt"`
	RunnerID            string               `json:"runnerId"`
	NomadJobID          string               `json:"nomadJobId"`
	ArtifactURI         string               `json:"artifactUri"`
	DownloadURL         string               `json:"downloadUrl"`
	Error               string               `json:"error"`
	Variables           map[string]any       `json:"variables"`
	FinalConfig         map[string]any       `json:"finalConfig"`
	Components          []ResolvedComponent  `json:"components"`
	Workloads           []ResolvedWorkload   `json:"workloads"`
	BuildLock           BuildLock            `json:"buildLock"`
	CreatedAt           time.Time            `json:"createdAt"`
	UpdatedAt           time.Time            `json:"updatedAt"`
	SchedulerLog        string               `json:"schedulerLog"`
	Logs                []BuildLog           `json:"logs"`
	ApprovedAt          *time.Time           `json:"approvedAt,omitempty"`
	ValidatedAt         *time.Time           `json:"validatedAt,omitempty"`
	PublishedAt         *time.Time           `json:"publishedAt,omitempty"`
	CanceledAt          *time.Time           `json:"canceledAt,omitempty"`
	ReleaseCheckSummary *ReleaseCheckSummary `json:"releaseCheckSummary,omitempty"`
}

type BuildLog struct {
	Time     time.Time `json:"time"`
	Source   string    `json:"source"`
	Message  string    `json:"message"`
	Sequence int       `json:"sequence,omitempty"`
}

type RunnerClaimRequest struct {
	RunnerID string `json:"runnerId"`
	Target   string `json:"target"`
}

type RunnerCompleteRequest struct {
	Status      string `json:"status"`
	ArtifactURI string `json:"artifactUri"`
	DownloadURL string `json:"downloadUrl"`
	Error       string `json:"error"`
}

type RunnerHeartbeatRequest struct {
	RunnerID string `json:"runnerId"`
	BuildID  string `json:"buildId"`
	Target   string `json:"target"`
	Message  string `json:"message"`
}

type BuildLogRequest struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type UploadRecord struct {
	ID              string            `json:"id"`
	FileName        string            `json:"fileName"`
	URI             string            `json:"uri"`
	DownloadURL     string            `json:"downloadUrl"`
	SHA256          string            `json:"sha256"`
	Size            int64             `json:"size"`
	ContentType     string            `json:"contentType,omitempty"`
	DownloadPolicy  string            `json:"downloadPolicy"`
	RetentionDays   int               `json:"retentionDays"`
	ExpiresAt       *time.Time        `json:"expiresAt,omitempty"`
	RetentionStatus string            `json:"retentionStatus"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
}

type RunnerStatus struct {
	RunnerID          string     `json:"runnerId"`
	Target            string     `json:"target,omitempty"`
	Status            string     `json:"status"`
	CurrentBuildID    string     `json:"currentBuildId,omitempty"`
	CurrentBuildState string     `json:"currentBuildState,omitempty"`
	LastHeartbeatAt   *time.Time `json:"lastHeartbeatAt,omitempty"`
	LastClaimAt       *time.Time `json:"lastClaimAt,omitempty"`
	LastLogAt         *time.Time `json:"lastLogAt,omitempty"`
	RunningBuilds     int        `json:"runningBuilds"`
	CompletedBuilds   int        `json:"completedBuilds"`
	FailedBuilds      int        `json:"failedBuilds"`
	RecentMessages    []BuildLog `json:"recentMessages"`
}

const (
	BuildPendingApproval  = "pending_approval"
	BuildQueued           = "queued"
	BuildRunning          = "running"
	BuildSucceeded        = "succeeded"
	BuildPreviewValidated = "preview_validated"
	BuildFailed           = "failed"
	BuildRejected         = "rejected"
	BuildPublished        = "published"
	BuildCanceled         = "canceled"
)
