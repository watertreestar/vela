package store

import (
	"encoding/json"
	"time"

	"vela/conductor/internal/domain"
)

type Store interface {
	ListProducts() ([]domain.Product, error)
	ReadProduct(id string) (domain.Product, error)
	ReadProductManifest(id string) (json.RawMessage, error)
	WriteProductManifest(id string, raw json.RawMessage) (domain.Product, error)
	ListManifestRevisions(productID string) ([]domain.ManifestRevision, error)
	ReadManifestRevision(productID string, revisionID string) (domain.ManifestRevision, error)
	RestoreManifestRevision(productID string, revisionID string) (domain.Product, error)
	ListOverlays(productID string, kind string) ([]OverlayRecord, error)
	ReadOverlay(productID string, kind string, name string) (map[string]any, error)
	WriteOverlay(productID string, kind string, name string, raw json.RawMessage) (OverlayRecord, error)
	ListBuilds() ([]domain.Build, error)
	ReadBuild(id string) (domain.Build, error)
	SaveBuild(build domain.Build) error
	UpdateBuild(id string, update func(*domain.Build) error) (domain.Build, error)
	ClaimBuild(target string, runnerID string, requestID string, correlationID string) (domain.Build, bool, error)
	RetryBuild(id string) (domain.Build, error)
	CancelBuild(id string) (domain.Build, error)
	SaveUploadRecord(record domain.UploadRecord) error
	ListUploads() ([]domain.UploadRecord, error)
	ReadUpload(id string) (domain.UploadRecord, error)
	SaveAuditEvent(event domain.AuditEvent) error
	ListAuditEvents(filter domain.AuditEventFilter) ([]domain.AuditEvent, error)
	SaveAuthSession(session domain.AuthSession) error
	ReadAuthSession(id string) (domain.AuthSession, error)
	RevokeAuthSession(id string, revokedAt time.Time) (domain.AuthSession, error)
	SaveNotification(notification domain.Notification) error
	ListNotifications(filter domain.NotificationFilter) ([]domain.Notification, error)
	MarkNotificationRead(id string) (domain.Notification, error)
	MarkAllNotificationsRead() ([]domain.Notification, error)
	PruneReadNotificationsBefore(before time.Time) (int, error)
}

type OverlayRecord struct {
	Name string         `json:"name"`
	Kind string         `json:"kind"`
	Data map[string]any `json:"data"`
}
