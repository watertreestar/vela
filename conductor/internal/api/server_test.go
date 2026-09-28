package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vela/conductor/internal/apperr"
	"vela/conductor/internal/artifact"
	"vela/conductor/internal/config"
	"vela/conductor/internal/domain"
	storepkg "vela/conductor/internal/store"
)

type failingArtifactStore struct {
	saveUploadErr error
}

func (s failingArtifactStore) ResolveArtifactSource(domain.ComponentSource, string) (domain.ArtifactResolution, bool, error) {
	return domain.ArtifactResolution{}, false, nil
}

func (s failingArtifactStore) SaveUpload(multipart.File, *multipart.FileHeader) (domain.UploadRecord, error) {
	if s.saveUploadErr != nil {
		return domain.UploadRecord{}, s.saveUploadErr
	}
	return domain.UploadRecord{}, errors.New("save upload failed")
}

func (s failingArtifactStore) ServeUpload(http.ResponseWriter, *http.Request, string) error {
	return errors.New("serve upload failed")
}

func (s failingArtifactStore) AuditArchiveURI(digest string) (string, error) {
	return "memory-audit-archive://" + digest, nil
}

func (s failingArtifactStore) AuditArchiveStoragePolicy(time.Time) domain.AuditArchiveStoragePolicy {
	return domain.AuditArchiveStoragePolicy{}
}

func (s failingArtifactStore) SaveAuditArchive(string, []byte, domain.AuditArchiveStoragePolicy) (string, error) {
	return "", errors.New("save audit archive failed")
}

func (s failingArtifactStore) ReleaseEvidenceArchiveURI(digest string) (string, error) {
	return "memory-release-evidence://" + digest, nil
}

func (s failingArtifactStore) SaveReleaseEvidenceArchive(string, []byte, domain.AuditArchiveStoragePolicy) (string, error) {
	return "", errors.New("save release evidence archive failed")
}

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

func TestManifestRoutesValidateDryRunAndRestore(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodPut, "/api/products/lsap/manifest", `{"id":"lsap","name":"Broken"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid manifest status 400, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "MANIFEST_SCHEMA_INVALID" || len(apiErr.Error.Details) == 0 {
		t.Fatalf("expected structured manifest error, got %#v", apiErr)
	}

	updated := `{
	  "id":"lsap",
	  "name":"LSAP Updated",
	  "targets":["linux","windows"],
	  "targetConfig":{
	    "linux":{"installDir":"/opt/lsap","packageName":"lsap-linux"},
	    "windows":{"installDir":"C:\\\\LSAP","packageName":"lsap-windows"}
	  },
	  "profiles":["qa","prod"],
	  "versions":["1.0.0"],
	  "components":[{"id":"ui","type":"archive","required":true,"source":{"kind":"fixed-url","url":"https://example.test/ui.zip"}}],
	  "workloads":[{"id":"ui","runtime":"docker","component":"ui"}],
	  "config":{"modules":{"map":true}}
	}`
	response = doRequest(t, server, http.MethodPut, "/api/products/lsap/manifest", updated)
	if response.Code != http.StatusOK {
		t.Fatalf("expected valid manifest status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/dry-run", `{"target":"linux","profile":"qa","version":"1.0.0"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("expected dry run status 200, got %d: %s", response.Code, response.Body.String())
	}
	var dryRun domain.ManifestDryRunResult
	if err := json.NewDecoder(response.Body).Decode(&dryRun); err != nil {
		t.Fatal(err)
	}
	if !dryRun.Valid || dryRun.BuildLock.Target != "linux" || len(dryRun.Components) != 1 {
		t.Fatalf("unexpected dry run result: %#v", dryRun)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/lsap/manifest/revisions", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected revisions status 200, got %d: %s", response.Code, response.Body.String())
	}
	var revisions []domain.ManifestRevision
	if err := json.NewDecoder(response.Body).Decode(&revisions); err != nil {
		t.Fatal(err)
	}
	if len(revisions) == 0 || revisions[0].Manifest != nil {
		t.Fatalf("unexpected revisions: %#v", revisions)
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/revisions/"+revisions[0].ID+"/restore", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected restore status 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAPIUsesRichFixtureProductsOverlaysAndBuildData(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodGet, "/api/products", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected products status 200, got %d: %s", response.Code, response.Body.String())
	}
	var products []domain.Product
	if err := json.NewDecoder(response.Body).Decode(&products); err != nil {
		t.Fatal(err)
	}
	if len(products) != 2 {
		t.Fatalf("expected rich fixture to expose 2 products, got %#v", products)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/vision-edge/overlays/customers/city-bank", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected customer overlay status 200, got %d: %s", response.Code, response.Body.String())
	}
	var overlay storepkg.OverlayRecord
	if err := json.NewDecoder(response.Body).Decode(&overlay); err != nil {
		t.Fatal(err)
	}
	config := overlay.Data["config"].(map[string]any)
	customer := config["customer"].(map[string]any)
	if customer["name"] != "City Bank" {
		t.Fatalf("unexpected customer overlay: %#v", overlay)
	}

	dryRunBody := `{
	  "target":"windows",
	  "profile":"prod",
	  "customer":"city-bank",
	  "version":"2026.05.28",
	  "releaseChannel":"preview",
	  "configOverrides":{"ports":{"web":51000}},
	  "componentOverrides":{
	    "camera-model":{"kind":"fixed-url","url":"https://artifacts.example.test/camera-model-2026.05.28.zip"}
	  }
	}`
	response = doRequest(t, server, http.MethodPost, "/api/products/vision-edge/manifest/dry-run", dryRunBody)
	if response.Code != http.StatusOK {
		t.Fatalf("expected dry-run status 200, got %d: %s", response.Code, response.Body.String())
	}
	var dryRun domain.ManifestDryRunResult
	if err := json.NewDecoder(response.Body).Decode(&dryRun); err != nil {
		t.Fatal(err)
	}
	if !dryRun.Valid || dryRun.BuildLock.Target != "windows" || dryRun.BuildLock.Customer != "city-bank" {
		t.Fatalf("unexpected dry run lock: %#v", dryRun)
	}
	if len(dryRun.Components) != 3 {
		t.Fatalf("expected 3 resolved components, got %#v", dryRun.Components)
	}
	if !hasResolvedComponentURI(dryRun.Components, "camera-model", "https://artifacts.example.test/camera-model-2026.05.28.zip") {
		t.Fatalf("component override was not resolved: %#v", dryRun.Components)
	}

	now := time.Now().UTC()
	seedBuilds := []domain.Build{
		releaseReadyBuild("vision-release-ready", "vision-edge", "windows", "prod", "2026.05.28", now),
		{
			ID:             "vision-running",
			ProductID:      "vision-edge",
			Target:         "linux",
			Profile:        "qa",
			Version:        "latest",
			ReleaseChannel: "preview",
			Status:         domain.BuildRunning,
			RunnerID:       "linux-runner-01",
			CreatedAt:      now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-90 * time.Minute),
		},
	}
	for _, build := range seedBuilds {
		if err := store.SaveBuild(build); err != nil {
			t.Fatal(err)
		}
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected builds status 200, got %d: %s", response.Code, response.Body.String())
	}
	var builds []domain.Build
	if err := json.NewDecoder(response.Body).Decode(&builds); err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 {
		t.Fatalf("expected 2 seeded builds, got %#v", builds)
	}
	if !hasBuildStatus(builds, "vision-release-ready", domain.BuildPreviewValidated) {
		t.Fatalf("expected release-ready build in list, got %#v", builds)
	}
	if !hasBuildStatus(builds, "vision-running", domain.BuildRunning) {
		t.Fatalf("expected running build in list, got %#v", builds)
	}
	for _, build := range builds {
		if build.ReleaseCheckSummary == nil || build.ReleaseCheckSummary.BuildID != build.ID {
			t.Fatalf("expected release check summary for build %s, got %#v", build.ID, build.ReleaseCheckSummary)
		}
		if build.ID == "vision-release-ready" && !build.ReleaseCheckSummary.ReleaseReady {
			t.Fatalf("expected release-ready summary, got %#v", build.ReleaseCheckSummary)
		}
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/vision-release-ready/release-check", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected release check status 200, got %d: %s", response.Code, response.Body.String())
	}
	var check domain.ReleaseCheckResult
	if err := json.NewDecoder(response.Body).Decode(&check); err != nil {
		t.Fatal(err)
	}
	if !check.ReleaseReady || check.BlockingCount != 0 {
		t.Fatalf("expected seeded release build to be ready, got %#v", check)
	}
}

