package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"vela/conductor/internal/apperr"
	"vela/conductor/internal/domain"
)

type PostgresStore struct {
	db          *sql.DB
	productsDir string
}

func NewPostgresStore(databaseURL string, productsDir string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	store := &PostgresStore{db: db, productsDir: productsDir}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.seedProducts(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
create table if not exists products (
  id text primary key,
  manifest jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists builds (
  id text primary key,
  product_id text not null,
  target text not null,
  profile text,
  customer text,
  version text,
  release_channel text,
  status text not null,
  attempt integer not null default 0,
  runner_id text,
  nomad_job_id text,
  artifact_uri text,
  download_url text,
  error text,
  payload jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists builds_status_target_idx on builds(status, target);
create index if not exists builds_created_at_idx on builds(created_at desc);

create table if not exists uploads (
  id text primary key,
  file_name text not null,
  uri text not null,
  download_url text not null,
  sha256 text not null,
  size_bytes bigint not null,
  payload jsonb not null,
  created_at timestamptz not null default now()
);

create table if not exists manifest_revisions (
  id text not null,
  product_id text not null,
  hash text not null,
  summary text not null,
  manifest jsonb not null,
  created_at timestamptz not null default now(),
  primary key (product_id, id)
);

create index if not exists manifest_revisions_product_created_idx on manifest_revisions(product_id, created_at desc);

create table if not exists audit_events (
  id text primary key,
  action text not null,
  actor_id text not null,
  actor_role text,
  resource_type text not null,
  resource_id text not null,
  product_id text,
  build_id text,
  upload_id text,
  request_id text,
  correlation_id text,
  status text not null,
  summary text not null,
  metadata jsonb not null default '{}'::jsonb,
  payload jsonb not null,
  created_at timestamptz not null
);

create index if not exists audit_events_created_idx on audit_events(created_at desc);
create index if not exists audit_events_resource_idx on audit_events(resource_type, resource_id, created_at desc);
create index if not exists audit_events_build_idx on audit_events(build_id, created_at desc);
create index if not exists audit_events_product_idx on audit_events(product_id, created_at desc);
create index if not exists audit_events_upload_idx on audit_events(upload_id, created_at desc);

create table if not exists auth_sessions (
  id text primary key,
  subject text not null,
  role text not null,
  expires_at timestamptz not null,
  revoked_at timestamptz,
  payload jsonb not null,
  created_at timestamptz not null
);

create index if not exists auth_sessions_subject_idx on auth_sessions(subject, created_at desc);
create index if not exists auth_sessions_expires_idx on auth_sessions(expires_at);

create table if not exists notifications (
  id text primary key,
  type text not null,
  severity text not null,
  resource_type text,
  resource_id text,
  product_id text,
  build_id text,
  upload_id text,
  runner_id text,
  read_at timestamptz,
  metadata jsonb not null default '{}'::jsonb,
  payload jsonb not null,
  created_at timestamptz not null
);

create index if not exists notifications_created_idx on notifications(created_at desc);
create index if not exists notifications_unread_idx on notifications(created_at desc) where read_at is null;
create index if not exists notifications_resource_idx on notifications(resource_type, resource_id, created_at desc);
create index if not exists notifications_build_idx on notifications(build_id, created_at desc);
create index if not exists notifications_product_idx on notifications(product_id, created_at desc);
`)
	return err
}

func (s *PostgresStore) seedProducts(ctx context.Context) error {
	entries, err := os.ReadDir(s.productsDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(s.productsDir, entry.Name(), "product.json")
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var product domain.Product
		if err := json.Unmarshal(content, &product); err != nil {
			return err
		}
		if product.ID == "" {
			product.ID = entry.Name()
		}
		content, err = json.Marshal(product)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `
insert into products (id, manifest, updated_at)
values ($1, $2::jsonb, now())
on conflict (id) do update set manifest = excluded.manifest, updated_at = now()
`, product.ID, string(content))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) ListProducts() ([]domain.Product, error) {
	rows, err := s.db.Query(`select manifest from products order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	products := []domain.Product{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var product domain.Product
		if err := json.Unmarshal(raw, &product); err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, rows.Err()
}

func (s *PostgresStore) ReadProduct(id string) (domain.Product, error) {
	raw, err := s.ReadProductManifest(id)
	if err != nil {
		return domain.Product{}, err
	}
	var product domain.Product
	if err := json.Unmarshal(raw, &product); err != nil {
		return domain.Product{}, err
	}
	if product.ID == "" {
		product.ID = id
	}
	return product, nil
}

func (s *PostgresStore) ReadProductManifest(id string) (json.RawMessage, error) {
	var raw []byte
	if err := s.db.QueryRow(`select manifest from products where id = $1`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, apperr.NotFound("product", id)
		}
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func (s *PostgresStore) WriteProductManifest(id string, raw json.RawMessage) (domain.Product, error) {
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
	content, err := json.Marshal(product)
	if err != nil {
		return domain.Product{}, err
	}
	_, err = s.db.Exec(`
insert into products (id, manifest, updated_at)
values ($1, $2::jsonb, now())
on conflict (id) do update set manifest = excluded.manifest, updated_at = now()
`, id, string(content))
	if err != nil {
		return domain.Product{}, err
	}
	if err := s.saveManifestRevision(id, content, "manifest saved"); err != nil {
		return domain.Product{}, err
	}
	return product, nil
}

func (s *PostgresStore) ListManifestRevisions(productID string) ([]domain.ManifestRevision, error) {
	rows, err := s.db.Query(`
select id, product_id, hash, summary, created_at
from manifest_revisions
where product_id = $1
order by created_at desc
`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := []domain.ManifestRevision{}
	for rows.Next() {
		var revision domain.ManifestRevision
		if err := rows.Scan(&revision.ID, &revision.ProductID, &revision.Hash, &revision.Summary, &revision.CreatedAt); err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *PostgresStore) ReadManifestRevision(productID string, revisionID string) (domain.ManifestRevision, error) {
	var revision domain.ManifestRevision
	var raw []byte
	if err := s.db.QueryRow(`
select id, product_id, hash, summary, manifest, created_at
from manifest_revisions
where product_id = $1 and id = $2
`, productID, revisionID).Scan(&revision.ID, &revision.ProductID, &revision.Hash, &revision.Summary, &raw, &revision.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return domain.ManifestRevision{}, apperr.NotFound("manifest_revision", revisionID)
		}
		return domain.ManifestRevision{}, err
	}
	revision.Manifest = json.RawMessage(raw)
	return revision, nil
}

func (s *PostgresStore) RestoreManifestRevision(productID string, revisionID string) (domain.Product, error) {
	revision, err := s.ReadManifestRevision(productID, revisionID)
	if err != nil {
		return domain.Product{}, err
	}
	return s.WriteProductManifest(productID, revision.Manifest)
}

func (s *PostgresStore) saveManifestRevision(productID string, raw json.RawMessage, summary string) error {
	hash := sha256.Sum256(raw)
	now := nowUTC()
	id := fmt.Sprintf("%s-%s", now.Format("20060102150405"), hex.EncodeToString(hash[:])[:10])
	_, err := s.db.Exec(`
insert into manifest_revisions (id, product_id, hash, summary, manifest, created_at)
values ($1, $2, $3, $4, $5::jsonb, $6)
on conflict (product_id, id) do nothing
`, id, productID, hex.EncodeToString(hash[:]), summary, string(raw), now)
	return err
}

func (s *PostgresStore) ReadOverlay(productID string, kind string, name string) (map[string]any, error) {
	return readOverlayFile(s.productsDir, productID, kind, name)
}

func (s *PostgresStore) ListOverlays(productID string, kind string) ([]OverlayRecord, error) {
	return listOverlayFiles(s.productsDir, productID, kind)
}

func (s *PostgresStore) WriteOverlay(productID string, kind string, name string, raw json.RawMessage) (OverlayRecord, error) {
	return writeOverlayFile(s.productsDir, productID, kind, name, raw)
}

func (s *PostgresStore) ListBuilds() ([]domain.Build, error) {
	rows, err := s.db.Query(`select payload from builds order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	builds := []domain.Build{}
	for rows.Next() {
		build, err := scanBuildPayload(rows)
		if err != nil {
			return nil, err
		}
		builds = append(builds, build)
	}
	return builds, rows.Err()
}

func (s *PostgresStore) ReadBuild(id string) (domain.Build, error) {
	var raw []byte
	if err := s.db.QueryRow(`select payload from builds where id = $1`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return domain.Build{}, apperr.NotFound("build", id)
		}
		return domain.Build{}, err
	}
	var build domain.Build
	if err := json.Unmarshal(raw, &build); err != nil {
		return domain.Build{}, err
	}
	return build, nil
}

func (s *PostgresStore) SaveBuild(build domain.Build) error {
	return s.saveBuild(context.Background(), nil, build)
}

func (s *PostgresStore) UpdateBuild(id string, update func(*domain.Build) error) (domain.Build, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Build{}, err
	}
	defer tx.Rollback()

	build, err := s.readBuildForUpdate(ctx, tx, id)
	if err != nil {
		return domain.Build{}, err
	}
	if err := update(&build); err != nil {
		return domain.Build{}, err
	}
	if err := s.saveBuild(ctx, tx, build); err != nil {
		return domain.Build{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Build{}, err
	}
	return build, nil
}

func (s *PostgresStore) ClaimBuild(target string, runnerID string, requestID string, correlationID string) (domain.Build, bool, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Build{}, false, err
	}
	defer tx.Rollback()

	query := `
select payload from builds
where status = $1 and ($2 = '' or target = $2)
order by created_at asc
for update skip locked
limit 1`
	var raw []byte
	err = tx.QueryRowContext(ctx, query, domain.BuildQueued, target).Scan(&raw)
	if err == sql.ErrNoRows {
		return domain.Build{}, false, nil
	}
	if err != nil {
		return domain.Build{}, false, err
	}
	var build domain.Build
	if err := json.Unmarshal(raw, &build); err != nil {
		return domain.Build{}, false, err
	}
	build.Status = domain.BuildRunning
	build.RunnerID = runnerID
	build.Attempt++
	build.UpdatedAt = nowUTC()
	build.Logs = append(build.Logs, domain.BuildLog{
		Time:          nowUTC(),
		Source:        runnerID,
		Message:       fmt.Sprintf("claimed by runner %s", runnerID),
		RequestID:     requestID,
		CorrelationID: correlationID,
	})
	if err := s.saveBuild(ctx, tx, build); err != nil {
		return domain.Build{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Build{}, false, err
	}
	return build, true, nil
}

func (s *PostgresStore) RetryBuild(id string) (domain.Build, error) {
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

func (s *PostgresStore) CancelBuild(id string) (domain.Build, error) {
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

func (s *PostgresStore) SaveUploadRecord(record domain.UploadRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
insert into uploads (id, file_name, uri, download_url, sha256, size_bytes, payload, created_at)
values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
on conflict (id) do update set
  file_name = excluded.file_name,
  uri = excluded.uri,
  download_url = excluded.download_url,
  sha256 = excluded.sha256,
  size_bytes = excluded.size_bytes,
  payload = excluded.payload
`, record.ID, record.FileName, record.URI, record.DownloadURL, record.SHA256, record.Size, string(payload), record.CreatedAt)
	return err
}

func (s *PostgresStore) ListUploads() ([]domain.UploadRecord, error) {
	rows, err := s.db.Query(`select payload from uploads order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := []domain.UploadRecord{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var record domain.UploadRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresStore) ReadUpload(id string) (domain.UploadRecord, error) {
	var raw []byte
	if err := s.db.QueryRow(`select payload from uploads where id = $1`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return domain.UploadRecord{}, apperr.NotFound("upload", id)
		}
		return domain.UploadRecord{}, err
	}
	var record domain.UploadRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return domain.UploadRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) SaveAuditEvent(event domain.AuditEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
insert into audit_events (
  id, action, actor_id, actor_role, resource_type, resource_id, product_id,
  build_id, upload_id, request_id, correlation_id, status, summary,
  metadata, payload, created_at
) values (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13,
  $14::jsonb, $15::jsonb, $16
) on conflict (id) do nothing
`, event.ID, event.Action, event.ActorID, event.ActorRole, event.ResourceType, event.ResourceID, event.ProductID,
		event.BuildID, event.UploadID, event.RequestID, event.CorrelationID, event.Status, event.Summary,
		string(metadata), string(payload), event.Time)
	return err
}

func (s *PostgresStore) ListAuditEvents(filter domain.AuditEventFilter) ([]domain.AuditEvent, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`
select payload
from audit_events
where ($1 = '' or resource_type = $1)
  and ($2 = '' or resource_id = $2)
  and ($3 = '' or build_id = $3)
  and ($4 = '' or product_id = $4)
  and ($5 = '' or upload_id = $5)
  and ($6 = '' or action = $6)
order by created_at desc
limit $7
`, filter.ResourceType, filter.ResourceID, filter.BuildID, filter.ProductID, filter.UploadID, filter.Action, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []domain.AuditEvent{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event domain.AuditEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) SaveAuthSession(session domain.AuthSession) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
insert into auth_sessions (id, subject, role, expires_at, revoked_at, payload, created_at)
values ($1, $2, $3, $4, $5, $6::jsonb, $7)
on conflict (id) do update set
  subject = excluded.subject,
  role = excluded.role,
  expires_at = excluded.expires_at,
  revoked_at = excluded.revoked_at,
  payload = excluded.payload
`, session.ID, session.Subject, session.Role, session.ExpiresAt, session.RevokedAt, string(payload), session.IssuedAt)
	return err
}

func (s *PostgresStore) ReadAuthSession(id string) (domain.AuthSession, error) {
	var raw []byte
	if err := s.db.QueryRow(`select payload from auth_sessions where id = $1`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return domain.AuthSession{}, apperr.NotFound("auth_session", id)
		}
		return domain.AuthSession{}, err
	}
	var session domain.AuthSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return domain.AuthSession{}, err
	}
	return session, nil
}

func (s *PostgresStore) RevokeAuthSession(id string, revokedAt time.Time) (domain.AuthSession, error) {
	session, err := s.ReadAuthSession(id)
	if err != nil {
		return domain.AuthSession{}, err
	}
	session.RevokedAt = &revokedAt
	payload, err := json.Marshal(session)
	if err != nil {
		return domain.AuthSession{}, err
	}
	result, err := s.db.Exec(`
update auth_sessions
set revoked_at = $2, payload = $3::jsonb
where id = $1
`, id, revokedAt, string(payload))
	if err != nil {
		return domain.AuthSession{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.AuthSession{}, err
	}
	if affected == 0 {
		return domain.AuthSession{}, apperr.NotFound("auth_session", id)
	}
	return session, nil
}

func (s *PostgresStore) SaveNotification(notification domain.Notification) error {
	payload, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(notification.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
insert into notifications (
  id, type, severity, resource_type, resource_id, product_id, build_id,
  upload_id, runner_id, read_at, metadata, payload, created_at
) values (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11::jsonb, $12::jsonb, $13
) on conflict (id) do nothing
`, notification.ID, notification.Type, notification.Severity, notification.ResourceType, notification.ResourceID,
		notification.ProductID, notification.BuildID, notification.UploadID, notification.RunnerID, notification.ReadAt,
		string(metadata), string(payload), notification.CreatedAt)
	return err
}

func (s *PostgresStore) ListNotifications(filter domain.NotificationFilter) ([]domain.Notification, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`
select payload
from notifications
where ($1 = '' or type = $1)
  and ($2 = '' or severity = $2)
  and ($3 = '' or resource_type = $3)
  and ($4 = '' or resource_id = $4)
  and ($5 = '' or build_id = $5)
  and ($6 = '' or product_id = $6)
  and ($7 = '' or upload_id = $7)
  and ($8 = '' or runner_id = $8)
  and ($9 = '' or ($9 = 'read' and read_at is not null) or ($9 = 'unread' and read_at is null))
order by created_at desc
limit $10
`, filter.Type, filter.Severity, filter.ResourceType, filter.ResourceID, filter.BuildID,
		filter.ProductID, filter.UploadID, filter.RunnerID, filter.ReadStatus, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notifications := []domain.Notification{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var notification domain.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			return nil, err
		}
		notifications = append(notifications, notification)
	}
	return notifications, rows.Err()
}

func (s *PostgresStore) MarkNotificationRead(id string) (domain.Notification, error) {
	var raw []byte
	if err := s.db.QueryRow(`select payload from notifications where id = $1`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return domain.Notification{}, apperr.NotFound("notification", id)
		}
		return domain.Notification{}, err
	}
	var notification domain.Notification
	if err := json.Unmarshal(raw, &notification); err != nil {
		return domain.Notification{}, err
	}
	if notification.ReadAt == nil {
		now := time.Now().UTC()
		notification.ReadAt = &now
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		return domain.Notification{}, err
	}
	_, err = s.db.Exec(`
update notifications
set read_at = $2, payload = $3::jsonb
where id = $1
`, notification.ID, notification.ReadAt, string(payload))
	if err != nil {
		return domain.Notification{}, err
	}
	return notification, nil
}

func (s *PostgresStore) MarkAllNotificationsRead() ([]domain.Notification, error) {
	rows, err := s.db.Query(`select payload from notifications where read_at is null order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	notifications := []domain.Notification{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var notification domain.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			return nil, err
		}
		notification.ReadAt = &now
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, notification := range notifications {
		payload, err := json.Marshal(notification)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.Exec(`
update notifications
set read_at = $2, payload = $3::jsonb
where id = $1
`, notification.ID, notification.ReadAt, string(payload)); err != nil {
			return nil, err
		}
	}
	return notifications, nil
}

func (s *PostgresStore) PruneReadNotificationsBefore(before time.Time) (int, error) {
	result, err := s.db.Exec(`delete from notifications where read_at is not null and read_at < $1`, before)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *PostgresStore) readBuildForUpdate(ctx context.Context, tx *sql.Tx, id string) (domain.Build, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `select payload from builds where id = $1 for update`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return domain.Build{}, apperr.NotFound("build", id)
		}
		return domain.Build{}, err
	}
	var build domain.Build
	if err := json.Unmarshal(raw, &build); err != nil {
		return domain.Build{}, err
	}
	return build, nil
}

func (s *PostgresStore) saveBuild(ctx context.Context, tx *sql.Tx, build domain.Build) error {
	payload, err := json.Marshal(build)
	if err != nil {
		return err
	}
	query := `
insert into builds (
  id, product_id, target, profile, customer, version, release_channel,
  status, attempt, runner_id, nomad_job_id, artifact_uri, download_url,
  error, payload, created_at, updated_at
) values (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11, $12, $13,
  $14, $15::jsonb, $16, $17
) on conflict (id) do update set
  product_id = excluded.product_id,
  target = excluded.target,
  profile = excluded.profile,
  customer = excluded.customer,
  version = excluded.version,
  release_channel = excluded.release_channel,
  status = excluded.status,
  attempt = excluded.attempt,
  runner_id = excluded.runner_id,
  nomad_job_id = excluded.nomad_job_id,
  artifact_uri = excluded.artifact_uri,
  download_url = excluded.download_url,
  error = excluded.error,
  payload = excluded.payload,
  updated_at = excluded.updated_at`
	args := []any{
		build.ID, build.ProductID, build.Target, build.Profile, build.Customer, build.Version, build.ReleaseChannel,
		build.Status, build.Attempt, build.RunnerID, build.NomadJobID, build.ArtifactURI, build.DownloadURL,
		build.Error, string(payload), build.CreatedAt, build.UpdatedAt,
	}
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, args...)
	} else {
		_, err = s.db.ExecContext(ctx, query, args...)
	}
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBuildPayload(row rowScanner) (domain.Build, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return domain.Build{}, err
	}
	var build domain.Build
	if err := json.Unmarshal(raw, &build); err != nil {
		return domain.Build{}, err
	}
	return build, nil
}

func sortBuildsNewestFirst(builds []domain.Build) {
	sort.Slice(builds, func(i, j int) bool {
		return builds[i].CreatedAt.After(builds[j].CreatedAt)
	})
}
