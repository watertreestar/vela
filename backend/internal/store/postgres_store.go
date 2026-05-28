package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"packaging-platform/backend/internal/domain"
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
		return domain.Product{}, fmt.Errorf("manifest id %q does not match product %q", product.ID, id)
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
	return product, err
}

func (s *PostgresStore) ReadOverlay(productID string, kind string, name string) (map[string]any, error) {
	return readOverlayFile(s.productsDir, productID, kind, name)
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

func (s *PostgresStore) ClaimBuild(target string, runnerID string) (domain.Build, bool, error) {
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
		Time:    nowUTC(),
		Source:  "runner",
		Message: fmt.Sprintf("claimed by runner %s", runnerID),
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
			return fmt.Errorf("only failed or canceled builds can be retried")
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
			return fmt.Errorf("only pending, queued, or running builds can be canceled")
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
		return domain.UploadRecord{}, err
	}
	var record domain.UploadRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return domain.UploadRecord{}, err
	}
	return record, nil
}

func (s *PostgresStore) readBuildForUpdate(ctx context.Context, tx *sql.Tx, id string) (domain.Build, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `select payload from builds where id = $1 for update`, id).Scan(&raw); err != nil {
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
