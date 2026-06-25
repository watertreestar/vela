package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"packaging-platform/backend/internal/apperr"
	"packaging-platform/backend/internal/domain"
)

type JSONStore struct {
	dataDir           string
	productsDir       string
	buildsFile        string
	uploadsFile       string
	revisionsFile     string
	auditFile         string
	authSessionsFile  string
	notificationsFile string
	mu                sync.Mutex
}

func NewJSONStore(dataDir string, productsDir string) (*JSONStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return &JSONStore{
		dataDir:           dataDir,
		productsDir:       productsDir,
		buildsFile:        filepath.Join(dataDir, "builds.json"),
		uploadsFile:       filepath.Join(dataDir, "uploads.json"),
		revisionsFile:     filepath.Join(dataDir, "manifest_revisions.json"),
		auditFile:         filepath.Join(dataDir, "audit_events.json"),
		authSessionsFile:  filepath.Join(dataDir, "auth_sessions.json"),
		notificationsFile: filepath.Join(dataDir, "notifications.json"),
	}, nil
}

func (s *JSONStore) ListProducts() ([]domain.Product, error) {
	entries, err := os.ReadDir(s.productsDir)
	if err != nil {
		return nil, err
	}

	products := make([]domain.Product, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		product, err := s.ReadProduct(entry.Name())
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	sort.Slice(products, func(i, j int) bool {
		return products[i].ID < products[j].ID
	})
	return products, nil
}

func (s *JSONStore) ReadProduct(id string) (domain.Product, error) {
	file := filepath.Join(s.productsDir, id, "product.json")
	content, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.Product{}, apperr.NotFound("product", id)
		}
		return domain.Product{}, err
	}

	var product domain.Product
	if err := json.Unmarshal(content, &product); err != nil {
		return domain.Product{}, err
	}
	if product.ID == "" {
		product.ID = id
	}
	return product, nil
}

func (s *JSONStore) ReadProductManifest(id string) (json.RawMessage, error) {
	file := filepath.Join(s.productsDir, id, "product.json")
	content, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, apperr.NotFound("product", id)
		}
		return nil, err
	}
	var raw json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *JSONStore) WriteProductManifest(id string, raw json.RawMessage) (domain.Product, error) {
	var product domain.Product
	if err := json.Unmarshal(raw, &product); err != nil {
		return domain.Product{}, err
	}
	if product.ID == "" {
		product.ID = id
	}
	if product.ID != id {
		return domain.Product{}, apperr.Validation("VALIDATION_INVALID_REQUEST", fmt.Sprintf("manifest id %q does not match product %q", product.ID, id)).
			WithField("id").
			WithParam("manifestId", product.ID).
			WithParam("productId", id)
	}

	dir := filepath.Join(s.productsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.Product{}, err
	}
	content, err := json.MarshalIndent(product, "", "  ")
	if err != nil {
		return domain.Product{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "product.json"), content, 0o644); err != nil {
		return domain.Product{}, err
	}
	if err := s.saveManifestRevision(id, content, "manifest saved"); err != nil {
		return domain.Product{}, err
	}
	return product, nil
}

