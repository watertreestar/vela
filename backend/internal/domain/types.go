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

type ArtifactResolution struct {
	URI    string
	SHA256 string
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

type ReproducibilityReport struct {
	GeneratedAt       time.Time                       `json:"generatedAt"`
	BuildID           string                          `json:"buildId"`
	ProductID         string                          `json:"productId"`
	Target            string                          `json:"target"`
	Profile           string                          `json:"profile"`
	Customer          string                          `json:"customer"`
	Version           string                          `json:"version"`
	ReleaseChannel    string                          `json:"releaseChannel"`
	Status            string                          `json:"status"`
	Reproducible      bool                            `json:"reproducible"`
	ReleaseCheck      ReleaseCheckResult              `json:"releaseCheck"`
	BuildLock         BuildLock                       `json:"buildLock"`
	SourceArtifacts   []ReproducibilitySourceArtifact `json:"sourceArtifacts"`
	OutputArtifact    ReproducibilityArtifact         `json:"outputArtifact"`
	Runner            ReproducibilityRunner           `json:"runner"`
	Lifecycle         ReproducibilityLifecycle        `json:"lifecycle"`
	ConfigFingerprint map[string]string               `json:"configFingerprint,omitempty"`
	Metadata          map[string]string               `json:"metadata,omitempty"`
}

type ReleaseEvidenceArchive struct {
	SchemaVersion        string                `json:"schemaVersion"`
	GeneratedAt          time.Time             `json:"generatedAt"`
	ArchiveURI           string                `json:"archiveUri,omitempty"`
	ArchiveStoredAt      *time.Time            `json:"archiveStoredAt,omitempty"`
	ArchiveRetentionMode string                `json:"archiveRetentionMode,omitempty"`
	ArchiveRetainUntil   *time.Time            `json:"archiveRetainUntil,omitempty"`
	ArchiveLegalHold     string                `json:"archiveLegalHold,omitempty"`
	BuildID              string                `json:"buildId"`
	ProductID            string                `json:"productId"`
	Reproducible         bool                  `json:"reproducible"`
	Report               ReproducibilityReport `json:"report"`
	DigestAlgorithm      string                `json:"digestAlgorithm"`
	Digest               string                `json:"digest"`
	SignatureAlgorithm   string                `json:"signatureAlgorithm,omitempty"`
	SignatureKeyID       string                `json:"signatureKeyId,omitempty"`
	Signature            string                `json:"signature,omitempty"`
}

type ReproducibilitySourceArtifact struct {
	ComponentID string `json:"componentId"`
	Type        string `json:"type"`
	Version     string `json:"version"`
	SourceKind  string `json:"sourceKind"`
	URI         string `json:"uri"`
	SHA256      string `json:"sha256,omitempty"`
	Required    bool   `json:"required"`
}

type ReproducibilityArtifact struct {
	URI             string     `json:"uri"`
	DownloadURL     string     `json:"downloadUrl"`
	UploadID        string     `json:"uploadId,omitempty"`
	SHA256          string     `json:"sha256,omitempty"`
	Size            int64      `json:"size,omitempty"`
	ContentType     string     `json:"contentType,omitempty"`
	DownloadPolicy  string     `json:"downloadPolicy,omitempty"`
	RetentionStatus string     `json:"retentionStatus,omitempty"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
}

type ReproducibilityRunner struct {
	RunnerID   string `json:"runnerId,omitempty"`
	NomadJobID string `json:"nomadJobId,omitempty"`
}

type ReproducibilityLifecycle struct {
	CreatedAt   time.Time  `json:"createdAt"`
	ApprovedAt  *time.Time `json:"approvedAt,omitempty"`
	ValidatedAt *time.Time `json:"validatedAt,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type AuditEvent struct {
	ID            string            `json:"id"`
	Time          time.Time         `json:"time"`
	Action        string            `json:"action"`
	ActorID       string            `json:"actorId"`
	ActorRole     string            `json:"actorRole,omitempty"`
	ResourceType  string            `json:"resourceType"`
	ResourceID    string            `json:"resourceId"`
	ProductID     string            `json:"productId,omitempty"`
	BuildID       string            `json:"buildId,omitempty"`
	UploadID      string            `json:"uploadId,omitempty"`
	RequestID     string            `json:"requestId,omitempty"`
	CorrelationID string            `json:"correlationId,omitempty"`
	Status        string            `json:"status"`
	Summary       string            `json:"summary"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type AuditEventFilter struct {
	ResourceType string `json:"resourceType,omitempty"`
	ResourceID   string `json:"resourceId,omitempty"`
	BuildID      string `json:"buildId,omitempty"`
	ProductID    string `json:"productId,omitempty"`
	UploadID     string `json:"uploadId,omitempty"`
	Action       string `json:"action,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type AuditArchive struct {
	SchemaVersion        string           `json:"schemaVersion"`
	GeneratedAt          time.Time        `json:"generatedAt"`
	ArchiveURI           string           `json:"archiveUri,omitempty"`
	ArchiveStoredAt      *time.Time       `json:"archiveStoredAt,omitempty"`
	ArchiveRetentionMode string           `json:"archiveRetentionMode,omitempty"`
	ArchiveRetainUntil   *time.Time       `json:"archiveRetainUntil,omitempty"`
	ArchiveLegalHold     string           `json:"archiveLegalHold,omitempty"`
	EventCount           int              `json:"eventCount"`
	Filter               AuditEventFilter `json:"filter"`
	Events               []AuditEvent     `json:"events"`
	DigestAlgorithm      string           `json:"digestAlgorithm"`
	Digest               string           `json:"digest"`
	SignatureAlgorithm   string           `json:"signatureAlgorithm,omitempty"`
	SignatureKeyID       string           `json:"signatureKeyId,omitempty"`
	Signature            string           `json:"signature,omitempty"`
}

type AuditArchiveStoragePolicy struct {
	RetentionMode string
	RetainUntil   *time.Time
	LegalHold     string
}

type AuthSession struct {
	ID        string     `json:"id"`
	Subject   string     `json:"subject"`
	Role      string     `json:"role"`
	IssuedAt  time.Time  `json:"issuedAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

type Notification struct {
	ID           string            `json:"id"`
	CreatedAt    time.Time         `json:"createdAt"`
	ReadAt       *time.Time        `json:"readAt,omitempty"`
	Type         string            `json:"type"`
	Severity     string            `json:"severity"`
	Title        string            `json:"title"`
	Message      string            `json:"message"`
	ResourceType string            `json:"resourceType,omitempty"`
	ResourceID   string            `json:"resourceId,omitempty"`
	ProductID    string            `json:"productId,omitempty"`
	BuildID      string            `json:"buildId,omitempty"`
	UploadID     string            `json:"uploadId,omitempty"`
	RunnerID     string            `json:"runnerId,omitempty"`
	ActionURL    string            `json:"actionUrl,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type NotificationFilter struct {
	Type         string
	Severity     string
	ResourceType string
	ResourceID   string
	BuildID      string
	ProductID    string
	UploadID     string
	RunnerID     string
	ReadStatus   string
	Limit        int
}

type APIErrorDetail struct {
	Code       string            `json:"code,omitempty"`
	Field      string            `json:"field,omitempty"`
	Stage      string            `json:"stage,omitempty"`
	Title      string            `json:"title,omitempty"`
	TitleKey   string            `json:"titleKey,omitempty"`
	MessageKey string            `json:"messageKey,omitempty"`
	Message    string            `json:"message"`
	Params     map[string]string `json:"params,omitempty"`
}

type APIError struct {
	Code          string           `json:"code"`
	Status        int              `json:"status"`
	MessageKey    string           `json:"messageKey,omitempty"`
	Message       string           `json:"message"`
	Severity      string           `json:"severity"`
	Category      string           `json:"category"`
	Retryable     bool             `json:"retryable"`
	UserActionKey string           `json:"userActionKey,omitempty"`
	UserAction    string           `json:"userAction,omitempty"`
	RequestID     string           `json:"requestId,omitempty"`
	CorrelationID string           `json:"correlationId,omitempty"`
	Details       []APIErrorDetail `json:"details,omitempty"`
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
	Time          time.Time `json:"time"`
	Source        string    `json:"source"`
	Message       string    `json:"message"`
	Sequence      int       `json:"sequence,omitempty"`
	RequestID     string    `json:"requestId,omitempty"`
	CorrelationID string    `json:"correlationId,omitempty"`
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
	RequestID       string            `json:"requestId,omitempty"`
	CorrelationID   string            `json:"correlationId,omitempty"`
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
