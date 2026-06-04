package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"packaging-platform/backend/internal/apperr"
)

var overlayNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateOverlayKind(kind string) error {
	if kind != "profiles" && kind != "customers" {
		return apperr.Validation("VALIDATION_INVALID_REQUEST", fmt.Sprintf("unsupported overlay kind %q", kind)).
			WithField("kind").
			WithParam("kind", kind)
	}
	return nil
}

func validateOverlayName(name string) error {
	if name == "" || !overlayNamePattern.MatchString(name) {
		return apperr.Validation("VALIDATION_INVALID_REQUEST", fmt.Sprintf("invalid overlay name %q", name)).
			WithField("name").
			WithParam("name", name)
	}
	return nil
}

func listOverlayFiles(productsDir string, productID string, kind string) ([]OverlayRecord, error) {
	if err := validateOverlayKind(kind); err != nil {
		return nil, err
	}
	dir := filepath.Join(productsDir, productID, kind)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []OverlayRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := []OverlayRecord{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := entry.Name()[:len(entry.Name())-len(".json")]
		overlay, err := readOverlayFile(productsDir, productID, kind, name)
		if err != nil {
			return nil, err
		}
		records = append(records, OverlayRecord{Name: name, Kind: kind, Data: overlay})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Name < records[j].Name
	})
	return records, nil
}

func readOverlayFile(productsDir string, productID string, kind string, name string) (map[string]any, error) {
	if err := validateOverlayKind(kind); err != nil {
		return nil, err
	}
	if err := validateOverlayName(name); err != nil {
		return nil, err
	}
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

func writeOverlayFile(productsDir string, productID string, kind string, name string, raw json.RawMessage) (OverlayRecord, error) {
	if err := validateOverlayKind(kind); err != nil {
		return OverlayRecord{}, err
	}
	if err := validateOverlayName(name); err != nil {
		return OverlayRecord{}, err
	}
	var overlay map[string]any
	if err := json.Unmarshal(raw, &overlay); err != nil {
		return OverlayRecord{}, err
	}
	dir := filepath.Join(productsDir, productID, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return OverlayRecord{}, err
	}
	content, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return OverlayRecord{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), content, 0o644); err != nil {
		return OverlayRecord{}, err
	}
	return OverlayRecord{Name: name, Kind: kind, Data: overlay}, nil
}