func (s *JSONStore) ListManifestRevisions(productID string) ([]domain.ManifestRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revisions, err := s.readManifestRevisionsLocked()
	if err != nil {
		return nil, err
	}
	filtered := []domain.ManifestRevision{}
	for _, revision := range revisions {
		if revision.ProductID == productID {
			revision.Manifest = nil
			filtered = append(filtered, revision)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	return filtered, nil
}

func (s *JSONStore) ReadManifestRevision(productID string, revisionID string) (domain.ManifestRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revisions, err := s.readManifestRevisionsLocked()
	if err != nil {
		return domain.ManifestRevision{}, err
	}
	for _, revision := range revisions {
		if revision.ProductID == productID && revision.ID == revisionID {
			return revision, nil
		}
	}
	return domain.ManifestRevision{}, apperr.NotFound("manifest_revision", revisionID)
}

func (s *JSONStore) RestoreManifestRevision(productID string, revisionID string) (domain.Product, error) {
	revision, err := s.ReadManifestRevision(productID, revisionID)
	if err != nil {
		return domain.Product{}, err
	}
	return s.WriteProductManifest(productID, revision.Manifest)
}

func (s *JSONStore) ReadOverlay(productID string, kind string, name string) (map[string]any, error) {
	return readOverlayFile(s.productsDir, productID, kind, name)
}

func (s *JSONStore) ListOverlays(productID string, kind string) ([]OverlayRecord, error) {
	return listOverlayFiles(s.productsDir, productID, kind)
}

func (s *JSONStore) WriteOverlay(productID string, kind string, name string, raw json.RawMessage) (OverlayRecord, error) {
	return writeOverlayFile(s.productsDir, productID, kind, name, raw)
}

func (s *JSONStore) ListBuilds() ([]domain.Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	builds, err := s.readBuildsLocked()
	if err != nil {
		return nil, err
	}
	sort.Slice(builds, func(i, j int) bool {
		return builds[i].CreatedAt.After(builds[j].CreatedAt)
	})
	return builds, nil
}

func (s *JSONStore) ReadBuild(id string) (domain.Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	builds, err := s.readBuildsLocked()
	if err != nil {
		return domain.Build{}, err
	}
	for _, build := range builds {
		if build.ID == id {
			return build, nil
		}
	}
	return domain.Build{}, apperr.NotFound("build", id)
}

func (s *JSONStore) SaveBuild(build domain.Build) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	builds, err := s.readBuildsLocked()
	if err != nil {
		return err
	}
	replaced := false
	for index := range builds {
		if builds[index].ID == build.ID {
			builds[index] = build
			replaced = true
			break
		}
	}
	if !replaced {
		builds = append(builds, build)
	}
	return s.writeBuildsLocked(builds)
}

func (s *JSONStore) UpdateBuild(id string, update func(*domain.Build) error) (domain.Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	builds, err := s.readBuildsLocked()
	if err != nil {
		return domain.Build{}, err
	}
	for index := range builds {
		if builds[index].ID != id {
			continue
		}
		if err := update(&builds[index]); err != nil {
			return domain.Build{}, err
		}
		if err := s.writeBuildsLocked(builds); err != nil {
			return domain.Build{}, err
		}
		return builds[index], nil
	}
	return domain.Build{}, apperr.NotFound("build", id)
}

func (s *JSONStore) ClaimBuild(target string, runnerID string, requestID string, correlationID string) (domain.Build, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	builds, err := s.readBuildsLocked()
	if err != nil {
		return domain.Build{}, false, err
	}
	for index := range builds {
		if builds[index].Status != domain.BuildQueued {
			continue
		}
		if target != "" && builds[index].Target != target {
			continue
		}
		builds[index].Status = domain.BuildRunning
		builds[index].RunnerID = runnerID
		builds[index].Attempt++
		builds[index].UpdatedAt = nowUTC()
		builds[index].Logs = append(builds[index].Logs, domain.BuildLog{
			Time:          nowUTC(),
			Source:        runnerID,
			Message:       fmt.Sprintf("claimed by runner %s", runnerID),
			RequestID:     requestID,
			CorrelationID: correlationID,
		})
		if err := s.writeBuildsLocked(builds); err != nil {
			return domain.Build{}, false, err
		}
		return builds[index], true, nil
	}
	return domain.Build{}, false, nil
}

func (s *JSONStore) RetryBuild(id string) (domain.Build, error) {
	return s.UpdateBuild(id, func(build *domain.Build) error {
		if build.Status != domain.BuildFailed && build.Status != domain.BuildCanceled {
			return apperr.Conflict("BUILD_STATE_CONFLICT", "only failed or canceled builds can be retried")
		}
		build.Status = domain.BuildQueued
		build.Error = ""
		build.RunnerID = ""
		build.UpdatedAt = nowUTC()
		build.Logs = append(build.Logs, domain.BuildLog{
			Time:    nowUTC(),
			Source:  "system",
			Message: "build queued for retry",
		})
		return nil
	})
}

func (s *JSONStore) CancelBuild(id string) (domain.Build, error) {
	now := nowUTC()
	return s.UpdateBuild(id, func(build *domain.Build) error {
		switch build.Status {
		case domain.BuildQueued, domain.BuildRunning, domain.BuildPendingApproval:
			build.Status = domain.BuildCanceled
			build.CanceledAt = &now
			build.UpdatedAt = now
			build.Logs = append(build.Logs, domain.BuildLog{
				Time:    now,
				Source:  "system",
				Message: "build canceled",
			})
			return nil
		default:
			return apperr.Conflict("BUILD_STATE_CONFLICT", "only pending, queued, or running builds can be canceled")
		}
	})
}

func (s *JSONStore) SaveUploadRecord(record domain.UploadRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readUploadsLocked()
	if err != nil {
		return err
	}
	replaced := false
	for index := range records {
		if records[index].ID == record.ID {
			records[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, record)
	}
	return s.writeUploadsLocked(records)
}

func (s *JSONStore) ListUploads() ([]domain.UploadRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readUploadsLocked()
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, nil
}

func (s *JSONStore) ReadUpload(id string) (domain.UploadRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readUploadsLocked()
	if err != nil {
		return domain.UploadRecord{}, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, nil
		}
	}
	return domain.UploadRecord{}, apperr.NotFound("upload", id)
}

func (s *JSONStore) SaveAuditEvent(event domain.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	events, err := s.readAuditEventsLocked()
	if err != nil {
		return err
	}
	events = append(events, event)
	return s.writeAuditEventsLocked(events)
}

func (s *JSONStore) ListAuditEvents(filter domain.AuditEventFilter) ([]domain.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events, err := s.readAuditEventsLocked()
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.AuditEvent, 0, len(events))
	for _, event := range events {
		if !auditEventMatches(event, filter) {
			continue
		}
		filtered = append(filtered, event)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Time.After(filtered[j].Time)
	})
	return limitAuditEvents(filtered, filter.Limit), nil
}

