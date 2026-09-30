package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vela/conductor/internal/artifact"
	"vela/conductor/internal/config"
	"vela/conductor/internal/domain"
)

func TestRunnerOutputBindsAndVerifiesCurrentAttempt(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{DataDir: dataDir, APIBaseURL: "http://api.test", ArtifactStore: "local"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	build := domain.Build{
		ID: "output-build", ProductID: "lsap", Target: "linux", Profile: "qa", Version: "1.0.0", ReleaseChannel: "preview", Status: domain.BuildQueued,
		Attempt: 1, RunnerID: "nomad-output-build-a1", ExecutionJTI: "output-jti", ExecutionExpiresAt: &expires,
		BuildLock: domain.BuildLock{BuildID: "output-build", ProductID: "lsap", Target: "linux", Profile: "qa", Version: "1.0.0", ReleaseChannel: "preview"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(store, &fakeScheduler{}, artifacts, testAuthConfig()).Routes()
	headers := runnerExecutionHeaders(t, build, now)

	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/output-build/claim", `{}`, headers)
	assertStatus(t, response, http.StatusOK)

	finalPayload := []byte("trusted final artifact\n")
	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-build/artifacts/final", "package.tar.gz", finalPayload, headers)
	assertStatus(t, response, http.StatusCreated)
	var final domain.UploadRecord
	decodeJSON(t, response, &final)
	if final.BuildID != build.ID || final.Attempt != 1 || final.RunnerID != build.RunnerID || final.Kind != "final" {
		t.Fatalf("unexpected final ownership: %#v", final)
	}

	digest := sha256.Sum256(finalPayload)
	checksumPayload := []byte(hex.EncodeToString(digest[:]) + "  package.tar.gz\n")
	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-build/artifacts/checksum", "package.tar.gz.sha256", checksumPayload, headers)
	assertStatus(t, response, http.StatusCreated)
	var checksum domain.UploadRecord
	decodeJSON(t, response, &checksum)
	if checksum.ParentUploadID != final.ID || checksum.Kind != "checksum" || checksum.RunnerID != build.RunnerID {
		t.Fatalf("unexpected checksum ownership: %#v", checksum)
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/output-build/complete", `{"status":"succeeded","artifactUri":"https://attacker.test/override","downloadUrl":"https://attacker.test/download"}`, headers)
	assertStatus(t, response, http.StatusOK)
	var completed domain.Build
	decodeJSON(t, response, &completed)
	if completed.Status != domain.BuildSucceeded || completed.ArtifactUploadID != final.ID || completed.ChecksumUploadID != checksum.ID || completed.ArtifactSHA256 != hex.EncodeToString(digest[:]) || completed.ArtifactVerifiedAt == nil {
		t.Fatalf("unexpected verified completion: %#v", completed)
	}
	if completed.ArtifactURI != final.URI || completed.DownloadURL != final.DownloadURL || completed.ArtifactURI == "https://attacker.test/override" {
		t.Fatalf("completion trusted runner URI instead of server record: %#v", completed)
	}

	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/output-build/validate-preview", `{}`, bearer("operator-token"))
	assertStatus(t, response, http.StatusOK)
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/builds/output-build/publish", `{"artifactUri":"https://attacker.test/override"}`, bearer("admin-token"))
	assertStatus(t, response, http.StatusBadRequest)
}

func TestRunnerOutputRejectsUntrustedAndTamperedArtifacts(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{DataDir: dataDir, APIBaseURL: "http://api.test", ArtifactStore: "local"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	build := domain.Build{
		ID: "output-negative", ProductID: "lsap", Target: "linux", Status: domain.BuildQueued,
		Attempt: 1, RunnerID: "nomad-output-negative-a1", ExecutionJTI: "negative-jti", ExecutionExpiresAt: &expires,
		BuildLock: domain.BuildLock{BuildID: "output-negative", ProductID: "lsap", Target: "linux", Version: "1.0.0"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(store, &fakeScheduler{}, artifacts, testAuthConfig()).Routes()
	headers := runnerExecutionHeaders(t, build, now)

	response := doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/output-negative/claim", `{}`, headers)
	assertStatus(t, response, http.StatusOK)
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/output-negative/complete", `{"status":"succeeded"}`, headers)
	assertStatus(t, response, http.StatusConflict)

	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-negative/artifacts/checksum", "package.tar.gz.sha256", []byte("bad\n"), headers)
	assertStatus(t, response, http.StatusConflict)

	finalPayload := []byte("original output")
	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-negative/artifacts/final", "package.tar.gz", finalPayload, headers)
	assertStatus(t, response, http.StatusCreated)
	var final domain.UploadRecord
	decodeJSON(t, response, &final)

	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-negative/artifacts/final", "second.tar.gz", []byte("second"), headers)
	assertStatus(t, response, http.StatusConflict)

	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-negative/artifacts/checksum", "package.tar.gz.sha256", []byte("0"+string(make([]byte, 63))+"  package.tar.gz\n"), headers)
	assertStatus(t, response, http.StatusBadRequest)

	digest := sha256.Sum256(finalPayload)
	response = runnerMultipartRequest(t, server, "/api/runner/tasks/output-negative/artifacts/checksum", "package.tar.gz.sha256", []byte(hex.EncodeToString(digest[:])+"  package.tar.gz\n"), headers)
	assertStatus(t, response, http.StatusCreated)

	filePath := filepath.Join(dataDir, "uploads", final.ID, "package.tar.gz")
	if err := os.WriteFile(filePath, []byte("tampered output"), 0o644); err != nil {
		t.Fatal(err)
	}
	response = doRequestWithHeaders(t, server, http.MethodPost, "/api/runner/tasks/output-negative/complete", `{"status":"succeeded"}`, headers)
	assertStatus(t, response, http.StatusConflict)
	updated, err := store.ReadBuild(build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != domain.BuildRunning {
		t.Fatalf("tampered artifact must not complete build: %#v", updated)
	}
}

func TestLegacyOutputRemainsDownloadableButIsUnverified(t *testing.T) {
	store, _ := newTestServerStore(t)
	dataDir := t.TempDir()
	artifacts, err := artifact.NewStore(config.Config{DataDir: dataDir, APIBaseURL: "http://api.test", ArtifactStore: "local"})
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload := []byte("legacy package")
	legacy := saveTestUpload(t, artifacts, store, "legacy.tar.gz", legacyPayload)
	now := time.Now().UTC()
	build := domain.Build{
		ID: "legacy-output", ProductID: "lsap", Target: "linux", Profile: "qa", Version: "1.0.0", ReleaseChannel: "preview", Status: domain.BuildSucceeded,
		ArtifactURI: legacy.URI, DownloadURL: legacy.DownloadURL,
		BuildLock: domain.BuildLock{BuildID: "legacy-output", ProductID: "lsap", Target: "linux", Profile: "qa", Version: "1.0.0", ReleaseChannel: "preview"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveBuild(build); err != nil {
		t.Fatal(err)
	}
	server := NewServer(store, nil, artifacts).Routes()
	response := doRequest(t, server, http.MethodGet, "/api/builds/legacy-output/download", "")
	assertStatus(t, response, http.StatusFound)
	response = doRequest(t, server, http.MethodGet, "/api/builds/legacy-output/reproducibility-report", "")
	assertStatus(t, response, http.StatusOK)
	var report domain.ReproducibilityReport
	decodeJSON(t, response, &report)
	if report.OutputArtifact.VerificationStatus != "legacy-unverified" || report.Reproducible {
		t.Fatalf("unexpected legacy report: %#v", report)
	}
	response = doRequest(t, server, http.MethodPost, "/api/builds/legacy-output/validate-preview", `{}`)
	assertStatus(t, response, http.StatusUnprocessableEntity)
}

func TestUploadListRequiresViewerWhenAuthEnabled(t *testing.T) {
	store, _ := newTestServerStore(t)
	server := NewServerWithAuth(store, nil, nil, testAuthConfig()).Routes()
	response := doRequest(t, server, http.MethodGet, "/api/uploads", "")
	assertStatus(t, response, http.StatusUnauthorized)
	response = doRequestWithHeaders(t, server, http.MethodGet, "/api/uploads", "", bearer("viewer-token"))
	assertStatus(t, response, http.StatusOK)
}

func runnerExecutionHeaders(t *testing.T, build domain.Build, now time.Time) map[string]string {
	t.Helper()
	capability, err := issueExecutionCapability("test-runner-execution-key", domain.ExecutionClaims{
		BuildID: build.ID, Attempt: build.Attempt, Target: build.Target, RunnerID: build.RunnerID,
		JTI: build.ExecutionJTI, IssuedAt: now.Add(-time.Minute), ExpiresAt: *build.ExecutionExpiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	headers := bearer("runner-token")
	headers["X-Runner-Execution"] = capability
	return headers
}

func runnerMultipartRequest(t *testing.T, handler http.Handler, target string, filename string, payload []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func saveTestUpload(t *testing.T, artifacts *artifact.Store, store interface {
	SaveUploadRecord(domain.UploadRecord) error
}, filename string, payload []byte) domain.UploadRecord {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	record, err := artifacts.SaveUpload(file, header)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUploadRecord(record); err != nil {
		t.Fatal(err)
	}
	return record
}
