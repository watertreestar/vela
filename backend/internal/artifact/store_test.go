package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"packaging-platform/backend/internal/config"
	"packaging-platform/backend/internal/domain"
)

func TestResolveArtifactSourceFromLocalRootLocksWildcardAndSHA(t *testing.T) {
	dataDir := t.TempDir()
	artifactRoot := t.TempDir()
	artifactDir := filepath.Join(artifactRoot, "artifact", "engine", "1.2.3")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(artifactDir, "engine-1.2.2.tar.gz")
	newPath := filepath.Join(artifactDir, "engine-1.2.3.tar.gz")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
		ArtifactRoot:  artifactRoot,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolution, resolved, err := store.ResolveArtifactSource(domain.ComponentSource{
		Kind:       "artifact-repository",
		Repository: "minio",
		Path:       "artifact/engine/{{version}}/engine-*.tar.gz",
	}, "1.2.3")
	if err != nil {
		t.Fatalf("ResolveArtifactSource returned error: %v", err)
	}
	if !resolved {
		t.Fatal("expected artifact source to be resolved")
	}
	if resolution.URI != "minio://artifact/engine/1.2.3/engine-1.2.3.tar.gz" {
		t.Fatalf("unexpected resolved uri: %s", resolution.URI)
	}
	hash := sha256.Sum256([]byte("new"))
	if resolution.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("unexpected sha256: %s", resolution.SHA256)
	}
}

