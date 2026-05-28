package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storepkg "packaging-platform/backend/internal/store"
)

func TestProductOverlayRoutes(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodGet, "/api/products/lsap/overlays/profiles", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected list overlays status 200, got %d: %s", response.Code, response.Body.String())
	}
	var records []storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != "qa" {
		t.Fatalf("unexpected overlay list: %#v", records)
	}

	response = doRequest(
		t,
		server,
		http.MethodPut,
		"/api/products/lsap/overlays/customers/acme",
		`{"config":{"customer":{"name":"ACME"}}}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("expected write overlay status 200, got %d: %s", response.Code, response.Body.String())
	}
	var written storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&written); err != nil {
		t.Fatal(err)
	}
	if written.Name != "acme" || written.Kind != "customers" {
		t.Fatalf("unexpected written overlay: %#v", written)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/lsap/overlays/customers/acme", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected read overlay status 200, got %d: %s", response.Code, response.Body.String())
	}
	var read storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&read); err != nil {
		t.Fatal(err)
	}
	config := read.Data["config"].(map[string]any)
	customer := config["customer"].(map[string]any)
	if customer["name"] != "ACME" {
		t.Fatalf("unexpected read overlay data: %#v", read.Data)
	}
}

func TestProductOverlayRoutesRejectInvalidInput(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodGet, "/api/products/lsap/overlays/bad-kind", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid kind status 400, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPut, "/api/products/lsap/overlays/profiles/bad%20name", `{}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid name status 400, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPut, "/api/products/lsap/overlays/profiles/dev", `[]`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected non-object JSON status 400, got %d: %s", response.Code, response.Body.String())
	}
}

func newTestServerStore(t *testing.T) (*storepkg.JSONStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	productsDir := t.TempDir()
	productDir := filepath.Join(productsDir, "lsap")
	if err := os.MkdirAll(filepath.Join(productDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "product.json"), []byte(`{"id":"lsap","name":"LSAP"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "profiles", "qa.json"), []byte(`{"config":{"ports":{"web":48001}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := storepkg.NewJSONStore(dataDir, productsDir)
	if err != nil {
		t.Fatal(err)
	}
	return store, productsDir
}

func doRequest(t *testing.T, handler http.Handler, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