func TestAPIReadEndpointsExposeProductsBuildsLocksAndUploads(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := releaseReadyBuild("read-build", "lsap", "linux", "qa", "1.0.0", now)
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUploadRecord(domain.UploadRecord{
		ID:             "upload-read",
		FileName:       "read-package.tar.gz",
		URI:            "local-upload://upload-read/read-package.tar.gz",
		DownloadURL:    "http://api.test/api/uploads/upload-read/download",
		SHA256:         "sha256-read",
		Size:           128,
		DownloadPolicy: "",
		RetentionDays:  0,
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodGet, "/api/products/lsap", "")
	assertStatus(t, response, http.StatusOK)
	var product domain.Product
	decodeJSON(t, response, &product)
	if product.ID != "lsap" || len(product.Targets) != 2 {
		t.Fatalf("unexpected product response: %#v", product)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/lsap/manifest", "")
	assertStatus(t, response, http.StatusOK)
	var manifest map[string]any
	decodeJSON(t, response, &manifest)
	if manifest["id"] != "lsap" {
		t.Fatalf("unexpected manifest response: %#v", manifest)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/lsap/versions", "")
	assertStatus(t, response, http.StatusOK)
	var versionsResponse struct {
		Versions []string `json:"versions"`
	}
	decodeJSON(t, response, &versionsResponse)
	if len(versionsResponse.Versions) != 2 || versionsResponse.Versions[0] != "latest" {
		t.Fatalf("unexpected versions response: %#v", versionsResponse)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/read-build", "")
	assertStatus(t, response, http.StatusOK)
	var readBuild domain.Build
	decodeJSON(t, response, &readBuild)
	if readBuild.ID != "read-build" || readBuild.ReleaseCheckSummary == nil || !readBuild.ReleaseCheckSummary.ReleaseReady {
		t.Fatalf("unexpected build response: %#v", readBuild)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/read-build/lock", "")
	assertStatus(t, response, http.StatusOK)
	var lock domain.BuildLock
	decodeJSON(t, response, &lock)
	if lock.BuildID != "read-build" || lock.ProductID != "lsap" {
		t.Fatalf("unexpected lock response: %#v", lock)
	}

	response = doRequest(t, server, http.MethodGet, "/api/uploads", "")
	assertStatus(t, response, http.StatusOK)
	var uploads []domain.UploadRecord
	decodeJSON(t, response, &uploads)
	if len(uploads) != 1 || uploads[0].DownloadPolicy != "platform" || uploads[0].RetentionStatus != "permanent" {
		t.Fatalf("unexpected uploads response: %#v", uploads)
	}

	response = doRequest(t, server, http.MethodGet, "/api/products/missing", "")
	assertStatus(t, response, http.StatusNotFound)
}

func TestAPIDownloadCreateAndLogStreamBoundaries(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	artifactOnly := domain.Build{
		ID:          "artifact-only",
		ProductID:   "lsap",
		Target:      "linux",
		Profile:     "qa",
		Version:     "1.0.0",
		Status:      domain.BuildSucceeded,
		ArtifactURI: "minio://packages/artifact-only.tar.gz",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	noArtifact := domain.Build{
		ID:        "no-artifact",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildSucceeded,
		CreatedAt: now,
		UpdatedAt: now,
	}
	logged := domain.Build{
		ID:        "logged",
		ProductID: "lsap",
		Target:    "linux",
		Profile:   "qa",
		Version:   "1.0.0",
		Status:    domain.BuildRunning,
		Logs:      []domain.BuildLog{{Time: now, Source: "runner-1", Message: "first log"}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, build := range []domain.Build{artifactOnly, noArtifact, logged} {
		if err := store.SaveBuild(build); err != nil {
			t.Fatal(err)
		}
	}

	response := doRequest(t, server, http.MethodGet, "/api/builds/artifact-only/download", "")
	assertStatus(t, response, http.StatusOK)
	var artifact map[string]string
	decodeJSON(t, response, &artifact)
	if artifact["artifactUri"] != "minio://packages/artifact-only.tar.gz" {
		t.Fatalf("unexpected artifact download response: %#v", artifact)
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/no-artifact/download", "")
	assertStatus(t, response, http.StatusNotFound)

	response = doRequest(t, server, http.MethodPost, "/api/builds", `{`)
	assertStatus(t, response, http.StatusBadRequest)
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "VALIDATION_INVALID_JSON" {
		t.Fatalf("expected invalid json error, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds", `{"productId":"lsap"}`)
	assertStatus(t, response, http.StatusBadRequest)
	apiErr = decodeAPIError(t, response)
	if apiErr.Error.Code != "VALIDATION_REQUIRED_FIELD" || len(apiErr.Error.Details) != 2 {
		t.Fatalf("expected required field error, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/logged/logs", `{"message":"default source"}`)
	assertStatus(t, response, http.StatusOK)
	var logs []domain.BuildLog
	decodeJSON(t, response, &logs)
	if logs[len(logs)-1].Source != "runner" {
		t.Fatalf("expected default log source, got %#v", logs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/builds/logged/logs/stream?follow=true", nil).WithContext(ctx)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	assertStatus(t, response, http.StatusOK)
	if !strings.Contains(response.Body.String(), "event: log") {
		t.Fatalf("expected followed stream to emit existing log, got %s", response.Body.String())
	}

	response = doRequest(t, server, http.MethodGet, "/api/builds/missing/logs/stream", "")
	assertStatus(t, response, http.StatusOK)
	if !strings.Contains(response.Body.String(), "event: error") ||
		!strings.Contains(response.Body.String(), `"code":"RESOURCE_NOT_FOUND"`) ||
		!strings.Contains(response.Body.String(), `"requestId":"req_`) {
		t.Fatalf("expected stream error for missing build, got %s", response.Body.String())
	}
}

func TestAPIManifestDryRunAndMissingResourceBoundaries(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(t, server, http.MethodPut, "/api/products/lsap/manifest", `{`)
	assertStatus(t, response, http.StatusBadRequest)
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "VALIDATION_INVALID_JSON" {
		t.Fatalf("expected manifest invalid json error, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/dry-run", `{`)
	assertStatus(t, response, http.StatusBadRequest)
	apiErr = decodeAPIError(t, response)
	if apiErr.Error.Code != "VALIDATION_INVALID_JSON" {
		t.Fatalf("expected dry-run invalid json error, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/missing/manifest/dry-run", `{}`)
	assertStatus(t, response, http.StatusNotFound)

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/dry-run", `{"manifest":{"id":"broken"}}`)
	assertStatus(t, response, http.StatusOK)
	var dryRun domain.ManifestDryRunResult
	decodeJSON(t, response, &dryRun)
	if dryRun.Valid || len(dryRun.Errors) == 0 {
		t.Fatalf("expected invalid dry-run result, got %#v", dryRun)
	}

	manualManifest := `{
	  "id":"lsap",
	  "name":"LSAP",
	  "targets":["linux"],
	  "targetConfig":{"linux":{"installDir":"/opt/lsap","packageName":"lsap-linux"}},
	  "profiles":["qa"],
	  "versions":["1.0.0"],
	  "components":[{"id":"license","type":"file","required":true,"source":{"kind":"manual-upload","uploadId":"missing-upload"}}],
	  "config":{}
	}`
	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/dry-run", `{"manifest":`+manualManifest+`}`)
	assertStatus(t, response, http.StatusOK)
	decodeJSON(t, response, &dryRun)
	if dryRun.Valid || len(dryRun.Errors) == 0 {
		t.Fatalf("expected resolver dry-run error, got %#v", dryRun)
	}

	missingGets := []string{
		"/api/products/missing/manifest",
		"/api/products/missing/versions",
		"/api/products/lsap/overlays/customers/missing",
		"/api/builds/missing",
		"/api/builds/missing/lock",
		"/api/builds/missing/release-check",
		"/api/builds/missing/logs",
	}
	for _, path := range missingGets {
		response = doRequest(t, server, http.MethodGet, path, "")
		assertStatus(t, response, http.StatusNotFound)
	}

	response = doRequest(t, server, http.MethodPost, "/api/products/lsap/manifest/revisions/missing/restore", "")
	assertStatus(t, response, http.StatusNotFound)
}

func TestPreviewValidationRequiredBeforeRelease(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-preview",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "1.0.0",
		ReleaseChannel: "preview",
		Status:         domain.BuildSucceeded,
		ArtifactURI:    "local-upload://preview/lsap.tar.gz",
		DownloadURL:    "http://example.test/preview.tar.gz",
		BuildLock: domain.BuildLock{
			BuildID:        "build-preview",
			ProductID:      "lsap",
			Target:         "linux",
			Profile:        "qa",
			Version:        "1.0.0",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "1.0.0",
				URI:      "minio://artifact/Publish/ui/1.0.0/ui.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/build-preview/publish", `{}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected publish before validation status 409, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "BUILD_STATE_CONFLICT" {
		t.Fatalf("expected state conflict error, got %#v", apiErr)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/build-preview/validate-preview", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected validate preview status 200, got %d: %s", response.Code, response.Body.String())
	}
	var validated domain.Build
	if err := json.NewDecoder(response.Body).Decode(&validated); err != nil {
		t.Fatal(err)
	}
	if validated.Status != domain.BuildPreviewValidated || validated.ValidatedAt == nil || validated.ReleaseChannel != "preview" {
		t.Fatalf("unexpected validated build: %#v", validated)
	}

	response = doRequest(t, server, http.MethodPost, "/api/builds/build-preview/publish", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("expected publish after validation status 200, got %d: %s", response.Code, response.Body.String())
	}
	var published domain.Build
	if err := json.NewDecoder(response.Body).Decode(&published); err != nil {
		t.Fatal(err)
	}
	if published.Status != domain.BuildPublished || published.ReleaseChannel != "release" || published.PublishedAt == nil {
		t.Fatalf("unexpected published build: %#v", published)
	}
}

func TestReleaseCheckReportsPreviewAndReleaseBlockingChecks(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-check",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "latest",
		ReleaseChannel: "preview",
		Status:         domain.BuildSucceeded,
		ArtifactURI:    "local-upload://preview/lsap.tar.gz",
		DownloadURL:    "http://example.test/preview.tar.gz",
		BuildLock: domain.BuildLock{
			BuildID:        "build-check",
			ProductID:      "lsap",
			Target:         "linux",
			Profile:        "qa",
			Version:        "latest",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "latest",
				URI:      "minio://artifact/Publish/ui/latest/*.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodGet, "/api/builds/build-check/release-check", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected release check status 200, got %d: %s", response.Code, response.Body.String())
	}
	var check domain.ReleaseCheckResult
	if err := json.NewDecoder(response.Body).Decode(&check); err != nil {
		t.Fatal(err)
	}
	if !check.PreviewReady || check.ReleaseReady || check.BlockingCount == 0 {
		t.Fatalf("unexpected release check readiness: %#v", check)
	}
	if !hasReleaseCheck(check.Checks, "release.fixed-version", "blocked") {
		t.Fatalf("expected fixed version release blocker: %#v", check.Checks)
	}
	if !hasReleaseCheck(check.Checks, "release.exact-component-uris", "blocked") {
		t.Fatalf("expected exact uri release blocker: %#v", check.Checks)
	}
}

func TestMetricsEndpointExposesOperationalCounters(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	running := domain.Build{
		ID:             "metrics-running",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "latest",
		ReleaseChannel: "preview",
		Status:         domain.BuildRunning,
		RunnerID:       "linux-runner-01",
		ArtifactURI:    "",
		CreatedAt:      now.Add(-10 * time.Minute),
		UpdatedAt:      now,
		Logs: []domain.BuildLog{{
			Time:    now.Add(-30 * time.Second),
			Source:  "linux-runner-01",
			Message: "heartbeat: active",
		}, {
			Time:    now.Add(-25 * time.Second),
			Source:  "linux-runner-01",
			Message: "heartbeat: cache before: bun=1.0KB/3 files; dagger=missing; artifacts=unconfigured",
		}, {
			Time:    now.Add(-20 * time.Second),
			Source:  "linux-runner-01",
			Message: "heartbeat: cache after: bun=2.0KB/4+ files; dagger=512B/2 files error=scan timeout",
		}},
	}
	if err := store.SaveBuild(running); err != nil {
		t.Fatal(err)
	}
	ready := releaseReadyBuild("metrics-release-ready", "lsap", "windows", "qa", "1.0.0", now)
	if err := store.SaveBuild(ready); err != nil {
		t.Fatal(err)
	}
	expiredAt := now.Add(-time.Hour)
	if err := store.SaveUploadRecord(domain.UploadRecord{
		ID:              "metrics-expired",
		FileName:        "old.tar.gz",
		URI:             "local-upload://metrics-expired/old.tar.gz",
		SHA256:          "expired-sha",
		Size:            128,
		DownloadPolicy:  "restricted",
		RetentionDays:   1,
		ExpiresAt:       &expiredAt,
		RetentionStatus: "expired",
		CreatedAt:       now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUploadRecord(domain.UploadRecord{
		ID:              "metrics-active",
		FileName:        "new.tar.gz",
		URI:             "local-upload://metrics-active/new.tar.gz",
		SHA256:          "active-sha",
		Size:            256,
		DownloadPolicy:  "platform",
		RetentionDays:   30,
		RetentionStatus: "active",
		CreatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodGet, "/metrics", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	expected := []string{
		"# HELP packaging_builds_total",
		`packaging_builds_total{status="running",target="linux",release_channel="preview"} 1`,
		`packaging_builds_total{status="preview_validated",target="windows",release_channel="preview"} 1`,
		`packaging_uploads_total{retention_status="active",download_policy="platform"} 1`,
		`packaging_uploads_total{retention_status="expired",download_policy="restricted"} 1`,
		`packaging_release_ready_builds_total 1`,
		`packaging_runners_total{status="busy",target="linux"} 1`,
		`packaging_runner_running_builds{runner_id="linux-runner-01",status="busy",target="linux"} 1`,
		`packaging_runner_cache_entries{runner_id="linux-runner-01",target="linux",stage="after",cache="bun",status="ok"} 1`,
		`packaging_runner_cache_entries{runner_id="linux-runner-01",target="linux",stage="after",cache="dagger",status="error"} 1`,
		`packaging_runner_cache_entries{runner_id="linux-runner-01",target="linux",stage="before",cache="artifacts",status="unconfigured"} 1`,
		`packaging_runner_cache_entries{runner_id="linux-runner-01",target="linux",stage="before",cache="dagger",status="missing"} 1`,
		`packaging_runner_cache_bytes{runner_id="linux-runner-01",target="linux",stage="after",cache="bun"} 2048`,
		`packaging_runner_cache_files{runner_id="linux-runner-01",target="linux",stage="after",cache="bun"} 4`,
		`packaging_runner_cache_scan_truncated{runner_id="linux-runner-01",target="linux",stage="after",cache="bun"} 1`,
	}
	for _, text := range expected {
		if !strings.Contains(body, text) {
			t.Fatalf("expected metrics body to contain %q, got:\n%s", text, body)
		}
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("expected text/plain metrics, got %s", contentType)
	}
}

func TestParseRunnerCacheLogIgnoresNonCacheMessages(t *testing.T) {
	if metrics := parseRunnerCacheLog("heartbeat: active"); len(metrics) != 0 {
		t.Fatalf("expected non-cache heartbeat to be ignored, got %#v", metrics)
	}
}

func TestLatestRunnerCacheMetricsKeepsLatestPerRunnerStageAndCache(t *testing.T) {
	now := time.Now().UTC()
	builds := []domain.Build{{
		ID:       "cache-build",
		Target:   "linux",
		RunnerID: "runner-1",
		Logs: []domain.BuildLog{{
			Time:    now.Add(-time.Minute),
			Source:  "runner-1",
			Message: "heartbeat: cache after: bun=1.0KB/3 files",
		}, {
			Time:    now,
			Source:  "runner-1",
			Message: "heartbeat: cache after: bun=3.0KB/5+ files",
		}},
	}}

	metrics := latestRunnerCacheMetrics(builds)
	if len(metrics) != 1 {
		t.Fatalf("expected one latest cache metric, got %#v", metrics)
	}
	metric := metrics[0]
	if metric.RunnerID != "runner-1" || metric.Target != "linux" || metric.Stage != "after" || metric.CacheName != "bun" {
		t.Fatalf("unexpected cache metric identity: %#v", metric)
	}
	if metric.Bytes != 3072 || metric.Files != 5 || !metric.Truncated {
		t.Fatalf("unexpected latest cache values: %#v", metric)
	}
}

func TestMetricsEndpointRequiresViewerWhenAuthEnabled(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServerWithAuth(store, nil, nil, testAuthConfig()).Routes()

	response := doRequest(t, server, http.MethodGet, "/metrics", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing token status 401, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", apiErr)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/metrics", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected viewer token can read metrics, got %d: %s", response.Code, response.Body.String())
	}
}

func TestReproducibilityReportIncludesLockGateAndArtifactEvidence(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	uploadCreatedAt := now.Add(-30 * time.Minute)
	if err := store.SaveUploadRecord(domain.UploadRecord{
		ID:              "final-upload",
		FileName:        "setup.exe",
		URI:             "local-upload://final-upload/setup.exe",
		DownloadURL:     "http://api.test/api/uploads/final-upload/download",
		SHA256:          "final-sha256",
		Size:            4096,
		ContentType:     "application/octet-stream",
		DownloadPolicy:  "restricted",
		RetentionDays:   30,
		RetentionStatus: "active",
		CreatedAt:       uploadCreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	build := releaseReadyBuild("report-build", "lsap", "windows", "qa", "1.0.0", now)
	build.ArtifactURI = "local-upload://final-upload/setup.exe"
	build.DownloadURL = "http://api.test/api/uploads/final-upload/download"
	build.RunnerID = "windows-runner-01"
	build.NomadJobID = "job-report-build"
	build.BuildLock.Components[0].Source.Kind = "artifact-repository"
	build.BuildLock.Components[0].SHA256 = "component-sha256"
	build.Components = build.BuildLock.Components
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodGet, "/api/builds/report-build/reproducibility-report", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected report status 200, got %d: %s", response.Code, response.Body.String())
	}
	var report domain.ReproducibilityReport
	decodeJSON(t, response, &report)
	if !report.Reproducible || !report.ReleaseCheck.ReleaseReady || report.BuildLock.BuildID != "report-build" {
		t.Fatalf("unexpected report gate/lock: %#v", report)
	}
	if len(report.SourceArtifacts) != 2 || report.SourceArtifacts[0].SHA256 != "component-sha256" {
		t.Fatalf("expected source artifact evidence, got %#v", report.SourceArtifacts)
	}
	if report.OutputArtifact.UploadID != "final-upload" || report.OutputArtifact.SHA256 != "final-sha256" || report.OutputArtifact.DownloadPolicy != "restricted" {
		t.Fatalf("expected output artifact metadata, got %#v", report.OutputArtifact)
	}
	if report.Runner.RunnerID != "windows-runner-01" || report.Runner.NomadJobID != "job-report-build" {
		t.Fatalf("expected runner evidence, got %#v", report.Runner)
	}
	if report.Lifecycle.ValidatedAt == nil || report.Lifecycle.CreatedAt.IsZero() {
		t.Fatalf("expected lifecycle evidence, got %#v", report.Lifecycle)
	}
}

func TestReleaseEvidenceArchivePersistsDigestSignatureAndAudit(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions(store, &fakeScheduler{}, artifacts, ServerOptions{
		Auth:                  testAuthConfig(),
		AuditArchiveHMACKey:   "archive-secret",
		AuditArchiveHMACKeyID: "release-key-2026",
	}).Routes()
	now := time.Now().UTC()
	build := releaseReadyBuild("release-evidence-build", "lsap", "linux", "qa", "1.0.0", now)
	build.Status = domain.BuildPreviewValidated
	build.ArtifactURI = "local-upload://final-upload/package.tar.gz"
	build.DownloadURL = "http://api.test/api/uploads/final-upload/download"
	if err := store.SaveUploadRecord(domain.UploadRecord{
		ID:              "final-upload",
		FileName:        "package.tar.gz",
		URI:             build.ArtifactURI,
		DownloadURL:     build.DownloadURL,
		SHA256:          "final-sha256",
		Size:            1024,
		ContentType:     "application/gzip",
		DownloadPolicy:  "restricted",
		RetentionDays:   30,
		RetentionStatus: "active",
		CreatedAt:       now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodGet, "/api/builds/release-evidence-build/release-evidence/archive", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected release evidence archive to require auth, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/builds/release-evidence-build/release-evidence/archive", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected release evidence archive status 200, got %d: %s", response.Code, response.Body.String())
	}
	var archive domain.ReleaseEvidenceArchive
	decodeJSON(t, response, &archive)
	if archive.SchemaVersion != "packaging.release.evidence/v1" || archive.BuildID != build.ID || !archive.Reproducible {
		t.Fatalf("unexpected release evidence archive: %#v", archive)
	}
	if archive.SignatureAlgorithm != "hmac-sha256" || archive.SignatureKeyID != "release-key-2026" || archive.Signature == "" {
		t.Fatalf("expected signed release evidence archive, got %#v", archive)
	}
	expected, err := buildReleaseEvidenceArchive(archive.Report, "archive-secret", "release-key-2026", archive.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Digest != expected.Digest || archive.Signature != expected.Signature {
		t.Fatalf("release evidence digest/signature mismatch: got %#v expected %#v", archive, expected)
	}
	if archive.ArchiveURI != "local-release-evidence://sha256/"+archive.Digest+".json" || archive.ArchiveStoredAt == nil {
		t.Fatalf("expected persisted release evidence URI and timestamp, got %#v", archive)
	}
	archivePath := filepath.Join(dataDir, "release-evidence", "sha256", archive.Digest+".json")
	content, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("expected release evidence file at %s: %v", archivePath, err)
	}
	var stored domain.ReleaseEvidenceArchive
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Digest != archive.Digest || stored.ArchiveURI != archive.ArchiveURI {
		t.Fatalf("stored release evidence mismatch: got %#v want %#v", stored, archive)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?action=release.evidence.archive.export", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit event lookup status 200, got %d: %s", response.Code, response.Body.String())
	}
	var exportEvents []domain.AuditEvent
	decodeJSON(t, response, &exportEvents)
	if !hasAuditEvent(exportEvents, "release.evidence.archive.export", "viewer", archive.Digest) {
		t.Fatalf("expected release evidence export audit event, got %#v", exportEvents)
	}
}

func TestValidatePreviewRejectsMismatchedBuildLock(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-mismatch",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "1.0.0",
		ReleaseChannel: "preview",
		Status:         domain.BuildSucceeded,
		ArtifactURI:    "local-upload://preview/lsap.tar.gz",
		DownloadURL:    "http://example.test/preview.tar.gz",
		BuildLock: domain.BuildLock{
			BuildID:        "build-mismatch",
			ProductID:      "lsap",
			Target:         "windows",
			Profile:        "qa",
			Version:        "1.0.0",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "1.0.0",
				URI:      "minio://artifact/Publish/ui/1.0.0/ui.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/build-mismatch/validate-preview", "")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected mismatched lock status 422, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "BUILD_PREVIEW_NOT_READY" || !hasAPIErrorDetail(apiErr.Error.Details, "preview.lock-identity") {
		t.Fatalf("expected preview gate error details, got %#v", apiErr)
	}
}

func TestCreateBuildRejectsDirectReleaseChannel(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequest(
		t,
		server,
		http.MethodPost,
		"/api/builds",
		`{"productId":"lsap","target":"linux","version":"1.0.0","releaseChannel":"release"}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected direct release create status 400, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "BUILD_CREATE_INVALID_RELEASE_CHANNEL" {
		t.Fatalf("expected direct release channel error, got %#v", apiErr)
	}
}

func TestAPIErrorIncludesI18nKeysAndRequestContext(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()

	response := doRequestWithHeaders(
		t,
		server,
		http.MethodPost,
		"/api/builds",
		`{"productId":"lsap","target":"linux","version":"1.0.0","releaseChannel":"release"}`,
		map[string]string{
			"X-Request-Id":     "req-test-123",
			"X-Correlation-Id": "corr-test-123",
		},
	)
	assertStatus(t, response, http.StatusBadRequest)
	if response.Header().Get("X-Request-Id") != "req-test-123" || response.Header().Get("X-Correlation-Id") != "corr-test-123" {
		t.Fatalf("request context headers were not propagated: %#v", response.Header())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Status != http.StatusBadRequest ||
		apiErr.Error.MessageKey != "errors.build.invalidReleaseChannel" ||
		apiErr.Error.UserActionKey != "actions.createPreviewFirst" ||
		apiErr.Error.RequestID != "req-test-123" ||
		apiErr.Error.CorrelationID != "corr-test-123" {
		t.Fatalf("unexpected i18n error contract: %#v", apiErr)
	}
}

func TestWriteErrorMapsAppErrorKindAndDetails(t *testing.T) {
	response := httptest.NewRecorder()
	err := apperr.Validation("VALIDATION_INVALID_REQUEST", "target is not supported").
		WithField("target").
		WithParam("target", "darwin")

	writeError(response, http.StatusInternalServerError, err)

	assertStatus(t, response, http.StatusBadRequest)
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "VALIDATION_INVALID_REQUEST" ||
		apiErr.Error.Status != http.StatusBadRequest ||
		apiErr.Error.MessageKey != "errors.validation.invalidRequest" {
		t.Fatalf("unexpected mapped app error: %#v", apiErr)
	}
	if len(apiErr.Error.Details) != 1 ||
		apiErr.Error.Details[0].Field != "target" ||
		apiErr.Error.Details[0].Params["target"] != "darwin" {
		t.Fatalf("expected field and params to become error details, got %#v", apiErr.Error.Details)
	}
}

func TestPublishRejectsNonReproducibleRelease(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, nil, nil).Routes()
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "build-latest",
		ProductID:      "lsap",
		Target:         "windows",
		Profile:        "qa",
		Version:        "latest",
		ReleaseChannel: "preview",
		Status:         domain.BuildPreviewValidated,
		ArtifactURI:    "local-upload://preview/lsap.exe",
		DownloadURL:    "http://example.test/preview.exe",
		BuildLock: domain.BuildLock{
			BuildID:        "build-latest",
			ProductID:      "lsap",
			Target:         "windows",
			Profile:        "qa",
			Version:        "latest",
			ReleaseChannel: "preview",
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Version:  "latest",
				URI:      "minio://artifact/Publish/ui/latest/*.zip",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	response := doRequest(t, server, http.MethodPost, "/api/builds/build-latest/publish", `{}`)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected non-reproducible publish status 422, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "BUILD_RELEASE_GATE_BLOCKED" || !hasAPIErrorDetail(apiErr.Error.Details, "release.fixed-version") {
		t.Fatalf("expected release gate error details, got %#v", apiErr)
	}
}

func TestAPIHelperBoundaries(t *testing.T) {
	if defaultErrorCode(http.StatusUnauthorized) != "AUTH_REQUIRED" {
		t.Fatalf("unexpected unauthorized error code")
	}
	if defaultErrorCode(http.StatusForbidden) != "AUTH_FORBIDDEN" {
		t.Fatalf("unexpected forbidden error code")
	}
	if defaultErrorCode(http.StatusGone) != "ARTIFACT_EXPIRED" {
		t.Fatalf("unexpected gone error code")
	}
	if defaultErrorCode(http.StatusUnprocessableEntity) != "BUSINESS_RULE_FAILED" {
		t.Fatalf("unexpected unprocessable error code")
	}
	if defaultErrorCode(http.StatusServiceUnavailable) != "UPSTREAM_UNAVAILABLE" {
		t.Fatalf("unexpected upstream error code")
	}
	if defaultErrorCode(http.StatusInternalServerError) != "SYSTEM_INTERNAL_ERROR" {
		t.Fatalf("unexpected internal error code")
	}
	if defaultErrorCode(http.StatusTooManyRequests) != "REQUEST_FAILED" {
		t.Fatalf("unexpected generic error code")
	}

	now := time.Now().UTC()
	if runnerAvailability(domain.RunnerStatus{RunningBuilds: 1, LastHeartbeatAt: &now}, now) != "busy" {
		t.Fatalf("expected busy runner")
	}
	staleHeartbeat := now.Add(-10 * time.Minute)
	if runnerAvailability(domain.RunnerStatus{RunningBuilds: 1, LastHeartbeatAt: &staleHeartbeat}, now) != "stale" {
		t.Fatalf("expected stale runner")
	}
	if runnerAvailability(domain.RunnerStatus{RunningBuilds: 1}, now) != "unknown" {
		t.Fatalf("expected unknown runner")
	}
	if runnerAvailability(domain.RunnerStatus{LastLogAt: &now}, now) != "online" {
		t.Fatalf("expected online runner")
	}
	if runnerAvailability(domain.RunnerStatus{LastHeartbeatAt: &staleHeartbeat}, now) != "offline" {
		t.Fatalf("expected offline runner")
	}
	if runnerAvailability(domain.RunnerStatus{}, now) != "idle" {
		t.Fatalf("expected idle runner")
	}

	if normalizedDownloadPolicy("PUBLIC") != "public" || normalizedDownloadPolicy("bad") != "platform" {
		t.Fatalf("unexpected download policy normalization")
	}
	if parseRetentionDays("", -1) != 30 || parseRetentionDays("not-a-number", 7) != 30 || parseRetentionDays("99999", 7) != 3650 {
		t.Fatalf("unexpected retention day parsing")
	}
}

func TestAuthProtectsWriteAndRunnerRoutes(t *testing.T) {
	store, _ := newTestServerStore(t)
	auth := testAuthConfig()
	server := NewServerWithAuth(store, &fakeScheduler{}, nil, auth).Routes()

	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview"
	}`
	response := doRequest(t, server, http.MethodPost, "/api/builds", buildBody)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing token status 401, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "AUTH_REQUIRED" || apiErr.Error.Category != "security" {
		t.Fatalf("expected AUTH_REQUIRED response, got %#v", apiErr)
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, bearer("runner-token"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected runner token status 403, got %d: %s", response.Code, response.Body.String())
	}
	apiErr = decodeAPIError(t, response)
	if apiErr.Error.Code != "AUTH_FORBIDDEN" || apiErr.Error.Category != "security" {
		t.Fatalf("expected AUTH_FORBIDDEN response, got %#v", apiErr)
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, bearer("operator-token"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected operator can create build, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/missing/publish", "", bearer("operator-token"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected publish to require admin, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequest(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"runner-1","target":"linux"}`)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected runner claim without token status 401, got %d: %s", response.Code, response.Body.String())
	}
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"runner-1","target":"linux"}`, bearer("runner-token"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected auth-enabled target-wide claim rejection, got %d: %s", response.Code, response.Body.String())
	}
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"runner-1","target":"linux"}`, map[string]string{})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected auth-enabled legacy claim without capability or authorization rejection, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAuthEnforcesRestrictedUploadDownload(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedServer := NewServer(store, nil, artifacts).Routes()
	uploadResponse := uploadRequestWithFields(t, seedServer, "restricted.txt", "secret-data", map[string]string{
		"downloadPolicy": "restricted",
	})
	assertStatus(t, uploadResponse, http.StatusCreated)
	var upload domain.UploadRecord
	decodeJSON(t, uploadResponse, &upload)

	server := NewServerWithAuth(store, nil, artifacts, testAuthConfig()).Routes()
	response := doRequest(t, server, http.MethodGet, "/api/uploads/"+upload.ID+"/download", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected restricted download without token status 401, got %d: %s", response.Code, response.Body.String())
	}
	apiErr := decodeAPIError(t, response)
	if apiErr.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", apiErr)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/uploads/"+upload.ID+"/download", "", bearer("viewer-token"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected restricted download with viewer token status 403, got %d: %s", response.Code, response.Body.String())
	}
	apiErr = decodeAPIError(t, response)
	if apiErr.Error.Code != "AUTH_FORBIDDEN" {
		t.Fatalf("expected AUTH_FORBIDDEN, got %#v", apiErr)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/uploads/"+upload.ID+"/download", "", bearer("operator-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected operator can download restricted artifact, got %d: %s", response.Code, response.Body.String())
	}
	if strings.TrimSpace(response.Body.String()) != "secret-data" {
		t.Fatalf("unexpected restricted download body: %q", response.Body.String())
	}
}

func TestAuthEnforcesBuildDownloadLinkedUploadPolicyAndAudit(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	seedServer := NewServer(store, nil, artifacts).Routes()
	uploadResponse := uploadRequestWithFields(t, seedServer, "restricted-build.txt", "build-artifact", map[string]string{
		"downloadPolicy":     "restricted",
		"metadata.productId": "lsap",
	})
	assertStatus(t, uploadResponse, http.StatusCreated)
	var upload domain.UploadRecord
	decodeJSON(t, uploadResponse, &upload)
	now := time.Now().UTC()
	build := domain.Build{
		ID:          "restricted-build-download",
		ProductID:   "lsap",
		Target:      "linux",
		Profile:     "qa",
		Version:     "1.0.0",
		Status:      domain.BuildSucceeded,
		ArtifactURI: upload.URI,
		DownloadURL: upload.DownloadURL,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}

	server := NewServerWithAuth(store, nil, artifacts, testAuthConfig()).Routes()
	response := doRequestWithHeaders(t, server, http.MethodGet, "/api/builds/"+build.ID+"/download", "", bearer("viewer-token"))
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected viewer to be blocked by linked restricted upload policy, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/builds/"+build.ID+"/download", "", map[string]string{
		"Authorization":    "Bearer operator-token",
		"X-Request-Id":     "req-build-download",
		"X-Correlation-Id": "corr-build-download",
	})
	if response.Code != http.StatusFound {
		t.Fatalf("expected operator build download redirect, got %d: %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != upload.DownloadURL {
		t.Fatalf("unexpected build download redirect: %s", location)
	}

	auditResponse := doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?buildId="+build.ID, "", bearer("viewer-token"))
	assertStatus(t, auditResponse, http.StatusOK)
	var events []domain.AuditEvent
	decodeJSON(t, auditResponse, &events)
	if !hasAuditEvent(events, "build.download", "operator", build.ID) {
		t.Fatalf("expected build.download audit event, got %#v", events)
	}
	for _, event := range events {
		if event.Action == "build.download" {
			if event.UploadID != upload.ID || event.ProductID != "lsap" {
				t.Fatalf("expected linked upload/product metadata, got %#v", event)
			}
			if event.RequestID != "req-build-download" || event.CorrelationID != "corr-build-download" {
				t.Fatalf("expected request context on build download audit, got %#v", event)
			}
		}
	}
}

func TestAuditEventsRecordBuildActionsWithActorAndFilter(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServerWithAuth(store, &fakeScheduler{}, nil, testAuthConfig()).Routes()
	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview"
	}`
	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, map[string]string{
		"Authorization": "Bearer operator-token",
		"X-Request-Id":  "req-create-build",
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected create build status 202, got %d: %s", response.Code, response.Body.String())
	}
	var build domain.Build
	decodeJSON(t, response, &build)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/"+build.ID+"/approve", "", map[string]string{
		"Authorization": "Bearer operator-token",
		"X-Request-Id":  "req-approve-build",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?buildId="+build.ID, "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit list status 200, got %d: %s", response.Code, response.Body.String())
	}
	var events []domain.AuditEvent
	decodeJSON(t, response, &events)
	if !hasAuditEvent(events, "build.create", "operator", build.ID) {
		t.Fatalf("expected build.create audit event, got %#v", events)
	}
	if !hasAuditEvent(events, "build.approve", "operator", build.ID) {
		t.Fatalf("expected build.approve audit event, got %#v", events)
	}
	for _, event := range events {
		if event.ActorRole != "operator" || event.RequestID == "" || event.CorrelationID == "" {
			t.Fatalf("expected actor and request context in audit event, got %#v", event)
		}
	}

	response = doRequest(t, server, http.MethodGet, "/api/audit-events?buildId="+build.ID, "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected audit endpoint to require auth, got %d: %s", response.Code, response.Body.String())
	}
}

func TestBuildLogsPersistRequestContextAcrossBuildAndRunnerFlow(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServer(store, &fakeScheduler{}, nil).Routes()
	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview"
	}`

	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, map[string]string{
		"X-Request-Id":     "req-build-create",
		"X-Correlation-Id": "corr-build-flow",
	})
	assertStatus(t, response, http.StatusAccepted)
	var build domain.Build
	decodeJSON(t, response, &build)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/"+build.ID+"/approve", "", map[string]string{
		"X-Request-Id":     "req-build-approve",
		"X-Correlation-Id": "corr-build-flow",
	})
	assertStatus(t, response, http.StatusOK)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/claim", `{"runnerId":"runner-1","target":"linux"}`, map[string]string{
		"X-Request-Id":     "req-runner-claim",
		"X-Correlation-Id": "corr-build-flow",
	})
	assertStatus(t, response, http.StatusOK)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/"+build.ID+"/logs", `{"source":"runner-1","message":"package step started"}`, map[string]string{
		"X-Request-Id":     "req-runner-log",
		"X-Correlation-Id": "corr-build-flow",
	})
	assertStatus(t, response, http.StatusOK)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/"+build.ID+"/complete", `{"status":"succeeded","artifactUri":"minio://artifact/lsap.tar.gz"}`, map[string]string{
		"X-Request-Id":     "req-runner-complete",
		"X-Correlation-Id": "corr-build-flow",
	})
	assertStatus(t, response, http.StatusOK)

	response = doRequest(t, server, http.MethodGet, "/api/builds/"+build.ID+"/logs", "")
	assertStatus(t, response, http.StatusOK)
	var logs []domain.BuildLog
	decodeJSON(t, response, &logs)
	expected := map[string]string{
		"build request created and waiting for approval": "req-build-create",
		"final config and component lock generated":      "req-build-create",
		"approved and queued for runner":                 "req-build-approve",
		"claimed by runner runner-1":                     "req-runner-claim",
		"package step started":                           "req-runner-log",
		"build completed successfully":                   "req-runner-complete",
	}
	for message, requestID := range expected {
		log, ok := findBuildLog(logs, message)
		if !ok {
			t.Fatalf("expected build log %q in %#v", message, logs)
		}
		if log.RequestID != requestID || log.CorrelationID != "corr-build-flow" {
			t.Fatalf("expected request context for %q, got %#v", message, log)
		}
	}
}

func TestAuditEventsRecordSecretResolutionWithoutValues(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServerWithAuth(store, &fakeScheduler{}, nil, testAuthConfig()).Routes()
	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview",
	  "configOverrides":{
	    "database":{"rootPassword":"secret:db/root-password"}
	  },
	  "variables":{
	    "licenseKey":"secret://license/main"
	  }
	}`
	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, bearer("operator-token"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected create build status 202, got %d: %s", response.Code, response.Body.String())
	}
	var build domain.Build
	decodeJSON(t, response, &build)

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/"+build.ID+"/approve", "", bearer("operator-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?action=secret.resolve&buildId="+build.ID, "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit list status 200, got %d: %s", response.Code, response.Body.String())
	}
	var events []domain.AuditEvent
	decodeJSON(t, response, &events)
	if len(events) != 1 {
		t.Fatalf("expected one secret.resolve audit event, got %#v", events)
	}
	event := events[0]
	if event.ActorID != "operator" || event.Status != "success" || event.Metadata["stage"] != "approve" {
		t.Fatalf("unexpected secret audit event: %#v", event)
	}
	if event.Metadata["secretCount"] != "2" {
		t.Fatalf("expected secret count metadata, got %#v", event.Metadata)
	}
	if event.Metadata["secretRefs"] != "db/root-password,license/main" {
		t.Fatalf("expected stable secret refs metadata, got %#v", event.Metadata)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "PACKAGING_SECRET") {
		t.Fatalf("secret audit event leaked a secret value or env name: %s", string(encoded))
	}
}

func TestAuditArchiveExportsDigestAndSignature(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServerWithOptions(store, &fakeScheduler{}, nil, ServerOptions{
		Auth:                  testAuthConfig(),
		AuditArchiveHMACKey:   "archive-secret",
		AuditArchiveHMACKeyID: "audit-hmac-2026-06",
	}).Routes()
	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview"
	}`
	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, bearer("operator-token"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected create build status 202, got %d: %s", response.Code, response.Body.String())
	}
	var build domain.Build
	decodeJSON(t, response, &build)

	response = doRequest(t, server, http.MethodGet, "/api/audit-events/archive?buildId="+build.ID, "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected audit archive to require auth, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events/archive?buildId="+build.ID, "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit archive status 200, got %d: %s", response.Code, response.Body.String())
	}
	var archive domain.AuditArchive
	decodeJSON(t, response, &archive)
	if archive.SchemaVersion != "packaging.audit.archive/v1" {
		t.Fatalf("unexpected archive schema: %#v", archive)
	}
	if archive.EventCount != 1 || len(archive.Events) != 1 {
		t.Fatalf("expected one archived event, got %#v", archive)
	}
	if archive.Events[0].Action != "build.create" || archive.Filter.BuildID != build.ID {
		t.Fatalf("unexpected archived events/filter: %#v", archive)
	}
	if archive.DigestAlgorithm != "sha256" || archive.Digest == "" {
		t.Fatalf("expected sha256 digest: %#v", archive)
	}
	if archive.SignatureAlgorithm != "hmac-sha256" || archive.Signature == "" {
		t.Fatalf("expected hmac signature: %#v", archive)
	}
	if archive.SignatureKeyID != "audit-hmac-2026-06" {
		t.Fatalf("expected signature key id, got %#v", archive)
	}
	expected, err := buildAuditArchive(archive.Filter, archive.Events, "archive-secret", "audit-hmac-2026-06", archive.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Digest != expected.Digest || archive.Signature != expected.Signature {
		t.Fatalf("archive digest/signature mismatch: got %#v expected %#v", archive, expected)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?action=audit.archive.export", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit event lookup status 200, got %d: %s", response.Code, response.Body.String())
	}
	var exportEvents []domain.AuditEvent
	decodeJSON(t, response, &exportEvents)
	if !hasAuditEvent(exportEvents, "audit.archive.export", "viewer", archive.Digest) {
		t.Fatalf("expected archive export audit event, got %#v", exportEvents)
	}
}