func (s *JSONStore) SaveAuthSession(session domain.AuthSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions, err := s.readAuthSessionsLocked()
	if err != nil {
		return err
	}
	for index := range sessions {
		if sessions[index].ID == session.ID {
			sessions[index] = session
			return s.writeAuthSessionsLocked(sessions)
		}
	}
	sessions = append(sessions, session)
	return s.writeAuthSessionsLocked(sessions)
}

func (s *JSONStore) ReadAuthSession(id string) (domain.AuthSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions, err := s.readAuthSessionsLocked()
	if err != nil {
		return domain.AuthSession{}, err
	}
	for _, session := range sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return domain.AuthSession{}, apperr.NotFound("auth_session", id)
}

func (s *JSONStore) RevokeAuthSession(id string, revokedAt time.Time) (domain.AuthSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions, err := s.readAuthSessionsLocked()
	if err != nil {
		return domain.AuthSession{}, err
	}
	for index := range sessions {
		if sessions[index].ID != id {
			continue
		}
		sessions[index].RevokedAt = &revokedAt
		if err := s.writeAuthSessionsLocked(sessions); err != nil {
			return domain.AuthSession{}, err
		}
		return sessions[index], nil
	}
	return domain.AuthSession{}, apperr.NotFound("auth_session", id)
}

func (s *JSONStore) SaveNotification(notification domain.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifications, err := s.readNotificationsLocked()
	if err != nil {
		return err
	}
	notifications = append(notifications, notification)
	return s.writeNotificationsLocked(notifications)
}

func (s *JSONStore) ListNotifications(filter domain.NotificationFilter) ([]domain.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifications, err := s.readNotificationsLocked()
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.Notification, 0, len(notifications))
	for _, notification := range notifications {
		if !notificationMatches(notification, filter) {
			continue
		}
		filtered = append(filtered, notification)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	return limitNotifications(filtered, filter.Limit), nil
}

func (s *JSONStore) MarkNotificationRead(id string) (domain.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifications, err := s.readNotificationsLocked()
	if err != nil {
		return domain.Notification{}, err
	}
	now := nowUTC()
	for index := range notifications {
		if notifications[index].ID != id {
			continue
		}
		if notifications[index].ReadAt == nil {
			notifications[index].ReadAt = &now
		}
		if err := s.writeNotificationsLocked(notifications); err != nil {
			return domain.Notification{}, err
		}
		return notifications[index], nil
	}
	return domain.Notification{}, apperr.NotFound("notification", id)
}

func (s *JSONStore) MarkAllNotificationsRead() ([]domain.Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifications, err := s.readNotificationsLocked()
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	changed := make([]domain.Notification, 0)
	for index := range notifications {
		if notifications[index].ReadAt != nil {
			continue
		}
		notifications[index].ReadAt = &now
		changed = append(changed, notifications[index])
	}
	if err := s.writeNotificationsLocked(notifications); err != nil {
		return nil, err
	}
	return changed, nil
}

func (s *JSONStore) PruneReadNotificationsBefore(before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifications, err := s.readNotificationsLocked()
	if err != nil {
		return 0, err
	}
	kept := make([]domain.Notification, 0, len(notifications))
	pruned := 0
	for _, notification := range notifications {
		if notification.ReadAt != nil && notification.ReadAt.Before(before) {
			pruned++
			continue
		}
		kept = append(kept, notification)
	}
	if pruned == 0 {
		return 0, nil
	}
	if err := s.writeNotificationsLocked(kept); err != nil {
		return 0, err
	}
	return pruned, nil
}

func (s *JSONStore) readBuildsLocked() ([]domain.Build, error) {
	content, err := os.ReadFile(s.buildsFile)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Build{}, nil
	}
	if err != nil {
		return nil, err
	}
	var builds []domain.Build
	if err := json.Unmarshal(content, &builds); err != nil {
		return nil, err
	}
	return builds, nil
}

func (s *JSONStore) writeBuildsLocked(builds []domain.Build) error {
	content, err := json.MarshalIndent(builds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.buildsFile, content, 0o644)
}

