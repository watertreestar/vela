package store

import (
	"encoding/json"

	"packaging-platform/backend/internal/domain"
)

type Store interface {
	ListProducts() ([]domain.Product, error)
	ReadProduct(id string) (domain.Product, error)
	ReadProductManifest(id string) (json.RawMessage, error)
	WriteProductManifest(id string, raw json.RawMessage) (domain.Product, error)
	ReadOverlay(productID string, kind string, name string) (map[string]any, error)
	ListBuilds() ([]domain.Build, error)
	ReadBuild(id string) (domain.Build, error)
	SaveBuild(build domain.Build) error
	UpdateBuild(id string, update func(*domain.Build) error) (domain.Build, error)
	ClaimBuild(target string, runnerID string) (domain.Build, bool, error)
	RetryBuild(id string) (domain.Build, error)
	CancelBuild(id string) (domain.Build, error)
	SaveUploadRecord(record domain.UploadRecord) error
	ListUploads() ([]domain.UploadRecord, error)
	ReadUpload(id string) (domain.UploadRecord, error)
}