func TestAuditArchivePersistsToArtifactStore(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions(store, &fakeScheduler{}, artifacts, ServerOptions{
		Auth:                testAuthConfig(),
		AuditArchiveHMACKey: "archive-secret",
	}).Routes()
	buildBody := `{
	  "productId":"lsap",
	  "target":"linux",
	  "profile":"qa",
	  "version":"1.0.0",
	  "releaseChannel":"preview"
	}`
	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/builds", buildBody, bearer("operator-token"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected create build status 202, got %d: %s", response.Code, response.Body.String())
	}
	var build domain.Build
	decodeJSON(t, response, &build)

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events/archive?buildId="+build.ID, "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit archive status 200, got %d: %s", response.Code, response.Body.String())
	}
	var archive domain.AuditArchive
	decodeJSON(t, response, &archive)
	if archive.ArchiveURI != "local-audit-archive://sha256/"+archive.Digest+".json" || archive.ArchiveStoredAt == nil {
		t.Fatalf("expected persisted archive URI and timestamp, got %#v", archive)
	}
	archivePath := filepath.Join(dataDir, "audit-archives", "sha256", archive.Digest+".json")
	content, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("expected archive file at %s: %v", archivePath, err)
	}
	var stored domain.AuditArchive
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Digest != archive.Digest || stored.ArchiveURI != archive.ArchiveURI {
		t.Fatalf("stored archive mismatch: got %#v want %#v", stored, archive)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?action=audit.archive.export", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit event lookup status 200, got %d: %s", response.Code, response.Body.String())
	}
	var events []domain.AuditEvent
	decodeJSON(t, response, &events)
	if len(events) != 1 || events[0].Metadata["archiveUri"] != archive.ArchiveURI {
		t.Fatalf("expected archive URI metadata on export audit event, got %#v", events)
	}
}