func TestResolveArtifactSourceWithoutResolverBackendKeepsFallbackAvailable(t *testing.T) {
	store, err := NewStore(config.Config{
		DataDir:       t.TempDir(),
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, resolved, err := store.ResolveArtifactSource(domain.ComponentSource{
		Kind: "artifact-repository",
		Path: "artifact/engine/{{version}}/engine.tar.gz",
	}, "1.2.3")
	if err != nil {
		t.Fatalf("ResolveArtifactSource returned error: %v", err)
	}
	if resolved {
		t.Fatal("expected unresolved source when ARTIFACT_ROOT and MinIO are not configured")
	}
}

func TestResolveArtifactSourceFromGitLabGenericPackage(t *testing.T) {
	payload := []byte("gitlab package artifact")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/42/packages/generic/engine/1.2.3/engine.tar.gz" {
			t.Fatalf("unexpected gitlab request path: %s", r.URL.Path)
		}
		if r.Header.Get("PRIVATE-TOKEN") != "gitlab-token" {
			t.Fatalf("gitlab token header missing: %#v", r.Header)
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	store, err := NewStore(config.Config{
		DataDir:              t.TempDir(),
		APIBaseURL:           "http://api.test",
		ArtifactStore:        "local",
		GitLabPackageBaseURL: server.URL,
		GitLabPackageToken:   "gitlab-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	resolution, resolved, err := store.ResolveArtifactSource(domain.ComponentSource{
		Kind:       "artifact-repository",
		Repository: "gitlab",
		Path:       "42/engine/{{version}}/engine.tar.gz",
	}, "1.2.3")
	if err != nil {
		t.Fatalf("ResolveArtifactSource returned error: %v", err)
	}
	if !resolved {
		t.Fatal("expected GitLab artifact source to resolve")
	}
	if resolution.URI != server.URL+"/api/v4/projects/42/packages/generic/engine/1.2.3/engine.tar.gz" {
		t.Fatalf("unexpected GitLab resolved uri: %s", resolution.URI)
	}
	hash := sha256.Sum256(payload)
	if resolution.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("unexpected GitLab sha256: %s", resolution.SHA256)
	}
}

func TestResolveArtifactSourceFromGitLabRejectsWildcard(t *testing.T) {
	store, err := NewStore(config.Config{
		DataDir:              t.TempDir(),
		APIBaseURL:           "http://api.test",
		ArtifactStore:        "local",
		GitLabPackageBaseURL: "https://gitlab.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, resolved, err := store.ResolveArtifactSource(domain.ComponentSource{
		Kind:       "artifact-repository",
		Repository: "gitlab:42",
		Path:       "engine/{{version}}/engine-*.tar.gz",
	}, "1.2.3")
	if err == nil {
		t.Fatal("expected GitLab wildcard to fail")
	}
	if !resolved {
		t.Fatal("GitLab wildcard is handled by resolver and should return resolved=true with an error")
	}
}

func TestMinIOBucketObjectUsesRepositoryOrPathBucket(t *testing.T) {
	store := Store{bucket: "packaging-artifacts"}
	bucket, objectName := store.minioBucketObject("service-artifacts", "Publish/engine/app.tar.gz")
	if bucket != "service-artifacts" || objectName != "Publish/engine/app.tar.gz" {
		t.Fatalf("unexpected repository bucket split: bucket=%s object=%s", bucket, objectName)
	}
	bucket, objectName = store.minioBucketObject("minio", "artifact/Publish/engine/app.tar.gz")
	if bucket != "artifact" || objectName != "Publish/engine/app.tar.gz" {
		t.Fatalf("unexpected path bucket split: bucket=%s object=%s", bucket, objectName)
	}
	bucket, objectName = store.minioBucketObject("minio", "app.tar.gz")
	if bucket != "packaging-artifacts" || objectName != "app.tar.gz" {
		t.Fatalf("unexpected fallback bucket split: bucket=%s object=%s", bucket, objectName)
	}
}

func TestParseSHA256Sidecar(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := parseSHA256Sidecar(hash + "  engine.tar.gz\n"); got != hash {
		t.Fatalf("unexpected parsed sidecar hash: %s", got)
	}
	if got := parseSHA256Sidecar("not-a-sha engine.tar.gz"); got != "" {
		t.Fatalf("invalid sidecar should not parse: %s", got)
	}
}

func TestAuditArchiveStoragePolicyMapsMinIORetentionAndLegalHold(t *testing.T) {
	store := Store{
		backend:                   "minio",
		auditArchiveRetentionMode: "COMPLIANCE",
		auditArchiveRetentionDays: 365,
		auditArchiveLegalHold:     true,
	}
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	policy := store.AuditArchiveStoragePolicy(now)
	if policy.RetentionMode != "COMPLIANCE" || policy.RetainUntil == nil {
		t.Fatalf("expected compliance retention policy, got %#v", policy)
	}
	if !policy.RetainUntil.Equal(now.Add(365 * 24 * time.Hour)) {
		t.Fatalf("unexpected retain-until: %s", policy.RetainUntil)
	}
	if policy.LegalHold != "ON" {
		t.Fatalf("expected legal hold ON, got %#v", policy)
	}
	options := store.auditArchivePutOptions("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", policy)
	if options.Mode != minio.Compliance || !options.RetainUntilDate.Equal(*policy.RetainUntil) || options.LegalHold != minio.LegalHoldEnabled {
		t.Fatalf("unexpected put options: %#v", options)
	}
}

func TestAuditArchiveStoragePolicyIsEmptyForLocalStore(t *testing.T) {
	store := Store{
		backend:                   "local",
		auditArchiveRetentionMode: "COMPLIANCE",
		auditArchiveRetentionDays: 365,
		auditArchiveLegalHold:     true,
	}
	policy := store.AuditArchiveStoragePolicy(time.Now().UTC())
	if policy.RetentionMode != "" || policy.RetainUntil != nil || policy.LegalHold != "" {
		t.Fatalf("local store must not claim object-lock policy, got %#v", policy)
	}
}

func TestSaveReleaseEvidenceArchiveUsesSeparateLocalPath(t *testing.T) {
	store, err := NewStore(config.Config{
		DataDir:       t.TempDir(),
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	uri, err := store.SaveReleaseEvidenceArchive(digest, []byte(`{"schemaVersion":"packaging.release.evidence/v1"}`), domain.AuditArchiveStoragePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	if uri != "local-release-evidence://sha256/"+digest+".json" {
		t.Fatalf("unexpected release evidence uri: %s", uri)
	}
	if _, err := os.Stat(filepath.Join(store.dataDir, "release-evidence", "sha256", digest+".json")); err != nil {
		t.Fatalf("release evidence archive was not persisted: %v", err)
	}
}

func TestResolveArtifactSourceWithRustFSIntegration(t *testing.T) {
	if os.Getenv("PACKAGING_RUSTFS_INTEGRATION") != "1" {
		t.Skip("set PACKAGING_RUSTFS_INTEGRATION=1 to run against local RustFS/MinIO")
	}
	endpoint := envOr("RUSTFS_ENDPOINT", "127.0.0.1:9000")
	accessKey := envOr("RUSTFS_ACCESS_KEY", "rustfsadmin")
	secretKey := envOr("RUSTFS_SECRET_KEY", "rustfsadmin")
	bucket := envOr("RUSTFS_BUCKET", fmt.Sprintf("packaging-it-%d", time.Now().UnixNano()))

	store, err := NewStore(config.Config{
		DataDir:        t.TempDir(),
		APIBaseURL:     "http://api.test",
		ArtifactStore:  "minio",
		MinIOEndpoint:  endpoint,
		MinIOAccessKey: accessKey,
		MinIOSecretKey: secretKey,
		MinIOBucket:    bucket,
		MinIOUseSSL:    envBool("RUSTFS_USE_SSL", false),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oldKey := "Publish/engine/1.2.3/engine-old.tar.gz"
	newKey := "Publish/engine/1.2.3/engine-new.tar.gz"
	oldPayload := []byte("old rustfs artifact")
	newPayload := []byte("new rustfs artifact")
	if _, err := store.minioClient.PutObject(ctx, bucket, oldKey, bytes.NewReader(oldPayload), int64(len(oldPayload)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := store.minioClient.PutObject(ctx, bucket, newKey, bytes.NewReader(newPayload), int64(len(newPayload)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(newPayload)
	sidecar := []byte(hex.EncodeToString(hash[:]) + "  engine-new.tar.gz\n")
	if _, err := store.minioClient.PutObject(ctx, bucket, newKey+".sha256", bytes.NewReader(sidecar), int64(len(sidecar)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.minioClient.RemoveObject(ctx, bucket, oldKey, minio.RemoveObjectOptions{})
		_ = store.minioClient.RemoveObject(ctx, bucket, newKey, minio.RemoveObjectOptions{})
		_ = store.minioClient.RemoveObject(ctx, bucket, newKey+".sha256", minio.RemoveObjectOptions{})
	})

	resolution, resolved, err := store.ResolveArtifactSource(domain.ComponentSource{
		Kind:       "artifact-repository",
		Repository: bucket,
		Path:       "Publish/engine/{{version}}/engine-*.tar.gz",
	}, "1.2.3")
	if err != nil {
		t.Fatalf("ResolveArtifactSource returned error: %v", err)
	}
	if !resolved {
		t.Fatal("expected RustFS artifact source to resolve")
	}
	if resolution.URI != "minio://"+bucket+"/"+newKey {
		t.Fatalf("unexpected RustFS resolved uri: %s", resolution.URI)
	}
	if resolution.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("unexpected RustFS resolved sha256: %s", resolution.SHA256)
	}
}

func TestAuditArchiveObjectLockWithRustFSIntegration(t *testing.T) {
	if os.Getenv("PACKAGING_RUSTFS_OBJECT_LOCK_INTEGRATION") != "1" {
		t.Skip("set PACKAGING_RUSTFS_OBJECT_LOCK_INTEGRATION=1 to run against local RustFS/MinIO object-lock bucket")
	}
	endpoint := envOr("RUSTFS_ENDPOINT", "127.0.0.1:9000")
	accessKey := envOr("RUSTFS_ACCESS_KEY", "rustfsadmin")
	secretKey := envOr("RUSTFS_SECRET_KEY", "rustfsadmin")
	bucket := envOr("RUSTFS_BUCKET", fmt.Sprintf("packaging-lock-%d", time.Now().UnixNano()))

	store, err := NewStore(config.Config{
		DataDir:                   t.TempDir(),
		APIBaseURL:                "http://api.test",
		ArtifactStore:             "minio",
		MinIOEndpoint:             endpoint,
		MinIOAccessKey:            accessKey,
		MinIOSecretKey:            secretKey,
		MinIOBucket:               bucket,
		MinIOUseSSL:               envBool("RUSTFS_USE_SSL", false),
		AuditArchiveRetentionMode: envOr("RUSTFS_OBJECT_LOCK_RETENTION_MODE", "GOVERNANCE"),
		AuditArchiveRetentionDays: envInt("RUSTFS_OBJECT_LOCK_RETENTION_DAYS", 1),
		AuditArchiveLegalHold:     envBool("RUSTFS_OBJECT_LOCK_LEGAL_HOLD", true),
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	now := time.Now().UTC()
	policy := store.AuditArchiveStoragePolicy(now)
	uri, err := store.SaveAuditArchive(digest, []byte(`{"schemaVersion":"test"}`), policy)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "minio://"+bucket+"/audit-archives/sha256/"+digest+".json" {
		t.Fatalf("unexpected archive uri: %s", uri)
	}
	mode, retainUntil, err := store.minioClient.GetObjectRetention(context.Background(), bucket, auditArchiveObjectName(digest), "")
	if err != nil {
		t.Fatal(err)
	}
	expectedMode := minio.RetentionMode(envOr("RUSTFS_OBJECT_LOCK_RETENTION_MODE", "GOVERNANCE"))
	if mode == nil || *mode != expectedMode || retainUntil == nil || retainUntil.Before(now) {
		t.Fatalf("unexpected object retention: mode=%v retainUntil=%v", mode, retainUntil)
	}
	legalHold, err := store.minioClient.GetObjectLegalHold(context.Background(), bucket, auditArchiveObjectName(digest), minio.GetObjectLegalHoldOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if envBool("RUSTFS_OBJECT_LOCK_LEGAL_HOLD", true) && (legalHold == nil || *legalHold != minio.LegalHoldEnabled) {
		t.Fatalf("unexpected legal hold: %v", legalHold)
	}
}

func envOr(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value == "true" || value == "1" || value == "yes"
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