func (s *JSONStore) saveManifestRevision(productID string, raw json.RawMessage, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	revisions, err := s.readManifestRevisionsLocked()
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	now := nowUTC()
	id := fmt.Sprintf("%s-%s", now.Format("20060102150405"), hex.EncodeToString(hash[:])[:10])
	revisions = append(revisions, domain.ManifestRevision{
		ID:        id,
		ProductID: productID,
		Hash:      hex.EncodeToString(hash[:]),
		Summary:   summary,
		Manifest:  append(json.RawMessage(nil), raw...),
		CreatedAt: now,
	})
	return s.writeManifestRevisionsLocked(revisions)
}

func (s *JSONStore) readManifestRevisionsLocked() ([]domain.ManifestRevision, error) {
	content, err := os.ReadFile(s.revisionsFile)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.ManifestRevision{}, nil
	}
	if err != nil {
		return nil, err
	}
	var revisions []domain.ManifestRevision
	if err := json.Unmarshal(content, &revisions); err != nil {
		return nil, err
	}
	return revisions, nil
}

func (s *JSONStore) writeManifestRevisionsLocked(revisions []domain.ManifestRevision) error {
	content, err := json.MarshalIndent(revisions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.revisionsFile, content, 0o644)
}

func (s *JSONStore) readUploadsLocked() ([]domain.UploadRecord, error) {
	content, err := os.ReadFile(s.uploadsFile)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.UploadRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []domain.UploadRecord
	if err := json.Unmarshal(content, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *JSONStore) writeUploadsLocked(records []domain.UploadRecord) error {
	content, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.uploadsFile, content, 0o644)
}

func (s *JSONStore) readAuditEventsLocked() ([]domain.AuditEvent, error) {
	content, err := os.ReadFile(s.auditFile)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.AuditEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	var events []domain.AuditEvent
	if err := json.Unmarshal(content, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *JSONStore) writeAuditEventsLocked(events []domain.AuditEvent) error {
	content, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.auditFile, content, 0o644)
}

func (s *JSONStore) readAuthSessionsLocked() ([]domain.AuthSession, error) {
	content, err := os.ReadFile(s.authSessionsFile)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.AuthSession{}, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []domain.AuthSession
	if err := json.Unmarshal(content, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (s *JSONStore) writeAuthSessionsLocked(sessions []domain.AuthSession) error {
	content, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.authSessionsFile, content, 0o644)
}

func (s *JSONStore) readNotificationsLocked() ([]domain.Notification, error) {
	content, err := os.ReadFile(s.notificationsFile)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Notification{}, nil
	}
	if err != nil {
		return nil, err
	}
	var notifications []domain.Notification
	if err := json.Unmarshal(content, &notifications); err != nil {
		return nil, err
	}
	return notifications, nil
}

func (s *JSONStore) writeNotificationsLocked(notifications []domain.Notification) error {
	content, err := json.MarshalIndent(notifications, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.notificationsFile, content, 0o644)
}

func auditEventMatches(event domain.AuditEvent, filter domain.AuditEventFilter) bool {
	if filter.ResourceType != "" && event.ResourceType != filter.ResourceType {
		return false
	}
	if filter.ResourceID != "" && event.ResourceID != filter.ResourceID {
		return false
	}
	if filter.BuildID != "" && event.BuildID != filter.BuildID {
		return false
	}
	if filter.ProductID != "" && event.ProductID != filter.ProductID {
		return false
	}
	if filter.UploadID != "" && event.UploadID != filter.UploadID {
		return false
	}
	if filter.Action != "" && event.Action != filter.Action {
		return false
	}
	return true
}

func notificationMatches(notification domain.Notification, filter domain.NotificationFilter) bool {
	if filter.Type != "" && notification.Type != filter.Type {
		return false
	}
	if filter.Severity != "" && notification.Severity != filter.Severity {
		return false
	}
	if filter.ResourceType != "" && notification.ResourceType != filter.ResourceType {
		return false
	}
	if filter.ResourceID != "" && notification.ResourceID != filter.ResourceID {
		return false
	}
	if filter.BuildID != "" && notification.BuildID != filter.BuildID {
		return false
	}
	if filter.ProductID != "" && notification.ProductID != filter.ProductID {
		return false
	}
	if filter.UploadID != "" && notification.UploadID != filter.UploadID {
		return false
	}
	if filter.RunnerID != "" && notification.RunnerID != filter.RunnerID {
		return false
	}
	switch filter.ReadStatus {
	case "read":
		return notification.ReadAt != nil
	case "unread":
		return notification.ReadAt == nil
	default:
		return true
	}
}

func limitNotifications(notifications []domain.Notification, limit int) []domain.Notification {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if len(notifications) <= limit {
		return notifications
	}
	return notifications[:limit]
}

func limitAuditEvents(events []domain.AuditEvent, limit int) []domain.AuditEvent {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if len(events) <= limit {
		return events
	}
	return events[:limit]
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