func TestAuditEventsRecordUploadCreateAndDownload(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{
		DataDir:       dataDir,
		APIBaseURL:    "http://api.test",
		ArtifactStore: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(store, nil, artifacts, testAuthConfig()).Routes()
	uploadResponse := uploadRequestWithFieldsAndHeaders(t, server, "license.lic", "license-data", map[string]string{
		"downloadPolicy":     "restricted",
		"metadata.productId": "lsap",
	}, map[string]string{
		"Authorization":    "Bearer operator-token",
		"X-Request-Id":     "req-upload-create",
		"X-Correlation-Id": "corr-upload-flow",
	})
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("expected upload status 201, got %d: %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var upload domain.UploadRecord
	decodeJSON(t, uploadResponse, &upload)
	if upload.RequestID != "req-upload-create" || upload.CorrelationID != "corr-upload-flow" {
		t.Fatalf("expected upload record request context, got %#v", upload)
	}

	response := doRequest(t, server, http.MethodGet, "/api/uploads", "")
	assertStatus(t, response, http.StatusOK)
	var uploads []domain.UploadRecord
	decodeJSON(t, response, &uploads)
	listedUpload, ok := findUploadRecord(uploads, upload.ID)
	if !ok || listedUpload.RequestID != "req-upload-create" || listedUpload.CorrelationID != "corr-upload-flow" {
		t.Fatalf("expected listed upload request context, got %#v", uploads)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/uploads/"+upload.ID+"/download", "", map[string]string{
		"Authorization":    "Bearer operator-token",
		"X-Request-Id":     "req-upload-download",
		"X-Correlation-Id": "corr-upload-flow",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("expected download status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/audit-events?uploadId="+upload.ID, "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected audit list status 200, got %d: %s", response.Code, response.Body.String())
	}
	var events []domain.AuditEvent
	decodeJSON(t, response, &events)
	if !hasAuditEvent(events, "upload.create", "operator", upload.ID) {
		t.Fatalf("expected upload.create audit event, got %#v", events)
	}
	if !hasAuditEvent(events, "upload.download", "operator", upload.ID) {
		t.Fatalf("expected upload.download audit event, got %#v", events)
	}
	for _, event := range events {
		if event.ProductID != "lsap" || event.UploadID != upload.ID {
			t.Fatalf("expected upload audit metadata, got %#v", event)
		}
		if event.CorrelationID != "corr-upload-flow" {
			t.Fatalf("expected upload audit correlation ID, got %#v", event)
		}
	}
}

func TestStorageUnavailableNotificationWhenUploadStoreFails(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServerWithAuth(store, nil, failingArtifactStore{saveUploadErr: errors.New("disk is full")}, testAuthConfig()).Routes()

	response := uploadRequestWithFieldsAndHeaders(t, server, "package.tar.gz", "payload", nil, bearer("operator-token"))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected upload failure status 500, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/notifications?type=storage.unavailable&read=unread", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected notification list status 200, got %d: %s", response.Code, response.Body.String())
	}
	var notifications []domain.Notification
	decodeJSON(t, response, &notifications)
	if len(notifications) != 1 {
		t.Fatalf("expected one storage notification, got %#v", notifications)
	}
	notification := notifications[0]
	if notification.Severity != "critical" || notification.ResourceType != "artifact_store" {
		t.Fatalf("unexpected storage notification: %#v", notification)
	}
	if notification.Metadata["operation"] != "upload" || !strings.Contains(notification.Metadata["error"], "disk is full") {
		t.Fatalf("unexpected storage notification metadata: %#v", notification.Metadata)
	}
}

