package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOverlayFiles(t *testing.T) {
	productsDir := t.TempDir()
	productDir := filepath.Join(productsDir, "lsap")
	if err := os.MkdirAll(filepath.Join(productDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "profiles", "qa.json"), []byte(`{"config":{"ports":{"web":48001}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := listOverlayFiles(productsDir, "lsap", "profiles")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != "qa" {
		t.Fatalf("unexpected records: %#v", records)
	}

	record, err := writeOverlayFile(productsDir, "lsap", "customers", "acme", json.RawMessage(`{"config":{"customer":{"name":"ACME"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "acme" || record.Kind != "customers" {
		t.Fatalf("unexpected written record: %#v", record)
	}

	overlay, err := readOverlayFile(productsDir, "lsap", "customers", "acme")
	if err != nil {
		t.Fatal(err)
	}
	config := overlay["config"].(map[string]any)
	customer := config["customer"].(map[string]any)
	if customer["name"] != "ACME" {
		t.Fatalf("unexpected overlay: %#v", overlay)
	}
}

func TestOverlayValidation(t *testing.T) {
	productsDir := t.TempDir()
	if _, err := listOverlayFiles(productsDir, "lsap", "bad-kind"); err == nil {
		t.Fatal("expected invalid kind error")
	}
	if _, err := writeOverlayFile(productsDir, "lsap", "profiles", "../bad", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected invalid name error")
	}
}
