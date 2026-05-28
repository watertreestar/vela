package artifact

import (
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
	"path/filepath"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"packaging-platform/backend/internal/config"
	"packaging-platform/backend/internal/domain"
)

type Store struct {
	dataDir     string
	baseURL     string
	backend     string
	bucket      string
	publicURL   string
	minioClient *minio.Client
}

func NewStore(cfg config.Config) (*Store, error) {
	uploadDir := filepath.Join(cfg.DataDir, "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return nil, err
	}
	store := &Store{
		dataDir:   cfg.DataDir,
		baseURL:   cfg.APIBaseURL,
		backend:   cfg.ArtifactStore,
		bucket:    cfg.MinIOBucket,
		publicURL: cfg.MinIOPublicURL,
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

func (s *Store) SaveUpload(file multipart.File, header *multipart.FileHeader) (domain.UploadRecord, error) {
	if s.backend == "minio" {
		return s.saveMinIOUpload(file, header)
	}
	return s.saveLocalUpload(file, header)
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
		ID:          id,
		FileName:    header.Filename,
		URI:         fmt.Sprintf("local-upload://%s/%s", id, header.Filename),
		DownloadURL: fmt.Sprintf("%s/api/uploads/%s/download", s.baseURL, id),
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		Size:        size,
		CreatedAt:   time.Now().UTC(),
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

	objectName := minioObjectName(id, filename)
	_, err = s.minioClient.PutObject(
		context.Background(),
		s.bucket,
		objectName,
		tmp,
		size,
		minio.PutObjectOptions{ContentType: header.Header.Get("Content-Type")},
	)
	if err != nil {
		return domain.UploadRecord{}, err
	}

	return domain.UploadRecord{
		ID:          id,
		FileName:    filename,
		URI:         fmt.Sprintf("minio://%s/%s", s.bucket, objectName),
		DownloadURL: fmt.Sprintf("%s/api/uploads/%s/download", s.baseURL, id),
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		Size:        size,
		CreatedAt:   time.Now().UTC(),
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
	return s.minioClient.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}

func minioObjectName(id string, filename string) string {
	return "uploads/" + id + "/" + filepath.Base(filename)
}