func TestNotificationsRecordBuildLifecycleAndReadState(t *testing.T) {
	store, _ := newTestServerStore(t)
	now := time.Now().UTC()
	build := domain.Build{
		ID:             "notify-build",
		ProductID:      "lsap",
		Target:         "linux",
		Profile:        "qa",
		Version:        "1.0.0",
		ReleaseChannel: "preview",
		Status:         domain.BuildRunning,
		BuildLock: domain.BuildLock{
			BuildID:   "notify-build",
			ProductID: "lsap",
			Target:    "linux",
			Profile:   "qa",
			Version:   "1.0.0",
			Config:    map[string]any{"installDir": "/opt/lsap"},
			Components: []domain.ResolvedComponent{{
				ID:       "ui",
				Type:     "archive",
				Version:  "1.0.0",
				URI:      "https://example.test/ui.zip",
				SHA256:   "0123456789abcdef0123456789abcdef",
				Required: true,
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, &fakeScheduler{}, nil).Routes()

	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/notify-build/complete", `{"status":"succeeded","artifactUri":"minio://artifact/lsap.tar.gz","downloadUrl":"http://api.test/api/uploads/final/download"}`, bearer("runner-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected runner complete status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/notifications?read=unread&buildId=notify-build", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected notification list status 200, got %d: %s", response.Code, response.Body.String())
	}
	var notifications []domain.Notification
	decodeJSON(t, response, &notifications)
	if !hasNotification(notifications, "build.preview_ready", "info", "notify-build", false) {
		t.Fatalf("expected preview-ready unread notification, got %#v", notifications)
	}

	response = doRequest(t, server, http.MethodGet, "/api/notifications", "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected notifications endpoint local compatibility status 200, got %d: %s", response.Code, response.Body.String())
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/notifications/"+notifications[0].ID+"/read", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected mark read status 200, got %d: %s", response.Code, response.Body.String())
	}
	var readNotification domain.Notification
	decodeJSON(t, response, &readNotification)
	if readNotification.ReadAt == nil {
		t.Fatalf("expected notification readAt to be set, got %#v", readNotification)
	}

	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/notifications?read=unread&buildId=notify-build", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected unread list status 200, got %d: %s", response.Code, response.Body.String())
	}
	decodeJSON(t, response, &notifications)
	if len(notifications) != 0 {
		t.Fatalf("expected no unread notifications after mark read, got %#v", notifications)
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/notifications/read-all", "", bearer("viewer-token"))
	if response.Code != http.StatusOK {
		t.Fatalf("expected mark all read status 200, got %d: %s", response.Code, response.Body.String())
	}
}

