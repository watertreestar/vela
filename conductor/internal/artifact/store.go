package artifact

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"vela/conductor/internal/config"
	"vela/conductor/internal/domain"
)

type Store struct {
	dataDir                   string
	baseURL                   string
	backend                   string
	bucket                    string
	publicURL                 string
	artifactRoot              string
	gitlabPackageBaseURL      string
	gitlabPackageToken        string
	auditArchiveRetentionMode string
	auditArchiveRetentionDays int
	auditArchiveLegalHold     bool
	minioClient               *minio.Client
	httpClient                *http.Client
}

func NewStore(cfg config.Config) (*Store, error) {
	uploadDir := filepath.Join(cfg.DataDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return nil, err
	}
	store := &Store{
		dataDir:                   cfg.DataDir,
		baseURL:                   cfg.APIBaseURL,
		backend:                   cfg.ArtifactStore,
		bucket:                    cfg.MinIOBucket,
		publicURL:                 cfg.MinIOPublicURL,
		artifactRoot:              cfg.ArtifactRoot,
		gitlabPackageBaseURL:      strings.TrimRight(strings.TrimSpace(cfg.GitLabPackageBaseURL), "/"),
		gitlabPackageToken:        cfg.GitLabPackageToken,
		auditArchiveRetentionMode: strings.ToUpper(strings.TrimSpace(cfg.AuditArchiveRetentionMode)),
		auditArchiveRetentionDays: cfg.AuditArchiveRetentionDays,
		auditArchiveLegalHold:     cfg.AuditArchiveLegalHold,
		httpClient:                http.DefaultClient,
	}
	if store.backend == "minio" {
		if cfg.MinIOEndpoint == "" || cfg.MinIOAccessKey == "" || cfg.MinIOSecretKey == "" {
			return nil, fmt.Errorf("ARTIFACT_STORE=minio requires MINIO_ENDPOINT, MINIO_ACCESS_KEY, and MINIO_SECRET_KEY")
		}
		client, err := minio.New(cfg.MinIOEndpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.MinIOAccessKey, cfg.MinIOSecretKey, ""),
			Secure: cfg.MinIOUseSSL,
		})
		if err != nil {
			return nil, err
		}
		store.minioClient = client
		if err := store.ensureBucket(context.Background()); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) ResolveArtifactSource(source domain.ComponentSource, version string) (domain.ArtifactResolution, bool, error) {
	if source.Kind != "" && source.Kind != "artifact-repository" {
		return domain.ArtifactResolution{}, false, nil
	}
	objectPath, err := cleanArtifactPath(strings.ReplaceAll(source.Path, "{{version}}", version))
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	repository := source.Repository
	if repository == "" {
		repository = "minio"
	}
	if isGitLabRepository(repository) {
		if s.gitlabPackageBaseURL == "" {
			return domain.ArtifactResolution{}, false, nil
		}
		return s.resolveGitLabPackage(repository, objectPath)
	}
	if s.artifactRoot != "" {
		return s.resolveLocalArtifact(repository, objectPath)
	}
	if s.minioClient != nil {
		return s.resolveMinIOArtifact(repository, objectPath)
	}
	return domain.ArtifactResolution{}, false, nil
}

