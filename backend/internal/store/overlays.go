package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func readOverlayFile(productsDir string, productID string, kind string, name string) (map[string]any, error) {
	candidates := []string{
		filepath.Join(productsDir, productID, kind, name+".json"),
		filepath.Join(filepath.Dir(productsDir), kind, name+".json"),
	}
	var lastErr error
	for _, file := range candidates {
		content, err := os.ReadFile(file)
		if err != nil {
			lastErr = err
			continue
		}
		var overlay map[string]any
		if err := json.Unmarshal(content, &overlay); err != nil {
			return nil, err
		}
		return overlay, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, os.ErrNotExist
}