func decodeAPIError(t *testing.T, response *httptest.ResponseRecorder) domain.APIErrorResponse {
	t.Helper()
	var apiErr domain.APIErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	return apiErr
}

func hasAPIErrorDetail(details []domain.APIErrorDetail, code string) bool {
	for _, detail := range details {
		if detail.Code == code {
			return true
		}
	}
	return false
}

func findBuildLog(logs []domain.BuildLog, message string) (domain.BuildLog, bool) {
	for _, log := range logs {
		if log.Message == message {
			return log, true
		}
	}
	return domain.BuildLog{}, false
}

func findUploadRecord(records []domain.UploadRecord, id string) (domain.UploadRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return domain.UploadRecord{}, false
}

func hasReleaseCheck(checks []domain.ReleaseCheckItem, id string, status string) bool {
	for _, check := range checks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func hasResolvedComponentURI(components []domain.ResolvedComponent, id string, uri string) bool {
	for _, component := range components {
		if component.ID == id && component.URI == uri {
			return true
		}
	}
	return false
}

func hasBuildStatus(builds []domain.Build, id string, status string) bool {
	for _, build := range builds {
		if build.ID == id && build.Status == status {
			return true
		}
	}
	return false
}

func releaseReadyBuild(id string, productID string, target string, profile string, version string, now time.Time) domain.Build {
	components := []domain.ResolvedComponent{
		{
			ID:       "vision-api",
			Type:     "docker-image",
			Version:  version,
			URI:      "minio://artifact/vision-api/" + version + "/vision-api.tar.gz",
			Required: true,
		},
		{
			ID:       "vision-ui",
			Type:     "archive",
			Version:  version,
			URI:      "https://artifacts.example.test/vision-ui-" + version + ".zip",
			Required: true,
		},
	}
	lock := domain.BuildLock{
		BuildID:        id,
		ProductID:      productID,
		Target:         target,
		Profile:        profile,
		Customer:       "city-bank",
		Version:        version,
		ReleaseChannel: "preview",
		Config: map[string]any{
			"installDir": "C:\\Program Files\\VisionEdge",
			"ports": map[string]any{
				"web": float64(51000),
			},
		},
		Components: components,
		Workloads: []domain.ResolvedWorkload{
			{ID: "vision-api", Runtime: "docker", Component: "vision-api", Enabled: true},
			{ID: "vision-worker", Runtime: "systemd", Component: "vision-api", Enabled: true},
		},
	}
	return domain.Build{
		ID:             id,
		ProductID:      productID,
		Target:         target,
		Profile:        profile,
		Customer:       "city-bank",
		Version:        version,
		ReleaseChannel: "preview",
		Status:         domain.BuildPreviewValidated,
		ArtifactURI:    "minio://artifact/packages/" + productID + "/" + version + "/" + target + "/setup.exe",
		DownloadURL:    "https://artifacts.example.test/packages/" + productID + "/" + version + "/" + target + "/setup.exe",
		FinalConfig:    lock.Config,
		Components:     components,
		Workloads:      lock.Workloads,
		BuildLock:      lock,
		CreatedAt:      now.Add(-3 * time.Hour),
		UpdatedAt:      now,
		ValidatedAt:    &now,
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
	if err := os.MkdirAll(filepath.Join(productDir, "customers"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "id":"lsap",
	  "name":"LSAP",
	  "targets":["linux","windows"],
	  "targetConfig":{
	    "linux":{"installDir":"/opt/lsap","packageName":"lsap-linux"},
	    "windows":{"installDir":"C:\\\\LSAP","packageName":"lsap-windows"}
	  },
	  "profiles":["qa","prod"],
	  "versions":["latest","1.0.0"],
	  "components":[{"id":"ui","type":"archive","required":true,"source":{"kind":"fixed-url","url":"https://example.test/ui.zip"}}],
	  "config":{"modules":{"map":true}}
	}`
	if err := os.WriteFile(filepath.Join(productDir, "product.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "profiles", "qa.json"), []byte(`{"config":{"ports":{"web":48001}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "customers", "acme.json"), []byte(`{"config":{"customer":{"name":"ACME"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	visionDir := filepath.Join(productsDir, "vision-edge")
	if err := os.MkdirAll(filepath.Join(visionDir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(visionDir, "customers"), 0o755); err != nil {
		t.Fatal(err)
	}
	visionManifest := `{
	  "id":"vision-edge",
	  "name":"Vision Edge",
	  "description":"Multi-target camera analytics installer fixture.",
	  "targets":["linux","windows"],
	  "targetConfig":{
	    "linux":{"installDir":"/opt/vision-edge","packageName":"vision-edge-linux"},
	    "windows":{"installDir":"C:\\\\Program Files\\\\VisionEdge","packageName":"vision-edge-setup"}
	  },
	  "profiles":["qa","prod","preview"],
	  "versions":["latest","2026.05.28","2026.06.01"],
	  "components":[
	    {
	      "id":"vision-api",
	      "type":"docker-image",
	      "required":true,
	      "source":{"kind":"artifact-repository","repository":"minio","path":"artifact/vision-api/{{version}}/vision-api-*.tar.gz"}
	    },
	    {
	      "id":"vision-ui",
	      "type":"archive",
	      "required":true,
	      "source":{"kind":"fixed-url","url":"https://artifacts.example.test/vision-ui-{{version}}.zip"}
	    },
	    {
	      "id":"camera-model",
	      "type":"model",
	      "required":false,
	      "source":{"kind":"manual-upload","accept":[".zip",".onnx"]}
	    }
	  ],
	  "workloads":[
	    {"id":"vision-api","runtime":"docker","component":"vision-api","module":"camera","docker":{"service":"vision-api"}},
	    {"id":"vision-worker","runtime":"systemd","component":"vision-api","module":"push","systemd":{"unitName":"vision-worker.service","execStart":"/opt/vision-edge/bin/worker"}}
	  ],
	  "config":{
	    "ports":{"web":50080,"grpc":50081},
	    "modules":{"camera":true,"push":true,"map":false},
	    "customer":{"name":"Default Vision Customer","license":"enterprise"},
	    "database":{"passwordPolicy":"strong"}
	  }
	}`
	if err := os.WriteFile(filepath.Join(visionDir, "product.json"), []byte(visionManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visionDir, "profiles", "prod.json"), []byte(`{"config":{"ports":{"web":50090},"modules":{"map":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(visionDir, "customers", "city-bank.json"), []byte(`{"config":{"customer":{"name":"City Bank","license":"platinum"},"ports":{"web":50100}}}`), 0o644); err != nil {
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
	return doRequestWithHeaders(t, handler, method, path, body, nil)
}

func doRequestWithHeaders(t *testing.T, handler http.Handler, method string, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func testAuthConfig() AuthConfig {
	return AuthConfig{
		Enabled:       true,
		AdminToken:    "admin-token",
		OperatorToken: "operator-token",
		ViewerToken:   "viewer-token",
		RunnerToken:   "runner-token",
	}
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func hasAuditEvent(events []domain.AuditEvent, action string, actorID string, resourceID string) bool {
	for _, event := range events {
		if event.Action == action && event.ActorID == actorID && event.ResourceID == resourceID {
			return true
		}
	}
	return false
}

func hasNotification(notifications []domain.Notification, notificationType string, severity string, buildID string, read bool) bool {
	for _, notification := range notifications {
		if notification.Type == notificationType &&
			notification.Severity == severity &&
			notification.BuildID == buildID &&
			(notification.ReadAt != nil) == read {
			return true
		}
	}
	return false
}