func (s *Store) resolveGitLabPackage(repository string, objectPath string) (domain.ArtifactResolution, bool, error) {
	if hasWildcard(objectPath) {
		return domain.ArtifactResolution{}, true, fmt.Errorf("gitlab package resolver requires exact file path: %s", objectPath)
	}
	downloadURL, err := s.gitlabPackageDownloadURL(repository, objectPath)
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	request, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	if s.gitlabPackageToken != "" {
		request.Header.Set("PRIVATE-TOKEN", s.gitlabPackageToken)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return domain.ArtifactResolution{}, true, fmt.Errorf("gitlab package download failed: %d", response.StatusCode)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, response.Body); err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	return domain.ArtifactResolution{
		URI:    downloadURL,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, true, nil
}

func (s *Store) gitlabPackageDownloadURL(repository string, objectPath string) (string, error) {
	project := ""
	if _, value, ok := strings.Cut(repository, ":"); ok {
		project = strings.TrimSpace(value)
	}
	parts := strings.Split(objectPath, "/")
	var packageName string
	var packageVersion string
	var fileParts []string
	switch {
	case project != "":
		if len(parts) < 3 {
			return "", fmt.Errorf("gitlab path requires package/version/file when repository contains project: %s", objectPath)
		}
		packageName, packageVersion, fileParts = parts[0], parts[1], parts[2:]
	case len(parts) >= 7 && parts[0] == "projects" && parts[2] == "packages" && parts[3] == "generic":
		project, packageName, packageVersion, fileParts = parts[1], parts[4], parts[5], parts[6:]
	case len(parts) >= 6 && parts[1] == "packages" && parts[2] == "generic":
		project, packageName, packageVersion, fileParts = parts[0], parts[3], parts[4], parts[5:]
	case len(parts) >= 4:
		project, packageName, packageVersion, fileParts = parts[0], parts[1], parts[2], parts[3:]
	default:
		return "", fmt.Errorf("gitlab path requires project/package/version/file: %s", objectPath)
	}
	if project == "" || packageName == "" || packageVersion == "" || len(fileParts) == 0 {
		return "", fmt.Errorf("gitlab path requires project/package/version/file: %s", objectPath)
	}
	return strings.TrimRight(s.gitlabPackageBaseURL, "/") +
		"/api/v4/projects/" + gitlabProjectPath(project) +
		"/packages/generic/" + pathEscape(packageName) +
		"/" + pathEscape(packageVersion) +
		"/" + escapePathParts(fileParts), nil
}

func (s *Store) SaveUpload(file multipart.File, header *multipart.FileHeader) (domain.UploadRecord, error) {
	if s.backend == "minio" {
		return s.saveMinIOUpload(file, header)
	}
	return s.saveLocalUpload(file, header)
}

func (s *Store) SaveAuditArchive(digest string, payload []byte, policy domain.AuditArchiveStoragePolicy) (string, error) {
	return s.saveArchive(digest, payload, policy, "audit", "audit-archives", auditArchiveObjectName, s.AuditArchiveURI, "local-audit-archive")
}

func (s *Store) SaveReleaseEvidenceArchive(digest string, payload []byte, policy domain.AuditArchiveStoragePolicy) (string, error) {
	return s.saveArchive(digest, payload, policy, "release-evidence", "release-evidence", releaseEvidenceArchiveObjectName, s.ReleaseEvidenceArchiveURI, "local-release-evidence")
}

func (s *Store) saveArchive(
	digest string,
	payload []byte,
	policy domain.AuditArchiveStoragePolicy,
	kind string,
	localDirName string,
	objectNameFor func(string) string,
	uriFor func(string) (string, error),
	localScheme string,
) (string, error) {
	uri, err := uriFor(digest)
	if err != nil {
		return "", err
	}
	digest = strings.TrimSpace(strings.ToLower(digest))
	if s.backend == "minio" {
		objectName := objectNameFor(digest)
		if _, err := s.minioClient.StatObject(context.Background(), s.bucket, objectName, minio.StatObjectOptions{}); err == nil {
			return uri, nil
		}
		_, err := s.minioClient.PutObject(
			context.Background(),
			s.bucket,
			objectName,
			bytes.NewReader(payload),
			int64(len(payload)),
			s.archivePutOptions(digest, kind, policy),
		)
		if err != nil {
			return "", err
		}
		return uri, nil
	}
	dir := filepath.Join(s.dataDir, localDirName, "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, digest+".json")
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return localScheme + "://sha256/" + digest + ".json", nil
		}
		return "", err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return localScheme + "://sha256/" + digest + ".json", nil
}

func (s *Store) AuditArchiveStoragePolicy(now time.Time) domain.AuditArchiveStoragePolicy {
	if s.backend != "minio" {
		return domain.AuditArchiveStoragePolicy{}
	}
	policy := domain.AuditArchiveStoragePolicy{}
	if retentionMode := s.auditArchiveRetentionModeValue(); retentionMode != "" && s.auditArchiveRetentionDays > 0 {
		retainUntil := now.Add(time.Duration(s.auditArchiveRetentionDays) * 24 * time.Hour).UTC()
		policy.RetentionMode = string(retentionMode)
		policy.RetainUntil = &retainUntil
	}
	if s.auditArchiveLegalHold {
		policy.LegalHold = string(minio.LegalHoldEnabled)
	}
	return policy
}

func (s *Store) auditArchivePutOptions(digest string, policy domain.AuditArchiveStoragePolicy) minio.PutObjectOptions {
	return s.archivePutOptions(digest, "audit", policy)
}

func (s *Store) archivePutOptions(digest string, kind string, policy domain.AuditArchiveStoragePolicy) minio.PutObjectOptions {
	options := minio.PutObjectOptions{
		ContentType:  "application/json",
		UserMetadata: map[string]string{"sha256": digest, "archive-kind": kind},
	}
	if mode := minio.RetentionMode(policy.RetentionMode); mode.IsValid() && policy.RetainUntil != nil {
		options.Mode = mode
		options.RetainUntilDate = *policy.RetainUntil
	}
	if policy.LegalHold == string(minio.LegalHoldEnabled) {
		options.LegalHold = minio.LegalHoldEnabled
	}
	return options
}

func (s *Store) auditArchiveRetentionModeValue() minio.RetentionMode {
	switch s.auditArchiveRetentionMode {
	case "GOVERNANCE":
		return minio.Governance
	case "COMPLIANCE":
		return minio.Compliance
	default:
		return ""
	}
}

func (s *Store) AuditArchiveURI(digest string) (string, error) {
	digest = strings.TrimSpace(strings.ToLower(digest))
	if len(digest) != 64 {
		return "", fmt.Errorf("invalid audit archive digest: %s", digest)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid audit archive digest: %s", digest)
	}
	if s.backend == "minio" {
		return fmt.Sprintf("minio://%s/%s", s.bucket, auditArchiveObjectName(digest)), nil
	}
	return "local-audit-archive://sha256/" + digest + ".json", nil
}

func (s *Store) ReleaseEvidenceArchiveURI(digest string) (string, error) {
	digest = strings.TrimSpace(strings.ToLower(digest))
	if len(digest) != 64 {
		return "", fmt.Errorf("invalid release evidence archive digest: %s", digest)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid release evidence archive digest: %s", digest)
	}
	if s.backend == "minio" {
		return fmt.Sprintf("minio://%s/%s", s.bucket, releaseEvidenceArchiveObjectName(digest)), nil
	}
	return "local-release-evidence://sha256/" + digest + ".json", nil
}

func (s *Store) DeleteUpload(id string) error {
	if s.backend == "minio" {
		prefix := "uploads/" + id + "/"
		objects := s.minioClient.ListObjects(context.Background(), s.bucket, minio.ListObjectsOptions{Prefix: prefix})
		for item := range objects {
			if item.Err != nil {
				return item.Err
			}
			if err := s.minioClient.RemoveObject(context.Background(), s.bucket, item.Key, minio.RemoveObjectOptions{}); err != nil {
				return err
			}
		}
		return nil
	}
	return os.RemoveAll(filepath.Join(s.dataDir, "uploads", id))
}

func (s *Store) ReadUpload(id string) ([]byte, error) {
	if s.backend == "minio" {
		prefix := "uploads/" + id + "/"
		for item := range s.minioClient.ListObjects(context.Background(), s.bucket, minio.ListObjectsOptions{Prefix: prefix}) {
			if item.Err != nil {
				return nil, item.Err
			}
			object, err := s.minioClient.GetObject(context.Background(), s.bucket, item.Key, minio.GetObjectOptions{})
			if err != nil {
				return nil, err
			}
			defer object.Close()
			return io.ReadAll(object)
		}
		return nil, os.ErrNotExist
	}
	file, _, err := s.UploadPath(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(file)
}

func (s *Store) ServeUpload(w http.ResponseWriter, r *http.Request, id string) error {
	if s.backend == "minio" {
		return s.serveMinIOUpload(w, r, id)
	}
	file, name, err := s.UploadPath(id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeFile(w, r, file)
	return nil
}

func (s *Store) saveLocalUpload(file multipart.File, header *multipart.FileHeader) (domain.UploadRecord, error) {
	id := randomID()
	dir := filepath.Join(s.dataDir, "uploads", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.UploadRecord{}, err
	}
	target := filepath.Join(dir, filepath.Base(header.Filename))
	out, err := os.Create(target)
	if err != nil {
		return domain.UploadRecord{}, err
	}
	defer out.Close()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hash), file)
	if err != nil {
		return domain.UploadRecord{}, err
	}

	return domain.UploadRecord{
		ID:             id,
		FileName:       header.Filename,
		URI:            fmt.Sprintf("local-upload://%s/%s", id, header.Filename),
		DownloadURL:    fmt.Sprintf("%s/api/uploads/%s/download", s.baseURL, id),
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Size:           size,
		ContentType:    header.Header.Get("Content-Type"),
		DownloadPolicy: "platform",
		RetentionDays:  30,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

func (s *Store) saveMinIOUpload(file multipart.File, header *multipart.FileHeader) (domain.UploadRecord, error) {
	id := randomID()
	filename := filepath.Base(header.Filename)
	tmp, err := os.CreateTemp("", "packaging-upload-*")
	if err != nil {
		return domain.UploadRecord{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), file)
	if err != nil {
		return domain.UploadRecord{}, err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return domain.UploadRecord{}, err
	}

	digest := hex.EncodeToString(hash.Sum(nil))
	objectName := minioObjectName(id, filename)
	_, err = s.minioClient.PutObject(
		context.Background(),
		s.bucket,
		objectName,
		tmp,
		size,
		minio.PutObjectOptions{
			ContentType:  header.Header.Get("Content-Type"),
			UserMetadata: map[string]string{"sha256": digest},
		},
	)
	if err != nil {
		return domain.UploadRecord{}, err
	}

	return domain.UploadRecord{
		ID:             id,
		FileName:       filename,
		URI:            fmt.Sprintf("minio://%s/%s", s.bucket, objectName),
		DownloadURL:    fmt.Sprintf("%s/api/uploads/%s/download", s.baseURL, id),
		SHA256:         digest,
		Size:           size,
		ContentType:    header.Header.Get("Content-Type"),
		DownloadPolicy: "platform",
		RetentionDays:  30,
		CreatedAt:      time.Now().UTC(),
	}, nil
}

func (s *Store) serveMinIOUpload(w http.ResponseWriter, r *http.Request, id string) error {
	ctx := r.Context()
	prefix := "uploads/" + id + "/"
	for item := range s.minioClient.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix}) {
		if item.Err != nil {
			return item.Err
		}
		object, err := s.minioClient.GetObject(ctx, s.bucket, item.Key, minio.GetObjectOptions{})
		if err != nil {
			return err
		}
		defer object.Close()
		name := filepath.Base(item.Key)
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
		w.Header().Set("Content-Type", "application/octet-stream")
		_, err = io.Copy(w, object)
		return err
	}
	return os.ErrNotExist
}

func (s *Store) UploadPath(id string) (string, string, error) {
	dir := filepath.Join(s.dataDir, "uploads", id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		return filepath.Join(dir, entry.Name()), entry.Name(), nil
	}
	return "", "", os.ErrNotExist
}

func (s *Store) resolveLocalArtifact(repository string, objectPath string) (domain.ArtifactResolution, bool, error) {
	pattern := filepath.Join(s.artifactRoot, filepath.FromSlash(objectPath))
	if !hasWildcard(objectPath) {
		info, err := os.Stat(pattern)
		if err != nil {
			return domain.ArtifactResolution{}, true, err
		}
		if info.IsDir() {
			return domain.ArtifactResolution{}, true, fmt.Errorf("artifact path is a directory: %s", objectPath)
		}
		sha, err := fileSHA256(pattern)
		if err != nil {
			return domain.ArtifactResolution{}, true, err
		}
		return domain.ArtifactResolution{URI: repositoryURI(repository, objectPath), SHA256: sha}, true, nil
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	files := make([]string, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, match)
	}
	if len(files) == 0 {
		return domain.ArtifactResolution{}, true, fmt.Errorf("artifact wildcard did not match: %s", objectPath)
	}
	sort.Slice(files, func(i, j int) bool {
		left, _ := os.Stat(files[i])
		right, _ := os.Stat(files[j])
		if left.ModTime().Equal(right.ModTime()) {
			return files[i] > files[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	relative, err := filepath.Rel(s.artifactRoot, files[0])
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	resolvedPath := filepath.ToSlash(relative)
	sha, err := fileSHA256(files[0])
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	return domain.ArtifactResolution{URI: repositoryURI(repository, resolvedPath), SHA256: sha}, true, nil
}

func (s *Store) resolveMinIOArtifact(repository string, objectPath string) (domain.ArtifactResolution, bool, error) {
	bucket, objectName := s.minioBucketObject(repository, objectPath)
	if !hasWildcard(objectPath) {
		info, err := s.minioClient.StatObject(context.Background(), bucket, objectName, minio.StatObjectOptions{})
		if err != nil {
			return domain.ArtifactResolution{}, true, err
		}
		sha, err := s.minioObjectSHA256(bucket, objectName, info.UserMetadata)
		if err != nil {
			return domain.ArtifactResolution{}, true, err
		}
		return domain.ArtifactResolution{
			URI:    fmt.Sprintf("minio://%s/%s", bucket, objectName),
			SHA256: sha,
		}, true, nil
	}

	prefix := objectName[:firstWildcardIndex(objectName)]
	if slash := strings.LastIndex(prefix, "/"); slash >= 0 {
		prefix = prefix[:slash+1]
	} else {
		prefix = ""
	}
	var best minio.ObjectInfo
	found := false
	for item := range s.minioClient.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if item.Err != nil {
			return domain.ArtifactResolution{}, true, item.Err
		}
		matched, err := pathpkg.Match(objectName, item.Key)
		if err != nil {
			return domain.ArtifactResolution{}, true, err
		}
		if !matched {
			continue
		}
		if !found || item.LastModified.After(best.LastModified) || (item.LastModified.Equal(best.LastModified) && item.Key > best.Key) {
			best = item
			found = true
		}
	}
	if !found {
		return domain.ArtifactResolution{}, true, fmt.Errorf("artifact wildcard did not match: %s", objectPath)
	}
	info, err := s.minioClient.StatObject(context.Background(), bucket, best.Key, minio.StatObjectOptions{})
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	sha, err := s.minioObjectSHA256(bucket, best.Key, info.UserMetadata)
	if err != nil {
		return domain.ArtifactResolution{}, true, err
	}
	return domain.ArtifactResolution{
		URI:    fmt.Sprintf("minio://%s/%s", bucket, best.Key),
		SHA256: sha,
	}, true, nil
}

func (s *Store) minioObjectSHA256(bucket string, objectName string, metadata map[string]string) (string, error) {
	if sha := metadataSHA256(metadata); sha != "" {
		return sha, nil
	}
	object, err := s.minioClient.GetObject(context.Background(), bucket, objectName+".sha256", minio.GetObjectOptions{})
	if err != nil {
		return "", nil
	}
	defer object.Close()
	payload, err := io.ReadAll(object)
	if err != nil {
		return "", err
	}
	return parseSHA256Sidecar(string(payload)), nil
}

func (s *Store) minioBucketObject(repository string, objectPath string) (string, string) {
	if repository != "" && repository != "minio" {
		return repository, objectPath
	}
	if bucket, objectName, ok := strings.Cut(objectPath, "/"); ok && bucket != "" && objectName != "" {
		return bucket, objectName
	}
	return s.bucket, objectPath
}

func cleanArtifactPath(value string) (string, error) {
	clean := pathpkg.Clean(strings.TrimLeft(strings.ReplaceAll(value, "\\", "/"), "/"))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("invalid artifact path: %s", value)
	}
	return clean, nil
}

func repositoryURI(repository string, objectPath string) string {
	if repository == "" {
		repository = "minio"
	}
	return fmt.Sprintf("%s://%s", repository, objectPath)
}

func isGitLabRepository(repository string) bool {
	normalized := strings.ToLower(strings.TrimSpace(repository))
	return normalized == "gitlab" || normalized == "gitlab-generic" || strings.HasPrefix(normalized, "gitlab:")
}

func gitlabProjectPath(project string) string {
	project = strings.TrimSpace(project)
	if strings.Contains(project, "%2F") || strings.Contains(project, "%2f") {
		return project
	}
	return pathEscape(project)
}

func escapePathParts(parts []string) string {
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, pathEscape(part))
	}
	return strings.Join(escaped, "/")
}

func pathEscape(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

func hasWildcard(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

func firstWildcardIndex(value string) int {
	index := len(value)
	for _, marker := range []string{"*", "?", "["} {
		if current := strings.Index(value, marker); current >= 0 && current < index {
			index = current
		}
	}
	return index
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func metadataSHA256(metadata map[string]string) string {
	for key, value := range metadata {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "sha256" || normalized == "x-amz-meta-sha256" {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func parseSHA256Sidecar(payload string) string {
	fields := strings.Fields(payload)
	if len(fields) == 0 {
		return ""
	}
	value := strings.TrimSpace(fields[0])
	if len(value) == 64 {
		return value
	}
	return ""
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *Store) ensureBucket(ctx context.Context) error {
	exists, err := s.minioClient.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.minioClient.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{ObjectLocking: s.auditArchiveRetentionModeValue() != "" || s.auditArchiveLegalHold})
}

func minioObjectName(id string, filename string) string {
	return "uploads/" + id + "/" + filepath.Base(filename)
}

func auditArchiveObjectName(digest string) string {
	return "audit-archives/sha256/" + digest + ".json"
}

func releaseEvidenceArchiveObjectName(digest string) string {
	return "release-evidence/sha256/" + digest + ".json"
}
