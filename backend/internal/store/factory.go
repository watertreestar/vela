package store

import (
	"fmt"

	"packaging-platform/backend/internal/config"
)

func New(cfg config.Config) (Store, error) {
	switch cfg.StoreDriver {
	case "postgres", "postgresql", "":
		return NewPostgresStore(cfg.DatabaseURL, cfg.ProductsDir)
	case "json":
		return NewJSONStore(cfg.DataDir, cfg.ProductsDir)
	default:
		return nil, fmt.Errorf("unsupported STORE_DRIVER %q", cfg.StoreDriver)
	}
}
